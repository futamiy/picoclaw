package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	memorytool "github.com/sipeed/picoclaw/pkg/tools/memory"
)

// newTestMemoryStore builds a store with a tiny cap so over-limit behavior is easy to assert.
func newTestMemoryStore(t *testing.T, capLimit int) *MemoryStore {
	t.Helper()
	return NewMemoryStoreWithOptions(t.TempDir(), MemoryOptions{
		LongTermCharLimit:  capLimit,
		DailyNotesEnabled:  true,
		DailyNoteCharLimit: capLimit,
		DailyNotesDays:     3,
	})
}

func TestMemoryStore_WriteLimitRejected(t *testing.T) {
	ms := newTestMemoryStore(t, 10)
	err := ms.WriteLongTerm(strings.Repeat("x", 11))
	if err == nil || !strings.Contains(err.Error(), "memory limit exceeded") {
		t.Fatalf("expected ErrMemoryFull, got %v", err)
	}
	if got := ms.ReadLongTerm(); got != "" {
		t.Fatalf("write must be rejected and not persisted, got %q", got)
	}
}

func TestMemoryStore_WriteLimitErrorIsActionable(t *testing.T) {
	ms := newTestMemoryStore(t, 10)
	err := ms.WriteLongTerm(strings.Repeat("x", 25))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"memory limit exceeded", "limit 10 runes", "got 25", "free at least 15 runes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestMemoryStore_AddReplaceRemove(t *testing.T) {
	ms := newTestMemoryStore(t, 100000)

	if err := ms.AddLongTerm("user prefers concise answers"); err != nil {
		t.Fatalf("AddLongTerm: %v", err)
	}
	// Exact duplicate must be ignored.
	if err := ms.AddLongTerm("user prefers concise answers"); err != nil {
		t.Fatalf("duplicate AddLongTerm: %v", err)
	}
	if got := strings.Count(ms.ReadLongTerm(), "concise"); got != 1 {
		t.Fatalf("expected 1 entry, got %d", got)
	}

	if err := ms.ReplaceLongTerm("prefers", "likes"); err != nil {
		t.Fatalf("ReplaceLongTerm: %v", err)
	}
	if !strings.Contains(ms.ReadLongTerm(), "likes concise") {
		t.Fatalf("replacement not applied: %q", ms.ReadLongTerm())
	}

	if err := ms.RemoveLongTerm("does-not-exist"); err == nil {
		t.Fatal("expected error on removing a non-matching entry")
	}
	if err := ms.RemoveLongTerm("likes"); err != nil {
		t.Fatalf("RemoveLongTerm: %v", err)
	}
	if got := ms.ReadLongTerm(); strings.TrimSpace(got) != "" {
		t.Fatalf("expected empty memory after remove, got %q", got)
	}
}

func TestMemoryStore_DailyNotes(t *testing.T) {
	ms := newTestMemoryStore(t, 100000)

	if err := ms.AppendToday("implemented FTS5 retrieval"); err != nil {
		t.Fatalf("AppendToday: %v", err)
	}
	// Daily note must include today's header.
	notes := ms.GetRecentDailyNotes(1)
	if !strings.Contains(notes, "implemented FTS5 retrieval") {
		t.Fatalf("recent notes missing entry: %q", notes)
	}

	small := NewMemoryStoreWithOptions(t.TempDir(), MemoryOptions{
		DailyNotesEnabled:  true,
		DailyNoteCharLimit: 5,
	})
	if err := small.AppendToday("way too long"); err == nil {
		t.Fatal("expected ErrMemoryFull on oversized daily note")
	}
}

