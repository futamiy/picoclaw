package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/mem0"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/retaindb"
)

// memorySync is the minimal contract the pipeline uses to trigger an optional
// external memory sync at the end of a turn. Both the Honcho-specific syncer
// and the generic MemorySyncer implement it.
type memorySync interface {
	MaybeSync(al *AgentLoop, sessionKey string)
}

// memoryRecaller is an optional extension implemented by syncers that can also
// read memory back (currently only the Honcho syncer). Recall returns a
// cached, ready-to-inject block and never blocks on the network.
type memoryRecaller interface {
	RecallContext(sessionKey string) string
}

// externalMemorySink pushes a filtered transcript to a provider. Adapters wrap
// each provider client so the syncer stays provider-agnostic.
type externalMemorySink interface {
	// Name identifies the provider for logging.
	Name() string
	// AddMessages pushes the transcript. sessionKey scopes the conversation.
	AddMessages(ctx context.Context, sessionKey string, msgs []providers.Message) error
}

// MemorySyncer pushes conversation transcripts to an external memory provider
// (Mem0, RetainDB) at a turn cadence. Unlike Honcho, it has no staged approval
// mode: messages are pushed automatically.
type MemorySyncer struct {
	agent      *AgentInstance
	sink       externalMemorySink
	everyTurns int
	sanitize   func(string) string

	countMu sync.Mutex
	counts  map[string]int // sessionKey -> message count at last sync
	syncing sync.Map       // "agentID:sessionKey" -> bool, dedup in-flight
}

func newMemorySyncer(agent *AgentInstance, sink externalMemorySink, everyTurns int, sanitize func(string) string) *MemorySyncer {
	if everyTurns < honchoSyncEveryTurnsMin {
		everyTurns = honchoSyncEveryTurnsMin
	}
	return &MemorySyncer{
		agent:      agent,
		sink:       sink,
		everyTurns: everyTurns,
		sanitize:   sanitize,
		counts:     make(map[string]int),
	}
}

// MaybeSync pushes the conversation to the sink when the cadence is met.
func (ms *MemorySyncer) MaybeSync(al *AgentLoop, sessionKey string) {
	agent := ms.agent
	if al == nil || agent == nil || agent.Sessions == nil || ms.sink == nil {
		return
	}
	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) == 0 {
		return
	}

	ms.countMu.Lock()
	last := ms.counts[sessionKey]
	ms.countMu.Unlock()
	if len(history)-last < ms.everyTurns {
		return
	}

	extractKey := agent.ID + ":" + sessionKey
	if _, loading := ms.syncing.LoadOrStore(extractKey, true); loading {
		return
	}

	go func() {
		defer ms.syncing.Delete(extractKey)
		defer func() {
			if r := recover(); r != nil {
				logger.WarnCF("agent", "Memory sync panic recovered", map[string]any{
					"provider":    ms.sink.Name(),
					"session_key": sessionKey,
					"panic":       r,
				})
			}
		}()

		transcriptMsgs := buildExternalMemoryTranscript(history)
		if len(transcriptMsgs) == 0 {
			ms.countMu.Lock()
			ms.counts[sessionKey] = len(history)
			ms.countMu.Unlock()
			return
		}
		if ms.sanitize != nil {
			for i := range transcriptMsgs {
				transcriptMsgs[i].Content = ms.sanitize(transcriptMsgs[i].Content)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := ms.sink.AddMessages(ctx, sessionKey, transcriptMsgs); err != nil {
			logger.WarnCF("agent", "Memory sync failed; skipping", map[string]any{
				"provider":    ms.sink.Name(),
				"session_key": sessionKey,
				"error":       err,
			})
			return
		}
		ms.countMu.Lock()
		ms.counts[sessionKey] = len(history)
		ms.countMu.Unlock()
	}()
}

// buildExternalMemoryTranscript filters session history to the user/assistant messages
// that fit the character budget, preserving order.
func buildExternalMemoryTranscript(history []providers.Message) []providers.Message {
	out := make([]providers.Message, 0, len(history))
	total := 0
	for _, msg := range history {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		if total+len([]rune(content)) > maxHonchoTranscriptRunes {
			continue
		}
		total += len([]rune(content))
		out = append(out, providers.Message{Role: msg.Role, Content: content})
	}
	return out
}

// mem0Sink adapts a Mem0 client to the externalMemorySink contract.
type mem0Sink struct {
	client  *mem0.Client
	userID  string
	agentID string
}

func (s *mem0Sink) Name() string { return "mem0" }

func (s *mem0Sink) AddMessages(ctx context.Context, _ string, msgs []providers.Message) error {
	out := make([]mem0.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, mem0.Message{Role: m.Role, Content: m.Content})
	}
	return s.client.AddMessages(ctx, s.userID, s.agentID, out)
}

// retainDBSink adapts a RetainDB client to the externalMemorySink contract.
type retainDBSink struct {
	client *retaindb.Client
}

func (s *retainDBSink) Name() string { return "retaindb" }

func (s *retainDBSink) AddMessages(ctx context.Context, _ string, msgs []providers.Message) error {
	out := make([]retaindb.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, retaindb.Message{Role: m.Role, Content: m.Content})
	}
	return s.client.AddMessages(ctx, out)
}
