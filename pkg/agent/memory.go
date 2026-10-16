// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/logger"
	memorytool "github.com/sipeed/picoclaw/pkg/tools/memory"
)

// ErrMemoryFull is returned when a memory write would exceed the configured
// character limit. The agent is expected to condense (replace/remove) first.
var ErrMemoryFull = errors.New("memory limit exceeded")

// MemoryOptions controls the bounded memory store behavior.
type MemoryOptions struct {
	// LongTermCharLimit caps MEMORY.md size in runes. 0 disables enforcement.
	LongTermCharLimit int
	// DailyNotesEnabled controls whether daily notes are written and injected.
	DailyNotesEnabled bool
	// DailyNoteCharLimit caps each daily note file size in runes. 0 disables enforcement.
	DailyNoteCharLimit int
	// DailyNotesDays is how many recent days of notes are injected.
	DailyNotesDays int
}

// DefaultMemoryOptions mirrors the config defaults, used when no options are given.
func DefaultMemoryOptions() MemoryOptions {
	return MemoryOptions{
		LongTermCharLimit:  config.DefaultMemoryLongTermCharLimit,
		DailyNotesEnabled:  true,
		DailyNoteCharLimit: config.DefaultMemoryDailyNoteCharLimit,
		DailyNotesDays:     config.DefaultMemoryDailyNotesDays,
	}
}

// MemoryStore manages persistent memory for the agent.
// - Long-term memory: memory/MEMORY.md
// - Daily notes: memory/YYYYMM/YYYYMMDD.md
type MemoryStore struct {
	workspace  string
	memoryDir  string
	memoryFile string
	opts       MemoryOptions
	mu         sync.Mutex // guards read-modify-write ops (tool + async extractor)

	overLimitWarn sync.Once // fires once per store when MEMORY.md is found over-limit
}

// NewMemoryStore creates a new MemoryStore with the given workspace path and
// default limits. It ensures the memory directory exists.
func NewMemoryStore(workspace string) *MemoryStore {
	return NewMemoryStoreWithOptions(workspace, DefaultMemoryOptions())
}

// NewMemoryStoreWithOptions creates a new MemoryStore with explicit options.
// It ensures the memory directory exists.
func NewMemoryStoreWithOptions(workspace string, opts MemoryOptions) *MemoryStore {
	if opts.DailyNotesDays <= 0 {
		opts.DailyNotesDays = config.DefaultMemoryDailyNotesDays
	}
	memoryDir := filepath.Join(workspace, "memory")
	memoryFile := filepath.Join(memoryDir, "MEMORY.md")

	// Ensure memory directory exists
	os.MkdirAll(memoryDir, 0o755)

	return &MemoryStore{
		workspace:  workspace,
		memoryDir:  memoryDir,
		memoryFile: memoryFile,
		opts:       opts,
	}
}

// LongTermCharLimit returns the configured MEMORY.md cap (0 = unlimited).
func (ms *MemoryStore) LongTermCharLimit() int { return ms.opts.LongTermCharLimit }

// DailyNotesEnabled reports whether daily notes are written and injected.
func (ms *MemoryStore) DailyNotesEnabled() bool { return ms.opts.DailyNotesEnabled }

// getTodayFile returns the path to today's daily note file (memory/YYYYMM/YYYYMMDD.md).
func (ms *MemoryStore) getTodayFile() string {
	today := time.Now().Format("20060102") // YYYYMMDD
	monthDir := today[:6]                  // YYYYMM
	filePath := filepath.Join(ms.memoryDir, monthDir, today+".md")
	return filePath
}

// recentDailyNotePaths returns the daily note paths for the last days (incl. today).
// Used for prompt cache invalidation tracking.
func (ms *MemoryStore) recentDailyNotePaths(days int) []string {
	paths := make([]string, 0, days)
	for i := range days {
		date := time.Now().AddDate(0, 0, -i)
		dateStr := date.Format("20060102") // YYYYMMDD
		paths = append(paths, filepath.Join(ms.memoryDir, dateStr[:6], dateStr+".md"))
	}
	return paths
}

// ReadLongTerm reads the long-term memory (MEMORY.md).
// Returns empty string if the file doesn't exist.
func (ms *MemoryStore) ReadLongTerm() string {
	if data, err := os.ReadFile(ms.memoryFile); err == nil {
		return string(data)
	}
	return ""
}

// WriteLongTerm writes content to the long-term memory file (MEMORY.md).
// Returns ErrMemoryFull when the content exceeds the configured limit.
func (ms *MemoryStore) WriteLongTerm(content string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.writeLongTermUnlocked(content)
}

