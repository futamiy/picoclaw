package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestBuildHonchoTranscript_RoleToPeer(t *testing.T) {
	history := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "tool", Content: "tool output"},
		{Role: "user", Content: "   "},
	}
	msgs := buildHonchoTranscript(history, "user-peer", "agent-1")
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].PeerID != "user-peer" || msgs[0].Content != "hello" {
		t.Errorf("user message: %+v", msgs[0])
	}
	if msgs[1].PeerID != "agent-1" || msgs[1].Content != "hi there" {
		t.Errorf("assistant message: %+v", msgs[1])
	}
}

func TestBuildHonchoTranscript_SanitizesEmptyPeer(t *testing.T) {
	msgs := buildHonchoTranscript(
		[]providers.Message{{Role: "user", Content: "x"}, {Role: "assistant", Content: "y"}},
		"", "",
	)
	if len(msgs) != 2 || msgs[0].PeerID != "user" || msgs[1].PeerID != "assistant" {
		t.Fatalf("fallback peers wrong: %+v", msgs)
	}
}
