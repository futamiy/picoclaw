package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// TestLegacyClear_ClearsSummary verifies that /clear drops live history AND
// the running context summary, returning the session to a fully cleared state
// (matching the original pre-summary-preservation behavior).
func TestLegacyClear_ClearsSummary(t *testing.T) {
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
	if s := agent.Sessions.GetSummary(sessionKey); s != "" {
		t.Fatalf("summary after clear = %q, want empty", s)
	}
}