func TestMemoryStore_GetMemoryContext(t *testing.T) {
	ms := newTestMemoryStore(t, 1000)
	_ = ms.WriteLongTerm("stay grounded\n")
	_ = ms.AppendToday("power went out at 3pm")

	ctx := ms.GetMemoryContext()
	for _, want := range []string{"## Long-term Memory", "stay grounded", "## Recent Daily Notes", "power went out at 3pm"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context missing %q:\n%s", want, ctx)
		}
	}

	// Disabled daily notes must render long-term memory only.
	ms.opts.DailyNotesEnabled = false
	ctx = ms.GetMemoryContext()
	if strings.Contains(ctx, "Recent Daily Notes") {
		t.Errorf("daily notes should be excluded when disabled: %s", ctx)
	}
}

func TestMemoryStore_GetMemoryContextTruncatesOverLimit(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStoreWithOptions(dir, MemoryOptions{
		LongTermCharLimit: 10,
		DailyNotesEnabled: false,
	})
	// Oversized content arrives via hand-editing or a lowered limit; reads must stay bounded.
	if err := os.WriteFile(filepath.Join(dir, "memory", "MEMORY.md"), []byte(strings.Repeat("x", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := ms.GetMemoryContext()
	if !strings.Contains(ctx, "[memory truncated]") {
		t.Fatalf("expected truncation marker, got %q", ctx)
	}
}

func TestMemoryStore_ApplyOperationsChecksFinalSize(t *testing.T) {
	ms := newTestMemoryStore(t, 45)
	if err := ms.WriteLongTerm("keep fact one\nstale entry\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// add-alone would overflow (52 > 45); remove-first makes it fit (40). The batch
	// must be judged on the FINAL size, not the intermediate add-first state.
	err := ms.ApplyOperations(TargetMemory, []memorytool.Operation{
		{Op: "add", Content: "prefers open-source tools"},
		{Op: "remove", Text: "stale entry"},
	})
	if err != nil {
		t.Fatalf("final size fits the cap, batch must succeed: %v", err)
	}
	got := ms.ReadLongTerm()
	if strings.Contains(got, "stale entry") || !strings.Contains(got, "prefers open-source tools") {
		t.Fatalf("batch not applied: %q", got)
	}
}

func TestMemoryStore_ApplyOperationsRollsBackOnInvalidOp(t *testing.T) {
	ms := newTestMemoryStore(t, 1000)
	if err := ms.WriteLongTerm("keep me\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := ms.ReadLongTerm()
	err := ms.ApplyOperations(TargetMemory, []memorytool.Operation{
		{Op: "add", Content: "new entry"},
		{Op: "remove", Text: "does-not-exist"},
	})
	if err == nil {
		t.Fatal("expected error from an unmatched remove")
	}
	if ms.ReadLongTerm() != before {
		t.Fatalf("failed batch must write nothing, got %q", ms.ReadLongTerm())
	}
}

func TestMemoryStore_DriftBackupsPruned(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStoreWithOptions(dir, MemoryOptions{LongTermCharLimit: 10000, DriftKeep: 2})
	for i := 0; i < 4; i++ {
		if err := ms.WriteLongTerm(fmt.Sprintf("v%d\n", i)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	baks, _ := filepath.Glob(filepath.Join(dir, "memory", "MEMORY.md.bak.*"))
	if len(baks) != 2 {
		t.Fatalf("want 2 retained drift snapshots, got %d: %v", len(baks), baks)
	}
	sort.Strings(baks)
	if b, _ := os.ReadFile(baks[len(baks)-1]); string(b) != "v2\n" {
		t.Fatalf("newest drift snapshot should hold the previous content v2, got %q", b)
	}
	if cur := ms.ReadLongTerm(); cur != "v3\n" {
		t.Fatalf("current content = %q, want v3", cur)
	}
}

func TestMemoryStore_PathLayout(t *testing.T) {
	dir := t.TempDir()
	_ = NewMemoryStoreWithOptions(dir, DefaultMemoryOptions())
	if _, err := os.Stat(filepath.Join(dir, "memory")); err != nil {
		t.Fatalf("memory dir should be created: %v", err)
	}
}
