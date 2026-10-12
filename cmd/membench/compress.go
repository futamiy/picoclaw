package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/sipeed/picoclaw/pkg/seahorse"
)

// CompressCycleResult is one row of the compression scorecard. Field names
// match the hermes-agent evals/compaction scorecard.json schema
// (policy, before_tokens, after_tokens, recall_pct, compress_seconds) so the
// same report.py can render either harness's output.
type CompressCycleResult struct {
	Policy          string  `json:"policy"`
	RecallPct       float64 `json:"recall_pct"`
	BeforeTokens    int     `json:"before_tokens"`
	AfterTokens     int     `json:"after_tokens"`
	KeptPct         float64 `json:"kept_pct"`
	FreedPct        float64 `json:"freed_pct"`
	Slight          bool    `json:"slight"` // freed < SlightFreedThresholdPct
	CompressSeconds float64 `json:"compress_seconds"`
	Questions       int     `json:"questions"`
}

// SlightFreedThresholdPct is the upper bound on freed tokens for a compaction
// cycle to count as "slight" (matching hermes-agent's <28% definition).
const SlightFreedThresholdPct = 28.0

// compressPolicy describes one compaction strategy under test.
type compressPolicy struct {
	name string
	// run executes a single compaction cycle and returns tokens after.
	run func(ctx context.Context, engine *seahorse.Engine, sessionKey string, budget int) (int, error)
}

// compressPolicies is the policy matrix evaluated by the compress-cycle mode.
func compressPolicies() []compressPolicy {
	return []compressPolicy{
		{
			name: "leaf",
			run: func(ctx context.Context, engine *seahorse.Engine, sessionKey string, _ int) (int, error) {
				if _, err := engine.Compact(ctx, sessionKey, seahorse.CompactInput{}); err != nil {
					return 0, err
				}
				return contextTokens(ctx, engine, sessionKey)
			},
		},
		{
			name: "until_under",
			run: func(ctx context.Context, engine *seahorse.Engine, sessionKey string, budget int) (int, error) {
				if _, err := engine.CompactUntilUnder(ctx, sessionKey, budget); err != nil {
					return 0, err
				}
				return contextTokens(ctx, engine, sessionKey)
			},
		},
	}
}

// contextTokens returns the current context token count for a session.
func contextTokens(ctx context.Context, engine *seahorse.Engine, sessionKey string) (int, error) {
	store := engine.GetRetrieval().Store()
	conv, err := store.GetConversationBySessionKey(ctx, sessionKey)
	if err != nil {
		return 0, err
	}
	if conv == nil {
		return 0, fmt.Errorf("conversation not found for %s", sessionKey)
	}
	return store.GetContextTokenCount(ctx, conv.ConversationID)
}

