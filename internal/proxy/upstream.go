package proxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
)

// UpstreamConfig configures one upstream LLM provider.
type UpstreamConfig struct {
	Name             string `yaml:"name"`
	Dialect          string `yaml:"dialect"`            // "openai", "anthropic", or "gemini"
	APIURL           string `yaml:"api_url"`
	APIKey           string `yaml:"api_key"`            // literal key value
	APIKeyEnv        string `yaml:"api_key_env"`        // env var to read the key from
	APIKeyFile       string `yaml:"api_key_file"`       // file path containing the key (e.g. a mounted secret)
	DefaultModel     string `yaml:"default_model"`      // used when request model is empty
	DefaultMaxTokens int    `yaml:"default_max_tokens"` // used when request max_tokens is absent
	TLSSkipVerify    bool   `yaml:"tls_skip_verify"`    // skip TLS certificate verification (for self-signed certs)
	StaticModelsOnly bool   `yaml:"static_models_only"` // when true, skip ListModels — only explicitly configured routes are exposed
}

// buildHTTPClient returns an *http.Client appropriate for this upstream.
// When TLSSkipVerify is false the caller should use http.DefaultClient.
func (c UpstreamConfig) buildHTTPClient() *http.Client {
	if !c.TLSSkipVerify {
		return nil // callers treat nil as "use SDK default"
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
}

// resolveAPIKey returns the API key for an upstream config.
// Priority: literal api_key > api_key_file contents > api_key_env value.
func (c UpstreamConfig) resolveAPIKey() string {
	if c.APIKey != "" {
		return c.APIKey
	}
	if c.APIKeyFile != "" {
		if data, err := os.ReadFile(c.APIKeyFile); err == nil {
			return strings.TrimRight(string(data), "\r\n")
		}
	}
	if c.APIKeyEnv != "" {
		return os.Getenv(c.APIKeyEnv)
	}
	return ""
}

// RouteConfig maps a model name (or glob) to an upstream name.
type RouteConfig struct {
	Model    string `yaml:"model"`    // exact match or "*" wildcard
	Upstream string `yaml:"upstream"` // upstream name
}

// Upstream wraps an llm.Client with its config.
type Upstream struct {
	cfg    UpstreamConfig
	client llm.Client
}

// NewUpstream creates an Upstream from config by instantiating the appropriate SDK client.
func NewUpstream(cfg UpstreamConfig) (*Upstream, error) {
	maxTokens := cfg.DefaultMaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	var client llm.Client
	var err error
	apiKey := cfg.resolveAPIKey()

	httpClient := cfg.buildHTTPClient()
	switch cfg.Dialect {
	case "openai":
		client, err = llm.NewOpenAIClientSimple(apiKey, cfg.APIURL, cfg.DefaultModel, maxTokens, httpClient)
	case "anthropic":
		client, err = llm.NewAnthropicClientSimple(apiKey, cfg.APIURL, cfg.DefaultModel, maxTokens, httpClient)
	case "gemini":
		client, err = llm.NewGeminiClient(apiKey, cfg.APIURL, cfg.DefaultModel, maxTokens, httpClient)
	default:
		return nil, fmt.Errorf("unknown dialect %q (want \"openai\", \"anthropic\", or \"gemini\")", cfg.Dialect)
	}
	if err != nil {
		return nil, fmt.Errorf("upstream %q: %w", cfg.Name, err)
	}
	return &Upstream{cfg: cfg, client: client}, nil
}

// Name returns the upstream's configured name.
func (u *Upstream) Name() string { return u.cfg.Name }

// Stream calls the upstream and returns a StreamChunk channel plus an
// UpstreamHeaders sink. The sink is populated (asynchronously, before the
// first chunk) with the HTTP response headers from the upstream — callers can
// use sink.Wait(ctx) to retrieve rate-limit and retry headers before writing
// their own response body.
func (u *Upstream) Stream(ctx context.Context, messages []chat.Message, tools []llm.Tool, model string, maxTokens *int, temperature, topP *float64, toolChoice json.RawMessage, thinkingEnabled *bool, thinkingBudget *int, anthropicBeta string, responseFormat json.RawMessage, extras *llm.Overrides) (<-chan llm.StreamChunk, *llm.UpstreamHeaders, error) {
	// Resolve model: use upstream default when not specified by the request.
	if model == "" {
		model = u.cfg.DefaultModel
	}

	// Build overrides from per-request parameters so they override the upstream's defaults.
	mt := u.cfg.DefaultMaxTokens
	if mt == 0 {
		mt = 4096
	}
	if maxTokens != nil {
		mt = *maxTokens
	}
	ov := &llm.Overrides{
		Model:                &model,
		MaxTokens:            &mt,
		Temperature:          temperature,
		TopP:                 topP,
		ToolChoice:           toolChoice,
		EnableThinking:       thinkingEnabled,
		ThinkingBudgetTokens: thinkingBudget,
		AnthropicBeta:        anthropicBeta,
		ResponseFormat:       responseFormat,
	}
	if extras != nil {
		if extras.AnthropicUserID != "" {
			ov.AnthropicUserID = extras.AnthropicUserID
		}
		if extras.Seed != nil {
			ov.Seed = extras.Seed
		}
		if extras.User != "" {
			ov.User = extras.User
		}
		if extras.LogitBias != nil {
			ov.LogitBias = extras.LogitBias
		}
		if extras.Logprobs != nil {
			ov.Logprobs = extras.Logprobs
		}
		if extras.TopLogprobs != nil {
			ov.TopLogprobs = extras.TopLogprobs
		}
		if extras.ParallelToolCalls != nil {
			ov.ParallelToolCalls = extras.ParallelToolCalls
		}
	}
	ctx = llm.WithOverrides(ctx, ov)

	// Attach a header sink so the LLM client can populate upstream response headers.
	headerSink := llm.NewUpstreamHeaders()
	ctx = llm.WithUpstreamHeaders(ctx, headerSink)

	ch, err := u.client.StreamMessage(ctx, messages, tools)
	if err != nil {
		return nil, nil, err
	}
	return ch, headerSink, nil
}

// ─── Router ────────────────────────────────────────────────────────────────

// Router maps inbound model names to upstreams.
type Router struct {
	upstreams  map[string]*Upstream
	routes     []RouteConfig
	fallback   *Upstream // first upstream registered
	mu         sync.RWMutex
	modelIndex map[string]string // model ID → upstream name, populated by ListModels
}

// NewRouter creates a Router from the upstream and route configs.
func NewRouter(upstreamCfgs []UpstreamConfig, routes []RouteConfig) (*Router, error) {
	r := &Router{
		upstreams:  make(map[string]*Upstream),
		routes:     routes,
		modelIndex: make(map[string]string),
	}
	for _, uc := range upstreamCfgs {
		up, err := NewUpstream(uc)
		if err != nil {
			return nil, err
		}
		r.upstreams[uc.Name] = up
		if r.fallback == nil {
			r.fallback = up
		}
	}
	if r.fallback == nil {
		return nil, fmt.Errorf("no upstreams configured")
	}
	return r, nil
}

// Resolve returns the upstream for the given model name.
// Checks explicit routes first, then the dynamic model index built from
// ListModels responses, then falls back to the first upstream.
func (r *Router) Resolve(model string) *Upstream {
	for _, route := range r.routes {
		if route.Model == model || route.Model == "*" {
			if up, ok := r.upstreams[route.Upstream]; ok {
				return up
			}
		}
	}
	r.mu.RLock()
	upstreamName := r.modelIndex[model]
	r.mu.RUnlock()
	if upstreamName != "" {
		if up, ok := r.upstreams[upstreamName]; ok {
			return up
		}
	}
	return r.fallback
}

// IndexModels records model → upstreamName mappings discovered via ListModels.
// Existing explicit route entries are not overwritten at resolution time.
func (r *Router) IndexModels(upstreamName string, models []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range models {
		r.modelIndex[m] = upstreamName
	}
}

// ResolveChain returns the primary upstream for model followed by any additional
// upstreams named in fallbackChain (in order, deduped). When fallbackChain is
// empty only the primary upstream is returned.
func (r *Router) ResolveChain(model string, fallbackChain []string) []*Upstream {
	primary := r.Resolve(model)
	if len(fallbackChain) == 0 {
		return []*Upstream{primary}
	}
	seen := map[string]bool{primary.cfg.Name: true}
	chain := []*Upstream{primary}
	for _, name := range fallbackChain {
		if seen[name] {
			continue
		}
		if up, ok := r.upstreams[name]; ok {
			chain = append(chain, up)
			seen[name] = true
		}
	}
	return chain
}
