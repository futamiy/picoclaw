package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestRunChatABProducesAllAxes verifies the harness emits one row per (axis,
// sample) and that the modified/original rows are well-formed.
func TestRunChatABProducesAllAxes(t *testing.T) {
	ctx := context.Background()
	mock := newMockHoncho()
	srv := mock.start()
	defer srv.Close()
	t.Setenv(honchoEnvBase, srv.URL)

	samples := []LocomoSample{syntheticSample()}
	dir := t.TempDir()

	rows, err := RunChatAB(ctx, samples, dir, 400)
	if err != nil {
		t.Fatalf("RunChatAB: %v", err)
	}
	// 4 axes × 1 sample.
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows (4 axes), got %d", len(rows))
	}

	seen := map[string]ChatVariantResult{}
	for _, r := range rows {
		name := r.Policy
		if i := indexOf(name, "/"); i >= 0 {
			name = name[:i]
		}
		seen[name] = r
	}
	for _, want := range []string{"original", "compress-only", "honcho-only", "modified"} {
		if _, ok := seen[want]; !ok {
			t.Errorf("missing axis row %q", want)
		}
	}

	orig := seen["original"]
	if orig.AutoComp || orig.HonchoRec {
		t.Errorf("original must have both axes off: %+v", orig)
	}
	mod := seen["modified"]
	if !mod.AutoComp || !mod.HonchoRec {
		t.Errorf("modified must have both axes on: %+v", mod)
	}
	if mod.BeforeTokens == 0 {
		t.Error("BeforeTokens should be > 0")
	}
	if mod.KeptPct < 0 || mod.KeptPct > 100 {
		t.Errorf("KeptPct out of range: %v", mod.KeptPct)
	}
	if mod.Turns == 0 {
		t.Error("Turns should be > 0")
	}
}

// TestChatHonchoRecallImprovesCoverage checks that enabling the Honcho recall
// axis actually injects the recalled memory into the assembled prompt, so a
// leaf-level turn that compaction may drop is still recoverable.
func TestChatHonchoRecallImprovesCoverage(t *testing.T) {
	ctx := context.Background()
	mock := newMockHoncho()
	srv := mock.start()
	defer srv.Close()
	t.Setenv(honchoEnvBase, srv.URL)

	samples := []LocomoSample{syntheticSample()}
	dir := t.TempDir()

	rows, err := RunChatAB(ctx, samples, dir, 400)
	if err != nil {
		t.Fatalf("RunChatAB: %v", err)
	}
	byAxis := map[string]ChatVariantResult{}
	for _, r := range rows {
		name := r.Policy
		if i := indexOf(name, "/"); i >= 0 {
			name = name[:i]
		}
		byAxis[name] = r
	}

	// The Honcho axis injects the full recalled transcript, so evidence
	// coverage must be at least as high as the plain (original) path.
	orig := byAxis["original"]
	honchoOnly := byAxis["honcho-only"]
	if honchoOnly.RecallPct < orig.RecallPct {
		t.Errorf("honcho recall (%.1f%%) should be >= original (%.1f%%)",
			honchoOnly.RecallPct, orig.RecallPct)
	}
	if honchoOnly.RecallPct <= 0 {
		t.Errorf("expected positive recall with honcho recall, got %.1f%%", honchoOnly.RecallPct)
	}
}

// TestChatModifiedRecoversCompressedEvidence locks in the core thesis: the
// modified path (auto-compress + Honcho recall) keeps the recall of the
// uncompressed original while still freeing tokens, whereas auto-compress alone
// loses the deep evidence. The sample buries a distinctive passphrase under a
// large filler/tail volume so compaction drops it and only Honcho's distilled
// session summary can recover it.
func TestChatModifiedRecoversCompressedEvidence(t *testing.T) {
	ctx := context.Background()
	mock := newMockHoncho()
	srv := mock.start()
	defer srv.Close()
	t.Setenv(honchoEnvBase, srv.URL)

	sample := buriedEvidenceSample(t)
	dir := t.TempDir()
	rows, err := RunChatAB(ctx, []LocomoSample{sample}, dir, 400)
	if err != nil {
		t.Fatalf("RunChatAB: %v", err)
	}
	byAxis := map[string]ChatVariantResult{}
	for _, r := range rows {
		name := r.Policy
		if i := indexOf(name, "/"); i >= 0 {
			name = name[:i]
		}
		byAxis[name] = r
	}

	orig := byAxis["original"]
	compressOnly := byAxis["compress-only"]
	modified := byAxis["modified"]

	if orig.RecallPct < 100 {
		t.Fatalf("original should have full recall, got %.1f%%", orig.RecallPct)
	}
	if compressOnly.RecallPct != 0 {
		t.Fatalf("compress-only should lose the buried evidence, got %.1f%%", compressOnly.RecallPct)
	}
	if modified.RecallPct != 100 {
		t.Fatalf("modified should recover the evidence via Honcho summary, got %.1f%%", modified.RecallPct)
	}
	if modified.FreedPct <= SlightFreedThresholdPct {
		t.Errorf("modified should actually free tokens, got %.1f%% freed", modified.FreedPct)
	}
}