func (ms *MemoryStore) writeLongTermUnlocked(content string) error {
	if err := ms.checkLimit(content, ms.opts.LongTermCharLimit); err != nil {
		return err
	}
	// Use unified atomic write utility with explicit sync for flash storage reliability.
	// Using 0o600 (owner read/write only) for secure default permissions.
	return fileutil.WriteFileAtomic(ms.memoryFile, []byte(content), 0o600)
}

// AddLongTerm appends an entry to MEMORY.md. Duplicate entries are ignored.
func (ms *MemoryStore) AddLongTerm(entry string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	entry = strings.TrimSpace(entry)
	if entry == "" {
		return fmt.Errorf("entry is required")
	}
	updated, changed := memoryApplyAdd(ms.ReadLongTerm(), entry)
	if !changed {
		return nil
	}
	return ms.writeLongTermUnlocked(updated)
}

// ReplaceLongTerm replaces oldText with newText in MEMORY.md.
func (ms *MemoryStore) ReplaceLongTerm(oldText, newText string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if oldText == "" {
		return fmt.Errorf("old_text is required")
	}
	updated, err := memoryApplyReplace(ms.ReadLongTerm(), oldText, newText)
	if err != nil {
		return err
	}
	return ms.writeLongTermUnlocked(updated)
}

// RemoveLongTerm removes the first line containing substr from MEMORY.md.
func (ms *MemoryStore) RemoveLongTerm(substr string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	substr = strings.TrimSpace(substr)
	if substr == "" {
		return fmt.Errorf("text is required")
	}
	current := ms.ReadLongTerm()
	if current == "" {
		return nil
	}
	updated, removed := memoryApplyRemove(current, substr)
	if !removed {
		return fmt.Errorf("no memory entry matched %q", substr)
	}
	return ms.writeLongTermUnlocked(updated)
}

// ApplyOperations applies a batch of long-term memory mutations atomically: ops
// run in order against a working copy and the result is written once. The limit
// is checked only on the FINAL content, so a batch can free room (remove/replace)
// and add new entries even when an intermediate size would exceed the cap. Any
// invalid op aborts the whole batch with nothing written.
func (ms *MemoryStore) ApplyOperations(ops []memorytool.Operation) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	working := ms.ReadLongTerm()
	for _, op := range ops {
		switch strings.ToLower(strings.TrimSpace(op.Op)) {
		case "add":
			entry := strings.TrimSpace(op.Content)
			if entry == "" {
				return fmt.Errorf("add: content is required")
			}
			working, _ = memoryApplyAdd(working, entry)
		case "replace":
			if op.OldText == "" {
				return fmt.Errorf("replace: old_text is required")
			}
			updated, err := memoryApplyReplace(working, op.OldText, op.NewText)
			if err != nil {
				return fmt.Errorf("replace %q: %w", op.OldText, err)
			}
			working = updated
		case "remove":
			text := strings.TrimSpace(op.Text)
			if text == "" {
				return fmt.Errorf("remove: text is required")
			}
			updated, removed := memoryApplyRemove(working, text)
			if !removed {
				return fmt.Errorf("remove %q: no memory entry matched", text)
			}
			working = updated
		default:
			return fmt.Errorf("unknown op %q; expected add, replace or remove", op.Op)
		}
	}
	return ms.writeLongTermUnlocked(working)
}

// memoryApplyAdd appends entry to current unless it is already present (dedup).
// It returns the new content and whether anything changed.
func memoryApplyAdd(current, entry string) (string, bool) {
	if memoryHasEntry(current, entry) {
		return current, false
	}
	if strings.TrimSpace(current) == "" {
		return entry + "\n", true
	}
	return strings.TrimRight(current, "\n") + "\n" + entry + "\n", true
}

// memoryApplyReplace swaps the single occurrence of oldText for newText.
func memoryApplyReplace(current, oldText, newText string) (string, error) {
	if !strings.Contains(current, oldText) {
		return "", fmt.Errorf("old_text not found in memory")
	}
	if strings.Count(current, oldText) > 1 {
		return "", fmt.Errorf("old_text appears more than once; provide more context")
	}
	return strings.Replace(current, oldText, newText, 1), nil
}

