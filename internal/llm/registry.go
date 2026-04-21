package llm

import (
	"context"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
)

// ModelInfo describes a model available from a provider.
type ModelInfo struct {
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
// The "default" entry corresponds to the main LLMConfig provider.
type ProviderRegistry struct {
	mu      sync.RWMutex
	clients map[string]Client       // provider name → client
	models  map[string][]string     // provider name → model IDs (sorted)
	types   map[string]string       // provider name → "openai"/"anthropic"
	order   []string                // insertion order for stable listing
}

// NewProviderRegistry creates a registry from a Config.
// The primary LLM config is registered as "default".
// Additional providers from cfg.LLMProviders are registered by their Name.
func NewProviderRegistry(cfg *config.Config) *ProviderRegistry {
	r := &ProviderRegistry{
		clients: make(map[string]Client),
		models:  make(map[string][]string),
		types:   make(map[string]string),
	}

	if cfg == nil {
		return r
	}

	// Register the primary provider as "default".
	r.registerFromLLMConfig("default", cfg)

	// Register additional named providers.
	for _, pc := range cfg.LLMProviders {
		if pc.Name == "" {
			logging.Warnf("llm: skipping provider with empty name")
			continue
		}
		r.registerFromProviderConfig(pc, cfg)
	}

	return r
}

// registerFromLLMConfig adds the primary LLMConfig as a named entry.
func (r *ProviderRegistry) registerFromLLMConfig(name string, cfg *config.Config) {
	var client Client
	var err error

	switch cfg.LLM.Provider {
	case "anthropic":
		client, err = NewAnthropicClient(cfg)
	case "gemini":
		client, err = NewGeminiClient(cfg.LLM.APIKey, cfg.LLM.APIURL, cfg.LLM.Model, cfg.LLM.MaxTokens, nil)
	default:
		client, err = NewOpenAIClient(cfg)
	}
	if err != nil {
		logging.Warnf("llm: registry: cannot create default client: %v", err)
		return
	}

	r.clients[name] = client
	r.types[name] = cfg.LLM.Provider
	if r.types[name] == "" {
		r.types[name] = "openai"
	}
	r.order = append(r.order, name)
	// Seed with the configured model if provided.
	if cfg.LLM.Model != "" {
		r.models[name] = []string{cfg.LLM.Model}
	}
}

// resolveProviderAPIKey returns the API key for a provider config.
// Priority: explicit api_key > named env var (api_key_env) > auto-derived LLM_<NAME>_API_KEY.
func resolveProviderAPIKey(pc config.LLMProviderConfig) string {
	if pc.APIKey != "" {
		return pc.APIKey
	}
	envVar := pc.APIKeyEnv
	if envVar == "" {
		envVar = "LLM_" + strings.ToUpper(strings.ReplaceAll(pc.Name, "-", "_")) + "_API_KEY"
	}
	return os.Getenv(envVar)
}

// registerFromProviderConfig adds an LLMProviderConfig entry.
func (r *ProviderRegistry) registerFromProviderConfig(pc config.LLMProviderConfig, baseCfg *config.Config) {
	// Build a minimal config for this provider.
	derived := *baseCfg
	derived.LLM.Provider = pc.Type
	derived.LLM.APIURL = pc.APIURL
	derived.LLM.APIKey = resolveProviderAPIKey(pc)
	if len(pc.Models) > 0 {
		derived.LLM.Model = pc.Models[0] // first is the default
	}

	var client Client
	var err error
	switch pc.Type {
	case "anthropic":
		client, err = NewAnthropicClient(&derived)
	case "gemini":
		client, err = NewGeminiClient(derived.LLM.APIKey, derived.LLM.APIURL, derived.LLM.Model, derived.LLM.MaxTokens, nil)
	default:
		client, err = NewOpenAIClient(&derived)
	}
	if err != nil {
		logging.Warnf("llm: registry: cannot create client %q: %v", pc.Name, err)
		return
	}

	r.clients[pc.Name] = client
	r.types[pc.Name] = pc.Type
	r.order = append(r.order, pc.Name)
	if len(pc.Models) > 0 {
		sorted := append([]string(nil), pc.Models...)
		sort.Strings(sorted)
		r.models[pc.Name] = sorted
	}
}

// GetDefaultClient returns the client for the "default" provider.
func (r *ProviderRegistry) GetDefaultClient() Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.clients["default"]
}

// GetClient returns the client for the named provider, or false if not found.
func (r *ProviderRegistry) GetClient(name string) (Client, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.clients[name]
	return c, ok
}

// ListAllModels returns all known models across all providers.
func (r *ProviderRegistry) ListAllModels() []ModelInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []ModelInfo
	for _, name := range r.order {
		for _, id := range r.models[name] {
			result = append(result, ModelInfo{Provider: name, ID: id})
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
// Providers with a static model list (from config) are not refreshed unless forceAll is true.
func (r *ProviderRegistry) RefreshModels(ctx context.Context, names []string) {
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
			continue
		}
		sort.Strings(models)

		r.mu.Lock()
		r.models[name] = models
		r.mu.Unlock()
		logging.Infof("llm: registry: discovered %d models for provider %q", len(models), name)
	}
}

// StartPeriodicRefresh discovers models on startup, then repeats at interval.
// Pass an empty or nil providerNames to refresh all. Pass zero interval to only refresh once.
func (r *ProviderRegistry) StartPeriodicRefresh(ctx context.Context, interval time.Duration) {
	names := r.ProviderNames()
	go func() {
		r.RefreshModels(ctx, names)
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
