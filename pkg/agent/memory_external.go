package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
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
// read memory back. Recall returns a cached, ready-to-inject block and never
// blocks on the network. query is the current user message, which providers
// with search support use to rank what they return.
type memoryRecaller interface {
	RecallContext(sessionKey, query string) string
	// MemoryProviderName identifies the backend for the injected prompt block.
	MemoryProviderName() string
}

// externalMemoryReader is implemented by sinks that can pull memory back from
// their provider. Recall renders the provider response into a compact block.
type externalMemoryReader interface {
	Recall(ctx context.Context, sessionKey, query string) (string, error)
}

// externalMemorySink pushes a filtered transcript to a provider. Adapters wrap
// each provider client so the syncer stays provider-agnostic.
type externalMemorySink interface {
	// Name identifies the provider for logging.
	Name() string
	// AddMessages pushes the transcript. sessionKey scopes the conversation.
	AddMessages(ctx context.Context, sessionKey string, msgs []providers.Message) error
}

// memoryRecallConfig configures read-back for a MemorySyncer. The zero value
// disables recall.
type memoryRecallConfig struct {
	Enabled bool
	Cadence int
}

// MemorySyncer pushes conversation transcripts to an external memory provider
// (Mem0, RetainDB) at a turn cadence and, when recall is enabled, prefetches
// relevant memories in the background so turns never block on the network.
// Unlike Honcho, it has no staged approval mode: messages are pushed
// automatically.
type MemorySyncer struct {
	agent      *AgentInstance
	sink       externalMemorySink
	everyTurns int
	sanitize   func(string) string

	recallEnabled  bool
	contextCadence int

	countMu sync.Mutex
	counts  map[string]int // sessionKey -> message count at last sync
	syncing sync.Map       // "agentID:sessionKey" -> bool, dedup in-flight

	prefetchMu sync.Mutex
	prefetch   map[string]*memoryPrefetchEntry
}

func newMemorySyncer(agent *AgentInstance, sink externalMemorySink, everyTurns int, sanitize func(string) string, recall memoryRecallConfig) *MemorySyncer {
	if everyTurns < honchoSyncEveryTurnsMin {
		everyTurns = honchoSyncEveryTurnsMin
	}
	cadence := recall.Cadence
	if cadence < 1 {
		cadence = 1
	}
	return &MemorySyncer{
		agent:          agent,
		sink:           sink,
		everyTurns:     everyTurns,
		sanitize:       sanitize,
		recallEnabled:  recall.Enabled,
		contextCadence: cadence,
		counts:         make(map[string]int),
		prefetch:       make(map[string]*memoryPrefetchEntry),
	}
}

// MemoryProviderName identifies the configured provider for prompt labelling.
func (ms *MemorySyncer) MemoryProviderName() string {
	if ms == nil || ms.sink == nil {
		return ""
	}
	return ms.sink.Name()
}

// RecallContext returns the cached recall block for sessionKey, scheduling a
// background refresh when the cadence is met. It never blocks on the network.
func (ms *MemorySyncer) RecallContext(sessionKey, query string) string {
	if ms == nil || !ms.recallEnabled || sessionKey == "" {
		return ""
	}
	ms.maybePrefetch(sessionKey, query)

	ms.prefetchMu.Lock()
	defer ms.prefetchMu.Unlock()
	if e := ms.prefetch[sessionKey]; e != nil {
		return e.content
	}
	return ""
}

// maybePrefetch schedules a background recall for sessionKey when the cadence
// has elapsed and no fetch is already in flight.
func (ms *MemorySyncer) maybePrefetch(sessionKey, query string) {
	if ms.sink == nil {
		return
	}
	if _, ok := ms.sink.(externalMemoryReader); !ok {
		return
	}
	historyLen := 0
	if ms.agent != nil && ms.agent.Sessions != nil {
		historyLen = len(ms.agent.Sessions.GetHistory(sessionKey))
	}

	ms.prefetchMu.Lock()
	e := ms.prefetch[sessionKey]
	if e == nil {
		e = &memoryPrefetchEntry{}
		if len(ms.prefetch) >= maxHonchoCacheKeys {
			ms.evictOldestLocked()
		}
		ms.prefetch[sessionKey] = e
	}
	if e.inFlight {
		ms.prefetchMu.Unlock()
		return
	}
	if e.content != "" && historyLen-e.lastTurn < ms.contextCadence {
		ms.prefetchMu.Unlock()
		return
	}
	e.inFlight = true
	e.lastTurn = historyLen
	ms.prefetchMu.Unlock()

	provider := ms.sink.Name()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.WarnCF("agent", "Memory recall prefetch panic recovered", map[string]any{
					"provider":    provider,
					"session_key": sessionKey,
					"panic":       r,
				})
			}
		}()

		content := ms.fetchContext(sessionKey, query)

		ms.prefetchMu.Lock()
		if entry := ms.prefetch[sessionKey]; entry != nil {
			entry.inFlight = false
			if content != "" {
				entry.content = content
			}
		}
		ms.prefetchMu.Unlock()

		if content != "" {
			logger.DebugCF("agent", "External memory context prefetched", map[string]any{
				"provider":    provider,
				"session_key": sessionKey,
				"chars":       len([]rune(content)),
			})
		}
	}()
}

