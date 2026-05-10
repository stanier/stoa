package agentproto

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/stanier/stoa/internal/chat"
	"golang.org/x/net/websocket"
)

// WSClient implements Client using HTTP for REST endpoints and WebSocket for streaming.
type WSClient struct {
	baseURL       string // e.g. "http://localhost:7777"
	token         string // Bearer token; empty means no auth header
	tlsSkipVerify bool   // skip TLS certificate verification
	httpClient    *http.Client
}

// NewWSClient creates a new WSClient targeting the given base URL.
// The URL should be the HTTP base (e.g. "http://localhost:7777"); the client will
// convert it to "ws://" automatically for WebSocket connections.
func NewWSClient(baseURL string) *WSClient {
	return &WSClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{},
	}
}

// NewWSClientWithToken creates a WSClient that attaches a Bearer token to every
// HTTP request and WebSocket handshake.
func NewWSClientWithToken(baseURL, token string) *WSClient {
	return &WSClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		httpClient: &http.Client{},
	}
}

// NewWSClientWithOptions creates a WSClient with full option control.
// If tlsSkipVerify is true, TLS certificate verification is disabled for both
// HTTP and WebSocket connections — only use this in development or with known
// self-signed certificates.
func NewWSClientWithOptions(baseURL, token string, tlsSkipVerify bool) *WSClient {
	var httpClient *http.Client
	if tlsSkipVerify {
		log.Printf("agentproto: WARNING: TLS certificate verification disabled for %s — do not use in production", baseURL)
		httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		}
	} else {
		httpClient = &http.Client{}
	}
	return &WSClient{
		baseURL:       strings.TrimRight(baseURL, "/"),
		token:         token,
		tlsSkipVerify: tlsSkipVerify,
		httpClient:    httpClient,
	}
}

// setAuth attaches the Bearer token to r if one is configured.
func (c *WSClient) setAuth(r *http.Request) {
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// CreateSession calls POST /sessions and returns the new session ID.
func (c *WSClient) CreateSession(ctx context.Context, cfg SessionConfig) (string, error) {
	body, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("agentproto: marshal session config: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/sessions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("agentproto: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("agentproto: POST /sessions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("agentproto: POST /sessions returned %d: %s", resp.StatusCode, string(data))
	}

	var result CreateSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("agentproto: decode session response: %w", err)
	}

	return result.SessionID, nil
}

// ListSessions calls GET /sessions and returns the list of active sessions.
func (c *WSClient) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/sessions", nil)
	if err != nil {
		return nil, fmt.Errorf("agentproto: build request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentproto: GET /sessions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agentproto: GET /sessions returned %d: %s", resp.StatusCode, string(data))
	}

	var sessions []SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, fmt.Errorf("agentproto: decode sessions list: %w", err)
	}

	return sessions, nil
}

