// Package proxy implements a bidirectional LLM API proxy that can accept
// requests in OpenAI or Anthropic format and forward them to upstream
// providers in either format.
package proxy

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/apikeydclient"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/llm/stats"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/ratelimit"
)

type proxyCtxKey int

const ctxUserID proxyCtxKey = iota

// GatewayRateLimitConfig controls per-user request velocity at the proxy.
type GatewayRateLimitConfig struct {
	Enabled           bool `yaml:"enabled"`
	RequestsPerMinute int  `yaml:"requests_per_minute"`
}

// GatewayConfig holds optional gateway-level features for llm-proxy.
type GatewayConfig struct {
	// FallbackChain is an ordered list of upstream names to try when the
	// primary upstream returns a retryable error.
	FallbackChain []string               `yaml:"fallback_chain"`
	RateLimit     GatewayRateLimitConfig `yaml:"rate_limit"`
}

// Config is the top-level proxy configuration.
type Config struct {
	Listen     string           `yaml:"listen"`      // e.g. ":8080"
	Upstreams  []UpstreamConfig `yaml:"upstreams"`
	Routes     []RouteConfig    `yaml:"routes"`
	APIKeydURL string           `yaml:"apikeyd_url"` // if set, inbound requests require a valid API key
	Gateway    GatewayConfig    `yaml:"gateway"`
}

// Server is an HTTP server that proxies LLM API requests.
type Server struct {
	cfg       Config
	router    *Router
	keyClient *apikeydclient.Client // nil = no auth required
	statsReg  *stats.Registry       // always non-nil
	limiter   *ratelimit.Limiter    // nil when rate limiting is disabled
	hooks     Hooks
}

// NewServer creates a proxy Server from the given Config.
func NewServer(cfg Config) (*Server, error) {
	router, err := NewRouter(cfg.Upstreams, cfg.Routes)
	if err != nil {
		return nil, fmt.Errorf("proxy: %w", err)
	}
	s := &Server{
		cfg:      cfg,
		router:   router,
		statsReg: stats.NewRegistry(),
	}
	if cfg.APIKeydURL != "" {
		s.keyClient = apikeydclient.New(cfg.APIKeydURL)
	}
	if cfg.Gateway.RateLimit.Enabled && cfg.Gateway.RateLimit.RequestsPerMinute > 0 {
		s.limiter = ratelimit.New(time.Minute, cfg.Gateway.RateLimit.RequestsPerMinute)
		logging.Infof("proxy: rate limiting enabled (%d req/min per user)", cfg.Gateway.RateLimit.RequestsPerMinute)
	}
	return s, nil
}

// requireAPIKey is middleware that validates an inbound API key via apikeyd.
// If no keyClient is configured the request passes through without auth.
// On success it attaches the caller's user_id to the request context.
func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.keyClient == nil {
			next.ServeHTTP(w, r)
			return
		}
		rawKey := extractInboundKey(r)
		if rawKey == "" {
			writeOpenAIError(w, http.StatusUnauthorized, "missing API key")
			return
		}
		info, err := s.keyClient.VerifyKey(r.Context(), rawKey)
		if err != nil {
			writeOpenAIError(w, http.StatusUnauthorized, "invalid API key: "+err.Error())
			return
		}
		// Attach user_id for downstream rate limiting and attribution.
		ctx := context.WithValue(r.Context(), ctxUserID, info.UserID)

		// Per-user rate limiting.
		if s.limiter != nil && info.UserID != "" {
			if ok, reason := s.limiter.Allow(info.UserID); !ok {
				writeOpenAIError(w, http.StatusTooManyRequests, "rate limit exceeded: "+reason)
				return
			}
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// userIDFromContext returns the authenticated user ID attached by requireAPIKey.
func userIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxUserID).(string)
	return v
}

// extractInboundKey returns the raw key from Authorization: Bearer or x-api-key.
func extractInboundKey(r *http.Request) string {
	if v := r.Header.Get("x-api-key"); v != "" {
		return v
	}
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
		return strings.TrimPrefix(v, "Bearer ")
	}
	return ""
}

// Handler returns an http.Handler for the proxy server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	auth := s.requireAPIKey
	// OpenAI-compatible endpoint
	mux.Handle("/v1/chat/completions", auth(http.HandlerFunc(s.handleOpenAI)))
	// Anthropic-compatible endpoint
	mux.Handle("/v1/messages", auth(http.HandlerFunc(s.handleAnthropic)))
	// Gemini-compatible endpoints — model name is in the URL path
	mux.Handle("/v1beta/models/", auth(http.HandlerFunc(s.handleGemini)))
	// Models listing (best-effort: return models from all upstreams)
	mux.Handle("/v1/models", auth(http.HandlerFunc(s.handleModels)))
	// Metrics — auth-exempt so Prometheus scrapers do not need an API key.
	mux.HandleFunc("/metrics", s.handleMetrics)
	return corsMiddleware(requestIDMiddleware(stripSDKHeaders(mux)))
}

