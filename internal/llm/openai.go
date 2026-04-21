package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

// OpenAIClient implements the Client interface using the OpenAI API
type OpenAIClient struct {
	client          openai.Client
	config          *config.Config
	thinkingBuffer  strings.Builder // Accumulates thinking content
	inThinkingBlock bool            // Tracks if we're inside a thinking block
}

// NewOpenAIClientSimple creates an OpenAI-compatible client with minimal parameters.
// Useful for proxy and other lightweight consumers that don't have a full config.Config.
// Per-request parameters (model, maxTokens, temperature, etc.) can be passed via
// llm.Overrides in context. httpClient may be nil to use the SDK default.
func NewOpenAIClientSimple(apiKey, apiURL, defaultModel string, defaultMaxTokens int, httpClient *http.Client) (*OpenAIClient, error) {
	cfg := &config.Config{}
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = apiKey
	cfg.LLM.APIURL = apiURL
	cfg.LLM.Model = defaultModel
	cfg.LLM.MaxTokens = defaultMaxTokens
	return newOpenAIClientWithHTTP(cfg, httpClient)
}

// NewOpenAIClient creates a new OpenAI-compatible client
func NewOpenAIClient(cfg *config.Config) (*OpenAIClient, error) {
	return newOpenAIClientWithHTTP(cfg, nil)
}

func newOpenAIClientWithHTTP(cfg *config.Config, httpClient *http.Client) (*OpenAIClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}

	opts := []option.RequestOption{
		option.WithAPIKey(cfg.LLM.APIKey),
		option.WithBaseURL(cfg.LLM.APIURL),
	}
	if httpClient != nil {
		opts = append(opts, option.WithHTTPClient(httpClient))
	}

	client := openai.NewClient(opts...)

	return &OpenAIClient{
		client:          client,
		config:          cfg,
		inThinkingBlock: false,
	}, nil
}

// openAIUserMessage builds an OpenAI user message param, including image parts
// when the message carries vision content.
func openAIUserMessage(msg chat.Message) openai.ChatCompletionMessageParamUnion {
	if len(msg.ImageParts) == 0 {
		return openai.UserMessage(msg.Content)
	}
	// Build a multipart content array: images first (preserving order: text at end).
	parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(msg.ImageParts)+1)
	for _, ip := range msg.ImageParts {
		imgParam := openai.ChatCompletionContentPartImageParam{
			Type: "image_url",
			ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
				URL:    ip.URL,
				Detail: ip.Detail,
			},
		}
		parts = append(parts, openai.ChatCompletionContentPartUnionParam{
			OfImageURL: &imgParam,
		})
	}
	if msg.Content != "" {
		textParam := openai.ChatCompletionContentPartTextParam{
			Type: "text",
			Text: msg.Content,
		}
		parts = append(parts, openai.ChatCompletionContentPartUnionParam{
			OfText: &textParam,
		})
	}
	return openai.ChatCompletionMessageParamUnion{
		OfUser: &openai.ChatCompletionUserMessageParam{
			Role: "user",
			Content: openai.ChatCompletionUserMessageParamContentUnion{
				OfArrayOfContentParts: parts,
			},
		},
	}
}

// convertToolsToOpenAI converts our Tool format to OpenAI SDK format (v1.12.0)
func convertToolsToOpenAI(tools []Tool) []openai.ChatCompletionToolParam {
	result := make([]openai.ChatCompletionToolParam, len(tools))
	for i, tool := range tools {
		// Use shared utility to extract schema and convert to FunctionParameters
		schemaMap := extractToolSchemaMap(tool.InputSchema)
		params := shared.FunctionParameters{}
		for k, v := range schemaMap {
			params[k] = v
		}

		// Create function definition
		funcDef := shared.FunctionDefinitionParam{
			Name:       tool.Name,
			Parameters: params,
		}

		// Add description if present (using openai.String helper)
		if tool.Description != "" {
			funcDef.Description = openai.String(tool.Description)
		}

		// Create tool param
		// Note: Type field can be elided and will default to "function"
		result[i] = openai.ChatCompletionToolParam{
			Function: funcDef,
		}
	}
	return result
}

