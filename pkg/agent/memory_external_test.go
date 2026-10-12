package agent

import (
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestBuildExternalMemoryTranscript(t *testing.T) {
	history := []providers.Message{
		{Role: "system", Content: "ignored"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "  "},
		{Role: "assistant", Content: "world"},
		{Role: "tool", Content: "ignored"},
	}
	got := buildExternalMemoryTranscript(history)
	if len(got) != 2 {
		t.Fatalf("kept %d messages, want 2: %+v", len(got), got)
	}
	if got[0].Role != "user" || got[0].Content != "hello" {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "world" {
		t.Fatalf("second = %+v", got[1])
	}
}

func TestBuildExternalMemoryTranscript_Budget(t *testing.T) {
	huge := strings.Repeat("x", maxHonchoTranscriptRunes)
	history := []providers.Message{
		{Role: "user", Content: "small"},
		{Role: "user", Content: huge},
		{Role: "user", Content: "also small"},
	}
	got := buildExternalMemoryTranscript(history)
	if len(got) != 2 {
		t.Fatalf("kept %d messages, want 2 (budget should drop the huge one)", len(got))
	}
}
