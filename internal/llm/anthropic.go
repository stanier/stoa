package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicClient implements the Client interface using the Anthropic API
type AnthropicClient struct {
	client anthropic.Client
	pc     *config.LLMProviderConfig
}

// NewAnthropicClient creates a new Anthropic client from a provider config entry.
func NewAnthropicClient(pc *config.LLMProviderConfig) (*AnthropicClient, error) {
	return newAnthropicClientWithHTTP(pc, nil)
}

func newAnthropicClientWithHTTP(pc *config.LLMProviderConfig, httpClient *http.Client) (*AnthropicClient, error) {
	if pc == nil {
		return nil, fmt.Errorf("provider config is required")
	}

	opts := []option.RequestOption{
		option.WithAPIKey(pc.APIKey),
	}

	const defaultAnthropicURL = "https://api.anthropic.com"
	if pc.APIURL != "" && pc.APIURL != defaultAnthropicURL {
		opts = append(opts, option.WithBaseURL(pc.APIURL))
	}
	if httpClient != nil {
		opts = append(opts, option.WithHTTPClient(httpClient))
	}

	client := anthropic.NewClient(opts...)

	return &AnthropicClient{
		client: client,
		pc:     pc,
	}, nil
}

// wrapAnthropicAuthError replaces SDK-generated credential error messages (which
// reference ANTHROPIC_API_KEY) with messages that name the configured api_key_env.
func (c *AnthropicClient) wrapAnthropicAuthError(err error) error {
	keyEnv := c.pc.APIKeyEnv
	if keyEnv == "" {
		keyEnv = "ANTHROPIC_API_KEY"
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == 401 {
		return fmt.Errorf("authentication failed for provider %q (HTTP 401): check the value of %s", c.pc.Name, keyEnv)
	}
	if strings.Contains(err.Error(), "no Anthropic credentials found") || strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		return fmt.Errorf("no API key for provider %q: set %s", c.pc.Name, keyEnv)
	}
	return err
}

// anthropicUserMessage builds an Anthropic user message param, including image
// parts when the message carries vision content. Mirrors openAIUserMessage.
func anthropicUserMessage(msg chat.Message) anthropic.MessageParam {
	if len(msg.ImageParts) == 0 {
		return anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content))
	}
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.ImageParts)+1)
	for _, ip := range msg.ImageParts {
		if strings.HasPrefix(ip.URL, "data:") {
			// data:<mediaType>;base64,<data>
			semi := strings.Index(ip.URL, ";")
			comma := strings.Index(ip.URL, ",")
			if semi > 5 && comma > semi {
				mediaType := ip.URL[5:semi]
				b64Data := ip.URL[comma+1:]
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfImage: &anthropic.ImageBlockParam{
						Type: "image",
						Source: anthropic.ImageBlockParamSourceUnion{
							OfBase64: &anthropic.Base64ImageSourceParam{
								Type:      "base64",
								MediaType: anthropic.Base64ImageSourceMediaType(mediaType),
								Data:      b64Data,
							},
						},
					},
				})
				continue
			}
		}
		// Fall back to URL source for https:// links.
		blocks = append(blocks, anthropic.ContentBlockParamUnion{
			OfImage: &anthropic.ImageBlockParam{
				Type: "image",
				Source: anthropic.ImageBlockParamSourceUnion{
					OfURL: &anthropic.URLImageSourceParam{
						Type: "url",
						URL:  ip.URL,
					},
				},
			},
		})
	}
	if msg.Content != "" {
		blocks = append(blocks, anthropic.ContentBlockParamUnion{
			OfText: &anthropic.TextBlockParam{Text: msg.Content},
		})
	}
	return anthropic.MessageParam{
		Role:    anthropic.MessageParamRoleUser,
		Content: blocks,
	}
}

// convertToolsToAnthropic converts our Tool format to Anthropic SDK format
func convertToolsToAnthropic(tools []Tool) []anthropic.ToolUnionParam {
	result := make([]anthropic.ToolUnionParam, len(tools))
	for i, tool := range tools {
		// Use shared utility to extract schema
		schemaMap := extractToolSchemaMap(tool.InputSchema)
		properties := extractSchemaProperties(schemaMap)

		toolParam := anthropic.ToolParam{
			Name:        tool.Name,
			Description: anthropic.String(tool.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: properties,
			},
		}
		if tool.CacheControl == "ephemeral" {
			toolParam.CacheControl = anthropic.CacheControlEphemeralParam{Type: "ephemeral"}
		}

		result[i] = anthropic.ToolUnionParam{OfTool: &toolParam}
	}
	return result
}

