package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

func TestParseMemoryOps_FencedJSON(t *testing.T) {
	content := "```json\n{\"memory\":{\"remove\":[\"stale note\"],\"replace\":[{\"old_text\":\"old fact\",\"new_text\":\"updated fact\"}],\"add\":[\"durable fact\"],\"note\":[\"fixed FTS5 today\"]}}\n```"
	ops := parseMemoryOps(content)
	if ops == nil {
		t.Fatal("expected ops")
	}
	if len(ops.Add) != 1 || ops.Add[0] != "durable fact" {
		t.Errorf("add: %v", ops.Add)
	}
	if len(ops.Replace) != 1 || ops.Replace[0].OldText != "old fact" || ops.Replace[0].NewText != "updated fact" {
		t.Errorf("replace: %+v", ops.Replace)
	}
	if len(ops.Remove) != 1 || len(ops.Note) != 1 {
		t.Errorf("remove/note: %v / %v", ops.Remove, ops.Note)
	}
}

func TestParseMemoryOps_Garbage(t *testing.T) {
	if ops := parseMemoryOps("I have no memory operations to report."); ops != nil {
		t.Fatalf("expected nil ops, got %+v", ops)
	}
}

func TestParseMemoryOps_EmptyArrays(t *testing.T) {
	ops := parseMemoryOps(`{"memory":{}}`)
	if ops == nil {
		t.Fatal("expected empty ops, not nil")
	}
	if len(ops.Add) != 0 {
		t.Errorf("expected no add entries, got %v", ops.Add)
	}
}

func TestBuildMemoryTranscript_FiltersNonTurns(t *testing.T) {	history := []providers.Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "tool", Content: "some result", ToolCallID: "x"},
	}
	transcript := buildMemoryTranscript(history)
	if strings.Contains(transcript, "you are an agent") || strings.Contains(transcript, "some result") {
		t.Fatalf("transcript should only contain user/assistant turns:\n%s", transcript)
	}
	if !strings.Contains(transcript, "user: hello") || !strings.Contains(transcript, "assistant: hi there") {
		t.Fatalf("transcript missing turns:\n%s", transcript)
	}
}

type extractorProvider struct {
	content string
	gotBody strings.Builder
}

func (p *extractorProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.gotBody.WriteString(messages[0].Content)
	return &providers.LLMResponse{Content: p.content}, nil
}

func (p *extractorProvider) GetDefaultModel() string { return "extract-model" }

func TestMemoryManager_RunExtraction(t *testing.T) {
	dir := t.TempDir()
	sessions := session.NewSessionManager(dir + "/sessions")
	sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "prefer open-source tools"})
	sessions.AddFullMessage("s1", providers.Message{Role: "assistant", Content: "noted, I will respect that"})

	store := NewMemoryStoreWithOptions(dir, MemoryOptions{
		LongTermCharLimit:  10000,
		DailyNotesEnabled:  true,
		DailyNoteCharLimit: 10000,
		DailyNotesDays:     1,
	})
	_ = store.WriteLongTerm("old fact to update\nstale entry\n")

	prov := &extractorProvider{
		content: `{"memory":{"remove":["stale entry"],"add":["prefers open-source tools"],"replace":[{"old_text":"old fact to update","new_text":"updated fact"}],"note":["user preference clarified"]}}`,
	}
	agent := &AgentInstance{
		ID:       "default-agent",
		Model:    "main-model",
		Provider: prov,
		Sessions: sessions,
	}
	mm := newMemoryManager(agent, store, MemoryExtractOptions{EveryTurns: 1, Model: ""}, nil)
	if mm.model != "main-model" {
		t.Fatalf("expected fallback to agent model, got %q", mm.model)
	}

	payload := mm.runExtraction("s1")
	if payload == nil {
		t.Fatal("expected non-nil payload")
	}
	if payload.Additions != 1 || payload.Replacements != 1 || payload.Removals != 1 || payload.Notes != 1 {
		t.Fatalf("unexpected counts: %+v", payload)
	}
	got := store.ReadLongTerm()
	if !strings.Contains(got, "updated fact") || strings.Contains(got, "old fact to update") {
		t.Fatalf("replace not applied: %q", got)
	}
	if !strings.Contains(got, "prefers open-source tools") {
		t.Fatalf("add not applied: %q", got)
	}
}

func TestMemoryManager_RunExtractionSanitizes(t *testing.T) {
	dir := t.TempDir()
	sessions := session.NewSessionManager(dir + "/sessions")
	sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "my API key is SECRET42"})

	store := NewMemoryStoreWithOptions(dir, MemoryOptions{LongTermCharLimit: 10000})
	prov := &extractorProvider{content: `{"memory":{}}`}
	agent := &AgentInstance{
		ID:       "default-agent",
		Model:    "main-model",
		Provider: prov,
		Sessions: sessions,
	}
	sanitize := func(s string) string { return strings.ReplaceAll(s, "SECRET42", "REDACTED") }
	mm := newMemoryManager(agent, store, MemoryExtractOptions{EveryTurns: 1}, sanitize)

	if payload := mm.runExtraction("s1"); payload == nil {
		t.Fatal("expected payload")
	}
	if strings.Contains(prov.gotBody.String(), "SECRET42") {
		t.Fatalf("sanitization not applied to extraction prompt")
	}
	if !strings.Contains(prov.gotBody.String(), "REDACTED") {
		t.Fatalf("expected redacted transcript")
	}
}

func TestMemoryManager_ExtractionFailureNoCountAdvance(t *testing.T) {
	dir := t.TempDir()
	sessions := session.NewSessionManager(dir + "/sessions")
	sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "hello"})

	store := NewMemoryStoreWithOptions(dir, MemoryOptions{LongTermCharLimit: 10000})
	prov := &extractorProvider{content: "no json here"}
	agent := &AgentInstance{
		ID:       "default-agent",
		Model:    "main-model",
		Provider: prov,
		Sessions: sessions,
	}
	mm := newMemoryManager(agent, store, MemoryExtractOptions{EveryTurns: 1}, nil)

	if payload := mm.runExtraction("s1"); payload != nil {
		t.Fatalf("unparsable LLM output must yield nil payload, got %+v", payload)
	}
}
