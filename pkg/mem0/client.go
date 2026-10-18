// Package mem0 is a minimal REST client for a Mem0 memory server
// (https://mem0.ai). It only covers the slice of the API needed to push
// conversation transcripts: message ingestion. There is no official Go SDK, so
// requests are hand-rolled.
package mem0

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Message is a single conversation message ingested into Mem0.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Memory is a single memory returned by the read endpoints. Different Mem0
// versions return the text under "memory" or "content", so both are captured.
type Memory struct {
	ID        string  `json:"id"`
	Memory    string  `json:"memory"`
	Content   string  `json:"content"`
	UserID    string  `json:"user_id"`
	AgentID   string  `json:"agent_id"`
	Score     float64 `json:"score"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// Text returns the best available memory text.
func (m Memory) Text() string {
	if s := strings.TrimSpace(m.Memory); s != "" {
		return s
	}
	return strings.TrimSpace(m.Content)
}

// Client talks to a Mem0 server.
type Client struct {
	baseURL string
	apiKey  string
	hc      *http.Client
}

// New builds a client for baseURL (no trailing slash needed) using apiKey as a
// token. An empty apiKey is allowed for unauthenticated self-hosted instances.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		hc:      &http.Client{Timeout: 15 * time.Second},
	}
}

// AddMessages ingests messages scoped to userID and agentID. Mem0 reasons over
// them in the background.
//
// It targets the current V3 additive pipeline (POST /v3/memories/add/) and
// falls back to the legacy /v1/memories/ route for older or self-hosted
// builds. Entity IDs stay at the top level, which V3 accepts for ingestion.
func (c *Client) AddMessages(ctx context.Context, userID, agentID string, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	body := map[string]any{"messages": msgs}
	if s := strings.TrimSpace(userID); s != "" {
		body["user_id"] = s
	}
	if s := strings.TrimSpace(agentID); s != "" {
		body["agent_id"] = s
	}
	var lastErr error
	for _, path := range []string{"/v3/memories/add/", "/v1/memories/"} {
		data, status, err := c.request(ctx, path, body)
		if err != nil {
			return fmt.Errorf("add messages: %w", err)
		}
		if status >= 200 && status < 300 {
			return nil
		}
		lastErr = fmt.Errorf("mem0 %s: status %d: %s", path, status, strings.TrimSpace(string(data)))
		if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
			break
		}
	}
	return fmt.Errorf("add messages: %w", lastErr)
}

// SearchMemories returns memories relevant to query. It targets the current
// platform search route (v3) and falls back to the older v2 route and the OSS
// "/search" route, so cloud and self-hosted builds both work.
func (c *Client) SearchMemories(ctx context.Context, query, userID, agentID string, limit int) ([]Memory, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("mem0 search query is empty")
	}
	if limit <= 0 {
		limit = 10
	}
	filters := memoryFilters(userID, agentID)
	attempts := []struct {
		path string
		body map[string]any
	}{
		// V3 (current cloud) takes entity IDs inside filters and top_k.
		{"/v3/memories/search/", map[string]any{"query": query, "filters": filters, "top_k": limit}},
		// V2 (older cloud) takes top-level entity IDs and limit.
		{"/v2/memories/search/", map[string]any{"query": query, "user_id": strings.TrimSpace(userID), "agent_id": strings.TrimSpace(agentID), "limit": limit}},
		// OSS / self-hosted dashboard route.
		{"/search", map[string]any{"query": query, "user_id": strings.TrimSpace(userID), "agent_id": strings.TrimSpace(agentID), "limit": limit}},
	}
	return c.readMemories(ctx, "search", attempts)
}

// ListMemories returns the most recent memories for the configured scope. It is
// used when no query is available (the first turn of a session) and as the
// fallback when search is unsupported.
func (c *Client) ListMemories(ctx context.Context, userID, agentID string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 10
	}
	filters := memoryFilters(userID, agentID)
	q := url.Values{}
	q.Set("page", "1")
	q.Set("page_size", strconv.Itoa(limit))
	attempts := []struct {
		path string
		body map[string]any
	}{
		{"/v3/memories/?" + q.Encode(), map[string]any{"filters": filters, "page": 1, "page_size": limit}},
		{"/v2/memories/?" + memoryScopeQuery(userID, agentID, limit).Encode(), nil},
		{"/v1/memories/?" + memoryScopeQuery(userID, agentID, limit).Encode(), nil},
	}
	return c.readMemories(ctx, "list", attempts)
}

func memoryFilters(userID, agentID string) map[string]any {
	filters := map[string]any{}
	if s := strings.TrimSpace(userID); s != "" {
		filters["user_id"] = s
	}
	if s := strings.TrimSpace(agentID); s != "" {
		filters["agent_id"] = s
	}
	return filters
}

func memoryScopeQuery(userID, agentID string, limit int) url.Values {
	q := url.Values{}
	if s := strings.TrimSpace(userID); s != "" {
		q.Set("user_id", s)
	}
	if s := strings.TrimSpace(agentID); s != "" {
		q.Set("agent_id", s)
	}
	q.Set("page", "1")
	q.Set("page_size", strconv.Itoa(limit))
	return q
}

// readMemories walks the endpoint attempts in order, skipping routes the server
// does not implement (404/405) and returning any other failure immediately.
func (c *Client) readMemories(ctx context.Context, label string, attempts []struct {
	path string
	body map[string]any
}) ([]Memory, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("mem0 base url is empty")
	}
	var lastErr error
	for _, attempt := range attempts {
		data, status, err := c.request(ctx, attempt.path, attempt.body)
		if err != nil {
			return nil, err
		}
		if status >= 200 && status < 300 {
			return decodeMemories(data)
		}
		lastErr = fmt.Errorf("mem0 %s %s: status %d: %s", label, attempt.path, status, strings.TrimSpace(string(data)))
		if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
			return nil, lastErr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

// decodeMemories accepts a bare array as well as the paginated/search
// envelopes ({"results": [...]}, {"memories": [...]}).
func decodeMemories(data []byte) ([]Memory, error) {
	var list []Memory
	if err := json.Unmarshal(data, &list); err == nil {
		return list, nil
	}
	var envelope struct {
		Results  []Memory `json:"results"`
		Memories []Memory `json:"memories"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode memories: %w", err)
	}
	if envelope.Results != nil {
		return envelope.Results, nil
	}
	return envelope.Memories, nil
}

// Ping checks that the server is reachable. It does not verify credentials.
func (c *Client) Ping(ctx context.Context) error {
	if c.baseURL == "" {
		return fmt.Errorf("mem0 base url is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/", nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 500 {
		return fmt.Errorf("mem0 returned status %d", resp.StatusCode)
	}
	return nil
}

// request performs POST (when body is non-nil) or GET against path, returning
// the body and status code without treating non-2xx as a transport error. That
// lets callers fall back across API versions on 404/405 while still surfacing
// auth and server errors.
func (c *Client) request(ctx context.Context, path string, body any) ([]byte, int, error) {
	if c.baseURL == "" {
		return nil, 0, fmt.Errorf("mem0 base url is empty")
	}
	method := http.MethodGet
	var reader io.Reader
	if body != nil {
		method = http.MethodPost
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		// Mem0 Platform uses "Token <key>"; several self-hosted builds accept
		// "Bearer <key>". Send both forms for broad compatibility.
		req.Header.Set("Authorization", "Token "+c.apiKey)
		req.Header.Set("X-API-Key", c.apiKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return data, resp.StatusCode, nil
}
