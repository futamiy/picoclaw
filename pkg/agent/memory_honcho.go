package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/honcho"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

const (
	honchoSyncEveryTurnsMin = 1
	// maxHonchoTranscriptRunes bounds how much conversation is sent to Honcho.
	maxHonchoTranscriptRunes = 20000
	// honchoPrefetchTimeout bounds a single read/reasoning call. Kept short so
	// background prefetches never pile up on a slow server.
	honchoPrefetchTimeout = 8 * time.Second
	// maxHonchoContextRunes bounds the injected memory block.
	maxHonchoContextRunes = 4000
	// maxHonchoDialecticRunes bounds the dialectic (LLM reasoning) supplement.
	maxHonchoDialecticRunes = 600
	// maxHonchoCacheKeys bounds the per-session prefetch cache.
	maxHonchoCacheKeys = 64
)

// HonchoSyncer syncs conversation transcripts to a Honcho server at a turn
// cadence. When the config specifies "ask", batches are staged for user approval
// (written to disk) instead of being pushed directly.
//
// When recall is enabled it also prefetches memory back from Honcho in the
// background and caches it per session so turns never block on the network.
type HonchoSyncer struct {
	agent       *AgentInstance
	client      *honcho.Client
	workspaceID string
	userPeerID  string
	agentPeerID string
	everyTurns  int
	triggerAuto bool // true if trigger is "auto"; else ask mode (stage only)
	sanitize    func(string) string

	// Recall configuration.
	recallEnabled    bool
	contextCadence   int
	dialecticCadence int
	dialecticLevel   string
	contextTokens    int

	countMu sync.Mutex
	counts  map[string]int // sessionKey -> message count at last sync
	syncing sync.Map       // "agentID:sessionKey" -> bool, dedup in-flight

	prefetchMu sync.Mutex
	prefetch   map[string]*memoryPrefetchEntry
	// loop is captured lazily from MaybeSync so background prefetches can emit
	// events. Guarded by prefetchMu.
	loop *AgentLoop
}

// memoryPrefetchEntry is the per-session cached recall block shared by the
// Honcho and generic external syncers. It is guarded by the syncer's
// prefetchMu.
type memoryPrefetchEntry struct {
	content       string
	lastTurn      int
	lastDialectic int
	inFlight      bool
}

func newHonchoSyncer(agent *AgentInstance, client *honcho.Client, hcfg honchoConfig, sanitize func(string) string) *HonchoSyncer {
	every := hcfg.EveryTurns
	if every < honchoSyncEveryTurnsMin {
		every = honchoSyncEveryTurnsMin
	}
	wid := strings.TrimSpace(hcfg.WorkspaceID)
	if wid == "" {
		wid = agent.ID
	}
	agentPeer := agent.ID
	if hcfg.AgentPeerID != "" {
		agentPeer = hcfg.AgentPeerID
	}
	ctxCadence := hcfg.ContextCadence
	if ctxCadence < 1 {
		ctxCadence = 1
	}
	diaCadence := hcfg.DialecticCadence
	if diaCadence < 1 {
		diaCadence = 1
	}
	return &HonchoSyncer{
		agent:            agent,
		client:           client,
		workspaceID:      wid,
		userPeerID:       hcfg.UserPeerID,
		agentPeerID:      agentPeer,
		everyTurns:       every,
		triggerAuto:      hcfg.TriggerAuto,
		sanitize:         sanitize,
		recallEnabled:    hcfg.RecallEnabled,
		contextCadence:   ctxCadence,
		dialecticCadence: diaCadence,
		dialecticLevel:   hcfg.DialecticLevel,
		contextTokens:    hcfg.ContextTokens,
		counts:           make(map[string]int),
		prefetch:         make(map[string]*memoryPrefetchEntry),
	}
}

type honchoConfig struct {
	WorkspaceID      string
	UserPeerID       string
	AgentPeerID      string
	TriggerAuto      bool
	EveryTurns       int
	RecallEnabled    bool
	ContextCadence   int
	DialecticCadence int
	DialecticLevel   string
	ContextTokens    int
}

// MemoryProviderName identifies the backend for the injected prompt block.
func (hs *HonchoSyncer) MemoryProviderName() string { return "honcho" }

