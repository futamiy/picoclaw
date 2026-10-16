package memorytools

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeStore is an in-memory Store implementation for tests.
type fakeStore struct {
	longTerm  string
	longLimit int
	notes     []string
	notesOn   bool
	failAll   bool
}

func (f *fakeStore) ReadLongTerm() string    { return f.longTerm }
func (f *fakeStore) LongTermCharLimit() int  { return f.longLimit }
func (f *fakeStore) DailyNotesEnabled() bool { return f.notesOn }
func (f *fakeStore) AddLongTerm(e string) error {
	if f.failAll {
		return errors.New("memory limit exceeded")
	}
	if f.longTerm != "" {
		f.longTerm += "\n" + e
	} else {
		f.longTerm = e
	}
	return nil
}
func (f *fakeStore) ReplaceLongTerm(o, n string) error {
	if !strings.Contains(f.longTerm, o) {
		return nil
	}
	f.longTerm = strings.Replace(f.longTerm, o, n, 1)
	return nil
}
func (f *fakeStore) RemoveLongTerm(s string) error {
	f.longTerm = ""
	return nil
}
func (f *fakeStore) ApplyOperations(ops []Operation) error {
	if f.failAll {
		return errors.New("memory limit exceeded")
	}
	for _, op := range ops {
		switch op.Op {
		case "add":
			_ = f.AddLongTerm(op.Content)
		case "replace":
			_ = f.ReplaceLongTerm(op.OldText, op.NewText)
		case "remove":
			_ = f.RemoveLongTerm(op.Text)
		default:
			return errors.New("unknown op " + op.Op)
		}
	}
	return nil
}
func (f *fakeStore) AppendToday(c string) error {
	if !f.notesOn {
		return errors.New("daily notes are disabled")
	}
	f.notes = append(f.notes, c)
	return nil
}

func execute(t *testing.T, store Store, args map[string]any) (bool, string) {
	t.Helper()
	res := NewMemoryTool(store).Execute(context.Background(), args)
	return res.IsError, res.ForLLM
}

func TestMemoryTool_Actions(t *testing.T) {
	ctx := context.Background()
	tool := NewMemoryTool(&fakeStore{notesOn: true})

	tests := []struct {
		name    string
		args    map[string]any
		wantErr bool
	}{
		{"add", map[string]any{"action": "add", "content": "entry A"}, false},
		{"replace", map[string]any{"action": "add", "content": "user likes X"}, false},
		{"read", map[string]any{"action": "read"}, false},
		{"note", map[string]any{"action": "note", "content": "noted today"}, false},
		{"unknown action", map[string]any{"action": "nuke"}, true},
		{"add missing content", map[string]any{"action": "add"}, true},
		{"replace missing old_text", map[string]any{"action": "replace", "new_text": "y"}, true},
		{"remove missing text", map[string]any{"action": "remove"}, true},
		{"note missing content", map[string]any{"action": "note"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := tool.Execute(ctx, tt.args)
			if res.IsError != tt.wantErr {
				t.Errorf("IsError = %v, want %v (msg=%q)", res.IsError, tt.wantErr, res.ForLLM)
			}
		})
	}
}

func TestMemoryTool_NotesDisabled(t *testing.T) {
	tool := NewMemoryTool(&fakeStore{notesOn: false})
	res := tool.Execute(context.Background(), map[string]any{"action": "note", "content": "x"})
	if !res.IsError || !strings.Contains(res.ForLLM, "disabled") {
		t.Fatalf("expected disabled error, got err=%v msg=%q", res.IsError, res.ForLLM)
	}
}

func TestMemoryTool_DescriptionMentionsCap(t *testing.T) {
	tool := NewMemoryTool(&fakeStore{longLimit: 2200, notesOn: true})
	if !strings.Contains(tool.Description(), "capped") {
		t.Fatalf("description should mention the cap: %q", tool.Description())
	}
}

func TestMemoryTool_BatchOperations(t *testing.T) {
	store := &fakeStore{notesOn: true}
	res := NewMemoryTool(store).Execute(context.Background(), map[string]any{
		"operations": []any{
			map[string]any{"op": "add", "content": "entry A"},
			map[string]any{"op": "replace", "old_text": "A", "new_text": "B"},
			map[string]any{"op": "remove", "text": "B"},
		},
	})
	if res.IsError {
		t.Fatalf("batch should succeed, got %q", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Applied 3 memory operations atomically.") {
		t.Fatalf("unexpected batch result: %q", res.ForLLM)
	}
}

func TestMemoryTool_BatchTakesPrecedenceOverAction(t *testing.T) {
	store := &fakeStore{notesOn: true}
	res := NewMemoryTool(store).Execute(context.Background(), map[string]any{
		"action":     "nuke", // would error on its own
		"operations": []any{map[string]any{"op": "add", "content": "kept"}},
	})
	if res.IsError {
		t.Fatalf("operations must take precedence over action, got %q", res.ForLLM)
	}
	if !strings.Contains(store.longTerm, "kept") {
		t.Fatalf("batch op not applied: %q", store.longTerm)
	}
}

func TestMemoryTool_ConsecutiveFailureCap(t *testing.T) {
	tool := NewMemoryTool(&fakeStore{notesOn: true, failAll: true})
	ctx := context.Background()
	args := map[string]any{"action": "add", "content": "x"}

	for i := 1; i < maxConsecutiveFailures; i++ {
		if res := tool.Execute(ctx, args); !res.IsError || strings.Contains(res.ForLLM, "STOP") {
			t.Fatalf("failure %d should error without STOP, got %q", i, res.ForLLM)
		}
	}
	res := tool.Execute(ctx, args)
	if !res.IsError || !strings.Contains(res.ForLLM, "STOP") {
		t.Fatalf("call %d should return the terminal STOP message, got %q", maxConsecutiveFailures, res.ForLLM)
	}
}

func TestMemoryTool_SuccessResetsFailureCap(t *testing.T) {
	store := &fakeStore{notesOn: true, failAll: true}
	tool := NewMemoryTool(store)
	ctx := context.Background()
	for i := 0; i < maxConsecutiveFailures-1; i++ {
		tool.Execute(ctx, map[string]any{"action": "add", "content": "x"})
	}
	store.failAll = false
	if res := tool.Execute(ctx, map[string]any{"action": "add", "content": "ok"}); res.IsError {
		t.Fatalf("expected success after store recovers, got %q", res.ForLLM)
	}
	// Counter reset: subsequent failures start over, no STOP on the first.
	store.failAll = true
	if res := tool.Execute(ctx, map[string]any{"action": "add", "content": "x"}); strings.Contains(res.ForLLM, "STOP") {
		t.Fatalf("failure counter should have reset, got %q", res.ForLLM)
	}
}
