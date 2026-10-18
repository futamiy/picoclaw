// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

// Package retaindbtools provides the agent-facing `retaindb` tool for reading
// and writing memories in a RetainDB backend: semantic search, the stored user
// profile, and explicit fact storage.
package retaindbtools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/retaindb"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// defaultToolTimeout bounds a single tool call so a slow server cannot hang
// the agent loop.
const defaultToolTimeout = 20 * time.Second

// defaultLimit is used when a call does not pass an explicit limit.
const defaultLimit = 10

// Config carries the resolved RetainDB scope for this agent.
type Config struct {
	Client *retaindb.Client
	UserID string
}

// Tool exposes RetainDB memory operations to the agent.
type Tool struct {
	cfg Config
}

// NewTool constructs the `retaindb` tool.
func NewTool(cfg Config) *Tool { return &Tool{cfg: cfg} }

func (t *Tool) Name() string { return "retaindb" }

func (t *Tool) Description() string {
	return "Read and write long-term memory stored in the RetainDB service. " +
		"Actions: search (find relevant stored memories by query), " +
		"profile (the stable profile RetainDB holds for the user), " +
		"remember (store a durable fact, preference, decision, or correction). " +
		"Prefer `search` for specific lookups and `profile` for a user overview."
}

func (t *Tool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"search", "profile", "remember"},
				"description": "The RetainDB operation to perform",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (action=search)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Fact or preference to persist (action=remember)",
			},
			"memory_type": map[string]any{
				"type":        "string",
				"description": "Type for action=remember (default factual; e.g. preference, decision, correction)",
			},
			"importance": map[string]any{
				"type":        "number",
				"description": "Importance from 0 to 1 for action=remember (default 0.7)",
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
		return toolshared.ErrorResult("retaindb is not configured")
	}
	action := strings.ToLower(strings.TrimSpace(stringArg(args, "action")))

	callCtx, cancel := context.WithTimeout(ctx, defaultToolTimeout)
	defer cancel()

	switch action {
	case "search":
		return t.runSearch(callCtx, args)
	case "profile":
		return t.runProfile(callCtx)
	case "remember":
		return t.runRemember(callCtx, args)
	default:
		return toolshared.ErrorResult("unknown action; expected one of search, profile, remember")
	}
}

func (t *Tool) runSearch(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	query := strings.TrimSpace(stringArg(args, "query"))
	if query == "" {
		return toolshared.ErrorResult("query is required for action=search")
	}
	mems, err := t.cfg.Client.SearchMemories(ctx, t.cfg.UserID, toolshared.ToolSessionKey(ctx), query, limitArg(args))
	if err != nil {
		return toolshared.ErrorResult(fmt.Sprintf("retaindb search failed: %v", err))
	}
	return memoryListResult(mems, "(no matching memories)")
}

func (t *Tool) runProfile(ctx context.Context) *toolshared.ToolResult {
	mems, err := t.cfg.Client.GetProfile(ctx, t.cfg.UserID)
	if err != nil {
		return toolshared.ErrorResult(fmt.Sprintf("retaindb profile failed: %v", err))
	}
	return memoryListResult(mems, "(no profile available yet)")
}

func (t *Tool) runRemember(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	content := strings.TrimSpace(stringArg(args, "content"))
	if content == "" {
		return toolshared.ErrorResult("content is required for action=remember")
	}
	importance := floatArg(args, "importance")
	memoryType := strings.TrimSpace(stringArg(args, "memory_type"))
	if err := t.cfg.Client.AddMemory(
		ctx,
		t.cfg.UserID,
		toolshared.ToolSessionKey(ctx),
		content,
		memoryType,
		importance,
	); err != nil {
		return toolshared.ErrorResult(fmt.Sprintf("retaindb remember failed: %v", err))
	}
	return toolshared.SilentResult("Memory stored.")
}

// memoryListResult renders memories as a numbered list, or a placeholder when
// nothing was found.
func memoryListResult(mems []retaindb.Memory, empty string) *toolshared.ToolResult {
	items := make([]string, 0, len(mems))
	for _, m := range mems {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		if s := strings.TrimSpace(m.MemoryType); s != "" {
			text = "[" + s + "] " + text
		}
		items = append(items, text)
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

func floatArg(args map[string]any, key string) float64 {
	switch v := args[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}