// resolveAndStream resolves the upstream chain for model and calls fn with each
// upstream in order. On a retryable error it advances to the next upstream.
// Returns the first successful result.
type streamFn func(up *Upstream) (<-chan llm.StreamChunk, *llm.UpstreamHeaders, error)

func (s *Server) resolveAndStream(model string, fn streamFn) (<-chan llm.StreamChunk, *llm.UpstreamHeaders, error) {
	chain := s.router.ResolveChain(model, s.cfg.Gateway.FallbackChain)
	var lastErr error
	for _, up := range chain {
		start := time.Now()
		ch, headers, err := fn(up)
		if err == nil {
			if s.statsReg != nil {
				ch = wrapStream(ch, start, up.Name(), s.statsReg)
			}
			return ch, headers, nil
		}
		elapsed := time.Since(start).Milliseconds()
		if s.statsReg != nil {
			s.statsReg.Get(up.Name()).Record(elapsed, true)
		}
		if llm.IsRetryable(err) {
			logging.Warnf("proxy: upstream %q retryable error, trying next: %v", up.Name(), err)
			lastErr = err
			continue
		}
		return nil, nil, err
	}
	if lastErr != nil {
		return nil, nil, fmt.Errorf("all upstreams failed: %w", lastErr)
	}
	return nil, nil, fmt.Errorf("no upstreams for model %q", model)
}

// wrapStream passes all chunks through and records stats when the stream closes.
func wrapStream(src <-chan llm.StreamChunk, start time.Time, provider string, reg *stats.Registry) <-chan llm.StreamChunk {
	out := make(chan llm.StreamChunk, cap(src)+1)
	go func() {
		defer close(out)
		hasErr := false
		for chunk := range src {
			if chunk.Error != nil {
				hasErr = true
			}
			out <- chunk
		}
		reg.Get(provider).Record(time.Since(start).Milliseconds(), hasErr)
	}()
	return out
}

// handleMetrics serves provider health statistics in JSON or Prometheus text format.
// This endpoint is auth-exempt.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	accept := r.Header.Get("Accept")
	wantPrometheus := strings.Contains(accept, "application/openmetrics-text") ||
		strings.Contains(accept, "text/plain")

	snap := s.statsReg.Snapshot()

	type providerSnap struct {
		RequestCount int     `json:"request_count"`
		ErrorRate    float64 `json:"error_rate"`
		P95LatencyMs int64   `json:"p95_latency_ms"`
	}
	providers := make(map[string]providerSnap, len(snap))
	for name, ps := range snap {
		providers[name] = providerSnap{
			RequestCount: ps.RequestCount,
			ErrorRate:    ps.ErrorRate,
			P95LatencyMs: ps.P95LatencyMs,
		}
	}

	type rateLimitSnap struct {
		Enabled bool `json:"enabled"`
	}
	var rl *rateLimitSnap
	if s.limiter != nil {
		rl = &rateLimitSnap{Enabled: true}
	}

	if wantPrometheus {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		b := &strings.Builder{}
		b.WriteString("# HELP stoa_proxy_requests_total Total LLM requests per upstream\n")
		b.WriteString("# TYPE stoa_proxy_requests_total counter\n")
		for name, ps := range providers {
			fmt.Fprintf(b, "stoa_proxy_requests_total{upstream=%q} %d\n", name, ps.RequestCount)
		}
		b.WriteString("# HELP stoa_proxy_error_rate Error rate (0-1) per upstream\n")
		b.WriteString("# TYPE stoa_proxy_error_rate gauge\n")
		for name, ps := range providers {
			fmt.Fprintf(b, "stoa_proxy_error_rate{upstream=%q} %g\n", name, ps.ErrorRate)
		}
		b.WriteString("# HELP stoa_proxy_p95_latency_ms P95 latency in ms per upstream\n")
		b.WriteString("# TYPE stoa_proxy_p95_latency_ms gauge\n")
		for name, ps := range providers {
			fmt.Fprintf(b, "stoa_proxy_p95_latency_ms{upstream=%q} %d\n", name, ps.P95LatencyMs)
		}
		_, _ = io.WriteString(w, b.String())
		return
	}

	type jsonResponse struct {
		Upstreams map[string]providerSnap `json:"upstreams"`
		RateLimit *rateLimitSnap          `json:"rate_limit,omitempty"`
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jsonResponse{Upstreams: providers, RateLimit: rl})
}

