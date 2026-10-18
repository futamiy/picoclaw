package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/honcho"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/seahorse"
)

// ChatVariantResult is one row of the chat-mode A/B scorecard. The field names
// match the hermes-agent evals/compaction scorecard.json schema
// (policy, before_tokens, after_tokens, recall_pct, compress_seconds) so the
// same report.py can render it directly.
//
// In chat mode "policy" is the variant label (e.g. "modified" / "original"),
// "before"/"after" are the prompt token counts on the first vs. the final chat
// turn, and "recall_pct" is the mean evidence coverage of the assembled chat
// prompt across the sample's QA bank.
type ChatVariantResult struct {
	Policy          string  `json:"policy"`
	RecallPct       float64 `json:"recall_pct"`
	BeforeTokens    int     `json:"before_tokens"`
	AfterTokens     int     `json:"after_tokens"`
	KeptPct         float64 `json:"kept_pct"`
	FreedPct        float64 `json:"freed_pct"`
	Slight          bool    `json:"slight"`
	CompressSeconds float64 `json:"compress_seconds"`
	Questions       int     `json:"questions"`

	// Chat-mode extras (not in the hermes schema; harmless additions).
	Turns     int  `json:"turns"`
	AutoComp  bool `json:"auto_compress"`
	HonchoRec bool `json:"honcho_recall"`
}

// chatAxis describes one config combination under test: whether transparent
// auto-compression and Honcho-backed recall are enabled. Enumerating the full
// cross-product lets the scorecard attribute the delta to each axis.
type chatAxis struct {
	name      string
	autoComp  bool
	honchoRec bool
}

// chatAxes returns the A/B matrix: the fully-modified configuration (both axes
// ON), the original configuration (both OFF), and the two single-axis
// intermediate points so each axis can be isolated.
func chatAxes() []chatAxis {
	return []chatAxis{
		{name: "original", autoComp: false, honchoRec: false},
		{name: "compress-only", autoComp: true, honchoRec: false},
		{name: "honcho-only", autoComp: false, honchoRec: true},
		{name: "modified", autoComp: true, honchoRec: true},
	}
}

