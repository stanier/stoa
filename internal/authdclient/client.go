// Package authdclient provides a lightweight HTTP client for validating session
// tokens against the authd authentication daemon.
//
// Services (agentd, bridges) use this to authenticate inbound requests without
// needing direct Postgres access or knowledge of upstream OAuth providers.
package authdclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// TokenInfo is the parsed response from authd's /token/verify endpoint.
type TokenInfo struct {
	UserID    string
	Email     string
	Name      string
	ExpiresAt time.Time
	IsService bool // true for service accounts (bridges, daemons)
}

// Client calls authd to validate Bearer tokens.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New returns a Client that validates tokens against the given authd base URL.
// Example: New("http://authd:8888") or New("https://auth.example.com")
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Verify validates rawToken against authd and returns the associated TokenInfo.
// Returns ErrUnauthorized when the token is missing, invalid, or expired.
// Returns a wrapped error for network/server failures.
func (c *Client) Verify(ctx context.Context, rawToken string) (*TokenInfo, error) {
	url := c.baseURL + "/token/verify"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("authdclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+rawToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("authdclient: verify request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		var body struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			log.Printf("authdclient: decode error body: %v", err)
		}
		if body.Error == "token expired" {
			return nil, ErrExpired
		}
		return nil, ErrUnauthorized
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authdclient: unexpected status %d from authd", resp.StatusCode)
	}

	var body struct {
		UserID    string `json:"user_id"`
		Email     string `json:"email"`
		Name      string `json:"name"`
		ExpiresAt string `json:"expires_at"`
		IsService bool   `json:"is_service"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("authdclient: decode response: %w", err)
	}

	info := &TokenInfo{
		UserID:    body.UserID,
		Email:     body.Email,
		Name:      body.Name,
		IsService: body.IsService,
	}
	if body.ExpiresAt != "" {
		info.ExpiresAt, err = time.Parse(time.RFC3339, body.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("authdclient: parse expires_at %q: %w", body.ExpiresAt, err)
		}
	}
	return info, nil
}

// ErrUnauthorized is returned when a token is invalid or not recognised by authd.
var ErrUnauthorized = fmt.Errorf("authdclient: unauthorized")

// ErrExpired is returned when a token exists but has passed its expiry time.
var ErrExpired = fmt.Errorf("authdclient: token expired")