// stripSDKHeaders removes SDK telemetry headers (e.g. x-stainless-*) before
// forwarding to upstream providers, since they are instrumentation-only and
// may confuse providers that validate inbound headers.
func stripSDKHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for key := range r.Header {
			if strings.HasPrefix(strings.ToLower(key), "x-stainless-") {
				r.Header.Del(key)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware adds CORS headers and handles OPTIONS preflight requests
// so browser-based clients (e.g. the official OpenAI JS SDK) can reach the proxy.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, anthropic-version, anthropic-beta")
		w.Header().Set("Access-Control-Expose-Headers", "x-request-id, x-ratelimit-limit-requests, x-ratelimit-limit-tokens, x-ratelimit-remaining-requests, x-ratelimit-remaining-tokens, x-ratelimit-reset-requests, x-ratelimit-reset-tokens, retry-after, retry-after-ms")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestIDMiddleware attaches a unique x-request-id header to every response
// so clients can use it for tracing.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := generateRequestID()
		w.Header().Set("x-request-id", id)
		r = r.WithContext(context.WithValue(r.Context(), ctxRequestID, id))
		next.ServeHTTP(w, r)
	})
}

// generateRequestID returns a short random hex string (8 bytes, 16 hex chars).
func generateRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// ─── OpenAI handler ────────────────────────────────────────────────────────

