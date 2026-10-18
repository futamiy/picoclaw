// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
