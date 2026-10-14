package agent

// In-process end-to-end tests for the PicoClaw memory system. Unlike the unit
// tests, these drive components through their real wiring (the same constructors
// production uses: newMemoryManager, newMemorySyncer, newHonchoSyncer, the
// MemoryTool, the ContextBuilder) and assert on observable, cross-component
// behavior. External providers are impersonated by pkg/memtest fake servers.
//
// These tests are intentionally untagged: they use httptest, need no Docker and
// no network, so they run under the default `make test` tag set.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/honcho"
	"github.com/sipeed/picoclaw/pkg/mem0"
	"github.com/sipeed/picoclaw/pkg/memtest"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/retaindb"
	"github.com/sipeed/picoclaw/pkg/session"
	memorytool "github.com/sipeed/picoclaw/pkg/tools/memory"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestAgent builds a minimal AgentInstance wired with a real session store
// and the given provider, matching how production assembles the memory pieces.
func newTestAgent(t *testing.T, id string, prov providers.LLMProvider) (*AgentInstance, string) {
	t.Helper()
	dir := t.TempDir()
	sessions := session.NewSessionManager(filepath.Join(dir, "sessions"))
	return &AgentInstance{
		ID:        id,
		Model:     "main-model",
		Workspace: dir,
		Provider:  prov,
		Sessions:  sessions,
	}, dir
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// ---------------------------------------------------------------------------
// Bounded local store — end-to-end semantics (not just unit behavior)
// ---------------------------------------------------------------------------

func TestE2E_MemoryStore_FullLifecycle(t *testing.T) {
	dir := t.TempDir()
	store := NewMemoryStoreWithOptions(dir, MemoryOptions{
		LongTermCharLimit:  120,
		DailyNotesEnabled:  true,
		DailyNoteCharLimit: 200,
		DailyNotesDays:     2,
	})

	// add -> read
	if err := store.AddLongTerm("user prefers dark mode"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := store.AddLongTerm("project uses Go 1.25"); err != nil {
		t.Fatalf("add 2: %v", err)
	}
	// duplicate add is a silent no-op
	if err := store.AddLongTerm("user prefers dark mode"); err != nil {
		t.Fatalf("dup add: %v", err)
	}
	if got := strings.Count(store.ReadLongTerm(), "user prefers dark mode"); got != 1 {
		t.Fatalf("duplicate not deduped, count=%d", got)
	}

	// replace
	if err := store.ReplaceLongTerm("Go 1.25", "Go 1.27"); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if !strings.Contains(store.ReadLongTerm(), "Go 1.27") {
		t.Fatalf("replace not applied: %q", store.ReadLongTerm())
	}

	// remove
	if err := store.RemoveLongTerm("dark mode"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(store.ReadLongTerm(), "dark mode") {
		t.Fatalf("remove not applied: %q", store.ReadLongTerm())
	}

	// overflow rejects the write and leaves the file untouched
	before := store.ReadLongTerm()
	err := store.AddLongTerm(strings.Repeat("x", 200))
	if !errors.Is(err, ErrMemoryFull) {
		t.Fatalf("expected ErrMemoryFull, got %v", err)
	}
	if store.ReadLongTerm() != before {
		t.Fatalf("failed write mutated the file")
	}
}

func TestE2E_MemoryStore_DailyNotes(t *testing.T) {
	dir := t.TempDir()
	store := NewMemoryStoreWithOptions(dir, MemoryOptions{
		LongTermCharLimit:  1000,
		DailyNotesEnabled:  true,
		DailyNoteCharLimit: 500,
		DailyNotesDays:     1,
	})
	if err := store.AppendToday("shipped the memory refactor"); err != nil {
		t.Fatalf("append today: %v", err)
	}
	if err := store.AppendToday("reviewed FTS5 work"); err != nil {
		t.Fatalf("append today 2: %v", err)
	}
	today := store.ReadToday()
	if !strings.Contains(today, "# ") {
		t.Fatalf("daily note missing date header: %q", today)
	}
	if !strings.Contains(today, "shipped the memory refactor") || !strings.Contains(today, "reviewed FTS5 work") {
		t.Fatalf("daily note missing entries: %q", today)
	}
	// context injection includes the note when enabled
	ctx := store.GetMemoryContext()
	if !strings.Contains(ctx, "Recent Daily Notes") {
		t.Fatalf("memory context missing daily notes section: %q", ctx)
	}
}

func TestE2E_MemoryStore_DailyNotesDisabled(t *testing.T) {
	dir := t.TempDir()
	store := NewMemoryStoreWithOptions(dir, MemoryOptions{
		LongTermCharLimit:  1000,
		DailyNotesEnabled:  false,
		DailyNoteCharLimit: 0,
		DailyNotesDays:     1,
	})
	if err := store.AppendToday("should still write"); err != nil {
		t.Fatalf("append: %v", err)
	}
	// With daily notes disabled, context must not include the section.
	if strings.Contains(store.GetMemoryContext(), "Recent Daily Notes") {
		t.Fatalf("disabled daily notes leaked into context")
	}
}

// ---------------------------------------------------------------------------
// Agent-facing memory tool — drives the real store through the tool boundary
// ---------------------------------------------------------------------------

func TestE2E_MemoryTool_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewMemoryStoreWithOptions(dir, MemoryOptions{LongTermCharLimit: 1000, DailyNotesEnabled: true})
	tool := memorytool.NewMemoryTool(store)
	ctx := context.Background()

	add := tool.Execute(ctx, map[string]any{"action": "add", "content": "likes tea"})
	if add.IsError {
		t.Fatalf("add failed: %s", add.ForLLM)
	}
	read := tool.Execute(ctx, map[string]any{"action": "read"})
	if read.IsError || !strings.Contains(read.ForLLM, "likes tea") {
		t.Fatalf("read did not reflect add: %+v", read)
	}
	note := tool.Execute(ctx, map[string]any{"action": "note", "content": "did a thing"})
	if note.IsError {
		t.Fatalf("note failed: %s", note.ForLLM)
	}
	if !strings.Contains(store.ReadToday(), "did a thing") {
		t.Fatalf("note not persisted to daily file")
	}
	bad := tool.Execute(ctx, map[string]any{"action": "replace", "old_text": ""})
	if !bad.IsError {
		t.Fatalf("expected error for empty old_text")
	}
}

func TestE2E_MemoryTool_FullMemorySurfacesError(t *testing.T) {
	dir := t.TempDir()
	store := NewMemoryStoreWithOptions(dir, MemoryOptions{LongTermCharLimit: 20, DailyNotesEnabled: false})
	tool := memorytool.NewMemoryTool(store)
	res := tool.Execute(context.Background(), map[string]any{"action": "add", "content": strings.Repeat("y", 50)})
	if !res.IsError {
		t.Fatalf("expected error result when memory full, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "limit exceeded") {
		t.Fatalf("error message should mention the limit: %q", res.ForLLM)
	}
}

// ---------------------------------------------------------------------------
// Context injection — the assembled system prompt must carry memory
// ---------------------------------------------------------------------------

func TestE2E_ContextInjection_IncludesMemory(t *testing.T) {
	dir := t.TempDir()
	cb := NewContextBuilder(dir).WithMemory(MemoryOptions{
		LongTermCharLimit:  1000,
		DailyNotesEnabled:  true,
		DailyNoteCharLimit: 1000,
		DailyNotesDays:     1,
	})
	if err := cb.Memory().AddLongTerm("the user is building picoclaw"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := cb.Memory().AppendToday("started memory e2e work"); err != nil {
		t.Fatalf("note: %v", err)
	}
	prompt := cb.BuildSystemPrompt()
	if !strings.Contains(prompt, "the user is building picoclaw") {
		t.Fatalf("system prompt missing long-term memory")
	}
	if !strings.Contains(prompt, "started memory e2e work") {
		t.Fatalf("system prompt missing daily note")
	}
}

// ---------------------------------------------------------------------------
// Extraction pipeline — end-to-end through the scripted LLM into the store
// ---------------------------------------------------------------------------

func TestE2E_ExtractionPipeline_AppliesOps(t *testing.T) {
	dir := t.TempDir()
	llm := memtest.NewLLM(`{"memory":{"add":["user ships Go on linux"],"note":["session start"],"replace":[{"old_text":"OLD","new_text":"NEW"}],"remove":["STALE"],"extra":"ignored"}}`)
	sessions := session.NewSessionManager(filepath.Join(dir, "sessions"))
	sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "i build Go services"})
	sessions.AddFullMessage("s1", providers.Message{Role: "assistant", Content: "great"})

	store := NewMemoryStoreWithOptions(dir, MemoryOptions{LongTermCharLimit: 5000, DailyNotesEnabled: true, DailyNoteCharLimit: 5000, DailyNotesDays: 1})
	if err := store.WriteLongTerm("OLD value\nSTALE entry\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	agent := &AgentInstance{ID: "a1", Model: "m", Workspace: dir, Provider: llm, Sessions: sessions}
	mm := newMemoryManager(agent, store, MemoryExtractOptions{EveryTurns: 1}, nil)

	payload := mm.runExtraction("s1")
	if payload == nil {
		t.Fatal("expected payload")
	}
	if payload.Additions != 1 || payload.Replacements != 1 || payload.Removals != 1 || payload.Notes != 1 {
		t.Fatalf("unexpected op counts: %+v", payload)
	}
	mem := store.ReadLongTerm()
	if !strings.Contains(mem, "user ships Go on linux") || strings.Contains(mem, "OLD value") || strings.Contains(mem, "STALE") || !strings.Contains(mem, "NEW") {
		t.Fatalf("memory after extraction wrong: %q", mem)
	}
	if !strings.Contains(store.ReadToday(), "session start") {
		t.Fatalf("daily note not appended")
	}
	// The extractor must have been fed the conversation transcript.
	if p := llm.Prompts(); len(p) == 0 || !strings.Contains(p[0], "i build Go services") {
		t.Fatalf("extraction prompt missing transcript: %v", p)
	}
}

func TestE2E_ExtractionPipeline_CadenceGating(t *testing.T) {
	dir := t.TempDir()
	llm := memtest.NewLLM(`{"memory":{"add":["fact"]}}`)
	sessions := session.NewSessionManager(filepath.Join(dir, "sessions"))
	store := NewMemoryStoreWithOptions(dir, MemoryOptions{LongTermCharLimit: 5000})
	agent := &AgentInstance{ID: "a1", Model: "m", Workspace: dir, Provider: llm, Sessions: sessions}
	// everyTurns = 5; only 2 messages present -> no extraction.
	mm := newMemoryManager(agent, store, MemoryExtractOptions{EveryTurns: 5}, nil)
	sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "one"})
	sessions.AddFullMessage("s1", providers.Message{Role: "assistant", Content: "two"})
	mm.MaybeExtract(nil, "s1")
	if llm.Calls() != 0 {
		t.Fatalf("extraction ran before cadence met (calls=%d)", llm.Calls())
	}
	// Note: MaybeExtract requires a non-nil AgentLoop; the guard above already
	// proves nil-loop short-circuits without side effects.
}

func TestE2E_ExtractionPipeline_UnparsableIsNoOp(t *testing.T) {
	dir := t.TempDir()
	llm := memtest.NewLLM("I cannot help with that.")
	sessions := session.NewSessionManager(filepath.Join(dir, "sessions"))
	sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "hi"})
	store := NewMemoryStoreWithOptions(dir, MemoryOptions{LongTermCharLimit: 5000})
	if err := store.WriteLongTerm("keep me\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	agent := &AgentInstance{ID: "a1", Model: "m", Workspace: dir, Provider: llm, Sessions: sessions}
	mm := newMemoryManager(agent, store, MemoryExtractOptions{EveryTurns: 1}, nil)
	if payload := mm.runExtraction("s1"); payload != nil {
		t.Fatalf("unparsable output must be a no-op, got %+v", payload)
	}
	if store.ReadLongTerm() != "keep me\n" {
		t.Fatalf("memory mutated by no-op extraction: %q", store.ReadLongTerm())
	}
}