// extractThinkingFromContent parses content for thinking delimiters
// Supports patterns like <thinking>...</thinking> or [THINKING]...[/THINKING]
// Returns (contentWithoutThinking, thinkingExtracted)
func (c *OpenAIClient) extractThinkingFromContent(content string) (string, string) {
	var cleanContent strings.Builder
	var thinking strings.Builder

	// Common thinking delimiters used by various endpoints
	delimiters := []struct {
		start string
		end   string
	}{
		{"<think>", "</think>"},         // qwen3, deepseek-r1
		{"<thinking>", "</thinking>"},
		{"<THINKING>", "</THINKING>"},
		{"[thinking]", "[/thinking]"},
		{"[THINKING]", "[/THINKING]"},
		{"<reason>", "</reason>"},
		{"<reasoning>", "</reasoning>"},
	}

	remaining := content
	for len(remaining) > 0 {
		foundDelimiter := false

		for _, delim := range delimiters {
			if !c.inThinkingBlock {
				// Look for start delimiter
				if idx := strings.Index(remaining, delim.start); idx != -1 {
					// Add content before delimiter
					cleanContent.WriteString(remaining[:idx])
					// Start thinking block
					c.inThinkingBlock = true
					remaining = remaining[idx+len(delim.start):]
					foundDelimiter = true
					break
				}
			} else {
				// Look for end delimiter
				if idx := strings.Index(remaining, delim.end); idx != -1 {
					// Add to thinking
					thinking.WriteString(remaining[:idx])
					// End thinking block
					c.inThinkingBlock = false
					remaining = remaining[idx+len(delim.end):]
					foundDelimiter = true
					break
				}
			}
		}

		if !foundDelimiter {
			// No delimiter found, add remaining to appropriate buffer
			if c.inThinkingBlock {
				thinking.WriteString(remaining)
			} else {
				cleanContent.WriteString(remaining)
			}
			break
		}
	}

	return cleanContent.String(), thinking.String()
}

// SendMessage sends a message and returns the complete response
func (c *OpenAIClient) SendMessage(ctx context.Context, messages []chat.Message, tools []Tool) (string, error) {
	// Convert our message format to OpenAI format
	apiMessages := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case chat.RoleUser:
			apiMessages = append(apiMessages, openAIUserMessage(msg))
		case chat.RoleAssistant:
			apiMessages = append(apiMessages, openai.AssistantMessage(msg.Content))
		case chat.RoleSystem:
			apiMessages = append(apiMessages, openai.SystemMessage(msg.Content))
		}
	}

	// Merge model override from context if present.
	ov := OverridesFromContext(ctx)
	sendModel := c.config.LLM.Model
	if ov != nil && ov.Model != nil {
		sendModel = *ov.Model
	}

	// Convert tools to OpenAI format
	params := openai.ChatCompletionNewParams{
		Model:     shared.ChatModel(sendModel),
		Messages:  apiMessages,
		MaxTokens: openai.Int(int64(c.config.LLM.MaxTokens)),
	}

	// Add optional parameters only if configured
	if c.config.LLM.Temperature != nil {
		params.Temperature = openai.Float(*c.config.LLM.Temperature)
	}

	if c.config.LLM.TopP != nil {
		params.TopP = openai.Float(*c.config.LLM.TopP)
	}

	if c.config.LLM.PresencePenalty != nil {
		params.PresencePenalty = openai.Float(*c.config.LLM.PresencePenalty)
	}

	if c.config.LLM.FrequencyPenalty != nil {
		params.FrequencyPenalty = openai.Float(*c.config.LLM.FrequencyPenalty)
	}

	if len(c.config.LLM.Stop) > 0 {
		// OpenAI Stop parameter can be string or array - use array form
		params.Stop = openai.ChatCompletionNewParamsStopUnion{
			OfStringArray: c.config.LLM.Stop,
		}
	}

	// Add tools if provided (OpenAI function calling)
	if len(tools) > 0 {
		params.Tools = convertToolsToOpenAI(tools)
	}

	// Pass thinking overrides as extra body fields for endpoints that support it
	// (e.g. qwen3 via vLLM/Ollama: enable_thinking + thinking_budget_tokens).
	sendExtraFields := map[string]any{}
	if ov != nil && ov.EnableThinking != nil {
		sendExtraFields["enable_thinking"] = *ov.EnableThinking
		sendExtraFields["thinking_budget_tokens"] = func() int {
			if ov.ThinkingBudgetTokens != nil {
				return *ov.ThinkingBudgetTokens
			}
			return 0
		}()
	}
	// Apply response_format if provided.
	if ov != nil && len(ov.ResponseFormat) > 0 {
		var rf struct{ Type string `json:"type"` }
		if json.Unmarshal(ov.ResponseFormat, &rf) == nil {
			switch rf.Type {
			case "json_object":
				params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
					OfJSONObject: &openai.ResponseFormatJSONObjectParam{Type: "json_object"},
				}
			case "text":
				params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
					OfText: &openai.ResponseFormatTextParam{Type: "text"},
				}
			case "json_schema":
				sendExtraFields["response_format"] = json.RawMessage(ov.ResponseFormat)
			}
		}
	}
	// Apply P3/P4 pass-through fields.
	if ov != nil {
		if ov.Seed != nil {
			params.Seed = openai.Int(*ov.Seed)
		}
		if ov.User != "" {
			params.User = openai.String(ov.User)
		}
		if ov.LogitBias != nil {
			params.LogitBias = ov.LogitBias
		}
		if ov.Logprobs != nil {
			params.Logprobs = openai.Bool(*ov.Logprobs)
		}
		if ov.TopLogprobs != nil {
			params.TopLogprobs = openai.Int(*ov.TopLogprobs)
		}
		if ov.ParallelToolCalls != nil {
			params.ParallelToolCalls = openai.Bool(*ov.ParallelToolCalls)
		}
	}
	if len(sendExtraFields) > 0 {
		params.SetExtraFields(sendExtraFields)
	}

	// Create chat completion
	completion, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("error creating completion: %w", err)
	}

	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("no completion choices returned")
	}

	return completion.Choices[0].Message.Content, nil
}

