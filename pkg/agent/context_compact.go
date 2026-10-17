package agent

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// CompactSession forces synchronous summarization of a session's context (the
// /compact command) and reports before/after message and token counts. It
// resolves the session's owning agent so routed (non-default) sessions are
// handled too.
func (al *AgentLoop) CompactSession(ctx context.Context, sessionKey string) (commands.CompactResult, error) {
	if al.contextManager == nil {
		return commands.CompactResult{}, fmt.Errorf("context manager not initialized")
	}
	agent := al.agentForSession(sessionKey)
	if agent == nil {
		agent = al.registry.GetDefaultAgent()
	}
	if agent == nil || agent.Sessions == nil {
		return commands.CompactResult{}, fmt.Errorf("sessions not initialized")
	}

	result := commands.CompactResult{
		BeforeMessages: len(agent.Sessions.GetHistory(sessionKey)),
		BeforeTokens:   estimateHistoryTokens(agent.Sessions.GetHistory(sessionKey)),
	}

	req := &CompactRequest{SessionKey: sessionKey, Reason: ContextCompressReasonManual}
	if agent.ContextWindow > 0 {
		req.Budget = agent.ContextWindow
	}
	if err := al.contextManager.Compact(ctx, req); err != nil {
		return result, err
	}

	after := agent.Sessions.GetHistory(sessionKey)
	result.AfterMessages = len(after)
	result.AfterTokens = estimateHistoryTokens(after)
	return result, nil
}

func estimateHistoryTokens(history []providers.Message) int {
	total := 0
	for _, msg := range history {
		total += EstimateMessageTokens(msg)
	}
	return total
}
