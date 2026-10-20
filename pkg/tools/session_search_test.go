package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSessionFile(t *testing.T, dir, id string, records []sessionSearchRecord, modTime time.Time) {
	t.Helper()
	var sb strings.Builder
	for _, r := range records {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
		sb.WriteString("\n")
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func TestSessionSearch_FindsAcrossSessionsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeSessionFile(t, dir, "old", []sessionSearchRecord{
		{Role: "user", Content: "we discussed the docker deployment"},
	}, now.Add(-2*time.Hour))
	writeSessionFile(t, dir, "new", []sessionSearchRecord{
		{Role: "assistant", Content: "the docker deployment is ready"},
	}, now.Add(-1*time.Hour))

	res := NewSessionSearchTool(dir).Execute(context.Background(), map[string]any{"query": "DOCKER"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "session: new") || !strings.Contains(res.ForLLM, "session: old") {
		t.Fatalf("expected both sessions in result:\n%s", res.ForLLM)
	}
	if strings.Index(res.ForLLM, "session: new") > strings.Index(res.ForLLM, "session: old") {
		t.Fatalf("newest session must come first:\n%s", res.ForLLM)
	}
}

func TestSessionSearch_RoleFilter(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, "s1", []sessionSearchRecord{
		{Role: "user", Content: "alpha from user"},
		{Role: "assistant", Content: "alpha from assistant"},
	}, time.Now())

	res := NewSessionSearchTool(dir).Execute(context.Background(), map[string]any{
		"query": "alpha",
		"role":  "user",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "assistant") {
		t.Fatalf("role filter ignored:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "alpha from user") {
		t.Fatalf("expected user match:\n%s", res.ForLLM)
	}
}

func TestSessionSearch_Regex(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, "s1", []sessionSearchRecord{
		{Role: "user", Content: "deploy v2.5 now"},
		{Role: "user", Content: "no version here"},
	}, time.Now())

	res := NewSessionSearchTool(dir).Execute(context.Background(), map[string]any{
		"query": `v\d+\.\d+`,
		"regex": true,
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "deploy v2.5 now") || strings.Contains(res.ForLLM, "no version here") {
		t.Fatalf("regex filter wrong:\n%s", res.ForLLM)
	}
}

func TestSessionSearch_EmptyQueryAndMissingDir(t *testing.T) {
	tool := NewSessionSearchTool(filepath.Join(t.TempDir(), "missing"))
	if res := tool.Execute(context.Background(), map[string]any{"query": "  "}); !res.IsError {
		t.Fatal("empty query must be an error")
	}
	res := tool.Execute(context.Background(), map[string]any{"query": "anything"})
	if res.IsError {
		t.Fatalf("missing sessions dir should be an empty result, got error: %s", res.ForLLM)
	}
}

func TestSessionSearch_BoundsMatchesAndSnippet(t *testing.T) {
	dir := t.TempDir()
	var records []sessionSearchRecord
	for i := 0; i < sessionSearchMaxMatches+5; i++ {
		records = append(records, sessionSearchRecord{Role: "user", Content: "needle " + strings.Repeat("x", 1000)})
	}
	writeSessionFile(t, dir, "s1", records, time.Now())

	res := NewSessionSearchTool(dir).Execute(context.Background(), map[string]any{"query": "needle"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if n := strings.Count(res.ForLLM, "- session:"); n != sessionSearchMaxMatches {
		t.Fatalf("returned %d matches, want cap %d", n, sessionSearchMaxMatches)
	}
	for _, line := range strings.Split(res.ForLLM, "\n") {
		if strings.Contains(line, "needle") && len([]rune(line)) > sessionSearchSnippetRunes+10 {
			t.Fatalf("snippet not bounded (%d runes)", len([]rune(line)))
		}
	}
}
