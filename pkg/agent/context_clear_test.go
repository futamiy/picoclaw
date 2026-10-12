package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// TestLegacyClear_PreservesSummary verifies that /clear drops live history
// (so token usage falls to ~0) while retaining the running context summary so
// that memory remains usable after the clear.
func TestLegacyClear_PreservesSummary(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	sessionKey := "clear-session"
	agent.Sessions.SetHistory(sessionKey, []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	})
	agent.Sessions.SetSummary(sessionKey, "running summary of earlier conversation")

	if err := al.contextManager.Clear(context.Background(), sessionKey); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	if h := agent.Sessions.GetHistory(sessionKey); len(h) != 0 {
		t.Fatalf("history after clear = %d messages, want 0", len(h))
	}
	if s := agent.Sessions.GetSummary(sessionKey); s != "running summary of earlier conversation" {
		t.Fatalf("summary after clear = %q, want preserved summary", s)
	}
}
