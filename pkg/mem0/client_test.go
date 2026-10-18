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
	if gotPath != "/v3/memories/add/" {
		t.Fatalf("path = %q, want /v3/memories/add/", gotPath)
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

func TestClient_AddMessages_FallsBackToLegacyRoute(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/v1/memories/" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	if err := c.AddMessages(context.Background(), "u1", "a1", []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("AddMessages fallback: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/v3/memories/add/" || paths[1] != "/v1/memories/" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestClient_SearchMemories(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"id":"m1","memory":"prefers tea","score":0.9}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "secret")
	mems, err := c.SearchMemories(context.Background(), "what do I like?", "u1", "a1", 5)
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if gotPath != "/v3/memories/search/" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["query"] != "what do I like?" || gotBody["top_k"] != float64(5) {
		t.Fatalf("body = %+v", gotBody)
	}
	filters, _ := gotBody["filters"].(map[string]any)
	if filters["user_id"] != "u1" || filters["agent_id"] != "a1" {
		t.Fatalf("filters = %+v", filters)
	}
	if len(mems) != 1 || mems[0].Text() != "prefers tea" || mems[0].Score != 0.9 {
		t.Fatalf("mems = %+v", mems)
	}
}

func TestClient_SearchMemories_FallsBackToOSSRoute(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`[{"id":"m1","memory":"uses Go"}]`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	mems, err := c.SearchMemories(context.Background(), "lang", "u1", "", 3)
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(mems) != 1 || mems[0].Memory != "uses Go" {
		t.Fatalf("mems = %+v", mems)
	}
	if len(paths) < 3 || paths[len(paths)-1] != "/search" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestClient_SearchMemories_FallsBackToV2Shape(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/memories/search/" {
			http.NotFound(w, r)
			return
		}
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"results":[{"id":"m1","memory":"uses Go"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	mems, err := c.SearchMemories(context.Background(), "lang", "u1", "a1", 3)
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if gotPath != "/v2/memories/search/" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["user_id"] != "u1" || gotBody["agent_id"] != "a1" || gotBody["limit"] != float64(3) {
		t.Fatalf("v2 body = %+v", gotBody)
	}
	if len(mems) != 1 || mems[0].Memory != "uses Go" {
		t.Fatalf("mems = %+v", mems)
	}
}

func TestClient_ListMemories(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"count":1,"results":[{"id":"m1","memory":"likes tea"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	mems, err := c.ListMemories(context.Background(), "u1", "", 4)
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if gotPath != "/v3/memories/" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["page"] != float64(1) || gotBody["page_size"] != float64(4) {
		t.Fatalf("body = %+v", gotBody)
	}
	if len(mems) != 1 || mems[0].Text() != "likes tea" {
		t.Fatalf("mems = %+v", mems)
	}
}

func TestClient_SearchMemories_AuthErrorStopsFallback(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, "bad")
	if _, err := c.SearchMemories(context.Background(), "q", "u", "", 5); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("expected one call before aborting, got %d", calls)
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
