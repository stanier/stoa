// Package apikeydclient is a thin HTTP client for validating API keys against apikeyd.
// It mirrors the authdclient package contract so services can treat both
// the session-token and API-key validation paths uniformly.
package apikeydclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

var (
	ErrUnauthorized = errors.New("apikeydclient: invalid or missing API key")
	ErrRevoked      = errors.New("apikeydclient: API key has been revoked")
	ErrExpired      = errors.New("apikeydclient: API key has expired")
)

// KeyInfo is the metadata returned by a successful key verification.
type KeyInfo struct {
	KeyID     string     `json:"key_id"`
	UserID    string     `json:"user_id"`
	Name      string     `json:"name"`       // human label the owner gave the key
	ExpiresAt *time.Time `json:"expires_at"` // nil = never expires
}

// Client validates API keys against apikeyd with a short-lived in-memory cache.
type Client struct {
	baseURL    string
	httpClient *http.Client

	mu    sync.Mutex
	cache map[string]*cacheEntry
}

type cacheEntry struct {
	info    *KeyInfo
	err     error
	expires time.Time
}

// cacheTTL is how long a successful verification result is cached.
// Keep short so revocations take effect quickly.
const cacheTTL = 30 * time.Second

// New creates a Client pointing at the given apikeyd base URL.
// e.g. New("http://apikeyd:8889")
func New(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		cache:      make(map[string]*cacheEntry),
	}
}

// VerifyKey validates rawKey against apikeyd and returns key metadata.
// Results are cached for cacheTTL to avoid hitting apikeyd on every request.
// Errors (revoked, expired, not found) are NOT cached — they are re-checked each time.
func (c *Client) VerifyKey(ctx context.Context, rawKey string) (*KeyInfo, error) {
	// Check cache first (successes only).
	c.mu.Lock()
	if e, ok := c.cache[rawKey]; ok && time.Now().Before(e.expires) {
		info, err := e.info, e.err
		c.mu.Unlock()
		return info, err
	}
	c.mu.Unlock()

	info, err := c.verify(ctx, rawKey)
	if err == nil {
		// Cache successes only.
		c.mu.Lock()
		c.cache[rawKey] = &cacheEntry{info: info, expires: time.Now().Add(cacheTTL)}
		c.mu.Unlock()
	}
	return info, err
}

func (c *Client) verify(ctx context.Context, rawKey string) (*KeyInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/keys/verify", http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("apikeydclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+rawKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apikeydclient: request failed: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var info KeyInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return nil, fmt.Errorf("apikeydclient: decode response: %w", err)
		}
		return &info, nil

	case http.StatusUnauthorized:
		// Distinguish revoked/expired from simply invalid via the error field.
		var body struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		switch body.Error {
		case "API key has been revoked":
			return nil, ErrRevoked
		case "API key has expired":
			return nil, ErrExpired
		default:
			return nil, ErrUnauthorized
		}

	default:
		return nil, fmt.Errorf("apikeydclient: unexpected status %d", resp.StatusCode)
	}
}

// InvalidateCache removes a cached entry for rawKey, forcing re-validation on the next call.
func (c *Client) InvalidateCache(rawKey string) {
	c.mu.Lock()
	delete(c.cache, rawKey)
	c.mu.Unlock()
}
