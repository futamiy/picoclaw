package config

import "testing"

func TestMemoryConfig_EffectiveMemoryProvider(t *testing.T) {
	cases := []struct {
		name string
		mem  MemoryConfig
		want string
	}{
		{"default empty -> local", MemoryConfig{}, MemoryProviderLocal},
		{"explicit local", MemoryConfig{Provider: MemoryProviderLocal}, MemoryProviderLocal},
		{"explicit mem0", MemoryConfig{Provider: MemoryProviderMem0}, MemoryProviderMem0},
		{"explicit retaindb", MemoryConfig{Provider: " RetainDB "}, MemoryProviderRetainDB},
		{"explicit honcho", MemoryConfig{Provider: MemoryProviderHoncho}, MemoryProviderHoncho},
		{"unknown -> local", MemoryConfig{Provider: "bogus"}, MemoryProviderLocal},
		{
			"legacy honcho enabled with empty provider",
			MemoryConfig{Honcho: HonchoMemoryConfig{Enabled: true}},
			MemoryProviderHoncho,
		},
		{
			"explicit local overrides legacy honcho enabled",
			MemoryConfig{Provider: MemoryProviderLocal, Honcho: HonchoMemoryConfig{Enabled: true}},
			MemoryProviderLocal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.mem.EffectiveMemoryProvider(); got != tc.want {
				t.Fatalf("EffectiveMemoryProvider() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMem0RetainDB_RecallHelpers(t *testing.T) {
	var m Mem0MemoryConfig
	if got := m.EffectiveRecallMode(); got != DefaultMemoryRecallMode {
		t.Fatalf("mem0 default recall = %q, want %q", got, DefaultMemoryRecallMode)
	}
	if got := m.EffectiveRecallMode(); got != HonchoRecallHybrid {
		t.Fatalf("mem0 default recall = %q, want hybrid", got)
	}
	if !m.InjectsContext() {
		t.Fatal("mem0 recall should inject context by default")
	}
	if !m.ExposesTools() {
		t.Fatal("mem0 recall should expose tools by default")
	}
	if got := m.EffectiveContextCadence(); got != DefaultMemoryContextCadence {
		t.Fatalf("mem0 cadence = %d", got)
	}
	if got := m.EffectiveRecallLimit(); got != DefaultMemoryRecallLimit {
		t.Fatalf("mem0 limit = %d", got)
	}
	m = Mem0MemoryConfig{RecallMode: "off"}
	if m.InjectsContext() || m.ExposesTools() {
		t.Fatal("mem0 recall off still recalls")
	}
	m = Mem0MemoryConfig{RecallMode: "context"}
	if !m.InjectsContext() || m.ExposesTools() {
		t.Fatal("mem0 context mode should inject without tools")
	}
	m = Mem0MemoryConfig{RecallMode: "tools"}
	if m.InjectsContext() || !m.ExposesTools() {
		t.Fatal("mem0 tools mode should expose tools without injecting")
	}
	m = Mem0MemoryConfig{RecallMode: "bogus"}
	if m.InjectsContext() || m.ExposesTools() {
		t.Fatal("unknown mem0 recall mode must not recall")
	}
	m = Mem0MemoryConfig{RecallMode: " hybrid ", ContextCadence: 3, RecallLimit: 4}
	if got := m.EffectiveRecallMode(); got != HonchoRecallHybrid {
		t.Fatalf("mem0 hybrid mode = %q", got)
	}
	if !m.InjectsContext() {
		t.Fatal("mem0 hybrid should inject")
	}
	if m.EffectiveContextCadence() != 3 || m.EffectiveRecallLimit() != 4 {
		t.Fatalf("mem0 overrides not honored: cadence=%d limit=%d", m.EffectiveContextCadence(), m.EffectiveRecallLimit())
	}

	var r RetainDBMemoryConfig
	if got := r.EffectiveUserID(); got != DefaultHonchoUserPeerID {
		t.Fatalf("retaindb default user = %q", got)
	}
	if !r.InjectsContext() || !r.ExposesTools() {
		t.Fatal("retaindb recall should inject and expose tools by default")
	}
	r = RetainDBMemoryConfig{UserID: "u1", RecallMode: "off", ContextCadence: 2, RecallLimit: 7}
	if r.InjectsContext() || r.ExposesTools() {
		t.Fatal("retaindb recall off still recalls")
	}
	if r.EffectiveUserID() != "u1" || r.EffectiveContextCadence() != 2 || r.EffectiveRecallLimit() != 7 {
		t.Fatalf("retaindb overrides not honored: %+v", r)
	}
}

func TestMem0RetainDB_EffectiveHelpers(t *testing.T) {
	var m Mem0MemoryConfig
	if got := m.EffectiveBaseURL(); got != DefaultMem0BaseURL {
		t.Fatalf("mem0 base url = %q, want %q", got, DefaultMem0BaseURL)
	}
	if got := m.EffectiveSyncEveryTurns(); got != DefaultMemorySyncEveryTurns {
		t.Fatalf("mem0 sync turns = %d, want %d", got, DefaultMemorySyncEveryTurns)
	}
	m = Mem0MemoryConfig{BaseURL: "https://m.example.com/", SyncEveryTurns: 3}
	if got := m.EffectiveBaseURL(); got != "https://m.example.com" {
		t.Fatalf("mem0 trimmed base url = %q", got)
	}
	if got := m.EffectiveSyncEveryTurns(); got != 3 {
		t.Fatalf("mem0 sync turns = %d, want 3", got)
	}

	var r RetainDBMemoryConfig
	if got := r.EffectiveBaseURL(); got != "" {
		t.Fatalf("retaindb base url = %q, want empty", got)
	}
	r = RetainDBMemoryConfig{BaseURL: "https://r.example.com/"}
	if got := r.EffectiveBaseURL(); got != "https://r.example.com" {
		t.Fatalf("retaindb trimmed base url = %q", got)
	}
}
