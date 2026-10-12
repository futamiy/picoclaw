package config

import (
	"encoding/json"
	"testing"
)

func TestEffectiveAutoCompress_DefaultTrue(t *testing.T) {
	d := AgentDefaults{}
	if !d.EffectiveAutoCompress() {
		t.Fatalf("EffectiveAutoCompress() = false, want true when unset")
	}
	// nil pointer explicitly set
	d.Experimental.AutoCompress = nil
	if !d.EffectiveAutoCompress() {
		t.Fatalf("EffectiveAutoCompress() = false, want true when nil")
	}
}

func TestEffectiveAutoCompress_Explicit(t *testing.T) {
	enabled := true
	disabled := false

	on := AgentDefaults{Experimental: ExperimentalConfig{AutoCompress: &enabled}}
	if !on.EffectiveAutoCompress() {
		t.Fatalf("EffectiveAutoCompress() = false, want true when explicitly enabled")
	}

	off := AgentDefaults{Experimental: ExperimentalConfig{AutoCompress: &disabled}}
	if off.EffectiveAutoCompress() {
		t.Fatalf("EffectiveAutoCompress() = true, want false when explicitly disabled")
	}
}

func TestDefaultConfig_AutoCompressEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Agents.Defaults.EffectiveAutoCompress() {
		t.Fatalf("default config should have auto-compress enabled")
	}
}

func TestExperimentalConfig_Unmarshal(t *testing.T) {
	var d AgentDefaults
	if err := json.Unmarshal([]byte(`{"experimental":{"auto_compress":false}}`), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.EffectiveAutoCompress() {
		t.Fatalf("EffectiveAutoCompress() = true, want false after JSON unmarshal")
	}

	var d2 AgentDefaults
	if err := json.Unmarshal([]byte(`{}`), &d2); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if !d2.EffectiveAutoCompress() {
		t.Fatalf("EffectiveAutoCompress() = false, want true when absent")
	}
}
