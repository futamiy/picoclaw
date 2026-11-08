package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestResolveModelCandidate_InheritsGlobalRetryAfter(t *testing.T) {
	cfg := &config.Config{
		ModelList: []*config.ModelConfig{
			{ModelName: "primary", Provider: "openai", Model: "gpt-4o-mini", Enabled: true},
			{ModelName: "patient", Provider: "openai", Model: "gpt-4.1", Enabled: true, RetryAfter: "wait"},
			{ModelName: "eager", Provider: "openai", Model: "gpt-4.1-mini", Enabled: true, RetryAfter: "fallback"},
		},
	}
	cfg.Agents.Defaults.RetryAfter = "wait"

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{"no per-model setting inherits global wait", "primary", "wait"},
		{"explicit wait stays wait", "patient", "wait"},
		{"explicit fallback overrides global wait", "eager", "fallback"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate, ok := resolveModelCandidate(cfg, "openai", tc.ref)
			if !ok {
				t.Fatalf("resolveModelCandidate(%q) failed", tc.ref)
			}
			if candidate.RetryAfterMode != tc.want {
				t.Fatalf("RetryAfterMode = %q, want %q", candidate.RetryAfterMode, tc.want)
			}
		})
	}
}

func TestResolveModelCandidate_DefaultsToFallbackWithoutGlobal(t *testing.T) {
	cfg := &config.Config{
		ModelList: []*config.ModelConfig{
			{ModelName: "plain", Provider: "openai", Model: "gpt-4o-mini", Enabled: true},
		},
	}
	candidate, ok := resolveModelCandidate(cfg, "openai", "plain")
	if !ok {
		t.Fatal("resolveModelCandidate failed")
	}
	if candidate.RetryAfterMode != "fallback" {
		t.Fatalf("RetryAfterMode = %q, want fallback", candidate.RetryAfterMode)
	}
}

func TestResolveModelCandidate_RawRefUsesGlobalRetryAfter(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agents.Defaults.RetryAfter = "wait"

	candidate, ok := resolveModelCandidate(cfg, "openai", "openai/gpt-4o-mini")
	if !ok {
		t.Fatal("resolveModelCandidate failed")
	}
	if candidate.RetryAfterMode != "wait" {
		t.Fatalf("RetryAfterMode = %q, want wait", candidate.RetryAfterMode)
	}
}
