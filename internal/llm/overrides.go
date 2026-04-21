package llm

import (
	"context"
	"encoding/json"
)

type contextKey int

const overridesKey contextKey = iota

// Overrides holds per-session LLM parameter overrides.
// A nil pointer means "use the global config value".
type Overrides struct {
	// APIKey overrides the API key used for this specific call.
	// Set by agentd when routing through llm-proxy with a per-user llmp_* key.
	APIKey *string `json:"api_key,omitempty"`
	Temperature          *float64 `json:"temperature,omitempty"`
	TopP                 *float64 `json:"top_p,omitempty"`
	TopK                 *int     `json:"top_k,omitempty"`
	MaxTokens            *int     `json:"max_tokens,omitempty"`
	EnableThinking       *bool    `json:"enable_thinking,omitempty"`
	ThinkingBudgetTokens *int     `json:"thinking_budget_tokens,omitempty"`
	// Provider selects a named provider from the registry (e.g. "anthropic-opus").
	// Empty string means use the default server provider.
	Provider *string `json:"provider,omitempty"`
	// Model overrides the model ID used for this session.
	// When Provider is also set, the model must be available on that provider.
	Model *string `json:"model,omitempty"`
	// ToolChoice carries the raw JSON tool_choice value from the client request.
	// OpenAI: "none"|"auto"|"required"|{"type":"function","function":{"name":"..."}}
	// Anthropic: {"type":"auto"}|{"type":"any"}|{"type":"tool","name":"..."}
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
	// ResponseFormat is the raw JSON response_format value from an OpenAI-dialect request.
	// e.g. {"type":"json_object"} or {"type":"json_schema","json_schema":{...}}
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
	// AnthropicBeta is the value of the anthropic-beta header from the inbound request.
	// Forwarded verbatim to the upstream Anthropic API.
	AnthropicBeta string `json:"anthropic_beta,omitempty"`
	// AnthropicUserID is forwarded to the Anthropic API as metadata.user_id.
	AnthropicUserID string `json:"anthropic_user_id,omitempty"`
	// OpenAI pass-through fields (P3/P4).
	Seed              *int64           `json:"seed,omitempty"`
	User              string           `json:"user,omitempty"`
	LogitBias         map[string]int64 `json:"logit_bias,omitempty"`
	Logprobs          *bool            `json:"logprobs,omitempty"`
	TopLogprobs       *int64           `json:"top_logprobs,omitempty"`
	ParallelToolCalls *bool            `json:"parallel_tool_calls,omitempty"`
}

// WithOverrides attaches per-call overrides to the context.
func WithOverrides(ctx context.Context, o *Overrides) context.Context {
	return context.WithValue(ctx, overridesKey, o)
}

// OverridesFromContext retrieves overrides from the context, or nil if absent.
func OverridesFromContext(ctx context.Context) *Overrides {
	v, _ := ctx.Value(overridesKey).(*Overrides)
	return v
}
