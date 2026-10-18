package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/config"
)

// fullBackupDir resolves the configured full-backup directory, falling back to
// the default (~/.picoclaw/backups) when unset.
func fullBackupDir(cfg *config.Config) string {
	if dir := strings.TrimSpace(cfg.Backup.Dir); dir != "" {
		return dir
	}
	return agent.DefaultFullBackupDir()
}

// handleListFullBackups returns full-home backup archives, newest first.
//
//	GET /api/backup/full
func (h *Handler) handleListFullBackups(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	dir := fullBackupDir(cfg)
	archives, err := agent.ListFullBackups(dir)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list backups: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"dir": dir, "backups": archives})
}

// handleCreateFullBackup writes a full-home backup archive on demand.
//
//	POST /api/backup/full
func (h *Handler) handleCreateFullBackup(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	home := strings.TrimSpace(config.GetHome())
	if home == "" {
		http.Error(w, "cannot resolve PicoClaw home", http.StatusInternalServerError)
		return
	}
	path, err := agent.CreateFullBackup(
		home,
		fullBackupDir(cfg),
		cfg.Backup.EffectiveKeep(),
		cfg.Backup.EffectiveIncludeSecrets(),
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create backup: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": path})
}
