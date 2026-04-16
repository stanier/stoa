package stats

import "sync"

// Snapshot is a point-in-time copy of a provider's statistics, safe for serialisation.
type Snapshot struct {
	RequestCount int     `json:"request_count"`
	ErrorRate    float64 `json:"error_rate"`
	P95LatencyMs int64   `json:"p95_latency_ms"`
}

// Registry is a thread-safe map from provider name to *ProviderStats.
type Registry struct {
	mu      sync.Mutex
	entries map[string]*ProviderStats
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]*ProviderStats)}
}

// Get returns the ProviderStats for the given provider name, creating it if absent.
func (r *Registry) Get(provider string) *ProviderStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	ps, ok := r.entries[provider]
	if !ok {
		ps = NewProviderStats()
		r.entries[provider] = ps
	}
	return ps
}

// Snapshot returns a copy of all per-provider statistics, safe for serialisation.
func (r *Registry) Snapshot() map[string]Snapshot {
	r.mu.Lock()
	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	r.mu.Unlock()

	out := make(map[string]Snapshot, len(names))
	for _, name := range names {
		r.mu.Lock()
		ps := r.entries[name]
		r.mu.Unlock()
		out[name] = Snapshot{
			RequestCount: ps.RequestCount(),
			ErrorRate:    ps.ErrorRate(),
			P95LatencyMs: ps.P95LatencyMs(),
		}
	}
	return out
}