// RecallContext returns the cached Honcho memory block for sessionKey, kicking
// off a background refresh when the cadence is met. It never blocks on the
// network: a miss returns "" until a background fetch completes. The base
// context read is not query-ranked, but the dialectic supplement uses query.
func (hs *HonchoSyncer) RecallContext(sessionKey, query string) string {
	if hs == nil || !hs.recallEnabled || sessionKey == "" {
		return ""
	}
	hs.maybePrefetch(sessionKey, query)

	hs.prefetchMu.Lock()
	defer hs.prefetchMu.Unlock()
	if e := hs.prefetch[sessionKey]; e != nil {
		return e.content
	}
	return ""
}

// maybePrefetch schedules a background context fetch for sessionKey if the
// cadence has elapsed and no fetch is already in flight. When the dialectic
// cadence is also met, the fetch adds Honcho's LLM reasoning supplement.
func (hs *HonchoSyncer) maybePrefetch(sessionKey, query string) {
	if hs.client == nil {
		return
	}
	historyLen := 0
	if hs.agent != nil && hs.agent.Sessions != nil {
		historyLen = len(hs.agent.Sessions.GetHistory(sessionKey))
	}

	// Dialectic calls hit Honcho's LLM, so they run on their own cadence and
	// only when there is a query to reason about.
	wantDialectic := hs.dialecticCadence > 0 &&
		strings.TrimSpace(hs.dialecticLevel) != "" &&
		strings.TrimSpace(query) != ""

	hs.prefetchMu.Lock()
	e := hs.prefetch[sessionKey]
	if e == nil {
		e = &memoryPrefetchEntry{lastDialectic: -hs.dialecticCadence}
		if len(hs.prefetch) >= maxHonchoCacheKeys {
			hs.evictOldestLocked()
		}
		hs.prefetch[sessionKey] = e
	}
	if e.inFlight {
		hs.prefetchMu.Unlock()
		return
	}
	wantDialectic = wantDialectic && historyLen-e.lastDialectic >= hs.dialecticCadence
	if e.content != "" && historyLen-e.lastTurn < hs.contextCadence && !wantDialectic {
		hs.prefetchMu.Unlock()
		return
	}
	e.inFlight = true
	e.lastTurn = historyLen
	hs.prefetchMu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.WarnCF("agent", "Honcho prefetch panic recovered", map[string]any{
					"session_key": sessionKey,
					"panic":       r,
				})
			}
		}()

		content, usedDialectic := hs.fetchContext(sessionKey, query, wantDialectic)

		hs.prefetchMu.Lock()
		if entry := hs.prefetch[sessionKey]; entry != nil {
			entry.inFlight = false
			if content != "" {
				entry.content = content
			}
			if usedDialectic {
				entry.lastDialectic = historyLen
			}
		}
		al := hs.loop
		hs.prefetchMu.Unlock()

		if content != "" {
			logger.DebugCF("agent", "Honcho context prefetched", map[string]any{
				"session_key": sessionKey,
				"chars":       len([]rune(content)),
				"dialectic":   usedDialectic,
			})
			if al != nil {
				al.emitEvent(
					runtimeevents.KindAgentMemoryHonchoPrefetched,
					al.newTurnEventScope(hs.agent.ID, sessionKey, nil).
						meta(0, "honchoPrefetch", "turn.memory.honcho.prefetched"),
					HonchoPrefetchedPayload{
						SessionKey:  sessionKey,
						AgentID:     hs.agent.ID,
						WorkspaceID: hs.workspaceID,
						Chars:       len([]rune(content)),
					},
				)
			}
		}
	}()
}

// evictOldestLocked drops one cache entry to bound memory. The map iteration
// order is random; dropping any entry is acceptable since they are rebuilt
// on demand.
func (hs *HonchoSyncer) evictOldestLocked() {
	for k := range hs.prefetch {
		delete(hs.prefetch, k)
		return
	}
}

