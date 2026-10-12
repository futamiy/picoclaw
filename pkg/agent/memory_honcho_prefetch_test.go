package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/honcho"
)

func TestRenderHonchoContext(t *testing.T) {
	summary := &honcho.Summary{Content: "prior summary"}
	out := renderHonchoContext(summary, "likes go", []string{"prefers tea"})
	for _, want := range []string{"prior summary", "likes go", "prefers tea"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if renderHonchoContext(nil, "", nil) != "" {
		t.Error("expected empty block when nothing available")
	}
}

func TestTruncateHonchoRunes(t *testing.T) {
	if got := truncateHonchoRunes("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
	got := truncateHonchoRunes("abcdef", 3)
	if len([]rune(got)) != 4 || !strings.Contains(got, "…") {
		t.Errorf("truncation = %q", got)
	}
}

func TestRecallContextDisabled(t *testing.T) {
	hs := &HonchoSyncer{recallEnabled: false}
	if got := hs.RecallContext("s1"); got != "" {
		t.Errorf("disabled recall returned %q", got)
	}
}

func TestRecallContextEmptySessionKey(t *testing.T) {
	hs := &HonchoSyncer{recallEnabled: true, prefetch: map[string]*honchoPrefetchEntry{}}
	if got := hs.RecallContext(""); got != "" {
		t.Errorf("empty session key returned %q", got)
	}
}

func TestRecallContextServesCacheAndFetches(t *testing.T) {
	var hits int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte(`{"id":"s1","peer_representation":"knows Go"}`))
	}))
	defer s.Close()

	hs := newHonchoSyncer(
		&AgentInstance{ID: "agent1"},
		honcho.New(s.URL, ""),
		honchoConfig{
			WorkspaceID:    "ws",
			UserPeerID:     "user",
			AgentPeerID:    "agent1",
			RecallEnabled:  true,
			ContextCadence: 1,
		},
		nil,
	)

	// First call schedules a background fetch and returns "" immediately.
	if got := hs.RecallContext("s1"); got != "" {
		t.Fatalf("first call should be empty, got %q", got)
	}

	// Wait for the background fetch to populate the cache.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := hs.RecallContext("s1"); got != "" {
			if !strings.Contains(got, "knows Go") {
				t.Fatalf("cached block = %q", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("prefetch never populated the cache")
}

func TestFetchContextFallsBackToPeerContext(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sessions/s1/context") {
			w.WriteHeader(500)
			w.Write([]byte("boom"))
			return
		}
		w.Write([]byte(`{"peer_id":"agent1","target_id":"user","representation":"engineer"}`))
	}))
	defer s.Close()

	hs := &HonchoSyncer{
		client:      honcho.New(s.URL, ""),
		workspaceID: "ws",
		agentPeerID: "agent1",
		userPeerID:  "user",
	}
	got := hs.fetchContext("s1")
	if !strings.Contains(got, "engineer") {
		t.Fatalf("fallback block = %q", got)
	}
}

func TestFetchContextReturnsEmptyOnErrors(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer s.Close()
	hs := &HonchoSyncer{client: honcho.New(s.URL, ""), workspaceID: "ws", agentPeerID: "a", userPeerID: "u"}
	if got := hs.fetchContext("s1"); got != "" {
		t.Errorf("expected empty on errors, got %q", got)
	}
}
