// Package memtest provides in-process fakes for the memory system's external
// dependencies: HTTP servers that impersonate the Honcho, Mem0 and RetainDB
// REST surfaces PicoClaw pushes to and reads from, plus a scripted LLM provider.
//
// It is intentionally free of build tags so the same code backs both the
// in-process E2E tests (via httptest) and the Docker integration suite (via a
// thin main() shim that wraps Handler in a real http.Server). Nothing here is
// imported by production code.
package memtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Provider identifies which external memory backend a request belongs to.
type Provider string

const (
	ProviderHoncho   Provider = "honcho"
	ProviderMem0     Provider = "mem0"
	ProviderRetainDB Provider = "retaindb"
)

// RecordedRequest captures a single non-GET request the fake server observed.
type RecordedRequest struct {
	Provider Provider
	Method   string
	Path     string
	Query    string
	Body     []byte
	Auth     string
}

// Server is an http.Handler that impersonates Honcho, Mem0 and RetainDB. It
// records every request it receives so tests can assert on exact payloads, and
// exposes configurable canned responses for the read endpoints.
//
// A single Server serves all three providers: the provider is inferred from the
// request path, so one instance can back every client under test.
type Server struct {
	mu sync.Mutex

	requests []RecordedRequest

	// failNext, when > 0, makes the next request return HTTP 500 and decrement.
	// Used to exercise the callers' error paths.
	failNext int
	// pingStatus overrides the status returned for GET / (0 = 200). Set to a
	// 5xx value to simulate an unreachable server for Ping checks.
	pingStatus int

	// Canned read responses. Keyed by session or peer id where relevant.
	// SessionContextJSON is returned for Honcho session-context fetches.
	sessionContextJSON map[string]string
	// PeerContextJSON is returned for Honcho peer-context fetches, keyed by peer id.
	peerContextJSON map[string]string
	// ChatContentJSON is returned for Honcho chat/dialectic calls.
	chatContentJSON string
	// mem0Memories is returned by Mem0 list/search read endpoints.
	mem0Memories []string
	// retainDBMemories is returned by RetainDB search; retainDBProfile by the
	// profile read endpoint.
	retainDBMemories []string
	retainDBProfile  []string
}

// NewServer returns an empty recording server.
func NewServer() *Server {
	return &Server{
		sessionContextJSON: make(map[string]string),
		peerContextJSON:    make(map[string]string),
	}
}

// FailNext makes the next n requests return HTTP 500.
func (s *Server) FailNext(n int) {
	s.mu.Lock()
	s.failNext = n
	s.mu.Unlock()
}

// SetPingStatus overrides the status code returned for GET / (0 resets to 200).
func (s *Server) SetPingStatus(code int) {
	s.mu.Lock()
	s.pingStatus = code
	s.mu.Unlock()
}

// SetSessionContext registers a canned Honcho session-context JSON body.
func (s *Server) SetSessionContext(sessionID, body string) {
	s.mu.Lock()
	s.sessionContextJSON[sessionID] = body
	s.mu.Unlock()
}

// SetPeerContext registers a canned Honcho peer-context JSON body keyed by peer id.
func (s *Server) SetPeerContext(peerID, body string) {
	s.mu.Lock()
	s.peerContextJSON[peerID] = body
	s.mu.Unlock()
}

// SetChatContent sets the content returned by Honcho chat/dialectic calls.
func (s *Server) SetChatContent(content string) {
	s.mu.Lock()
	s.chatContentJSON = content
	s.mu.Unlock()
}

// SetMem0Memories registers the memories returned by Mem0 read endpoints.
func (s *Server) SetMem0Memories(items ...string) {
	s.mu.Lock()
	s.mem0Memories = append([]string(nil), items...)
	s.mu.Unlock()
}

// SetRetainDBMemories registers the memories returned by RetainDB search and
// profile read endpoints.
func (s *Server) SetRetainDBMemories(items ...string) {
	s.mu.Lock()
	s.retainDBMemories = append([]string(nil), items...)
	s.retainDBProfile = append([]string(nil), items...)
	s.mu.Unlock()
}

// Requests returns a snapshot of everything recorded so far.
func (s *Server) Requests() []RecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RecordedRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

// Reset clears all recorded requests and canned read responses.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
	s.sessionContextJSON = make(map[string]string)
	s.peerContextJSON = make(map[string]string)
	s.chatContentJSON = ""
	s.mem0Memories = nil
	s.retainDBMemories = nil
	s.retainDBProfile = nil
	s.failNext = 0
	s.pingStatus = 0
}

