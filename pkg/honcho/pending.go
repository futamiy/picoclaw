package honcho

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// PendingBatch is a transcript batch staged for user approval before it is
// pushed to Honcho (the "ask" trigger mode).
type PendingBatch struct {
	AgentID      string    `json:"agent_id"`
	SessionKey   string    `json:"session_key"`
	Messages     []Message `json:"messages"`
	ScannedTurns int       `json:"scanned_turns"`
	CreatedAt    time.Time `json:"created_at"`
}

// PendingDir returns the directory where staged batches live. It is rooted at
// the agent workspace so both the agent process and the web backend resolve
// the same path.
func PendingDir(workspace string) string {
	return filepath.Join(workspace, "memory", "honcho-pending")
}

func pendingPath(workspace, agentID, sessionKey string) string {
	name := SanitizeID(agentID) + "-" + SanitizeID(sessionKey) + ".json"
	return filepath.Join(PendingDir(workspace), name)
}

// WritePending stages a batch, replacing any previous batch for the same
// agent/session.
func WritePending(workspace string, b PendingBatch) error {
	dir := PendingDir(workspace)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pendingPath(workspace, b.AgentID, b.SessionKey), data, 0o600)
}

// DeletePending removes the staged batch for an agent/session (after approve or
// dismiss). Missing files are not an error.
func DeletePending(workspace, agentID, sessionKey string) error {
	err := os.Remove(pendingPath(workspace, agentID, sessionKey))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ListPending returns all staged batches, newest first.
func ListPending(workspace string) ([]PendingBatch, error) {
	entries, err := os.ReadDir(PendingDir(workspace))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	batches := make([]PendingBatch, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(PendingDir(workspace), entry.Name()))
		if err != nil {
			continue
		}
		var b PendingBatch
		if err := json.Unmarshal(data, &b); err != nil {
			continue
		}
		batches = append(batches, b)
	}
	sort.Slice(batches, func(i, j int) bool {
		return batches[i].CreatedAt.After(batches[j].CreatedAt)
	})
	return batches, nil
}

// FindPending returns the staged batch for an agent/session, if any.
func FindPending(workspace, agentID, sessionKey string) (*PendingBatch, error) {
	data, err := os.ReadFile(pendingPath(workspace, agentID, sessionKey))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read pending: %w", err)
	}
	var b PendingBatch
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("decode pending: %w", err)
	}
	return &b, nil
}
