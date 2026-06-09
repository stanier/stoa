// Package cache provides an in-memory response cache for LLM clients.
// Responses are keyed by a hash of the messages and tools; cache hits replay
// the original chunks rather than calling the upstream provider.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
)

// Stats holds cache hit/miss counters.
type Stats struct {
	Hits   int64
	Misses int64
}

type entry struct {
	response string
	chunks   []llm.StreamChunk
	addedAt  time.Time
}

// MemoryCache is a thread-safe in-memory response cache.
// maxEntries <= 0 means unlimited.
type MemoryCache struct {
	mu         sync.Mutex
	entries    map[string]*entry
	maxEntries int
	hits       atomic.Int64
	misses     atomic.Int64
}

// NewMemoryCache returns a MemoryCache that evicts expired entries every 5 minutes.
// The context controls the background cleanup goroutine lifetime.
func NewMemoryCache(ctx context.Context, maxEntries int) *MemoryCache {
	c := &MemoryCache{
		entries:    make(map[string]*entry),
		maxEntries: maxEntries,
	}
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.evictExpired(time.Now())
			}
		}
	}()
	return c
}

// Stats returns current hit/miss counters.
func (c *MemoryCache) Stats() Stats {
	return Stats{
		Hits:   c.hits.Load(),
		Misses: c.misses.Load(),
	}
}

func (c *MemoryCache) get(key string, ttl time.Duration) (*entry, bool) {
	c.mu.Lock()
	e, ok := c.entries[key]
	c.mu.Unlock()
	if !ok || time.Since(e.addedAt) > ttl {
		c.misses.Add(1)
		return nil, false
	}
	c.hits.Add(1)
	return e, true
}

func (c *MemoryCache) set(key string, e *entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxEntries > 0 && len(c.entries) >= c.maxEntries {
		// Evict one arbitrary entry to stay within the cap.
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
	c.entries[key] = e
}

func (c *MemoryCache) evictExpired(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Without per-entry TTL knowledge we evict entries older than 24h as a
	// safety net; the CachingClient re-checks TTL on every get.
	const maxAge = 24 * time.Hour
	for k, e := range c.entries {
		if now.Sub(e.addedAt) > maxAge {
			delete(c.entries, k)
		}
	}
}

// cachingClient wraps an llm.Client and serves cached responses when available.
type cachingClient struct {
	inner        llm.Client
	cache        *MemoryCache
	ttl          time.Duration
	providerName string
	model        string
}

// NewCachingClient returns an llm.Client that transparently caches responses.
// Cache keys include the provider name and model so that config changes
// automatically bypass stale entries.
func NewCachingClient(inner llm.Client, c *MemoryCache, ttl time.Duration, providerName, model string) llm.Client {
	return &cachingClient{
		inner:        inner,
		cache:        c,
		ttl:          ttl,
		providerName: providerName,
		model:        model,
	}
}

func (c *cachingClient) key(messages []chat.Message, tools []llm.Tool) (string, error) {
	type cacheKey struct {
		Provider string
		Model    string
		Messages []chat.Message
		Tools    []llm.Tool
	}
	b, err := json.Marshal(cacheKey{
		Provider: c.providerName,
		Model:    c.model,
		Messages: messages,
		Tools:    tools,
	})
	if err != nil {
		return "", fmt.Errorf("cache: marshal key: %w", err)
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum), nil
}

// SendMessage returns a cached response when available, otherwise delegates and caches the result.
func (c *cachingClient) SendMessage(ctx context.Context, messages []chat.Message, tools []llm.Tool) (string, error) {
	k, err := c.key(messages, tools)
	if err == nil {
		if e, ok := c.cache.get(k, c.ttl); ok {
			return e.response, nil
		}
	}

	resp, err := c.inner.SendMessage(ctx, messages, tools)
	if err != nil {
		return "", err
	}

	if k != "" {
		c.cache.set(k, &entry{response: resp, addedAt: time.Now()})
	}
	return resp, nil
}

// StreamMessage returns a channel that replays cached chunks or streams live
// from the upstream and stores the result for future cache hits.
func (c *cachingClient) StreamMessage(ctx context.Context, messages []chat.Message, tools []llm.Tool) (<-chan llm.StreamChunk, error) {
	k, keyErr := c.key(messages, tools)
	if keyErr == nil {
		if e, ok := c.cache.get(k, c.ttl); ok && len(e.chunks) > 0 {
			ch := make(chan llm.StreamChunk, len(e.chunks))
			for _, chunk := range e.chunks {
				ch <- chunk
			}
			close(ch)
			return ch, nil
		}
	}

	upstream, err := c.inner.StreamMessage(ctx, messages, tools)
	if err != nil {
		return nil, err
	}

	// Intercept the stream: collect chunks for caching, then relay them.
	out := make(chan llm.StreamChunk, 64)
	go func() {
		defer close(out)
		var collected []llm.StreamChunk
		var fullResponse string
		for chunk := range upstream {
			collected = append(collected, chunk)
			fullResponse += chunk.Content
			out <- chunk
		}
		if keyErr == nil && len(collected) > 0 {
			c.cache.set(k, &entry{
				response: fullResponse,
				chunks:   collected,
				addedAt:  time.Now(),
			})
		}
	}()
	return out, nil
}

// ListModels delegates directly without caching (model lists change infrequently
// and are already fetched lazily by the provider registry).
func (c *cachingClient) ListModels(ctx context.Context) ([]string, error) {
	return c.inner.ListModels(ctx)
}

// ListModelInfo delegates directly without caching.
func (c *cachingClient) ListModelInfo(ctx context.Context) ([]llm.ModelInfo, error) {
	return c.inner.ListModelInfo(ctx)
}
