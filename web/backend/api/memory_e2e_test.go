package api

// End-to-end tests for the web-facing memory API. They exercise the real HTTP
// handlers (via the same mux wiring production uses) against fake memory
// provider servers from pkg/memtest, plus a config round-trip on disk.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/memtest"
)

func newMemoryAPI(t *testing.T) (http.Handler, string, func()) {
	t.Helper()
	configPath, cleanup := setupOAuthTestEnv(t)
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux, configPath, cleanup
}

func TestE2E_WebMemoryConfig_RoundTrip(t *testing.T) {
	mux, configPath, cleanup := newMemoryAPI(t)
	defer cleanup()

	// Update memory config via the API.
	body := `{
		"enabled": true,
		"provider": "mem0",
		"long_term_char_limit": 1234,
		"daily_notes_enabled": true,
		"daily_notes_days": 5,
		"daily_note_char_limit": 999,
		"extract_enabled": true,
		"extract_every_turns": 7,
		"mem0": {"enabled": true, "base_url": "http://mem0.local", "user_id": "u9", "agent_id": "a9", "sync_every_turns": 3}
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/tools/memory-config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// The change must be durable on disk.
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	m := cfg.Agents.Defaults.Memory
	if !m.Enabled || m.EffectiveMemoryProvider() != config.MemoryProviderMem0 {
		t.Fatalf("provider not persisted: enabled=%v provider=%q", m.Enabled, m.Provider)
	}
	if m.EffectiveLongTermCharLimit() != 1234 || m.EffectiveExtractEveryTurns() != 7 {
		t.Fatalf("limits not persisted: %+v", m)
	}
	if m.Mem0.UserID != "u9" || m.Mem0.AgentID != "a9" || m.Mem0.EffectiveSyncEveryTurns() != 3 {
		t.Fatalf("mem0 config not persisted: %+v", m.Mem0)
	}

	// Read it back through the API.
	getReq := httptest.NewRequest(http.MethodGet, "/api/tools/memory-config", nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d", getRec.Code)
	}
	var resp memoryConfigResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode get response: %v (body=%s)", err, getRec.Body.String())
	}
	if resp.Provider != config.MemoryProviderMem0 || !resp.Enabled {
		t.Fatalf("get response mismatch: %+v", resp)
	}
	if resp.LongTermCharLimit != 1234 || resp.Mem0.UserID != "u9" {
		t.Fatalf("get response limits mismatch: %+v", resp)
	}
}

func TestE2E_WebMemoryTest_AllProviders(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
	}{
		{"honcho", config.MemoryProviderHoncho},
		{"mem0", config.MemoryProviderMem0},
		{"retaindb", config.MemoryProviderRetainDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := memtest.NewServer()
			srv := httptest.NewServer(fs)
			defer srv.Close()

			mux, _, cleanup := newMemoryAPI(t)
			defer cleanup()

			body, _ := json.Marshal(map[string]any{
				"provider": tc.provider,
				"base_url": srv.URL,
				"api_key":  "test-key",
			})
			req := httptest.NewRequest(http.MethodPost, "/api/tools/memory/test", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s test status = %d, body=%s", tc.provider, rec.Code, rec.Body.String())
			}
			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if ok, _ := resp["ok"].(bool); !ok {
				t.Fatalf("%s ping reported not ok: %+v", tc.provider, resp)
			}
			// The handler must have reached the fake server's ping endpoint.
			if fs.Requests() == nil {
				t.Fatalf("%s: fake server saw no request", tc.provider)
			}
		})
	}
}

func TestE2E_WebMemoryTest_UnreachableProvider(t *testing.T) {
	fs := memtest.NewServer()
	fs.SetPingStatus(http.StatusBadGateway)
	srv := httptest.NewServer(fs)
	defer srv.Close()

	mux, _, cleanup := newMemoryAPI(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]any{
		"provider": config.MemoryProviderMem0,
		"base_url": srv.URL,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/tools/memory/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for unreachable provider, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("expected ok=false body, got %s", rec.Body.String())
	}
}

func TestE2E_WebMemoryTest_UnsupportedProvider(t *testing.T) {
	mux, _, cleanup := newMemoryAPI(t)
	defer cleanup()

	body := `{"provider":"not-a-real-provider"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tools/memory/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unsupported provider, got %d body=%s", rec.Code, rec.Body.String())
	}
}
