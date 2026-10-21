package channels

import (
	"context"
	"time"
)

// ChannelHealth is the passive connectivity view of one channel: whether it is
// running plus the last successful/failed outbound attempt.
type ChannelHealth struct {
	Enabled      bool       `json:"enabled"`
	Running      bool       `json:"running"`
	LastSentAt   *time.Time `json:"last_sent_at,omitempty"`
	LastErrorAt  *time.Time `json:"last_error_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	ProbeError   string     `json:"probe_error,omitempty"`
	ProbedAt     *time.Time `json:"probed_at,omitempty"`
	ProbeLatency int64      `json:"probe_latency_ms,omitempty"`
}

func (m *Manager) recordChannelSent(name string) {
	now := time.Now()
	m.mutateChannelHealth(name, func(h *ChannelHealth) {
		h.LastSentAt = &now
		h.LastError = ""
		h.LastErrorAt = nil
	})
}

func (m *Manager) recordChannelError(name string, err error) {
	if err == nil {
		return
	}
	now := time.Now()
	m.mutateChannelHealth(name, func(h *ChannelHealth) {
		h.LastErrorAt = &now
		h.LastError = err.Error()
	})
}

func (m *Manager) mutateChannelHealth(name string, fn func(*ChannelHealth)) {
	v, _ := m.channelHealth.Load(name)
	h, ok := v.(ChannelHealth)
	if !ok {
		h = ChannelHealth{}
	}
	if ch, exists := m.GetChannel(name); exists {
		h.Enabled = true
		h.Running = ch.IsRunning()
	}
	fn(&h)
	m.channelHealth.Store(name, h)
}

// ChannelHealth returns the passive health snapshot for every configured
// channel. It performs no network calls.
func (m *Manager) ChannelHealth() map[string]ChannelHealth {
	m.mu.RLock()
	names := make([]string, 0, len(m.channels))
	for name := range m.channels {
		names = append(names, name)
	}
	m.mu.RUnlock()

	out := make(map[string]ChannelHealth, len(names))
	for _, name := range names {
		v, _ := m.channelHealth.Load(name)
		h, _ := v.(ChannelHealth)
		h.Enabled = true
		if ch, ok := m.GetChannel(name); ok {
			h.Running = ch.IsRunning()
		}
		out[name] = h
	}
	return out
}

// ProbeChannel actively checks connectivity for one channel. Channels that do
// not implement ProbeCapable report only their running state.
func (m *Manager) ProbeChannel(ctx context.Context, name string) ChannelHealth {
	ch, ok := m.GetChannel(name)
	now := time.Now()
	if !ok {
		return ChannelHealth{ProbedAt: &now, ProbeError: "channel not found"}
	}

	h := ChannelHealth{Enabled: true, Running: ch.IsRunning()}
	prober, ok := ch.(ProbeCapable)
	if !ok {
		h.ProbedAt = &now
		if !h.Running {
			h.ProbeError = "channel not running"
		}
		m.storeProbeResult(name, h)
		return h
	}

	start := time.Now()
	err := prober.Probe(ctx)
	h.ProbeLatency = time.Since(start).Milliseconds()
	h.ProbedAt = &now
	if err != nil {
		h.ProbeError = err.Error()
	}
	m.storeProbeResult(name, h)
	return h
}

// storeProbeResult merges a probe result into the passive health entry so the
// last activity/error history survives re-probes.
func (m *Manager) storeProbeResult(name string, probe ChannelHealth) {
	v, _ := m.channelHealth.Load(name)
	prev, _ := v.(ChannelHealth)
	probe.LastSentAt = prev.LastSentAt
	probe.LastErrorAt = prev.LastErrorAt
	probe.LastError = prev.LastError
	m.channelHealth.Store(name, probe)
}
