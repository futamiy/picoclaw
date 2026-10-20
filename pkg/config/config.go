package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/sipeed/picoclaw/pkg"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/logger"
	providercommon "github.com/sipeed/picoclaw/pkg/providers/common"
)

// rrCounter is a global counter for round-robin load balancing across models.
var rrCounter atomic.Uint64

// CurrentVersion is the latest config schema version
const CurrentVersion = 3

func init() {
	initChannel()
}

// Config is the current config structure with version support.
type Config struct {
	// Config schema version for migration.
	Version   int             `json:"version"             yaml:"-"`
	Isolation IsolationConfig `json:"isolation,omitempty" yaml:"-"`
	Agents    AgentsConfig    `json:"agents"              yaml:"-"`
	Session   SessionConfig   `json:"session,omitempty"   yaml:"-"`
	Evolution EvolutionConfig `json:"evolution,omitempty" yaml:"-"`
	Channels  ChannelsConfig  `json:"channel_list"        yaml:"channel_list"`
	ModelList SecureModelList `json:"model_list"          yaml:"model_list"` // New model-centric provider configuration
	Gateway   GatewayConfig   `json:"gateway"             yaml:"-"`
	Events    EventsConfig    `json:"events,omitempty"    yaml:"-"`
	Hooks     HooksConfig     `json:"hooks,omitempty"     yaml:"-"`
	Tools     ToolsConfig     `json:"tools"               yaml:",inline"`
	Heartbeat HeartbeatConfig `json:"heartbeat"           yaml:"-"`
	Devices   DevicesConfig   `json:"devices"             yaml:"-"`
	Voice     VoiceConfig     `json:"voice"               yaml:"-"`
	// Backup configures full-data backups of the whole PicoClaw home directory.
	// This is deliberately separate from the memory-only archive under
	// agents.defaults.memory.backup.
	Backup FullBackupConfig `json:"backup,omitempty" yaml:"-"`
	// BuildInfo contains build-time version information
	BuildInfo BuildInfo `json:"build_info,omitempty" yaml:"-"`

	// cache for sensitive values and compiled regex (computed once)
	sensitiveCache *SensitiveDataCache
}

type EvolutionConfig struct {
	Enabled         bool     `json:"enabled,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	StateDir        string   `json:"state_dir,omitempty"`
	MinTaskCount    int      `json:"min_task_count,omitempty"`
	MinSuccessRatio float64  `json:"min_success_ratio,omitempty"`
	ColdPathTrigger string   `json:"cold_path_trigger,omitempty"`
	ColdPathTimes   []string `json:"cold_path_times,omitempty"`
	// Deprecated: use MinTaskCount.
	MinCaseCount int `json:"min_case_count,omitempty"`
	// Deprecated: use MinSuccessRatio.
	MinSuccessRate float64 `json:"min_success_rate,omitempty"`
}

func (c EvolutionConfig) MarshalJSON() ([]byte, error) {
	out := struct {
		Enabled         bool     `json:"enabled,omitempty"`
		Mode            string   `json:"mode,omitempty"`
		StateDir        string   `json:"state_dir,omitempty"`
		MinTaskCount    int      `json:"min_task_count,omitempty"`
		MinSuccessRatio float64  `json:"min_success_ratio,omitempty"`
		ColdPathTrigger string   `json:"cold_path_trigger,omitempty"`
		ColdPathTimes   []string `json:"cold_path_times,omitempty"`
	}{
		Enabled:         c.Enabled,
		Mode:            c.Mode,
		StateDir:        c.StateDir,
		MinTaskCount:    c.EffectiveMinTaskCount(),
		MinSuccessRatio: c.EffectiveMinSuccessRatio(),
		ColdPathTrigger: strings.TrimSpace(c.ColdPathTrigger),
		ColdPathTimes:   c.EffectiveColdPathTimes(),
	}
	if !out.Enabled {
		out.Mode = ""
		out.ColdPathTrigger = ""
		out.ColdPathTimes = nil
	}
	return json.Marshal(out)
}

func (c EvolutionConfig) EffectiveMode() string {
	if !c.Enabled {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(c.Mode)) {
	case "draft":
		return "draft"
	case "apply":
		return "apply"
	case "", "observe":
		return "observe"
	default:
		return "observe"
	}
}

func (c EvolutionConfig) RunsColdPathAutomatically() bool {
	return c.RunsColdPathAfterTurn() || c.RunsColdPathScheduled()
}

func (c EvolutionConfig) ColdPathTriggerMode() string {
	if c.EffectiveMode() != "draft" && c.EffectiveMode() != "apply" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(c.ColdPathTrigger)) {
	case "", "after_turn":
		return "after_turn"
	case "scheduled":
		return "scheduled"
	case "manual", "none", "off":
		return "manual"
	default:
		return "after_turn"
	}
}

func (c EvolutionConfig) RunsColdPathAfterTurn() bool {
	return c.ColdPathTriggerMode() == "after_turn"
}

func (c EvolutionConfig) RunsColdPathScheduled() bool {
	return c.ColdPathTriggerMode() == "scheduled"
}

func (c EvolutionConfig) EffectiveMinTaskCount() int {
	if c.MinTaskCount > 0 {
		return c.MinTaskCount
	}
	if c.MinCaseCount > 0 {
		return c.MinCaseCount
	}
	return 2
}

func (c EvolutionConfig) EffectiveMinSuccessRatio() float64 {
	if c.MinSuccessRatio > 0 {
		return c.MinSuccessRatio
	}
	if c.MinSuccessRate > 0 {
		return c.MinSuccessRate
	}
	return 0.7
}

func (c EvolutionConfig) EffectiveColdPathTimes() []string {
	out := make([]string, 0, len(c.ColdPathTimes))
	for _, value := range c.ColdPathTimes {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func (c EvolutionConfig) AutoAppliesDrafts() bool {
	return c.EffectiveMode() == "apply"
}

// IsolationConfig controls subprocess isolation for commands started by PicoClaw.
// It is applied by the isolation package rather than by sandboxing the main process.
type IsolationConfig struct {
	Enabled     bool         `json:"enabled,omitempty"`
	ExposePaths []ExposePath `json:"expose_paths,omitempty"`
}

// ExposePath describes a host path that should remain visible inside the isolated
// child-process environment. This is currently implemented on Linux only.
type ExposePath struct {
	Source string `json:"source"`
	Target string `json:"target,omitempty"`
	Mode   string `json:"mode"`
}

// FilterSensitiveData filters sensitive values from content before sending to LLM.
// This prevents the LLM from seeing its own credentials.
// Uses strings.Replacer for O(n+m) performance (computed once per SecurityConfig).
// Short content (below FilterMinLength) is returned unchanged for performance.
func (c *Config) FilterSensitiveData(content string) string {
	if c == nil {
		return content
	}
	// Check if filtering is enabled (default: true)
	if !c.Tools.IsFilterSensitiveDataEnabled() {
		return content
	}
	// Fast path: skip filtering for short content
	if len(content) < c.Tools.GetFilterMinLength() {
		return content
	}
	return c.SensitiveDataReplacer().Replace(content)
}

type HooksConfig struct {
	Enabled   bool                         `json:"enabled"`
	Defaults  HookDefaultsConfig           `json:"defaults,omitempty"`
	Builtins  map[string]BuiltinHookConfig `json:"builtins,omitempty"`
	Processes map[string]ProcessHookConfig `json:"processes,omitempty"`
}

type HookDefaultsConfig struct {
	ObserverTimeoutMS    int `json:"observer_timeout_ms,omitempty"`
	InterceptorTimeoutMS int `json:"interceptor_timeout_ms,omitempty"`
	ApprovalTimeoutMS    int `json:"approval_timeout_ms,omitempty"`
}

type BuiltinHookConfig struct {
	Enabled  bool            `json:"enabled"`
	Priority int             `json:"priority,omitempty"`
	Config   json.RawMessage `json:"config,omitempty"`
}

type ProcessHookConfig struct {
	Enabled   bool              `json:"enabled"`
	Priority  int               `json:"priority,omitempty"`
	Transport string            `json:"transport,omitempty"`
	Command   []string          `json:"command,omitempty"`
	Dir       string            `json:"dir,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Observe   []string          `json:"observe,omitempty"`
	Intercept []string          `json:"intercept,omitempty"`
}