// ClearSession calls POST /sessions/:id/clear to reset the conversation history.
func (c *WSClient) ClearSession(ctx context.Context, sessionID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/sessions/"+sessionID+"/clear", nil)
	if err != nil {
		return fmt.Errorf("agentproto: build request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("agentproto: POST /sessions/%s/clear: %w", sessionID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("agentproto: clear session returned %d: %s", resp.StatusCode, string(data))
	}

	return nil
}

// CompactSession calls POST /sessions/:id/compact to trigger manual compaction.
func (c *WSClient) CompactSession(ctx context.Context, sessionID string) (CompactResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/sessions/"+sessionID+"/compact", nil)
	if err != nil {
		return CompactResponse{}, fmt.Errorf("agentproto: build request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CompactResponse{}, fmt.Errorf("agentproto: POST /sessions/%s/compact: %w", sessionID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return CompactResponse{}, fmt.Errorf("agentproto: compact session returned %d: %s", resp.StatusCode, string(data))
	}

	var result CompactResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return CompactResponse{}, fmt.Errorf("agentproto: decode compact response: %w", err)
	}
	return result, nil
}

// GetTokens calls GET /sessions/:id/tokens to retrieve token counts.
func (c *WSClient) GetTokens(ctx context.Context, sessionID string) (TokensResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/sessions/"+sessionID+"/tokens", nil)
	if err != nil {
		return TokensResponse{}, fmt.Errorf("agentproto: build request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return TokensResponse{}, fmt.Errorf("agentproto: GET /sessions/%s/tokens: %w", sessionID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return TokensResponse{}, fmt.Errorf("agentproto: get tokens returned %d: %s", resp.StatusCode, string(data))
	}

	var result TokensResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return TokensResponse{}, fmt.Errorf("agentproto: decode tokens response: %w", err)
	}
	return result, nil
}

// GetMessages calls GET /sessions/:id/messages and returns the conversation history
// (excluding system messages).
func (c *WSClient) GetMessages(ctx context.Context, sessionID string) ([]chat.Message, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/sessions/"+sessionID+"/messages", nil)
	if err != nil {
		return nil, fmt.Errorf("agentproto: build request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentproto: GET /sessions/%s/messages: %w", sessionID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agentproto: get messages returned %d: %s", resp.StatusCode, string(data))
	}

	var msgs []chat.Message
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return nil, fmt.Errorf("agentproto: decode messages response: %w", err)
	}
	return msgs, nil
}

// RestoreMessages calls POST /sessions/:id/restore to replace a session's non-system
// conversation history with the given messages.
func (c *WSClient) RestoreMessages(ctx context.Context, sessionID string, messages []chat.Message) error {
	body, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("agentproto: marshal messages: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/sessions/"+sessionID+"/restore", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agentproto: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("agentproto: POST /sessions/%s/restore: %w", sessionID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("agentproto: restore messages returned %d: %s", resp.StatusCode, string(data))
	}
	return nil
}

// Connect opens a WebSocket connection to the given session and returns a ClientConn.
func (c *WSClient) Connect(ctx context.Context, sessionID string) (ClientConn, error) {
	// Convert http:// → ws:// (or https:// → wss://)
	wsURL := strings.Replace(c.baseURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL = wsURL + "/sessions/" + sessionID + "/ws"

	// The origin is required by golang.org/x/net/websocket but not validated by most servers.
	origin := c.baseURL

	wsCfg, err := websocket.NewConfig(wsURL, origin)
	if err != nil {
		return nil, fmt.Errorf("agentproto: build ws config: %w", err)
	}
	if c.token != "" {
		wsCfg.Header = http.Header{}
		wsCfg.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.tlsSkipVerify {
		wsCfg.TlsConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}

	conn, err := websocket.DialConfig(wsCfg)
	if err != nil {
		return nil, fmt.Errorf("agentproto: dial %s: %w", wsURL, err)
	}

	wc := &wsConn{
		ws:     conn,
		events: make(chan Event, 64),
	}

	// Start background reader goroutine that pushes events to the channel.
	go wc.readLoop()

	return wc, nil
}

// Close is a no-op for WSClient since it holds no persistent connections.
func (c *WSClient) Close() error {
	return nil
}

// wsConn wraps a websocket.Conn and implements ClientConn.
type wsConn struct {
	ws     *websocket.Conn
	events chan Event
}

// Send serialises msg as JSON and writes it to the WebSocket connection.
func (w *wsConn) Send(msg ClientMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("agentproto: marshal client message: %w", err)
	}
	if err := websocket.Message.Send(w.ws, string(data)); err != nil {
		return fmt.Errorf("agentproto: ws send: %w", err)
	}
	return nil
}

// Events returns the channel of server→client events.
func (w *wsConn) Events() <-chan Event {
	return w.events
}

// Close closes the underlying WebSocket connection and drains the event channel.
func (w *wsConn) Close() error {
	return w.ws.Close()
}

// readLoop reads JSON frames from the WebSocket and decodes them into Events.
// It closes the events channel when the connection ends.
func (w *wsConn) readLoop() {
	defer close(w.events)
	for {
		var raw string
		if err := websocket.Message.Receive(w.ws, &raw); err != nil {
			// Connection closed or error — send a fatal error event then stop.
			if err != io.EOF {
				w.events <- Event{
					Type:  EventTypeError,
					Error: &ErrorEvent{Message: err.Error(), Fatal: true},
				}
			}
			return
		}

		var ev Event
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			// Skip malformed frames.
			continue
		}
		w.events <- ev
	}
}