// SendMessage sends a message and returns the complete response
func (c *AnthropicClient) SendMessage(ctx context.Context, messages []chat.Message, tools []Tool) (string, error) {
	// Separate system messages from conversation messages.
	// If the proxy attached raw Anthropic message JSON (to preserve cache_control blocks),
	// use it directly; otherwise convert from chat.Message.
	var systemBlocks []anthropic.TextBlockParam
	var apiMessages []anthropic.MessageParam

	if raw, ok := anthropicRawFromContext(ctx); ok && len(raw.Messages) > 0 {
		apiMessages = rebuildAnthropicMessages(raw.Messages)
		systemBlocks = rebuildAnthropicSystem(raw.System)
	} else {
		apiMessages = make([]anthropic.MessageParam, 0, len(messages))
		for _, msg := range messages {
			switch msg.Role {
			case chat.RoleSystem:
				systemBlocks = append(systemBlocks, anthropic.TextBlockParam{Text: msg.Content})
			case chat.RoleUser:
				apiMessages = append(apiMessages, anthropicUserMessage(msg))
			case chat.RoleAssistant:
				apiMessages = append(apiMessages, anthropic.MessageParam{
					Role: anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{{
						OfText: &anthropic.TextBlockParam{Text: msg.Content},
					}},
				})
			}
		}
	}

	// Merge model override from context if present.
	ov := OverridesFromContext(ctx)
	sendModel := c.pc.Model
	if ov != nil && ov.Model != nil {
		sendModel = *ov.Model
	}

	// Create message parameters
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(sendModel),
		Messages:  apiMessages,
		MaxTokens: int64(c.pc.MaxTokens),
	}

	// Add tools if provided
	if len(tools) > 0 {
		params.Tools = convertToolsToAnthropic(tools)
	}

	// Add optional parameters only if configured
	if c.pc.Temperature != nil {
		params.Temperature = anthropic.Float(*c.pc.Temperature)
	}

	if c.pc.TopP != nil {
		params.TopP = anthropic.Float(*c.pc.TopP)
	}

	if c.pc.TopK != nil {
		params.TopK = anthropic.Int(int64(*c.pc.TopK))
	}

	if len(c.pc.Stop) > 0 {
		params.StopSequences = c.pc.Stop
	}

	// Add system prompt if present
	if len(systemBlocks) > 0 {
		params.System = systemBlocks
	}

	// Set metadata.user_id if present in overrides.
	if ov != nil && ov.AnthropicUserID != "" {
		params.Metadata = anthropic.MetadataParam{UserID: anthropic.String(ov.AnthropicUserID)}
	}

	// Forward anthropic-beta header and per-call API key if present in overrides.
	sendOpts := []option.RequestOption{}
	if ov != nil && ov.AnthropicBeta != "" {
		sendOpts = append(sendOpts, option.WithHeader("anthropic-beta", ov.AnthropicBeta))
	}
	if ov != nil && ov.APIKey != nil {
		sendOpts = append(sendOpts, option.WithAPIKey(*ov.APIKey))
	}

	// Create message
	message, err := c.client.Messages.New(ctx, params, sendOpts...)
	if err != nil {
		return "", c.wrapAnthropicAuthError(err)
	}

	// Extract text content (tool_use blocks will be handled separately by the UI)
	var response string
	for _, block := range message.Content {
		if block.Type == "text" {
			response += block.Text
		}
		// Note: tool_use blocks are ignored in non-streaming mode
		// Streaming mode should be used for tool calling
	}

	return response, nil
}