// RunCompressCycles ingests each sample, runs every policy over a fresh engine,
// and returns one scorecard row per (sample, policy).
//
// The seahorse compaction engine is driven with a nil completions function,
// which makes it fall back to deterministic truncation — so this mode runs
// fully offline while still exercising the real compaction pipeline.
func RunCompressCycles(
	ctx context.Context,
	samples []LocomoSample,
	outDir string,
	budgetTokens int,
) ([]CompressCycleResult, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("create out dir: %w", err)
	}

	noopFn := func(ctx context.Context, prompt string, opts seahorse.CompleteOptions) (string, error) {
		return "", nil
	}

	var rows []CompressCycleResult
	for si := range samples {
		sample := &samples[si]
		for _, pol := range compressPolicies() {
			row, err := runOneCycle(ctx, sample, pol, outDir, budgetTokens, noopFn)
			if err != nil {
				return nil, fmt.Errorf("sample %s policy %s: %w", sample.SampleID, pol.name, err)
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// runOneCycle ingests one sample into a fresh seahorse engine and runs a single
// compaction cycle with the given policy.
func runOneCycle(
	ctx context.Context,
	sample *LocomoSample,
	pol compressPolicy,
	outDir string,
	budgetTokens int,
	complete seahorse.CompleteFn,
) (CompressCycleResult, error) {
	dbPath := filepath.Join(outDir, fmt.Sprintf("compress_%s_%s.db", pol.name, sample.SampleID))
	engine, err := seahorse.NewEngine(seahorse.Config{DBPath: dbPath}, complete)
	if err != nil {
		return CompressCycleResult{}, fmt.Errorf("create engine: %w", err)
	}
	defer engine.Close()

	sessionKey := "compress-" + sample.SampleID
	turns := GetTurns(sample)
	msgs := make([]seahorse.Message, 0, len(turns))
	for _, turn := range turns {
		msgs = append(msgs, seahorse.Message{
			Role:       "user",
			Content:    turn.Speaker + ": " + turn.Text,
			TokenCount: len(turn.Text) / 4,
		})
	}
	if _, err := engine.Ingest(ctx, sessionKey, msgs); err != nil {
		return CompressCycleResult{}, fmt.Errorf("ingest: %w", err)
	}

	before, err := contextTokens(ctx, engine, sessionKey)
	if err != nil {
		return CompressCycleResult{}, err
	}

	start := time.Now()
	after, err := pol.run(ctx, engine, sessionKey, budgetTokens)
	if err != nil {
		return CompressCycleResult{}, err
	}
	elapsed := time.Since(start).Seconds()

	// Recall: assemble the post-compaction context and check evidence coverage.
	// Use a generous budget so assembly does not truncate what compaction
	// retained — we want to measure what the compaction *kept*, not what the
	// assembler's budget allows.
	assembleBudget := after * 2
	if assembleBudget < before*2 {
		assembleBudget = before * 2
	}
	if assembleBudget < 4096 {
		assembleBudget = 4096
	}
	assembled, err := engine.Assemble(ctx, sessionKey, seahorse.AssembleInput{Budget: assembleBudget})
	contextText := ""
	if err == nil && assembled != nil {
		var parts []string
		if assembled.Summary != "" {
			parts = append(parts, assembled.Summary)
		}
		for _, m := range assembled.Messages {
			parts = append(parts, m.Content)
		}
		contextText = StringListToContent(parts)
	}
	if err != nil {
		log.Printf("WARN: assemble for %s: %v", sample.SampleID, err)
	}

	recall := averageEvidenceCoverage(sample, contextText)

	kept := 100.0
	if before > 0 {
		kept = 100 * float64(after) / float64(before)
	}
	freed := 100 - kept
	if before == 0 {
		freed = 0
	}

	return CompressCycleResult{
		Policy:       fmt.Sprintf("%s/%s", pol.name, sample.SampleID),
		RecallPct:    recall,
		BeforeTokens: before,
		AfterTokens:  after,
		KeptPct:      kept,
		FreedPct:     freed,
		Slight:       freed < SlightFreedThresholdPct,
		CompressSeconds: elapsed,
		Questions:    len(sample.QA),
	}, nil
}

// averageEvidenceCoverage returns the mean RecallHitRate across a sample's QA
// bank, as a percentage.
func averageEvidenceCoverage(sample *LocomoSample, contextText string) float64 {
	if len(sample.QA) == 0 {
		return 0
	}
	sum := 0.0
	for qi := range sample.QA {
		sum += RecallHitRate(sample.QA[qi].Evidence, sample, contextText)
	}
	return 100 * sum / float64(len(sample.QA))
}

// SaveCompressResults writes the compress scorecard as JSON. Two files are
// emitted:
//   - compress_scorecard.json (this harness's native name)
//   - scorecard.json (hermes-agent evals/compaction schema, so report.py can
//     render it directly with zero glue)
func SaveCompressResults(rows []CompressCycleResult, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}
	if err := writeJSONFile(filepath.Join(outDir, "compress_scorecard.json"), rows); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(outDir, "scorecard.json"), rows)
}