func (ms *MemorySyncer) evictOldestLocked() {
	for k := range ms.prefetch {
		delete(ms.prefetch, k)
		return
	}
}

// fetchContext pulls a recall block from the sink, logging and swallowing
// provider errors so a failure never disrupts the turn.
func (ms *MemorySyncer) fetchContext(sessionKey, query string) string {
	reader, ok := ms.sink.(externalMemoryReader)
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), honchoPrefetchTimeout)
	defer cancel()
	block, err := reader.Recall(ctx, sessionKey, query)
	if err != nil {
		logger.DebugCF("agent", "Memory recall fetch failed", map[string]any{
			"provider":    ms.sink.Name(),
			"session_key": sessionKey,
			"error":       err,
		})
		return ""
	}
	return truncateHonchoRunes(strings.TrimSpace(block), maxHonchoContextRunes)
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

// recallGroup is one titled list of memories in a rendered recall block.
type recallGroup struct {
	Title string
	Items []string
}

// renderExternalRecall composes deduplicated, non-empty groups into a compact
// block suitable for prompt injection. Duplicate memories (ignoring case and
// surrounding whitespace) are dropped across groups.
func renderExternalRecall(groups ...recallGroup) string {
	seen := make(map[string]struct{})
	var b strings.Builder
	for _, g := range groups {
		items := make([]string, 0, len(g.Items))
		for _, item := range g.Items {
			s := strings.TrimSpace(item)
			if s == "" {
				continue
			}
			key := strings.ToLower(s)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, s)
		}
		if len(items) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(g.Title)
		b.WriteString(":\n")
		for _, item := range items {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// mem0Sink adapts a Mem0 client to the externalMemorySink and
// externalMemoryReader contracts.
type mem0Sink struct {
	client      *mem0.Client
	userID      string
	agentID     string
	recallLimit int
}

func (s *mem0Sink) Name() string { return "mem0" }

func (s *mem0Sink) AddMessages(ctx context.Context, _ string, msgs []providers.Message) error {
	out := make([]mem0.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, mem0.Message{Role: m.Role, Content: m.Content})
	}
	return s.client.AddMessages(ctx, s.userID, s.agentID, out)
}

// Recall searches Mem0 for memories relevant to query, falling back to the most
// recent memories when search fails or no query is available.
func (s *mem0Sink) Recall(ctx context.Context, _ string, query string) (string, error) {
	limit := s.recallLimit
	if limit <= 0 {
		limit = config.DefaultMemoryRecallLimit
	}
	var (
		mems []mem0.Memory
		err  error
	)
	if strings.TrimSpace(query) != "" {
		mems, err = s.client.SearchMemories(ctx, query, s.userID, s.agentID, limit)
		if err != nil {
			mems, err = s.client.ListMemories(ctx, s.userID, s.agentID, limit)
		}
	} else {
		mems, err = s.client.ListMemories(ctx, s.userID, s.agentID, limit)
	}
	if err != nil {
		return "", err
	}
	items := make([]string, 0, len(mems))
	for _, m := range mems {
		if text := m.Text(); text != "" {
			items = append(items, text)
		}
	}
	if len(items) == 0 {
		return "", nil
	}
	return renderExternalRecall(recallGroup{Title: "What you know about the user", Items: items}), nil
}

// retainDBSink adapts a RetainDB client to the externalMemorySink and
// externalMemoryReader contracts.
type retainDBSink struct {
	client      *retaindb.Client
	userID      string
	recallLimit int
}

func (s *retainDBSink) Name() string { return "retaindb" }

func (s *retainDBSink) AddMessages(ctx context.Context, sessionKey string, msgs []providers.Message) error {
	out := make([]retaindb.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, retaindb.Message{Role: m.Role, Content: m.Content})
	}
	return s.client.IngestSession(ctx, s.userID, sessionKey, out)
}

// Recall combines the user profile with query-relevant memories, matching the
// Hermes RetainDB provider's prefetch overlay.
func (s *retainDBSink) Recall(ctx context.Context, sessionKey, query string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", nil
	}
	limit := s.recallLimit
	if limit <= 0 {
		limit = config.DefaultMemoryRecallLimit
	}

	searchMems, searchErr := s.client.SearchMemories(ctx, s.userID, sessionKey, query, limit)
	profileMems, profileErr := s.client.GetProfile(ctx, s.userID)
	if searchErr != nil && profileErr != nil {
		return "", searchErr
	}

	queryItems := make([]string, 0, len(searchMems))
	for _, m := range searchMems {
		if text := strings.TrimSpace(m.Content); text != "" {
			queryItems = append(queryItems, text)
		}
	}
	profileItems := make([]string, 0, len(profileMems))
	for _, m := range profileMems {
		if text := strings.TrimSpace(m.Content); text != "" {
			profileItems = append(profileItems, text)
		}
	}
	if len(queryItems) == 0 && len(profileItems) == 0 {
		return "", nil
	}
	return renderExternalRecall(
		recallGroup{Title: "Profile", Items: profileItems},
		recallGroup{Title: "Relevant memories", Items: queryItems},
	), nil
}
