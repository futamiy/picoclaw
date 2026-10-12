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