// BuildInfo contains build-time version information
type BuildInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// MarshalJSON implements custom JSON marshaling for Config
// to omit providers section when empty and session when empty.
func (c *Config) MarshalJSON() ([]byte, error) {
	type Alias Config
	aux := &struct {
		Session *SessionConfig `json:"session,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(c),
	}

	if len(c.Session.Dimensions) > 0 || len(c.Session.IdentityLinks) > 0 || c.Session.DmScope != "" {
		sessionCfg := c.Session
		aux.Session = &sessionCfg
	}

	return json.Marshal(aux)
}

type AgentsConfig struct {
	Defaults AgentDefaults   `json:"defaults"`
	List     []AgentConfig   `json:"list,omitempty"`
	Dispatch *DispatchConfig `json:"dispatch,omitempty"`
}

// AgentModelConfig supports both string and structured model config.
// String format: "gpt-4" (just primary, no fallbacks)
// Object format: {"primary": "gpt-4", "fallbacks": ["claude-haiku"]}
type AgentModelConfig struct {
	Primary   string   `json:"primary,omitempty"`
	Fallbacks []string `json:"fallbacks,omitempty"`
}

func (m *AgentModelConfig) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		m.Primary = s
		m.Fallbacks = nil
		return nil
	}
	type raw struct {
		Primary   string   `json:"primary"`
		Fallbacks []string `json:"fallbacks"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	m.Primary = r.Primary
	m.Fallbacks = r.Fallbacks
	return nil
}

func (m AgentModelConfig) MarshalJSON() ([]byte, error) {
	if len(m.Fallbacks) == 0 && m.Primary != "" {
		return json.Marshal(m.Primary)
	}
	type raw struct {
		Primary   string   `json:"primary,omitempty"`
		Fallbacks []string `json:"fallbacks,omitempty"`
	}
	return json.Marshal(raw{Primary: m.Primary, Fallbacks: m.Fallbacks})
}

type AgentConfig struct {
	ID        string            `json:"id"`
	Default   bool              `json:"default,omitempty"`
	Name      string            `json:"name,omitempty"`
	Workspace string            `json:"workspace,omitempty"`
	Model     *AgentModelConfig `json:"model,omitempty"`
	Skills    []string          `json:"skills,omitempty"`
	Subagents *SubagentsConfig  `json:"subagents,omitempty"`
}

type SubagentsConfig struct {
	AllowAgents []string          `json:"allow_agents,omitempty"`
	Model       *AgentModelConfig `json:"model,omitempty"`
}

type DispatchConfig struct {
	Rules []DispatchRule `json:"rules,omitempty"`
}

type DispatchRule struct {
	Name              string           `json:"name,omitempty"`
	Agent             string           `json:"agent"`
	When              DispatchSelector `json:"when"`
	SessionDimensions []string         `json:"session_dimensions,omitempty"`
}

type DispatchSelector struct {
	Channel   string `json:"channel,omitempty"`
	Account   string `json:"account,omitempty"`
	Space     string `json:"space,omitempty"`
	Chat      string `json:"chat,omitempty"`
	Topic     string `json:"topic,omitempty"`
	Sender    string `json:"sender,omitempty"`
	Mentioned *bool  `json:"mentioned,omitempty"`
}

type SessionConfig struct {
	Dimensions    []string            `json:"dimensions,omitempty"`
	IdentityLinks map[string][]string `json:"identity_links,omitempty"`
	DmScope       string              `json:"dm_scope,omitempty"`
}

// ApplyDmScope translates the user-facing dm_scope value into the internal
// dimensions array that the routing layer consumes. It is a no-op when
// DmScope is empty or when Dimensions is already set (explicit Dimensions
// take precedence over the derived value).
func (s *SessionConfig) ApplyDmScope() {
	if s.DmScope == "" || len(s.Dimensions) > 0 {
		return
	}
	switch s.DmScope {
	case "per-channel-peer":
		s.Dimensions = []string{"chat", "sender"}
	case "per-channel":
		s.Dimensions = []string{"chat"}
	case "per-peer":
		s.Dimensions = []string{"sender"}
	case "global":
		s.Dimensions = nil
	}
}

// DeriveDmScope sets DmScope based on Dimensions when DmScope is empty.
// This handles legacy/fresh configs that only have explicit Dimensions
// without a corresponding DmScope value, ensuring the API response always
// includes a dm_scope that matches the actual runtime dimensions.
func (s *SessionConfig) DeriveDmScope() {
	if s.DmScope != "" || len(s.Dimensions) == 0 {
		return
	}
	switch {
	case slices.Equal(s.Dimensions, []string{"chat", "sender"}):
		s.DmScope = "per-channel-peer"
	case slices.Equal(s.Dimensions, []string{"chat"}):
		s.DmScope = "per-channel"
	case slices.Equal(s.Dimensions, []string{"sender"}):
		s.DmScope = "per-peer"
	}
	// Dimensions not matching any known scope mapping (custom array)
	// is fine — DmScope stays empty and the UI can handle it.
}

// RoutingConfig controls the intelligent model routing feature.
// When enabled, each incoming message is scored against structural features
// (message length, code blocks, tool call history, conversation depth, attachments).
// Messages scoring below Threshold are sent to LightModel; all others use the
// agent's primary model. This reduces cost and latency for simple tasks without
// requiring any keyword matching — all scoring is language-agnostic.
type RoutingConfig struct {
	Enabled    bool    `json:"enabled"`
	LightModel string  `json:"light_model"` // model_name from model_list to use for simple tasks
	Threshold  float64 `json:"threshold"`   // complexity score in [0,1]; score >= threshold → primary model
}

// SubTurnConfig configures the SubTurn execution system.
type SubTurnConfig struct {
	MaxDepth              int `json:"max_depth"               env:"PICOCLAW_AGENTS_DEFAULTS_SUBTURN_MAX_DEPTH"`
	MaxConcurrent         int `json:"max_concurrent"          env:"PICOCLAW_AGENTS_DEFAULTS_SUBTURN_MAX_CONCURRENT"`
	DefaultTimeoutMinutes int `json:"default_timeout_minutes" env:"PICOCLAW_AGENTS_DEFAULTS_SUBTURN_DEFAULT_TIMEOUT_MINUTES"`
	DefaultTokenBudget    int `json:"default_token_budget"    env:"PICOCLAW_AGENTS_DEFAULTS_SUBTURN_DEFAULT_TOKEN_BUDGET"`
	ConcurrencyTimeoutSec int `json:"concurrency_timeout_sec" env:"PICOCLAW_AGENTS_DEFAULTS_SUBTURN_CONCURRENCY_TIMEOUT_SEC"`
}

type ToolFeedbackConfig struct {
	Enabled          bool `json:"enabled"           env:"PICOCLAW_AGENTS_DEFAULTS_TOOL_FEEDBACK_ENABLED"`
	MaxArgsLength    int  `json:"max_args_length"   env:"PICOCLAW_AGENTS_DEFAULTS_TOOL_FEEDBACK_MAX_ARGS_LENGTH"`
	SeparateMessages bool `json:"separate_messages" env:"PICOCLAW_AGENTS_DEFAULTS_TOOL_FEEDBACK_SEPARATE_MESSAGES"`
}

// ErrorActionsConfig controls interactive recovery affordances attached to
// limit/error messages. When ContinueButton is enabled, recoverable failures
// (tool-iteration limit or exhausted transient LLM retries) are accompanied by
// a "continue" action on channels that support it (e.g. a Telegram inline
// button) so the user can resume the turn without retyping.
type ErrorActionsConfig struct {
	ContinueButton *bool `json:"continue_button,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_ERROR_ACTIONS_CONTINUE_BUTTON"`
}

// MemoryConfig configures the agent's persistent memory store.
// Long-term memory lives in workspace/memory/MEMORY.md (bounded); daily notes
// live in workspace/memory/YYYYMM/YYYYMMDD.md. Distilled from Hermes Agent.
type MemoryConfig struct {
	// Enabled controls whether the `memory` tool is registered and memory is injected.
	Enabled bool `json:"enabled" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_ENABLED"`
	// Provider selects the external memory backend: "local" (built-in only),
	// "honcho", "mem0" or "retaindb". Empty behaves as "local" unless a legacy
	// Honcho config is enabled.
	Provider string `json:"provider,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_PROVIDER"`
	// LongTermCharLimit caps MEMORY.md size; writes that exceed it are rejected
	// so the agent must condense first.
	LongTermCharLimit int `json:"long_term_char_limit" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_LONG_TERM_CHAR_LIMIT"`
	// ProfileEnabled enables the bounded user-profile memory (PROFILE.md),
	// distinct from the workspace USER.md definition file.
	ProfileEnabled bool `json:"profile_enabled" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_PROFILE_ENABLED"`
	// ProfileCharLimit caps PROFILE.md size in runes; writes that exceed it are
	// rejected. 0 uses the default.
	ProfileCharLimit int `json:"profile_char_limit" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_PROFILE_CHAR_LIMIT"`
	// DailyNotesEnabled controls whether dated daily notes are written/read.
	DailyNotesEnabled bool `json:"daily_notes_enabled" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_DAILY_NOTES_ENABLED"`
	// DailyNotesDays is how many recent days of notes are injected into the prompt.
	DailyNotesDays int `json:"daily_notes_days" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_DAILY_NOTES_DAYS"`
	// DailyNoteCharLimit caps each daily note file size.
	DailyNoteCharLimit int `json:"daily_note_char_limit" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_DAILY_NOTE_CHAR_LIMIT"`
	// ExtractEnabled enables the end-of-turn memory extraction pipeline.
	ExtractEnabled bool `json:"extract_enabled" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_EXTRACT_ENABLED"`
	// ExtractEveryTurns runs extraction once every N completed turns (min 1). 0 disables.
	ExtractEveryTurns int `json:"extract_every_turns" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_EXTRACT_EVERY_TURNS"`
	// ExtractModel optionally overrides the model used for extraction (cheaper model).
	ExtractModel string `json:"extract_model,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_EXTRACT_MODEL"`
	// Honcho is an optional external memory sink: conversation transcripts are
	// pushed to a Honcho (https://honcho.dev) workspace where it reasons over
	// them in the background to build peer representations.
	Honcho HonchoMemoryConfig `json:"honcho,omitempty"`
	// Mem0 is an optional external memory sink backed by a Mem0 server.
	Mem0 Mem0MemoryConfig `json:"mem0,omitempty"`
	// RetainDB is an optional external memory sink backed by a RetainDB server.
	RetainDB RetainDBMemoryConfig `json:"retaindb,omitempty"`
	// Backup configures local memory backups (drift .bak files and archive zips).
	Backup MemoryBackupConfig `json:"backup,omitempty"`
}

// HonchoMemoryConfig configures pushing conversation transcripts to a Honcho
// server (managed at api.honcho.dev or a self-hosted instance).
type HonchoMemoryConfig struct {
	// Enabled enables transcript syncing to Honcho.
	Enabled bool `json:"enabled" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_ENABLED"`
	// BaseURL is the Honcho server base URL.
	BaseURL string `json:"base_url,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_BASE_URL"`
	// APIKey authenticates against the Honcho server (Bearer token).
	APIKey string `json:"api_key,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_API_KEY"`
	// WorkspaceID scopes Honcho data. Empty uses the agent ID.
	WorkspaceID string `json:"workspace_id,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_WORKSPACE_ID"`
	// UserPeerID is the Honcho peer representing the human user.
	UserPeerID string `json:"user_peer_id,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_USER_PEER_ID"`
	// Trigger controls how transcripts are sent: "auto" pushes without asking;
	// "ask" stages a pending batch that the user must approve first.
	Trigger string `json:"trigger,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_TRIGGER"`
	// SyncEveryTurns syncs once every N completed turns (min 1). 0 uses the default.
	SyncEveryTurns int `json:"sync_every_turns,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_SYNC_EVERY_TURNS"`
	// RecallMode controls how the agent reads memory back from Honcho:
	// "hybrid" injects context and exposes tools, "context" injects only,
	// "tools" exposes only, "off" disables recall entirely.
	RecallMode string `json:"recall_mode,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_RECALL_MODE"`
	// AgentPeerID is the Honcho peer representing this agent. Empty uses the agent ID.
	AgentPeerID string `json:"agent_peer_id,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_AGENT_PEER_ID"`
	// ContextCadence is the minimum number of turns between context prefetch
	// calls (min 1). Higher values reduce API calls. 0 uses the default.
	ContextCadence int `json:"context_cadence,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_CONTEXT_CADENCE"`
	// DialecticCadence is the minimum number of turns between dialectic
	// (reasoning) calls (min 1). 0 uses the default.
	DialecticCadence int `json:"dialectic_cadence,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_DIALECTIC_CADENCE"`
	// DialecticLevel is the default reasoning level for dialectic calls:
	// minimal, low, medium, high, or max. Empty uses the default.
	DialecticLevel string `json:"dialectic_level,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_DIALECTIC_LEVEL"`
	// ContextTokens bounds the combined injected memory-context block
	// (summary + representation + card). 0 leaves it uncapped.
	ContextTokens int `json:"context_tokens,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_HONCHO_CONTEXT_TOKENS"`
}

// Mem0MemoryConfig configures pushing conversation transcripts to a Mem0 server
// (managed at api.mem0.ai or a self-hosted instance).
type Mem0MemoryConfig struct {
	// Enabled enables transcript syncing to Mem0.
	Enabled bool `json:"enabled" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_MEM0_ENABLED"`
	// BaseURL is the Mem0 server base URL.
	BaseURL string `json:"base_url,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_MEM0_BASE_URL"`
	// APIKey authenticates against the Mem0 server (Bearer token).
	APIKey string `json:"api_key,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_MEM0_API_KEY"`
	// UserID scopes stored memories to the human user.
	UserID string `json:"user_id,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_MEM0_USER_ID"`
	// AgentID scopes stored memories to the agent. Empty uses the agent ID.
	AgentID string `json:"agent_id,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_MEM0_AGENT_ID"`
	// SyncEveryTurns syncs once every N completed turns (min 1). 0 uses the default.
	SyncEveryTurns int `json:"sync_every_turns,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_MEM0_SYNC_EVERY_TURNS"`
	// RecallMode controls how memory is read back: "context" (or "hybrid")
	// injects relevant memories into the prompt, "off" disables recall. Mem0
	// exposes no recall tools, so "tools" behaves as "off". Empty uses the
	// default ("context").
	RecallMode string `json:"recall_mode,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_MEM0_RECALL_MODE"`
	// ContextCadence is the minimum number of turns between recall prefetches
	// (min 1). Higher values reduce API calls. 0 uses the default.
	ContextCadence int `json:"context_cadence,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_MEM0_CONTEXT_CADENCE"`
	// RecallLimit caps how many memories are fetched per prefetch. 0 uses the default.
	RecallLimit int `json:"recall_limit,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_MEM0_RECALL_LIMIT"`
}

// RetainDBMemoryConfig configures pushing conversation transcripts to a RetainDB
// server.
type RetainDBMemoryConfig struct {
	// Enabled enables transcript syncing to RetainDB.
	Enabled bool `json:"enabled" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_RETAINDB_ENABLED"`
	// BaseURL is the RetainDB server base URL.
	BaseURL string `json:"base_url,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_RETAINDB_BASE_URL"`
	// APIKey authenticates against the RetainDB server (Bearer token).
	APIKey string `json:"api_key,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_RETAINDB_API_KEY"`
	// Namespace scopes stored data. Empty uses the agent ID.
	Namespace string `json:"namespace,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_RETAINDB_NAMESPACE"`
	// UserID scopes stored memories to the human user. Empty uses the default.
	UserID string `json:"user_id,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_RETAINDB_USER_ID"`
	// SyncEveryTurns syncs once every N completed turns (min 1). 0 uses the default.
	SyncEveryTurns int `json:"sync_every_turns,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_RETAINDB_SYNC_EVERY_TURNS"`
	// RecallMode controls how memory is read back: "context" (or "hybrid")
	// injects relevant memories into the prompt, "off" disables recall. RetainDB
	// exposes no recall tools, so "tools" behaves as "off". Empty uses the
	// default ("context").
	RecallMode string `json:"recall_mode,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_RETAINDB_RECALL_MODE"`
	// ContextCadence is the minimum number of turns between recall prefetches
	// (min 1). Higher values reduce API calls. 0 uses the default.
	ContextCadence int `json:"context_cadence,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_RETAINDB_CONTEXT_CADENCE"`
	// RecallLimit caps how many memories are fetched per prefetch. 0 uses the default.
	RecallLimit int `json:"recall_limit,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_RETAINDB_RECALL_LIMIT"`
}

// MemoryBackupConfig configures local backups of the memory directory.
//
// Two layers exist:
//   - a drift backup: whenever a memory file is rewritten, the previous content
//     is copied to <file>.bak.<unix_ts> (bounded to remember the last few).
//   - an archive: an on-demand (or scheduled) zip of the whole memory directory,
//     optionally including config.json.
//
// Automatic backups are OFF by default so they cannot silently grow the disk;
// set Enabled to turn on drift snapshots and scheduled archives.
type MemoryBackupConfig struct {
	// Enabled turns on the memory-backup feature: it gates both per-write drift
	// snapshots (.bak.<ts> files) and scheduled archive backups. Manual backups
	// (CLI or web) always work regardless. Off by default.
	Enabled bool `json:"enabled" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_BACKUP_ENABLED"`
	// Dir is where archives are written. Empty uses ~/.picoclaw/backups.
	Dir string `json:"dir,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_BACKUP_DIR"`
	// Keep is how many archives to retain (oldest pruned). 0 uses the default.
	Keep int `json:"keep,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_BACKUP_KEEP"`
	// IntervalHours is how often to archive when Enabled (min 1). 0 uses the default.
	IntervalHours int `json:"interval_hours,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_BACKUP_INTERVAL_HOURS"`
	// DriftKeep is how many <file>.bak.<ts> snapshots to retain per file (min 1). 0 uses the default.
	DriftKeep int `json:"drift_keep,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MEMORY_BACKUP_DRIFT_KEEP"`
}

// FullBackupConfig configures local full-data backups of the whole PicoClaw
// home directory (config, workspace, memory, sessions, state). It is separate
// from Agents.Defaults.Memory.Backup, which archives only the workspace memory
// directory. Full backups are manual (triggered from the WebUI); they are never
// scheduled automatically.
type FullBackupConfig struct {
	// Dir is where archives are written. Empty uses ~/.picoclaw/backups.
	Dir string `json:"dir,omitempty" env:"PICOCLAW_BACKUP_DIR"`
	// Keep is how many archives to retain (oldest pruned). 0 uses the default.
	Keep int `json:"keep,omitempty" env:"PICOCLAW_BACKUP_KEEP"`
	// IncludeSecrets includes secret-bearing files (.security.yml, auth store)
	// in the archive. Unset defaults to true so a full backup is complete.
	IncludeSecrets *bool `json:"include_secrets,omitempty" env:"PICOCLAW_BACKUP_INCLUDE_SECRETS"`
}

// EffectiveKeep returns the configured archive retention or the default.
func (b FullBackupConfig) EffectiveKeep() int {
	if b.Keep > 0 {
		return b.Keep
	}
	return DefaultBackupKeep
}

// EffectiveIncludeSecrets reports whether secret-bearing files are included.
// It defaults to true when unset.
func (b FullBackupConfig) EffectiveIncludeSecrets() bool {
	if b.IncludeSecrets != nil {
		return *b.IncludeSecrets
	}
	return true
}

const (
	DefaultMemoryLongTermCharLimit  = 2200
	DefaultMemoryProfileCharLimit   = 1375
	DefaultMemoryDailyNoteCharLimit = 2200
	DefaultMemoryDailyNotesDays     = 3
	DefaultMemoryExtractEveryTurns  = 10
	DefaultMemorySyncEveryTurns     = 10
	DefaultMemoryBackupKeep         = 20
	DefaultMemoryBackupIntervalHour = 24
	DefaultMemoryBackupDriftKeep    = 3
	DefaultBackupKeep               = 10
	DefaultBackupIntervalHour       = 24
	DefaultHonchoBaseURL            = "https://api.honcho.dev"
	DefaultHonchoUserPeerID         = "user"
	DefaultMem0BaseURL              = "https://api.mem0.ai"
	// DefaultMemoryRecallMode injects recalled memory and exposes the provider's
	// native tool for external providers without a separate tools toggle.
	DefaultMemoryRecallMode = HonchoRecallHybrid
	// DefaultMemoryContextCadence is the min turns between external prefetches.
	DefaultMemoryContextCadence = 1
	// DefaultMemoryRecallLimit caps memories fetched per recall prefetch.
	DefaultMemoryRecallLimit = 10
	// HonchoTriggerAuto pushes transcripts without asking the user.
	HonchoTriggerAuto = "auto"
	// HonchoTriggerAsk stages a pending batch awaiting user approval.
	HonchoTriggerAsk = "ask"

	// HonchoRecallHybrid injects context and exposes recall tools.
	HonchoRecallHybrid = "hybrid"
	// HonchoRecallContext injects context only (tools hidden).
	HonchoRecallContext = "context"
	// HonchoRecallTools exposes tools only (no auto-injection).
	HonchoRecallTools = "tools"
	// HonchoRecallOff disables reading memory back from Honcho.
	HonchoRecallOff = "off"

	// DefaultHonchoContextCadence is the min turns between context prefetches.
	DefaultHonchoContextCadence = 1
	// DefaultHonchoDialecticCadence is the min turns between dialectic calls.
	DefaultHonchoDialecticCadence = 2
	// DefaultHonchoDialecticLevel is the default dialectic reasoning level.
	DefaultHonchoDialecticLevel = "low"

	// Memory provider identifiers for MemoryConfig.Provider.
	MemoryProviderLocal    = "local"
	MemoryProviderHoncho   = "honcho"
	MemoryProviderMem0     = "mem0"
	MemoryProviderRetainDB = "retaindb"

	// DefaultContinueButtonEnabled is the default for
	// error_actions.continue_button when unset.
	DefaultContinueButtonEnabled = true
)

// EffectiveLongTermCharLimit returns the configured cap or the default.
func (m MemoryConfig) EffectiveLongTermCharLimit() int {
	if m.LongTermCharLimit > 0 {
		return m.LongTermCharLimit
	}
	return DefaultMemoryLongTermCharLimit
}

// EffectiveProfileCharLimit returns the configured PROFILE.md cap or the default.
func (m MemoryConfig) EffectiveProfileCharLimit() int {
	if m.ProfileCharLimit > 0 {
		return m.ProfileCharLimit
	}
	return DefaultMemoryProfileCharLimit
}

// EffectiveDailyNoteCharLimit returns the configured cap or the default.
func (m MemoryConfig) EffectiveDailyNoteCharLimit() int {
	if m.DailyNoteCharLimit > 0 {
		return m.DailyNoteCharLimit
	}
	return DefaultMemoryDailyNoteCharLimit
}

// EffectiveDailyNotesDays returns the configured window or the default.
func (m MemoryConfig) EffectiveDailyNotesDays() int {
	if m.DailyNotesDays > 0 {
		return m.DailyNotesDays
	}
	return DefaultMemoryDailyNotesDays
}

// EffectiveExtractEveryTurns returns the configured cadence or the default.
func (m MemoryConfig) EffectiveExtractEveryTurns() int {
	if m.ExtractEveryTurns > 0 {
		return m.ExtractEveryTurns
	}
	return DefaultMemoryExtractEveryTurns
}

// EffectiveBackupKeep returns the configured archive retention or the default.
func (b MemoryBackupConfig) EffectiveKeep() int {
	if b.Keep > 0 {
		return b.Keep
	}
	return DefaultMemoryBackupKeep
}

// EffectiveIntervalHours returns the configured archive interval or the default.
func (b MemoryBackupConfig) EffectiveIntervalHours() int {
	if b.IntervalHours > 0 {
		return b.IntervalHours
	}
	return DefaultMemoryBackupIntervalHour
}

// EffectiveDriftKeep returns the configured drift-backup retention or the default.
func (b MemoryBackupConfig) EffectiveDriftKeep() int {
	if b.DriftKeep > 0 {
		return b.DriftKeep
	}
	return DefaultMemoryBackupDriftKeep
}

// EffectiveBaseURL returns the configured Honcho server or the default.
func (h HonchoMemoryConfig) EffectiveBaseURL() string {
	if s := strings.TrimSpace(h.BaseURL); s != "" {
		return strings.TrimRight(s, "/")
	}
	return DefaultHonchoBaseURL
}

// EffectiveUserPeerID returns the configured user peer or the default.
func (h HonchoMemoryConfig) EffectiveUserPeerID() string {
	if s := strings.TrimSpace(h.UserPeerID); s != "" {
		return s
	}
	return DefaultHonchoUserPeerID
}

// EffectiveSyncEveryTurns returns the configured cadence or the default.
func (h HonchoMemoryConfig) EffectiveSyncEveryTurns() int {
	if h.SyncEveryTurns > 0 {
		return h.SyncEveryTurns
	}
	return DefaultMemorySyncEveryTurns
}

// AskFirst reports whether transcript pushes need explicit user approval.
func (h HonchoMemoryConfig) AskFirst() bool {
	return h.Trigger == HonchoTriggerAsk
}

// EffectiveRecallMode resolves the recall mode. An empty value defaults to
// "hybrid"; unknown values fall back to "off" so a typo never silently
// enables extra API traffic.
func (h HonchoMemoryConfig) EffectiveRecallMode() string {
	switch strings.ToLower(strings.TrimSpace(h.RecallMode)) {
	case HonchoRecallHybrid:
		return HonchoRecallHybrid
	case HonchoRecallContext:
		return HonchoRecallContext
	case HonchoRecallTools:
		return HonchoRecallTools
	case HonchoRecallOff:
		return HonchoRecallOff
	case "":
		return HonchoRecallHybrid
	default:
		return HonchoRecallOff
	}
}

// InjectsContext reports whether the recall mode injects context into prompts.
func (h HonchoMemoryConfig) InjectsContext() bool {
	switch h.EffectiveRecallMode() {
	case HonchoRecallHybrid, HonchoRecallContext:
		return true
	default:
		return false
	}
}

// ExposesTools reports whether the recall mode exposes Honcho tools.
func (h HonchoMemoryConfig) ExposesTools() bool {
	switch h.EffectiveRecallMode() {
	case HonchoRecallHybrid, HonchoRecallTools:
		return true
	default:
		return false
	}
}

// EffectiveContextCadence returns the configured cadence or the default.
func (h HonchoMemoryConfig) EffectiveContextCadence() int {
	if h.ContextCadence > 0 {
		return h.ContextCadence
	}
	return DefaultHonchoContextCadence
}

// EffectiveDialecticCadence returns the configured cadence or the default.
func (h HonchoMemoryConfig) EffectiveDialecticCadence() int {
	if h.DialecticCadence > 0 {
		return h.DialecticCadence
	}
	return DefaultHonchoDialecticCadence
}

// EffectiveDialecticLevel returns the configured dialectic level or the default.
func (h HonchoMemoryConfig) EffectiveDialecticLevel() string {
	if s := strings.TrimSpace(h.DialecticLevel); s != "" {
		return s
	}
	return DefaultHonchoDialecticLevel
}

// EffectiveContextTokens returns the configured context budget, or 0 (uncapped).
func (h HonchoMemoryConfig) EffectiveContextTokens() int {
	if h.ContextTokens > 0 {
		return h.ContextTokens
	}
	return 0
}

// EffectiveMemoryProvider resolves the selected external memory backend. An
// empty provider keeps legacy behavior: a previously enabled Honcho config is
// still honored, otherwise "local" (built-in memory only) is used.
func (m MemoryConfig) EffectiveMemoryProvider() string {
	switch strings.ToLower(strings.TrimSpace(m.Provider)) {
	case MemoryProviderHoncho:
		return MemoryProviderHoncho
	case MemoryProviderMem0:
		return MemoryProviderMem0
	case MemoryProviderRetainDB:
		return MemoryProviderRetainDB
	case MemoryProviderLocal:
		return MemoryProviderLocal
	case "":
		if m.Honcho.Enabled {
			return MemoryProviderHoncho
		}
		return MemoryProviderLocal
	default:
		return MemoryProviderLocal
	}
}

// EffectiveBaseURL returns the configured Mem0 server or the default.
func (m Mem0MemoryConfig) EffectiveBaseURL() string {
	if s := strings.TrimSpace(m.BaseURL); s != "" {
		return strings.TrimRight(s, "/")
	}
	return DefaultMem0BaseURL
}

// EffectiveSyncEveryTurns returns the configured cadence or the default.
func (m Mem0MemoryConfig) EffectiveSyncEveryTurns() int {
	if m.SyncEveryTurns > 0 {
		return m.SyncEveryTurns
	}
	return DefaultMemorySyncEveryTurns
}

// EffectiveBaseURL returns the configured RetainDB server, or "" if unset.
func (m RetainDBMemoryConfig) EffectiveBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(m.BaseURL), "/")
}

