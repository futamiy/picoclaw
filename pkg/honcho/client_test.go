package honcho

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSanitizeID(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"agent-1", "agent-1"},
		{"Agent_1", "Agent_1"},
		{"a.b/c", "a_b_c"},
		{"!!!", "default"},
		{"", "default"},
	}
	for _, c := range cases {
		if got := SanitizeID(c.in); got != c.out {
			t.Errorf("SanitizeID(%q)=%q want %q", c.in, got, c.out)
		}
	}
}

func TestClientEnsureAndAddMessages(t *testing.T) {
	var gotPaths []string
	var gotBodies []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		var body any
		json.NewDecoder(r.Body).Decode(&body)
		b, _ := json.Marshal(body)
		gotBodies = append(gotBodies, string(b))
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer s.Close()
	c := New(s.URL, "token")
	ctx := context.Background()
	if err := c.Ensure(ctx, "ws", "s1", "user", "agent"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := c.AddMessages(ctx, "ws", "s1", []Message{{Content: "hi", PeerID: "user"}}); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	if len(gotPaths) != 3 {
		t.Fatalf("paths=%v want 3", gotPaths)
	}
}
