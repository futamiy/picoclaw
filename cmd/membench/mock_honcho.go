package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"
)

// mockHoncho is a tiny in-memory Honcho v3-compatible server used to run the
// benchmark offline. It implements only the endpoints the picoclaw honcho
// client touches, and answers context/search queries with a deterministic
// keyword-overlap retrieval so recall and token metrics stay meaningful
// without a live Honcho backend.
//
// Semantics deliberately mirror the schema in pkg/honcho/read.go:
//
//	GET  /v3/workspaces/{ws}/sessions/{sid}/context
//	GET  /v3/workspaces/{ws}/peers/{pid}/context
//	GET  /v3/workspaces/{ws}/peers/{pid}/card
//	POST /v3/workspaces/{ws}/peers/{pid}/search
//	POST /v3/workspaces/{ws}/peers/{pid}/chat
//	POST /v3/workspaces/{ws}/sessions/{sid}/messages
//	POST /v3/workspaces
//	POST /v3/workspaces/{ws}/sessions
type mockHoncho struct {
	mu       sync.Mutex
	sessions map[string]*mockSession // key: ws/sid
	// messages is the flat, session-scoped store keyed the same way.
	messages map[string][]mockMessage
}

type mockSession struct {
	peers map[string]struct{}
}

type mockMessage struct {
	content string
	peerID  string
	seq     int
}

// newMockHoncho builds a mock server. The caller owns the returned server and
// must Close it.
func newMockHoncho() *mockHoncho {
	return &mockHoncho{
		sessions: map[string]*mockSession{},
		messages: map[string][]mockMessage{},
	}
}

// start launches the mock over an httptest server and returns it.
func (m *mockHoncho) start() *httptest.Server {
	return httptest.NewServer(m)
}