// EffectiveSyncEveryTurns returns the configured cadence or the default.
func (m RetainDBMemoryConfig) EffectiveSyncEveryTurns() int {
	if m.SyncEveryTurns > 0 {
		return m.SyncEveryTurns
	}
	return DefaultMemorySyncEveryTurns
}

// EffectiveUserID returns the configured RetainDB user or the default.
func (m RetainDBMemoryConfig) EffectiveUserID() string {
	if s := strings.TrimSpace(m.UserID); s != "" {
		return s
	}
	return DefaultHonchoUserPeerID
}

// EffectiveRecallMode resolves the Mem0 recall mode.
func (m Mem0MemoryConfig) EffectiveRecallMode() string {
	return effectiveExternalRecallMode(m.RecallMode)
}

// InjectsContext reports whether Mem0 recall injects context into prompts.
func (m Mem0MemoryConfig) InjectsContext() bool {
	return externalRecallInjectsContext(m.EffectiveRecallMode())
}

// ExposesTools reports whether the Mem0 read/write tool is registered.
func (m Mem0MemoryConfig) ExposesTools() bool {
	return externalRecallExposesTools(m.EffectiveRecallMode())
}

// EffectiveContextCadence returns the configured cadence or the default.
func (m Mem0MemoryConfig) EffectiveContextCadence() int {
	if m.ContextCadence > 0 {
		return m.ContextCadence
	}
	return DefaultMemoryContextCadence
}

// EffectiveRecallLimit returns the configured recall limit or the default.
func (m Mem0MemoryConfig) EffectiveRecallLimit() int {
	if m.RecallLimit > 0 {
		return m.RecallLimit
	}
	return DefaultMemoryRecallLimit
}

// EffectiveRecallMode resolves the RetainDB recall mode.
func (m RetainDBMemoryConfig) EffectiveRecallMode() string {
	return effectiveExternalRecallMode(m.RecallMode)
}

// InjectsContext reports whether RetainDB recall injects context into prompts.
func (m RetainDBMemoryConfig) InjectsContext() bool {
	return externalRecallInjectsContext(m.EffectiveRecallMode())
}

// ExposesTools reports whether the RetainDB read/write tool is registered.
func (m RetainDBMemoryConfig) ExposesTools() bool {
	return externalRecallExposesTools(m.EffectiveRecallMode())
}

// EffectiveContextCadence returns the configured cadence or the default.
func (m RetainDBMemoryConfig) EffectiveContextCadence() int {
	if m.ContextCadence > 0 {
		return m.ContextCadence
	}
	return DefaultMemoryContextCadence
}

// EffectiveRecallLimit returns the configured recall limit or the default.
func (m RetainDBMemoryConfig) EffectiveRecallLimit() int {
	if m.RecallLimit > 0 {
		return m.RecallLimit
	}
	return DefaultMemoryRecallLimit
}

// effectiveExternalRecallMode normalizes a recall mode for providers that
// expose a native read/write tool (Mem0, RetainDB). Unknown values fall back to
// "off" so a typo never silently enables extra API traffic.
func effectiveExternalRecallMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case HonchoRecallHybrid, HonchoRecallContext, HonchoRecallTools, HonchoRecallOff:
		return strings.ToLower(strings.TrimSpace(mode))
	case "":
		return DefaultMemoryRecallMode
	default:
		return HonchoRecallOff
	}
}

// externalRecallInjectsContext reports whether the mode injects recalled
// memory into the prompt.
func externalRecallInjectsContext(mode string) bool {
	return mode == HonchoRecallHybrid || mode == HonchoRecallContext
}

// externalRecallExposesTools reports whether the mode exposes native memory
// tools.
func externalRecallExposesTools(mode string) bool {
	return mode == HonchoRecallHybrid || mode == HonchoRecallTools
}

type AgentDefaults struct {
	Workspace                 string             `json:"workspace"                        env:"PICOCLAW_AGENTS_DEFAULTS_WORKSPACE"`
	RestrictToWorkspace       bool               `json:"restrict_to_workspace"            env:"PICOCLAW_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE"`
	AllowReadOutsideWorkspace bool               `json:"allow_read_outside_workspace"     env:"PICOCLAW_AGENTS_DEFAULTS_ALLOW_READ_OUTSIDE_WORKSPACE"`
	Provider                  string             `json:"provider"                         env:"PICOCLAW_AGENTS_DEFAULTS_PROVIDER"`
	ModelName                 string             `json:"model_name"                       env:"PICOCLAW_AGENTS_DEFAULTS_MODEL_NAME"`
	ModelFallbacks            []string           `json:"model_fallbacks,omitempty"`
	ImageModel                string             `json:"image_model,omitempty"            env:"PICOCLAW_AGENTS_DEFAULTS_IMAGE_MODEL"`
	ImageModelFallbacks       []string           `json:"image_model_fallbacks,omitempty"`
	MaxTokens                 int                `json:"max_tokens"                       env:"PICOCLAW_AGENTS_DEFAULTS_MAX_TOKENS"`
	ContextWindow             int                `json:"context_window,omitempty"         env:"PICOCLAW_AGENTS_DEFAULTS_CONTEXT_WINDOW"`
	Temperature               *float64           `json:"temperature,omitempty"            env:"PICOCLAW_AGENTS_DEFAULTS_TEMPERATURE"`
	MaxToolIterations         int                `json:"max_tool_iterations"              env:"PICOCLAW_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS"`
	SummarizeMessageThreshold int                `json:"summarize_message_threshold"      env:"PICOCLAW_AGENTS_DEFAULTS_SUMMARIZE_MESSAGE_THRESHOLD"`
	SummarizeTokenPercent     int                `json:"summarize_token_percent"          env:"PICOCLAW_AGENTS_DEFAULTS_SUMMARIZE_TOKEN_PERCENT"`
	MaxMediaSize              int                `json:"max_media_size,omitempty"         env:"PICOCLAW_AGENTS_DEFAULTS_MAX_MEDIA_SIZE"`
	Routing                   *RoutingConfig     `json:"routing,omitempty"`
	SteeringMode              string             `json:"steering_mode,omitempty"          env:"PICOCLAW_AGENTS_DEFAULTS_STEERING_MODE"`      // "one-at-a-time" (default) or "all"
	MaxParallelTurns          int                `json:"max_parallel_turns,omitempty"     env:"PICOCLAW_AGENTS_DEFAULTS_MAX_PARALLEL_TURNS"` // Max concurrent turns (0 or 1 = sequential)
	SubTurn                   SubTurnConfig      `json:"subturn"                                                                                      envPrefix:"PICOCLAW_AGENTS_DEFAULTS_SUBTURN_"`
	ToolFeedback              ToolFeedbackConfig `json:"tool_feedback,omitempty"`
	ErrorActions              ErrorActionsConfig `json:"error_actions,omitempty"`
	SplitOnMarker             bool               `json:"split_on_marker"                  env:"PICOCLAW_AGENTS_DEFAULTS_SPLIT_ON_MARKER"` // split messages on <|[SPLIT]|> marker
	ContextManager            string             `json:"context_manager,omitempty"        env:"PICOCLAW_AGENTS_DEFAULTS_CONTEXT_MANAGER"`
	ContextManagerConfig      json.RawMessage    `json:"context_manager_config,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_CONTEXT_MANAGER_CONFIG"`
	TurnProfile               TurnProfileConfig  `json:"turn_profile,omitempty"`
	MaxLLMRetries             int                `json:"max_llm_retries,omitempty"        env:"PICOCLAW_AGENTS_DEFAULTS_MAX_LLM_RETRIES"`
	LLMRetryBackoffSecs       int                `json:"llm_retry_backoff_secs,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_LLM_RETRY_BACKOFF_SECS"`
	Memory                    MemoryConfig       `json:"memory,omitempty"`
	Experimental              ExperimentalConfig `json:"experimental,omitempty"`
}

