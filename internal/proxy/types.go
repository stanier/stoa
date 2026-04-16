package proxy

import "encoding/json"

// ─── OpenAI wire types ─────────────────────────────────────────────────────

// OpenAIRequest is the JSON body for POST /v1/chat/completions.
type OpenAIRequest struct {
	Model            string          `json:"model"`
	Messages         []OpenAIMessage `json:"messages"`
	Tools            []OpenAITool    `json:"tools,omitempty"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	Stop             json.RawMessage `json:"stop,omitempty"` // string or []string
	Stream           bool            `json:"stream"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	ToolChoice       json.RawMessage `json:"tool_choice,omitempty"`
	ResponseFormat   json.RawMessage  `json:"response_format,omitempty"`
	Seed             *int64           `json:"seed,omitempty"`
	User             string           `json:"user,omitempty"`
	LogitBias        map[string]int64 `json:"logit_bias,omitempty"`
	Logprobs         *bool            `json:"logprobs,omitempty"`
	TopLogprobs      *int64           `json:"top_logprobs,omitempty"`
	ParallelToolCalls *bool                `json:"parallel_tool_calls,omitempty"`
	N                *int                 `json:"n,omitempty"`
	StreamOptions    *OpenAIStreamOptions `json:"stream_options,omitempty"`
}

// OpenAIStreamOptions controls streaming-specific behaviour.
type OpenAIStreamOptions struct {
	IncludeUsage *bool `json:"include_usage,omitempty"`
}

// OpenAIMessage is one message in the OpenAI messages array.
type OpenAIMessage struct {
	Role       string           `json:"role"`
	Content    json.RawMessage  `json:"content,omitempty"` // string or []content-part
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
	Refusal    *string          `json:"refusal,omitempty"`
}

// OpenAIToolCall is a tool invocation inside an assistant message.
type OpenAIToolCall struct {
	Index    *int               `json:"index,omitempty"`
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function OpenAIFunctionCall `json:"function"`
}

// OpenAIFunctionCall holds function name and JSON-encoded arguments.
type OpenAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// OpenAITool describes a callable tool.
type OpenAITool struct {
	Type     string            `json:"type"` // "function"
	Function OpenAIToolDef     `json:"function"`
}

// OpenAIToolDef is the function definition inside a tool descriptor.
type OpenAIToolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

// OpenAIResponse is the non-streaming response body.
type OpenAIResponse struct {
	ID                 string         `json:"id"`
	Object             string         `json:"object"`
	Created            int64          `json:"created"`
	Model              string         `json:"model"`
	Choices            []OpenAIChoice `json:"choices"`
	Usage              OpenAIUsage    `json:"usage"`
	SystemFingerprint  *string        `json:"system_fingerprint,omitempty"`
	ServiceTier        string         `json:"service_tier,omitempty"`
}

// OpenAIChoice is one choice in a non-streaming response.
type OpenAIChoice struct {
	Index        int           `json:"index"`
	Message      OpenAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

// OpenAIUsage contains token usage counts.
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIStreamChunk is one SSE data object for streaming responses.
type OpenAIStreamChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []OpenAIStreamChoice `json:"choices"`
	Usage   *OpenAIUsage         `json:"usage,omitempty"`
}

// OpenAIStreamChoice is one choice inside a streaming chunk.
type OpenAIStreamChoice struct {
	Index        int          `json:"index"`
	Delta        OpenAIDelta  `json:"delta"`
	FinishReason *string      `json:"finish_reason"`
}

// OpenAIDelta is the incremental content inside a streaming choice.
type OpenAIDelta struct {
	Role      string           `json:"role,omitempty"`
	Content   *string          `json:"content,omitempty"`
	ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
}

// ─── Anthropic wire types ──────────────────────────────────────────────────

// AnthropicMetadata holds optional per-request metadata sent to the Anthropic API.
type AnthropicMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

// AnthropicRequest is the JSON body for POST /v1/messages.
type AnthropicRequest struct {
	Model         string             `json:"model"`
	Messages      []AnthropicMessage `json:"messages"`
	System        json.RawMessage    `json:"system,omitempty"` // string or []block
	Tools         []AnthropicTool    `json:"tools,omitempty"`
	MaxTokens     int                `json:"max_tokens"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	TopK          *int               `json:"top_k,omitempty"`
	Stream        bool               `json:"stream"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	ToolChoice    json.RawMessage    `json:"tool_choice,omitempty"`
	Thinking      json.RawMessage    `json:"thinking,omitempty"`
	Metadata      *AnthropicMetadata `json:"metadata,omitempty"`
}

// AnthropicMessage is one message in the Anthropic messages array.
type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []AnthropicContentBlock
}

// AnthropicCacheControl marks a block for prompt caching. Currently only "ephemeral" is supported.
type AnthropicCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// AnthropicContentBlock is one block inside a message's content array.
type AnthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // for tool_result
}

// AnthropicTool describes a callable tool.
type AnthropicTool struct {
	Name         string                `json:"name"`
	Description  string                `json:"description,omitempty"`
	InputSchema  interface{}           `json:"input_schema"`
	CacheControl *AnthropicCacheControl `json:"cache_control,omitempty"`
}

// AnthropicResponse is the non-streaming response body.
type AnthropicResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Model        string                  `json:"model"`
	Content      []AnthropicContentBlock `json:"content"`
	StopReason   string                  `json:"stop_reason"`
	StopSequence *string                 `json:"stop_sequence"`
	Usage        AnthropicUsage          `json:"usage"`
}

// AnthropicUsage contains token usage counts.
type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// AnthropicStreamEvent is one SSE event object for Anthropic streaming.
type AnthropicStreamEvent struct {
	Type         string                  `json:"type"`
	Index        *int                    `json:"index,omitempty"`
	Message      *AnthropicStreamMessage `json:"message,omitempty"`
	ContentBlock *AnthropicStreamBlock   `json:"content_block,omitempty"`
	Delta        *AnthropicStreamDelta   `json:"delta,omitempty"`
	Usage        *AnthropicUsage         `json:"usage,omitempty"`
}

// AnthropicStreamMessage is the metadata in a message_start event.
type AnthropicStreamMessage struct {
	ID    string         `json:"id"`
	Type  string         `json:"type"`
	Role  string         `json:"role"`
	Model string         `json:"model"`
	Usage AnthropicUsage `json:"usage"`
}

// AnthropicStreamBlock is the metadata in a content_block_start event.
type AnthropicStreamBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// AnthropicStreamDelta is the incremental content in a content_block_delta or message_delta event.
type AnthropicStreamDelta struct {
	Type         string  `json:"type"`
	Text         string  `json:"text,omitempty"`
	Thinking     string  `json:"thinking,omitempty"`
	PartialJSON  string  `json:"partial_json,omitempty"`
	StopReason   string  `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence,omitempty"`
}