func (m *mockHoncho) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Split into path segments after the /v3 prefix.
	segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(segs) < 2 || segs[0] != "v3" || segs[1] != "workspaces" {
		http.NotFound(w, r)
		return
	}

	switch {
	case r.Method == http.MethodPost && len(segs) == 2:
		m.bootstrapWorkspace(w)
	case r.Method == http.MethodPost && len(segs) == 4 && segs[3] == "sessions":
		m.bootstrapSession(w, r, segs[2])
	case r.Method == http.MethodPost && len(segs) == 6 && segs[3] == "sessions" &&
		segs[5] == "messages":
		m.addMessages(w, r, segs[4])
	case r.Method == http.MethodGet && len(segs) == 6 && segs[3] == "sessions" &&
		segs[5] == "context":
		m.sessionContext(w, r, segs[4])
	case r.Method == http.MethodGet && len(segs) == 6 && segs[3] == "peers" &&
		segs[5] == "context":
		m.peerContext(w, r, segs[2], segs[4])
	case r.Method == http.MethodGet && len(segs) == 6 && segs[3] == "peers" &&
		segs[5] == "card":
		m.peerCard(w, r, segs[2], segs[4])
	case r.Method == http.MethodPost && len(segs) == 6 && segs[3] == "peers" &&
		segs[5] == "search":
		m.search(w, r, segs[2], segs[4])
	case r.Method == http.MethodPost && len(segs) == 6 && segs[3] == "peers" &&
		segs[5] == "chat":
		m.chat(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (m *mockHoncho) bootstrapWorkspace(w http.ResponseWriter) {
	writeJSON(w, map[string]any{"id": "ok"})
}

func (m *mockHoncho) bootstrapSession(w http.ResponseWriter, r *http.Request, ws string) {
	var body struct {
		ID    string                     `json:"id"`
		Peers map[string]json.RawMessage `json:"peers"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	m.mu.Lock()
	defer m.mu.Unlock()
	key := ws + "/" + body.ID
	s, ok := m.sessions[key]
	if !ok {
		s = &mockSession{peers: map[string]struct{}{}}
		m.sessions[key] = s
	}
	for p := range body.Peers {
		s.peers[p] = struct{}{}
	}
	writeJSON(w, map[string]any{"id": body.ID})
}

func (m *mockHoncho) addMessages(w http.ResponseWriter, r *http.Request, sid string) {
	var body struct {
		Messages []struct {
			Content string `json:"content"`
			PeerID  string `json:"peer_id"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Session key is ws/sid; the workspace is embedded in the path already.
	segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	ws := segs[2]
	key := ws + "/" + sid

	m.mu.Lock()
	defer m.mu.Unlock()
	base := len(m.messages[key])
	for i, msg := range body.Messages {
		m.messages[key] = append(m.messages[key], mockMessage{
			content: msg.Content,
			peerID:  msg.PeerID,
			seq:     base + i,
		})
	}
	writeJSON(w, map[string]any{"count": len(body.Messages)})
}

func (m *mockHoncho) sessionContext(w http.ResponseWriter, r *http.Request, sid string) {
	segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	ws := segs[2]
	key := ws + "/" + sid
	tokens := atoiDefault(r.URL.Query().Get("tokens"), 0)

	m.mu.Lock()
	msgs := append([]mockMessage(nil), m.messages[key]...)
	m.mu.Unlock()

	// Return the most recent messages that fit the token budget (4 chars/token),
	// mirroring a bounded context window.
	out := make([]map[string]any, 0, len(msgs))
	used := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		t := len(msgs[i].content) / 4
		if tokens > 0 && used+t > tokens && len(out) > 0 {
			break
		}
		out = append(out, map[string]any{
			"id":         fmt.Sprintf("m%d", msgs[i].seq),
			"content":    msgs[i].content,
			"peer_id":    msgs[i].peerID,
			"session_id": sid,
			"created_at": time.Now().Format(time.RFC3339),
		})
		used += t
	}
	// Reverse to chronological order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}

	writeJSON(w, map[string]any{
		"id":       sid,
		"messages": out,
		"summary":  m.synthesizeSummary(msgs),
	})
}

// synthesizeSummary builds a deterministic session summary the way a real
// Honcho reasoning server would: it distills the session into a compact
// salience-ranked digest rather than replaying it chronologically. The bench
// relies on this because the runtime prefers the summary over raw messages
// (renderHonchoContext), and a chronological dump would push deep, salient
// facts past maxHonchoContextRunes.
//
// The heuristic marks a message as salient when it carries a distinctive token
// (an ALL-CAPS / dashed identifier such as a passphrase or code) or key
// declarative phrasing ("is", "named", "remember"). Salient lines are kept in
// order; filler is elided.
func (m *mockHoncho) synthesizeSummary(msgs []mockMessage) map[string]any {
	var salient []string
	for _, msg := range msgs {
		if isSalientContent(msg.content) {
			salient = append(salient, msg.content)
		}
	}
	if len(salient) == 0 {
		return nil
	}
	content := strings.Join(salient, "\n")
	return map[string]any{
		"content":      content,
		"message_id":   "s1",
		"summary_type": "session",
		"token_count":  len(content) / 4,
	}
}

// isSalientContent reports whether a message carries a distinctive, memorable
// fact worth distilling into the session summary.
func isSalientContent(content string) bool {
	// A dashed/ALL-CAPS identifier (e.g. ZEPHYR-QUARTZ-9917) is a strong signal.
	for _, field := range strings.Fields(content) {
		clean := strings.Trim(field, ".,;:!?\"'()")
		if len(clean) < 8 {
			continue
		}
		if strings.ContainsAny(clean, "-0123456789") && strings.ToUpper(clean) == clean &&
			strings.IndexFunc(clean, func(r rune) bool { return r >= 'A' && r <= 'Z' }) >= 0 {
			return true
		}
	}
	lower := strings.ToLower(content)
	for _, marker := range []string{"secret", "passphrase", "password", "remember", "named", "must never"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (m *mockHoncho) peerContext(w http.ResponseWriter, r *http.Request, ws, pid string) {
	target := r.URL.Query().Get("target")
	writeJSON(w, map[string]any{
		"peer_id":        pid,
		"target_id":      target,
		"representation": "",
	})
}

func (m *mockHoncho) peerCard(w http.ResponseWriter, r *http.Request, ws, pid string) {
	writeJSON(w, map[string]any{"peer_card": []string{}})
}

// search performs keyword-overlap retrieval across every session in the
// workspace and returns the best-scoring messages, ranked by overlap.
func (m *mockHoncho) search(w http.ResponseWriter, r *http.Request, ws, pid string) {
	var body struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Limit <= 0 {
		body.Limit = 10
	}

	m.mu.Lock()
	var all []mockMessage
	for key, msgs := range m.messages {
		if strings.HasPrefix(key, ws+"/") {
			all = append(all, msgs...)
		}
	}
	m.mu.Unlock()

	scored := scoreMessages(all, body.Query)
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })

	out := make([]map[string]any, 0, body.Limit)
	for _, sm := range scored {
		if len(out) >= body.Limit {
			break
		}
		if sm.score <= 0 {
			break
		}
		out = append(out, map[string]any{
			"id":      fmt.Sprintf("m%d", sm.msg.seq),
			"content": sm.msg.content,
			"peer_id": sm.msg.peerID,
		})
	}
	writeJSON(w, out)
}

func (m *mockHoncho) chat(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"content": ""})
}

// scoredMessage pairs a message with its keyword-overlap score.
type scoredMessage struct {
	msg   mockMessage
	score int
}

// scoreMessages ranks messages by how many distinct query keywords they contain.
func scoreMessages(msgs []mockMessage, query string) []scoredMessage {
	kws := ExtractKeywords(query)
	out := make([]scoredMessage, 0, len(msgs))
	for _, msg := range msgs {
		lower := strings.ToLower(msg.content)
		score := 0
		for _, kw := range kws {
			if strings.Contains(lower, kw) {
				score++
			}
		}
		out = append(out, scoredMessage{msg: msg, score: score})
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	return n
}
