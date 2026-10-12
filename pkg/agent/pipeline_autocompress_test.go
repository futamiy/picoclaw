package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// setupAutoCompressLoop builds an AgentLoop with a tracking context manager and
// a tiny context window so that a modest history exceeds the proactive budget.
func setupAutoCompressLoop(t *testing.T, autoCompress *bool) (*AgentLoop, *AgentInstance, *trackingContextManager, func()) {
	t.Helper()
	cleanup := resetCMRegistry()

	mock := &trackingContextManager{}
	factory := func(_ json.RawMessage, _ *AgentLoop) (ContextManager, error) {
		return mock, nil
	}
	if err := RegisterContextManager("autocompress_test_cm", factory); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         8,
				ContextWindow:     64,
				MaxToolIterations: 10,
				ContextManager:    "autocompress_test_cm",
				Experimental: config.ExperimentalConfig{
					AutoCompress: autoCompress,
				},
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &simpleMockProvider{response: "ok"})
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	return al, agent, mock, func() {
		al.Close()
		cleanup()
	}
}

func TestSetupTurn_ProactiveCompress_Enabled(t *testing.T) {
	enabled := true
	al, agent, mock, cleanup := setupAutoCompressLoop(t, &enabled)
	defer cleanup()

	// Long history so the proactive budget check trips.
	history := make([]providers.Message, 0, 20)
	for i := 0; i < 20; i++ {
		history = append(history, providers.Message{
			Role:    "user",
			Content: "this is a fairly long message used to blow past the tiny context window limit",
		})
		history = append(history, providers.Message{
			Role:    "assistant",
			Content: "this is a fairly long assistant reply used to blow past the tiny limit",
		})
	}
	agent.Sessions.SetHistory("sess-enabled", history)

	pipeline := NewPipeline(al)
	ts := newTurnState(agent, makeTestProcessOpts("sess-enabled"), turnEventScope{
		turnID:  "turn-1",
		context: newTurnContext(nil, nil, nil),
	})

	if _, err := pipeline.SetupTurn(context.Background(), ts); err != nil {
		t.Fatalf("SetupTurn failed: %v", err)
	}

	if got := mock.compactCalls.Load(); got == 0 {
		t.Fatalf("compactCalls = 0, want proactive compact when auto-compress enabled")
	}
	mock.mu.Lock()
	reason := ContextCompressReason("")
	if mock.lastCompact != nil {
		reason = mock.lastCompact.Reason
	}
	mock.mu.Unlock()
	if reason != ContextCompressReasonProactive {
		t.Fatalf("compact reason = %q, want %q", reason, ContextCompressReasonProactive)
	}
}

func TestSetupTurn_ProactiveCompress_Disabled(t *testing.T) {
	disabled := false
	al, agent, mock, cleanup := setupAutoCompressLoop(t, &disabled)
	defer cleanup()

	history := make([]providers.Message, 0, 20)
	for i := 0; i < 20; i++ {
		history = append(history, providers.Message{
			Role:    "user",
			Content: "this is a fairly long message used to blow past the tiny context window limit",
		})
		history = append(history, providers.Message{
			Role:    "assistant",
			Content: "this is a fairly long assistant reply used to blow past the tiny limit",
		})
	}
	agent.Sessions.SetHistory("sess-disabled", history)

	pipeline := NewPipeline(al)
	ts := newTurnState(agent, makeTestProcessOpts("sess-disabled"), turnEventScope{
		turnID:  "turn-1",
		context: newTurnContext(nil, nil, nil),
	})

	if _, err := pipeline.SetupTurn(context.Background(), ts); err != nil {
		t.Fatalf("SetupTurn failed: %v", err)
	}

	if got := mock.compactCalls.Load(); got != 0 {
		t.Fatalf("compactCalls = %d, want 0 when auto-compress disabled", got)
	}
}