// ExperimentalConfig groups opt-in, behavior-changing settings. These are
// intentionally separated from stable config so their semantics can evolve.
type ExperimentalConfig struct {
	// AutoCompress enables transparent, opencode-style context compaction:
	// when the estimated token count would exceed the model's context window,
	// the runtime pauses, compresses/trims history, and continues without
	// surfacing an error or a "compressing..." notice to the user. A nil value
	// preserves the default (enabled).
	AutoCompress *bool `json:"auto_compress,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_EXPERIMENTAL_AUTO_COMPRESS"`
}

const DefaultMaxMediaSize = 20 * 1024 * 1024 // 20 MB

func (d *AgentDefaults) GetMaxMediaSize() int {
	if d.MaxMediaSize > 0 {
		return d.MaxMediaSize
	}
	return DefaultMaxMediaSize
}

// GetToolFeedbackMaxArgsLength returns the max visible text length for tool argument previews.
func (d *AgentDefaults) GetToolFeedbackMaxArgsLength() int {
	if d.ToolFeedback.MaxArgsLength > 0 {
		return d.ToolFeedback.MaxArgsLength
	}
	return 300
}

// IsToolFeedbackEnabled returns true when tool feedback messages should be sent to the chat.
func (d *AgentDefaults) IsToolFeedbackEnabled() bool {
	return d.ToolFeedback.Enabled
}

// IsToolFeedbackSeparateMessagesEnabled returns true when each tool feedback
// update should be sent as its own chat message instead of editing a single
// in-place progress message.
func (d *AgentDefaults) IsToolFeedbackSeparateMessagesEnabled() bool {
	return d.ToolFeedback.SeparateMessages
}

// IsContinueButtonEnabled reports whether recoverable limit/error messages
// should be accompanied by a "continue" action. Defaults to true when unset so
// the affordance is available out of the box; set error_actions.continue_button
// to false to disable it.
func (d *AgentDefaults) IsContinueButtonEnabled() bool {
	if d.ErrorActions.ContinueButton == nil {
		return DefaultContinueButtonEnabled
	}
	return *d.ErrorActions.ContinueButton
}

// GetModelName returns the effective model name for the agent defaults.
// It prefers the new "model_name" field but falls back to "model" for backward compatibility.
func (d *AgentDefaults) GetModelName() string {
	return d.ModelName
}

// EffectiveAutoCompress reports whether transparent context auto-compression is
// enabled. It defaults to true so existing installations keep their current
// silky pre-call compaction behavior; setting experimental.auto_compress=false
// disables it.
func (d *AgentDefaults) EffectiveAutoCompress() bool {
	if d.Experimental.AutoCompress == nil {
		return true
	}
	return *d.Experimental.AutoCompress
}

// GroupTriggerConfig controls when the bot responds in group chats.
type GroupTriggerConfig struct {
	MentionOnly bool     `json:"mention_only,omitempty"`
	Prefixes    []string `json:"prefixes,omitempty"`
}

// TypingConfig controls typing indicator behavior (Phase 10).
type TypingConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

// PlaceholderConfig controls placeholder message behavior (Phase 10).
type PlaceholderConfig struct {
	Enabled bool                `json:"enabled"`
	Text    FlexibleStringSlice `json:"text,omitempty"`
	// FinalAsNewMessage, when enabled, deletes the placeholder and sends the
	// final answer as a brand-new message instead of editing the placeholder in
	// place. This makes platforms that only notify on new messages (e.g.
	// Telegram) ring the user for the answer. Currently honored for Telegram.
	FinalAsNewMessage bool `json:"final_as_new_message,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_PLACEHOLDER_FINAL_AS_NEW_MESSAGE"`
}

// GetRandomText returns a random placeholder text, or default if none set.
func (p *PlaceholderConfig) GetRandomText() string {
	if len(p.Text) == 0 {
		return "Thinking..."
	}
	if len(p.Text) == 1 {
		return p.Text[0]
	}
	idx := rand.Intn(len(p.Text))
	return p.Text[idx]
}

type StreamingConfig struct {
	Enabled         bool `json:"enabled,omitempty"`
	ThrottleSeconds int  `json:"throttle_seconds,omitempty"`
	MinGrowthChars  int  `json:"min_growth_chars,omitempty"`
}

func (c StreamingConfig) IsZero() bool {
	return !c.Enabled && c.ThrottleSeconds == 0 && c.MinGrowthChars == 0
}

func (c StreamingConfig) WithDefaults(throttleSeconds, minGrowthChars int) StreamingConfig {
	if c.Enabled {
		if c.ThrottleSeconds == 0 {
			c.ThrottleSeconds = throttleSeconds
		}
		if c.MinGrowthChars == 0 {
			c.MinGrowthChars = minGrowthChars
		}
	}
	return c
}

type WhatsAppSettings struct {
	BridgeURL        string `json:"bridge_url"         yaml:"-" env:"PICOCLAW_CHANNELS_WHATSAPP_BRIDGE_URL"`
	UseNative        bool   `json:"use_native"         yaml:"-" env:"PICOCLAW_CHANNELS_WHATSAPP_USE_NATIVE"`
	SessionStorePath string `json:"session_store_path" yaml:"-" env:"PICOCLAW_CHANNELS_WHATSAPP_SESSION_STORE_PATH"`
}

type TelegramSettings struct {
	Token             SecureString    `json:"token,omitzero"       yaml:"token,omitempty" env:"PICOCLAW_CHANNELS_TELEGRAM_TOKEN"`
	BaseURL           string          `json:"base_url"             yaml:"-"               env:"PICOCLAW_CHANNELS_TELEGRAM_BASE_URL"`
	Proxy             string          `json:"proxy"                yaml:"-"               env:"PICOCLAW_CHANNELS_TELEGRAM_PROXY"`
	Streaming         StreamingConfig `json:"streaming,omitzero"   yaml:"-"`
	UseMarkdownV2     bool            `json:"use_markdown_v2"      yaml:"-"               env:"PICOCLAW_CHANNELS_TELEGRAM_USE_MARKDOWN_V2"`
	MediaGroupDelayMS int             `json:"media_group_delay_ms" yaml:"-"               env:"PICOCLAW_CHANNELS_TELEGRAM_MEDIA_GROUP_DELAY_MS"`
}

type FeishuSettings struct {
	AppID               string              `json:"app_id"                      yaml:"-"                            env:"PICOCLAW_CHANNELS_FEISHU_APP_ID"`
	AppSecret           SecureString        `json:"app_secret,omitzero"         yaml:"app_secret,omitempty"         env:"PICOCLAW_CHANNELS_FEISHU_APP_SECRET"`
	EncryptKey          SecureString        `json:"encrypt_key,omitzero"        yaml:"encrypt_key,omitempty"        env:"PICOCLAW_CHANNELS_FEISHU_ENCRYPT_KEY"`
	VerificationToken   SecureString        `json:"verification_token,omitzero" yaml:"verification_token,omitempty" env:"PICOCLAW_CHANNELS_FEISHU_VERIFICATION_TOKEN"`
	RandomReactionEmoji FlexibleStringSlice `json:"random_reaction_emoji"       yaml:"-"                            env:"PICOCLAW_CHANNELS_FEISHU_RANDOM_REACTION_EMOJI"`
	IsLark              bool                `json:"is_lark"                     yaml:"-"                            env:"PICOCLAW_CHANNELS_FEISHU_IS_LARK"`
}

type DiscordSettings struct {
	Token       SecureString `json:"token,omitzero" yaml:"token,omitempty" env:"PICOCLAW_CHANNELS_DISCORD_TOKEN"`
	Proxy       string       `json:"proxy"          yaml:"-"               env:"PICOCLAW_CHANNELS_DISCORD_PROXY"`
	MentionOnly bool         `json:"mention_only"   yaml:"-"               env:"PICOCLAW_CHANNELS_DISCORD_MENTION_ONLY"`
}

type MaixCamSettings struct {
	Host string `json:"host" yaml:"-" env:"PICOCLAW_CHANNELS_MAIXCAM_HOST"`
	Port int    `json:"port" yaml:"-" env:"PICOCLAW_CHANNELS_MAIXCAM_PORT"`
}

type QQSettings struct {
	AppID                string       `json:"app_id"                   yaml:"-"                    env:"PICOCLAW_CHANNELS_QQ_APP_ID"`
	AppSecret            SecureString `json:"app_secret,omitzero"      yaml:"app_secret,omitempty" env:"PICOCLAW_CHANNELS_QQ_APP_SECRET"`
	MaxMessageLength     int          `json:"max_message_length"       yaml:"-"                    env:"PICOCLAW_CHANNELS_QQ_MAX_MESSAGE_LENGTH"`
	MaxBase64FileSizeMiB int64        `json:"max_base64_file_size_mib" yaml:"-"                    env:"PICOCLAW_CHANNELS_QQ_MAX_BASE64_FILE_SIZE_MIB"`
	SendMarkdown         bool         `json:"send_markdown"            yaml:"-"                    env:"PICOCLAW_CHANNELS_QQ_SEND_MARKDOWN"`
}

type DingTalkSettings struct {
	ClientID     string       `json:"client_id"              yaml:"-"                       env:"PICOCLAW_CHANNELS_DINGTALK_CLIENT_ID"`
	ClientSecret SecureString `json:"client_secret,omitzero" yaml:"client_secret,omitempty" env:"PICOCLAW_CHANNELS_DINGTALK_CLIENT_SECRET"`
}

type SlackSettings struct {
	BotToken SecureString `json:"bot_token,omitzero" yaml:"bot_token,omitempty" env:"PICOCLAW_CHANNELS_SLACK_BOT_TOKEN"`
	AppToken SecureString `json:"app_token,omitzero" yaml:"app_token,omitempty" env:"PICOCLAW_CHANNELS_SLACK_APP_TOKEN"`
}

type MatrixSettings struct {
	Homeserver         string       `json:"homeserver"                     yaml:"-"                      env:"PICOCLAW_CHANNELS_MATRIX_HOMESERVER"`
	UserID             string       `json:"user_id"                        yaml:"-"                      env:"PICOCLAW_CHANNELS_MATRIX_USER_ID"`
	AccessToken        SecureString `json:"access_token,omitzero"          yaml:"access_token,omitempty" env:"PICOCLAW_CHANNELS_MATRIX_ACCESS_TOKEN"`
	DeviceID           string       `json:"device_id,omitempty"            yaml:"-"`
	JoinOnInvite       bool         `json:"join_on_invite"                 yaml:"-"`
	MessageFormat      string       `json:"message_format,omitempty"       yaml:"-"`
	CryptoDatabasePath string       `json:"crypto_database_path,omitempty" yaml:"-"`
	CryptoPassphrase   string       `json:"crypto_passphrase,omitempty"    yaml:"-"`
}

// DeltaChatSettings configures the Delta Chat channel. Delta Chat is an
// email-based, end-to-end encrypted messenger; PicoClaw talks to a local
// `deltachat-rpc-server` process over JSON-RPC (stdio).
//
// Email is the only required setting. A full address selects an already
// configured account in DataDir; a first-run marker such as "@nine.testrun.org"
// creates a chatmail account and tells the user which full email to save.
// Mailbox credentials stay in the Delta Chat account store. DisplayName and
// AvatarImage are optional profile settings applied on startup. Password remains
// only for legacy PicoClaw-managed email configuration.
type DeltaChatSettings struct {
	Email          string       `json:"email"                     yaml:"-"                  env:"PICOCLAW_CHANNELS_DELTACHAT_EMAIL"`
	Password       SecureString `json:"password,omitzero"         yaml:"password,omitempty" env:"PICOCLAW_CHANNELS_DELTACHAT_PASSWORD"`
	DisplayName    string       `json:"display_name,omitempty"    yaml:"-"                  env:"PICOCLAW_CHANNELS_DELTACHAT_DISPLAY_NAME"`
	AvatarImage    string       `json:"avatar_image,omitempty"    yaml:"-"                  env:"PICOCLAW_CHANNELS_DELTACHAT_AVATAR_IMAGE"`
	DataDir        string       `json:"data_dir,omitempty"        yaml:"-"                  env:"PICOCLAW_CHANNELS_DELTACHAT_DATA_DIR"`
	RPCServerPath  string       `json:"rpc_server_path,omitempty" yaml:"-"                  env:"PICOCLAW_CHANNELS_DELTACHAT_RPC_SERVER_PATH"`
	InviteLink     string       `json:"invite_link,omitempty"     yaml:"-"                  env:"PICOCLAW_CHANNELS_DELTACHAT_INVITE_LINK"`
	AllowCrosspost bool         `json:"allow_crosspost,omitempty" yaml:"-"                  env:"PICOCLAW_CHANNELS_DELTACHAT_ALLOW_CROSSPOST"`
	IMAPServer     string       `json:"imap_server,omitempty"     yaml:"-"`
	IMAPPort       int          `json:"imap_port,omitempty"       yaml:"-"`
	SMTPServer     string       `json:"smtp_server,omitempty"     yaml:"-"`
	SMTPPort       int          `json:"smtp_port,omitempty"       yaml:"-"`
}

