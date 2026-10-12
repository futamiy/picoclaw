package honcho

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetSessionContext(t *testing.T) {
	var gotPath, gotQuery string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"s1","summary":{"content":"prior chat"},"peer_representation":"likes go"}`))
	}))
	defer s.Close()

	c := New(s.URL, "tok")
	sc, err := c.GetSessionContext(context.Background(), "ws", "s1", SessionContextOptions{
		Tokens:     500,
		Summary:    true,
		PeerTarget: "user",
	})
	if err != nil {
		t.Fatalf("GetSessionContext: %v", err)
	}
	if gotPath != "GET /v3/workspaces/ws/sessions/s1/context" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "tokens=500") || !strings.Contains(gotQuery, "peer_target=user") {
		t.Errorf("query = %q", gotQuery)
	}
	if sc.Summary == nil || sc.Summary.Content != "prior chat" {
		t.Errorf("summary = %+v", sc.Summary)
	}
	if sc.PeerRepr != "likes go" {
		t.Errorf("repr = %q", sc.PeerRepr)
	}
}

func TestGetPeerContextAndCard(t *testing.T) {
	var gotPaths []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/context"):
			w.Write([]byte(`{"peer_id":"agent","target_id":"user","representation":"engineer","peer_card":["likes tea"]}`))
		default:
			w.Write([]byte(`{"peer_card":["likes tea"]}`))
		}
	}))
	defer s.Close()

	c := New(s.URL, "")
	pc, err := c.GetPeerContext(context.Background(), "ws", "agent", "user")
	if err != nil {
		t.Fatalf("GetPeerContext: %v", err)
	}
	if pc.Representation != "engineer" || len(pc.PeerCard) != 1 {
		t.Errorf("peer context = %+v", pc)
	}
	card, err := c.GetPeerCard(context.Background(), "ws", "agent", "user")
	if err != nil {
		t.Fatalf("GetPeerCard: %v", err)
	}
	if len(card.PeerCard) != 1 || card.PeerCard[0] != "likes tea" {
		t.Errorf("card = %+v", card)
	}
	if len(gotPaths) != 2 {
		t.Errorf("paths = %v", gotPaths)
	}
}

func TestSearchPeer(t *testing.T) {
	var gotBody map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`[{"content":"past note","peer_id":"user"}]`))
	}))
	defer s.Close()

	c := New(s.URL, "k")
	msgs, err := c.SearchPeer(context.Background(), "ws", "user", SearchOptions{Query: "note"})
	if err != nil {
		t.Fatalf("SearchPeer: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "past note" {
		t.Fatalf("msgs = %+v", msgs)
	}
	if gotBody["limit"].(float64) != 10 {
		t.Errorf("limit default not applied: %v", gotBody["limit"])
	}
}

func TestChatReturnsContent(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat") {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"content":"the answer"}`))
	}))
	defer s.Close()

	c := New(s.URL, "")
	got, err := c.Chat(context.Background(), "ws", "agent", ChatOptions{Query: "q"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got != "the answer" {
		t.Errorf("got %q", got)
	}
}

func TestCreateConclusion(t *testing.T) {
	var gotBody map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/workspaces/ws/conclusions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(201)
		w.Write([]byte(`[{"id":"c1","content":"likes go","observer_id":"agent","observed_id":"user"}]`))
	}))
	defer s.Close()

	c := New(s.URL, "")
	concl, err := c.CreateConclusion(context.Background(), "ws", ConclusionInput{
		Content:    "likes go",
		ObserverID: "agent",
		ObservedID: "user",
	})
	if err != nil {
		t.Fatalf("CreateConclusion: %v", err)
	}
	if concl == nil || concl.ID != "c1" {
		t.Fatalf("conclusion = %+v", concl)
	}
	if _, ok := gotBody["conclusions"].([]any); !ok {
		t.Errorf("body missing conclusions array: %v", gotBody)
	}
}

func TestCreateConclusionEmptyContent(t *testing.T) {
	c := New("http://unused", "")
	if _, err := c.CreateConclusion(context.Background(), "ws", ConclusionInput{ObserverID: "a", ObservedID: "b"}); err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestGetErrorStatus(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))
	defer s.Close()
	c := New(s.URL, "")
	if _, err := c.GetPeerContext(context.Background(), "ws", "a", "b"); err == nil {
		t.Fatal("expected error on 404")
	} else if !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v", err)
	}
}
