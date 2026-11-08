package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers/common"
)

func TestResolveRetryAfterMode_ModelOverridesGlobal(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		global string
		want   string
	}{
		{"model wait beats global fallback", "wait", "fallback", RetryAfterWait},
		{"model fallback beats global wait", "fallback", "wait", RetryAfterFallback},
		{"unset model inherits global wait", "", "wait", RetryAfterWait},
		{"default marker inherits global", "default", "wait", RetryAfterWait},
		{"both unset defaults to fallback", "", "", RetryAfterFallback},
		{"garbage falls back", "nonsense", "", RetryAfterFallback},
		{"whitespace and case tolerated", "  WAIT  ", "", RetryAfterWait},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveRetryAfterMode(tc.model, tc.global); got != tc.want {
				t.Fatalf("ResolveRetryAfterMode(%q,%q) = %q, want %q", tc.model, tc.global, got, tc.want)
			}
		})
	}
}

func TestParseRetryAfter_Sources(t *testing.T) {
	if got := common.ParseRetryAfter("", ""); got != 0 {
		t.Fatalf("empty = %v, want 0", got)
	}
	if got := common.ParseRetryAfter("30", ""); got != 30*time.Second {
		t.Fatalf("seconds = %v, want 30s", got)
	}
	if got := common.ParseRetryAfter("not-a-date", ""); got != 0 {
		t.Fatalf("garbage = %v, want 0", got)
	}
	// Clamped to MaxRetryAfterDelay (105s).
	if got := common.ParseRetryAfter("9999", ""); got != common.MaxRetryAfterDelay {
		t.Fatalf("clamp = %v, want %v", got, common.MaxRetryAfterDelay)
	}
	if common.MaxRetryAfterDelay != 105*time.Second {
		t.Fatalf("MaxRetryAfterDelay = %v, want 105s", common.MaxRetryAfterDelay)
	}
}

func TestClassifyError_CarriesRetryAfterHeader(t *testing.T) {
	err := &common.HTTPError{
		StatusCode: http.StatusTooManyRequests,
		RetryAfter: 42 * time.Second,
	}
	got := ClassifyError(err, "openai", "gpt-4o-mini")
	if got == nil {
		t.Fatal("expected classification")
	}
	if got.Reason != FailoverRateLimit {
		t.Fatalf("reason = %q, want rate_limit", got.Reason)
	}
	if got.RetryAfter != 42*time.Second {
		t.Fatalf("RetryAfter = %v, want 42s", got.RetryAfter)
	}
}

func TestClassifyError_RetryAfterFromMessageBody(t *testing.T) {
	err := errors.New(`API request failed: Status: 429 Body: {"error":{"message":"rate limit","retry_after":12.5}}`)
	got := ClassifyError(err, "openai", "gpt-4o-mini")
	if got == nil || got.Reason != FailoverRateLimit {
		t.Fatalf("expected rate_limit, got %+v", got)
	}
	if got.RetryAfter != 12500*time.Millisecond {
		t.Fatalf("RetryAfter = %v, want 12.5s", got.RetryAfter)
	}
}

func TestClassifyError_IgnoresRetryAfterForNonRateLimit(t *testing.T) {
	err := errors.New(`Status: 500 Body: {"retry_after":30}`)
	got := ClassifyError(err, "openai", "gpt-4o-mini")
	if got == nil {
		t.Fatal("expected classification")
	}
	if got.RetryAfter != 0 {
		t.Fatalf("RetryAfter = %v, want 0 for non-rate-limit", got.RetryAfter)
	}
}

func TestFallback_RetryAfterWaitRetriesSameModel(t *testing.T) {
	fc := NewFallbackChain(NewCooldownTracker(), nil)
	candidate := makeCandidate("openai", "gpt-4o-mini")
	candidate.RetryAfterMode = RetryAfterWait

	attempts := 0
	run := func(ctx context.Context, c FallbackCandidate) (*LLMResponse, error) {
		attempts++
		if attempts == 1 {
			return nil, &common.HTTPError{
				StatusCode: http.StatusTooManyRequests,
				RetryAfter: 10 * time.Millisecond,
			}
		}
		return &LLMResponse{Content: "after wait", FinishReason: "stop"}, nil
	}

	start := time.Now()
	res, err := fc.ExecuteCandidate(context.Background(), []FallbackCandidate{candidate}, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Response.Content != "after wait" {
		t.Fatalf("content = %q", res.Response.Content)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (sleep then retry same model)", attempts)
	}
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Fatalf("elapsed = %v, want >= 10ms sleep", elapsed)
	}
	if len(res.Attempts) != 1 {
		t.Fatalf("recorded attempts = %d, want 1 failed attempt", len(res.Attempts))
	}
}

