package ratelimit

import (
	"fmt"
	"sync"
	"time"
)

// entry holds per-key state.
type entry struct {
	mu         sync.Mutex
	timestamps []time.Time
}

// Limiter is a sliding-window request rate limiter keyed by arbitrary strings
// (typically user IDs). It is safe for concurrent use.
type Limiter struct {
	windowSize time.Duration
	limit      int

	mu      sync.Mutex
	entries map[string]*entry

	stopOnce sync.Once
	stopCh   chan struct{}
}

// New creates a Limiter with the given sliding window and per-key request limit.
// A background goroutine sweeps idle keys every 5 minutes to prevent unbounded
// map growth; call Close to stop it.
// limit == 0 means unlimited — all calls to Allow return (true, "").
func New(windowSize time.Duration, limit int) *Limiter {
	l := &Limiter{
		windowSize: windowSize,
		limit:      limit,
		entries:    make(map[string]*entry),
		stopCh:     make(chan struct{}),
	}
	go l.sweep()
	return l
}

// Allow reports whether a request from key is permitted under the rate limit.
// Thread-safe.
func (l *Limiter) Allow(key string) (ok bool, reason string) {
	if l.limit == 0 {
		return true, ""
	}

	e := l.getOrCreate(key)
	now := time.Now()
	cutoff := now.Add(-l.windowSize)

	e.mu.Lock()
	defer e.mu.Unlock()

	// Prune expired timestamps.
	pruned := e.timestamps[:0]
	for _, t := range e.timestamps {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	e.timestamps = pruned

	if len(e.timestamps) >= l.limit {
		oldest := e.timestamps[0]
		retryAfter := oldest.Add(l.windowSize).Sub(now).Round(time.Second)
		return false, fmt.Sprintf("%d requests per %s exceeded; retry after %s",
			l.limit, l.windowSize, retryAfter)
	}

	e.timestamps = append(e.timestamps, now)
	return true, ""
}

// Stats returns the number of requests recorded in the current window for key.
func (l *Limiter) Stats(key string) int {
	if l.limit == 0 {
		return 0
	}

	l.mu.Lock()
	e, ok := l.entries[key]
	l.mu.Unlock()
	if !ok {
		return 0
	}

	now := time.Now()
	cutoff := now.Add(-l.windowSize)

	e.mu.Lock()
	defer e.mu.Unlock()

	count := 0
	for _, t := range e.timestamps {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}

// Close stops the background sweep goroutine.
func (l *Limiter) Close() {
	l.stopOnce.Do(func() { close(l.stopCh) })
}

// getOrCreate returns (or lazily creates) the entry for key.
func (l *Limiter) getOrCreate(key string) *entry {
	l.mu.Lock()
	e, ok := l.entries[key]
	if !ok {
		e = &entry{}
		l.entries[key] = e
	}
	l.mu.Unlock()
	return e
}

// sweep periodically removes keys with no recent activity to bound memory use.
func (l *Limiter) sweep() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.purgeIdle()
		}
	}
}

func (l *Limiter) purgeIdle() {
	cutoff := time.Now().Add(-l.windowSize)

	l.mu.Lock()
	defer l.mu.Unlock()

	for key, e := range l.entries {
		e.mu.Lock()
		active := false
		for _, t := range e.timestamps {
			if t.After(cutoff) {
				active = true
				break
			}
		}
		e.mu.Unlock()
		if !active {
			delete(l.entries, key)
		}
	}
}
