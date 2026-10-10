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
}

func (f *fakeStore) ReadLongTerm() string    { return f.longTerm }
func (f *fakeStore) LongTermCharLimit() int  { return f.longLimit }
func (f *fakeStore) DailyNotesEnabled() bool { return f.notesOn }
func (f *fakeStore) AddLongTerm(e string) error {
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
