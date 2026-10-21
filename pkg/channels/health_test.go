package channels

import (
	"context"
	"errors"
	"testing"
	"time"
)

// probeStubChannel is a mockChannel that also implements ProbeCapable.
type probeStubChannel struct {
	mockChannel
	probeErr error
	probed   bool
}

func (c *probeStubChannel) Probe(context.Context) error {
	c.probed = true
	return c.probeErr
}

func newProbeStub(name string, running bool, probeErr error) *probeStubChannel {
	ch := &probeStubChannel{probeErr: probeErr}
	ch.name = name
	ch.SetRunning(running)
	return ch
}

func newPlainStub(name string, running bool) *mockChannel {
	ch := &mockChannel{}
	ch.name = name
	ch.SetRunning(running)
	return ch
}

func newHealthManager(channels ...Channel) *Manager {
	m := &Manager{channels: make(map[string]Channel, len(channels))}
	for _, ch := range channels {
		m.channels[ch.Name()] = ch
	}
	return m
}

func TestProbeChannel_RecordsSuccessAndFailure(t *testing.T) {
	ok := newProbeStub("stub", true, nil)
	bad := newProbeStub("bad", true, errors.New("lookup api.telegram.org: i/o timeout"))
	m := newHealthManager(ok, bad)

	if got := m.ProbeChannel(context.Background(), "stub"); got.ProbeError != "" || got.ProbedAt == nil {
		t.Fatalf("expected successful probe, got %+v", got)
	}
	if got := m.ProbeChannel(context.Background(), "bad"); got.ProbeError == "" {
		t.Fatalf("expected probe error, got %+v", got)
	}
	if !ok.probed || !bad.probed {
		t.Fatal("expected Probe to be called on both channels")
	}

	health := m.ChannelHealth()
	if health["stub"].ProbeError != "" || !health["stub"].Running {
		t.Fatalf("stub health = %+v", health["stub"])
	}
	if health["bad"].ProbeError == "" {
		t.Fatalf("bad health = %+v", health["bad"])
	}
}

func TestProbeChannel_FallsBackToRunningState(t *testing.T) {
	m := newHealthManager(newPlainStub("plain", false))

	if got := m.ProbeChannel(context.Background(), "plain"); got.ProbeError != "channel not running" {
		t.Fatalf("expected not-running error, got %+v", got)
	}
}

func TestChannelHealth_TracksLastSentAndError(t *testing.T) {
	m := newHealthManager(newPlainStub("plain", true))

	m.recordChannelError("plain", errors.New("send failed"))
	health := m.ChannelHealth()["plain"]
	if health.LastError == "" || health.LastErrorAt == nil {
		t.Fatalf("expected recorded error, got %+v", health)
	}

	m.recordChannelSent("plain")
	health = m.ChannelHealth()["plain"]
	if health.LastSentAt == nil {
		t.Fatalf("expected last sent timestamp, got %+v", health)
	}
	if health.LastError != "" {
		t.Fatalf("expected error cleared after successful send, got %+v", health)
	}
}

func TestProbeChannel_PreservesLastSentAcrossProbe(t *testing.T) {
	m := newHealthManager(newProbeStub("stub", true, nil))

	m.recordChannelSent("stub")
	before := m.ChannelHealth()["stub"].LastSentAt
	if before == nil {
		t.Fatal("expected last sent timestamp")
	}

	time.Sleep(time.Millisecond)
	m.ProbeChannel(context.Background(), "stub")
	if got := m.ChannelHealth()["stub"].LastSentAt; got == nil || !got.Equal(*before) {
		t.Fatalf("last sent changed across probe: %v -> %v", before, got)
	}
}