// fetchContext builds the injected block: the base context (session summary +
// peer representation) plus, when requested, the dialectic supplement. It
// reports whether a dialectic answer was included so the caller can advance the
// dialectic cadence.
func (hs *HonchoSyncer) fetchContext(sessionKey, query string, dialectic bool) (string, bool) {
	base := hs.fetchBaseContext(sessionKey)
	if !dialectic {
		return base, false
	}
	answer := hs.fetchDialectic(sessionKey, query)
	if answer == "" {
		return base, false
	}
	combined := strings.TrimSpace(base)
	if combined != "" {
		combined += "\n\n"
	}
	combined += "Reasoning about what matters here:\n" + answer
	return truncateHonchoRunes(combined, maxHonchoContextRunes), true
}

// fetchDialectic asks Honcho to reason over the user's stored model for the
// current query. Failures are logged and surfaced as an empty string.
func (hs *HonchoSyncer) fetchDialectic(sessionKey, query string) string {
	if hs.client == nil || strings.TrimSpace(query) == "" || strings.TrimSpace(hs.dialecticLevel) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), honchoPrefetchTimeout)
	defer cancel()

	answer, err := hs.client.Chat(ctx, hs.workspaceID, hs.agentPeerID, honcho.ChatOptions{
		Query:          query,
		SessionID:      sessionKey,
		Target:         hs.userPeerID,
		ReasoningLevel: hs.dialecticLevel,
	})
	if err != nil {
		logger.DebugCF("agent", "Honcho dialectic fetch failed", map[string]any{
			"session_key": sessionKey,
			"error":       err,
		})
		return ""
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}
	return truncateHonchoRunes(answer, maxHonchoDialecticRunes)
}

// fetchBaseContext pulls the session summary and peer representation and
// renders a compact memory block. Errors are logged and surfaced as an empty
// string.
func (hs *HonchoSyncer) fetchBaseContext(sessionKey string) string {
	ctx, cancel := context.WithTimeout(context.Background(), honchoPrefetchTimeout)
	defer cancel()

	// Prefer the session context (summary + representation) when available;
	// fall back to the peer representation alone.
	if sc, err := hs.client.GetSessionContext(ctx, hs.workspaceID, sessionKey, honcho.SessionContextOptions{
		Tokens:     hs.contextTokens,
		Summary:    true,
		PeerTarget: hs.userPeerID,
	}); err == nil && sc != nil {
		if block := renderHonchoContext(sc.Summary, sc.PeerRepr, sc.PeerCards); block != "" {
			return block
		}
	} else if err != nil {
		logger.DebugCF("agent", "Honcho session context fetch failed", map[string]any{
			"session_key": sessionKey,
			"error":       err,
		})
	}

	pc, err := hs.client.GetPeerContext(ctx, hs.workspaceID, hs.agentPeerID, hs.userPeerID)
	if err != nil {
		logger.DebugCF("agent", "Honcho peer context fetch failed", map[string]any{
			"session_key": sessionKey,
			"error":       err,
		})
		return ""
	}
	return renderHonchoContext(nil, pc.Representation, pc.PeerCard)
}

// renderHonchoContext composes the injected block from the available pieces,
// returning "" when nothing useful was retrieved.
func renderHonchoContext(summary *honcho.Summary, representation string, cards []string) string {
	var b strings.Builder
	if summary != nil && strings.TrimSpace(summary.Content) != "" {
		b.WriteString("Session summary:\n")
		b.WriteString(strings.TrimSpace(summary.Content))
		b.WriteString("\n")
	}
	if r := strings.TrimSpace(representation); r != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("What you know about the user:\n")
		b.WriteString(r)
		b.WriteString("\n")
	}
	if len(cards) > 0 {
		items := make([]string, 0, len(cards))
		for _, c := range cards {
			if s := strings.TrimSpace(c); s != "" {
				items = append(items, "- "+s)
			}
		}
		if len(items) > 0 {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString("User card:\n")
			b.WriteString(strings.Join(items, "\n"))
			b.WriteString("\n")
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return ""
	}
	return truncateHonchoRunes(out, maxHonchoContextRunes)
}

func truncateHonchoRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// MaybeSync pushes staged conversations to Honcho when the cadence is
// met. If in "ask" mode, it writes a pending batch to disk and emits an
// awaiting-approval event instead of pushing.
func (hs *HonchoSyncer) MaybeSync(al *AgentLoop, sessionKey string) {
	agent := hs.agent
	if al == nil || agent == nil || agent.Sessions == nil || hs.client == nil {
		return
	}
	if hs.recallEnabled {
		hs.prefetchMu.Lock()
		hs.loop = al
		hs.prefetchMu.Unlock()
	}
	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) == 0 {
		return
	}

	hs.countMu.Lock()
	last := hs.counts[sessionKey]
	hs.countMu.Unlock()
	if len(history)-last < hs.everyTurns {
		return
	}

	extractKey := agent.ID + ":" + sessionKey
	if _, loading := hs.syncing.LoadOrStore(extractKey, true); loading {
		return
	}

	go func() {
		defer hs.syncing.Delete(extractKey)
		defer func() {
			if r := recover(); r != nil {
				logger.WarnCF("agent", "Honcho sync panic recovered", map[string]any{
					"session_key": sessionKey,
					"panic":       r,
				})
			}
		}()

		transcriptMsgs := buildHonchoTranscript(history, hs.userPeerID, hs.agentPeerID)
		if len(transcriptMsgs) == 0 {
			hs.countMu.Lock()
			hs.counts[sessionKey] = len(history)
			hs.countMu.Unlock()
			return
		}
		if hs.sanitize != nil {
			for i := range transcriptMsgs {
				transcriptMsgs[i].Content = hs.sanitize(transcriptMsgs[i].Content)
			}
		}

		if !hs.triggerAuto {
			batch := honcho.PendingBatch{
				AgentID:      agent.ID,
				SessionKey:   sessionKey,
				Messages:     transcriptMsgs,
				ScannedTurns: len(history),
				CreatedAt:    time.Now().UTC(),
			}
			if err := honcho.WritePending(agent.Workspace, batch); err != nil {
				logger.WarnCF("agent", "Honcho pending write failed", map[string]any{
					"session_key": sessionKey,
					"error":       err,
				})
				return
			}
			hs.countMu.Lock()
			hs.counts[sessionKey] = len(history)
			hs.countMu.Unlock()
			al.emitEvent(
				runtimeevents.KindAgentMemoryHonchoAwaitingApproval,
				al.newTurnEventScope(agent.ID, sessionKey, nil).meta(0, "honchoSync", "turn.memory.honcho.awaiting_approval"),
				HonchoAwaitingApprovalPayload{
					SessionKey:   sessionKey,
					AgentID:      agent.ID,
					MessageCount: len(transcriptMsgs),
					ScannedTurns: len(history),
					CreatedAt:    batch.CreatedAt.Format(time.RFC3339Nano),
					WorkspaceID:  hs.workspaceID,
				},
			)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := hs.client.Ensure(ctx, hs.workspaceID, sessionKey, hs.userPeerID, hs.agentPeerID); err != nil {
			logger.WarnCF("agent", "Honcho ensure failed; skipping", map[string]any{
				"session_key": sessionKey,
				"error":       err,
			})
			return
		}
		if err := hs.client.AddMessages(ctx, hs.workspaceID, sessionKey, transcriptMsgs); err != nil {
			logger.WarnCF("agent", "Honcho add messages failed; skipping", map[string]any{
				"session_key": sessionKey,
				"error":       err,
			})
			return
		}
		hs.countMu.Lock()
		hs.counts[sessionKey] = len(history)
		hs.countMu.Unlock()
		al.emitEvent(
			runtimeevents.KindAgentMemoryHonchoSynced,
			al.newTurnEventScope(agent.ID, sessionKey, nil).meta(0, "honchoSync", "turn.memory.honcho.synced"),
			HonchoSyncedPayload{
				SessionKey:   sessionKey,
				AgentID:      agent.ID,
				MessageCount: len(transcriptMsgs),
				WorkspaceID:  hs.workspaceID,
			},
		)
	}()
}

func buildHonchoTranscript(history []providers.Message, userPeerID, agentPeerID string) []honcho.Message {
	out := make([]honcho.Message, 0, len(history))
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
		var peer string
		if msg.Role == "assistant" {
			peer = honcho.SanitizeID(defaultIfEmpty(agentPeerID, "assistant"))
		} else {
			peer = honcho.SanitizeID(defaultIfEmpty(userPeerID, "user"))
		}
		out = append(out, honcho.Message{Content: content, PeerID: peer})
	}
	return out
}

func defaultIfEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
