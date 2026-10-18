// Package retaindb is a minimal REST client for a RetainDB memory server. It
// only covers the slice needed to push conversation transcripts. There is no
// official Go SDK, so requests are hand-rolled.
//
// ponytail: RetainDB's public REST contract is not pinned here; the endpoint
// (/v1/memories/) and payload shape are the conventional ones. Adjust `memoriesPath`
// (or New) when the exact API is known.
package retaindb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const memoriesPath = "/v1/memories/"

// Message is a single conversation message ingested into RetainDB.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Memory is a single memory returned by the read endpoints.
type Memory struct {
	ID         string  `json:"id"`
	Content    string  `json:"content"`
	MemoryType string  `json:"memory_type"`
	Score      float64 `json:"score"`
}

// Client talks to a RetainDB server.
type Client struct {
	baseURL   string
	apiKey    string
	namespace string
	hc        *http.Client
}

// New builds a client for baseURL (no trailing slash needed) using apiKey as a
// Bearer token. An empty apiKey is allowed for unauthenticated instances.
func New(baseURL, apiKey, namespace string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:    strings.TrimSpace(apiKey),
		namespace: strings.TrimSpace(namespace),
		hc:        &http.Client{Timeout: 15 * time.Second},
	}
}

// AddMessages ingests messages into the configured namespace.
func (c *Client) AddMessages(ctx context.Context, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	body := map[string]any{"messages": msgs}
	if c.namespace != "" {
		body["namespace"] = c.namespace
	}
	if _, err := c.post(ctx, memoriesPath, body); err != nil {
		return fmt.Errorf("add messages: %w", err)
	}
	return nil
}

// SearchMemories returns memories relevant to query, scoped to userID and the
// configured namespace (RetainDB's project partition).
func (c *Client) SearchMemories(ctx context.Context, userID, sessionID, query string, limit int) ([]Memory, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("retaindb search query is empty")
	}
	if limit <= 0 {
		limit = 10
	}
	body := map[string]any{
		"user_id":         strings.TrimSpace(userID),
		"query":           query,
		"top_k":           limit,
		"include_pending": true,
	}
	if c.namespace != "" {
		body["project"] = c.namespace
	}
	if s := strings.TrimSpace(sessionID); s != "" {
		body["session_id"] = s
	}
	data, err := c.post(ctx, "/v1/memory/search", body)
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	return decodeRetainDBMemories(data, "results")
}

// GetProfile returns the stable memories RetainDB holds for userID. It falls
// back to the memories list route on older servers, mirroring the Hermes
// provider.
func (c *Client) GetProfile(ctx context.Context, userID string) ([]Memory, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("retaindb user id is empty")
	}
	q := url.Values{}
	q.Set("include_pending", "true")
	if c.namespace != "" {
		q.Set("project", c.namespace)
	}
	data, err := c.get(ctx, "/v1/memory/profile/"+url.PathEscape(userID)+"?"+q.Encode())
	if err == nil {
		return decodeRetainDBMemories(data, "memories")
	}

	q.Set("user_id", userID)
	q.Set("limit", "200")
	data, fallbackErr := c.get(ctx, "/v1/memories?"+q.Encode())
	if fallbackErr != nil {
		return nil, fmt.Errorf("get profile: %w", err)
	}
	return decodeRetainDBMemories(data, "memories")
}

// decodeRetainDBMemories accepts a list of memory objects as well as the
// search envelope, where each item nests the memory under "memory".
func decodeRetainDBMemories(data []byte, key string) ([]Memory, error) {
	// A bare array is also accepted, mirroring the Mem0 client.
	var direct []json.RawMessage
	if err := json.Unmarshal(data, &direct); err == nil {
		return collectRetainDBMemories(direct), nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode memories: %w", err)
	}
	raw, ok := envelope[key]
	if !ok {
		if list, ok := envelope["results"]; ok {
			raw = list
		} else if list, ok := envelope["memories"]; ok {
			raw = list
		}
	}
	if raw == nil {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("decode memories: %w", err)
	}
	return collectRetainDBMemories(items), nil
}