func (s *Server) handleOpenAI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req OpenAIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	messages, err := ParseOpenAIMessages(req.Messages)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "messages: "+err.Error())
		return
	}
	tools := ParseOpenAITools(req.Tools)

	if err := s.runPreRequest(r.Context(), &HookRequest{
		RequestID: requestIDFromContext(r.Context()),
		Model:     req.Model,
		Messages:  messages,
		Tools:     tools,
	}); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "pre-request hook: "+err.Error())
		return
	}

	upstream := s.router.Resolve(req.Model)
	id := makeID("chatcmpl")

	extras := &llm.Overrides{
		Seed:              req.Seed,
		User:              req.User,
		LogitBias:         req.LogitBias,
		Logprobs:          req.Logprobs,
		TopLogprobs:       req.TopLogprobs,
		ParallelToolCalls: req.ParallelToolCalls,
	}

	// n controls how many independent completions to generate (default 1).
	n := 1
	if req.N != nil && *req.N > 1 {
		n = *req.N
	}

	// includeUsage: default true; client can opt out via stream_options.
	includeUsage := true
	if req.StreamOptions != nil && req.StreamOptions.IncludeUsage != nil {
		includeUsage = *req.StreamOptions.IncludeUsage
	}

	if req.Stream {
		// Fan out n concurrent upstream streams, merging chunks tagged with their choice index.
		// We collect the header sink from choice 0 to forward rate-limit headers.
		type taggedChunk struct {
			choiceIdx int
			toolCount int
			chunk     llm.StreamChunk
		}
		var streamContent strings.Builder
		var streamToolCalls []llm.ToolUse
		var streamInputTokens, streamOutputTokens int
		merged := make(chan taggedChunk, n*16)
		var (
			headerSinkMu sync.Mutex
			headerSink   *llm.UpstreamHeaders
		)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(choiceIdx int) {
				defer wg.Done()
				ch, sink, err := s.resolveAndStream(req.Model, func(up *Upstream) (<-chan llm.StreamChunk, *llm.UpstreamHeaders, error) {
					return up.Stream(r.Context(), messages, tools, req.Model, req.MaxTokens, req.Temperature, req.TopP, req.ToolChoice, nil, nil, "", req.ResponseFormat, extras)
				})
				// Keep the sink from choice 0 for rate-limit header forwarding.
				if choiceIdx == 0 && sink != nil {
					headerSinkMu.Lock()
					headerSink = sink
					headerSinkMu.Unlock()
				}
				if err != nil {
					merged <- taggedChunk{choiceIdx: choiceIdx, chunk: llm.StreamChunk{Error: err}}
					return
				}
				toolCount := 0
				for chunk := range ch {
					merged <- taggedChunk{choiceIdx: choiceIdx, toolCount: toolCount, chunk: chunk}
					if chunk.ToolUse != nil {
						toolCount++
					}
				}
			}(i)
		}
		go func() { wg.Wait(); close(merged) }()

		// Wait briefly for the upstream response headers (populated before first
		// chunk) and forward rate-limit / retry headers to the client.
		// This must happen before w.WriteHeader / first body write.
		headerSinkMu.Lock()
		hs := headerSink
		headerSinkMu.Unlock()
		if hs != nil {
			if upstreamHeaders := hs.Wait(r.Context()); upstreamHeaders != nil {
				for _, h := range rateLimitHeaders {
					if v := upstreamHeaders.Get(h); v != "" {
						w.Header().Set(h, v)
					}
				}
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		// Send role delta for each choice.
		for i := 0; i < n; i++ {
			roleDelta := "assistant"
			roleChunk := &OpenAIStreamChunk{
				ID:      id,
				Object:  "chat.completion.chunk",
				Model:   req.Model,
				Choices: []OpenAIStreamChoice{{Index: i, Delta: OpenAIDelta{Role: roleDelta}}},
			}
			writeSSE(w, "data: ", roleChunk)
		}

		var streamErr error
		for tc := range merged {
			if tc.chunk.Error != nil {
				logging.Warnf("proxy openai stream error: %v", tc.chunk.Error)
				streamErr = tc.chunk.Error
				continue
			}
			// Accumulate choice 0 for post-response hook.
			if tc.choiceIdx == 0 {
				streamContent.WriteString(tc.chunk.Content)
				if tc.chunk.ToolUse != nil {
					streamToolCalls = append(streamToolCalls, *tc.chunk.ToolUse)
				}
				if tc.chunk.Done {
					streamInputTokens = tc.chunk.InputTokens
					streamOutputTokens += tc.chunk.OutputTokens
				}
			}
			if data := MakeOpenAIStreamChunk(tc.chunk, id, req.Model, tc.choiceIdx, tc.toolCount, includeUsage); data != nil {
				if _, err := w.Write(data); err != nil {
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		s.runPostResponse(r.Context(), &HookResponse{
			RequestID:    requestIDFromContext(r.Context()),
			Model:        req.Model,
			Content:      streamContent.String(),
			ToolCalls:    streamToolCalls,
			InputTokens:  streamInputTokens,
			OutputTokens: streamOutputTokens,
			Err:          streamErr,
		})
		return
	}

	// Non-streaming: fan out n concurrent drain calls, collect all choices.
	// TODO: forward x-ratelimit-* headers from upstream (requires surfacing HTTP response headers through streaming layer)
	type choiceResult struct {
		content      string
		toolCalls    []llm.ToolUse
		inputTokens  int
		outputTokens int
		err          error
	}
	results := make([]choiceResult, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c, tc, in, out, _, err := drainStream(r.Context(), upstream, messages, tools, req.Model, req.MaxTokens, req.Temperature, req.TopP, req.ToolChoice, nil, nil, "", req.ResponseFormat, extras)
			results[idx] = choiceResult{c, tc, in, out, err}
		}(i)
	}
	wg.Wait()

	// Use token counts from choice 0; report the first error encountered.
	var inputTokens, outputTokens int
	choices := make([]OpenAIChoice, 0, n)
	for i, r := range results {
		if r.err != nil {
			writeOpenAIError(w, http.StatusBadGateway, "upstream error: "+r.err.Error())
			return
		}
		msg := OpenAIMessage{Role: "assistant"}
		if r.content != "" {
			raw, _ := json.Marshal(r.content)
			msg.Content = raw
		}
		finishReason := "stop"
		if len(r.toolCalls) > 0 {
			finishReason = "tool_calls"
			for _, tc := range r.toolCalls {
				msg.ToolCalls = append(msg.ToolCalls, OpenAIToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: OpenAIFunctionCall{Name: tc.Name, Arguments: string(tc.Parameters)},
				})
			}
		}
		choices = append(choices, OpenAIChoice{Index: i, Message: msg, FinishReason: finishReason})
		if i == 0 {
			inputTokens, outputTokens = r.inputTokens, r.outputTokens
		}
	}
	resp := &OpenAIResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		ServiceTier: "default",
		Choices: choices,
		Usage: OpenAIUsage{
			PromptTokens:     inputTokens,
			CompletionTokens: outputTokens,
			TotalTokens:      inputTokens + outputTokens,
		},
	}
	writeJSON(w, http.StatusOK, resp)
	var nonStreamContent string
	var nonStreamToolCalls []llm.ToolUse
	if len(results) > 0 {
		nonStreamContent = results[0].content
		nonStreamToolCalls = results[0].toolCalls
	}
	s.runPostResponse(r.Context(), &HookResponse{
		RequestID:    requestIDFromContext(r.Context()),
		Model:        req.Model,
		Content:      nonStreamContent,
		ToolCalls:    nonStreamToolCalls,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	})
}

// ─── Anthropic handler ─────────────────────────────────────────────────────

