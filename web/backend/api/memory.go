package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/honcho"
	"github.com/sipeed/picoclaw/pkg/mem0"
	"github.com/sipeed/picoclaw/pkg/retaindb"
)

type memoryConfigResponse struct {
	Enabled            bool                   `json:"enabled"`
	Provider           string                 `json:"provider"`
	LongTermCharLimit  int                    `json:"long_term_char_limit"`
	DailyNotesEnabled  bool                   `json:"daily_notes_enabled"`
	DailyNotesDays     int                    `json:"daily_notes_days"`
	DailyNoteCharLimit int                    `json:"daily_note_char_limit"`
	ExtractEnabled     bool                   `json:"extract_enabled"`
	ExtractEveryTurns  int                    `json:"extract_every_turns"`
	ExtractModel       string                 `json:"extract_model,omitempty"`
	Honcho             honchoConfigResponse   `json:"honcho"`
	Mem0               mem0ConfigResponse     `json:"mem0"`
	RetainDB           retaindbConfigResponse `json:"retaindb"`
}

type honchoConfigResponse struct {
	Enabled          bool   `json:"enabled"`
	BaseURL          string `json:"base_url,omitempty"`
	APIKeySet        bool   `json:"api_key_set"`
	WorkspaceID      string `json:"workspace_id,omitempty"`
	UserPeerID       string `json:"user_peer_id,omitempty"`
	AgentPeerID      string `json:"agent_peer_id,omitempty"`
	Trigger          string `json:"trigger"`
	SyncEveryTurns   int    `json:"sync_every_turns"`
	RecallMode       string `json:"recall_mode"`
	ContextCadence   int    `json:"context_cadence"`
	DialecticCadence int    `json:"dialectic_cadence"`
	DialecticLevel   string `json:"dialectic_level"`
	ContextTokens    int    `json:"context_tokens"`
}

