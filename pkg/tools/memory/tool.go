// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

// Package memorytools provides the agent-facing `memory` tool for managing
// bounded persistent memory (MEMORY.md and daily notes).
package memorytools

import (
	"context"
	"strings"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// Store is the subset of the memory store the tool needs. It is satisfied by
// agent.MemoryStore without importing the agent package (avoids an import cycle).
type Store interface {
	ReadLongTerm() string
	AddLongTerm(entry string) error
	ReplaceLongTerm(oldText, newText string) error
	RemoveLongTerm(substr string) error
	AppendToday(content string) error
	LongTermCharLimit() int
	DailyNotesEnabled() bool
}

// MemoryTool lets the agent curate its own persistent memory.
type MemoryTool struct {
	store Store
}

// NewMemoryTool creates the `memory` tool backed by store.
func NewMemoryTool(store Store) *MemoryTool {
	return &MemoryTool{store: store}
}

func (t *MemoryTool) Name() string { return "memory" }

func (t *MemoryTool) Description() string {
	limit := t.store.LongTermCharLimit()
	desc := "Manage your persistent long-term memory (MEMORY.md), injected into " +
		"your system prompt in future sessions. Actions: add (append an entry), " +
		"replace (old_text -> new_text), remove (delete the first entry containing text), " +
		"read (return current memory)"
	if t.store.DailyNotesEnabled() {
		desc += ", note (append to today's dated daily note, for time-stamped context)"
	}
	desc += "."
	if limit > 0 {
		desc += " Long-term memory is a scarce resource, capped at " +
			"a limited size — condense or remove stale entries when it is full."
	}
	return desc
}

func (t *MemoryTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"add", "replace", "remove", "read", "note"},
				"description": "The memory operation to perform",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Entry text for action=add, or note text for action=note",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "Exact existing text to replace (action=replace)",
			},
			"new_text": map[string]any{
				"type":        "string",
				"description": "Replacement text (action=replace)",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Substring identifying the entry to delete (action=remove)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *MemoryTool) Execute(_ context.Context, args map[string]any) *toolshared.ToolResult {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))

	switch action {
	case "add":
		content, _ := args["content"].(string)
		if strings.TrimSpace(content) == "" {
			return toolshared.ErrorResult("content is required for action=add")
		}
		if err := t.store.AddLongTerm(content); err != nil {
			return toolshared.ErrorResult(err.Error())
		}
		return toolshared.SilentResult("Memory entry added.")

	case "replace":
		oldText, _ := args["old_text"].(string)
		newText, _ := args["new_text"].(string)
		if oldText == "" {
			return toolshared.ErrorResult("old_text is required for action=replace")
		}
		if err := t.store.ReplaceLongTerm(oldText, newText); err != nil {
			return toolshared.ErrorResult(err.Error())
		}
		return toolshared.SilentResult("Memory entry replaced.")

	case "remove":
		text, _ := args["text"].(string)
		if text == "" {
			return toolshared.ErrorResult("text is required for action=remove")
		}
		if err := t.store.RemoveLongTerm(text); err != nil {
			return toolshared.ErrorResult(err.Error())
		}
		return toolshared.SilentResult("Memory entry removed.")

	case "read":
		current := t.store.ReadLongTerm()
		if strings.TrimSpace(current) == "" {
			return toolshared.SilentResult("(memory is empty)")
		}
		return toolshared.SilentResult(current)

	case "note":
		if !t.store.DailyNotesEnabled() {
			return toolshared.ErrorResult("daily notes are disabled")
		}
		content, _ := args["content"].(string)
		if strings.TrimSpace(content) == "" {
			return toolshared.ErrorResult("content is required for action=note")
		}
		if err := t.store.AppendToday(content); err != nil {
			return toolshared.ErrorResult(err.Error())
		}
		return toolshared.SilentResult("Daily note appended.")

	default:
		return toolshared.ErrorResult("unknown action; expected one of add, replace, remove, read, note")
	}
}
