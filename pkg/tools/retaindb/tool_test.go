package retaindbtools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/retaindb"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

func newTestTool(t *testing.T, handler http.HandlerFunc) *Tool {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return NewTool(Config{
		Client: retaindb.New(s.URL, "", "proj"),
		UserID: "u1",
	})
}

func sessionCtx() context.Context {
	return toolshared.WithToolSessionContext(context.Background(), "agent", "sess-1", nil)
}

func TestToolNameDescription(t *testing.T) {
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	if tool.Name() != "retaindb" {
		t.Errorf("name = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("empty description")
	}
}

func TestExecuteUnconfigured(t *testing.T) {
	tool := NewTool(Config{})
	res := tool.Execute(context.Background(), map[string]any{"action": "profile"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error result, got %+v", res)
	}
}

func TestExecuteUnknownAction(t *testing.T) {
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "bogus"})
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
		w.Write([]byte(`{"results":[{"memory":{"id":"m1","content":"prefers concise answers","memory_type":"preference"},"score":0.8}]}`))
	})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "search", "query": "reply style"})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "prefers concise answers") {
		t.Errorf("result = %q", res.ForLLM)
	}
	if gotPath != "/v1/memory/search" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["user_id"] != "u1" || gotBody["session_id"] != "sess-1" || gotBody["query"] != "reply style" {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestRunSearchRequiresQuery(t *testing.T) {
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "search"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error, got %+v", res)
	}
}

func TestRunProfile(t *testing.T) {
	var gotPath string
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"memories":[{"id":"m1","content":"ships Go on linux"}]}`))
	})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "profile"})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "ships Go on linux") {
		t.Errorf("result = %q", res.ForLLM)
	}
	if gotPath != "/v1/memory/profile/u1" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestRunRememberRequiresContent(t *testing.T) {
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {})
	res := tool.Execute(sessionCtx(), map[string]any{"action": "remember"})
	if res == nil || !res.IsError {
		t.Fatalf("expected error, got %+v", res)
	}
}

func TestRunRemember(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	tool := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	})
	res := tool.Execute(sessionCtx(), map[string]any{
		"action":      "remember",
		"content":     "user prefers dark mode",
		"memory_type": "preference",
		"importance":  float64(0.9),
	})
	if res == nil || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
	if gotPath != "/v1/memory" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["content"] != "user prefers dark mode" || gotBody["memory_type"] != "preference" {
		t.Errorf("body = %+v", gotBody)
	}
	if gotBody["importance"] != 0.9 || gotBody["session_id"] != "sess-1" {
		t.Errorf("body = %+v", gotBody)
	}
}
