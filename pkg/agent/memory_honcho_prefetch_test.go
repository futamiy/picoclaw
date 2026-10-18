package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/honcho"
	"github.com/sipeed/picoclaw/pkg/providers"
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
	if got := hs.RecallContext("s1", ""); got != "" {
		t.Errorf("disabled recall returned %q", got)
	}
}

func TestRecallContextEmptySessionKey(t *testing.T) {
	hs := &HonchoSyncer{recallEnabled: true, prefetch: map[string]*memoryPrefetchEntry{}}
	if got := hs.RecallContext("", ""); got != "" {
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
	if got := hs.RecallContext("s1", ""); got != "" {
		t.Fatalf("first call should be empty, got %q", got)
	}

	// Wait for the background fetch to populate the cache.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := hs.RecallContext("s1", ""); got != "" {
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
	got, _ := hs.fetchContext("s1", "", false)
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
	if got, _ := hs.fetchContext("s1", "", false); got != "" {
		t.Errorf("expected empty on errors, got %q", got)
	}
}

func TestFetchContextIncludesDialectic(t *testing.T) {
	var chatCalls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat"):
			chatCalls++
			w.Write([]byte(`{"content":"she builds Go services"}`))
		case strings.HasSuffix(r.URL.Path, "/context"):
			w.Write([]byte(`{"peer_representation":"engineer"}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer s.Close()

	hs := &HonchoSyncer{
		client:         honcho.New(s.URL, ""),
		workspaceID:    "ws",
		agentPeerID:    "agent1",
		userPeerID:     "user",
		dialecticLevel: "low",
	}
	got, used := hs.fetchContext("s1", "what do they do", true)
	if !used {
		t.Fatal("expected dialectic to be used")
	}
	if chatCalls != 1 {
		t.Fatalf("chat calls = %d, want 1", chatCalls)
	}
	for _, want := range []string{"engineer", "she builds Go services", "Reasoning about what matters here"} {
		if !strings.Contains(got, want) {
			t.Errorf("block missing %q: %q", want, got)
		}
	}

	// Without a dialectic request the chat endpoint must not be called.
	if _, used := hs.fetchContext("s1", "what do they do", false); used {
		t.Fatal("dialectic reported as used when disabled")
	}
	if chatCalls != 1 {
		t.Fatalf("chat calls after non-dialectic fetch = %d, want 1", chatCalls)
	}
}

func TestMaybePrefetchDialecticCadence(t *testing.T) {
	var chatCalls int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat"):
			atomic.AddInt32(&chatCalls, 1)
			w.Write([]byte(`{"content":"reasoned answer"}`))
		case strings.HasSuffix(r.URL.Path, "/context"):
			w.Write([]byte(`{"peer_representation":"engineer"}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer s.Close()

	agent, _ := newTestAgent(t, "agent1", nil)
	for i := 0; i < 4; i++ {
		agent.Sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "m"})
	}

	hs := newHonchoSyncer(agent, honcho.New(s.URL, ""), honchoConfig{
		WorkspaceID:      "ws",
		UserPeerID:       "user",
		AgentPeerID:      "agent1",
		RecallEnabled:    true,
		ContextCadence:   1,
		DialecticCadence: 2,
		DialecticLevel:   "low",
	}, nil)

	// First recall schedules a fetch; the dialectic is due on a fresh entry.
	if got := hs.RecallContext("s1", "what do they do?"); got != "" {
		t.Fatalf("first recall should be empty, got %q", got)
	}
	if !waitFor(t, 3*time.Second, func() bool { return hs.RecallContext("s1", "what do they do?") != "" }) {
		t.Fatal("prefetch never populated the cache")
	}
	if got := atomic.LoadInt32(&chatCalls); got != 1 {
		t.Fatalf("chat calls = %d, want 1 after first fetch", got)
	}

	// Same history: neither base context nor dialectic is due again.
	hs.RecallContext("s1", "what do they do?")
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&chatCalls); got != 1 {
		t.Fatalf("chat calls = %d, want 1 while cadence holds", got)
	}

	// Two more turns make the dialectic cadence due again.
	for i := 0; i < 2; i++ {
		agent.Sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "m"})
	}
	if !waitFor(t, 3*time.Second, func() bool {
		hs.RecallContext("s1", "what do they do?")
		return atomic.LoadInt32(&chatCalls) >= 2
	}) {
		t.Fatal("dialectic never refreshed after the cadence elapsed")
	}
}

func TestFetchDialecticSkipsWithoutQuery(t *testing.T) {
	var chatCalls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat") {
			chatCalls++
		}
		w.Write([]byte(`{}`))
	}))
	defer s.Close()

	hs := &HonchoSyncer{client: honcho.New(s.URL, ""), workspaceID: "ws", agentPeerID: "a", userPeerID: "u", dialecticLevel: "low"}
	if got := hs.fetchDialectic("s1", "   "); got != "" {
		t.Fatalf("expected empty answer, got %q", got)
	}
	if chatCalls != 0 {
		t.Fatalf("chat called %d times for an empty query", chatCalls)
	}
}