// collectRetainDBMemories unwraps search results ({"memory": {...}}) and plain
// memory objects, dropping items without content.
func collectRetainDBMemories(items []json.RawMessage) []Memory {
	out := make([]Memory, 0, len(items))
	for _, item := range items {
		var wrapper struct {
			Memory Memory  `json:"memory"`
			Score  float64 `json:"score"`
		}
		if err := json.Unmarshal(item, &wrapper); err == nil && strings.TrimSpace(wrapper.Memory.Content) != "" {
			m := wrapper.Memory
			if m.Score == 0 {
				m.Score = wrapper.Score
			}
			out = append(out, m)
			continue
		}
		var m Memory
		if err := json.Unmarshal(item, &m); err == nil && strings.TrimSpace(m.Content) != "" {
			out = append(out, m)
		}
	}
	return out
}

// Ping checks that the server is reachable. It does not verify credentials.
func (c *Client) Ping(ctx context.Context) error {
	if c.baseURL == "" {
		return fmt.Errorf("retaindb base url is empty")
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
		return fmt.Errorf("retaindb returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("retaindb base url is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("X-API-Key", c.apiKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return data, fmt.Errorf("retaindb GET %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (c *Client) post(ctx context.Context, path string, body any) ([]byte, error) {
	data, status, err := c.postStatus(ctx, path, body)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return data, fmt.Errorf("retaindb %s: status %d: %s", path, status, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// postStatus performs a POST and returns the body plus status code without
// treating non-2xx as a transport error, so callers can fall back to legacy
// routes on 404/405 while still surfacing auth and server failures.
func (c *Client) postStatus(ctx context.Context, path string, body any) ([]byte, int, error) {
	if c.baseURL == "" {
		return nil, 0, fmt.Errorf("retaindb base url is empty")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
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

// postMaybeLegacy posts body to the current route and retries the legacy route
// on 404/405. Other non-2xx statuses abort immediately.
func (c *Client) postMaybeLegacy(ctx context.Context, label string, primary map[string]any, primaryPath, legacyPath string, legacyBody map[string]any) error {
	attempts := []struct {
		path string
		body map[string]any
	}{
		{primaryPath, primary},
		{legacyPath, legacyBody},
	}
	var lastErr error
	for _, attempt := range attempts {
		data, status, err := c.postStatus(ctx, attempt.path, attempt.body)
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		if status >= 200 && status < 300 {
			return nil
		}
		lastErr = fmt.Errorf("retaindb %s %s: status %d: %s", label, attempt.path, status, strings.TrimSpace(string(data)))
		if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
			return lastErr
		}
	}
	return lastErr
}

// IngestSession pushes a conversation transcript to the current RetainDB ingest
// route, falling back to the legacy /v1/memories/ route for older servers.
func (c *Client) IngestSession(ctx context.Context, userID, sessionID string, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	primary := map[string]any{
		"messages":   msgs,
		"write_mode": "sync",
	}
	if c.namespace != "" {
		primary["project"] = c.namespace
	}
	if s := strings.TrimSpace(userID); s != "" {
		primary["user_id"] = s
	}
	if s := strings.TrimSpace(sessionID); s != "" {
		primary["session_id"] = s
	}
	legacy := map[string]any{"messages": msgs}
	if c.namespace != "" {
		legacy["namespace"] = c.namespace
	}
	return c.postMaybeLegacy(ctx, "ingest session", primary, "/v1/memory/ingest/session", "/v1/memories/", legacy)
}

// AddMemory stores one explicit fact or preference about the user.
func (c *Client) AddMemory(ctx context.Context, userID, sessionID, content, memoryType string, importance float64) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("retaindb memory content is empty")
	}
	if strings.TrimSpace(memoryType) == "" {
		memoryType = "factual"
	}
	if importance <= 0 || importance > 1 {
		importance = 0.7
	}
	primary := map[string]any{
		"content":     content,
		"memory_type": strings.TrimSpace(memoryType),
		"importance":  importance,
		"write_mode":  "sync",
	}
	if c.namespace != "" {
		primary["project"] = c.namespace
	}
	if s := strings.TrimSpace(userID); s != "" {
		primary["user_id"] = s
	}
	if s := strings.TrimSpace(sessionID); s != "" {
		primary["session_id"] = s
	}
	legacy := map[string]any{"content": content, "memory_type": primary["memory_type"]}
	if c.namespace != "" {
		legacy["namespace"] = c.namespace
	}
	return c.postMaybeLegacy(ctx, "add memory", primary, "/v1/memory", "/v1/memories", legacy)
}
