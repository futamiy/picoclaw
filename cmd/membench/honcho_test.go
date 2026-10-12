package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sipeed/picoclaw/pkg/honcho"
)

// syntheticSample builds a minimal LOCOMO-shaped sample for tests: two
// sessions of turns plus a QA bank whose evidence points at real dia_ids.
func syntheticSample() LocomoSample {
	s1, _ := json.Marshal([]LocomoTurn{
		{Speaker: "Alice", DiaID: "D1:1", Text: "I adopted a golden retriever named Rex last spring in Denver."},
		{Speaker: "Bob", DiaID: "D1:2", Text: "That is wonderful, dogs make great companions for hiking trips."},
		{Speaker: "Alice", DiaID: "D1:3", Text: "Rex loves the mountain trails near Boulder on weekend mornings."},
	})
	s2, _ := json.Marshal([]LocomoTurn{
		{Speaker: "Alice", DiaID: "D2:1", Text: "My sister Mary moved to Seattle for a new engineering job."},
		{Speaker: "Bob", DiaID: "D2:2", Text: "Seattle is rainy but the coffee culture there is unmatched."},
		{Speaker: "Alice", DiaID: "D2:3", Text: "Rex and I visit Mary whenever we drive up the coast in summer."},
	})
	return LocomoSample{
		SampleID: "syn1",
		Conversation: map[string]json.RawMessage{
			"session_1": s1,
			"session_2": s2,
			"speaker_a": json.RawMessage(`"Alice"`),
			"speaker_b": json.RawMessage(`"Bob"`),
		},
		QA: []LocomoQA{
			{
				Question: "What is the name of Alice's dog?",
				Answer:   json.RawMessage(`"Rex"`),
				Evidence: []string{"D1:1"},
				Category: 1,
			},
			{
				Question: "Where did Mary move for her engineering job?",
				Answer:   json.RawMessage(`"Seattle"`),
				Evidence: []string{"D2:1"},
				Category: 1,
			},
		},
	}
}

func TestMockHonchoIngestAndContext(t *testing.T) {
	mock := newMockHoncho()
	srv := mock.start()
	defer srv.Close()

	ctx := context.Background()
	sample := syntheticSample()
	samples := []LocomoSample{sample}

	// Point the ingest helper at the mock by setting the env base.
	t.Setenv(honchoEnvBase, srv.URL)

	ir, err := IngestHoncho(ctx, samples)
	if err != nil {
		t.Fatalf("IngestHoncho: %v", err)
	}
	if ir.SessionMap[sample.SampleID] == "" {
		t.Fatal("session map not populated")
	}

	// Session context should return the ingested messages.
	ctxText := buildHonchoContext(ctx, ir, &samples[0], &samples[0].QA[0], 4000)
	if ctxText == "" {
		t.Fatal("expected non-empty honcho context")
	}
	if !containsFold(ctxText, "Rex") {
		t.Errorf("context missing 'Rex': %q", ctxText)
	}
}

func TestMockHonchoSearchRanksByOverlap(t *testing.T) {
	mock := newMockHoncho()
	srv := mock.start()
	defer srv.Close()

	ctx := context.Background()
	t.Setenv(honchoEnvBase, srv.URL)

	samples := []LocomoSample{syntheticSample()}
	ir, err := IngestHoncho(ctx, samples)
	if err != nil {
		t.Fatalf("IngestHoncho: %v", err)
	}

	// SearchPeer should surface the Seattle message for a Seattle query.
	msgs, err := ir.Client.SearchPeer(ctx, ir.WorkspaceID, "user", honcho.SearchOptions{
		Query: "Seattle engineering",
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("SearchPeer: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected search hits")
	}
	if !containsFold(msgs[0].Content, "Seattle") {
		t.Errorf("best hit should mention Seattle, got %q", msgs[0].Content)
	}
}

func TestEvalHonchoTokenMetrics(t *testing.T) {
	mock := newMockHoncho()
	srv := mock.start()
	defer srv.Close()

	ctx := context.Background()
	t.Setenv(honchoEnvBase, srv.URL)

	samples := []LocomoSample{syntheticSample()}
	ir, err := IngestHoncho(ctx, samples)
	if err != nil {
		t.Fatalf("IngestHoncho: %v", err)
	}

	results := EvalHoncho(ctx, samples, ir, 4000)
	if len(results) != 1 {
		t.Fatalf("expected 1 sample result, got %d", len(results))
	}
	if results[0].Mode != "honcho" {
		t.Errorf("mode = %q, want honcho", results[0].Mode)
	}
	if len(results[0].QAResults) != 2 {
		t.Errorf("QA results = %d, want 2", len(results[0].QAResults))
	}
	// The dog question's evidence turn mentions Rex, which the session
	// context returns, so hit rate should be > 0.
	if results[0].QAResults[0].HitRate <= 0 {
		t.Errorf("expected positive hit rate, got %v", results[0].QAResults[0].HitRate)
	}
}

func TestRunCompressCycles(t *testing.T) {
	ctx := context.Background()
	samples := []LocomoSample{syntheticSample()}
	dir := t.TempDir()

	rows, err := RunCompressCycles(ctx, samples, dir, 200)
	if err != nil {
		t.Fatalf("RunCompressCycles: %v", err)
	}
	// 1 sample × 2 policies.
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.TokensBefore == 0 {
			t.Errorf("row %s: TokensBefore should be > 0", r.Policy)
		}
		if r.KeptPct < 0 || r.KeptPct > 100 {
			t.Errorf("row %s: KeptPct out of range: %v", r.Policy, r.KeptPct)
		}
	}
}

func TestCompressScorecardMarkdown(t *testing.T) {
	rows := []CompressCycleResult{
		{Policy: "leaf/s1", RecallPct: 50, TokensBefore: 1000, TokensAfter: 800, KeptPct: 80, FreedPct: 20, Slight: true},
		{Policy: "until_under/s1", RecallPct: 25, TokensBefore: 1000, TokensAfter: 300, KeptPct: 30, FreedPct: 70, Slight: false},
	}
	md := CompressScorecardMarkdown(rows)
	if !containsFold(md, "| policy |") {
		t.Errorf("markdown missing header: %s", md)
	}
	// Sorted by recall desc → leaf first.
	if idxLeaf, idxUntil := indexOf(md, "leaf/s1"), indexOf(md, "until_under/s1"); idxLeaf > idxUntil {
		t.Errorf("rows not sorted by recall desc: %s", md)
	}
	if !containsFold(md, "| yes |") || !containsFold(md, "| no |") {
		t.Errorf("markdown missing slight column values: %s", md)
	}
}

// --- small test helpers ---

func containsFold(haystack, needle string) bool {
	return indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