// StreamMessage sends a message and returns a channel for streaming responses
func (c *AnthropicClient) StreamMessage(ctx context.Context, messages []chat.Message, tools []Tool) (<-chan StreamChunk, error) {
	// Separate system messages from conversation messages.
	// If the proxy attached raw Anthropic message JSON (to preserve cache_control blocks),
	// use it directly; otherwise convert from chat.Message.
	var systemBlocks []anthropic.TextBlockParam
	var apiMessages []anthropic.MessageParam

	if raw, ok := anthropicRawFromContext(ctx); ok && len(raw.Messages) > 0 {
		apiMessages = rebuildAnthropicMessages(raw.Messages)
		systemBlocks = rebuildAnthropicSystem(raw.System)
	} else {
		apiMessages = make([]anthropic.MessageParam, 0, len(messages))
		for _, msg := range messages {
			switch msg.Role {
			case chat.RoleSystem:
				systemBlocks = append(systemBlocks, anthropic.TextBlockParam{Text: msg.Content})
			case chat.RoleUser:
				apiMessages = append(apiMessages, anthropicUserMessage(msg))
			case chat.RoleAssistant:
				apiMessages = append(apiMessages, anthropic.MessageParam{
					Role: anthropic.MessageParamRoleAssistant,
					Content: []anthropic.ContentBlockParamUnion{{
						OfText: &anthropic.TextBlockParam{Text: msg.Content},
					}},
				})
			}
		}
	}

	// Create message parameters — MaxTokens set below after merging overrides.
	params := anthropic.MessageNewParams{
		Model:    anthropic.Model(c.pc.Model),
		Messages: apiMessages,
	}

	// Add tools if provided
	if len(tools) > 0 {
		params.Tools = convertToolsToAnthropic(tools)
	}

	// Merge global config with any per-session overrides from context.
	// Overrides take precedence over global config values.
	o := OverridesFromContext(ctx)

	temperature := c.pc.Temperature
	if o != nil && o.Temperature != nil {
		temperature = o.Temperature
	}
	topP := c.pc.TopP
	if o != nil && o.TopP != nil {
		topP = o.TopP
	}
	topK := c.pc.TopK
	if o != nil && o.TopK != nil {
		topK = o.TopK
	}
	maxTokens := int64(c.pc.MaxTokens)
	if o != nil && o.MaxTokens != nil {
		maxTokens = int64(*o.MaxTokens)
	}
	if o != nil && o.Model != nil {
		params.Model = anthropic.Model(*o.Model)
	}
	enableThinking := c.pc.EnableThinking
	if o != nil && o.EnableThinking != nil {
		enableThinking = o.EnableThinking
	}
	thinkingBudget := c.pc.ThinkingBudgetTokens
	if o != nil && o.ThinkingBudgetTokens != nil {
		thinkingBudget = o.ThinkingBudgetTokens
	}

	params.MaxTokens = maxTokens

	if temperature != nil {
		params.Temperature = anthropic.Float(*temperature)
	}
	if topP != nil {
		params.TopP = anthropic.Float(*topP)
	}
	if topK != nil {
		params.TopK = anthropic.Int(int64(*topK))
	}
	if len(c.pc.Stop) > 0 {
		params.StopSequences = c.pc.Stop
	}
	if enableThinking != nil && *enableThinking {
		budget := 10000
		if thinkingBudget != nil {
			budget = *thinkingBudget
		}
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(int64(budget))
	}

	// Add system prompt if present
	if len(systemBlocks) > 0 {
		params.System = systemBlocks
	}

	// Apply tool_choice override if present.
	if o != nil && len(o.ToolChoice) > 0 {
		var tc struct {
			Type string `json:"type"`
			Name string `json:"name,omitempty"`
		}
		if err := json.Unmarshal(o.ToolChoice, &tc); err == nil {
			switch tc.Type {
			case "auto":
				params.ToolChoice = anthropic.ToolChoiceUnionParam{
					OfAuto: &anthropic.ToolChoiceAutoParam{},
				}
			case "any":
				params.ToolChoice = anthropic.ToolChoiceUnionParam{
					OfAny: &anthropic.ToolChoiceAnyParam{},
				}
			case "tool":
				params.ToolChoice = anthropic.ToolChoiceUnionParam{
					OfTool: &anthropic.ToolChoiceToolParam{Name: tc.Name},
				}
			}
		}
	}

	// Set metadata.user_id if present in overrides.
	if o != nil && o.AnthropicUserID != "" {
		params.Metadata = anthropic.MetadataParam{UserID: anthropic.String(o.AnthropicUserID)}
	}

	// Build request options; forward anthropic-beta header if present.
	streamOpts := []option.RequestOption{}
	if o != nil && o.AnthropicBeta != "" {
		streamOpts = append(streamOpts, option.WithHeader("anthropic-beta", o.AnthropicBeta))
	}
	if o != nil && o.APIKey != nil {
		streamOpts = append(streamOpts, option.WithAPIKey(*o.APIKey))
	}

	// Capture upstream HTTP response headers (e.g. rate-limit headers) via
	// middleware so callers that attach an UpstreamHeaders sink to the context
	// can forward them to their own clients.
	if sink := UpstreamHeadersFromContext(ctx); sink != nil {
		streamOpts = append(streamOpts, option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
			resp, err := next(req)
			if resp != nil {
				sink.Set(resp.Header)
			}
			return resp, err
		}))
	}

	// Create streaming message
	stream := c.client.Messages.NewStreaming(ctx, params, streamOpts...)

	// Create output channel
	out := make(chan StreamChunk, 10)

	// Process stream in goroutine
	go func() {
		defer close(out)
		defer stream.Close()

		// Track current tool use
		var currentToolUse *ToolUse
		var toolInputBuffer string

		var acc anthropic.Message
		for stream.Next() {
			_ = acc.Accumulate(stream.Current())
			event := stream.Current()

			// Check for tool use start
			if event.Type == "content_block_start" {
				if event.ContentBlock.Type == "tool_use" {
					currentToolUse = &ToolUse{
						ID:   event.ContentBlock.ID,
						Name: event.ContentBlock.Name,
					}
					toolInputBuffer = ""
				}
			}

			// Check for content block delta
			if event.Type == "content_block_delta" {
				if event.Delta.Type == "text_delta" {
					select {
					case out <- StreamChunk{Content: event.Delta.Text, Done: false}:
					case <-ctx.Done():
						out <- StreamChunk{Error: ctx.Err()}
						return
					}
				} else if event.Delta.Type == "thinking_delta" {
					// Capture thinking/reasoning text
					select {
					case out <- StreamChunk{Thinking: event.Delta.Thinking, Done: false}:
					case <-ctx.Done():
						out <- StreamChunk{Error: ctx.Err()}
						return
					}
				} else if event.Delta.Type == "input_json_delta" && currentToolUse != nil {
					// Accumulate tool input JSON
					toolInputBuffer += event.Delta.PartialJSON
				}
			}

			// Check for tool use completion
			if event.Type == "content_block_stop" && currentToolUse != nil {
				// Tool input is complete, send it
				currentToolUse.Parameters = []byte(toolInputBuffer)
				select {
				case out <- StreamChunk{ToolUse: currentToolUse, Done: false}:
				case <-ctx.Done():
					out <- StreamChunk{Error: ctx.Err()}
					return
				}
				currentToolUse = nil
				toolInputBuffer = ""
			}
		}

		if err := stream.Err(); err != nil {
			out <- StreamChunk{Error: c.wrapAnthropicAuthError(err)}
			return
		}

		// Send done signal
		out <- StreamChunk{
			Done:         true,
			InputTokens:  int(acc.Usage.InputTokens),
			OutputTokens: int(acc.Usage.OutputTokens),
			StopSequence: acc.StopSequence,
		}
	}()

	return out, nil
}

// ListModels returns models available from the Anthropic API.
func (c *AnthropicClient) ListModels(ctx context.Context) ([]string, error) {
	infos, err := c.ListModelInfo(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(infos))
	for i, m := range infos {
		ids[i] = m.ID
	}
	return ids, nil
}

// ListModelInfo returns Anthropic models with capability flags sourced from the SDK.
func (c *AnthropicClient) ListModelInfo(ctx context.Context) ([]ModelInfo, error) {
	page, err := c.client.Models.List(ctx, anthropic.ModelListParams{})
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	infos := make([]ModelInfo, 0, len(page.Data))
	for _, m := range page.Data {
		caps := m.Capabilities
		infos = append(infos, ModelInfo{
			ID:                m.ID,
			Vision:            caps.ImageInput.Supported,
			ToolUse:           true, // all current Anthropic models support tool use
			Thinking:          caps.Thinking.Supported,
			PDFInput:          caps.PDFInput.Supported,
			StructuredOutputs: caps.StructuredOutputs.Supported,
		})
	}
	return infos, nil
}