type LINESettings struct {
	ChannelSecret      SecureString `json:"channel_secret,omitzero"       yaml:"channel_secret,omitempty"       env:"PICOCLAW_CHANNELS_LINE_CHANNEL_SECRET"`
	ChannelAccessToken SecureString `json:"channel_access_token,omitzero" yaml:"channel_access_token,omitempty" env:"PICOCLAW_CHANNELS_LINE_CHANNEL_ACCESS_TOKEN"`
	WebhookHost        string       `json:"webhook_host"                  yaml:"-"                              env:"PICOCLAW_CHANNELS_LINE_WEBHOOK_HOST"`
	WebhookPort        int          `json:"webhook_port"                  yaml:"-"                              env:"PICOCLAW_CHANNELS_LINE_WEBHOOK_PORT"`
	WebhookPath        string       `json:"webhook_path"                  yaml:"-"                              env:"PICOCLAW_CHANNELS_LINE_WEBHOOK_PATH"`
}

type OneBotSettings struct {
	WSUrl              string       `json:"ws_url"                yaml:"-"                      env:"PICOCLAW_CHANNELS_ONEBOT_WS_URL"`
	AccessToken        SecureString `json:"access_token,omitzero" yaml:"access_token,omitempty" env:"PICOCLAW_CHANNELS_ONEBOT_ACCESS_TOKEN"`
	ReconnectInterval  int          `json:"reconnect_interval"    yaml:"-"                      env:"PICOCLAW_CHANNELS_ONEBOT_RECONNECT_INTERVAL"`
	GroupTriggerPrefix []string     `json:"group_trigger_prefix"  yaml:"-"                      env:"PICOCLAW_CHANNELS_ONEBOT_GROUP_TRIGGER_PREFIX"`
}

type WeComGroupConfig struct {
	AllowFrom FlexibleStringSlice `json:"allow_from,omitempty"`
}

type WeComSettings struct {
	BotID               string          `json:"bot_id"                  yaml:"-"                env:"BOT_ID"`
	Secret              SecureString    `json:"secret,omitzero"         yaml:"secret,omitempty" env:"SECRET"`
	WebSocketURL        string          `json:"websocket_url,omitempty" yaml:"-"                env:"WEBSOCKET_URL"`
	SendThinkingMessage bool            `json:"send_thinking_message"   yaml:"-"                env:"SEND_THINKING_MESSAGE"`
	Streaming           StreamingConfig `json:"streaming,omitzero"      yaml:"-"`
}

func (c *WeComSettings) SetSecret(secret string) {
	c.Secret = *NewSecureString(secret)
}

type WeixinSettings struct {
	Token      SecureString `json:"token,omitzero"       yaml:"token,omitempty" env:"PICOCLAW_CHANNELS_WEIXIN_TOKEN"`
	AccountID  string       `json:"account_id,omitempty" yaml:"-"               env:"PICOCLAW_CHANNELS_WEIXIN_ACCOUNT_ID"`
	BaseURL    string       `json:"base_url"             yaml:"-"               env:"PICOCLAW_CHANNELS_WEIXIN_BASE_URL"`
	CDNBaseURL string       `json:"cdn_base_url"         yaml:"-"               env:"PICOCLAW_CHANNELS_WEIXIN_CDN_BASE_URL"`
	Proxy      string       `json:"proxy"                yaml:"-"               env:"PICOCLAW_CHANNELS_WEIXIN_PROXY"`
}

// SetToken sets the Weixin token and marks it as dirty for security saving
func (c *WeixinSettings) SetToken(token string) {
	c.Token = *NewSecureString(token)
}

type PicoSettings struct {
	Token           SecureString    `json:"token,omitzero"              yaml:"token,omitempty" env:"PICOCLAW_CHANNELS_PICO_TOKEN"`
	AllowTokenQuery bool            `json:"allow_token_query,omitempty" yaml:"-"`
	AllowOrigins    []string        `json:"allow_origins,omitempty"     yaml:"-"`
	Streaming       StreamingConfig `json:"streaming,omitzero"          yaml:"-"`
	PingInterval    int             `json:"ping_interval,omitempty"     yaml:"-"`
	ReadTimeout     int             `json:"read_timeout,omitempty"      yaml:"-"`
	WriteTimeout    int             `json:"write_timeout,omitempty"     yaml:"-"`
	MaxConnections  int             `json:"max_connections,omitempty"   yaml:"-"`
}

// SetToken sets the Pico token and marks it as dirty for security saving
func (c *PicoSettings) SetToken(token string) {
	c.Token = *NewSecureString(token)
}

type PicoClientSettings struct {
	URL          string       `json:"url"                     yaml:"-"               env:"PICOCLAW_CHANNELS_PICO_CLIENT_URL"`
	Token        SecureString `json:"token,omitzero"          yaml:"token,omitempty" env:"PICOCLAW_CHANNELS_PICO_CLIENT_TOKEN"`
	SessionID    string       `json:"session_id,omitempty"    yaml:"-"`
	PingInterval int          `json:"ping_interval,omitempty" yaml:"-"`
	ReadTimeout  int          `json:"read_timeout,omitempty"  yaml:"-"`
}

type IRCSettings struct {
	Server           string              `json:"server"                     yaml:"-"                           env:"PICOCLAW_CHANNELS_IRC_SERVER"`
	TLS              bool                `json:"tls"                        yaml:"-"                           env:"PICOCLAW_CHANNELS_IRC_TLS"`
	Nick             string              `json:"nick"                       yaml:"-"                           env:"PICOCLAW_CHANNELS_IRC_NICK"`
	User             string              `json:"user,omitempty"             yaml:"-"                           env:"PICOCLAW_CHANNELS_IRC_USER"`
	RealName         string              `json:"real_name,omitempty"        yaml:"-"`
	Password         SecureString        `json:"password,omitzero"          yaml:"password,omitempty"          env:"PICOCLAW_CHANNELS_IRC_PASSWORD"`
	NickServPassword SecureString        `json:"nickserv_password,omitzero" yaml:"nickserv_password,omitempty" env:"PICOCLAW_CHANNELS_IRC_NICKSERV_PASSWORD"`
	SASLUser         string              `json:"sasl_user"                  yaml:"-"                           env:"PICOCLAW_CHANNELS_IRC_SASL_USER"`
	SASLPassword     SecureString        `json:"sasl_password,omitzero"     yaml:"sasl_password,omitempty"     env:"PICOCLAW_CHANNELS_IRC_SASL_PASSWORD"`
	Channels         FlexibleStringSlice `json:"channels"                   yaml:"-"                           env:"PICOCLAW_CHANNELS_IRC_CHANNELS"`
	RequestCaps      FlexibleStringSlice `json:"request_caps,omitempty"     yaml:"-"`
}

type VKSettings struct {
	Token   SecureString `json:"token,omitzero" yaml:"token,omitempty" env:"PICOCLAW_CHANNELS_VK_TOKEN"`
	GroupID int          `json:"group_id"       yaml:"-"               env:"PICOCLAW_CHANNELS_VK_GROUP_ID"`
}

func (c *VKSettings) SetToken(token string) {
	c.Token = *NewSecureString(token)
}

// TeamsWebhookSettings configures the output-only Microsoft Teams webhook channel.
// Multiple webhook targets can be configured and selected via ChatID at send time.
type TeamsWebhookSettings struct {
	Webhooks map[string]TeamsWebhookTarget `json:"webhooks" yaml:"webhooks,omitempty"`
}

// TeamsWebhookTarget represents a single Teams webhook destination.
type TeamsWebhookTarget struct {
	WebhookURL SecureString `json:"webhook_url,omitzero" yaml:"webhook_url,omitempty"`
	Title      string       `json:"title,omitempty"      yaml:"-"`
}

type MQTTSettings struct {
	Broker      string       `json:"broker"                 yaml:"-"                  env:"PICOCLAW_CHANNELS_MQTT_BROKER"`
	AgentID     string       `json:"agent_id"               yaml:"-"                  env:"PICOCLAW_CHANNELS_MQTT_AGENT_ID"`
	TopicPrefix string       `json:"topic_prefix,omitempty" yaml:"-"                  env:"PICOCLAW_CHANNELS_MQTT_TOPIC_PREFIX"`
	Username    SecureString `json:"username,omitzero"      yaml:"username,omitempty" env:"PICOCLAW_CHANNELS_MQTT_USERNAME"`
	Password    SecureString `json:"password,omitzero"      yaml:"password,omitempty" env:"PICOCLAW_CHANNELS_MQTT_PASSWORD"`
	ClientID    string       `json:"client_id,omitempty"    yaml:"-"                  env:"PICOCLAW_CHANNELS_MQTT_CLIENT_ID"`
	KeepAlive   int          `json:"keep_alive,omitempty"   yaml:"-"                  env:"PICOCLAW_CHANNELS_MQTT_KEEP_ALIVE"`
	QoS         int          `json:"qos,omitempty"          yaml:"-"                  env:"PICOCLAW_CHANNELS_MQTT_QOS"`
}

// SlackWebhookSettings configures the output-only Slack webhook channel.
type SlackWebhookSettings struct {
	Webhooks map[string]SlackWebhookTarget `json:"webhooks" yaml:"webhooks,omitempty"`
}

// SlackWebhookTarget represents a single Slack Incoming Webhook destination.
type SlackWebhookTarget struct {
	WebhookURL SecureString `json:"webhook_url,omitzero" yaml:"webhook_url,omitempty"`
	Username   string       `json:"username,omitempty"   yaml:"-"`
	IconEmoji  string       `json:"icon_emoji,omitempty" yaml:"-"`
}

type HeartbeatConfig struct {
	Enabled  bool `json:"enabled"  env:"PICOCLAW_HEARTBEAT_ENABLED"`
	Interval int  `json:"interval" env:"PICOCLAW_HEARTBEAT_INTERVAL"` // minutes, min 5
}

type DevicesConfig struct {
	Enabled    bool `json:"enabled"     env:"PICOCLAW_DEVICES_ENABLED"`
	MonitorUSB bool `json:"monitor_usb" env:"PICOCLAW_DEVICES_MONITOR_USB"`
}

type VoiceConfig struct {
	ModelName         string `json:"model_name,omitempty"         env:"PICOCLAW_VOICE_MODEL_NAME"`
	TTSModelName      string `json:"tts_model_name,omitempty"     env:"PICOCLAW_VOICE_TTS_MODEL_NAME"`
	EchoTranscription bool   `json:"echo_transcription"           env:"PICOCLAW_VOICE_ECHO_TRANSCRIPTION"`
	ElevenLabsAPIKey  string `json:"elevenlabs_api_key,omitempty" env:"PICOCLAW_VOICE_ELEVENLABS_API_KEY"`
}

type ModelStreamingConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

func (c ModelStreamingConfig) IsZero() bool {
	return !c.Enabled
}

// ModelConfig represents a model-centric provider configuration.
// It allows adding new providers (especially OpenAI-compatible ones) via configuration only.
// The Model field may be either a plain model identifier or a provider-prefixed
// identifier such as "openai/gpt-5.4" or "nvidia/z-ai/glm-5.1".
// Supported providers include openai, anthropic, antigravity, claude-cli,
// codex-cli, github-copilot, and named OpenAI-compatible protocols such as
// groq, deepseek, modelscope, and novita.
type ModelConfig struct {
	// Required fields
	ModelName string `json:"model_name"` // User-facing alias for the model
	Provider  string `json:"provider"`   // Provider name for routing and selection. When empty, provider resolution infers it from Model.
	Model     string `json:"model"`      // Model identifier, optionally provider-prefixed.

	// HTTP-based providers
	APIBase   string   `json:"api_base,omitempty"`  // API endpoint URL
	Proxy     string   `json:"proxy,omitempty"`     // HTTP proxy URL
	Fallbacks []string `json:"fallbacks,omitempty"` // Fallback model names for failover

	// Special providers (CLI-based, OAuth, etc.)
	AuthMethod  string `json:"auth_method,omitempty"`  // Authentication method: oauth, token
	ConnectMode string `json:"connect_mode,omitempty"` // Connection mode: stdio, grpc
	Workspace   string `json:"workspace,omitempty"`    // Workspace path for CLI-based providers

	// Optional optimizations
	RPM                 int                  `json:"rpm,omitempty"`              // Requests per minute limit
	MaxTokensField      string               `json:"max_tokens_field,omitempty"` // Field name for max tokens (e.g., "max_completion_tokens")
	RequestTimeout      int                  `json:"request_timeout,omitempty"`
	ThinkingLevel       string               `json:"thinking_level,omitempty"`        // Extended thinking: off|low|medium|high|xhigh|adaptive
	ToolSchemaTransform string               `json:"tool_schema_transform,omitempty"` // Optional tool schema compatibility transform (e.g. "simple")
	Streaming           ModelStreamingConfig `json:"streaming,omitzero"`              // Opt-in for provider streaming on this model entry
	ExtraBody           map[string]any       `json:"extra_body,omitempty"`            // Additional fields to inject into request body
	CustomHeaders       map[string]string    `json:"custom_headers,omitempty"`        // Additional headers to inject into every HTTP request

	APIKeys SecureStrings `json:"api_keys,omitzero" yaml:"api_keys,omitempty"` // API authentication keys (multiple keys for failover)

	// Enabled indicates whether this model entry is active. When omitted in
	// existing configs, the field is inferred during load: models with API keys
	// or the reserved "local-model" name are auto-enabled.
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// UserAgent is the user agent string to use for HTTP requests.
	UserAgent string `json:"user_agent,omitempty" yaml:"-"`

	// isVirtual marks this model as a virtual model generated from multi-key expansion.
	// Virtual models should not be persisted to config files.
	isVirtual bool
}

// APIKey returns the first API key from apiKeys
func (c *ModelConfig) APIKey() string {
	if len(c.APIKeys) > 0 {
		return c.APIKeys[0].String()
	}
	return ""
}

// IsVirtual returns true if this model was generated from multi-key expansion.
func (c *ModelConfig) IsVirtual() bool {
	return c.isVirtual
}

// Validate checks if the ModelConfig has all required fields.
func (c *ModelConfig) Validate() error {
	if c.ModelName == "" {
		return fmt.Errorf("model_name is required")
	}
	if c.Model == "" {
		return fmt.Errorf("model is required")
	}
	if _, err := providercommon.NormalizeToolSchemaTransform(c.ToolSchemaTransform); err != nil {
		return err
	}

	// Reject whitespace in model identifier
	if strings.ContainsAny(c.Model, " \t\n\r") {
		return fmt.Errorf("model identifier contains whitespace")
	}

	// Reject leading slash
	if strings.HasPrefix(c.Model, "/") {
		return fmt.Errorf("model identifier must not start with /")
	}

	// Reject consecutive slashes
	if strings.Contains(c.Model, "//") {
		return fmt.Errorf("model identifier must not contain //")
	}
	return nil
}

func (c *ModelConfig) SetAPIKey(value string) {
	if len(c.APIKeys) > 0 {
		c.APIKeys[0].Set(value)
	} else {
		c.APIKeys = append(c.APIKeys, NewSecureString(value))
	}
}