// RunChatAB replays each sample as a chat conversation through picoclaw's real
// context-assembly layer, once per config combination, and returns one
// scorecard row per (axis, sample).
//
// "Replaying as chat turns" means: every conversation turn is fed, in order,
// into a fresh seahorse engine (the same short-term memory the live agent
// uses). For the modified path the engine is proactively compacted
// (auto-compress) and a Honcho recall block is prefetched and injected; for the
// original path neither happens. The final turn's prompt is assembled with the
// real agent.ContextBuilder so the exact system/summary/honcho/message
// composition the runtime emits is what gets measured.
//
// The whole mode runs offline: seahorse is driven with a nil completion
// function (deterministic truncation instead of an LLM summary) and Honcho is
// read through the same client the runtime uses, pointed at either
// MEMBENCH_HONCHO_BASE or the in-process mock.
func RunChatAB(
	ctx context.Context,
	samples []LocomoSample,
	outDir string,
	budgetTokens int,
) ([]ChatVariantResult, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("create out dir: %w", err)
	}

	// One Honcho client (live or mock) shared by every honcho-enabled axis.
	client, _, srv, err := envOrMockHoncho()
	if err != nil {
		return nil, err
	}
	if srv != nil {
		defer srv.Close()
	}

	// Ingest every sample into Honcho once; the honcho-recalled axes read back
	// the same sessions via the session-context endpoint.
	sessionMap, err := ingestHonchoSessions(ctx, client, samples)
	if err != nil {
		return nil, err
	}

	workspace := filepath.Join(outDir, "chat-workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return nil, fmt.Errorf("create chat workspace: %w", err)
	}

	var rows []ChatVariantResult
	for _, axis := range chatAxes() {
		for si := range samples {
			row, err := runChatVariant(ctx, &samples[si], axis, client, sessionMap, workspace, budgetTokens)
			if err != nil {
				return nil, fmt.Errorf("axis %s sample %s: %w", axis.name, samples[si].SampleID, err)
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// ingestHonchoSessions pushes each sample's turns into a Honcho session and
// returns sampleID -> sessionID. Mirrors the runtime transcript sync but uses
// the plain user peer for every turn (the bench does not model peer identity).
func ingestHonchoSessions(
	ctx context.Context,
	client *honcho.Client,
	samples []LocomoSample,
) (map[string]string, error) {
	sessionMap := make(map[string]string, len(samples))
	for si := range samples {
		sample := &samples[si]
		sessionID := honcho.SanitizeID("chat-" + sample.SampleID)
		sessionMap[sample.SampleID] = sessionID
		if err := client.Ensure(ctx, honchoWorkspace, sessionID, "user", "agent"); err != nil {
			return nil, fmt.Errorf("ensure %s: %w", sample.SampleID, err)
		}
		turns := GetTurns(sample)
		msgs := make([]honcho.Message, 0, len(turns))
		for _, turn := range turns {
			msgs = append(msgs, honcho.Message{
				Content: turn.Speaker + ": " + turn.Text,
				PeerID:  "user",
			})
		}
		if err := client.AddMessages(ctx, honchoWorkspace, sessionID, msgs); err != nil {
			return nil, fmt.Errorf("add messages %s: %w", sample.SampleID, err)
		}
	}
	return sessionMap, nil
}

// runChatVariant replays one sample's conversation under one config axis and
// scores the resulting chat prompt.
func runChatVariant(
	ctx context.Context,
	sample *LocomoSample,
	axis chatAxis,
	client *honcho.Client,
	sessionMap map[string]string,
	workspace string,
	budgetTokens int,
) (ChatVariantResult, error) {
	dbPath := filepath.Join(workspace, fmt.Sprintf("chat_%s_%s.db", axis.name, sample.SampleID))
	engine, err := seahorse.NewEngine(seahorse.Config{DBPath: dbPath}, nil)
	if err != nil {
		return ChatVariantResult{}, fmt.Errorf("create engine: %w", err)
	}
	defer engine.Close()

	sessionKey := "chat-" + sample.SampleID
	turns := GetTurns(sample)

	// Replay turns as chat messages. Every turn is a user message carrying the
	// speaker prefix, matching how transcripts reach the short-term memory.
	seahorseMsgs := make([]seahorse.Message, 0, len(turns))
	providerHistory := make([]providers.Message, 0, len(turns))
	for _, turn := range turns {
		content := turn.Speaker + ": " + turn.Text
		seahorseMsgs = append(seahorseMsgs, seahorse.Message{
			Role:       "user",
			Content:    content,
			TokenCount: len(content) / 4,
		})
		providerHistory = append(providerHistory, providers.Message{Role: "user", Content: content})
	}
	if _, err := engine.Ingest(ctx, sessionKey, seahorseMsgs); err != nil {
		return ChatVariantResult{}, fmt.Errorf("ingest: %w", err)
	}

	before, err := contextTokens(ctx, engine, sessionKey)
	if err != nil {
		return ChatVariantResult{}, err
	}

	// --- auto-compress axis ---
	var compressSeconds float64
	if axis.autoComp {
		start := time.Now()
		if _, err := engine.CompactUntilUnder(ctx, sessionKey, budgetTokens); err != nil {
			return ChatVariantResult{}, fmt.Errorf("compact: %w", err)
		}
		compressSeconds = time.Since(start).Seconds()
	}

	after, err := contextTokens(ctx, engine, sessionKey)
	if err != nil {
		return ChatVariantResult{}, err
	}

	// Assemble the post-(optional-)compaction context the runtime would use.
	assembleBudget := after * 2
	if assembleBudget < before*2 {
		assembleBudget = before * 2
	}
	if assembleBudget < 4096 {
		assembleBudget = 4096
	}
	assembled, err := engine.Assemble(ctx, sessionKey, seahorse.AssembleInput{Budget: assembleBudget})
	if err != nil {
		log.Printf("WARN: assemble for %s/%s: %v", axis.name, sample.SampleID, err)
	}
	var history []providers.Message
	summary := ""
	if assembled != nil {
		summary = assembled.Summary
		for _, m := range assembled.Messages {
			role := m.Role
			if role == "" {
				role = "user"
			}
			history = append(history, providers.Message{Role: role, Content: m.Content})
		}
	}

	// --- Honcho recall axis ---
	// The Honcho read window mirrors the runtime default: config ContextTokens
	// defaults to 0 (uncapped), so recall reads the full session context rather
	// than being starved by the compaction budget. budgetTokens governs
	// compaction only, not the Honcho read.
	honchoContext := ""
	if axis.honchoRec {
		honchoContext = fetchRecallBlock(ctx, client, sessionMap[sample.SampleID], sample, 0)
	}

	// Build the real chat prompt through the agent's context builder.
	cb := agent.NewContextBuilder(workspace)
	currentMessage := "Given the conversation so far, answer the following questions concisely."
	promptReq := agent.PromptBuildRequest{
		History:                history,
		Summary:                summary,
		ExternalMemoryContext:  honchoContext,
		ExternalMemoryProvider: "honcho",
		CurrentMessage:         currentMessage,
		Channel:                "bench",
		ChatID:                 sample.SampleID,
		SenderID:               "bench",
	}
	prompt := cb.BuildMessagesFromPrompt(promptReq)
	contextText := StringListToContent(promptContents(prompt))

	recall := averageEvidenceCoverage(sample, contextText)

	kept := 100.0
	if before > 0 {
		kept = 100 * float64(after) / float64(before)
	}
	freed := 100 - kept
	if before == 0 {
		freed = 0
	}

	return ChatVariantResult{
		Policy:          fmt.Sprintf("%s/%s", axis.name, sample.SampleID),
		RecallPct:       recall,
		BeforeTokens:    before,
		AfterTokens:     after,
		KeptPct:         kept,
		FreedPct:        freed,
		Slight:          freed < SlightFreedThresholdPct,
		CompressSeconds: compressSeconds,
		Questions:       len(sample.QA),
		Turns:           len(turns),
		AutoComp:        axis.autoComp,
		HonchoRec:       axis.honchoRec,
	}, nil
}

// fetchRecallBlock mirrors agent.HonchoSyncer.fetchContext using the exported
// honcho client read API: prefer the session context (summary + peer
// representation), fall back to the peer context, and truncate the block to the
// same rune cap the runtime applies.
//
// contextTokens bounds the Honcho read window, matching
// HonchoMemoryConfig.EffectiveContextTokens: 0 means uncapped (the runtime
// default), so the bench reads the full session context.
func fetchRecallBlock(
	ctx context.Context,
	client *honcho.Client,
	sessionID string,
	sample *LocomoSample,
	contextTokens int,
) string {
	if sessionID == "" {
		return ""
	}
	if sc, err := client.GetSessionContext(ctx, honchoWorkspace, sessionID, honcho.SessionContextOptions{
		Tokens:  contextTokens,
		Summary: true,
	}); err == nil && sc != nil {
		if block := renderRecallBlock(sc.Summary, sc.Messages); block != "" {
			return block
		}
	} else if err != nil {
		log.Printf("WARN: honcho session context %s: %v", sample.SampleID, err)
	}

	pc, err := client.GetPeerContext(ctx, honchoWorkspace, "agent", "user")
	if err != nil {
		log.Printf("WARN: honcho peer context %s: %v", sample.SampleID, err)
		return ""
	}
	if pc == nil {
		return ""
	}
	return truncateRecallRunes(strings.TrimSpace(pc.Representation), chatRecallMaxRunes)
}

// renderRecallBlock composes the injected block from a session-context
// response, preferring the summary but falling back to the raw messages.
func renderRecallBlock(summary *honcho.Summary, msgs []honcho.ContextMessage) string {
	var parts []string
	if summary != nil && strings.TrimSpace(summary.Content) != "" {
		parts = append(parts, "Session summary:\n"+strings.TrimSpace(summary.Content))
	}
	if len(parts) == 0 {
		for _, m := range msgs {
			if s := strings.TrimSpace(m.Content); s != "" {
				parts = append(parts, s)
			}
		}
	}
	out := strings.TrimSpace(strings.Join(parts, "\n"))
	if out == "" {
		return ""
	}
	return truncateRecallRunes(out, chatRecallMaxRunes)
}

// chatRecallMaxRunes mirrors agent.maxHonchoContextRunes so the bench injects a
// block no larger than the runtime would.
const chatRecallMaxRunes = 4000

func truncateRecallRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// promptContents flattens a built prompt into its message content strings.
func promptContents(messages []providers.Message) []string {
	out := make([]string, 0, len(messages))
	for _, m := range messages {
		if strings.TrimSpace(m.Content) != "" {
			out = append(out, m.Content)
		}
	}
	return out
}

// SaveChatResults writes the chat A/B scorecard as JSON. Two files are emitted:
//   - chat_scorecard.json (this harness's native name)
//   - chat_scorecard_hermes.json (hermes-agent evals/compaction schema) so
//     report.py can render it directly.
func SaveChatResults(rows []ChatVariantResult, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}
	if err := writeJSONFile(filepath.Join(outDir, "chat_scorecard.json"), rows); err != nil {
		return err
	}
	// Project onto the hermes schema (drop the chat extras) for report.py.
	hermes := make([]CompressCycleResult, 0, len(rows))
	for _, r := range rows {
		hermes = append(hermes, CompressCycleResult{
			Policy:          r.Policy,
			RecallPct:       r.RecallPct,
			BeforeTokens:    r.BeforeTokens,
			AfterTokens:     r.AfterTokens,
			KeptPct:         r.KeptPct,
			FreedPct:        r.FreedPct,
			Slight:          r.Slight,
			CompressSeconds: r.CompressSeconds,
			Questions:       r.Questions,
		})
	}
	return writeJSONFile(filepath.Join(outDir, "chat_scorecard_hermes.json"), hermes)
}
