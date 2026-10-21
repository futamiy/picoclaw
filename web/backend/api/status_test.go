package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/pid"
)

func TestGatewayProbeForwardsTokenAndDecodesResult(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"model":    map[string]any{"ok": true},
			"channels": map[string]any{"telegram": map[string]any{"running": true}},
		})
	}))
	defer server.Close()

	host, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	gateway.mu.Lock()
	gateway.pidData = &pid.PidFileData{PID: 1, Token: "secret", Port: port, Host: host}
	gateway.mu.Unlock()
	t.Cleanup(func() {
		gateway.mu.Lock()
		gateway.pidData = nil
		gateway.mu.Unlock()
	})

	h := NewHandler(filepath.Join(t.TempDir(), "config.json"))
	result, ok := h.gatewayProbe(context.Background(), config.DefaultConfig()).(map[string]any)
	if !ok {
		t.Fatalf("gatewayProbe returned %T, want map", result)
	}
	if _, hasErr := result["error"]; hasErr {
		t.Fatalf("gatewayProbe error: %v", result["error"])
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("Authorization = %q, want Bearer secret", gotAuth)
	}
	if _, ok := result["model"]; !ok {
		t.Fatalf("result = %v, want model key", result)
	}
}

func TestGatewayProbeReportsUnreachableGateway(t *testing.T) {
	gateway.mu.Lock()
	gateway.pidData = &pid.PidFileData{PID: 1, Token: "secret", Port: 1, Host: "127.0.0.1"}
	gateway.mu.Unlock()
	t.Cleanup(func() {
		gateway.mu.Lock()
		gateway.pidData = nil
		gateway.mu.Unlock()
	})

	h := NewHandler(filepath.Join(t.TempDir(), "config.json"))
	result, ok := h.gatewayProbe(context.Background(), config.DefaultConfig()).(map[string]any)
	if !ok {
		t.Fatalf("gatewayProbe returned %T, want map", result)
	}
	if _, hasErr := result["error"]; !hasErr {
		t.Fatalf("result = %v, want error key", result)
	}
}
