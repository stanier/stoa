package transport

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// HTTPTransport implements Transport using HTTP with Server-Sent Events
type HTTPTransport struct {
	url    string
	client *http.Client

	conn   *http.Response
	reader *bufio.Reader
	mu     sync.Mutex
}

// NewHTTPTransport creates a new HTTP transport
func NewHTTPTransport(url string) *HTTPTransport {
	return &HTTPTransport{
		url:    url,
		client: &http.Client{},
	}
}

// Connect establishes an SSE connection to the server
func (t *HTTPTransport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.conn != nil {
		return fmt.Errorf("already connected")
	}

	// Create request for SSE endpoint
	req, err := http.NewRequestWithContext(ctx, "GET", t.url+"/sse", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")

	// Make request
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	t.conn = resp
	t.reader = bufio.NewReader(resp.Body)

	return nil
}

// Close closes the HTTP connection
func (t *HTTPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.conn != nil {
		t.conn.Body.Close()
		t.conn = nil
	}

	return nil
}

// Send sends a message via HTTP POST
func (t *HTTPTransport) Send(data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Send via POST to /message endpoint
	req, err := http.NewRequest("POST", t.url+"/message", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// Receive reads an SSE event from the connection
func (t *HTTPTransport) Receive() ([]byte, error) {
	if t.reader == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Read SSE event
	// Format: "data: <json>\n\n"
	for {
		line, err := t.reader.ReadBytes('\n')
		if err != nil {
			return nil, err
		}

		// Skip empty lines
		if len(line) <= 1 {
			continue
		}

		// Check if it's a data line
		if bytes.HasPrefix(line, []byte("data: ")) {
			// Extract JSON data (remove "data: " prefix and trailing newline)
			data := bytes.TrimPrefix(line, []byte("data: "))
			data = bytes.TrimSpace(data)
			return data, nil
		}

		// Skip other SSE fields (event, id, retry)
	}
}

// Reader returns the response body reader
func (t *HTTPTransport) Reader() io.Reader {
	if t.conn != nil {
		return t.conn.Body
	}
	return nil
}

// Writer returns nil (HTTP uses POST for sending)
func (t *HTTPTransport) Writer() io.Writer {
	return nil
}
