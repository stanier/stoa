package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm/stats"
	"github.com/stanier/stoa/internal/logging"
)

// FallbackClient implements Client by trying a chain of providers in order.
// On a retryable error it logs a warning and advances to the next provider.
// On a non-retryable error it returns immediately without trying further.
// If the stream has already started sending content, errors are returned as-is
// (partial content already delivered to the caller).
type FallbackClient struct {
	reg            *ProviderRegistry
	chain          []string
	statsReg       *stats.Registry // may be nil — instrumentation is no-op when nil
	dynamicRouting bool
}

// NewFallbackClient constructs a FallbackClient from a registry and an ordered
// list of provider names. Names must match entries registered in the registry.
// Pass a non-nil statsReg to enable per-provider health instrumentation.
// The optional dynamicRouting bool (first bool variadic) enables health-aware
// provider ordering; when true, the chain is re-sorted before each request.
func NewFallbackClient(reg *ProviderRegistry, chain []string, statsReg ...*stats.Registry) *FallbackClient {
	fc := &FallbackClient{reg: reg, chain: chain}
	if len(statsReg) > 0 {
		fc.statsReg = statsReg[0]
	}
	return fc
}

// WithDynamicRouting returns a copy of the FallbackClient with dynamic routing
// enabled or disabled. It does not mutate the receiver.
func (f *FallbackClient) WithDynamicRouting(enabled bool) *FallbackClient {
	copy := *f
	copy.dynamicRouting = enabled
	return &copy
}

// orderedChain returns the provider names to try for a single request.
// When dynamicRouting is false or reg is nil, it returns f.chain unchanged.
// When dynamicRouting is true, it returns a copy of f.chain sorted by:
//  1. Whether the provider has any samples (zero-sample providers sort after those with samples).
//  2. Error rate ascending.
//  3. P95 latency ascending.
//  4. Original chain index ascending (stable tiebreaker preserving configured priority).
func (f *FallbackClient) orderedChain(reg *stats.Registry) []string {
	if !f.dynamicRouting || reg == nil {
		return f.chain
	}

	snap := reg.Snapshot()

	type entry struct {
		name      string
		origIndex int
		hasData   bool
		errRate   float64
		p95       int64
	}

	entries := make([]entry, len(f.chain))
	for i, name := range f.chain {
		s, ok := snap[name]
		entries[i] = entry{
			name:      name,
			origIndex: i,
			hasData:   ok && s.RequestCount > 0,
			errRate:   s.ErrorRate,
			p95:       s.P95LatencyMs,
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		ei, ej := entries[i], entries[j]
		// Providers with no data sort after those with data.
		if ei.hasData != ej.hasData {
			return ei.hasData // hasData=true sorts before hasData=false
		}
		// Both have data (or both lack data): sort by error rate.
		if ei.errRate != ej.errRate {
			return ei.errRate < ej.errRate
		}
		// Same error rate: sort by P95.
		if ei.p95 != ej.p95 {
			return ei.p95 < ej.p95
		}
		// Final tiebreaker: original configured order.
		return ei.origIndex < ej.origIndex
	})

	result := make([]string, len(entries))
	for i, e := range entries {
		result[i] = e.name
	}
	return result
}

// SendMessage tries each provider in the chain, returning the first success.
// Retryable errors advance to the next provider; non-retryable errors abort.
func (f *FallbackClient) SendMessage(ctx context.Context, messages []chat.Message, tools []Tool) (string, error) {
	if len(f.chain) == 0 {
		return "", fmt.Errorf("llm: fallback: empty provider chain")
	}

	var lastErr error
	for _, name := range f.orderedChain(f.statsReg) {
		client, ok := f.reg.GetClient(name)
		if !ok {
			logging.Warnf("llm: fallback: provider %q not found in registry, skipping", name)
			lastErr = fmt.Errorf("provider %q not found", name)
			continue
		}

		start := time.Now()
		result, err := client.SendMessage(ctx, messages, tools)
		elapsed := time.Since(start).Milliseconds()
		if f.statsReg != nil {
			f.statsReg.Get(name).Record(elapsed, err != nil)
		}
		if err == nil {
			return result, nil
		}

		if isRetryable(err) {
			logging.Warnf("llm: fallback: provider %q retryable error, trying next: %v", name, err)
			lastErr = err
			continue
		}

		// Non-retryable — return immediately.
		return "", err
	}

	return "", fmt.Errorf("llm: fallback: all providers failed, last error: %w", lastErr)
}

