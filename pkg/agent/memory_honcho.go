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
)

// HonchoSyncer syncs conversation transcripts to a Honcho server at a turn
// cadence. When the config specifies "ask", batches are staged for user approval
// (written to disk) instead of being pushed directly.
type HonchoSyncer struct {
	agent       *AgentInstance
	client      *honcho.Client
	workspaceID string
	userPeerID  string
	agentPeerID string
	everyTurns  int
	triggerAuto bool // true if trigger is "auto"; else ask mode (stage only)
	sanitize    func(string) string

	countMu sync.Mutex
	counts  map[string]int // sessionKey -> message count at last sync
	syncing sync.Map       // "agentID:sessionKey" -> bool, dedup in-flight
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
	return &HonchoSyncer{
		agent:       agent,
		client:      client,
		workspaceID: wid,
		userPeerID:  hcfg.UserPeerID,
		agentPeerID: agentPeer,
		everyTurns:  every,
		triggerAuto: hcfg.TriggerAuto,
		sanitize:    sanitize,
		counts:      make(map[string]int),
	}
}

type honchoConfig struct {
	WorkspaceID string
	UserPeerID  string
	TriggerAuto bool
	EveryTurns  int
}

// MaybeSyncHoncho pushes staged conversations to Honcho when the cadence is
// met. If in "ask" mode, it writes a pending batch to disk and emits an
// awaiting-approval event instead of pushing.
func (hs *HonchoSyncer) MaybeSyncHoncho(al *AgentLoop, sessionKey string) {
	agent := hs.agent
	if al == nil || agent == nil || agent.Sessions == nil || hs.client == nil {
		return
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
