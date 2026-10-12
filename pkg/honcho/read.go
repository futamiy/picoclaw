package honcho

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// This file adds the read/reasoning half of the Honcho v3 API to Client.
// The base client (client.go) only pushes transcripts; these methods pull
// memory back so the agent can inject and query what Honcho has learned.
//
// All calls are cheap to construct and safe to invoke with a caller-supplied
// context timeout; none of them retain state on the Client. Responses are
// parsed defensively: unknown fields are ignored and missing optional fields
// decode to zero values so a server version skew degrades gracefully.

// SessionContext is the response of GetSessionContext: a session summary plus
// the recent messages that fit the token budget.
type SessionContext struct {
	ID        string           `json:"id"`
	Summary   *Summary         `json:"summary,omitempty"`
	Messages  []ContextMessage `json:"messages,omitempty"`
	PeerRepr  string           `json:"peer_representation,omitempty"`
	PeerCards []string         `json:"peer_card,omitempty"`
}

// ContextMessage is a message inside a session context response.
type ContextMessage struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	PeerID    string `json:"peer_id"`
	SessionID string `json:"session_id"`
	CreatedAt string `json:"created_at"`
}

// Summary is a session summary as returned by Honcho.
type Summary struct {
	Content     string `json:"content"`
	MessageID   string `json:"message_id"`
	SummaryType string `json:"summary_type"`
	CreatedAt   string `json:"created_at"`
	TokenCount  int    `json:"token_count"`
}

// PeerContext is the response of GetPeerContext: a peer representation and
// card, from an observer's perspective.
type PeerContext struct {
	PeerID         string   `json:"peer_id"`
	TargetID       string   `json:"target_id"`
	Representation string   `json:"representation,omitempty"`
	PeerCard       []string `json:"peer_card,omitempty"`
}

// PeerCard is the response of GetPeerCard.
type PeerCard struct {
	PeerCard []string `json:"peer_card,omitempty"`
}

// Conclusion is a single conclusion returned by CreateConclusions.
type Conclusion struct {
	ID         string `json:"id"`
	Content    string `json:"content"`
	ObserverID string `json:"observer_id"`
	ObservedID string `json:"observed_id"`
	SessionID  string `json:"session_id,omitempty"`
	Level      string `json:"level,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// SearchOptions configures SearchPeer.
type SearchOptions struct {
	Query   string         `json:"query"`
	Filters map[string]any `json:"filters,omitempty"`
	Limit   int            `json:"limit,omitempty"`
}

// SessionContextOptions configures GetSessionContext. All fields are optional.
type SessionContextOptions struct {
	// Tokens bounds the whole context (summary + messages + representation).
	// Zero leaves it unset (server uses its own default cap).
	Tokens int
	// Summary toggles inclusion of the session summary when available.
	Summary bool
	// PeerTarget, when set, includes the representation and peer card for that
	// peer (observed from the session's perspective).
	PeerTarget string
}

// GetSessionContext returns a context object for a session: its summary and
// the recent messages that fit within the requested token budget.
func (c *Client) GetSessionContext(ctx context.Context, workspaceID, sessionID string, opts SessionContextOptions) (*SessionContext, error) {
	q := url.Values{}
	if opts.Tokens > 0 {
		q.Set("tokens", strconv.Itoa(opts.Tokens))
	}
	q.Set("summary", strconv.FormatBool(opts.Summary))
	if p := strings.TrimSpace(opts.PeerTarget); p != "" {
		q.Set("peer_target", p)
	}
	path := "/v3/workspaces/" + workspaceID + "/sessions/" + sessionID + "/context"
	data, err := c.get(ctx, path, q)
	if err != nil {
		return nil, err
	}
	var out SessionContext
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode session context: %w", err)
	}
	return &out, nil
}

// GetPeerContext returns a peer's representation and card. When target is
// empty the observer's own context is returned.
func (c *Client) GetPeerContext(ctx context.Context, workspaceID, peerID, target string) (*PeerContext, error) {
	q := url.Values{}
	if t := strings.TrimSpace(target); t != "" {
		q.Set("target", t)
	}
	path := "/v3/workspaces/" + workspaceID + "/peers/" + peerID + "/context"
	data, err := c.get(ctx, path, q)
	if err != nil {
		return nil, err
	}
	var out PeerContext
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode peer context: %w", err)
	}
	return &out, nil
}

// GetPeerCard returns the card the observer peer holds for target. An empty
// target returns the observer's own card.
func (c *Client) GetPeerCard(ctx context.Context, workspaceID, peerID, target string) (*PeerCard, error) {
	q := url.Values{}
	if t := strings.TrimSpace(target); t != "" {
		q.Set("target", t)
	}
	path := "/v3/workspaces/" + workspaceID + "/peers/" + peerID + "/card"
	data, err := c.get(ctx, path, q)
	if err != nil {
		return nil, err
	}
	var out PeerCard
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode peer card: %w", err)
	}
	return &out, nil
}

// SearchPeer returns raw message excerpts matching the query, ranked by the
// server. No synthesis is performed.
func (c *Client) SearchPeer(ctx context.Context, workspaceID, peerID string, opts SearchOptions) ([]ContextMessage, error) {
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	if opts.Limit > 100 {
		opts.Limit = 100
	}
	path := "/v3/workspaces/" + workspaceID + "/peers/" + peerID + "/search"
	data, err := c.post(ctx, path, opts)
	if err != nil {
		return nil, err
	}
	var out []ContextMessage
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode peer search: %w", err)
	}
	return out, nil
}

// ChatOptions configures Chat (the dialectic reasoning endpoint).
type ChatOptions struct {
	Query          string `json:"query"`
	SessionID      string `json:"session_id,omitempty"`
	Target         string `json:"target,omitempty"`
	ReasoningLevel string `json:"reasoning_level,omitempty"`
}

// Chat runs a natural-language query through Honcho's dialectic engine and
// returns the synthesized answer. This triggers an LLM call on Honcho's
// backend, so callers should use it sparingly.
func (c *Client) Chat(ctx context.Context, workspaceID, peerID string, opts ChatOptions) (string, error) {
	path := "/v3/workspaces/" + workspaceID + "/peers/" + peerID + "/chat"
	data, err := c.post(ctx, path, opts)
	if err != nil {
		return "", err
	}
	var out struct {
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode chat: %w", err)
	}
	if out.Content == nil {
		return "", nil
	}
	return *out.Content, nil
}

// ConclusionInput describes a single conclusion to create.
type ConclusionInput struct {
	Content    string
	ObserverID string
	ObservedID string
	SessionID  string
}

// CreateConclusion writes a single persistent conclusion about a peer.
func (c *Client) CreateConclusion(ctx context.Context, workspaceID string, in ConclusionInput) (*Conclusion, error) {
	if strings.TrimSpace(in.Content) == "" {
		return nil, fmt.Errorf("conclusion content is empty")
	}
	item := map[string]any{
		"content":     in.Content,
		"observer_id": in.ObserverID,
		"observed_id": in.ObservedID,
	}
	if s := strings.TrimSpace(in.SessionID); s != "" {
		item["session_id"] = s
	}
	body := map[string]any{"conclusions": []any{item}}
	path := "/v3/workspaces/" + workspaceID + "/conclusions"
	data, err := c.post(ctx, path, body)
	if err != nil {
		return nil, err
	}
	var out []Conclusion
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode conclusion: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// get performs an authenticated GET against path with optional query values.
func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("honcho base url is empty")
	}
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
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
		return data, fmt.Errorf("honcho GET %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}
