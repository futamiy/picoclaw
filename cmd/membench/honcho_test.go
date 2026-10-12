package main

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestCompressScorecardMarkdown(t *testing.T) {
	rows := []CompressCycleResult{
		{Policy: "leaf/s1", RecallPct: 50, BeforeTokens: 1000, AfterTokens: 800, KeptPct: 80, FreedPct: 20, Slight: true},
		{Policy: "until_under/s1", RecallPct: 25, BeforeTokens: 1000, AfterTokens: 300, KeptPct: 30, FreedPct: 70, Slight: false},
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

// largeSampleWithBuriedEvidence builds a sample whose single QA evidence turn
// (a unique needle sentence) sits deep inside the compacted region — after
// enough filler to push it past the summary truncation window (2048 chars) and
// before the protected fresh tail. This forces a measurable recall drop on
// aggressive compaction.
func largeSampleWithBuriedEvidence(nFiller int) LocomoSample {
	const needle = "The secret passphrase is ZEPHYR-QUARTZ-9917 and it must never be forgotten."
	var turns []LocomoTurn
	// Filler first, so the needle lands well past the truncation cut.
	for i := 0; i < nFiller; i++ {
		spk := "Alice"
		if i%2 == 1 {
			spk = "Bob"
		}
		turns = append(turns, LocomoTurn{
			Speaker: spk,
			DiaID:   fmt.Sprintf("D1:%d", i+1),
			Text: fmt.Sprintf(
				"Filler turn %d about routine logistics, errands, weather, and other mundane chit-chat that carries no memorable content at all.",
				i,
			),
		})
	}
	// The needle, buried in the middle of the compacted region.
	turns = append(turns, LocomoTurn{Speaker: "Alice", DiaID: "D1:needle", Text: needle})
	// A large protected fresh tail after the needle.
	for i := 0; i < 60; i++ {
		spk := "Alice"
		if i%2 == 1 {
			spk = "Bob"
		}
		turns = append(turns, LocomoTurn{
			Speaker: spk,
			DiaID:   fmt.Sprintf("D1:tail%d", i),
			Text: fmt.Sprintf(
				"Tail turn %d recapping recent plans and confirmations with nothing distinctive.",
				i,
			),
		})
	}
	s1, _ := json.Marshal(turns)
	return LocomoSample{
		SampleID: "buried",
		Conversation: map[string]json.RawMessage{
			"session_1": s1,
			"speaker_a": json.RawMessage(`"Alice"`),
			"speaker_b": json.RawMessage(`"Bob"`),
		},
		QA: []LocomoQA{
			{
				Question: "What is the secret passphrase?",
				Answer:   json.RawMessage(`"ZEPHYR-QUARTZ-9917"`),
				Evidence: []string{"D1:needle"},
				Category: 1,
			},
		},
	}
}

func TestCompressRecallDegradation(t *testing.T) {
	ctx := context.Background()
	// Enough filler (~150 turns) that compaction splits several leaf chunks and
	// the needle's chunk summary truncates before reaching it.
	samples := []LocomoSample{largeSampleWithBuriedEvidence(150)}
	dir := t.TempDir()

	rows, err := RunCompressCycles(ctx, samples, dir, 400)
	if err != nil {
		t.Fatalf("RunCompressCycles: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	byPolicy := map[string]CompressCycleResult{}
	for _, r := range rows {
		name := r.Policy
		if i := indexOf(name, "/"); i >= 0 {
			name = name[:i]
		}
		byPolicy[name] = r
	}

	leaf, ok := byPolicy["leaf"]
	if !ok {
		t.Fatal("missing leaf policy row")
	}
	until, ok := byPolicy["until_under"]
	if !ok {
		t.Fatal("missing until_under policy row")
	}

	// Aggressive compaction must free more than the baseline leaf pass.
	if until.FreedPct <= leaf.FreedPct {
		t.Errorf("expected until_under to free more than leaf: until=%.1f%% leaf=%.1f%%",
			until.FreedPct, leaf.FreedPct)
	}

	// The buried needle should be dropped once its chunk is summarised and
	// truncated: aggressive compaction must not retain it at 100%.
	if until.RecallPct >= 100 {
		t.Errorf("expected recall degradation after aggressive compaction, got %.1f%%", until.RecallPct)
	}
	if until.RecallPct > leaf.RecallPct {
		t.Errorf("aggressive recall (%.1f%%) should be <= leaf recall (%.1f%%)",
			until.RecallPct, leaf.RecallPct)
	}
}

func TestRunCompressCyclesSmall(t *testing.T) {
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
		if r.BeforeTokens == 0 {
			t.Errorf("row %s: BeforeTokens should be > 0", r.Policy)
		}
		if r.KeptPct < 0 || r.KeptPct > 100 {
			t.Errorf("row %s: KeptPct out of range: %v", r.Policy, r.KeptPct)
		}
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
