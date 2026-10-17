// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

const fullBackupPrefix = "picoclaw-full-"

// CreateFullBackup zips the entire PicoClaw home directory (config, workspace,
// memory, sessions, state, auth) into outDir and prunes older archives so at
// most keep remain (keep <= 0 keeps all). It returns the new archive path.
//
// When includeSecrets is false, secret-bearing files (.security.yml, auth.json,
// *credential*/*secret* names) are skipped. The archive output directory is
// always excluded so backups never nest inside themselves.
func CreateFullBackup(homeDir, outDir string, keep int, includeSecrets bool) (string, error) {
	if fi, err := os.Stat(homeDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("home directory not found: %s", homeDir)
	}
	if outDir == "" {
		outDir = DefaultFullBackupDir()
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	absOut, _ := filepath.Abs(outDir)

	name := fmt.Sprintf("%s%s.zip", fullBackupPrefix, time.Now().Format(archiveTimeLayout))
	outPath := filepath.Join(outDir, name)
	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)

	walkErr := filepath.Walk(homeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if abs, aerr := filepath.Abs(path); aerr == nil &&
				(abs == absOut || strings.HasPrefix(abs, absOut+string(os.PathSeparator))) {
				return filepath.SkipDir
			}
			return nil
		}
		base := filepath.Base(path)
		// Skip drift snapshots and any existing full/memory archives.
		if strings.Contains(base, ".bak.") ||
			(strings.HasSuffix(base, ".zip") && strings.HasPrefix(base, "picoclaw-")) {
			return nil
		}
		if !includeSecrets && isSecretFile(base) {
			return nil
		}
		rel, relErr := filepath.Rel(homeDir, path)
		if relErr != nil {
			return relErr
		}
		return addFileToZip(zw, path, filepath.ToSlash(rel))
	})
	if err := zw.Close(); err != nil && walkErr == nil {
		walkErr = err
	}
	if err := f.Close(); err != nil && walkErr == nil {
		walkErr = err
	}
	if walkErr != nil {
		os.Remove(outPath)
		return "", walkErr
	}

	if keep > 0 {
		pruneFullBackups(outDir, keep)
	}
	return outPath, nil
}

// isSecretFile reports whether a file name denotes secret-bearing content that
// full backups omit when IncludeSecrets is off.
func isSecretFile(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case ".security.yml", "auth.json":
		return true
	}
	return strings.Contains(lower, "secret") || strings.Contains(lower, "credential")
}

// ListFullBackups returns full-backup archives in outDir, newest first.
func ListFullBackups(outDir string) ([]MemoryArchive, error) {
	if outDir == "" {
		outDir = DefaultFullBackupDir()
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []MemoryArchive
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), fullBackupPrefix) || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, MemoryArchive{
			Path:    filepath.Join(outDir, e.Name()),
			Name:    e.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

func pruneFullBackups(outDir string, keep int) {
	archives, err := ListFullBackups(outDir)
	if err != nil || len(archives) <= keep {
		return
	}
	for _, a := range archives[keep:] {
		os.Remove(a.Path)
	}
}

// DefaultFullBackupDir is where full backups are written unless configured
// otherwise: <home>/.picoclaw/backups (shared with memory archives).
func DefaultFullBackupDir() string {
	return DefaultMemoryArchiveDir()
}

// FullBackupService periodically archives the whole PicoClaw home directory to
// the configured backup directory. It is a no-op when disabled.
type FullBackupService struct {
	homeDir        string
	dir            string
	keep           int
	interval       time.Duration
	includeSecrets bool
	enabled        bool
	cancel         context.CancelFunc
}

// NewFullBackupService creates the scheduled full-backup service. A non-positive
// interval falls back to 24h.
func NewFullBackupService(homeDir, dir string, enabled bool, interval time.Duration, keep int, includeSecrets bool) *FullBackupService {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &FullBackupService{
		homeDir:        homeDir,
		dir:            dir,
		keep:           keep,
		interval:       interval,
		includeSecrets: includeSecrets,
		enabled:        enabled,
	}
}

// Start launches the ticker goroutine. It does nothing when disabled.
func (s *FullBackupService) Start() error {
	if s == nil || !s.enabled {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.loop(ctx)
	return nil
}

// Stop halts the ticker.
func (s *FullBackupService) Stop() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
}

func (s *FullBackupService) loop(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			path, err := CreateFullBackup(s.homeDir, s.dir, s.keep, s.includeSecrets)
			if err != nil {
				logger.WarnCF("agent", "scheduled full backup failed", map[string]any{"error": err.Error()})
				continue
			}
			logger.InfoCF("agent", "scheduled full backup written", map[string]any{"path": path})
		}
	}
}
