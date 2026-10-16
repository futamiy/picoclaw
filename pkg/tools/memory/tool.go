// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

// Package memorytools provides the agent-facing `memory` tool for managing
// bounded persistent memory (MEMORY.md, PROFILE.md and daily notes).
package memorytools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// Operation is one mutation in the atomic `operations` batch of the memory tool.
type Operation struct {
	Op      string `json:"op"`       // add | replace | remove
	Content string `json:"content"`  // add
	OldText string `json:"old_text"` // replace
	NewText string `json:"new_text"` // replace
	Text    string `json:"text"`     // remove
}

// Memory targets select which bounded file a mutation applies to.
const (
	// TargetMemory is the long-term memory file (MEMORY.md).
	TargetMemory = "memory"
	// TargetProfile is the bounded user profile file (PROFILE.md).
	TargetProfile = "profile"
)

// Store is the subset of the memory store the tool needs. It is satisfied by
// agent.MemoryStore without importing the agent package (avoids an import cycle).
type Store interface {
	ReadTarget(target string) string
	AddEntry(target, entry string) error
	ReplaceEntry(target, oldText, newText string) error
	RemoveEntry(target, substr string) error
	ApplyOperations(target string, ops []Operation) error
	AppendToday(content string) error
	CharLimit(target string) int
	TargetEnabled(target string) bool
	DailyNotesEnabled() bool
}

// maxConsecutiveFailures caps a retry loop: once the model has failed this many
// memory calls in a row, the tool returns a terminal "stop" message instead of
// letting it re-issue the same failing call.
const maxConsecutiveFailures = 3

// MemoryTool lets the agent curate its own persistent memory.
type MemoryTool struct {
	store Store
	mu    sync.Mutex
	fails int // consecutive failures; reset on any success
}

// NewMemoryTool creates the `memory` tool backed by store.
func NewMemoryTool(store Store) *MemoryTool {
	return &MemoryTool{store: store}
}

func (t *MemoryTool) Name() string { return "memory" }

func (t *MemoryTool) Description() string {
	limit := t.store.CharLimit(TargetMemory)
	profileLimit := t.store.CharLimit(TargetProfile)
	desc := "Manage your persistent long-term memory (MEMORY.md), injected into " +
		"your system prompt in future sessions. Actions: add (append an entry), " +
		"replace (old_text -> new_text), remove (delete the first entry containing text), " +
		"read (return current memory)"
	if t.store.DailyNotesEnabled() {
		desc += ", note (append to today's dated daily note, for time-stamped context)"
	}
	desc += ". Pass `target` = \"profile\" to manage your user profile (PROFILE.md, " +
		"durable facts about the human) instead. " +
		"Pass several mutations as `operations` to apply them atomically in one call."
	if limit > 0 {
		desc += " Long-term memory is a scarce resource, capped at a limited size; " +
			"condense or remove stale entries when it is full."
	}
	if profileLimit > 0 {
		desc += " Keep the profile small and factual too."
	}
	desc += " Store durable, cross-session facts only (preferences, decisions, recurring context). " +
		"Ephemeral task state belongs in the conversation; task-specific procedures belong in a skill."
	return desc
}

func (t *MemoryTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"add", "replace", "remove", "read", "note"},
				"description": "The single memory operation to perform (ignored when operations is given)",
			},
			"target": map[string]any{
				"type":        "string",
				"enum":        []string{TargetMemory, TargetProfile},
				"description": "Which memory to edit: \"memory\" (MEMORY.md, default) or \"profile\" (PROFILE.md, facts about the user)",
			},
			"operations": map[string]any{
				"type": "array",
				"description": "Atomic batch of long-term edits (add/replace/remove). Applied in order and " +
					"checked against the final size, so you can remove/replace stale entries and add new ones " +
					"in one call even when the memory is full. Takes precedence over action.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"op": map[string]any{
							"type": "string",
							"enum": []string{"add", "replace", "remove"},
						},
						"content":  map[string]any{"type": "string", "description": "Entry text (op=add)"},
						"old_text": map[string]any{"type": "string", "description": "Existing text to replace (op=replace)"},
						"new_text": map[string]any{"type": "string", "description": "Replacement text (op=replace)"},
						"text":     map[string]any{"type": "string", "description": "Substring identifying the entry to delete (op=remove)"},
					},
					"required": []string{"op"},
				},
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
	}
}

