package honchotools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/honcho"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// fakeBus captures published events for assertions.
type fakeBus struct {
	events []runtimeevents.Event
}

func (b *fakeBus) Publish(ctx context.Context, evt runtimeevents.Event) runtimeevents.PublishResult {
	b.events = append(b.events, evt)
	return runtimeevents.PublishResult{}
}

func (b *fakeBus) PublishNonBlocking(evt runtimeevents.Event) runtimeevents.PublishResult {
	b.events = append(b.events, evt)
	return runtimeevents.PublishResult{}
}

func (b *fakeBus) Channel() runtimeevents.EventChannel { return nil }
func (b *fakeBus) Close() error                        { return nil }
func (b *fakeBus) Stats() runtimeevents.Stats          { return runtimeevents.Stats{} }

func newTestTool(t *testing.T, handler http.HandlerFunc) (*Tool, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return NewTool(Config{
		Client:             honcho.New(s.URL, ""),
		WorkspaceID:        "ws",
		UserPeerID:         "user",
		AgentPeerID:        "agent",
		DefaultDialectical: "low",
	}), s
}

func sessionCtx() context.Context {
	return toolshared.WithToolSessionContext(context.Background(), "agent", "sess-1", nil)
}

func TestToolNameDescription(t *testing.T) {
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	if tool.Name() != "honcho" {
		t.Errorf("name = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("empty description")
	}
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("params type = %v", params["type"])
	}
}

func TestExecuteUnconfigured(t *testing.T) {
	tool := NewTool(Config{})
	res := tool.Execute(context.Background(), map[string]any{"action": "context"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error result, got %+v", res)
	}
}

func TestExecuteUnknownAction(t *testing.T) {
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "bogus"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error for unknown action, got %+v", res)
	}
}

func TestRunContext(t *testing.T) {
	var gotPath string
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"summary":{"content":"chat about go"},"peer_representation":"writes go"}`))
	})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "context"})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "chat about go") || !strings.Contains(res.ForLLM, "writes go") {
		t.Errorf("result = %q", res.ForLLM)
	}
	if !strings.Contains(gotPath, "/sessions/sess-1/context") {
		t.Errorf("path = %q", gotPath)
	}
}

func TestRunProfile(t *testing.T) {
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"representation":"a go dev","peer_card":["prefers tea"]}`))
	})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "profile"})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "a go dev") || !strings.Contains(res.ForLLM, "prefers tea") {
		t.Errorf("result = %q", res.ForLLM)
	}
}

func TestRunSearchRequiresQuery(t *testing.T) {
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "search"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error, got %+v", res)
	}
}

func TestRunSearch(t *testing.T) {
	var body map[string]any
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`[{"content":"used postgres","peer_id":"user"}]`))
	})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "search", "query": "db"})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "used postgres") {
		t.Errorf("result = %q", res.ForLLM)
	}
	if body["query"] != "db" {
		t.Errorf("query forwarded = %v", body["query"])
	}
}

func TestRunReasonRequiresQuery(t *testing.T) {
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "reason"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error, got %+v", res)
	}
}

func TestRunReason(t *testing.T) {
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":"she builds go services"}`))
	})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "reason", "query": "what do they do"})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "she builds go services") {
		t.Errorf("result = %q", res.ForLLM)
	}
}

func TestRunConcludeRequiresContent(t *testing.T) {
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "conclude"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error, got %+v", res)
	}
}

func TestRunConclude(t *testing.T) {
	var body map[string]any
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(201)
		w.Write([]byte(`[{"id":"c1","content":"prefers window seats"}]`))
	})
	res := tool.Execute(sessionCtx(), map[string]any{
		"action":  "conclude",
		"content": "prefers window seats",
	})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	concls, ok := body["conclusions"].([]any)
	if !ok || len(concls) != 1 {
		t.Fatalf("body = %v", body)
	}
	first := concls[0].(map[string]any)
	if first["content"] != "prefers window seats" {
		t.Errorf("content = %v", first["content"])
	}
	if first["observer_id"] != "agent" || first["observed_id"] != "user" {
		t.Errorf("peers = %v / %v", first["observer_id"], first["observed_id"])
	}
}

func TestRunReason_EmitsReasonedEvent(t *testing.T) {
	bus := &fakeBus{}
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":"she builds go services"}`))
	})
	tool.SetEventPublisher(bus)

	res := tool.Execute(sessionCtx(), map[string]any{"action": "reason", "query": "what do they do"})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(bus.events) != 1 {
		t.Fatalf("published %d events, want 1", len(bus.events))
	}
	evt := bus.events[0]
	if evt.Kind != runtimeevents.KindAgentMemoryHonchoReasoned {
		t.Errorf("kind = %q", evt.Kind)
	}
	if evt.Severity != runtimeevents.SeverityInfo {
		t.Errorf("severity = %q", evt.Severity)
	}
	payload, ok := evt.Payload.(HonchoReasonedPayload)
	if !ok {
		t.Fatalf("payload type = %T", evt.Payload)
	}
	if payload.Query != "what do they do" {
		t.Errorf("query = %q", payload.Query)
	}
	if payload.Level != "low" {
		t.Errorf("level = %q", payload.Level)
	}
	if payload.AnswerChars != len("she builds go services") {
		t.Errorf("answer chars = %d", payload.AnswerChars)
	}
	if payload.IsError {
		t.Error("payload marked as error")
	}
	if evt.Scope.SessionKey != "sess-1" || evt.Scope.AgentID != "agent" {
		t.Errorf("scope = %+v", evt.Scope)
	}
}

func TestRunReason_EmitsErrorEvent(t *testing.T) {
	bus := &fakeBus{}
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`boom`))
	})
	tool.SetEventPublisher(bus)

	res := tool.Execute(sessionCtx(), map[string]any{"action": "reason", "query": "q"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error result, got %+v", res)
	}
	if len(bus.events) != 1 {
		t.Fatalf("published %d events, want 1", len(bus.events))
	}
	evt := bus.events[0]
	if evt.Severity != runtimeevents.SeverityError {
		t.Errorf("severity = %q", evt.Severity)
	}
	payload, ok := evt.Payload.(HonchoReasonedPayload)
	if !ok {
		t.Fatalf("payload type = %T", evt.Payload)
	}
	if !payload.IsError || payload.ErrorMessage == "" {
		t.Errorf("payload = %+v", payload)
	}
}

func TestRunReason_NoBusNoPanic(t *testing.T) {
	tool, _ := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":"ok"}`))
	})
	// No SetEventPublisher call: must not panic.
	res := tool.Execute(sessionCtx(), map[string]any{"action": "reason", "query": "q"})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
}