// StreamMessage tries each provider in the chain for the initial call.
// Once a stream starts delivering content, errors within the stream are
// returned as-is (no retry for mid-stream failures).
func (f *FallbackClient) StreamMessage(ctx context.Context, messages []chat.Message, tools []Tool) (<-chan StreamChunk, error) {
	if len(f.chain) == 0 {
		return nil, fmt.Errorf("llm: fallback: empty provider chain")
	}

	var lastErr error
	for _, name := range f.orderedChain(f.statsReg) {
		client, ok := f.reg.GetClient(name)
		if !ok {
			logging.Warnf("llm: fallback: provider %q not found in registry, skipping", name)
			lastErr = fmt.Errorf("provider %q not found", name)
			continue
		}

		start := time.Now()
		ch, err := client.StreamMessage(ctx, messages, tools)
		if err == nil {
			// Stream opened successfully — wrap it so mid-stream errors pass through
			// and record timing/success status when the stream completes.
			return instrumentedStream(ch, start, name, f.statsReg), nil
		}

		elapsed := time.Since(start).Milliseconds()
		if f.statsReg != nil {
			f.statsReg.Get(name).Record(elapsed, true)
		}

		if isRetryable(err) {
			logging.Warnf("llm: fallback: provider %q retryable error opening stream, trying next: %v", name, err)
			lastErr = err
			continue
		}

		// Non-retryable — return immediately.
		return nil, err
	}

	return nil, fmt.Errorf("llm: fallback: all providers failed, last error: %w", lastErr)
}

// ListModels returns models from the first provider that responds successfully.
func (f *FallbackClient) ListModels(ctx context.Context) ([]string, error) {
	if len(f.chain) == 0 {
		return nil, fmt.Errorf("llm: fallback: empty provider chain")
	}

	var lastErr error
	for _, name := range f.chain {
		client, ok := f.reg.GetClient(name)
		if !ok {
			lastErr = fmt.Errorf("provider %q not found", name)
			continue
		}
		models, err := client.ListModels(ctx)
		if err == nil {
			return models, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("llm: fallback: all providers failed: %w", lastErr)
}

// instrumentedStream passes through all chunks from an already-opened stream
// unchanged and records timing + error status to the stats registry (if non-nil)
// when the stream completes. Mid-stream errors are delivered as-is; no retry is
// attempted once content has started flowing.
func instrumentedStream(src <-chan StreamChunk, start time.Time, provider string, reg *stats.Registry) <-chan StreamChunk {
	out := make(chan StreamChunk, cap(src)+1)
	go func() {
		defer close(out)
		isError := false
		for chunk := range src {
			if chunk.Error != nil {
				isError = true
			}
			out <- chunk
		}
		if reg != nil {
			elapsed := time.Since(start).Milliseconds()
			reg.Get(provider).Record(elapsed, isError)
		}
	}()
	return out
}

// IsRetryable is the exported form of isRetryable for use by other packages
// (e.g. inference-gateway) that need to apply the same retry classification.
func IsRetryable(err error) bool { return isRetryable(err) }

// isRetryable reports whether err warrants trying the next provider in the chain.
// It returns true for:
//   - context.DeadlineExceeded
//   - *net.OpError (network connectivity failures)
//   - *url.Error wrapping a retryable cause
//   - errors whose string representation contains a retryable HTTP status code
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Unwrap url.Error to check inner cause.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return isRetryable(urlErr.Err)
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// Check error message for retryable HTTP status codes.
	msg := err.Error()
	for _, code := range []int{429, 500, 502, 503, 504} {
		if strings.Contains(msg, strconv.Itoa(code)) {
			return true
		}
	}

	return false
}
