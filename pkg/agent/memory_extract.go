// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// memoryExtractEveryTurnsMin guards against degenerate cadence configs.
const memoryExtractEveryTurnsMin = 1

// maxMemoryTranscriptRunes bounds how much conversation is sent to the extractor.
const maxMemoryTranscriptRunes = 20000

// MemoryManager extracts long-term memory from conversation history at a
// turn cadence, then persists it through the same bounded MemoryStore the
// agent's `memory` tool uses.
type MemoryManager struct {
	agent      *AgentInstance
	store      *MemoryStore
	everyTurns int
	model      string
	sanitize   func(string) string

	countMu    sync.Mutex
	counts     map[string]int // sessionKey -> message count at last successful extraction
	extracting sync.Map       // "agentID:sessionKey" -> bool, dedup in-flight extractions
}

// MemoryExtractOptions configures a MemoryManager.
type MemoryExtractOptions struct {
	EveryTurns int
	Model      string // "" falls back to the agent's primary model
}

// newMemoryManager builds a MemoryManager tied to a fully-constructed agent.
func newMemoryManager(agent *AgentInstance, store *MemoryStore, opts MemoryExtractOptions, sanitize func(string) string) *MemoryManager {
	every := opts.EveryTurns
	if every < memoryExtractEveryTurnsMin {
		every = config.DefaultMemoryExtractEveryTurns
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = agent.Model
	}
	return &MemoryManager{
		agent:      agent,
		store:      store,
		everyTurns: every,
		model:      model,
		sanitize:   sanitize,
		counts:     make(map[string]int),
	}
}

// MaybeExtract kicks off an async extraction when at least everyTurns new
// messages have accumulated in the session since the last run. Extraction is
// best-effort; failures are logged and retried on later turns.
func (m *MemoryManager) MaybeExtract(al *AgentLoop, sessionKey string) {
	agent := m.agent
	if al == nil || agent == nil || agent.Sessions == nil || agent.Provider == nil || m.store == nil {
		return
	}

	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) == 0 {
		return
	}

	m.countMu.Lock()
	last := m.counts[sessionKey]
	m.countMu.Unlock()
	if len(history)-last < m.everyTurns {
		return
	}

	extractKey := agent.ID + ":" + sessionKey
	if _, loading := m.extracting.LoadOrStore(extractKey, true); loading {
		return
	}

	go func() {
		defer m.extracting.Delete(extractKey)
		defer func() {
			if r := recover(); r != nil {
				logger.WarnCF("agent", "Memory extraction panic recovered", map[string]any{
					"session_key": sessionKey,
					"panic":       r,
				})
			}
		}()

		payload := m.runExtraction(sessionKey)
		if payload == nil {
			return
		}
		m.countMu.Lock()
		m.counts[sessionKey] = len(history)
		m.countMu.Unlock()
		al.emitEvent(
			runtimeevents.KindAgentMemoryExtract,
			al.newTurnEventScope(agent.ID, sessionKey, nil).meta(0, "memoryExtract", "turn.memory.extract"),
			*payload,
		)
	}()
}

// runExtraction performs one LLM-driven memory curation pass.
func (m *MemoryManager) runExtraction(sessionKey string) *MemoryExtractPayload {
	agent := m.agent
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	transcript := buildMemoryTranscript(agent.Sessions.GetHistory(sessionKey))
	if strings.TrimSpace(transcript) == "" {
		return nil
	}
	if m.sanitize != nil {
		transcript = m.sanitize(transcript)
	}

	prompt := buildMemoryExtractionPrompt(transcript, m.store.ReadLongTerm(), m.store.ReadToday())
	resp, err := m.callExtractionLLM(ctx, prompt)
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		logger.WarnCF("agent", "Memory extraction LLM call failed; skipping", map[string]any{
			"session_key": sessionKey,
			"error":       llmErrString(err),
		})
		return nil
	}

	ops := parseMemoryOps(resp.Content)
	if ops == nil {
		logger.WarnCF("agent", "Memory extraction returned unparsable ops; skipping", map[string]any{
			"session_key": sessionKey,
		})
		return nil
	}

	payload := &MemoryExtractPayload{
		SessionKey:   sessionKey,
		ScannedTurns: len(agent.Sessions.GetHistory(sessionKey)),
	}
	for _, entry := range ops.Add {
		if err := m.store.AddLongTerm(entry); err == nil {
			payload.Additions++
		}
	}
	for _, rep := range ops.Replace {
		if err := m.store.ReplaceLongTerm(rep.OldText, rep.NewText); err == nil {
			payload.Replacements++
		}
	}
	for _, entry := range ops.Remove {
		if err := m.store.RemoveLongTerm(entry); err == nil {
			payload.Removals++
		}
	}
	if m.store.DailyNotesEnabled() {
		for _, note := range ops.Note {
			if err := m.store.AppendToday(note); err == nil {
				payload.Notes++
			}
		}
	}
	return payload
}