type ToolDiscoveryConfig struct {
	Enabled          bool `json:"enabled"            env:"PICOCLAW_TOOLS_DISCOVERY_ENABLED"`
	TTL              int  `json:"ttl"                env:"PICOCLAW_TOOLS_DISCOVERY_TTL"`
	MaxSearchResults int  `json:"max_search_results" env:"PICOCLAW_MAX_SEARCH_RESULTS"`
	UseBM25          bool `json:"use_bm25"           env:"PICOCLAW_TOOLS_DISCOVERY_USE_BM25"`
	UseRegex         bool `json:"use_regex"          env:"PICOCLAW_TOOLS_DISCOVERY_USE_REGEX"`
}

type ToolConfig struct {
	Enabled bool `json:"enabled" yaml:"-" env:"ENABLED"`
}

type MessageToolsConfig struct {
	ToolConfig `yaml:"-" envPrefix:"PICOCLAW_TOOLS_MESSAGE_"`

	MediaEnabled bool `json:"media_enabled" yaml:"-" env:"PICOCLAW_TOOLS_MESSAGE_MEDIA_ENABLED"`
}

type BraveConfig struct {
	Enabled    bool          `json:"enabled"           yaml:"-"                  env:"PICOCLAW_TOOLS_WEB_BRAVE_ENABLED"`
	APIKeys    SecureStrings `json:"api_keys,omitzero" yaml:"api_keys,omitempty" env:"PICOCLAW_TOOLS_WEB_BRAVE_API_KEYS"`
	MaxResults int           `json:"max_results"       yaml:"-"                  env:"PICOCLAW_TOOLS_WEB_BRAVE_MAX_RESULTS"`
}

// APIKey returns the Brave API key
func (c *BraveConfig) APIKey() string {
	if len(c.APIKeys) == 0 {
		return ""
	}
	return c.APIKeys[0].String()
}

// SetAPIKey sets the Brave API key
func (c *BraveConfig) SetAPIKey(key string) {
	c.APIKeys = SimpleSecureStrings(key)
}

func (c *BraveConfig) SetAPIKeys(keys []string) {
	c.APIKeys = SimpleSecureStrings(keys...)
}

type TavilyConfig struct {
	Enabled    bool          `json:"enabled"           yaml:"-"                  env:"PICOCLAW_TOOLS_WEB_TAVILY_ENABLED"`
	APIKeys    SecureStrings `json:"api_keys,omitzero" yaml:"api_keys,omitempty" env:"PICOCLAW_TOOLS_WEB_TAVILY_API_KEYS"`
	BaseURL    string        `json:"base_url"          yaml:"-"                  env:"PICOCLAW_TOOLS_WEB_TAVILY_BASE_URL"`
	MaxResults int           `json:"max_results"       yaml:"-"                  env:"PICOCLAW_TOOLS_WEB_TAVILY_MAX_RESULTS"`
}

// APIKey returns the Tavily API key
func (c *TavilyConfig) APIKey() string {
	if len(c.APIKeys) == 0 {
		return ""
	}
	return c.APIKeys[0].String()
}

// SetAPIKey sets the Tavily API key
func (c *TavilyConfig) SetAPIKey(key string) {
	c.APIKeys = SimpleSecureStrings(key)
}

// SetAPIKeys sets the Tavily API keys
func (c *TavilyConfig) SetAPIKeys(keys []string) {
	c.APIKeys = make(SecureStrings, len(keys))
	for i, k := range keys {
		c.APIKeys[i] = NewSecureString(k)
	}
}

type KagiConfig struct {
	Enabled    bool          `json:"enabled"           yaml:"-"                  env:"PICOCLAW_TOOLS_WEB_KAGI_ENABLED"`
	APIKeys    SecureStrings `json:"api_keys,omitzero" yaml:"api_keys,omitempty" env:"PICOCLAW_TOOLS_WEB_KAGI_API_KEYS"`
	BaseURL    string        `json:"base_url"          yaml:"-"                  env:"PICOCLAW_TOOLS_WEB_KAGI_BASE_URL"`
	MaxResults int           `json:"max_results"       yaml:"-"                  env:"PICOCLAW_TOOLS_WEB_KAGI_MAX_RESULTS"`
}

// APIKey returns the Kagi API key
func (c *KagiConfig) APIKey() string {
	if len(c.APIKeys) == 0 {
		return ""
	}
	return c.APIKeys[0].String()
}

// SetAPIKey sets the Kagi API key
func (c *KagiConfig) SetAPIKey(key string) {
	c.APIKeys = SimpleSecureStrings(key)
}

// SetAPIKeys sets the Kagi API keys
func (c *KagiConfig) SetAPIKeys(keys []string) {
	c.APIKeys = SimpleSecureStrings(keys...)
}

type DuckDuckGoConfig struct {
	Enabled    bool `json:"enabled"     env:"PICOCLAW_TOOLS_WEB_DUCKDUCKGO_ENABLED"`
	MaxResults int  `json:"max_results" env:"PICOCLAW_TOOLS_WEB_DUCKDUCKGO_MAX_RESULTS"`
}

type SogouConfig struct {
	Enabled    bool `json:"enabled"     env:"PICOCLAW_TOOLS_WEB_SOGOU_ENABLED"`
	MaxResults int  `json:"max_results" env:"PICOCLAW_TOOLS_WEB_SOGOU_MAX_RESULTS"`
}

type GeminiSearchConfig struct {
	Enabled    bool         `json:"enabled"          yaml:"-"                 env:"PICOCLAW_TOOLS_WEB_GEMINI_ENABLED"`
	APIKey     SecureString `json:"api_key,omitzero" yaml:"api_key,omitempty" env:"PICOCLAW_TOOLS_WEB_GEMINI_API_KEY"`
	Model      string       `json:"model"            yaml:"-"                 env:"PICOCLAW_TOOLS_WEB_GEMINI_MODEL"`
	MaxResults int          `json:"max_results"      yaml:"-"                 env:"PICOCLAW_TOOLS_WEB_GEMINI_MAX_RESULTS"`
}

type PerplexityConfig struct {
	Enabled    bool          `json:"enabled"           yaml:"-"                  env:"PICOCLAW_TOOLS_WEB_PERPLEXITY_ENABLED"`
	APIKeys    SecureStrings `json:"api_keys,omitzero" yaml:"api_keys,omitempty" env:"PICOCLAW_TOOLS_WEB_PERPLEXITY_API_KEYS"`
	MaxResults int           `json:"max_results"       yaml:"-"                  env:"PICOCLAW_TOOLS_WEB_PERPLEXITY_MAX_RESULTS"`
}

// APIKey returns the Perplexity API key
func (c *PerplexityConfig) APIKey() string {
	if len(c.APIKeys) == 0 {
		return ""
	}
	return c.APIKeys[0].String()
}

// SetAPIKey sets the Perplexity API key
func (c *PerplexityConfig) SetAPIKey(key string) {
	c.APIKeys = SimpleSecureStrings(key)
}

type SearXNGConfig struct {
	Enabled    bool   `json:"enabled"     env:"PICOCLAW_TOOLS_WEB_SEARXNG_ENABLED"`
	BaseURL    string `json:"base_url"    env:"PICOCLAW_TOOLS_WEB_SEARXNG_BASE_URL"`
	MaxResults int    `json:"max_results" env:"PICOCLAW_TOOLS_WEB_SEARXNG_MAX_RESULTS"`
}

type GLMSearchConfig struct {
	Enabled bool         `json:"enabled"          yaml:"-"                 env:"PICOCLAW_TOOLS_WEB_GLM_ENABLED"`
	APIKey  SecureString `json:"api_key,omitzero" yaml:"api_key,omitempty" env:"PICOCLAW_TOOLS_WEB_GLM_API_KEY"`
	BaseURL string       `json:"base_url"         yaml:"-"                 env:"PICOCLAW_TOOLS_WEB_GLM_BASE_URL"`
	// SearchEngine specifies the search backend: "search_std" (default),
	// "search_pro", "search_pro_sogou", or "search_pro_quark".
	SearchEngine string `json:"search_engine" yaml:"-" env:"PICOCLAW_TOOLS_WEB_GLM_SEARCH_ENGINE"`
	MaxResults   int    `json:"max_results"   yaml:"-" env:"PICOCLAW_TOOLS_WEB_GLM_MAX_RESULTS"`
}

type BaiduSearchConfig struct {
	Enabled    bool         `json:"enabled"          yaml:"-"                 env:"PICOCLAW_TOOLS_WEB_BAIDU_ENABLED"`
	APIKey     SecureString `json:"api_key,omitzero" yaml:"api_key,omitempty" env:"PICOCLAW_TOOLS_WEB_BAIDU_API_KEY"`
	BaseURL    string       `json:"base_url"         yaml:"-"                 env:"PICOCLAW_TOOLS_WEB_BAIDU_BASE_URL"`
	MaxResults int          `json:"max_results"      yaml:"-"                 env:"PICOCLAW_TOOLS_WEB_BAIDU_MAX_RESULTS"`
}

type WebToolsConfig struct {
	ToolConfig  `                   yaml:"-"                      envPrefix:"PICOCLAW_TOOLS_WEB_"`
	Brave       BraveConfig        `yaml:"brave,omitempty"                                        json:"brave"`
	Tavily      TavilyConfig       `yaml:"tavily,omitempty"                                       json:"tavily"`
	Kagi        KagiConfig         `yaml:"kagi,omitempty"                                         json:"kagi"`
	Sogou       SogouConfig        `yaml:"-"                                                      json:"sogou"`
	DuckDuckGo  DuckDuckGoConfig   `yaml:"-"                                                      json:"duckduckgo"`
	Gemini      GeminiSearchConfig `yaml:"gemini,omitempty"                                       json:"gemini"`
	Perplexity  PerplexityConfig   `yaml:"perplexity,omitempty"                                   json:"perplexity"`
	SearXNG     SearXNGConfig      `yaml:"-"                                                      json:"searxng"`
	GLMSearch   GLMSearchConfig    `yaml:"glm_search,omitempty"                                   json:"glm_search"`
	BaiduSearch BaiduSearchConfig  `yaml:"baidu_search,omitempty"                                 json:"baidu_search"`
	Provider    string             `yaml:"-"                                                      json:"provider,omitempty" env:"PICOCLAW_TOOLS_WEB_PROVIDER"`
	// PreferNative controls whether to use provider-native web search when
	// the active LLM supports it (e.g. OpenAI web_search_preview). When true,
	// the client-side web_search tool is hidden to avoid duplicate search surfaces,
	// and the provider's built-in search is used instead. Falls back to client-side
	// search when the provider does not support native search.
	PreferNative bool `yaml:"-" json:"prefer_native" env:"PICOCLAW_TOOLS_WEB_PREFER_NATIVE"`
	// Proxy is an optional proxy URL for web tools (http/https/socks5/socks5h).
	// For authenticated proxies, prefer HTTP_PROXY/HTTPS_PROXY env vars instead of embedding credentials in config.
	Proxy                string              `yaml:"-" json:"proxy,omitempty"                  env:"PICOCLAW_TOOLS_WEB_PROXY"`
	FetchLimitBytes      int64               `yaml:"-" json:"fetch_limit_bytes,omitempty"      env:"PICOCLAW_TOOLS_WEB_FETCH_LIMIT_BYTES"`
	Format               string              `yaml:"-" json:"format,omitempty"                 env:"PICOCLAW_TOOLS_WEB_FORMAT"`
	PrivateHostWhitelist FlexibleStringSlice `yaml:"-" json:"private_host_whitelist,omitempty" env:"PICOCLAW_TOOLS_WEB_PRIVATE_HOST_WHITELIST"`
}

type CronToolsConfig struct {
	ToolConfig `envPrefix:"PICOCLAW_TOOLS_CRON_"`
	// 0 means no timeout.
	ExecTimeoutMinutes    int      `json:"exec_timeout_minutes"    env:"PICOCLAW_TOOLS_CRON_EXEC_TIMEOUT_MINUTES"`
	AllowCommand          bool     `json:"allow_command"           env:"PICOCLAW_TOOLS_CRON_ALLOW_COMMAND"`
	CommandAllowedRemotes []string `json:"command_allowed_remotes" env:"PICOCLAW_TOOLS_CRON_COMMAND_ALLOWED_REMOTES"`
}

type ExecConfig struct {
	ToolConfig          `         envPrefix:"PICOCLAW_TOOLS_EXEC_"`
	EnableDenyPatterns  bool     `                                 json:"enable_deny_patterns"  env:"PICOCLAW_TOOLS_EXEC_ENABLE_DENY_PATTERNS"`
	AllowRemote         bool     `                                 json:"allow_remote"          env:"PICOCLAW_TOOLS_EXEC_ALLOW_REMOTE"`
	CustomDenyPatterns  []string `                                 json:"custom_deny_patterns"  env:"PICOCLAW_TOOLS_EXEC_CUSTOM_DENY_PATTERNS"`
	CustomAllowPatterns []string `                                 json:"custom_allow_patterns" env:"PICOCLAW_TOOLS_EXEC_CUSTOM_ALLOW_PATTERNS"`
	TimeoutSeconds      int      `                                 json:"timeout_seconds"       env:"PICOCLAW_TOOLS_EXEC_TIMEOUT_SECONDS"` // 0 means use default (60s)
}

type SkillsToolsConfig struct {
	ToolConfig `                       yaml:"-"                    envPrefix:"PICOCLAW_TOOLS_SKILLS_"`
	Registries SkillsRegistriesConfig `yaml:"registries,omitempty"                                    json:"registries"`
	// Deprecated: use registries.github instead.
	Github                SkillsGithubConfig `yaml:"github,omitempty" json:"github"`
	MaxConcurrentSearches int                `yaml:"-"                json:"max_concurrent_searches" env:"PICOCLAW_TOOLS_SKILLS_MAX_CONCURRENT_SEARCHES"`
	SearchCache           SearchCacheConfig  `yaml:"-"                json:"search_cache"`
}

type MediaCleanupConfig struct {
	ToolConfig `    envPrefix:"PICOCLAW_MEDIA_CLEANUP_"`
	MaxAge     int `                                    json:"max_age_minutes"  env:"PICOCLAW_MEDIA_CLEANUP_MAX_AGE"`
	Interval   int `                                    json:"interval_minutes" env:"PICOCLAW_MEDIA_CLEANUP_INTERVAL"`
}

type ReadFileToolConfig struct {
	Enabled         bool   `json:"enabled"`
	Mode            string `json:"mode"`
	MaxReadFileSize int    `json:"max_read_file_size"`
}

