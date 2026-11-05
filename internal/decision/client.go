package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxResponseBytes caps a decision response; real ones are a few KB.
const maxResponseBytes = 4 << 20

// Decider answers decision requests. Client implements it over HTTP; tests
// and future backends (a local runtime, a cascade) can supply their own.
type Decider interface {
	Decide(ctx context.Context, req Request) (*Result, error)
}

// Client calls a System One decision endpoint (Liquid hosted or Ollama).
type Client struct {
	// URL is the full endpoint, e.g. http://ollama:11434/v1/systemone.
	URL string
	// APIKey is sent as a Bearer token when set (Liquid hosted needs one).
	APIKey string
	HTTP   *http.Client
}

// NewClient returns a Client with a 60 s timeout.
func NewClient(url, apiKey string) *Client {
	return &Client{URL: url, APIKey: apiKey, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// Decide validates req, sends it and parses the answers.
func (c *Client) Decide(ctx context.Context, req Request) (*Result, error) {
	if c.URL == "" {
		return nil, fmt.Errorf("decision: no endpoint configured")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("decision: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decision: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("decision: call %s: %w", c.URL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("decision: read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, &HTTPError{Status: resp.StatusCode, Body: truncate(string(data), 500)}
	}
	return ParseResponse(data, req.Questions)
}

// HTTPError is a non-2xx answer from the decision endpoint.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("decision: endpoint returned %d: %s", e.Status, e.Body)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
