package mem0

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_AddMessages(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "secret")
	err := c.AddMessages(context.Background(), "u1", "a1", []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	if gotPath != "/v1/memories/" {
		t.Fatalf("path = %q, want /v1/memories/", gotPath)
	}
	if gotAuth != "Token secret" {
		t.Fatalf("auth = %q, want Token secret", gotAuth)
	}
	if gotBody["user_id"] != "u1" || gotBody["agent_id"] != "a1" {
		t.Fatalf("body ids = %+v", gotBody)
	}
	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages = %+v", gotBody["messages"])
	}
}

func TestClient_AddMessages_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	if err := c.AddMessages(context.Background(), "", "", []Message{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("expected error on 401")
	}
}