const (
	ReadFileModeBytes = "bytes"
	ReadFileModeLines = "lines"
)

func (c ReadFileToolConfig) EffectiveMode() string {
	switch strings.ToLower(strings.TrimSpace(c.Mode)) {
	case ReadFileModeLines:
		return ReadFileModeLines
	case "", ReadFileModeBytes:
		return ReadFileModeBytes
	default:
		return ReadFileModeBytes
	}
}

type ToolsConfig struct {
	AllowReadPaths  []string `json:"allow_read_paths"  yaml:"-" env:"PICOCLAW_TOOLS_ALLOW_READ_PATHS"`
	AllowWritePaths []string `json:"allow_write_paths" yaml:"-" env:"PICOCLAW_TOOLS_ALLOW_WRITE_PATHS"`
	// FilterSensitiveData controls whether to filter sensitive values (API keys,
	// tokens, secrets) from tool results before sending to the LLM.
	// Default: true (enabled)
	FilterSensitiveData bool `json:"filter_sensitive_data" yaml:"-" env:"PICOCLAW_TOOLS_FILTER_SENSITIVE_DATA"`
	// FilterMinLength is the minimum content length required for filtering.
	// Content shorter than this will be returned unchanged for performance.
	// Default: 8
	FilterMinLength int                `json:"filter_min_length" yaml:"-"                env:"PICOCLAW_TOOLS_FILTER_MIN_LENGTH"`
	Web             WebToolsConfig     `json:"web"               yaml:"web,omitempty"`
	Cron            CronToolsConfig    `json:"cron"              yaml:"-"`
	Exec            ExecConfig         `json:"exec"              yaml:"-"`
	Skills          SkillsToolsConfig  `json:"skills"            yaml:"skills,omitempty"`
	MediaCleanup    MediaCleanupConfig `json:"media_cleanup"     yaml:"-"`
	MCP             MCPConfig          `json:"mcp"               yaml:"-"`
	AppendFile      ToolConfig         `json:"append_file"       yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_APPEND_FILE_"`
	EditFile        ToolConfig         `json:"edit_file"         yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_EDIT_FILE_"`
	FindSkills      ToolConfig         `json:"find_skills"       yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_FIND_SKILLS_"`
	I2C             ToolConfig         `json:"i2c"               yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_I2C_"`
	InstallSkill    ToolConfig         `json:"install_skill"     yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_INSTALL_SKILL_"`
	ListDir         ToolConfig         `json:"list_dir"          yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_LIST_DIR_"`
	LoadImage       ToolConfig         `json:"load_image"        yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_LOAD_IMAGE_"`
	Message         MessageToolsConfig `json:"message"           yaml:"-"`
	ReadFile        ReadFileToolConfig `json:"read_file"         yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_READ_FILE_"`
	Serial          ToolConfig         `json:"serial"            yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_SERIAL_"`
	SendFile        ToolConfig         `json:"send_file"         yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_SEND_FILE_"`
	SendTTS         ToolConfig         `json:"send_tts"          yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_SEND_TTS_"`
	Spawn           ToolConfig         `json:"spawn"             yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_SPAWN_"`
	SpawnStatus     ToolConfig         `json:"spawn_status"      yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_SPAWN_STATUS_"`
	SPI             ToolConfig         `json:"spi"               yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_SPI_"`
	Subagent        ToolConfig         `json:"subagent"          yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_SUBAGENT_"`
	WebFetch        ToolConfig         `json:"web_fetch"        yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_WEB_FETCH_"`
	WriteFile       ToolConfig         `json:"write_file"       yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_WRITE_FILE_"`
	Memory          ToolConfig         `json:"memory"           yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_MEMORY_"`
	SessionSearch   ToolConfig         `json:"session_search"   yaml:"-"                                                       envPrefix:"PICOCLAW_TOOLS_SESSION_SEARCH_"`
}

// IsFilterSensitiveDataEnabled returns true if sensitive data filtering is enabled
func (c *ToolsConfig) IsFilterSensitiveDataEnabled() bool {
	return c.FilterSensitiveData
}

// GetFilterMinLength returns the minimum content length for filtering (default: 8)
func (c *ToolsConfig) GetFilterMinLength() int {
	if c.FilterMinLength <= 0 {
		return 8
	}
	return c.FilterMinLength
}

type SearchCacheConfig struct {
	MaxSize    int `json:"max_size"    env:"PICOCLAW_SKILLS_SEARCH_CACHE_MAX_SIZE"`
	TTLSeconds int `json:"ttl_seconds" env:"PICOCLAW_SKILLS_SEARCH_CACHE_TTL_SECONDS"`
}

type SkillsRegistriesConfig []*SkillRegistryConfig

func (c *SkillsRegistriesConfig) Get(name string) (SkillRegistryConfig, bool) {
	if c == nil {
		return SkillRegistryConfig{}, false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return SkillRegistryConfig{}, false
	}
	for _, registry := range *c {
		if registry == nil || registry.Name != name {
			continue
		}
		return *registry, true
	}
	return SkillRegistryConfig{}, false
}

func (c *SkillsRegistriesConfig) Set(name string, cfg SkillRegistryConfig) {
	if c == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	cfg.Name = name
	for i, registry := range *c {
		if registry == nil || registry.Name != name {
			continue
		}
		(*c)[i] = &cfg
		return
	}
	*c = append(*c, &cfg)
}

type SkillsGithubConfig struct {
	BaseURL string       `json:"base_url,omitempty" yaml:"-"               env:"PICOCLAW_TOOLS_SKILLS_GITHUB_BASE_URL"`
	Token   SecureString `json:"token,omitzero"     yaml:"token,omitempty" env:"PICOCLAW_TOOLS_SKILLS_GITHUB_TOKEN"`
	Proxy   string       `json:"proxy,omitempty"    yaml:"-"               env:"PICOCLAW_TOOLS_SKILLS_GITHUB_PROXY"`
}

type SkillRegistryConfig struct {
	Name      string         `json:"name,omitempty"      yaml:"-"                    env:"-"`
	Enabled   bool           `json:"enabled"             yaml:"-"                    env:"-"`
	BaseURL   string         `json:"base_url"            yaml:"-"                    env:"-"`
	AuthToken SecureString   `json:"auth_token,omitzero" yaml:"auth_token,omitempty" env:"-"`
	Param     map[string]any `json:"-"                   yaml:"-"                    env:"-"`
}

const (
	envSkillsClawHubEnabled         = "PICOCLAW_SKILLS_REGISTRIES_CLAWHUB_ENABLED"
	envSkillsClawHubBaseURL         = "PICOCLAW_SKILLS_REGISTRIES_CLAWHUB_BASE_URL"
	envSkillsClawHubAuthToken       = "PICOCLAW_SKILLS_REGISTRIES_CLAWHUB_AUTH_TOKEN"
	envSkillsClawHubSearchPath      = "PICOCLAW_SKILLS_REGISTRIES_CLAWHUB_SEARCH_PATH"
	envSkillsClawHubSkillsPath      = "PICOCLAW_SKILLS_REGISTRIES_CLAWHUB_SKILLS_PATH"
	envSkillsClawHubDownloadPath    = "PICOCLAW_SKILLS_REGISTRIES_CLAWHUB_DOWNLOAD_PATH"
	envSkillsClawHubTimeout         = "PICOCLAW_SKILLS_REGISTRIES_CLAWHUB_TIMEOUT"
	envSkillsClawHubMaxZipSize      = "PICOCLAW_SKILLS_REGISTRIES_CLAWHUB_MAX_ZIP_SIZE"
	envSkillsClawHubMaxResponseSize = "PICOCLAW_SKILLS_REGISTRIES_CLAWHUB_MAX_RESPONSE_SIZE"
	envSkillsGitHubEnabled          = "PICOCLAW_SKILLS_REGISTRIES_GITHUB_ENABLED"
	envSkillsGitHubBaseURL          = "PICOCLAW_SKILLS_REGISTRIES_GITHUB_BASE_URL"
	envSkillsGitHubAuthToken        = "PICOCLAW_SKILLS_REGISTRIES_GITHUB_AUTH_TOKEN"
	envSkillsGitHubProxy            = "PICOCLAW_SKILLS_REGISTRIES_GITHUB_PROXY"
)

func (c *SkillRegistryConfig) DecodeParam(target any) error {
	if c == nil {
		return nil
	}
	if len(c.Param) == 0 {
		return nil
	}
	data, err := json.Marshal(c.Param)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// MCPServerConfig defines configuration for a single MCP server
type MCPServerConfig struct {
	// Enabled indicates whether this MCP server is active
	Enabled bool `json:"enabled"`
	// Deferred controls whether this server's tools are registered as hidden (deferred/discovery mode).
	// When nil, the global Discovery.Enabled setting applies.
	// When explicitly set to true or false, it overrides the global setting for this server only.
	Deferred *bool `json:"deferred,omitempty"`
	// Command is the executable to run (e.g., "npx", "python", "/path/to/server")
	Command string `json:"command"`
	// Args are the arguments to pass to the command
	Args []string `json:"args,omitempty"`
	// Env are environment variables to set for the server process (stdio only)
	Env map[string]string `json:"env,omitempty"`
	// EnvFile is the path to a file containing environment variables (stdio only)
	EnvFile string `json:"env_file,omitempty"`
	// Type is "stdio", "sse", "http", or "streamable-http".
	// "http" and "streamable-http" both select streamable HTTP request-response
	// mode, while "sse" keeps the standalone SSE listener enabled for
	// server-initiated notifications. Defaults: stdio if command is set, sse if
	// url is set.
	Type string `json:"type,omitempty"`
	// URL is used for SSE/HTTP transport
	URL string `json:"url,omitempty"`
	// Headers are HTTP headers to send with requests (sse/http only)
	Headers map[string]string `json:"headers,omitempty"`
}

// MCPConfig defines configuration for all MCP servers
type MCPConfig struct {
	ToolConfig `                    envPrefix:"PICOCLAW_TOOLS_MCP_"`
	Discovery  ToolDiscoveryConfig `                                json:"discovery"`
	// MaxInlineTextChars controls how much MCP text stays inline before it is saved as an artifact.
	MaxInlineTextChars int `json:"max_inline_text_chars,omitempty" env:"PICOCLAW_TOOLS_MCP_MAX_INLINE_TEXT_CHARS"`
	// Servers is a map of server name to server configuration
	Servers map[string]MCPServerConfig `json:"servers,omitempty"`
}

const DefaultMCPMaxInlineTextChars = 16 * 1024

func (c *MCPConfig) GetMaxInlineTextChars() int {
	if c.MaxInlineTextChars > 0 {
		return c.MaxInlineTextChars
	}
	return DefaultMCPMaxInlineTextChars
}

func LoadConfig(path string) (*Config, error) {
	updateResolver(filepath.Dir(path))

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			logger.WarnF(
				"config file not found, using default config",
				map[string]any{"path": path},
			)
			return DefaultConfig(), nil
		}
		return nil, err
	}

	// First, try to detect config version by reading the version field
	var versionInfo struct {
		Version int `json:"version"`
	}
	if e := json.Unmarshal(data, &versionInfo); e != nil {
		e = wrapJSONError(data, e, "config.json")
		logger.ErrorCF("config", formatDiagnosticLogMessage("Malformed config file", e), map[string]any{"path": path})
		return nil, e
	}
	if len(data) <= 10 {
		logger.Warn(fmt.Sprintf("content is [%s]", string(data)))
		return DefaultConfig(), nil
	}

	// Load config based on detected version
	var cfg *Config
	switch versionInfo.Version {
	case 0:
		logger.InfoF(
			"config migrate start",
			map[string]any{"from": versionInfo.Version, "to": CurrentVersion},
		)
		if err = validateLegacyConfigDiagnostics(data); err != nil {
			logger.ErrorCF(
				"config",
				formatDiagnosticLogMessage("Failed to load config", err),
				map[string]any{"path": path},
			)
			return nil, err
		}

		var m map[string]any
		m, err = loadConfigMap(path)
		if err != nil {
			logger.ErrorCF(
				"config",
				formatDiagnosticLogMessage("Failed to load config", err),
				map[string]any{"path": path},
			)
			return nil, err
		}

		migrateErr := migrateV0ToV1(m)
		if migrateErr != nil {
			return nil, fmt.Errorf("V0→V1 migration failed: %w", migrateErr)
		}
		migrateErr = migrateV1ToV2(m)
		if migrateErr != nil {
			return nil, fmt.Errorf("V1→V2 migration failed: %w", migrateErr)
		}
		migrateErr = migrateV2ToV3(m)
		if migrateErr != nil {
			return nil, fmt.Errorf("V2→V3 migration failed: %w", migrateErr)
		}

		var migrated []byte
		migrated, err = json.Marshal(m)
		if err != nil {
			return nil, err
		}

		cfg, err = loadConfig(migrated)
		if err != nil {
			return nil, err
		}

		err = MakeBackup(path)
		if err != nil {
			return nil, err
		}

		defer func(cfg *Config) {
			_ = SaveConfig(path, cfg)
		}(cfg)
	case 1:
		// V1→V3 migration: rename channels→channel_list, infer Enabled, migrate channel configs
		logger.InfoF(
			"config migrate start",
			map[string]any{"from": versionInfo.Version, "to": CurrentVersion},
		)
		if err = validateLegacyConfigDiagnostics(data); err != nil {
			logger.ErrorCF(
				"config",
				formatDiagnosticLogMessage("Failed to load config", err),
				map[string]any{"path": path},
			)
			return nil, err
		}

		var m map[string]any
		m, err = loadConfigMap(path)
		if err != nil {
			logger.ErrorCF(
				"config",
				formatDiagnosticLogMessage("Failed to load config", err),
				map[string]any{"path": path},
			)
			return nil, err
		}

		migrateErr := migrateV1ToV2(m)
		if migrateErr != nil {
			return nil, fmt.Errorf("V1→V2 migration failed: %w", migrateErr)
		}
		migrateErr = migrateV2ToV3(m)
		if migrateErr != nil {
			return nil, fmt.Errorf("V2→V3 migration failed: %w", migrateErr)
		}

		var migrated []byte
		migrated, err = json.Marshal(m)
		if err != nil {
			return nil, err
		}

		cfg, err = loadConfig(migrated)
		if err != nil {
			return nil, err
		}

		err = MakeBackup(path)
		if err != nil {
			return nil, err
		}

		defer func(cfg *Config) {
			_ = SaveConfig(path, cfg)
		}(cfg)
		logger.InfoF(
			"config migrate success",
			map[string]any{"from": versionInfo.Version, "to": CurrentVersion},
		)
	case 2:
		// V2→V3 migration: rename channels→channel_list, convert flat→nested
		logger.InfoF(
			"config migrate start",
			map[string]any{"from": versionInfo.Version, "to": CurrentVersion},
		)
		if err = validateLegacyConfigDiagnostics(data); err != nil {
			logger.ErrorCF(
				"config",
				formatDiagnosticLogMessage("Failed to load config", err),
				map[string]any{"path": path},
			)
			return nil, err
		}
		var m map[string]any
		m, err = loadConfigMap(path)
		if err != nil {
			logger.ErrorCF(
				"config",
				formatDiagnosticLogMessage("Failed to load config", err),
				map[string]any{"path": path},
			)
			return nil, err
		}
		migrateErr := migrateV2ToV3(m)
		if migrateErr != nil {
			return nil, fmt.Errorf("V2→V3 migration failed: %w", migrateErr)
		}

		var migrated []byte
		migrated, err = json.Marshal(m)
		if err != nil {
			return nil, err
		}

		cfg, err = loadConfig(migrated)
		if err != nil {
			return nil, err
		}

		err = MakeBackup(path)
		if err != nil {
			return nil, err
		}

		defer func(cfg *Config) {
			_ = SaveConfig(path, cfg)
		}(cfg)
		logger.InfoF(
			"config migrate success",
			map[string]any{"from": versionInfo.Version, "to": CurrentVersion},
		)
	case CurrentVersion:
		// Current version
		cfg, err = loadConfig(data)
		if err != nil {
			logger.ErrorCF(
				"config",
				formatDiagnosticLogMessage("Failed to load config", err),
				map[string]any{"path": path},
			)
			return nil, err
		}
		// Load security configuration
		secPath := securityPath(path)
		err = loadSecurityConfig(cfg, secPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed to load security config: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported config version: %d", versionInfo.Version)
	}

	applyLegacyBindingsMigration(data, cfg)

	gatewayHostBeforeEnv := cfg.Gateway.Host

	if err = env.Parse(cfg); err != nil {
		return nil, err
	}
	applySkillsRegistryEnvCompat(cfg)

	if err = InitChannelList(cfg.Channels); err != nil {
		return nil, err
	}
	if err = cfg.ValidateTurnProfile(); err != nil {
		return nil, err
	}
	cfg.Gateway.Host, err = resolveGatewayHostFromEnv(gatewayHostBeforeEnv)
	if err != nil {
		return nil, fmt.Errorf("invalid gateway host: %w", err)
	}

	// Expand multi-key configs into separate entries for key-level failover
	cfg.ModelList = expandMultiKeyModels(cfg.ModelList)

	// Validate model_list for uniqueness and required fields
	if err = cfg.ValidateModelList(); err != nil {
		return nil, err
	}

	// Ensure Workspace has a default if not set
	if cfg.Agents.Defaults.Workspace == "" {
		homePath := GetHome()
		cfg.Agents.Defaults.Workspace = filepath.Join(homePath, pkg.WorkspaceName)
	}

	cfg.Session.ApplyDmScope()
	cfg.Session.DeriveDmScope()

	return cfg, nil
}