type mem0ConfigResponse struct {
	Enabled        bool   `json:"enabled"`
	BaseURL        string `json:"base_url,omitempty"`
	APIKeySet      bool   `json:"api_key_set"`
	UserID         string `json:"user_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	SyncEveryTurns int    `json:"sync_every_turns"`
}

type retaindbConfigResponse struct {
	Enabled        bool   `json:"enabled"`
	BaseURL        string `json:"base_url,omitempty"`
	APIKeySet      bool   `json:"api_key_set"`
	Namespace      string `json:"namespace,omitempty"`
	SyncEveryTurns int    `json:"sync_every_turns"`
}

type memoryConfigRequest struct {
	Enabled            bool                  `json:"enabled"`
	Provider           string                `json:"provider"`
	LongTermCharLimit  int                   `json:"long_term_char_limit"`
	DailyNotesEnabled  bool                  `json:"daily_notes_enabled"`
	DailyNotesDays     int                   `json:"daily_notes_days"`
	DailyNoteCharLimit int                   `json:"daily_note_char_limit"`
	ExtractEnabled     bool                  `json:"extract_enabled"`
	ExtractEveryTurns  int                   `json:"extract_every_turns"`
	ExtractModel       string                `json:"extract_model"`
	Honcho             honchoConfigRequest   `json:"honcho"`
	Mem0               mem0ConfigRequest     `json:"mem0"`
	RetainDB           retaindbConfigRequest `json:"retaindb"`
}

type honchoConfigRequest struct {
	Enabled          bool   `json:"enabled"`
	BaseURL          string `json:"base_url"`
	APIKey           string `json:"api_key"`
	ClearAPIKey      bool   `json:"clear_api_key"`
	WorkspaceID      string `json:"workspace_id"`
	UserPeerID       string `json:"user_peer_id"`
	AgentPeerID      string `json:"agent_peer_id"`
	Trigger          string `json:"trigger"`
	SyncEveryTurns   int    `json:"sync_every_turns"`
	RecallMode       string `json:"recall_mode"`
	ContextCadence   int    `json:"context_cadence"`
	DialecticCadence int    `json:"dialectic_cadence"`
	DialecticLevel   string `json:"dialectic_level"`
	ContextTokens    int    `json:"context_tokens"`
}

type mem0ConfigRequest struct {
	Enabled        bool   `json:"enabled"`
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	ClearAPIKey    bool   `json:"clear_api_key"`
	UserID         string `json:"user_id"`
	AgentID        string `json:"agent_id"`
	SyncEveryTurns int    `json:"sync_every_turns"`
}

type retaindbConfigRequest struct {
	Enabled        bool   `json:"enabled"`
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	ClearAPIKey    bool   `json:"clear_api_key"`
	Namespace      string `json:"namespace"`
	SyncEveryTurns int    `json:"sync_every_turns"`
}

type honchoPendingSummary struct {
	AgentID      string `json:"agent_id"`
	SessionKey   string `json:"session_key"`
	MessageCount int    `json:"message_count"`
	ScannedTurns int    `json:"scanned_turns"`
	CreatedAt    string `json:"created_at"`
	Preview      string `json:"preview,omitempty"`
}

type honchoPendingRequest struct {
	AgentID    string `json:"agent_id"`
	SessionKey string `json:"session_key"`
}

type honchoTestRequest struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

type memoryTestRequest struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
}

func (h *Handler) handleGetMemoryConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(buildMemoryConfigResponse(cfg)); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

func (h *Handler) handleUpdateMemoryConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	var req memoryConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	mem := &cfg.Agents.Defaults.Memory
	mem.Enabled = req.Enabled
	cfg.Tools.Memory.Enabled = req.Enabled
	if req.LongTermCharLimit > 0 {
		cfg.Agents.Defaults.Memory.LongTermCharLimit = req.LongTermCharLimit
	}
	cfg.Agents.Defaults.Memory.DailyNotesEnabled = req.DailyNotesEnabled
	if req.DailyNotesDays > 0 {
		cfg.Agents.Defaults.Memory.DailyNotesDays = req.DailyNotesDays
	}
	if req.DailyNoteCharLimit > 0 {
		cfg.Agents.Defaults.Memory.DailyNoteCharLimit = req.DailyNoteCharLimit
	}
	cfg.Agents.Defaults.Memory.ExtractEnabled = req.ExtractEnabled
	if req.ExtractEveryTurns > 0 {
		cfg.Agents.Defaults.Memory.ExtractEveryTurns = req.ExtractEveryTurns
	}
	cfg.Agents.Defaults.Memory.ExtractModel = strings.TrimSpace(req.ExtractModel)

	if provider := strings.ToLower(strings.TrimSpace(req.Provider)); provider != "" {
		mem.Provider = normalizeMemoryProvider(provider)
	}

	trigger := strings.TrimSpace(req.Honcho.Trigger)
	if trigger != config.HonchoTriggerAuto && trigger != config.HonchoTriggerAsk {
		trigger = config.HonchoTriggerAsk
	}
	cfg.Agents.Defaults.Memory.Honcho.Enabled = req.Honcho.Enabled
	cfg.Agents.Defaults.Memory.Honcho.BaseURL = strings.TrimSpace(req.Honcho.BaseURL)
	cfg.Agents.Defaults.Memory.Honcho.WorkspaceID = strings.TrimSpace(req.Honcho.WorkspaceID)
	cfg.Agents.Defaults.Memory.Honcho.UserPeerID = strings.TrimSpace(req.Honcho.UserPeerID)
	cfg.Agents.Defaults.Memory.Honcho.Trigger = trigger
	if req.Honcho.SyncEveryTurns > 0 {
		cfg.Agents.Defaults.Memory.Honcho.SyncEveryTurns = req.Honcho.SyncEveryTurns
	}
	cfg.Agents.Defaults.Memory.Honcho.AgentPeerID = strings.TrimSpace(req.Honcho.AgentPeerID)
	cfg.Agents.Defaults.Memory.Honcho.RecallMode = normalizeHonchoRecallMode(req.Honcho.RecallMode)
	cfg.Agents.Defaults.Memory.Honcho.ContextCadence = req.Honcho.ContextCadence
	cfg.Agents.Defaults.Memory.Honcho.DialecticCadence = req.Honcho.DialecticCadence
	cfg.Agents.Defaults.Memory.Honcho.DialecticLevel = normalizeHonchoDialecticLevel(req.Honcho.DialecticLevel)
	cfg.Agents.Defaults.Memory.Honcho.ContextTokens = req.Honcho.ContextTokens
	if req.Honcho.ClearAPIKey {
		cfg.Agents.Defaults.Memory.Honcho.APIKey = ""
	} else if key := strings.TrimSpace(req.Honcho.APIKey); key != "" {
		cfg.Agents.Defaults.Memory.Honcho.APIKey = key
	}

	cfg.Agents.Defaults.Memory.Mem0.Enabled = req.Mem0.Enabled
	cfg.Agents.Defaults.Memory.Mem0.BaseURL = strings.TrimSpace(req.Mem0.BaseURL)
	cfg.Agents.Defaults.Memory.Mem0.UserID = strings.TrimSpace(req.Mem0.UserID)
	cfg.Agents.Defaults.Memory.Mem0.AgentID = strings.TrimSpace(req.Mem0.AgentID)
	if req.Mem0.SyncEveryTurns > 0 {
		cfg.Agents.Defaults.Memory.Mem0.SyncEveryTurns = req.Mem0.SyncEveryTurns
	}
	if req.Mem0.ClearAPIKey {
		cfg.Agents.Defaults.Memory.Mem0.APIKey = ""
	} else if key := strings.TrimSpace(req.Mem0.APIKey); key != "" {
		cfg.Agents.Defaults.Memory.Mem0.APIKey = key
	}

	cfg.Agents.Defaults.Memory.RetainDB.Enabled = req.RetainDB.Enabled
	cfg.Agents.Defaults.Memory.RetainDB.BaseURL = strings.TrimSpace(req.RetainDB.BaseURL)
	cfg.Agents.Defaults.Memory.RetainDB.Namespace = strings.TrimSpace(req.RetainDB.Namespace)
	if req.RetainDB.SyncEveryTurns > 0 {
		cfg.Agents.Defaults.Memory.RetainDB.SyncEveryTurns = req.RetainDB.SyncEveryTurns
	}
	if req.RetainDB.ClearAPIKey {
		cfg.Agents.Defaults.Memory.RetainDB.APIKey = ""
	} else if key := strings.TrimSpace(req.RetainDB.APIKey); key != "" {
		cfg.Agents.Defaults.Memory.RetainDB.APIKey = key
	}

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) handleListHonchoPending(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	// Pending files live under the default agent workspace so the agent and the
	// web backend agree on the location. Per-agent workspaces are not consulted.
	batches, err := honcho.ListPending(cfg.WorkspacePath())
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list pending: %v", err), http.StatusInternalServerError)
		return
	}
	summaries := make([]honchoPendingSummary, 0, len(batches))
	for _, b := range batches {
		summaries = append(summaries, honchoPendingSummary{
			AgentID:      b.AgentID,
			SessionKey:   b.SessionKey,
			MessageCount: len(b.Messages),
			ScannedTurns: b.ScannedTurns,
			CreatedAt:    b.CreatedAt.Format(time.RFC3339),
			Preview:      honchoPreview(b.Messages),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"pending": summaries})
}

func (h *Handler) handleApproveHoncho(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	var req honchoPendingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	workspace := cfg.WorkspacePath()
	batch, err := honcho.FindPending(workspace, req.AgentID, req.SessionKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if batch == nil {
		http.Error(w, "pending batch not found", http.StatusNotFound)
		return
	}

	hcfg := cfg.Agents.Defaults.Memory.Honcho
	client := honcho.New(hcfg.EffectiveBaseURL(), hcfg.APIKey)
	workspaceID := strings.TrimSpace(hcfg.WorkspaceID)
	if workspaceID == "" {
		workspaceID = honcho.SanitizeID(batch.AgentID)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := client.Ensure(ctx, workspaceID, batch.SessionKey, hcfg.EffectiveUserPeerID(), honcho.SanitizeID(batch.AgentID)); err != nil {
		http.Error(w, fmt.Sprintf("Failed to reach Honcho: %v", err), http.StatusBadGateway)
		return
	}
	if err := client.AddMessages(ctx, workspaceID, batch.SessionKey, batch.Messages); err != nil {
		http.Error(w, fmt.Sprintf("Failed to send to Honcho: %v", err), http.StatusBadGateway)
		return
	}
	if err := honcho.DeletePending(workspace, req.AgentID, req.SessionKey); err != nil {
		http.Error(w, fmt.Sprintf("Sent but failed to clear pending: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) handleDismissHoncho(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	var req honchoPendingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if err := honcho.DeletePending(cfg.WorkspacePath(), req.AgentID, req.SessionKey); err != nil {
		http.Error(w, fmt.Sprintf("Failed to dismiss: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) handleTestHoncho(w http.ResponseWriter, r *http.Request) {
	baseURL := ""
	apiKey := ""
	if r.Body != nil {
		var req honchoTestRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		baseURL = strings.TrimSpace(req.BaseURL)
		apiKey = strings.TrimSpace(req.APIKey)
	}
	if baseURL == "" || apiKey == "" {
		cfg, err := config.LoadConfig(h.configPath)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
			return
		}
		hcfg := cfg.Agents.Defaults.Memory.Honcho
		if baseURL == "" {
			baseURL = hcfg.EffectiveBaseURL()
		}
		if apiKey == "" {
			apiKey = hcfg.APIKey
		}
	}
	client := honcho.New(baseURL, apiKey)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleTestMemoryProvider pings the selected external memory provider using the
// supplied (or stored) base URL and API key.
func (h *Handler) handleTestMemoryProvider(w http.ResponseWriter, r *http.Request) {
	var req memoryTestRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider == "" {
		provider = config.MemoryProviderHoncho
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	apiKey := strings.TrimSpace(req.APIKey)

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	m := cfg.Agents.Defaults.Memory
	switch provider {
	case config.MemoryProviderHoncho:
		if baseURL == "" {
			baseURL = m.Honcho.EffectiveBaseURL()
		}
		if apiKey == "" {
			apiKey = m.Honcho.APIKey
		}
	case config.MemoryProviderMem0:
		if baseURL == "" {
			baseURL = m.Mem0.EffectiveBaseURL()
		}
		if apiKey == "" {
			apiKey = m.Mem0.APIKey
		}
	case config.MemoryProviderRetainDB:
		if baseURL == "" {
			baseURL = m.RetainDB.EffectiveBaseURL()
		}
		if apiKey == "" {
			apiKey = m.RetainDB.APIKey
		}
	default:
		http.Error(w, fmt.Sprintf("unsupported memory provider: %q", provider), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	switch provider {
	case config.MemoryProviderHoncho:
		err = honcho.New(baseURL, apiKey).Ping(ctx)
	case config.MemoryProviderMem0:
		err = mem0.New(baseURL, apiKey).Ping(ctx)
	case config.MemoryProviderRetainDB:
		err = retaindb.New(baseURL, apiKey, "").Ping(ctx)
	}
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func buildMemoryConfigResponse(cfg *config.Config) memoryConfigResponse {
	m := cfg.Agents.Defaults.Memory
	trigger := m.Honcho.Trigger
	if trigger == "" {
		trigger = config.HonchoTriggerAsk
	}
	return memoryConfigResponse{
		Enabled:            m.Enabled,
		Provider:           m.EffectiveMemoryProvider(),
		LongTermCharLimit:  m.EffectiveLongTermCharLimit(),
		DailyNotesEnabled:  m.DailyNotesEnabled,
		DailyNotesDays:     m.EffectiveDailyNotesDays(),
		DailyNoteCharLimit: m.EffectiveDailyNoteCharLimit(),
		ExtractEnabled:     m.ExtractEnabled,
		ExtractEveryTurns:  m.EffectiveExtractEveryTurns(),
		ExtractModel:       m.ExtractModel,
		Honcho: honchoConfigResponse{
			Enabled:          m.Honcho.Enabled,
			BaseURL:          m.Honcho.BaseURL,
			APIKeySet:        strings.TrimSpace(m.Honcho.APIKey) != "",
			WorkspaceID:      m.Honcho.WorkspaceID,
			UserPeerID:       m.Honcho.UserPeerID,
			AgentPeerID:      m.Honcho.AgentPeerID,
			Trigger:          trigger,
			SyncEveryTurns:   m.Honcho.EffectiveSyncEveryTurns(),
			RecallMode:       m.Honcho.EffectiveRecallMode(),
			ContextCadence:   m.Honcho.EffectiveContextCadence(),
			DialecticCadence: m.Honcho.EffectiveDialecticCadence(),
			DialecticLevel:   m.Honcho.EffectiveDialecticLevel(),
			ContextTokens:    m.Honcho.EffectiveContextTokens(),
		},
		Mem0: mem0ConfigResponse{
			Enabled:        m.Mem0.Enabled,
			BaseURL:        m.Mem0.BaseURL,
			APIKeySet:      strings.TrimSpace(m.Mem0.APIKey) != "",
			UserID:         m.Mem0.UserID,
			AgentID:        m.Mem0.AgentID,
			SyncEveryTurns: m.Mem0.EffectiveSyncEveryTurns(),
		},
		RetainDB: retaindbConfigResponse{
			Enabled:        m.RetainDB.Enabled,
			BaseURL:        m.RetainDB.BaseURL,
			APIKeySet:      strings.TrimSpace(m.RetainDB.APIKey) != "",
			Namespace:      m.RetainDB.Namespace,
			SyncEveryTurns: m.RetainDB.EffectiveSyncEveryTurns(),
		},
	}
}

// normalizeMemoryProvider clamps an arbitrary provider string to a known value,
// falling back to "local".
func normalizeMemoryProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case config.MemoryProviderHoncho:
		return config.MemoryProviderHoncho
	case config.MemoryProviderMem0:
		return config.MemoryProviderMem0
	case config.MemoryProviderRetainDB:
		return config.MemoryProviderRetainDB
	default:
		return config.MemoryProviderLocal
	}
}

// normalizeHonchoRecallMode accepts a recall mode from the web form, returning
// "" for unrecognized input so the config default ("hybrid") applies.
func normalizeHonchoRecallMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case config.HonchoRecallHybrid, config.HonchoRecallContext,
		config.HonchoRecallTools, config.HonchoRecallOff:
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return ""
	}
}

// normalizeHonchoDialecticLevel accepts a dialectic reasoning level, returning
// "" for unrecognized input so the config default applies.
func normalizeHonchoDialecticLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "minimal", "low", "medium", "high", "max":
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return ""
	}
}

func honchoPreview(messages []honcho.Message) string {
	var parts []string
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		if len([]rune(content)) > 160 {
			content = string([]rune(content)[:160]) + "..."
		}
		parts = append(parts, content)
		if len(parts) >= 2 {
			break
		}
	}
	return strings.Join(parts, " | ")
}
