package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestFullBackupHandlers(t *testing.T) {
	home := t.TempDir()
	backupDir := filepath.Join(home, "backups")
	t.Setenv(config.EnvHome, home)

	configPath := filepath.Join(home, "config.json")
	cfg := config.DefaultConfig()
	cfg.Backup.Dir = backupDir
	cfg.Backup.Keep = 2
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	h := NewHandler(configPath)

	rec := httptest.NewRecorder()
	h.handleListFullBackups(rec, httptest.NewRequest(http.MethodGet, "/api/backup/full", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.handleCreateFullBackup(rec, httptest.NewRequest(http.MethodPost, "/api/backup/full", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !created.OK {
		t.Fatalf("create response not ok: %s", rec.Body.String())
	}
	if fi, err := os.Stat(created.Path); err != nil || fi.Size() == 0 {
		t.Fatalf("archive not written at %s: %v", created.Path, err)
	}

	rec = httptest.NewRecorder()
	h.handleListFullBackups(rec, httptest.NewRequest(http.MethodGet, "/api/backup/full", nil))
	var listed struct {
		Dir     string                `json:"dir"`
		Backups []agent.MemoryArchive `json:"backups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listed.Dir != backupDir {
		t.Fatalf("dir = %q, want %q", listed.Dir, backupDir)
	}
	if len(listed.Backups) != 1 || listed.Backups[0].Path != created.Path {
		t.Fatalf("backups = %+v, want the created archive", listed.Backups)
	}
}
