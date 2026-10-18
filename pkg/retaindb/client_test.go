package retaindb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestClient_SearchMemories(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"results":[{"memory":{"id":"m1","content":"prefers concise answers","memory_type":"preference"},"score":0.82}],"count":1}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "secret", "proj")
	mems, err := c.SearchMemories(context.Background(), "u1", "s1", "how to reply?", 5)
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if gotPath != "/v1/memory/search" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["project"] != "proj" || gotBody["user_id"] != "u1" || gotBody["session_id"] != "s1" {
		t.Fatalf("body scope = %+v", gotBody)
	}
	if gotBody["query"] != "how to reply?" || gotBody["top_k"] != float64(5) {
		t.Fatalf("body = %+v", gotBody)
	}
	if len(mems) != 1 || mems[0].Content != "prefers concise answers" || mems[0].Score != 0.82 {
		t.Fatalf("mems = %+v", mems)
	}
}

func TestClient_GetProfile(t *testing.T) {
	var gotPath string
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"memories":[{"id":"m1","content":"ships Go on linux"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "secret", "proj")
	mems, err := c.GetProfile(context.Background(), "u 1")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if gotPath != "/v1/memory/profile/u 1" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "project=proj") || !strings.Contains(gotQuery, "include_pending=true") {
		t.Fatalf("query = %q", gotQuery)
	}
	if len(mems) != 1 || mems[0].Content != "ships Go on linux" {
		t.Fatalf("mems = %+v", mems)
	}
}

func TestClient_IngestSession(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "secret", "proj")
	err := c.IngestSession(context.Background(), "u1", "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("IngestSession: %v", err)
	}
	if gotPath != "/v1/memory/ingest/session" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["project"] != "proj" || gotBody["user_id"] != "u1" || gotBody["session_id"] != "s1" {
		t.Fatalf("scope = %+v", gotBody)
	}
	if gotBody["write_mode"] != "sync" {
		t.Fatalf("write_mode = %v", gotBody["write_mode"])
	}
}

func TestClient_IngestSession_FallsBackToLegacy(t *testing.T) {
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

	c := New(srv.URL, "", "ns")
	if err := c.IngestSession(context.Background(), "u1", "s1", []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("IngestSession fallback: %v", err)
	}
	if len(paths) != 2 || paths[1] != "/v1/memories/" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestClient_AddMemory(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "secret", "proj")
	err := c.AddMemory(context.Background(), "u1", "s1", "prefers concise answers", "preference", 0.9)
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if gotPath != "/v1/memory" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["content"] != "prefers concise answers" || gotBody["memory_type"] != "preference" {
		t.Fatalf("body = %+v", gotBody)
	}
	if gotBody["importance"] != 0.9 || gotBody["write_mode"] != "sync" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestClient_AddMemory_DefaultsAndValidation(t *testing.T) {
	c := New("http://127.0.0.1:1", "", "")
	if err := c.AddMemory(context.Background(), "u", "", "  ", "", 0); err == nil {
		t.Fatal("expected empty-content error")
	}
}

func TestClient_AddMessages_EmptyBaseURL(t *testing.T) {
	c := New("", "", "")
	if err := c.AddMessages(context.Background(), []Message{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("expected error when base url is empty")
	}
}
