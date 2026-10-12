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
	"strings"
	"time"
)

const memoriesPath = "/v1/memories/"

// Message is a single conversation message ingested into RetainDB.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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

func (c *Client) post(ctx context.Context, path string, body any) ([]byte, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("retaindb base url is empty")
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
		return data, fmt.Errorf("retaindb %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}
