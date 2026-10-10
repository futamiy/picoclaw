package honcho

import (
	"testing"
	"time"
)

func TestPendingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	b := PendingBatch{
		AgentID:      "agent-1",
		SessionKey:   "sess/with/slashes",
		Messages:     []Message{{Content: "hi", PeerID: "user"}},
		ScannedTurns: 3,
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
	}
	if err := WritePending(dir, b); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	got, err := FindPending(dir, b.AgentID, b.SessionKey)
	if err != nil || got == nil {
		t.Fatalf("FindPending: %v %v", got, err)
	}
	if got.ScannedTurns != 3 || len(got.Messages) != 1 || got.Messages[0].Content != "hi" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	list, err := ListPending(dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListPending: %v %v", list, err)
	}
	if err := DeletePending(dir, b.AgentID, b.SessionKey); err != nil {
		t.Fatalf("DeletePending: %v", err)
	}
	if got, _ := FindPending(dir, b.AgentID, b.SessionKey); got != nil {
		t.Fatalf("expected deleted, got %+v", got)
	}
}