func applySkillsRegistryEnvCompat(cfg *Config) {
	if cfg == nil {
		return
	}

	registryCfg, foundClawHub := cfg.Tools.Skills.Registries.Get("clawhub")
	if !foundClawHub {
		registryCfg = SkillRegistryConfig{
			Name:  "clawhub",
			Param: map[string]any{},
		}
	}
	if registryCfg.Param == nil {
		registryCfg.Param = map[string]any{}
	}

	if raw, envSet := os.LookupEnv(envSkillsClawHubEnabled); envSet {
		if value, err := strconv.ParseBool(strings.TrimSpace(raw)); err == nil {
			registryCfg.Enabled = value
		}
	}
	if value, envSet := os.LookupEnv(envSkillsClawHubBaseURL); envSet {
		registryCfg.BaseURL = value
	}
	if value, envSet := os.LookupEnv(envSkillsClawHubAuthToken); envSet {
		registryCfg.AuthToken = *NewSecureString(value)
	}
	if value, envSet := os.LookupEnv(envSkillsClawHubSearchPath); envSet {
		registryCfg.Param["search_path"] = value
	}
	if value, envSet := os.LookupEnv(envSkillsClawHubSkillsPath); envSet {
		registryCfg.Param["skills_path"] = value
	}
	if value, envSet := os.LookupEnv(envSkillsClawHubDownloadPath); envSet {
		registryCfg.Param["download_path"] = value
	}
	if raw, envSet := os.LookupEnv(envSkillsClawHubTimeout); envSet {
		if value, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			registryCfg.Param["timeout"] = value
		}
	}
	if raw, envSet := os.LookupEnv(envSkillsClawHubMaxZipSize); envSet {
		if value, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			registryCfg.Param["max_zip_size"] = value
		}
	}
	if raw, envSet := os.LookupEnv(envSkillsClawHubMaxResponseSize); envSet {
		if value, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			registryCfg.Param["max_response_size"] = value
		}
	}

	cfg.Tools.Skills.Registries.Set("clawhub", registryCfg)

	githubCfg, foundGitHub := cfg.Tools.Skills.Registries.Get("github")
	if !foundGitHub {
		githubCfg = SkillRegistryConfig{
			Name:  "github",
			Param: map[string]any{},
		}
	}
	if githubCfg.Param == nil {
		githubCfg.Param = map[string]any{}
	}

	if raw, envSet := os.LookupEnv(envSkillsGitHubEnabled); envSet {
		if value, err := strconv.ParseBool(strings.TrimSpace(raw)); err == nil {
			githubCfg.Enabled = value
		}
	}
	if value, envSet := os.LookupEnv(envSkillsGitHubBaseURL); envSet {
		githubCfg.BaseURL = value
	}
	if value, envSet := os.LookupEnv(envSkillsGitHubAuthToken); envSet {
		githubCfg.AuthToken = *NewSecureString(value)
	}
	if value, envSet := os.LookupEnv(envSkillsGitHubProxy); envSet {
		githubCfg.Param["proxy"] = value
	}

	cfg.Tools.Skills.Registries.Set("github", githubCfg)
}

func MakeBackup(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	dateSuffix := time.Now().Format(".20060102.bak")
	// Backup config file
	bakPath := path + dateSuffix
	if err := fileutil.CopyFile(path, bakPath, 0o600); err != nil {
		logger.ErrorF("failed to create config backup", map[string]any{"error": err})
		return fmt.Errorf("failed to create config backup: %w", err)
	}
	// Backup security config file
	secPath := securityPath(path)
	if _, err := os.Stat(secPath); err == nil {
		secBakPath := secPath + dateSuffix
		if secErr := fileutil.CopyFile(secPath, secBakPath, 0o600); secErr != nil {
			logger.ErrorF("failed to create security backup", map[string]any{"error": secErr})
			return fmt.Errorf("failed to create security backup: %w", secErr)
		}
	}
	return nil
}

func toNameIndex(list []*ModelConfig) []string {
	nameList := make([]string, 0, len(list))
	countMap := make(map[string]int)
	for _, model := range list {
		name := model.ModelName
		index := countMap[name]
		nameList = append(nameList, fmt.Sprintf("%s:%d", name, index))
		countMap[name]++
	}
	return nameList
}

func SaveConfig(path string, cfg *Config) error {
	if cfg.Version < CurrentVersion {
		cfg.Version = CurrentVersion
	}
	// Filter out virtual models before serializing to config file
	nonVirtualModels := make([]*ModelConfig, 0, len(cfg.ModelList))
	for _, m := range cfg.ModelList {
		if !m.isVirtual {
			nonVirtualModels = append(nonVirtualModels, m)
		}
	}
	// Temporarily replace ModelList with filtered version for serialization
	originalModelList := cfg.ModelList
	defer func() {
		// Restore original ModelList after serialization
		cfg.ModelList = originalModelList
	}()
	cfg.ModelList = nonVirtualModels

	if err := saveSecurityConfig(securityPath(path), cfg); err != nil {
		logger.ErrorCF("config", "cannot save .security.yml", map[string]any{"error": err})
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

func (c *Config) WorkspacePath() string {
	return expandHome(c.Agents.Defaults.Workspace)
}

func expandHome(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, _ := os.UserHomeDir()
		if len(path) > 1 && path[1] == '/' {
			return home + path[1:]
		}
		return home
	}
	return path
}

// GetModelConfig returns the ModelConfig for the given model name.
// If multiple configs exist with the same model_name, it uses round-robin
// selection for load balancing. Returns an error if the model is not found.
func (c *Config) GetModelConfig(modelName string) (*ModelConfig, error) {
	matches := c.findMatches(modelName)
	if len(matches) == 0 {
		return nil, fmt.Errorf("model %q not found in model_list or providers", modelName)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}

	// Multiple configs - use round-robin for load balancing
	idx := (rrCounter.Add(1) - 1) % uint64(len(matches))
	return matches[idx], nil
}

// findMatches finds all ModelConfig entries with the given model_name.
func (c *Config) findMatches(modelName string) []*ModelConfig {
	var matches []*ModelConfig
	for i := range c.ModelList {
		if c.ModelList[i].ModelName == modelName {
			matches = append(matches, c.ModelList[i])
		}
	}
	return matches
}

// ValidateModelList validates all ModelConfig entries in the model_list.
// It checks that each model config is valid.
// Note: Multiple entries with the same model_name are allowed for load balancing.
func (c *Config) ValidateModelList() error {
	for i := range c.ModelList {
		if err := c.ModelList[i].Validate(); err != nil {
			return fmt.Errorf("model_list[%d]: %w", i, err)
		}
	}
	return nil
}

func (c *Config) SecurityCopyFrom(path string) error {
	return loadSecurityConfig(c, securityPath(path))
}

// ResetToDefaults backs up the current config, creates a default config,
// preserves security credentials from the existing config, and saves it.
func ResetToDefaults(configPath string) error {
	if err := MakeBackup(configPath); err != nil {
		return fmt.Errorf("backup before reset: %w", err)
	}
	cfg := DefaultConfig()
	cfg.Session.ApplyDmScope()
	cfg.Session.DeriveDmScope()
	if err := cfg.SecurityCopyFrom(configPath); err != nil {
		logger.WarnF("could not preserve security config", map[string]any{"error": err})
	}
	return SaveConfig(configPath, cfg)
}

func expandMultiKeyModels(models []*ModelConfig) []*ModelConfig {
	var expanded []*ModelConfig

	for _, m := range models {
		keys := m.APIKeys.Values()

		// Single key or no keys: keep as-is
		if len(keys) <= 1 {
			expanded = append(expanded, m)
			continue
		}

		// Multiple keys: expand
		originalName := m.ModelName

		// Create entries for additional keys (key_1, key_2, ...)
		var fallbackNames []string
		for i := 1; i < len(keys); i++ {
			suffix := fmt.Sprintf("__key_%d", i)
			expandedName := originalName + suffix

			// Create a copy for the additional key
			additionalEntry := &ModelConfig{
				ModelName:           expandedName,
				Provider:            m.Provider,
				Model:               m.Model,
				APIBase:             m.APIBase,
				APIKeys:             SimpleSecureStrings(keys[i]),
				Proxy:               m.Proxy,
				AuthMethod:          m.AuthMethod,
				ConnectMode:         m.ConnectMode,
				Workspace:           m.Workspace,
				RPM:                 m.RPM,
				MaxTokensField:      m.MaxTokensField,
				RequestTimeout:      m.RequestTimeout,
				ThinkingLevel:       m.ThinkingLevel,
				ToolSchemaTransform: m.ToolSchemaTransform,
				Streaming:           m.Streaming,
				ExtraBody:           m.ExtraBody,
				CustomHeaders:       m.CustomHeaders,
				UserAgent:           m.UserAgent,
				isVirtual:           true,
			}
			expanded = append(expanded, additionalEntry)
			fallbackNames = append(fallbackNames, expandedName)
		}

		// Create the primary entry with first key and fallbacks
		primaryEntry := &ModelConfig{
			ModelName:           originalName,
			Provider:            m.Provider,
			Model:               m.Model,
			APIBase:             m.APIBase,
			Proxy:               m.Proxy,
			AuthMethod:          m.AuthMethod,
			ConnectMode:         m.ConnectMode,
			Workspace:           m.Workspace,
			RPM:                 m.RPM,
			MaxTokensField:      m.MaxTokensField,
			RequestTimeout:      m.RequestTimeout,
			ThinkingLevel:       m.ThinkingLevel,
			ToolSchemaTransform: m.ToolSchemaTransform,
			Streaming:           m.Streaming,
			ExtraBody:           m.ExtraBody,
			CustomHeaders:       m.CustomHeaders,
			UserAgent:           m.UserAgent,
			APIKeys:             SimpleSecureStrings(keys[0]),
		}

		// Prepend new fallbacks to existing ones
		if len(fallbackNames) > 0 {
			primaryEntry.Fallbacks = append(fallbackNames, m.Fallbacks...)
		} else if len(m.Fallbacks) > 0 {
			primaryEntry.Fallbacks = m.Fallbacks
		}

		expanded = append(expanded, primaryEntry)
	}

	return expanded
}

func (t *ToolsConfig) IsToolEnabled(name string) bool {
	switch name {
	case "web":
		return t.Web.Enabled
	case "cron":
		return t.Cron.Enabled
	case "exec":
		return t.Exec.Enabled
	case "skills":
		return t.Skills.Enabled
	case "media_cleanup":
		return t.MediaCleanup.Enabled
	case "append_file":
		return t.AppendFile.Enabled
	case "edit_file":
		return t.EditFile.Enabled
	case "find_skills":
		return t.FindSkills.Enabled
	case "i2c":
		return t.I2C.Enabled
	case "install_skill":
		return t.InstallSkill.Enabled
	case "list_dir":
		return t.ListDir.Enabled
	case "load_image":
		return t.LoadImage.Enabled
	case "message":
		return t.Message.Enabled
	case "read_file":
		return t.ReadFile.Enabled
	case "serial":
		return t.Serial.Enabled
	case "spawn":
		return t.Spawn.Enabled
	case "spawn_status":
		return t.SpawnStatus.Enabled
	case "spi":
		return t.SPI.Enabled
	case "subagent":
		return t.Subagent.Enabled
	case "web_fetch":
		return t.WebFetch.Enabled
	case "send_file":
		return t.SendFile.Enabled
	case "send_tts":
		return t.SendTTS.Enabled
	case "write_file":
		return t.WriteFile.Enabled
	case "memory":
		return t.Memory.Enabled
	case "session_search":
		return t.SessionSearch.Enabled
	case "mcp":
		return t.MCP.Enabled
	default:
		return true
	}
}
