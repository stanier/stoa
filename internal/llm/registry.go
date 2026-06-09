package llm

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
)

// RegistryModel describes a model available from a provider in the registry.
type RegistryModel struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// ProviderEntry is a named LLM client in the registry.
type ProviderEntry struct {
	Name string
	Type string // "openai" or "anthropic"
	cfg  *config.Config
}

// ProviderRegistry holds one or more named LLM clients and their discovered models.
type ProviderRegistry struct {
	mu          sync.RWMutex
	clients     map[string]Client   // provider name → client
	models      map[string][]string // provider name → model IDs (sorted)
	types       map[string]string   // provider name → "openai"/"anthropic"
	order       []string            // insertion order for stable listing
	defaultName string              // name of the default provider
}

// NewProviderRegistry creates a registry from a Config.
// All providers are registered by their Name. The provider designated by
// cfg.LLM.Default (or the first provider when Default is empty) is also
// accessible via GetDefaultClient() and via GetClient("default").
func NewProviderRegistry(cfg *config.Config) *ProviderRegistry {
	r := &ProviderRegistry{
		clients: make(map[string]Client),
		models:  make(map[string][]string),
		types:   make(map[string]string),
	}

	if cfg == nil {
		return r
	}

	dp := cfg.LLM.DefaultProvider()
	if dp != nil {
		r.defaultName = dp.Name
	}

	for i := range cfg.LLM.Providers {
		pc := &cfg.LLM.Providers[i]
		if pc.Name == "" {
			logging.Warnf("llm: skipping provider with empty name")
			continue
		}
		r.registerProvider(pc)
	}

	return r
}

// registerProvider adds one LLMProviderConfig entry to the registry.
func (r *ProviderRegistry) registerProvider(pc *config.LLMProviderConfig) {
	var client Client
	var err error

	switch pc.Type {
	case "anthropic":
		client, err = NewAnthropicClient(pc)
	case "gemini":
		client, err = NewGeminiClient(pc.APIKey, pc.APIURL, pc.Model, pc.MaxTokens, nil)
	default:
		client, err = NewOpenAIClient(pc)
	}
	if err != nil {
		logging.Warnf("llm: registry: cannot create client %q: %v", pc.Name, err)
		return
	}

	r.clients[pc.Name] = client
	r.types[pc.Name] = pc.Type
	r.order = append(r.order, pc.Name)

	// Seed model list: static list takes priority, otherwise seed with the active model.
	if len(pc.Models) > 0 {
		sorted := append([]string(nil), pc.Models...)
		sort.Strings(sorted)
		r.models[pc.Name] = sorted
	} else if pc.Model != "" {
		r.models[pc.Name] = []string{pc.Model}
	}
}

// GetDefaultClient returns the client for the configured default provider.
func (r *ProviderRegistry) GetDefaultClient() Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.clients[r.defaultName]
}

// GetClient returns the client for the named provider.
// The special name "default" resolves to the configured default provider.
func (r *ProviderRegistry) GetClient(name string) (Client, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if name == "default" {
		c := r.clients[r.defaultName]
		return c, c != nil
	}
	c, ok := r.clients[name]
	return c, ok
}

// ListAllModels returns all known models across all providers.
func (r *ProviderRegistry) ListAllModels() []RegistryModel {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []RegistryModel
	for _, name := range r.order {
		for _, id := range r.models[name] {
			result = append(result, RegistryModel{Provider: name, ID: id})
		}
	}
	return result
}

// ProviderNames returns the names of all registered providers in insertion order.
func (r *ProviderRegistry) ProviderNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.order...)
}

// RefreshModels queries each provider for its current model list and updates the cache.
// Returns true if every provider was refreshed without error.
func (r *ProviderRegistry) RefreshModels(ctx context.Context, names []string) bool {
	allOK := true
	for _, name := range names {
		r.mu.RLock()
		client, ok := r.clients[name]
		r.mu.RUnlock()
		if !ok {
			continue
		}

		models, err := client.ListModels(ctx)
		if err != nil {
			logging.Warnf("llm: registry: list models for %q: %v", name, err)
			allOK = false
			continue
		}
		sort.Strings(models)

		r.mu.Lock()
		r.models[name] = models
		r.mu.Unlock()
		logging.Infof("llm: registry: discovered %d models for provider %q", len(models), name)
	}
	return allOK
}

// StartPeriodicRefresh discovers models on startup, then repeats at interval.
// If the initial refresh fails for any provider (e.g. because the upstream isn't
// ready yet), it retries with short delays before settling into the long interval.
// Pass zero interval to only refresh once (with startup retries).
func (r *ProviderRegistry) StartPeriodicRefresh(ctx context.Context, interval time.Duration) {
	names := r.ProviderNames()
	go func() {
		// Startup retries: try immediately, then back off if any provider failed.
		// This recovers quickly when the inference-gateway or an upstream starts
		// after agentd (common in compose setups).
		startupRetries := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute}
		if ok := r.RefreshModels(ctx, names); !ok {
			for _, wait := range startupRetries {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
				if r.RefreshModels(ctx, names) {
					break
				}
			}
		}

		if interval <= 0 {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.RefreshModels(ctx, names)
			}
		}
	}()
}
