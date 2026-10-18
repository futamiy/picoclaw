// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

// Package mem0tools provides the agent-facing `mem0` tool for reading and
// writing memories in a Mem0 backend: semantic search, recent-memory listing,
// and explicit fact storage.
package mem0tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/mem0"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// defaultToolTimeout bounds a single tool call so a slow server cannot hang
// the agent loop.
const defaultToolTimeout = 20 * time.Second

// defaultLimit is used when a call does not pass an explicit limit.
const defaultLimit = 10

// Config carries the resolved Mem0 scope for this agent.
type Config struct {
	Client  *mem0.Client
	UserID  string
	AgentID string
}

// Tool exposes Mem0 memory operations to the agent.
type Tool struct {
	cfg Config
}

// NewTool constructs the `mem0` tool.
func NewTool(cfg Config) *Tool { return &Tool{cfg: cfg} }

func (t *Tool) Name() string { return "mem0" }

func (t *Tool) Description() string {
	return "Read and write long-term memory stored in the Mem0 service, which " +
		"extracts and deduplicates facts in the background. Actions: " +
		"search (find relevant stored memories by query), " +
		"recent (list the most recently stored memories), " +
		"add (store a durable fact or preference). " +
		"Prefer `search` for specific lookups and `add` only when the user asks " +
		"you to remember something."
}

func (t *Tool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"search", "recent", "add"},
				"description": "The Mem0 operation to perform",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (action=search)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Fact or preference to persist (action=add)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum results (default 10)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *Tool) Execute(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	if t == nil || t.cfg.Client == nil {
		return toolshared.ErrorResult("mem0 is not configured")
	}
	action := strings.ToLower(strings.TrimSpace(stringArg(args, "action")))

	callCtx, cancel := context.WithTimeout(ctx, defaultToolTimeout)
	defer cancel()

	switch action {
	case "search":
		return t.runSearch(callCtx, args)
	case "recent":
		return t.runRecent(callCtx, args)
	case "add":
		return t.runAdd(callCtx, args)
	default:
		return toolshared.ErrorResult("unknown action; expected one of search, recent, add")
	}
}

func (t *Tool) runSearch(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	query := strings.TrimSpace(stringArg(args, "query"))
	if query == "" {
		return toolshared.ErrorResult("query is required for action=search")
	}
	mems, err := t.cfg.Client.SearchMemories(ctx, query, t.cfg.UserID, t.cfg.AgentID, limitArg(args))
	if err != nil {
		return toolshared.ErrorResult(fmt.Sprintf("mem0 search failed: %v", err))
	}
	return memoryListResult(mems, "(no matching memories)")
}

func (t *Tool) runRecent(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	mems, err := t.cfg.Client.ListMemories(ctx, t.cfg.UserID, t.cfg.AgentID, limitArg(args))
	if err != nil {
		return toolshared.ErrorResult(fmt.Sprintf("mem0 recent failed: %v", err))
	}
	return memoryListResult(mems, "(no memories stored yet)")
}

func (t *Tool) runAdd(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	content := strings.TrimSpace(stringArg(args, "content"))
	if content == "" {
		return toolshared.ErrorResult("content is required for action=add")
	}
	if err := t.cfg.Client.AddMessages(ctx, t.cfg.UserID, t.cfg.AgentID, []mem0.Message{
		{Role: "user", Content: content},
	}); err != nil {
		return toolshared.ErrorResult(fmt.Sprintf("mem0 add failed: %v", err))
	}
	return toolshared.SilentResult("Memory stored.")
}

// memoryListResult renders memories as a numbered list, or a placeholder when
// nothing was found.
func memoryListResult(mems []mem0.Memory, empty string) *toolshared.ToolResult {
	items := make([]string, 0, len(mems))
	for _, m := range mems {
		if text := m.Text(); text != "" {
			items = append(items, text)
		}
	}
	if len(items) == 0 {
		return toolshared.SilentResult(empty)
	}
	var b strings.Builder
	for i, item := range items {
		fmt.Fprintf(&b, "%d. %s\n", i+1, item)
	}
	return toolshared.SilentResult(strings.TrimSpace(b.String()))
}

func limitArg(args map[string]any) int {
	if v := intArg(args, "limit"); v > 0 {
		return v
	}
	return defaultLimit
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
