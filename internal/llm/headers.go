package llm

import (
	"context"
	"net/http"
	"sync"
	"time"
)

type ctxUpstreamHeadersKey struct{}

// UpstreamHeaders is a thread-safe slot for storing HTTP response headers
// captured from the upstream API call. Callers attach it to the context
// before calling StreamMessage; the client populates it when the response
// arrives.
type UpstreamHeaders struct {
	mu      sync.Mutex
	headers http.Header
	ready   chan struct{}
}

// NewUpstreamHeaders returns an initialised UpstreamHeaders.
func NewUpstreamHeaders() *UpstreamHeaders {
	return &UpstreamHeaders{ready: make(chan struct{})}
}

// Set stores headers and signals that they are ready.
// Safe to call from multiple goroutines; only the first call takes effect.
func (h *UpstreamHeaders) Set(headers http.Header) {
	h.mu.Lock()
	h.headers = headers.Clone()
	h.mu.Unlock()
	select {
	case <-h.ready: // already closed — no-op
	default:
		close(h.ready)
	}
}

// Get returns a copy of the stored headers (may be nil if not yet set).
func (h *UpstreamHeaders) Get() http.Header {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.headers
}

// Wait blocks until headers are populated, the context is done, or a 500 ms
// timeout elapses. It returns the headers (nil on timeout/cancellation).
// Headers are always available before the first StreamChunk, so in practice
// the 500 ms budget is never consumed.
func (h *UpstreamHeaders) Wait(ctx context.Context) http.Header {
	select {
	case <-h.ready:
		return h.Get()
	case <-ctx.Done():
		return nil
	case <-time.After(500 * time.Millisecond):
		return nil
	}
}

// WithUpstreamHeaders attaches a header sink to the context.
// The LLM client will populate it after receiving the upstream response.
func WithUpstreamHeaders(ctx context.Context, h *UpstreamHeaders) context.Context {
	return context.WithValue(ctx, ctxUpstreamHeadersKey{}, h)
}

// UpstreamHeadersFromContext retrieves the header sink from context.
// Returns nil if not present.
func UpstreamHeadersFromContext(ctx context.Context) *UpstreamHeaders {
	v, _ := ctx.Value(ctxUpstreamHeadersKey{}).(*UpstreamHeaders)
	return v
}