// buriedEvidenceSample builds a single-session sample whose only salient fact
// (a passphrase) sits deep in a long run of filler, with a tail of more filler
// after it, so neither the head nor the tail of a chronological dump contains it.
func buriedEvidenceSample(t *testing.T) LocomoSample {
	t.Helper()
	var turns []LocomoTurn
	for i := 0; i < 120; i++ {
		spk := "Alice"
		if i%2 == 1 {
			spk = "Bob"
		}
		turns = append(turns, LocomoTurn{
			Speaker: spk,
			DiaID:   fmt.Sprintf("D1:%d", i+1),
			Text:    fmt.Sprintf("Filler turn %d covering mundane logistics, errands, and chit-chat with nothing memorable.", i),
		})
	}
	turns = append(turns, LocomoTurn{
		Speaker: "Alice",
		DiaID:   "D1:200",
		Text:    "The secret passphrase is ZEPHYR-QUARTZ-9917 and it must never be forgotten.",
	})
	for i := 0; i < 60; i++ {
		spk := "Alice"
		if i%2 == 1 {
			spk = "Bob"
		}
		turns = append(turns, LocomoTurn{
			Speaker: spk,
			DiaID:   fmt.Sprintf("D1:%d", 300+i),
			Text:    fmt.Sprintf("Tail turn %d recapping recent plans and confirmations with nothing distinctive.", i),
		})
	}
	conv := map[string]json.RawMessage{}
	raw, err := json.Marshal(turns)
	if err != nil {
		t.Fatalf("marshal turns: %v", err)
	}
	conv["session_1"] = raw
	return LocomoSample{
		SampleID:     "buried",
		Conversation: conv,
		QA: []LocomoQA{{
			Question: "What is the secret passphrase?",
			Answer:   json.RawMessage(`"ZEPHYR-QUARTZ-9917"`),
			Evidence: []string{"D1:200"},
			Category: 1,
		}},
	}
}

// TestMockHonchoSummaryIsSalienceRanked verifies the mock's server-side summary
// distills salient facts instead of dumping the transcript chronologically,
// which is what lets recall survive deep-buried evidence.
func TestMockHonchoSummaryIsSalienceRanked(t *testing.T) {
	msgs := []mockMessage{
		{content: "Filler turn 0 about routine logistics and errands.", seq: 0},
		{content: "The secret passphrase is ZEPHYR-QUARTZ-9917.", seq: 1},
		{content: "Tail turn about recapping plans.", seq: 2},
	}
	m := newMockHoncho()
	sum := m.synthesizeSummary(msgs)
	if sum == nil {
		t.Fatal("expected a synthesized summary")
	}
	content, _ := sum["content"].(string)
	if !containsFold(content, "ZEPHYR-QUARTZ-9917") {
		t.Errorf("summary should carry the salient fact: %q", content)
	}
	if containsFold(content, "Filler turn 0") {
		t.Errorf("summary should elide filler: %q", content)
	}
	if containsFold(content, "Tail turn about") {
		t.Errorf("summary should elide filler tail: %q", content)
	}
}

// TestChatScorecardHermesSchemaParity verifies the hermes-schema projection
// drops the chat extras and keeps the fields report.py expects.
func TestChatScorecardHermesSchemaParity(t *testing.T) {
	ctx := context.Background()
	mock := newMockHoncho()
	srv := mock.start()
	defer srv.Close()
	t.Setenv(honchoEnvBase, srv.URL)

	dir := t.TempDir()
	rows, err := RunChatAB(ctx, []LocomoSample{syntheticSample()}, dir, 400)
	if err != nil {
		t.Fatalf("RunChatAB: %v", err)
	}
	if err := SaveChatResults(rows, dir); err != nil {
		t.Fatalf("SaveChatResults: %v", err)
	}

	// Native file carries the chat extras.
	native, err := os.ReadFile(filepath.Join(dir, "chat_scorecard.json"))
	if err != nil {
		t.Fatalf("read native: %v", err)
	}
	var nativeRows []map[string]any
	if err := json.Unmarshal(native, &nativeRows); err != nil {
		t.Fatalf("parse native: %v", err)
	}
	if _, ok := nativeRows[0]["auto_compress"]; !ok {
		t.Error("native scorecard should carry auto_compress")
	}

	// Hermes file must carry only the schema keys report.py reads.
	hermes, err := os.ReadFile(filepath.Join(dir, "chat_scorecard_hermes.json"))
	if err != nil {
		t.Fatalf("read hermes: %v", err)
	}
	var hermesRows []map[string]any
	if err := json.Unmarshal(hermes, &hermesRows); err != nil {
		t.Fatalf("parse hermes: %v", err)
	}
	for _, key := range []string{"policy", "before_tokens", "after_tokens", "recall_pct", "compress_seconds"} {
		if _, ok := hermesRows[0][key]; !ok {
			t.Errorf("hermes scorecard missing key %q", key)
		}
	}
	if _, ok := hermesRows[0]["auto_compress"]; ok {
		t.Error("hermes scorecard should not carry chat extras")
	}
}

// TestChatScorecardMarkdown verifies the markdown table sorts by recall desc
// and includes the axis columns.
func TestChatScorecardMarkdown(t *testing.T) {
	rows := []ChatVariantResult{
		{Policy: "original/s1", RecallPct: 40, BeforeTokens: 1000, AfterTokens: 1000, KeptPct: 100, FreedPct: 0, Slight: true},
		{Policy: "modified/s1", RecallPct: 90, BeforeTokens: 1000, AfterTokens: 600, KeptPct: 60, FreedPct: 40, Slight: false, AutoComp: true, HonchoRec: true},
	}
	md := ChatScorecardMarkdown(rows)
	if !containsFold(md, "| policy |") {
		t.Errorf("markdown missing header: %s", md)
	}
	if idxMod, idxOrig := indexOf(md, "modified/s1"), indexOf(md, "original/s1"); idxMod > idxOrig {
		t.Errorf("rows not sorted by recall desc: %s", md)
	}
	if !containsFold(md, "| true |") || !containsFold(md, "| false |") {
		t.Errorf("markdown missing axis column values: %s", md)
	}
}
