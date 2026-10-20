// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/tools/shared"
)

const (
	// sessionSearchMaxSessions bounds how many session files are scanned, newest first.
	sessionSearchMaxSessions = 50
	// sessionSearchMaxMatches bounds returned matches so one call stays cheap.
	sessionSearchMaxMatches = 10
	// sessionSearchSnippetRunes bounds each snippet.
	sessionSearchSnippetRunes = 300
)

// sessionSearchRecord is one line of a session JSONL file.
type sessionSearchRecord struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// sessionSearchMatch is one hit returned to the model.
type sessionSearchMatch struct {
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at,omitempty"`
	Snippet   string `json:"snippet"`
}

// SessionSearchTool searches past sessions stored as JSONL under
// workspace/sessions. Purely local: no network, no index, no LLM calls.
type SessionSearchTool struct {
	sessionsDir string
}

// NewSessionSearchTool creates the tool for a workspace's sessions directory.
func NewSessionSearchTool(sessionsDir string) *SessionSearchTool {
	return &SessionSearchTool{sessionsDir: sessionsDir}
}

func (t *SessionSearchTool) Name() string { return "session_search" }

func (t *SessionSearchTool) Description() string {
	return "Search past conversation sessions on disk (workspace/sessions/*.jsonl) for " +
		"something the user said or you did before. Use it when the user references earlier " +
		"work, a past decision, or asks what you discussed previously. Returns up to " +
		fmt.Sprintf("%d matches (newest sessions first) with a short snippet and the session id; ", sessionSearchMaxMatches) +
		"read the full session file with read_file if you need more detail. " +
		"Query is a case-insensitive literal, or a regex when regex=true."
}

func (t *SessionSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Text to look for (case-insensitive literal by default)",
			},
			"regex": map[string]any{
				"type":        "boolean",
				"description": "Treat query as a regular expression instead of a literal",
			},
			"role": map[string]any{
				"type":        "string",
				"enum":        []string{"user", "assistant"},
				"description": "Only match messages from this role",
			},
		},
		"required": []string{"query"},
	}
}

func (t *SessionSearchTool) Execute(_ context.Context, args map[string]any) *toolshared.ToolResult {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return toolshared.ErrorResult("query is required and must be non-empty")
	}

	useRegex, _ := args["regex"].(bool)
	role, _ := args["role"].(string)
	role = strings.ToLower(strings.TrimSpace(role))

	var match func(string) bool
	if useRegex {
		re, err := regexp.Compile("(?i)" + query)
		if err != nil {
			return toolshared.ErrorResult("invalid regex: " + err.Error())
		}
		match = re.MatchString
	} else {
		lowered := strings.ToLower(query)
		match = func(s string) bool { return strings.Contains(strings.ToLower(s), lowered) }
	}

	files, err := t.sessionFiles()
	if err != nil {
		return toolshared.ErrorResult("cannot read sessions directory: " + err.Error())
	}
	if len(files) == 0 {
		return toolshared.SilentResult("No session history found.")
	}

	var matches []sessionSearchMatch
	for _, path := range files {
		if len(matches) >= sessionSearchMaxMatches {
			break
		}
		matches = append(matches, searchSessionFile(path, match, role, sessionSearchMaxMatches-len(matches))...)
	}
	if len(matches) == 0 {
		return toolshared.SilentResult(fmt.Sprintf("No session messages matched %q.", query))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d match(es) for %q:\n", len(matches), query)
	for _, m := range matches {
		fmt.Fprintf(&sb, "\n- session: %s\n  role: %s", m.SessionID, m.Role)
		if m.CreatedAt != "" {
			fmt.Fprintf(&sb, "  at: %s", m.CreatedAt)
		}
		fmt.Fprintf(&sb, "\n  %s\n", m.Snippet)
	}
	sb.WriteString("\nSnippets are excerpts. Read the session file under workspace/sessions/ for full context.")
	return toolshared.SilentResult(sb.String())
}

// sessionFiles lists session JSONL files newest first, bounded by
// sessionSearchMaxSessions.
func (t *SessionSearchTool) sessionFiles() ([]string, error) {
	entries, err := os.ReadDir(t.sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	type fileInfo struct {
		path    string
		modTime time.Time
	}
	files := make([]fileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{path: filepath.Join(t.sessionsDir, e.Name()), modTime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.After(files[j].modTime) })
	if len(files) > sessionSearchMaxSessions {
		files = files[:sessionSearchMaxSessions]
	}

	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	return paths, nil
}

// searchSessionFile scans one JSONL session file and returns up to limit matches.
func searchSessionFile(path string, match func(string) bool, role string, limit int) []sessionSearchMatch {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var out []sessionSearchMatch
	for scanner.Scan() && len(out) < limit {
		var rec sessionSearchRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		if rec.Role != "user" && rec.Role != "assistant" {
			continue
		}
		if role != "" && rec.Role != role {
			continue
		}
		if !match(rec.Content) {
			continue
		}
		out = append(out, sessionSearchMatch{
			SessionID: sessionID,
			Role:      rec.Role,
			CreatedAt: rec.CreatedAt,
			Snippet:   sessionSnippet(rec.Content),
		})
	}
	return out
}

// sessionSnippet collapses whitespace and bounds length.
func sessionSnippet(content string) string {
	flat := strings.Join(strings.Fields(content), " ")
	runes := []rune(flat)
	if len(runes) <= sessionSearchSnippetRunes {
		return flat
	}
	return string(runes[:sessionSearchSnippetRunes]) + "…"
}