func TestFallback_RetryAfterWaitIgnoredWhenFallbackExists(t *testing.T) {
	fc := NewFallbackChain(NewCooldownTracker(), nil)
	primary := makeCandidate("openai", "gpt-4o-mini")
	primary.RetryAfterMode = RetryAfterWait
	secondary := makeCandidate("anthropic", "claude-haiku")

	var order []string
	run := func(ctx context.Context, c FallbackCandidate) (*LLMResponse, error) {
		order = append(order, c.Provider)
		if c.Provider == "openai" {
			return nil, &common.HTTPError{
				StatusCode: http.StatusTooManyRequests,
				RetryAfter: 500 * time.Millisecond,
			}
		}
		return &LLMResponse{Content: "from fallback", FinishReason: "stop"}, nil
	}

	start := time.Now()
	res, err := fc.ExecuteCandidate(context.Background(), []FallbackCandidate{primary, secondary}, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Provider != "anthropic" {
		t.Fatalf("provider = %q, want anthropic", res.Provider)
	}
	if fmt.Sprint(order) != "[openai anthropic]" {
		t.Fatalf("order = %v", order)
	}
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Fatalf("elapsed = %v: wait mode must not sleep when a fallback exists", elapsed)
	}
}

func TestFallback_RetryAfterWaitStopsAfterMaxAttempts(t *testing.T) {
	fc := NewFallbackChain(NewCooldownTracker(), nil)
	candidate := makeCandidate("openai", "gpt-4o-mini")
	candidate.RetryAfterMode = RetryAfterWait

	calls := 0
	run := func(ctx context.Context, c FallbackCandidate) (*LLMResponse, error) {
		calls++
		return nil, &common.HTTPError{
			StatusCode: http.StatusTooManyRequests,
			RetryAfter: time.Millisecond,
		}
	}

	_, err := fc.ExecuteCandidate(context.Background(), []FallbackCandidate{candidate}, run)
	if err == nil {
		t.Fatal("expected error when the model keeps rate limiting")
	}
	if calls != maxRetryAfterWaitAttempts+1 {
		t.Fatalf("calls = %d, want %d", calls, maxRetryAfterWaitAttempts+1)
	}
}

func TestFallback_RetryAfterFallbackModeDoesNotSleep(t *testing.T) {
	fc := NewFallbackChain(NewCooldownTracker(), nil)
	candidate := makeCandidate("openai", "gpt-4o-mini")
	candidate.RetryAfterMode = RetryAfterFallback

	start := time.Now()
	_, err := fc.ExecuteCandidate(context.Background(), []FallbackCandidate{candidate},
		func(ctx context.Context, c FallbackCandidate) (*LLMResponse, error) {
			return nil, &common.HTTPError{
				StatusCode: http.StatusTooManyRequests,
				RetryAfter: 5 * time.Second,
			}
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("elapsed = %v: fallback mode must not sleep", elapsed)
	}
}

func TestResolveRetryAfterMode_ConfigValidationRejectsUnknown(t *testing.T) {
	// Config-level guard: only default/wait/fallback are accepted.
	for _, bad := range []string{"waiting", "retry", "1"} {
		mc := configModelWithRetryAfter(bad)
		if err := mc.Validate(); err == nil {
			t.Fatalf("Validate() accepted retry_after=%q", bad)
		}
	}
	for _, ok := range []string{"", "default", "wait", "fallback"} {
		mc := configModelWithRetryAfter(ok)
		if err := mc.Validate(); err != nil {
			t.Fatalf("Validate() rejected retry_after=%q: %v", ok, err)
		}
	}
}

func configModelWithRetryAfter(value string) *config.ModelConfig {
	return &config.ModelConfig{
		ModelName:  "test",
		Provider:   "openai",
		Model:      "gpt-4o-mini",
		RetryAfter: value,
	}
}
