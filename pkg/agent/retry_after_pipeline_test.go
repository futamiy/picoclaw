package agent

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/providers/common"
)

func TestRetryAfterWaitDelay_AppliesForSingleWaitCandidate(t *testing.T) {
	exec := &turnExecution{
		activeCandidates: []providers.FallbackCandidate{
			{Provider: "openai", Model: "gpt-4o-mini", RetryAfterMode: providers.RetryAfterWait},
		},
	}
	err := &common.HTTPError{StatusCode: http.StatusTooManyRequests, RetryAfter: 40 * time.Second}

	delay, ok := retryAfterWaitDelay(exec, err, "rate_limit")
	if !ok {
		t.Fatal("expected retry_after delay to be applied")
	}
	if delay != 40*time.Second {
		t.Fatalf("delay = %v, want 40s", delay)
	}
}

func TestRetryAfterWaitDelay_ClampsTo105s(t *testing.T) {
	exec := &turnExecution{
		activeCandidates: []providers.FallbackCandidate{
			{Provider: "openai", Model: "gpt-4o-mini", RetryAfterMode: providers.RetryAfterWait},
		},
	}
	err := &common.HTTPError{StatusCode: http.StatusTooManyRequests, RetryAfter: 10 * time.Minute}

	delay, ok := retryAfterWaitDelay(exec, err, "rate_limit")
	if !ok {
		t.Fatal("expected delay")
	}
	if delay != common.MaxRetryAfterDelay {
		t.Fatalf("delay = %v, want %v", delay, common.MaxRetryAfterDelay)
	}
}

func TestRetryAfterWaitDelay_IgnoresFallbackModeAndMultipleCandidates(t *testing.T) {
	err := &common.HTTPError{StatusCode: http.StatusTooManyRequests, RetryAfter: 30 * time.Second}

	fallbackMode := &turnExecution{
		activeCandidates: []providers.FallbackCandidate{
			{Provider: "openai", Model: "gpt-4o-mini", RetryAfterMode: providers.RetryAfterFallback},
		},
	}
	if _, ok := retryAfterWaitDelay(fallbackMode, err, "rate_limit"); ok {
		t.Fatal("fallback mode must not wait")
	}

	twoCandidates := &turnExecution{
		activeCandidates: []providers.FallbackCandidate{
			{Provider: "openai", Model: "gpt-4o-mini", RetryAfterMode: providers.RetryAfterWait},
			{Provider: "anthropic", Model: "claude-haiku", RetryAfterMode: providers.RetryAfterFallback},
		},
	}
	if _, ok := retryAfterWaitDelay(twoCandidates, err, "rate_limit"); ok {
		t.Fatal("wait must be ignored when a fallback exists")
	}
}

func TestRetryAfterWaitDelay_NoHeaderUsesDefaultWait(t *testing.T) {
	exec := &turnExecution{
		activeCandidates: []providers.FallbackCandidate{
			{Provider: "openai", Model: "gpt-4o-mini", RetryAfterMode: providers.RetryAfterWait},
		},
	}
	err := errors.New("HTTP 429: status: 429 rate limit exceeded")
	delay, ok := retryAfterWaitDelay(exec, err, "rate_limit")
	if !ok {
		t.Fatal("wait mode must still wait when the provider sends no Retry-After")
	}
	if delay != providers.DefaultRetryAfterWait() {
		t.Fatalf("delay = %v, want %v", delay, providers.DefaultRetryAfterWait())
	}
}

func TestRetryAfterWaitDelay_UsesExponentialOutsideWaitMode(t *testing.T) {
	exec := &turnExecution{
		activeCandidates: []providers.FallbackCandidate{
			{Provider: "openai", Model: "gpt-4o-mini", RetryAfterMode: providers.RetryAfterFallback},
		},
	}
	err := &common.HTTPError{StatusCode: http.StatusTooManyRequests, RetryAfter: 30 * time.Second}
	if _, ok := retryAfterWaitDelay(exec, err, "rate_limit"); ok {
		t.Fatal("fallback mode must not override the retry loop backoff")
	}
}
