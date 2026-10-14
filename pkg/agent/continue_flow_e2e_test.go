// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// failingTransientProvider fails every Chat call with a transient error so the
// LLM retry loop is exhausted and the turn ends with a recoverable error.
type failingTransientProvider struct{}

func (m *failingTransientProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return nil, errors.New("stream error: unexpected EOF")
}

func (m *failingTransientProvider) GetDefaultModel() string { return "failing-transient-model" }

// inboundForTurn builds a normalized inbound message for a direct chat turn.
func inboundForTurn(channel, chatID, content string) bus.InboundMessage {
	return bus.NormalizeInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  channel,
			ChatID:   chatID,
			ChatType: "direct",
			SenderID: "e2e-user",
		},
		Content: content,
	})
}

// drainOutbound collects every outbound message published before the quiet
// window elapses.
func drainOutbound(t *testing.T, msgBus *bus.MessageBus) []bus.OutboundMessage {
	t.Helper()
	var out []bus.OutboundMessage
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-msgBus.OutboundChan():
			out = append(out, msg)
		case <-time.After(200 * time.Millisecond):
			return out
		case <-deadline:
			return out
		}
	}
}

// TestE2E_ContinueFlow_ToolLimitProducesActionableNonFinalMessage drives a real
// turn that exhausts max_tool_iterations through the inbound seam
// (runTurnWithSteering) and asserts the published outbound carries a continue
// action and is NOT marked final (so the channel manager delivers it as a
// fresh, ringing message instead of editing a placeholder).
func TestE2E_ContinueFlow_ToolLimitProducesActionableNonFinalMessage(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 1,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &toolLimitOnlyProvider{})
	al.RegisterTool(&toolLimitTestTool{})

	al.runTurnWithSteering(context.Background(), inboundForTurn("telegram", "chat1", "hello"))

	outs := drainOutbound(t, msgBus)
	if len(outs) != 1 {
		t.Fatalf("expected exactly 1 outbound, got %d: %#v", len(outs), outs)
	}
	out := outs[0]
	if out.Content != toolLimitResponse {
		t.Fatalf("outbound content = %q, want %q", out.Content, toolLimitResponse)
	}
	if len(out.Actions) != 1 || out.Actions[0].Type != bus.ActionContinue {
		t.Fatalf("expected a single continue action, got %#v", out.Actions)
	}
	if out.Context.Raw[metadataKeyOutboundKind] == outboundKindFinal {
		t.Fatal("actionable outbound must NOT be marked final")
	}
}

// TestE2E_ContinueFlow_ExhaustedTransientRetriesProducesActionableMessage drives
// a turn whose LLM provider fails every attempt with a transient error through
// the inbound seam and asserts the resulting error outbound carries a continue
// action and is not final.
func TestE2E_ContinueFlow_ExhaustedTransientRetriesProducesActionableMessage(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				MaxLLMRetries:     1,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &failingTransientProvider{})

	al.runTurnWithSteering(context.Background(), inboundForTurn("telegram", "chat1", "hello"))

	outs := drainOutbound(t, msgBus)
	if len(outs) != 1 {
		t.Fatalf("expected exactly 1 outbound, got %d: %#v", len(outs), outs)
	}
	out := outs[0]
	if out.Content == "" {
		t.Fatal("expected a formatted error message, got empty content")
	}
	if len(out.Actions) != 1 || out.Actions[0].Type != bus.ActionContinue {
		t.Fatalf("expected a single continue action, got %#v", out.Actions)
	}
	if out.Context.Raw[metadataKeyOutboundKind] == outboundKindFinal {
		t.Fatal("actionable error outbound must NOT be marked final")
	}
}

// TestE2E_ContinueFlow_DisabledEmitsNoAction verifies the opt-out path: with the
// continue button disabled, a tool-limit turn publishes no action and is marked
// final (regression guard for the default-on behavior).
func TestE2E_ContinueFlow_DisabledEmitsNoAction(t *testing.T) {
	disabled := false
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 1,
				ErrorActions:      config.ErrorActionsConfig{ContinueButton: &disabled},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &toolLimitOnlyProvider{})
	al.RegisterTool(&toolLimitTestTool{})

	al.runTurnWithSteering(context.Background(), inboundForTurn("telegram", "chat1", "hello"))

	outs := drainOutbound(t, msgBus)
	if len(outs) != 1 {
		t.Fatalf("expected exactly 1 outbound, got %d: %#v", len(outs), outs)
	}
	if len(outs[0].Actions) != 0 {
		t.Fatalf("expected no actions when disabled, got %#v", outs[0].Actions)
	}
	if outs[0].Context.Raw[metadataKeyOutboundKind] != outboundKindFinal {
		t.Fatal("non-actionable outbound should be marked final")
	}
}
