// Package honcho is a minimal REST client for a Honcho memory server
// (https://honcho.dev). It only covers the slice of the v3 API needed to push
// conversation transcripts: workspace/peer/session bootstrap and message
// ingestion. There is no official Go SDK, so requests are hand-rolled.
package honcho

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

// Message is a single unit ingested into a Honcho session.
type Message struct {
	Content string `json:"content"`
	PeerID  string `json:"peer_id"`
}

// Client talks to a Honcho server.
type Client struct {
	baseURL string
	apiKey  string
	hc      *http.Client
}

// New builds a client for baseURL (no trailing slash needed) using apiKey as a
// Bearer token. An empty apiKey is allowed for unauthenticated self-hosted
// instances.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		hc:      &http.Client{Timeout: 15 * time.Second},
	}
}

// Ensure bootstraps the workspace and a session containing the given peers.
// Honcho creates missing peers automatically when listed in a session.
func (c *Client) Ensure(ctx context.Context, workspaceID, sessionID, userPeerID, agentPeerID string) error {
	if _, err := c.post(ctx, "/v3/workspaces", map[string]any{"id": workspaceID}); err != nil {
		return fmt.Errorf("ensure workspace: %w", err)
	}
	peers := map[string]any{}
	if p := strings.TrimSpace(userPeerID); p != "" {
		peers[p] = map[string]any{}
	}
	if p := strings.TrimSpace(agentPeerID); p != "" {
		peers[p] = map[string]any{}
	}
	body := map[string]any{"id": sessionID}
	if len(peers) > 0 {
		body["peers"] = peers
	}
	if _, err := c.post(ctx, "/v3/workspaces/"+workspaceID+"/sessions", body); err != nil {
		return fmt.Errorf("ensure session: %w", err)
	}
	return nil
}

// AddMessages ingests messages into a session. Honcho reasons over them in the
// background.
func (c *Client) AddMessages(ctx context.Context, workspaceID, sessionID string, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	path := "/v3/workspaces/" + workspaceID + "/sessions/" + sessionID + "/messages"
	if _, err := c.post(ctx, path, map[string]any{"messages": msgs}); err != nil {
		return fmt.Errorf("add messages: %w", err)
	}
	return nil
}

// Ping checks that the server is reachable. It does not verify credentials.
func (c *Client) Ping(ctx context.Context) error {
	if c.baseURL == "" {
		return fmt.Errorf("honcho base url is empty")
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
		return fmt.Errorf("honcho returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, body any) ([]byte, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("honcho base url is empty")
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
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return data, fmt.Errorf("honcho %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// SanitizeID coerces a string into a Honcho-safe identifier matching
// ^[a-zA-Z0-9_-]+$ (max 512 chars). Empty input falls back to "default".
func SanitizeID(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "default"
	}
	if len(out) > 512 {
		out = out[:512]
	}
	return out
}
