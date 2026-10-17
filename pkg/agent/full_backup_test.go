// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// TestCreateFullBackup_ScopeAndExclusions verifies that a full backup zips the
// whole home (config + workspace + memory), skips drift snapshots and the
// output dir, and omits secret files when includeSecrets is false.
func TestCreateFullBackup_ScopeAndExclusions(t *testing.T) {
	home := t.TempDir()
	out := filepath.Join(t.TempDir(), "backups")

	mustWrite := func(rel, content string) {
		t.Helper()
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("config.json", `{"version":1}`)
	mustWrite("workspace/memory/MEMORY.md", "notes")
	mustWrite("workspace/memory/MEMORY.md.bak.123", "old")
	mustWrite(".security.yml", "secret: yes")
	mustWrite("auth.json", "{}")

	path, err := CreateFullBackup(home, out, 5, false)
	if err != nil {
		t.Fatalf("CreateFullBackup error = %v", err)
	}

	names := zipNames(t, path)
	for _, want := range []string{"config.json", "workspace/memory/MEMORY.md"} {
		if !names[want] {
			t.Errorf("archive missing %q; got %v", want, names)
		}
	}
	for _, skip := range []string{"workspace/memory/MEMORY.md.bak.123", ".security.yml", "auth.json"} {
		if names[skip] {
			t.Errorf("archive should have skipped %q", skip)
		}
	}

	// Secrets are included when explicitly requested.
	path2, err := CreateFullBackup(home, out, 5, true)
	if err != nil {
		t.Fatalf("CreateFullBackup(includeSecrets) error = %v", err)
	}
	if names2 := zipNames(t, path2); !names2[".security.yml"] || !names2["auth.json"] {
		t.Errorf("includeSecrets archive missing secret files: %v", names2)
	}
}

func TestCreateFullBackup_Prune(t *testing.T) {
	out := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		fullBackupPrefix + "20260101-000001.zip",
		fullBackupPrefix + "20260102-000001.zip",
		fullBackupPrefix + "20260103-000001.zip",
	} {
		if err := os.WriteFile(filepath.Join(out, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	pruneFullBackups(out, 2)

	archives, err := ListFullBackups(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 2 {
		t.Fatalf("archives = %d, want 2 (keep)", len(archives))
	}
	// Newest two are kept, oldest pruned.
	if archives[1].Name != fullBackupPrefix+"20260102-000001.zip" {
		t.Fatalf("oldest kept = %q, want 20260102 archive", archives[1].Name)
	}
}

func zipNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()
	names := make(map[string]bool, len(zr.File))
	for _, f := range zr.File {
		names[f.Name] = true
	}
	return names
}
