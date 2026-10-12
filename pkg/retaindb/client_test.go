package retaindb

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

	c := New(srv.URL, "secret", "ns1")
	err := c.AddMessages(context.Background(), []Message{
		{Role: "user", Content: "hi"},
	})
	if err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	if gotPath != memoriesPath {
		t.Fatalf("path = %q, want %q", gotPath, memoriesPath)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("auth = %q, want Bearer secret", gotAuth)
	}
	if gotBody["namespace"] != "ns1" {
		t.Fatalf("namespace = %+v", gotBody["namespace"])
	}
}

func TestClient_AddMessages_EmptyBaseURL(t *testing.T) {
	c := New("", "", "")
	if err := c.AddMessages(context.Background(), []Message{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("expected error when base url is empty")
	}
}