// fail records a failure and, past the consecutive-failure cap, returns a
// terminal message that tells the model to stop retrying.
// ponytail: consecutive counter, not a strict per-turn one; a turn boundary needs
// plumbing the tool does not have and the loop it breaks is consecutive anyway.
func (t *MemoryTool) fail(msg string) *toolshared.ToolResult {
	t.mu.Lock()
	t.fails++
	n := t.fails
	t.mu.Unlock()
	if n >= maxConsecutiveFailures {
		return toolshared.ErrorResult(fmt.Sprintf(
			"STOP: memory has failed %d times in a row (%s). Do not call memory again; "+
				"answer the user and mention that memory could not be updated.", n, msg))
	}
	return toolshared.ErrorResult(msg)
}

// ok clears the consecutive-failure counter and returns a silent success.
func (t *MemoryTool) ok(msg string) *toolshared.ToolResult {
	t.mu.Lock()
	t.fails = 0
	t.mu.Unlock()
	return toolshared.SilentResult(msg)
}

// normalizeTarget maps the `target` arg to a known target, defaulting to memory.
func normalizeTarget(v any) (string, bool) {
	s, _ := v.(string)
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "":
		return TargetMemory, true
	case TargetMemory, TargetProfile:
		return s, true
	default:
		return "", false
	}
}

func (t *MemoryTool) Execute(_ context.Context, args map[string]any) *toolshared.ToolResult {
	target, okTarget := normalizeTarget(args["target"])
	if !okTarget {
		return t.fail("target must be \"memory\" or \"profile\"")
	}
	if !t.store.TargetEnabled(target) {
		return t.fail("profile memory is disabled; set agents.defaults.memory.profile_enabled to use target=\"profile\"")
	}

	if ops := parseOperations(args["operations"]); len(ops) > 0 {
		if err := t.store.ApplyOperations(target, ops); err != nil {
			return t.fail(err.Error())
		}
		return t.ok(fmt.Sprintf("Applied %d memory operations atomically to %s.", len(ops), target))
	}

	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))

	switch action {
	case "add":
		content, _ := args["content"].(string)
		if strings.TrimSpace(content) == "" {
			return t.fail("content is required for action=add")
		}
		if err := t.store.AddEntry(target, content); err != nil {
			return t.fail(err.Error())
		}
		return t.ok(fmt.Sprintf("Entry added to %s.", target))

	case "replace":
		oldText, _ := args["old_text"].(string)
		newText, _ := args["new_text"].(string)
		if oldText == "" {
			return t.fail("old_text is required for action=replace")
		}
		if err := t.store.ReplaceEntry(target, oldText, newText); err != nil {
			return t.fail(err.Error())
		}
		return t.ok(fmt.Sprintf("Entry replaced in %s.", target))

	case "remove":
		text, _ := args["text"].(string)
		if text == "" {
			return t.fail("text is required for action=remove")
		}
		if err := t.store.RemoveEntry(target, text); err != nil {
			return t.fail(err.Error())
		}
		return t.ok(fmt.Sprintf("Entry removed from %s.", target))

	case "read":
		current := t.store.ReadTarget(target)
		if strings.TrimSpace(current) == "" {
			return t.ok(fmt.Sprintf("(%s is empty)", target))
		}
		return t.ok(current)

	case "note":
		if !t.store.DailyNotesEnabled() {
			return t.fail("daily notes are disabled")
		}
		content, _ := args["content"].(string)
		if strings.TrimSpace(content) == "" {
			return t.fail("content is required for action=note")
		}
		if err := t.store.AppendToday(content); err != nil {
			return t.fail(err.Error())
		}
		return t.ok("Daily note appended.")

	default:
		return t.fail("unknown action; expected one of add, replace, remove, read, note")
	}
}

// parseOperations normalizes the `operations` argument into typed Operations.
func parseOperations(v any) []Operation {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	ops := make([]Operation, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		op := Operation{}
		op.Op, _ = m["op"].(string)
		op.Content, _ = m["content"].(string)
		op.OldText, _ = m["old_text"].(string)
		op.NewText, _ = m["new_text"].(string)
		op.Text, _ = m["text"].(string)
		ops = append(ops, op)
	}
	return ops
}