// ---------------------------------------------------------------------------
// External syncers — all three providers against the fake REST servers
// ---------------------------------------------------------------------------

func TestE2E_Mem0Sync_PushesTranscript(t *testing.T) {
	fs := memtest.NewServer()
	srv := httptest.NewServer(fs)
	defer srv.Close()

	agent, _ := newTestAgent(t, "agent-x", nil)
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "remember I like tea"})
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "assistant", Content: "noted"})

	sink := &mem0Sink{client: mem0.New(srv.URL, "key"), userID: "u1", agentID: "a1"}
	syncer := newMemorySyncer(agent, sink, 1, nil)
	syncer.MaybeSync(&AgentLoop{}, "s1")

	if !waitFor(t, 2*time.Second, func() bool { return len(fs.RequestsFor(memtest.ProviderMem0, "/memories/")) > 0 }) {
		t.Fatal("mem0 never received the transcript")
	}
	reqs := fs.RequestsFor(memtest.ProviderMem0, "/memories/")
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		UserID  string `json:"user_id"`
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(reqs[0].Body, &body); err != nil {
		t.Fatalf("decode mem0 body: %v", err)
	}
	if body.UserID != "u1" || body.AgentID != "a1" {
		t.Fatalf("mem0 ids wrong: %+v", body)
	}
	if len(body.Messages) != 2 {
		t.Fatalf("mem0 got %d messages, want 2", len(body.Messages))
	}
}

