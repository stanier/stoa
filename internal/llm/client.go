package llm

import (
	"context"
	"encoding/json"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/presidio"
)

// Client is the interface for LLM providers
type Client interface {
	// SendMessage sends a message and returns the complete response
	// tools parameter is optional - pass nil or empty slice if not using tools
	SendMessage(ctx context.Context, messages []chat.Message, tools []Tool) (string, error)

	// StreamMessage sends a message and returns a channel for streaming responses
	// tools parameter is optional - pass nil or empty slice if not using tools
	StreamMessage(ctx context.Context, messages []chat.Message, tools []Tool) (<-chan StreamChunk, error)

	// ListModels returns the models available from this provider.
	// May return a static list or call the provider API.
	ListModels(ctx context.Context) ([]string, error)
}

// Tool represents an LLM tool definition
type Tool struct {
	Name         string
	Description  string
	InputSchema  interface{} // JSON Schema for tool parameters
	CacheControl string      // if "ephemeral", mark this tool for prompt caching (Anthropic only)
}

// ToolUse represents a tool invocation request from the LLM
type ToolUse struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters"`
}

// StreamChunk represents a chunk of streaming response
type StreamChunk struct {
	Content      string   // Main content text
	Thinking     string   // Thinking/reasoning text (for models that support it)
	ToolUse      *ToolUse // Tool invocation request (if model wants to use a tool)
	Done         bool
	Error        error
	InputTokens  int    // Populated on Done chunk: tokens consumed by the prompt
	OutputTokens int    // Populated on Done chunk: tokens in the generated response
	StopSequence string // Populated on Done chunk if a stop sequence triggered completion

	// PII anonymization metadata from the sidecar. Non-nil only when pii-filter is
	// active. InputAnonymizations is set on the first chunk per request;
	// OutputAnonymizations lists substitutions made in this specific chunk.
	InputAnonymizations  []presidio.AnonymizationRecord
	OutputAnonymizations []presidio.AnonymizationRecord
}