// memoryApplyRemove drops the first line containing substr.
func memoryApplyRemove(current, substr string) (string, bool) {
	lines := strings.Split(current, "\n")
	kept := make([]string, 0, len(lines))
	removed := false
	for _, line := range lines {
		if !removed && strings.Contains(line, substr) {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n"), removed
}

// ReadToday reads today's daily note.
// Returns empty string if the file doesn't exist.
func (ms *MemoryStore) ReadToday() string {
	todayFile := ms.getTodayFile()
	if data, err := os.ReadFile(todayFile); err == nil {
		return string(data)
	}
	return ""
}

// AppendToday appends content to today's daily note.
// If the file doesn't exist, it creates a new file with a date header.
// Returns ErrMemoryFull when the resulting file would exceed the configured limit.
func (ms *MemoryStore) AppendToday(content string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.appendTodayUnlocked(content)
}

func (ms *MemoryStore) appendTodayUnlocked(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("content is required")
	}
	todayFile := ms.getTodayFile()

	// Ensure month directory exists
	monthDir := filepath.Dir(todayFile)
	if err := os.MkdirAll(monthDir, 0o755); err != nil {
		return err
	}

	var existingContent string
	if data, err := os.ReadFile(todayFile); err == nil {
		existingContent = string(data)
	}

	var newContent string
	if existingContent == "" {
		// Add header for new day
		header := fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02"))
		newContent = header + content
	} else {
		// Append to existing content
		newContent = existingContent + "\n" + content
	}

	if err := ms.checkLimit(newContent, ms.opts.DailyNoteCharLimit); err != nil {
		return err
	}

	// Use unified atomic write utility with explicit sync for flash storage reliability.
	return fileutil.WriteFileAtomic(todayFile, []byte(newContent), 0o600)
}

// GetRecentDailyNotes returns daily notes from the last N days.
// Contents are joined with "---" separator.
func (ms *MemoryStore) GetRecentDailyNotes(days int) string {
	var sb strings.Builder
	first := true

	for i := range days {
		date := time.Now().AddDate(0, 0, -i)
		dateStr := date.Format("20060102") // YYYYMMDD
		monthDir := dateStr[:6]            // YYYYMM
		filePath := filepath.Join(ms.memoryDir, monthDir, dateStr+".md")

		if data, err := os.ReadFile(filePath); err == nil {
			if !first {
				sb.WriteString("\n\n---\n\n")
			}
			sb.Write(data)
			first = false
		}
	}

	return sb.String()
}

// GetMemoryContext returns formatted memory context for the agent prompt.
// Includes long-term memory and recent daily notes.
func (ms *MemoryStore) GetMemoryContext() string {
	longTerm := ms.ReadLongTerm()
	used := utf8.RuneCountInString(longTerm)
	limit := ms.opts.LongTermCharLimit
	if limit > 0 {
		if used > limit {
			// The file can exceed the limit via hand-editing or a lowered limit.
			// Warn once so the refusal reason is visible instead of opaque.
			ms.overLimitWarn.Do(func() {
				logger.WarnCF("agent", "MEMORY.md is over its rune limit; new entries will be refused until it is trimmed", map[string]any{
					"current_runes": used,
					"limit_runes":   limit,
					"over_by":       used - limit,
				})
			})
		}
		longTerm = truncateRunes(longTerm, limit)
	}

	recentNotes := ""
	if ms.opts.DailyNotesEnabled {
		recentNotes = ms.GetRecentDailyNotes(ms.opts.DailyNotesDays)
	}

	if longTerm == "" && recentNotes == "" {
		return ""
	}

	var sb strings.Builder

	if longTerm != "" {
		sb.WriteString("## Long-term Memory")
		if limit > 0 {
			fmt.Fprintf(&sb, " [%d/%d runes]", used, limit)
		}
		sb.WriteString("\n\n")
		sb.WriteString(longTerm)
	}

	if recentNotes != "" {
		if longTerm != "" {
			sb.WriteString("\n\n---\n\n")
		}
		sb.WriteString("## Recent Daily Notes\n\n")
		sb.WriteString(recentNotes)
	}

	return sb.String()
}

// checkLimit returns ErrMemoryFull when content exceeds limit (limit <= 0 = unlimited).
// The error states the current size, the limit and how many runes must be freed, so the
// agent can consolidate (remove/replace stale entries) instead of retrying blindly.
func (ms *MemoryStore) checkLimit(content string, limit int) error {
	if limit <= 0 {
		return nil
	}
	if n := utf8.RuneCountInString(content); n > limit {
		return fmt.Errorf("%w (limit %d runes, got %d; remove or replace stale entries to free at least %d runes, then retry)",
			ErrMemoryFull, limit, n, n-limit)
	}
	return nil
}

// memoryHasEntry reports whether entry already appears as a line in content.
func memoryHasEntry(content, entry string) bool {
	normalized := strings.TrimSpace(entry)
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == normalized {
			return true
		}
	}
	return false
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "\n…[memory truncated]"
}