func (s *Server) handleAnthropic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// The official Anthropic SDK always sends "anthropic-version: 2023-06-01".
	// As a proxy we accept any value (including absent) without error.
	// r.Header.Get("anthropic-version") is intentionally ignored here.

	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.MaxTokens <= 0 {
		writeAnthropicError(w, http.StatusBadRequest, "max_tokens is required and must be a positive integer")
		return
	}

	// Attach raw message JSON so cache_control blocks are forwarded verbatim.
	r = r.WithContext(llm.WithAnthropicRawMessages(r.Context(), RawMessagesForCacheControl(&req)))

	messages, err := ParseAnthropicMessages(req.Messages, req.System)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "messages: "+err.Error())
		return
	}
	tools := ParseAnthropicTools(req.Tools)

	if err := s.runPreRequest(r.Context(), &HookRequest{
		RequestID: requestIDFromContext(r.Context()),
		Model:     req.Model,
		Messages:  messages,
		Tools:     tools,
	}); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "pre-request hook: "+err.Error())
		return
	}

	maxTokens := req.MaxTokens
	upstream := s.router.Resolve(req.Model)
	id := makeID("msg")

	// Extract anthropic-beta header for forwarding.
	anthropicBeta := r.Header.Get("anthropic-beta")

	// Build extras from Anthropic-specific request fields.
	anthropicExtras := &llm.Overrides{}
	if req.Metadata != nil && req.Metadata.UserID != "" {
		anthropicExtras.AnthropicUserID = req.Metadata.UserID
	}

	// Extract thinking parameter.
	var thinkingEnabled *bool
	var thinkingBudget *int
	if len(req.Thinking) > 0 {
		var th struct {
			Type         string `json:"type"`
			BudgetTokens int    `json:"budget_tokens"`
		}
		if json.Unmarshal(req.Thinking, &th) == nil && th.Type == "enabled" {
			enabled := true
			thinkingEnabled = &enabled
			thinkingBudget = &th.BudgetTokens
		}
	}

	if req.Stream {
		ch, headerSink, err := s.resolveAndStream(req.Model, func(up *Upstream) (<-chan llm.StreamChunk, *llm.UpstreamHeaders, error) {
			return up.Stream(r.Context(), messages, tools, req.Model, &maxTokens, req.Temperature, req.TopP, req.ToolChoice, thinkingEnabled, thinkingBudget, anthropicBeta, nil, anthropicExtras)
		})
		if err != nil {
			writeAnthropicError(w, http.StatusBadGateway, "upstream error: "+err.Error())
			return
		}

		// Wait briefly for the upstream response headers (populated before first
		// chunk) and forward rate-limit / retry headers to the client.
		// This must happen before w.WriteHeader / first body write.
		if upstreamHeaders := headerSink.Wait(r.Context()); upstreamHeaders != nil {
			for _, h := range rateLimitHeaders {
				if v := upstreamHeaders.Get(h); v != "" {
					w.Header().Set(h, v)
				}
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		// Send start events
		for _, ev := range MakeAnthropicStartEvents(id, req.Model) {
			if _, err := w.Write(ev); err != nil {
				return
			}
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// blockIndex tracks the next block index to open.
		// openBlock is "none", "text", or "tool".
		blockIndex := 0
		openBlock := "none"
		var anthropicContent strings.Builder
		var anthropicToolCalls []llm.ToolUse
		var anthropicInputTokens, anthropicOutputTokens int

		writeEvents := func(evs [][]byte) bool {
			for _, ev := range evs {
				if _, err := w.Write(ev); err != nil {
					return false
				}
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return true
		}

		closeBlock := func() bool {
			if openBlock == "none" {
				return true
			}
			ok := writeEvents([][]byte{
				anthropicSSE("content_block_stop", AnthropicStreamEvent{
					Type:  "content_block_stop",
					Index: &[]int{blockIndex - 1}[0],
				}),
			})
			openBlock = "none"
			return ok
		}

		var anthropicStreamErr error
		for chunk := range ch {
			if chunk.Error != nil {
				logging.Warnf("proxy anthropic stream error: %v", chunk.Error)
				anthropicStreamErr = chunk.Error
				break
			}

			if chunk.Done {
				anthropicInputTokens = chunk.InputTokens
				anthropicOutputTokens = chunk.OutputTokens
				if !closeBlock() {
					return
				}
				stopReason := "end_turn"
				if !writeEvents([][]byte{
					anthropicSSE("message_delta", AnthropicStreamEvent{
						Type:  "message_delta",
						Delta: &AnthropicStreamDelta{Type: "message_delta", StopReason: stopReason},
						Usage: &AnthropicUsage{InputTokens: chunk.InputTokens, OutputTokens: chunk.OutputTokens},
					}),
					anthropicSSE("message_stop", AnthropicStreamEvent{Type: "message_stop"}),
				}) {
					return
				}
				continue
			}

			if chunk.Thinking != "" {
				if openBlock != "thinking" {
					if !closeBlock() {
						return
					}
					idx := blockIndex
					blockIndex++
					if !writeEvents([][]byte{
						anthropicSSE("content_block_start", AnthropicStreamEvent{
							Type:         "content_block_start",
							Index:        &idx,
							ContentBlock: &AnthropicStreamBlock{Type: "thinking"},
						}),
					}) {
						return
					}
					openBlock = "thinking"
				}
				idx := blockIndex - 1
				if !writeEvents([][]byte{
					anthropicSSE("content_block_delta", AnthropicStreamEvent{
						Type:  "content_block_delta",
						Index: &idx,
						Delta: &AnthropicStreamDelta{Type: "thinking_delta", Thinking: chunk.Thinking},
					}),
				}) {
					return
				}
			}

			if chunk.Content != "" {
				anthropicContent.WriteString(chunk.Content)
				if openBlock != "text" {
					if !closeBlock() {
						return
					}
					idx := blockIndex
					blockIndex++
					if !writeEvents([][]byte{
						anthropicSSE("content_block_start", AnthropicStreamEvent{
							Type:         "content_block_start",
							Index:        &idx,
							ContentBlock: &AnthropicStreamBlock{Type: "text", Text: ""},
						}),
					}) {
						return
					}
					openBlock = "text"
				}
				idx := blockIndex - 1
				if !writeEvents([][]byte{
					anthropicSSE("content_block_delta", AnthropicStreamEvent{
						Type:  "content_block_delta",
						Index: &idx,
						Delta: &AnthropicStreamDelta{Type: "text_delta", Text: chunk.Content},
					}),
				}) {
					return
				}
			}

			if chunk.ToolUse != nil {
				anthropicToolCalls = append(anthropicToolCalls, *chunk.ToolUse)
				if !closeBlock() {
					return
				}
				idx := blockIndex
				blockIndex++
				if !writeEvents([][]byte{
					anthropicSSE("content_block_start", AnthropicStreamEvent{
						Type:  "content_block_start",
						Index: &idx,
						ContentBlock: &AnthropicStreamBlock{
							Type: "tool_use",
							ID:   chunk.ToolUse.ID,
							Name: chunk.ToolUse.Name,
						},
					}),
					anthropicSSE("content_block_delta", AnthropicStreamEvent{
						Type:  "content_block_delta",
						Index: &idx,
						Delta: &AnthropicStreamDelta{Type: "input_json_delta", PartialJSON: string(chunk.ToolUse.Parameters)},
					}),
					anthropicSSE("content_block_stop", AnthropicStreamEvent{
						Type:  "content_block_stop",
						Index: &idx,
					}),
				}) {
					return
				}
				// tool block is self-contained; openBlock stays "none"
			}
		}
		s.runPostResponse(r.Context(), &HookResponse{
			RequestID:    requestIDFromContext(r.Context()),
			Model:        req.Model,
			Content:      anthropicContent.String(),
			ToolCalls:    anthropicToolCalls,
			InputTokens:  anthropicInputTokens,
			OutputTokens: anthropicOutputTokens,
			Err:          anthropicStreamErr,
		})
		return
	}

	// Non-streaming
	content, toolCalls, inputTokens, outputTokens, stopSequence, err := drainStream(r.Context(), upstream, messages, tools, req.Model, &maxTokens, req.Temperature, req.TopP, req.ToolChoice, thinkingEnabled, thinkingBudget, anthropicBeta, nil, anthropicExtras)
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		s.runPostResponse(r.Context(), &HookResponse{
			RequestID: requestIDFromContext(r.Context()),
			Model:     req.Model,
			Err:       err,
		})
		return
	}

	resp := MakeAnthropicResponse(content, toolCalls, id, req.Model, inputTokens, outputTokens, stopSequence)
	writeJSON(w, http.StatusOK, resp)
	s.runPostResponse(r.Context(), &HookResponse{
		RequestID:    requestIDFromContext(r.Context()),
		Model:        req.Model,
		Content:      content,
		ToolCalls:    toolCalls,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	})
}

// ─── Gemini handler ────────────────────────────────────────────────────────
//
// Handles: POST /v1beta/models/{model}:generateContent
//          POST /v1beta/models/{model}:streamGenerateContent
//
// The model name is extracted from the URL path; streaming is determined by
// either the action suffix (:streamGenerateContent) or ?alt=sse query param.

func (s *Server) handleGemini(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract model and action from path: /v1beta/models/{model}:{action}
	path := r.URL.Path // e.g. /v1beta/models/gemini-2.0-flash:generateContent
	model, action := parseGeminiPath(path)
	if model == "" {
		writeGeminiError(w, http.StatusBadRequest, "cannot parse model from path: "+path)
		return
	}

	streaming := action == "streamGenerateContent" || r.URL.Query().Get("alt") == "sse"

	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req GeminiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	messages, err := ParseGeminiMessages(req.Contents, req.SystemInstruction)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "messages: "+err.Error())
		return
	}
	tools := ParseGeminiTools(req.Tools)

	if err := s.runPreRequest(r.Context(), &HookRequest{
		RequestID: requestIDFromContext(r.Context()),
		Model:     model,
		Messages:  messages,
		Tools:     tools,
	}); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "pre-request hook: "+err.Error())
		return
	}

	var maxTokens *int
	var temperature, topP *float64
	if req.GenerationConfig != nil {
		maxTokens = req.GenerationConfig.MaxOutputTokens
		temperature = req.GenerationConfig.Temperature
		topP = req.GenerationConfig.TopP
	}

	// Attach Gemini-specific options (safetySettings, structured output) to the context.
	if len(req.SafetySettings) > 0 || (req.GenerationConfig != nil && (req.GenerationConfig.ResponseMimeType != "" || req.GenerationConfig.ResponseSchema != nil)) {
		opts := llm.GeminiRequestOptions{}
		if len(req.SafetySettings) > 0 {
			opts.SafetySettings = make([]llm.GeminiSafetySetting, len(req.SafetySettings))
			for i, s := range req.SafetySettings {
				opts.SafetySettings[i] = llm.GeminiSafetySetting{Category: s.Category, Threshold: s.Threshold}
			}
		}
		if req.GenerationConfig != nil {
			opts.ResponseMimeType = req.GenerationConfig.ResponseMimeType
			opts.ResponseSchema = req.GenerationConfig.ResponseSchema
		}
		r = r.WithContext(llm.WithGeminiOptions(r.Context(), opts))
	}

	upstream := s.router.Resolve(model)

	if streaming {
		ch, headerSink, err := s.resolveAndStream(model, func(up *Upstream) (<-chan llm.StreamChunk, *llm.UpstreamHeaders, error) {
			return up.Stream(r.Context(), messages, tools, model, maxTokens, temperature, topP, nil, nil, nil, "", nil, nil)
		})
		if err != nil {
			writeGeminiError(w, http.StatusBadGateway, "upstream error: "+err.Error())
			return
		}

		// Wait briefly for the upstream response headers (populated before first
		// chunk) and forward rate-limit / retry headers to the client.
		// This must happen before w.WriteHeader / first body write.
		if upstreamHeaders := headerSink.Wait(r.Context()); upstreamHeaders != nil {
			for _, h := range rateLimitHeaders {
				if v := upstreamHeaders.Get(h); v != "" {
					w.Header().Set(h, v)
				}
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		var inputTokens, outputTokens int
		var geminiContent strings.Builder
		var geminiToolCalls []llm.ToolUse
		var geminiStreamErr error
		for chunk := range ch {
			if chunk.Error != nil {
				logging.Warnf("proxy gemini stream error: %v", chunk.Error)
				geminiStreamErr = chunk.Error
				break
			}
			if chunk.InputTokens > 0 {
				inputTokens = chunk.InputTokens
			}
			geminiContent.WriteString(chunk.Content)
			if chunk.ToolUse != nil {
				geminiToolCalls = append(geminiToolCalls, *chunk.ToolUse)
			}
			if chunk.Done {
				outputTokens = chunk.OutputTokens
			}
			if data := MakeGeminiStreamChunk(chunk, model, &inputTokens); data != nil {
				if _, err := w.Write(data); err != nil {
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		}
		s.runPostResponse(r.Context(), &HookResponse{
			RequestID:    requestIDFromContext(r.Context()),
			Model:        model,
			Content:      geminiContent.String(),
			ToolCalls:    geminiToolCalls,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			Err:          geminiStreamErr,
		})
		return
	}

	// Non-streaming
	content, toolCalls, inputTokens, outputTokens, _, err := drainStream(r.Context(), upstream, messages, tools, model, maxTokens, temperature, topP, nil, nil, nil, "", nil, nil)
	if err != nil {
		writeGeminiError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		s.runPostResponse(r.Context(), &HookResponse{
			RequestID: requestIDFromContext(r.Context()),
			Model:     model,
			Err:       err,
		})
		return
	}

	resp := MakeGeminiResponse(content, toolCalls, model, inputTokens, outputTokens)
	writeJSON(w, http.StatusOK, resp)
	s.runPostResponse(r.Context(), &HookResponse{
		RequestID:    requestIDFromContext(r.Context()),
		Model:        model,
		Content:      content,
		ToolCalls:    toolCalls,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	})
}

// parseGeminiPath extracts the model name and action from a Gemini-style path.
// e.g. "/v1beta/models/gemini-2.0-flash:generateContent" → ("gemini-2.0-flash", "generateContent")
func parseGeminiPath(path string) (model, action string) {
	// Strip prefix
	for _, prefix := range []string{"/v1beta/models/", "/v1/models/"} {
		if len(path) > len(prefix) && path[:len(prefix)] == prefix {
			rest := path[len(prefix):]
			if idx := strings.Index(rest, ":"); idx >= 0 {
				return rest[:idx], rest[idx+1:]
			}
			return rest, "generateContent"
		}
	}
	return "", ""
}

func writeGeminiError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{
		"error": map[string]interface{}{
			"code":    status,
			"message": msg,
			"status":  geminiStatus(status),
		},
	})
}

// ─── Models handler ────────────────────────────────────────────────────────

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	type modelObj struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
	}
	var models []modelObj

	for name, up := range s.router.upstreams {
		list, err := up.client.ListModels(ctx)
		if err != nil {
			logging.Warnf("proxy: list models from %q: %v", name, err)
			continue
		}
		for _, m := range list {
			models = append(models, modelObj{ID: m, Object: "model", Created: 0})
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}

// rateLimitHeaders is the set of upstream headers forwarded to clients so they
// can implement rate-limit-aware backoff.
var rateLimitHeaders = []string{
	"x-ratelimit-limit-requests",
	"x-ratelimit-limit-tokens",
	"x-ratelimit-remaining-requests",
	"x-ratelimit-remaining-tokens",
	"x-ratelimit-reset-requests",
	"x-ratelimit-reset-tokens",
	"retry-after",
	"retry-after-ms",
}

// ─── helpers ───────────────────────────────────────────────────────────────

// drainStream calls the upstream in streaming mode and collects the full response.
func drainStream(ctx context.Context, upstream *Upstream, messages []chat.Message, tools []llm.Tool, model string, maxTokens *int, temperature, topP *float64, toolChoice json.RawMessage, thinkingEnabled *bool, thinkingBudget *int, anthropicBeta string, responseFormat json.RawMessage, extras *llm.Overrides) (string, []llm.ToolUse, int, int, string, error) {
	ch, _, err := upstream.Stream(ctx, messages, tools, model, maxTokens, temperature, topP, toolChoice, thinkingEnabled, thinkingBudget, anthropicBeta, responseFormat, extras)
	if err != nil {
		return "", nil, 0, 0, "", err
	}
	var sb strings.Builder
	var thinkingSb strings.Builder
	var toolCalls []llm.ToolUse
	var inputTokens, outputTokens int
	var stopSequence string
	for chunk := range ch {
		if chunk.Error != nil {
			return "", nil, 0, 0, "", chunk.Error
		}
		sb.WriteString(chunk.Content)
		thinkingSb.WriteString(chunk.Thinking)
		if chunk.ToolUse != nil {
			toolCalls = append(toolCalls, *chunk.ToolUse)
		}
		if chunk.Done {
			inputTokens = chunk.InputTokens
			outputTokens = chunk.OutputTokens
			stopSequence = chunk.StopSequence
		}
	}
	content := sb.String()
	// If the upstream returned only thinking content (e.g. a model that wraps
	// its entire response in a reasoning block), surface the thinking as the
	// response text so the caller gets something back.
	if content == "" && thinkingSb.Len() > 0 {
		content = thinkingSb.String()
	}
	return content, toolCalls, inputTokens, outputTokens, stopSequence, nil
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeSSE(w http.ResponseWriter, prefix string, v interface{}) {
	b, _ := json.Marshal(v)
	_, _ = fmt.Fprintf(w, "%s%s\n\n", prefix, b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func openAIErrType(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusInternalServerError, http.StatusBadGateway:
		return "api_error"
	default:
		return "invalid_request_error"
	}
}

func anthropicErrType(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusServiceUnavailable:
		return "overloaded_error"
	case http.StatusInternalServerError, http.StatusBadGateway:
		return "api_error"
	default:
		return "invalid_request_error"
	}
}

func geminiStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusServiceUnavailable, http.StatusBadGateway:
		return "UNAVAILABLE"
	case http.StatusInternalServerError:
		return "INTERNAL"
	default:
		return "INVALID_ARGUMENT"
	}
}

func writeOpenAIError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{
		"error": map[string]interface{}{
			"message": msg,
			"type":    openAIErrType(status),
			"code":    nil,
			"param":   nil,
		},
	})
}

func writeAnthropicError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{
		"type":  "error",
		"error": map[string]interface{}{"type": anthropicErrType(status), "message": msg},
	})
}

func makeID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x", prefix, b)
}
