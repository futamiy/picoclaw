package gateway

import (
	"context"
	"time"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// probeTimeout bounds the whole POST /probe request so a slow provider cannot
// hold the HTTP handler open.
const probeTimeout = 20 * time.Second

// modelProbeTimeout bounds the model round-trip inside the probe.
const modelProbeTimeout = 15 * time.Second

type modelProbeResult struct {
	OK        bool   `json:"ok"`
	Name      string `json:"name,omitempty"`
	Model     string `json:"model,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

// connectivityProbe builds the handler behind POST /probe: it actively tests the
// configured model and every channel, plus the passive channel health snapshot.
func connectivityProbe(al *agent.AgentLoop, cm *channels.Manager) func(context.Context) any {
	return func(ctx context.Context) any {
		ctx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()

		out := map[string]any{
			"checked_at": time.Now().Format(time.RFC3339),
		}

		if al != nil {
			if registry := al.GetRegistry(); registry != nil {
				if def := registry.GetDefaultAgent(); def != nil {
					out["model"] = probeModel(ctx, def)
				}
			}
		}

		if cm != nil {
			health := cm.ChannelHealth()
			for name := range health {
				health[name] = cm.ProbeChannel(ctx, name)
			}
			out["channels"] = health
		}

		return out
	}
}

func probeModel(ctx context.Context, agentInstance *agent.AgentInstance) modelProbeResult {
	res := modelProbeResult{
		Name:  agentInstance.ID,
		Model: agentInstance.Model,
	}
	if agentInstance.Provider == nil {
		res.Error = "no provider configured"
		return res
	}

	probeCtx, cancel := context.WithTimeout(ctx, modelProbeTimeout)
	defer cancel()

	start := time.Now()
	_, err := agentInstance.Provider.Chat(
		probeCtx,
		[]providers.Message{{Role: "user", Content: "ping"}},
		nil,
		agentInstance.Model,
		map[string]any{"max_tokens": 1},
	)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	return res
}
