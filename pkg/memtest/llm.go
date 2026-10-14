package memtest

import (
	"context"
	"sync"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// LLM is a scripted providers.LLMProvider. It returns queued responses in
// order (repeating the last once exhausted) and records every prompt it was
// asked to complete, so extraction tests can assert on the transcript fed to
// the curator.
type LLM struct {
	mu sync.Mutex

	// responses are returned round-robin. If empty, a benign empty response is
	// returned so callers don't nil-deref.
	responses []string

	// prompts records the concatenated message content of each Chat call.
	prompts []string

	// err, when set, is returned from every Chat call. Useful for testing the
	// extraction pipeline's retry/skip path.
	err error

	calls int
	model string
}

// NewLLM builds a scripted LLM returning the given responses in order.
func NewLLM(responses ...string) *LLM {
	if len(responses) == 0 {
		responses = []string{""}
	}
	return &LLM{responses: responses, model: "memtest-model"}
}

// SetError makes every subsequent Chat call fail with err.
func (l *LLM) SetError(err error) {
	l.mu.Lock()
	l.err = err
	l.mu.Unlock()
}

// Calls returns the number of Chat invocations.
func (l *LLM) Calls() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// Prompts returns the recorded prompt texts in call order.
func (l *LLM) Prompts() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.prompts))
	copy(out, l.prompts)
	return out
}

// Chat implements providers.LLMProvider.
func (l *LLM) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var prompt string
	for _, m := range messages {
		prompt += m.Content
	}
	l.prompts = append(l.prompts, prompt)

	idx := l.calls
	l.calls++
	if l.err != nil {
		return nil, l.err
	}
	if idx >= len(l.responses) {
		idx = len(l.responses) - 1
	}
	return &providers.LLMResponse{Content: l.responses[idx]}, nil
}

// GetDefaultModel implements providers.LLMProvider.
func (l *LLM) GetDefaultModel() string { return l.model }
