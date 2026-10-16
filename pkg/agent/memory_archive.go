// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MemoryArchive describes one archive zip on disk.
type MemoryArchive struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

const archiveTimeLayout = "20060102-150405"

// CreateMemoryArchive zips the workspace memory directory (and, when
// includeConfig, the given config file) into outDir and prunes older archives so
// at most keep remain (keep <= 0 keeps all). It returns the new archive path.
//
// Archives live outside the workspace by default so a wiped workspace does not
// take its only backups with it.
func CreateMemoryArchive(workspace, outDir, configPath string, includeConfig bool, keep int) (string, error) {
	memoryDir := filepath.Join(workspace, "memory")
	if fi, err := os.Stat(memoryDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("memory directory not found: %s", memoryDir)
	}
	if outDir == "" {
		outDir = DefaultMemoryArchiveDir()
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}

	name := fmt.Sprintf("picoclaw-memory-%s.zip", time.Now().Format(archiveTimeLayout))
	outPath := filepath.Join(outDir, name)
	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)

	// Walk memory/ and store entries as "memory/<rel>".
	walkErr := filepath.Walk(memoryDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		// Skip existing drift snapshots so archives do not nest backups.
		if strings.Contains(filepath.Base(path), ".bak.") {
			return nil
		}
		rel, err := filepath.Rel(workspace, path)
		if err != nil {
			return err
		}
		return addFileToZip(zw, path, filepath.ToSlash(rel))
	})
	if walkErr == nil && includeConfig && configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			walkErr = addFileToZip(zw, configPath, "config.json")
		}
	}
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
		pruneArchives(outDir, keep)
	}
	return outPath, nil
}

func addFileToZip(zw *zip.Writer, src, name string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}

// ListMemoryArchives returns archives in outDir, newest first.
func ListMemoryArchives(outDir string) ([]MemoryArchive, error) {
	if outDir == "" {
		outDir = DefaultMemoryArchiveDir()
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
		if e.IsDir() || !strings.HasPrefix(e.Name(), "picoclaw-memory-") || !strings.HasSuffix(e.Name(), ".zip") {
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

func pruneArchives(outDir string, keep int) {
	archives, err := ListMemoryArchives(outDir)
	if err != nil || len(archives) <= keep {
		return
	}
	for _, a := range archives[keep:] {
		os.Remove(a.Path)
	}
}

// RestoreMemoryArchive extracts an archive back into the workspace. Each file it
// overwrites is first copied to <file>.bak.<unix_ts> so a wrong restore is
// reversible. config.json in the archive (if present) is restored to configPath.
func RestoreMemoryArchive(archivePath, workspace, configPath string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()

	for _, zf := range zr.File {
		dest, err := archiveDestPath(zf.Name, workspace, configPath)
		if err != nil {
			return err
		}
		if dest == "" {
			continue
		}
		if err := restoreZipEntry(zf, dest); err != nil {
			return err
		}
	}
	return nil
}

// archiveDestPath maps an archive entry name to a filesystem path, rejecting
// path traversal. It returns "" for entries that should be skipped.
func archiveDestPath(name, workspace, configPath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("unsafe archive entry: %s", name)
	}
	switch {
	case clean == "config.json":
		if configPath == "" {
			return "", nil
		}
		return configPath, nil
	case clean == "memory" || strings.HasPrefix(clean, "memory"+string(os.PathSeparator)):
		rel := strings.TrimPrefix(clean, "memory"+string(os.PathSeparator))
		if rel == "" {
			return "", nil
		}
		return filepath.Join(workspace, "memory", rel), nil
	default:
		return "", nil
	}
}

func restoreZipEntry(zf *zip.File, dest string) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if prev, err := os.ReadFile(dest); err == nil && len(prev) > 0 {
		_ = os.WriteFile(fmt.Sprintf("%s.bak.%d", dest, time.Now().UnixNano()), prev, 0o600)
	}
	return os.WriteFile(dest, data, 0o600)
}

// DefaultMemoryArchiveDir is where archives are written unless configured
// otherwise: <home>/.picoclaw/backups.
func DefaultMemoryArchiveDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "picoclaw-backups")
	}
	return filepath.Join(home, ".picoclaw", "backups")
}
