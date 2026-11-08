package config

import (
	"encoding/json"
	"testing"
)

func TestRetryAfter_LoadsFromJSON(t *testing.T) {
	raw := `{
	  "agents": {"defaults": {"retry_after": "wait"}},
	  "model_list": [
	    {"model_name":"a","provider":"openai","model":"gpt-4o-mini"},
	    {"model_name":"b","provider":"openai","model":"gpt-4.1","retry_after":"fallback"}
	  ]
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Agents.Defaults.RetryAfter != "wait" {
		t.Fatalf("global retry_after = %q, want wait", cfg.Agents.Defaults.RetryAfter)
	}
	if len(cfg.ModelList) != 2 {
		t.Fatalf("model_list len = %d", len(cfg.ModelList))
	}
	if cfg.ModelList[0].RetryAfter != "" {
		t.Fatalf("model a retry_after = %q, want empty (inherit)", cfg.ModelList[0].RetryAfter)
	}
	if cfg.ModelList[1].RetryAfter != "fallback" {
		t.Fatalf("model b retry_after = %q, want fallback", cfg.ModelList[1].RetryAfter)
	}
	if err := cfg.ModelList[1].Validate(); err != nil {
		t.Fatalf("validate b: %v", err)
	}
}
