// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

// Package honchotools provides the agent-facing `honcho` tool for reading from
// and writing to a Honcho memory server: session context, peer profiles,
// message search, dialectic reasoning, and persistent conclusions.
package honchotools

import (
	"context"
	"fmt"
	"strings"
	"time"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"

	"github.com/sipeed/picoclaw/pkg/honcho"
)

// defaultToolTimeout bounds a single tool call so a slow server cannot hang
// the agent loop.
const defaultToolTimeout = 20 * time.Second

// Config carries the resolved Honcho scope for this agent.
type Config struct {
	Client             *honcho.Client
	WorkspaceID        string
	UserPeerID         string
	AgentPeerID        string
	DefaultDialectical string
}

// Tool exposes Honcho memory operations to the agent.
type Tool struct {
	cfg           Config
	runtimeEvents runtimeevents.Bus
}

// HonchoReasonedPayload describes a Honcho tool reasoning call. It is published
// as a KindAgentMemoryHonchoReasoned runtime event when the `reason` action
// completes, so observers can trace dialectic queries against memory.
type HonchoReasonedPayload struct {
	WorkspaceID  string `json:"workspace_id,omitempty"`
	SessionKey   string `json:"session_key,omitempty"`
	Query        string `json:"query,omitempty"`
	Level        string `json:"level,omitempty"`
	AnswerChars  int    `json:"answer_chars,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
	IsError      bool   `json:"is_error,omitempty"`
	ErrorMessage string `json:"error,omitempty"`
}

// SetEventPublisher injects the runtime event bus used to publish Honcho
// reasoning observations.
func (t *Tool) SetEventPublisher(bus runtimeevents.Bus) {
	if t == nil {
		return
	}
	t.runtimeEvents = bus
}

// NewTool constructs the `honcho` tool.
func NewTool(cfg Config) *Tool { return &Tool{cfg: cfg} }

func (t *Tool) Name() string { return "honcho" }

func (t *Tool) Description() string {
	return "Read and write long-term memory stored in the Honcho service, which " +
		"reasons over past conversations in the background. Actions: " +
		"context (session summary + what Honcho knows about the user), " +
		"profile (user representation and card), " +
		"search (find relevant past messages by query), " +
		"reason (ask Honcho a natural-language question answered from memory), " +
		"conclude (store a durable fact or preference about the user). " +
		"Prefer `search` for specific lookups and `reason` for synthesized answers."
}

func (t *Tool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"context", "profile", "search", "reason", "conclude"},
				"description": "The Honcho operation to perform",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (action=search) or question (action=reason)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Fact or preference to persist (action=conclude)",
			},
			"observed": map[string]any{
				"type":        "string",
				"description": "Peer the conclusion is about: 'user' or 'agent' (action=conclude, default user)",
			},
			"level": map[string]any{
				"type":        "string",
				"enum":        []string{"minimal", "low", "medium", "high", "max"},
				"description": "Reasoning depth for action=reason (default: configured level)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum results for action=search (default 10)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *Tool) Execute(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	if t == nil || t.cfg.Client == nil {
		return toolshared.ErrorResult("honcho is not configured")
	}
	action := strings.ToLower(strings.TrimSpace(stringArg(args, "action")))

	callCtx, cancel := context.WithTimeout(ctx, defaultToolTimeout)
	defer cancel()

	switch action {
	case "context":
		return t.runContext(callCtx)
	case "profile":
		return t.runProfile(callCtx)
	case "search":
		return t.runSearch(callCtx, args)
	case "reason":
		return t.runReason(callCtx, args)
	case "conclude":
		return t.runConclude(callCtx, args)
	default:
		return toolshared.ErrorResult("unknown action; expected one of context, profile, search, reason, conclude")
	}
}

func (t *Tool) runContext(ctx context.Context) *toolshared.ToolResult {
	sc, err := t.cfg.Client.GetSessionContext(ctx, t.cfg.WorkspaceID, toolshared.ToolSessionKey(ctx), honcho.SessionContextOptions{
		Summary:    true,
		PeerTarget: t.cfg.UserPeerID,
	})
	if err != nil {
		return toolshared.ErrorResult(fmt.Sprintf("honcho context failed: %v", err))
	}
	var b strings.Builder
	if sc.Summary != nil && strings.TrimSpace(sc.Summary.Content) != "" {
		b.WriteString("Session summary:\n")
		b.WriteString(strings.TrimSpace(sc.Summary.Content))
		b.WriteString("\n")
	}
	if r := strings.TrimSpace(sc.PeerRepr); r != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("What Honcho knows about the user:\n")
		b.WriteString(r)
	}
	if b.Len() == 0 {
		return toolshared.SilentResult("(no context available yet)")
	}
	return toolshared.SilentResult(strings.TrimSpace(b.String()))
}

func (t *Tool) runProfile(ctx context.Context) *toolshared.ToolResult {
	pc, err := t.cfg.Client.GetPeerContext(ctx, t.cfg.WorkspaceID, t.cfg.AgentPeerID, t.cfg.UserPeerID)
	if err != nil {
		return toolshared.ErrorResult(fmt.Sprintf("honcho profile failed: %v", err))
	}
	var b strings.Builder
	if r := strings.TrimSpace(pc.Representation); r != "" {
		b.WriteString("Representation:\n")
		b.WriteString(r)
		b.WriteString("\n")
	}
	if len(pc.PeerCard) > 0 {
		b.WriteString("Card:\n")
		for _, c := range pc.PeerCard {
			if s := strings.TrimSpace(c); s != "" {
				b.WriteString("- ")
				b.WriteString(s)
				b.WriteString("\n")
			}
		}
	}
	if b.Len() == 0 {
		return toolshared.SilentResult("(no profile available yet)")
	}
	return toolshared.SilentResult(strings.TrimSpace(b.String()))
}

func (t *Tool) runSearch(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	query := strings.TrimSpace(stringArg(args, "query"))
	if query == "" {
		return toolshared.ErrorResult("query is required for action=search")
	}
	limit := intArg(args, "limit")
	msgs, err := t.cfg.Client.SearchPeer(ctx, t.cfg.WorkspaceID, t.cfg.UserPeerID, honcho.SearchOptions{
		Query: query,
		Limit: limit,
	})
	if err != nil {
		return toolshared.ErrorResult(fmt.Sprintf("honcho search failed: %v", err))
	}
	if len(msgs) == 0 {
		return toolshared.SilentResult("(no matching memories)")
	}
	var b strings.Builder
	for i, m := range msgs {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, m.PeerID, strings.TrimSpace(m.Content))
	}
	return toolshared.SilentResult(strings.TrimSpace(b.String()))
}

func (t *Tool) runReason(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	query := strings.TrimSpace(stringArg(args, "query"))
	if query == "" {
		return toolshared.ErrorResult("query is required for action=reason")
	}
	level := strings.TrimSpace(stringArg(args, "level"))
	if level == "" {
		level = t.cfg.DefaultDialectical
	}
	startedAt := time.Now()
	answer, err := t.cfg.Client.Chat(ctx, t.cfg.WorkspaceID, t.cfg.AgentPeerID, honcho.ChatOptions{
		Query:          query,
		SessionID:      toolshared.ToolSessionKey(ctx),
		Target:         t.cfg.UserPeerID,
		ReasoningLevel: level,
	})
	duration := time.Since(startedAt)
	if err != nil {
		t.publishReasoned(ctx, query, level, 0, duration, err)
		return toolshared.ErrorResult(fmt.Sprintf("honcho reason failed: %v", err))
	}
	if strings.TrimSpace(answer) == "" {
		t.publishReasoned(ctx, query, level, 0, duration, nil)
		return toolshared.SilentResult("(no answer from memory)")
	}
	t.publishReasoned(ctx, query, level, len([]rune(answer)), duration, nil)
	return toolshared.SilentResult(answer)
}

// publishReasoned emits a KindAgentMemoryHonchoReasoned runtime event for a
// completed reason action. It is a no-op when no bus was injected.
func (t *Tool) publishReasoned(
	ctx context.Context,
	query, level string,
	answerChars int,
	duration time.Duration,
	callErr error,
) {
	if t == nil || t.runtimeEvents == nil {
		return
	}
	payload := HonchoReasonedPayload{
		WorkspaceID: t.cfg.WorkspaceID,
		SessionKey:  toolshared.ToolSessionKey(ctx),
		Query:       query,
		Level:       level,
		AnswerChars: answerChars,
		DurationMS:  duration.Milliseconds(),
	}
	severity := runtimeevents.SeverityInfo
	if callErr != nil {
		payload.IsError = true
		payload.ErrorMessage = callErr.Error()
		severity = runtimeevents.SeverityError
	}
	t.runtimeEvents.PublishNonBlocking(runtimeevents.Event{
		Kind:   runtimeevents.KindAgentMemoryHonchoReasoned,
		Source: runtimeevents.Source{Component: "honcho", Name: t.cfg.WorkspaceID},
		Scope: runtimeevents.Scope{
			AgentID:    toolshared.ToolAgentID(ctx),
			SessionKey: toolshared.ToolSessionKey(ctx),
			Channel:    toolshared.ToolChannel(ctx),
			ChatID:     toolshared.ToolChatID(ctx),
			MessageID:  toolshared.ToolMessageID(ctx),
		},
		Severity: severity,
		Payload:  payload,
		Attrs: map[string]any{
			"workspace_id": payload.WorkspaceID,
			"session_key":  payload.SessionKey,
			"level":        payload.Level,
			"answer_chars": payload.AnswerChars,
			"duration_ms":  payload.DurationMS,
			"is_error":     payload.IsError,
		},
	})
}

func (t *Tool) runConclude(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	content := strings.TrimSpace(stringArg(args, "content"))
	if content == "" {
		return toolshared.ErrorResult("content is required for action=conclude")
	}
	observed := strings.ToLower(strings.TrimSpace(stringArg(args, "observed")))
	observedID := t.cfg.UserPeerID
	if observed == "agent" || observed == "assistant" {
		observedID = t.cfg.AgentPeerID
	}
	c, err := t.cfg.Client.CreateConclusion(ctx, t.cfg.WorkspaceID, honcho.ConclusionInput{
		Content:    content,
		ObserverID: t.cfg.AgentPeerID,
		ObservedID: observedID,
		SessionID:  toolshared.ToolSessionKey(ctx),
	})
	if err != nil {
		return toolshared.ErrorResult(fmt.Sprintf("honcho conclude failed: %v", err))
	}
	if c == nil {
		return toolshared.SilentResult("Conclusion recorded.")
	}
	return toolshared.SilentResult("Conclusion recorded.")
}

func stringArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

func intArg(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}