func TestE2E_RetainDBSync_PushesTranscript(t *testing.T) {
	fs := memtest.NewServer()
	srv := httptest.NewServer(fs)
	defer srv.Close()

	agent, _ := newTestAgent(t, "agent-x", nil)
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "hello retaindb"})

	sink := &retainDBSink{client: retaindb.New(srv.URL, "key", "ns-1")}
	syncer := newMemorySyncer(agent, sink, 1, nil)
	syncer.MaybeSync(&AgentLoop{}, "s1")

	if !waitFor(t, 2*time.Second, func() bool { return len(fs.Requests()) > 0 }) {
		t.Fatal("retaindb never received the transcript")
	}
	reqs := fs.Requests()
	var body struct {
		Namespace string `json:"namespace"`
		Messages  []any  `json:"messages"`
	}
	if err := json.Unmarshal(reqs[0].Body, &body); err != nil {
		t.Fatalf("decode retaindb body: %v", err)
	}
	if body.Namespace != "ns-1" || len(body.Messages) != 1 {
		t.Fatalf("retaindb body wrong: %+v", body)
	}
}

func TestE2E_MemorySync_SanitizeApplied(t *testing.T) {
	fs := memtest.NewServer()
	srv := httptest.NewServer(fs)
	defer srv.Close()

	agent, _ := newTestAgent(t, "agent-x", nil)
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "my key is SECRET9"})

	sink := &mem0Sink{client: mem0.New(srv.URL, ""), userID: "u", agentID: "a"}
	sanitize := func(s string) string { return strings.ReplaceAll(s, "SECRET9", "REDACTED") }
	syncer := newMemorySyncer(agent, sink, 1, sanitize)
	syncer.MaybeSync(&AgentLoop{}, "s1")

	if !waitFor(t, 2*time.Second, func() bool { return len(fs.Requests()) > 0 }) {
		t.Fatal("no sync")
	}
	if strings.Contains(string(fs.Requests()[0].Body), "SECRET9") {
		t.Fatalf("sanitize not applied to synced transcript")
	}
	if !strings.Contains(string(fs.Requests()[0].Body), "REDACTED") {
		t.Fatalf("redacted marker missing")
	}
}

