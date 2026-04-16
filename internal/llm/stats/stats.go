package stats

import (
	"sort"
	"sync"
)

const ringSize = 1000

// Sample holds a single recorded request observation.
type Sample struct {
	DurationMs int64
	IsError    bool
}

// ProviderStats tracks rolling statistics for a single provider using a
// fixed-size ring buffer (last 1000 samples).
type ProviderStats struct {
	mu      sync.Mutex
	samples [ringSize]Sample
	head    int // next write position
	count   int // number of filled slots (capped at ringSize)
}

// NewProviderStats returns a new, empty ProviderStats.
func NewProviderStats() *ProviderStats {
	return &ProviderStats{}
}

// Record adds a new sample to the ring buffer.
func (s *ProviderStats) Record(durationMs int64, isError bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples[s.head] = Sample{DurationMs: durationMs, IsError: isError}
	s.head = (s.head + 1) % ringSize
	if s.count < ringSize {
		s.count++
	}
}

// ErrorRate returns the fraction of filled slots that are errors.
// Returns 0.0 if no samples have been recorded.
func (s *ProviderStats) ErrorRate() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count == 0 {
		return 0.0
	}
	var errs int
	for i := 0; i < s.count; i++ {
		if s.samples[i].IsError {
			errs++
		}
	}
	return float64(errs) / float64(s.count)
}

// P95LatencyMs returns the 95th-percentile latency across all filled slots.
// Returns 0 if no samples have been recorded.
func (s *ProviderStats) P95LatencyMs() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count == 0 {
		return 0
	}
	// Copy filled samples.
	filled := make([]int64, s.count)
	for i := 0; i < s.count; i++ {
		filled[i] = s.samples[i].DurationMs
	}
	sort.Slice(filled, func(i, j int) bool { return filled[i] < filled[j] })
	// Index at 95th percentile (0-based).
	idx := int(float64(s.count-1) * 0.95)
	return filled[idx]
}

// RequestCount returns the total number of recorded samples (capped at ring size).
func (s *ProviderStats) RequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}
