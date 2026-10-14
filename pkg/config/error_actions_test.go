package config

import (
	"testing"
)

func TestIsContinueButtonEnabled_DefaultsTrueWhenUnset(t *testing.T) {
	var d AgentDefaults
	if !d.IsContinueButtonEnabled() {
		t.Fatal("expected continue button to default to enabled when unset")
	}
	if !DefaultContinueButtonEnabled {
		t.Fatal("expected DefaultContinueButtonEnabled to be true")
	}
}

func TestIsContinueButtonEnabled_ExplicitValues(t *testing.T) {
	enabled := AgentDefaults{ErrorActions: ErrorActionsConfig{ContinueButton: boolPtr(true)}}
	if !enabled.IsContinueButtonEnabled() {
		t.Fatal("expected continue button enabled when explicitly true")
	}

	disabled := AgentDefaults{ErrorActions: ErrorActionsConfig{ContinueButton: boolPtr(false)}}
	if disabled.IsContinueButtonEnabled() {
		t.Fatal("expected continue button disabled when explicitly false")
	}
}

func TestDefaultConfig_ContinueButtonEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Agents.Defaults.IsContinueButtonEnabled() {
		t.Fatal("expected default config to enable the continue button")
	}
}

func TestChannel_IsFinalAsNewMessageEnabled(t *testing.T) {
	var ch Channel
	if ch.IsFinalAsNewMessageEnabled() {
		t.Fatal("expected final-as-new-message to default to false")
	}

	ch.Placeholder.FinalAsNewMessage = true
	if !ch.IsFinalAsNewMessageEnabled() {
		t.Fatal("expected final-as-new-message enabled when set")
	}
}