func (m *MemoryManager) callExtractionLLM(ctx context.Context, prompt string) (*providers.LLMResponse, error) {
	const maxRetries = 2
	var resp *providers.LLMResponse
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, lastErr = m.agent.Provider.Chat(
			ctx,
			[]providers.Message{{Role: "user", Content: prompt}},
			nil,
			m.model,
			map[string]any{
				"max_tokens":       2000,
				"temperature":      0.2,
				"prompt_cache_key": m.agent.ID,
			},
		)
		if lastErr == nil && resp != nil && resp.Content != "" {
			return resp, nil
		}
		if attempt < maxRetries-1 {
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
		}
	}
	return resp, lastErr
}

func llmErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// buildMemoryTranscript flattens recent user/assistant turns into labeled text.
func buildMemoryTranscript(history []providers.Message) string {
	var sb strings.Builder
	for _, msg := range history {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(msg.Role)
		sb.WriteString(": ")
		sb.WriteString(content)
	}
	transcript := truncateRunes(sb.String(), maxMemoryTranscriptRunes)
	if len([]rune(transcript)) >= maxMemoryTranscriptRunes {
		transcript += "\n[transcript truncated for memory extraction]"
	}
	return transcript
}

// buildMemoryExtractionPrompt instructs the extractor to output strict JSON ops.
func buildMemoryExtractionPrompt(transcript, longTerm, todayNote string) string {
	var sb strings.Builder
	sb.WriteString("You are a memory curator for an AI assistant. Review the conversation below and update the assistant's persistent long-term memory.\n\n")
	sb.WriteString("CURRENT LONG-TERM MEMORY (MEMORY.md):\n")
	if strings.TrimSpace(longTerm) == "" {
		sb.WriteString("(empty)\n")
	} else {
		sb.WriteString(longTerm)
		sb.WriteString("\n")
	}
	sb.WriteString("\nTODAY'S DAILY NOTE SO FAR:\n")
	if strings.TrimSpace(todayNote) == "" {
		sb.WriteString("(empty)\n")
	} else {
		sb.WriteString(todayNote)
		sb.WriteString("\n")
	}
	sb.WriteString(`
RULES:
- "add": durable facts about the user or the project worth remembering across sessions, already condensed to one concise line.
- "replace": "old_text" MUST exactly match an existing substring in MEMORY.md; "new_text" is its condensed update.
- "remove": entries that are now stale/duplicated; the string must match an existing entry line closely.
- "note": brief dated happenings worth keeping for today (a log entry, not duplicate memory).
- Store only what you are confident is useful and stable. Prefer few high-value operations over many speculative ones.
- Do NOT invent facts. Do NOT repeat what is already in MEMORY.md verbatim.

Respond with ONLY one JSON object, no markdown fences:
{"memory":{"remove":["entry-substring"],"replace":[{"old_text":"exact existing substring","new_text":"condensed replacement"}],"add":["concise line"],"note":["brief note"]}}
Omit keys with no operations.

CONVERSATION:
`)
	sb.WriteString(transcript)
	return sb.String()
}

type memoryOps struct {
	Add     []string
	Replace []memoryReplaceOp
	Remove  []string
	Note    []string
}

type memoryReplaceOp struct {
	OldText string
	NewText string
}

type memoryOpsJSON struct {
	Memory *struct {
		Remove  []string `json:"remove"`
		Replace []struct {
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		} `json:"replace"`
		Add  []string `json:"add"`
		Note []string `json:"note"`
	} `json:"memory"`
}

// parseMemoryOps tolerates fenced/verbose LLM output and extracts the ops JSON.
// The ops may be wrapped under a "memory" key or given bare at the top level.
func parseMemoryOps(content string) *memoryOps {
	var raw memoryOpsJSON
	if err := json.Unmarshal([]byte(extractJSONObject(content)), &raw); err != nil {
		return nil
	}
	if raw.Memory == nil {
		return nil
	}
	inner := raw.Memory
	ops := &memoryOps{
		Add:    nonempty(inner.Add),
		Remove: nonempty(inner.Remove),
		Note:   nonempty(inner.Note),
	}
	for _, rep := range inner.Replace {
		if strings.TrimSpace(rep.OldText) != "" {
			ops.Replace = append(ops.Replace, memoryReplaceOp{OldText: rep.OldText, NewText: rep.NewText})
		}
	}
	return ops
}

// extractJSONObject grabs the substring between the first '{' and last '}', stripping fences.
func extractJSONObject(content string) string {
	start := strings.Index(content, "{")
	if start < 0 {
		return ""
	}
	end := strings.LastIndex(content, "}")
	if end <= start {
		return ""
	}
	return content[start : end+1]
}

func nonempty(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}