// StreamMessage sends a message and returns a channel for streaming responses.
// Per-session overrides are read from ctx via OverridesFromContext.
func (c *OpenAIClient) StreamMessage(ctx context.Context, messages []chat.Message, tools []Tool) (<-chan StreamChunk, error) {
	// Convert our message format to OpenAI format
	apiMessages := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case chat.RoleUser:
			apiMessages = append(apiMessages, openAIUserMessage(msg))
		case chat.RoleAssistant:
			apiMessages = append(apiMessages, openai.AssistantMessage(msg.Content))
		case chat.RoleSystem:
			apiMessages = append(apiMessages, openai.SystemMessage(msg.Content))
		}
	}

	// Merge global config with per-session overrides from context.
	o := OverridesFromContext(ctx)

	temperature := c.config.LLM.Temperature
	if o != nil && o.Temperature != nil {
		temperature = o.Temperature
	}
	topP := c.config.LLM.TopP
	if o != nil && o.TopP != nil {
		topP = o.TopP
	}
	maxTokens := int64(c.config.LLM.MaxTokens)
	if o != nil && o.MaxTokens != nil {
		maxTokens = int64(*o.MaxTokens)
	}
	model := c.config.LLM.Model
	if o != nil && o.Model != nil {
		model = *o.Model
	}

	params := openai.ChatCompletionNewParams{
		Model:     shared.ChatModel(model),
		Messages:  apiMessages,
		MaxTokens: openai.Int(maxTokens),
	}

	if temperature != nil {
		params.Temperature = openai.Float(*temperature)
	}
	if topP != nil {
		params.TopP = openai.Float(*topP)
	}
	if c.config.LLM.PresencePenalty != nil {
		params.PresencePenalty = openai.Float(*c.config.LLM.PresencePenalty)
	}
	if c.config.LLM.FrequencyPenalty != nil {
		params.FrequencyPenalty = openai.Float(*c.config.LLM.FrequencyPenalty)
	}
	if len(c.config.LLM.Stop) > 0 {
		params.Stop = openai.ChatCompletionNewParamsStopUnion{
			OfStringArray: c.config.LLM.Stop,
		}
	}

	// Add tools if provided (OpenAI function calling)
	if len(tools) > 0 {
		params.Tools = convertToolsToOpenAI(tools)
	}

	// Pass thinking overrides as extra body fields.
	extraFields := map[string]any{}
	if o != nil && o.EnableThinking != nil {
		extraFields["enable_thinking"] = *o.EnableThinking
		extraFields["thinking_budget_tokens"] = func() int {
			if o.ThinkingBudgetTokens != nil {
				return *o.ThinkingBudgetTokens
			}
			return 0
		}()
	}
	// Pass tool_choice as an extra field so it works with OpenAI-compatible endpoints.
	if o != nil && len(o.ToolChoice) > 0 {
		var tc any
		if err := json.Unmarshal(o.ToolChoice, &tc); err == nil {
			extraFields["tool_choice"] = tc
		}
	}
	// Apply response_format if provided.
	if o != nil && len(o.ResponseFormat) > 0 {
		var rf struct{ Type string `json:"type"` }
		if json.Unmarshal(o.ResponseFormat, &rf) == nil {
			switch rf.Type {
			case "json_object":
				params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
					OfJSONObject: &openai.ResponseFormatJSONObjectParam{Type: "json_object"},
				}
			case "text":
				params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
					OfText: &openai.ResponseFormatTextParam{Type: "text"},
				}
			case "json_schema":
				extraFields["response_format"] = json.RawMessage(o.ResponseFormat)
			}
		}
	}
	// Apply P3/P4 pass-through fields.
	if o != nil {
		if o.Seed != nil {
			params.Seed = openai.Int(*o.Seed)
		}
		if o.User != "" {
			params.User = openai.String(o.User)
		}
		if o.LogitBias != nil {
			params.LogitBias = o.LogitBias
		}
		if o.Logprobs != nil {
			params.Logprobs = openai.Bool(*o.Logprobs)
		}
		if o.TopLogprobs != nil {
			params.TopLogprobs = openai.Int(*o.TopLogprobs)
		}
		if o.ParallelToolCalls != nil {
			params.ParallelToolCalls = openai.Bool(*o.ParallelToolCalls)
		}
	}
	if len(extraFields) > 0 {
		params.SetExtraFields(extraFields)
	}

	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
		IncludeUsage: openai.Bool(true),
	}

	// Capture upstream HTTP response headers (e.g. rate-limit headers) via
	// middleware so callers that attach an UpstreamHeaders sink to the context
	// can forward them to their own clients.
	var streamOpts []option.RequestOption
	if sink := UpstreamHeadersFromContext(ctx); sink != nil {
		streamOpts = append(streamOpts, option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
			resp, err := next(req)
			if resp != nil {
				sink.Set(resp.Header)
			}
			return resp, err
		}))
	}
	if ov := OverridesFromContext(ctx); ov != nil && ov.APIKey != nil {
		streamOpts = append(streamOpts, option.WithAPIKey(*ov.APIKey))
	}

	// Create streaming completion
	stream := c.client.Chat.Completions.NewStreaming(ctx, params, streamOpts...)

	// Create output channel
	out := make(chan StreamChunk, 10)

	// Process stream in goroutine
	go func() {
		defer close(out)
		defer stream.Close()

		// Track tool calls being built up
		toolCalls := make(map[int]*ToolUse)

		var usageInputTokens, usageOutputTokens int

		for stream.Next() {
			chunk := stream.Current()
			if len(chunk.Choices) > 0 {
				delta := chunk.Choices[0].Delta

				// Process content - may contain embedded thinking
				if delta.Content != "" {
					// Try to extract thinking from content
					cleanContent, thinkingText := c.extractThinkingFromContent(delta.Content)

					// Prepare the stream chunk
					streamChunk := StreamChunk{Done: false}

					// Add extracted content (if any)
					if cleanContent != "" {
						streamChunk.Content = cleanContent
					}

					// Add extracted thinking (if any)
					if thinkingText != "" {
						streamChunk.Thinking = thinkingText
					}

					// Only send if we have content or thinking
					if streamChunk.Content != "" || streamChunk.Thinking != "" {
						select {
						case out <- streamChunk:
						case <-ctx.Done():
							out <- StreamChunk{Error: ctx.Err()}
							return
						}
					}
				}

				// Process tool calls
				if len(delta.ToolCalls) > 0 {
					for _, toolCall := range delta.ToolCalls {
						idx := int(toolCall.Index)

						// Initialize tool call if needed
						if _, exists := toolCalls[idx]; !exists {
							toolCalls[idx] = &ToolUse{
								ID:   toolCall.ID,
								Name: toolCall.Function.Name,
							}
						}

						// Accumulate function arguments
						if toolCall.Function.Arguments != "" {
							toolCalls[idx].Parameters = append(toolCalls[idx].Parameters, toolCall.Function.Arguments...)
						}
					}
				}

				// Check if streaming is done for this choice
				if chunk.Choices[0].FinishReason != "" {
					// Send any accumulated tool calls
					for _, toolUse := range toolCalls {
						if toolUse != nil && toolUse.Name != "" {
							select {
							case out <- StreamChunk{ToolUse: toolUse, Done: false}:
							case <-ctx.Done():
								out <- StreamChunk{Error: ctx.Err()}
								return
							}
						}
					}
				}
			}

			// Capture usage from the final chunk (only present when stream_options.include_usage=true)
			if chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 {
				usageInputTokens = int(chunk.Usage.PromptTokens)
				usageOutputTokens = int(chunk.Usage.CompletionTokens)
			}
		}

		if err := stream.Err(); err != nil {
			out <- StreamChunk{Error: fmt.Errorf("streaming error: %w", err)}
			return
		}

		// Send done signal
		out <- StreamChunk{
			Done:         true,
			InputTokens:  usageInputTokens,
			OutputTokens: usageOutputTokens,
		}
	}()

	return out, nil
}

// ListModels returns all models available from this OpenAI-compatible endpoint.
func (c *OpenAIClient) ListModels(ctx context.Context) ([]string, error) {
	page, err := c.client.Models.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	ids := make([]string, 0, len(page.Data))
	for _, m := range page.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}
