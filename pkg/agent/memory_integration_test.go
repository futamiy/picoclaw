//go:build integration

package agent

// Cross-container integration test for the external memory syncers. Unlike the
// in-process E2E tests (which use httptest), this drives the real syncers
// against memory-shim containers over the Docker network, proving the full
// client -> wire -> server path works outside a single process.
//
// It is gated on the existing `integration` build tag and skipped unless the
// provider endpoints are provided by the compose suite.

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/honcho"
	"github.com/sipeed/picoclaw/pkg/mem0"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/retaindb"
)

func providerURLs(t *testing.T) (honchoURL, mem0URL, retaindbURL string) {
	t.Helper()
	honchoURL = strings.TrimSpace(os.Getenv("PICOCLAW_MEMTEST_HONCHO_URL"))
	mem0URL = strings.TrimSpace(os.Getenv("PICOCLAW_MEMTEST_MEM0_URL"))
	retaindbURL = strings.TrimSpace(os.Getenv("PICOCLAW_MEMTEST_RETAINDB_URL"))
	if honchoURL == "" || mem0URL == "" || retaindbURL == "" {
		t.Skip("PICOCLAW_MEMTEST_*_URL not set; run via the integration suite")
	}
	return
}

// recorded fetches the shim's recorded requests over the container network.
func recorded(t *testing.T, base string) []map[string]any {
	t.Helper()
	resp, err := http.Get(base + "/__recorded")
	if err != nil {
		t.Fatalf("GET %s/__recorded: %v", base, err)
	}
	defer resp.Body.Close()
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode recorded: %v", err)
	}
	return out
}

func waitForRecorded(t *testing.T, base string, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := recorded(t, base); len(got) >= want {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
	got := recorded(t, base)
	t.Fatalf("expected >= %d recorded requests at %s, got %d", want, base, len(got))
	return got
}

func TestMemoryE2EIntegration_Mem0CrossContainer(t *testing.T) {
	_, mem0URL, _ := providerURLs(t)

	agent, _ := newTestAgent(t, "integration-agent", nil)
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "cross-container mem0"})
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "assistant", Content: "ack"})

	sink := &mem0Sink{client: mem0.New(mem0URL, "key"), userID: "iu", agentID: "ia"}
	syncer := newMemorySyncer(agent, sink, 1, nil)
	syncer.MaybeSync(&AgentLoop{}, "s1")

	got := waitForRecorded(t, mem0URL, 1)
	if !strings.Contains(string(mustJSON(t, got[0])), "cross-container mem0") {
		t.Fatalf("mem0 shim did not record the transcript: %s", mustJSON(t, got[0]))
	}
}

func TestMemoryE2EIntegration_RetainDBCrossContainer(t *testing.T) {
	_, _, retaindbURL := providerURLs(t)

	agent, _ := newTestAgent(t, "integration-agent", nil)
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "cross-container retaindb"})

	sink := &retainDBSink{client: retaindb.New(retaindbURL, "key", "ns")}
	syncer := newMemorySyncer(agent, sink, 1, nil)
	syncer.MaybeSync(&AgentLoop{}, "s1")

	got := waitForRecorded(t, retaindbURL, 1)
	if !strings.Contains(string(mustJSON(t, got[0])), "cross-container retaindb") {
		t.Fatalf("retaindb shim did not record the transcript: %s", mustJSON(t, got[0]))
	}
}

func TestMemoryE2EIntegration_HonchoCrossContainer(t *testing.T) {
	honchoURL, _, _ := providerURLs(t)

	agent, _ := newTestAgent(t, "integration-agent", nil)
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "user", Content: "cross-container honcho"})
	agent.Sessions.AddFullMessage("s1", providers.Message{Role: "assistant", Content: "ack"})

	hs := newHonchoSyncer(agent, honcho.New(honchoURL, "key"), honchoConfig{
		WorkspaceID: "ws",
		UserPeerID:  "user",
		AgentPeerID: "agent",
		TriggerAuto: true,
		EveryTurns:  1,
	}, nil)
	hs.MaybeSync(&AgentLoop{}, "s1")

	// Honcho does Ensure (workspace + session) then AddMessages: >= 3 posts.
	got := waitForRecorded(t, honchoURL, 3)
	var sawMessages bool
	for _, r := range got {
		path, _ := r["path"].(string)
		if strings.HasSuffix(path, "/messages") {
			sawMessages = true
			if !strings.Contains(string(mustJSON(t, r)), "cross-container honcho") {
				t.Fatalf("honcho messages body missing transcript: %s", mustJSON(t, r))
			}
		}
	}
	if !sawMessages {
		t.Fatalf("honcho shim never saw a /messages request: %s", mustJSON(t, got))
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
