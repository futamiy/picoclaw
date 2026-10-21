package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// statusProbeTimeout bounds the launcher's call to the gateway /probe endpoint.
const statusProbeTimeout = 25 * time.Second

// registerStatusRoutes binds connectivity status endpoints.
func (h *Handler) registerStatusRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status/connectivity", h.handleConnectivityStatus)
}

// handleConnectivityStatus reports gateway reachability and, when ?probe=1 is
// set, actively tests the model and every channel through the gateway.
//
//	GET /api/status/connectivity[?probe=1]
func (h *Handler) handleConnectivityStatus(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.LoadConfig(h.configPath)

	result := map[string]any{
		"checked_at": time.Now().Format(time.RFC3339),
		"gateway":    h.gatewayConnectivity(cfg),
	}

	if r.URL.Query().Get("probe") == "1" {
		result["probe"] = h.gatewayProbe(r.Context(), cfg)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *Handler) gatewayConnectivity(cfg *config.Config) map[string]any {
	out := map[string]any{"ok": false}

	start := time.Now()
	health, statusCode, err := h.getGatewayHealth(cfg, 3*time.Second)
	latency := time.Since(start).Milliseconds()
	out["latency_ms"] = latency
	out["status_code"] = statusCode

	if err != nil {
		out["error"] = err.Error()
		return out
	}
	out["ok"] = true
	out["status"] = health.Status
	out["uptime"] = health.Uptime
	if health.PID != 0 {
		out["pid"] = health.PID
	}
	return out
}

// gatewayProbe forwards POST /probe to the gateway, authenticating with the
// token from the pid file. Returns an error object when the gateway is down.
func (h *Handler) gatewayProbe(ctx context.Context, cfg *config.Config) any {
	gateway.mu.Lock()
	pidData := gateway.pidData
	port := 0
	host := ""
	if pidData != nil {
		port = pidData.Port
		host = gatewayProbeHost(pidData.Host)
	}
	gateway.mu.Unlock()

	if port == 0 {
		port = 18790
		if cfg != nil && cfg.Gateway.Port != 0 {
			port = cfg.Gateway.Port
		}
	}
	if host == "" {
		host = gatewayProbeHost(h.effectiveGatewayBindHost(cfg))
	}

	ctx, cancel := context.WithTimeout(ctx, statusProbeTimeout)
	defer cancel()

	url := "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/probe"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(nil))
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	if pidData != nil && pidData.Token != "" {
		req.Header.Set("Authorization", "Bearer "+pidData.Token)
	}

	resp, err := (&http.Client{Timeout: statusProbeTimeout}).Do(req)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	defer resp.Body.Close()

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return map[string]any{"error": "invalid probe response: " + err.Error()}
	}
	return payload
}
