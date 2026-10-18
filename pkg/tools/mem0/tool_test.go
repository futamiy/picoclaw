package mem0tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/mem0"
)

func newTestTool(t *testing.T, handler http.HandlerFunc) *Tool {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return NewTool(Config{
		Client:  mem0.New(s.URL, ""),
		UserID:  "u1",
		AgentID: "a1",
	})
}

func TestToolNameDescription(t *testing.T) {
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	if tool.Name() != "mem0" {
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
	res := tool.Execute(context.Background(), map[string]any{"action": "recent"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error result, got %+v", res)
	}
}

func TestExecuteUnknownAction(t *testing.T) {
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	res := tool.Execute(context.Background(), map[string]any{"action": "bogus"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error for unknown action, got %+v", res)
	}
}

func TestRunSearch(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"results":[{"id":"m1","memory":"prefers tea"}]}`))
	})
	res := tool.Execute(context.Background(), map[string]any{"action": "search", "query": "drink", "limit": 5})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "prefers tea") {
		t.Errorf("result = %q", res.ForLLM)
	}
	if gotPath != "/v3/memories/search/" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["query"] != "drink" || gotBody["top_k"] != float64(5) {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestRunSearchRequiresQuery(t *testing.T) {
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	res := tool.Execute(context.Background(), map[string]any{"action": "search"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error, got %+v", res)
	}
}

func TestRunRecent(t *testing.T) {
	var gotPath string
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"count":1,"results":[{"id":"m1","memory":"likes tea"}]}`))
	})
	res := tool.Execute(context.Background(), map[string]any{"action": "recent"})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "likes tea") {
		t.Errorf("result = %q", res.ForLLM)
	}
	if gotPath != "/v3/memories/" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestRunAddRequiresContent(t *testing.T) {
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	res := tool.Execute(context.Background(), map[string]any{"action": "add"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error, got %+v", res)
	}
}

func TestRunAdd(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"event_id":"e1","status":"PENDING"}`))
	})
	res := tool.Execute(context.Background(), map[string]any{"action": "add", "content": "prefers window seats"})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	if gotPath != "/v3/memories/add/" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["user_id"] != "u1" || gotBody["agent_id"] != "a1" {
		t.Errorf("body scopes = %+v", gotBody)
	}
	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages = %+v", gotBody["messages"])
	}
	first := msgs[0].(map[string]any)
	if first["content"] != "prefers window seats" || first["role"] != "user" {
		t.Errorf("message = %+v", first)
	}
}
