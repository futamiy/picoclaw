package agent

import (
	"errors"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
)

func TestIsRecoverableProcessingError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"transient eof", errors.New("stream error: unexpected EOF"), true},
		{"connection reset", errors.New("read tcp: connection reset by peer"), true},
		{"timeout", errors.New("context deadline exceeded: request timeout"), true},
		{"context length", errors.New("400 context_length_exceeded"), true},
		{"token limit", errors.New("maximum token limit reached"), true},
		{"empty response", errors.New("empty response from provider"), true},
		{"auth failure", errors.New("401 unauthorized: invalid api key"), false},
		{"permanent", errors.New("400 bad request: malformed payload"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRecoverableProcessingError(tc.err); got != tc.want {
				t.Fatalf("isRecoverableProcessingError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRecoverableActionsFor(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	// Tool-limit response is a success path (err == nil) but must still get an action.
	actions := al.recoverableActionsFor(toolLimitResponse, nil)
	if len(actions) != 1 || actions[0].Type != bus.ActionContinue {
		t.Fatalf("expected a continue action for the tool-limit response, got %#v", actions)
	}

	// A normal answer gets no action.
	if a := al.recoverableActionsFor("Here is your answer.", nil); len(a) != 0 {
		t.Fatalf("expected no actions for a normal answer, got %#v", a)
	}

	// A recoverable error gets an action even when there is no response text.
	if a := al.recoverableActionsFor("", errors.New("unexpected EOF")); len(a) != 1 {
		t.Fatalf("expected a continue action for a transient error, got %#v", a)
	}

	// A permanent error gets no action.
	if a := al.recoverableActionsFor("", errors.New("401 unauthorized")); len(a) != 0 {
		t.Fatalf("expected no actions for a permanent error, got %#v", a)
	}

	// Disabling the feature suppresses all actions.
	cfg.Agents.Defaults.ErrorActions.ContinueButton = boolPtr(false)
	if a := al.recoverableActionsFor(toolLimitResponse, nil); len(a) != 0 {
		t.Fatalf("expected no actions when disabled, got %#v", a)
	}
}
