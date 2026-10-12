package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/sipeed/picoclaw/pkg/honcho"
)

// HonchoIngestResult holds the state needed to evaluate honcho-backed recall.
type HonchoIngestResult struct {
	Client      *honcho.Client
	WorkspaceID string
	// SessionMap maps a LOCOMO sample id to the honcho session id used for it.
	SessionMap map[string]string
	// Mock is non-nil when the benchmark ran against the in-process mock server.
	Mock *mockHoncho
	// MockServer is the httptest server backing Mock (closed by the caller).
	MockServer interface{ Close() }
}

// honchoWorkspace is the single workspace all samples are ingested under.
const honchoWorkspace = "membench"

// honchoEnvBase lets a caller point the benchmark at a real Honcho server.
const honchoEnvBase = "MEMBENCH_HONCHO_BASE"

// honchoEnvKey is the bearer token for a real Honcho server.
const honchoEnvKey = "MEMBENCH_HONCHO_KEY"

// envOrMockHoncho returns a client pointed at MEMBENCH_HONCHO_BASE when set,
// otherwise at a freshly started in-process mock server.
func envOrMockHoncho() (*honcho.Client, *mockHoncho, interface{ Close() }, error) {
	if base := strings.TrimSpace(os.Getenv(honchoEnvBase)); base != "" {
		log.Printf("honcho: using live server at %s", base)
		return honcho.New(base, os.Getenv(honchoEnvKey)), nil, nil, nil
	}
	mock := newMockHoncho()
	srv := mock.start()
	log.Printf("honcho: started in-process mock server at %s", srv.URL)
	return honcho.New(srv.URL, ""), mock, srv, nil
}

// IngestHoncho pushes every sample's turns into a honcho session.
// It is idempotent: sessions are keyed by sample id.
func IngestHoncho(ctx context.Context, samples []LocomoSample) (*HonchoIngestResult, error) {
	client, mock, srv, err := envOrMockHoncho()
	if err != nil {
		return nil, err
	}

	sessionMap := make(map[string]string, len(samples))
	for si := range samples {
		sample := &samples[si]
		sessionID := honcho.SanitizeID("locomo-" + sample.SampleID)
		sessionMap[sample.SampleID] = sessionID

		if err := client.Ensure(ctx, honchoWorkspace, sessionID, "user", "agent"); err != nil {
			return nil, fmt.Errorf("ensure sample %s: %w", sample.SampleID, err)
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
			return nil, fmt.Errorf("add messages sample %s: %w", sample.SampleID, err)
		}
		log.Printf("honcho: ingested sample %s: %d turns, session=%s", sample.SampleID, len(turns), sessionID)
	}

	log.Printf("honcho ingestion complete: %d samples", len(sessionMap))
	return &HonchoIngestResult{
		Client:      client,
		WorkspaceID: honchoWorkspace,
		SessionMap:  sessionMap,
		Mock:        mock,
		MockServer:  srv,
	}, nil
}

// buildHonchoContext retrieves context for a QA item via the session context
// endpoint, falling back to peer search when the session backend serves no
// messages (e.g. a real server whose summary has not been generated yet).
func buildHonchoContext(
	ctx context.Context,
	ir *HonchoIngestResult,
	sample *LocomoSample,
	qa *LocomoQA,
	budgetTokens int,
) string {
	sessionID, ok := ir.SessionMap[sample.SampleID]
	if !ok {
		return ""
	}

	sc, err := ir.Client.GetSessionContext(ctx, ir.WorkspaceID, sessionID, honcho.SessionContextOptions{
		Tokens:  budgetTokens,
		Summary: true,
	})
	if err != nil {
		log.Printf("WARN: honcho session context for %s: %v", sample.SampleID, err)
	}

	var parts []string
	if sc != nil {
		if sc.Summary != nil && sc.Summary.Content != "" {
			parts = append(parts, sc.Summary.Content)
		}
		for _, m := range sc.Messages {
			parts = append(parts, m.Content)
		}
	}
	if len(parts) == 0 {
		// Fallback: keyword search across the workspace.
		msgs, err := ir.Client.SearchPeer(ctx, ir.WorkspaceID, "user", honcho.SearchOptions{
			Query: qa.Question,
			Limit: 20,
		})
		if err != nil {
			log.Printf("WARN: honcho search for %s: %v", sample.SampleID, err)
		}
		for _, m := range msgs {
			parts = append(parts, m.Content)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	truncated, _ := BudgetTruncate(parts, budgetTokens)
	return StringListToContent(truncated)
}

// EvalHoncho evaluates honcho-backed recall with direct token metrics.
func EvalHoncho(
	ctx context.Context,
	samples []LocomoSample,
	ir *HonchoIngestResult,
	budgetTokens int,
) []EvalResult {
	results := make([]EvalResult, 0, len(samples))
	for si := range samples {
		sample := &samples[si]
		qaResults := make([]QAResult, 0, len(sample.QA))
		for qi := range sample.QA {
			qa := &sample.QA[qi]
			contextText := buildHonchoContext(ctx, ir, sample, qa, budgetTokens)
			f1 := TokenOverlapF1(contextText, qa.AnswerString())
			hitRate := RecallHitRate(qa.Evidence, sample, contextText)
			qaResults = append(qaResults, QAResult{
				Question:   qa.Question,
				Category:   qa.Category,
				GoldAnswer: qa.AnswerString(),
				TokenF1:    f1,
				HitRate:    hitRate,
			})
		}
		results = append(results, EvalResult{
			Mode:      "honcho",
			SampleID:  sample.SampleID,
			QAResults: qaResults,
			Agg:       aggregateMetrics(qaResults),
		})
	}
	return results
}

// EvalHonchoLLM evaluates honcho recall with LLM generation + LLM-as-Judge.
func EvalHonchoLLM(
	ctx context.Context,
	samples []LocomoSample,
	ir *HonchoIngestResult,
	budgetTokens int,
	answerClient, judgeClient *LLMClient,
	concurrency int,
) []EvalResult {
	if concurrency < 1 {
		concurrency = 1
	}
	totalQA := countTotalQA(samples)
	results := make([]EvalResult, 0, len(samples))

	for si := range samples {
		sample := &samples[si]
		qaResults := make([]QAResult, len(sample.QA))

		evalOne := func(qi int) {
			qa := &sample.QA[qi]
			contextText := buildHonchoContext(ctx, ir, sample, qa, budgetTokens)
			if contextText == "" {
				qaResults[qi] = QAResult{
					Question:   qa.Question,
					Category:   qa.Category,
					GoldAnswer: qa.AnswerString(),
					TokenF1:    0.0,
					HitRate:    0.0,
				}
				return
			}
			out := evalQAWorker(ctx, qaWork{
				sampleID: sample.SampleID, qaIndex: qi,
				globalIndex: si*len(sample.QA) + qi + 1, totalQA: totalQA,
				qa: qa, contextText: contextText, sample: sample,
			}, answerClient, judgeClient, "honcho-llm")
			qaResults[qi] = out.result
		}

		if concurrency <= 1 {
			for qi := range sample.QA {
				evalOne(qi)
			}
		} else {
			runConcurrent(len(sample.QA), concurrency, evalOne)
		}

		results = append(results, EvalResult{
			Mode:      "honcho-llm",
			SampleID:  sample.SampleID,
			QAResults: qaResults,
			Agg:       aggregateMetrics(qaResults),
		})
	}
	return results
}