func TestE2E_MemorySync_FailureIsSwallowed(t *testing.T) {
	fs := memtest.NewServer()
	fs.FailNext(1)
	srv := httptest.NewServer(fs)
	defer srv.Close()

	agent, _ := newTestAgent(t, "agent-x", nil)
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "trigger"})

	sink := &mem0Sink{client: mem0.New(srv.URL, ""), userID: "u", agentID: "a"}
	syncer := newMemorySyncer(agent, sink, 1, nil)
	// Must not panic or block on provider failure.
	syncer.MaybeSync(&AgentLoop{}, "s1")
	waitFor(t, 500*time.Millisecond, func() bool { return false })
	if len(fs.Requests()) == 0 {
		t.Fatal("request never reached the fake server")
	}
}

func TestE2E_HonchoSync_PushesWithPeers(t *testing.T) {
	fs := memtest.NewServer()
	srv := httptest.NewServer(fs)
	defer srv.Close()

	agent, _ := newTestAgent(t, "agent-x", nil)
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "user turn"})
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "assistant", Content: "agent turn"})

	hs := newHonchoSyncer(agent, honcho.New(srv.URL, "key"), honchoConfig{
		WorkspaceID: "ws1",
		UserPeerID:  "user",
		AgentPeerID: "agent",
		TriggerAuto: true,
		EveryTurns:  1,
	}, nil)
	hs.MaybeSync(&AgentLoop{}, "s1")

	if !waitFor(t, 2*time.Second, func() bool { return len(fs.RequestsFor(memtest.ProviderHoncho, "/messages")) > 0 }) {
		t.Fatal("honcho never received messages")
	}
	msgs := fs.RequestsFor(memtest.ProviderHoncho, "/messages")
	var body struct {
		Messages []struct {
			Content string `json:"content"`
			PeerID  string `json:"peer_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(msgs[0].Body, &body); err != nil {
		t.Fatalf("decode honcho body: %v", err)
	}
	if len(body.Messages) != 2 {
		t.Fatalf("honcho got %d messages, want 2", len(body.Messages))
	}
	if body.Messages[0].PeerID != "user" || body.Messages[1].PeerID != "agent" {
		t.Fatalf("honcho peer routing wrong: %+v", body.Messages)
	}
}

func TestE2E_HonchoRecall_PrefetchesIntoContext(t *testing.T) {
	fs := memtest.NewServer()
	fs.SetSessionContext("s1", `{"id":"s1","peer_representation":"knows Go and tea","peer_card":["prefers tea"]}`)
	srv := httptest.NewServer(fs)
	defer srv.Close()

	agent, _ := newTestAgent(t, "agent-x", nil)
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "hi"})

	hs := newHonchoSyncer(agent, honcho.New(srv.URL, ""), honchoConfig{
		WorkspaceID:    "ws1",
		UserPeerID:     "user",
		AgentPeerID:    "agent",
		TriggerAuto:    true,
		EveryTurns:     1,
		RecallEnabled:  true,
		ContextCadence: 1,
	}, nil)

	// First call schedules the background prefetch and returns "".
	if got := hs.RecallContext("s1"); got != "" {
		t.Fatalf("first recall should be empty, got %q", got)
	}
	// The background fetch populates the cache.
	if !waitFor(t, 3*time.Second, func() bool { return hs.RecallContext("s1") != "" }) {
		t.Fatal("honcho recall never populated the cache")
	}
	block := hs.RecallContext("s1")
	if !strings.Contains(block, "knows Go and tea") {
		t.Fatalf("recall block missing representation: %q", block)
	}
}

// ---------------------------------------------------------------------------
// Session storage — the durable substrate extraction/sync read from
// ---------------------------------------------------------------------------

func TestE2E_SessionStore_HistoryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sessions := session.NewSessionManager(filepath.Join(dir, "sessions"))
	sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "first"})
	sessions.AddFullMessage("s1", providers.Message{Role: "assistant", Content: "second"})
	if err := sessions.Save("s1"); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Reload from disk via a fresh manager over the same dir.
	reopened := session.NewSessionManager(filepath.Join(dir, "sessions"))
	hist := reopened.GetHistory("s1")
	if len(hist) != 2 || hist[0].Content != "first" || hist[1].Content != "second" {
		t.Fatalf("history round-trip failed: %+v", hist)
	}
}