// RequestsFor returns recorded requests matching the given provider and path
// suffix (empty suffix matches all for the provider).
func (s *Server) RequestsFor(p Provider, pathSuffix string) []RecordedRequest {
	var out []RecordedRequest
	for _, r := range s.Requests() {
		if r.Provider != p {
			continue
		}
		if pathSuffix == "" || strings.HasSuffix(r.Path, pathSuffix) {
			out = append(out, r)
		}
	}
	return out
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	auth := r.Header.Get("Authorization")
	if auth == "" {
		auth = r.Header.Get("X-Api-Key")
	}

	provider := inferProvider(r.URL.Path)

	s.mu.Lock()
	s.requests = append(s.requests, RecordedRequest{
		Provider: provider,
		Method:   r.Method,
		Path:     r.URL.Path,
		Query:    r.URL.RawQuery,
		Body:     body,
		Auth:     auth,
	})
	fail := s.failNext > 0
	if fail {
		s.failNext--
	}
	ping := s.pingStatus
	s.mu.Unlock()

	if fail {
		http.Error(w, "injected failure", http.StatusInternalServerError)
		return
	}

	// Ping endpoints: every provider probes GET / .
	if r.Method == http.MethodGet && r.URL.Path == "/" {
		if ping >= 500 {
			http.Error(w, "unavailable", ping)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	switch provider {
	case ProviderHoncho:
		s.serveHoncho(w, r)
	case ProviderMem0:
		s.serveMem0(w, r)
	case ProviderRetainDB:
		s.serveRetainDB(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveHoncho(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/messages"):
		writeJSON(w, map[string]any{"ok": true})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/workspaces"):
		writeJSON(w, map[string]any{"id": "ws"})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/sessions"):
		writeJSON(w, map[string]any{"id": "session"})
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/context"):
		s.serveHonchoContext(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/card"):
		writeJSON(w, map[string]any{"peer_card": []string{}})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/chat"):
		s.mu.Lock()
		content := s.chatContentJSON
		s.mu.Unlock()
		writeJSON(w, map[string]any{"content": content})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/conclusions"):
		writeJSON(w, []map[string]any{{"id": "c1", "content": "ok"}})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/search"):
		writeJSON(w, []any{})
	default:
		http.NotFound(w, r)
	}
}

// serveHonchoContext distinguishes session context (/sessions/{id}/context)
// from peer context (/peers/{id}/context) by path segment.
func (s *Server) serveHonchoContext(w http.ResponseWriter, r *http.Request) {
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// Expected: v3 workspaces {ws} sessions|peers {id} context
	if len(segments) >= 5 && segments[3] == "sessions" {
		sid := segments[4]
		s.mu.Lock()
		body, ok := s.sessionContextJSON[sid]
		s.mu.Unlock()
		if ok {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
			return
		}
		writeJSON(w, map[string]any{"id": sid})
		return
	}
	if len(segments) >= 5 && segments[3] == "peers" {
		pid := segments[4]
		s.mu.Lock()
		body, ok := s.peerContextJSON[pid]
		s.mu.Unlock()
		if ok {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
			return
		}
		writeJSON(w, map[string]any{"peer_id": pid, "representation": ""})
		return
	}
	writeJSON(w, map[string]any{})
}

func (s *Server) serveMem0(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/search/"):
		s.mu.Lock()
		items := append([]string(nil), s.mem0Memories...)
		s.mu.Unlock()
		writeJSON(w, map[string]any{"results": memoryObjects(items)})
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/memories/"):
		s.mu.Lock()
		items := append([]string(nil), s.mem0Memories...)
		s.mu.Unlock()
		writeJSON(w, map[string]any{"count": len(items), "results": memoryObjects(items)})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/memories/add/"):
		// AddMessages ingestion on the V3 additive pipeline.
		writeJSON(w, map[string]any{"event_id": "evt-1", "status": "PENDING"})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/memories/"):
		// AddMessages ingestion on the legacy /v1/memories/ route.
		writeJSON(w, map[string]any{"results": []any{}})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveRetainDB(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/memory/ingest/session"):
		writeJSON(w, map[string]any{"success": true, "mode": "sync"})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/v1/memory"):
		writeJSON(w, map[string]any{"success": true})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/memory/search"):
		s.mu.Lock()
		items := append([]string(nil), s.retainDBMemories...)
		s.mu.Unlock()
		results := make([]map[string]any, 0, len(items))
		for _, item := range items {
			results = append(results, map[string]any{
				"memory": map[string]any{"id": "m1", "content": item, "memory_type": "factual"},
				"score":  0.9,
			})
		}
		writeJSON(w, map[string]any{"results": results, "count": len(results)})
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/memory/profile/"):
		s.mu.Lock()
		items := append([]string(nil), s.retainDBProfile...)
		s.mu.Unlock()
		writeJSON(w, map[string]any{"memories": memoryObjects(items)})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/memories/"):
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.NotFound(w, r)
	}
}

// memoryObjects renders plain strings as the memory objects both providers
// return.
func memoryObjects(items []string) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for i, item := range items {
		out = append(out, map[string]any{
			"id":      fmt.Sprintf("mem-%d", i+1),
			"memory":  item,
			"content": item,
		})
	}
	return out
}

// inferProvider classifies a request path into a provider. Mem0 and RetainDB
// share the /v1/memories/ path, so callers that need exact attribution should
// run separate server instances per provider (as the E2E tests do).
func inferProvider(path string) Provider {
	switch {
	case strings.HasPrefix(path, "/v3/workspaces"):
		return ProviderHoncho
	case strings.HasPrefix(path, "/v1/memory"), strings.HasPrefix(path, "/v1/context"):
		return ProviderRetainDB
	case strings.HasPrefix(path, "/v3/memories"), strings.HasPrefix(path, "/v2/memories"):
		return ProviderMem0
	case strings.Contains(path, "/v1/memories"):
		// Ambiguous between mem0/retaindb add routes; default to mem0. Tests
		// that need RetainDB attribution use a dedicated server instance.
		return ProviderMem0
	default:
		return ProviderMem0
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
