package agent

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryArchiveRoundTrip(t *testing.T) {
	workspace := t.TempDir()
	outDir := t.TempDir()
	mem := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(mem, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "keep fact one\n"
	if err := os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := CreateMemoryArchive(workspace, outDir, "", false, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	archives, err := ListMemoryArchives(outDir)
	if err != nil || len(archives) != 1 || archives[0].Path != path {
		t.Fatalf("list: %v archives=%+v", err, archives)
	}

	// Change the file, then restore should bring the original back and leave a
	// drift .bak of the changed content.
	changed := "mutated\n"
	if err := os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreMemoryArchive(path, workspace, ""); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(mem, "MEMORY.md"))
	if string(got) != original {
		t.Fatalf("restore content = %q, want %q", got, original)
	}
	baks, _ := filepath.Glob(filepath.Join(mem, "MEMORY.md.bak.*"))
	if len(baks) != 1 {
		t.Fatalf("expected one safety .bak, got %v", baks)
	}
	if b, _ := os.ReadFile(baks[0]); strings.TrimSpace(string(b)) != "mutated" {
		t.Fatalf("safety .bak should hold pre-restore content, got %q", b)
	}
}

func TestMemoryArchivePrune(t *testing.T) {
	workspace := t.TempDir()
	outDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Distinct names require distinct timestamps; fake old archives directly.
	for _, n := range []string{"picoclaw-memory-20250101-000000.zip", "picoclaw-memory-20250102-000000.zip", "picoclaw-memory-20250103-000000.zip"} {
		if err := os.WriteFile(filepath.Join(outDir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pruneArchives(outDir, 2)
	left, _ := ListMemoryArchives(outDir)
	if len(left) != 2 || left[0].Name != "picoclaw-memory-20250103-000000.zip" {
		t.Fatalf("prune kept wrong set: %+v", left)
	}
}

func TestMemoryArchiveDriftSkipsSnapshots(t *testing.T) {
	workspace := t.TempDir()
	outDir := t.TempDir()
	mem := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(mem, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte("now\n"), 0o600)
	os.WriteFile(filepath.Join(mem, "MEMORY.md.bak.123"), []byte("old\n"), 0o600)

	path, err := CreateMemoryArchive(workspace, outDir, "", false, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if len(names) != 1 || names[0] != "memory/MEMORY.md" {
		t.Fatalf("archive should contain only the live file, got %v", names)
	}
}

func TestMemoryBackupService_ScheduledArchive(t *testing.T) {
	workspace := t.TempDir()
	outDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(workspace, "memory", "MEMORY.md"), []byte("x\n"), 0o600)

	svc := NewMemoryBackupService(workspace, "", outDir, true, 10*time.Millisecond, 3)
	if err := svc.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer svc.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if archives, _ := ListMemoryArchives(outDir); len(archives) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected a scheduled archive within 2s")
}

func TestMemoryBackupService_DisabledNoop(t *testing.T) {
	outDir := t.TempDir()
	svc := NewMemoryBackupService(t.TempDir(), "", outDir, false, time.Millisecond, 1)
	if err := svc.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	svc.Stop()
	time.Sleep(30 * time.Millisecond)
	if archives, _ := ListMemoryArchives(outDir); len(archives) != 0 {
		t.Fatalf("disabled service must not archive, got %v", archives)
	}
}
