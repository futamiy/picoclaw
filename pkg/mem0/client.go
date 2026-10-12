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
	"strings"
	"time"
)

// Message is a single conversation message ingested into Mem0.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
	if _, err := c.post(ctx, "/v1/memories/", body); err != nil {
		return fmt.Errorf("add messages: %w", err)
	}
	return nil
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

func (c *Client) post(ctx context.Context, path string, body any) ([]byte, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("mem0 base url is empty")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		// Mem0 Platform uses "Token <key>"; several self-hosted builds accept
		// "Bearer <key>". Send both forms for broad compatibility.
		req.Header.Set("Authorization", "Token "+c.apiKey)
		req.Header.Set("X-API-Key", c.apiKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return data, fmt.Errorf("mem0 %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}
