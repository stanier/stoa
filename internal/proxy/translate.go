package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
)

// ─── OpenAI → internal ─────────────────────────────────────────────────────

// ParseOpenAIMessages converts OpenAI wire messages to internal chat.Message format.
func ParseOpenAIMessages(msgs []OpenAIMessage) ([]chat.Message, error) {
	result := make([]chat.Message, 0, len(msgs))
	for _, m := range msgs {
		content, imageParts, err := extractOpenAIContent(m.Content)
		if err != nil {
			return nil, fmt.Errorf("message %q: %w", m.Role, err)
		}
		switch m.Role {
		case "user":
			msg := chat.NewUserMessage(content)
			msg.ImageParts = imageParts
			result = append(result, msg)
		case "assistant":
			result = append(result, chat.NewAssistantMessage(content))
		case "system":
			result = append(result, chat.NewSystemMessage(content))
		case "tool":
			result = append(result, chat.NewToolResultMessage(m.Name, m.ToolCallID, content, true))
		}
	}
	return result, nil
}

// ParseOpenAITools converts OpenAI tool definitions to internal llm.Tool format.
func ParseOpenAITools(tools []OpenAITool) []llm.Tool {
	result := make([]llm.Tool, 0, len(tools))
	for _, t := range tools {
		result = append(result, llm.Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	return result
}

// extractOpenAIContent extracts text and image parts from an OpenAI message content
// field, which can be either a JSON string or an array of content parts.
// Returns (text, imageParts, error).
func extractOpenAIContent(raw json.RawMessage) (string, []chat.ImagePart, error) {
	if len(raw) == 0 {
		return "", nil, nil
	}
	// Try plain string first
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil, nil
	}
	// Try array of content parts
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		ImageURL *struct {
			URL    string `json:"url"`
			Detail string `json:"detail,omitempty"`
		} `json:"image_url,omitempty"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil, fmt.Errorf("cannot parse message content: %w", err)
	}
	var sb strings.Builder
	var imageParts []chat.ImagePart
	for _, p := range parts {
		switch p.Type {
		case "text":
			sb.WriteString(p.Text)
		case "image_url":
			if p.ImageURL != nil {
				imageParts = append(imageParts, chat.ImagePart{
					URL:    p.ImageURL.URL,
					Detail: p.ImageURL.Detail,
				})
			}
		case "input_audio", "file":
			return "", nil, fmt.Errorf("content part type %q is not yet supported by this proxy", p.Type)
		}
	}
	return sb.String(), imageParts, nil
}

// ─── Anthropic → internal ──────────────────────────────────────────────────

// RawMessagesForCacheControl packages the inbound Anthropic request's messages
// and system prompt as raw JSON so cache_control blocks are forwarded verbatim
// to the Anthropic SDK rather than being lost during chat.Message conversion.
func RawMessagesForCacheControl(req *AnthropicRequest) llm.AnthropicRawMessages {
	raw := llm.AnthropicRawMessages{}
	for _, m := range req.Messages {
		b, _ := json.Marshal(m)
		raw.Messages = append(raw.Messages, b)
	}
	if len(req.System) > 0 {
		raw.System = []json.RawMessage{req.System}
	}
	return raw
}

// ParseAnthropicMessages converts Anthropic wire messages + system to internal format.
// systemRaw is the raw JSON value of the "system" field (string or block array).
func ParseAnthropicMessages(msgs []AnthropicMessage, systemRaw json.RawMessage) ([]chat.Message, error) {
	result := make([]chat.Message, 0, len(msgs)+1)

	// Prepend system prompt if provided
	if len(systemRaw) > 0 {
		sys, err := extractAnthropicSystem(systemRaw)
		if err != nil {
			return nil, fmt.Errorf("system: %w", err)
		}
		if sys != "" {
			result = append(result, chat.NewSystemMessage(sys))
		}
	}

	for _, m := range msgs {
		content, toolUseID, toolName, err := extractAnthropicContent(m.Content)
		if err != nil {
			return nil, fmt.Errorf("message %q: %w", m.Role, err)
		}
		switch m.Role {
		case "user":
			if toolUseID != "" {
				result = append(result, chat.NewToolResultMessage(toolName, toolUseID, content, true))
			} else {
				result = append(result, chat.NewUserMessage(content))
			}
		case "assistant":
			result = append(result, chat.NewAssistantMessage(content))
		}
	}
	return result, nil
}

// ParseAnthropicTools converts Anthropic tool definitions to internal llm.Tool format.
func ParseAnthropicTools(tools []AnthropicTool) []llm.Tool {
	result := make([]llm.Tool, 0, len(tools))
	for _, t := range tools {
		tool := llm.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
		if t.CacheControl != nil {
			tool.CacheControl = t.CacheControl.Type
		}
		result = append(result, tool)
	}
	return result
}

// extractAnthropicSystem returns the text of the system prompt from its raw JSON value.
func extractAnthropicSystem(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	// Plain string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	// Array of text blocks
	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", fmt.Errorf("cannot parse system field: %w", err)
	}
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String(), nil
}

// extractAnthropicContent extracts text from an Anthropic message content field.
// Returns (text, toolUseID, toolName, err).
func extractAnthropicContent(raw json.RawMessage) (string, string, string, error) {
	if len(raw) == 0 {
		return "", "", "", nil
	}
	// Plain string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, "", "", nil
	}
	// Array of content blocks
	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", "", "", fmt.Errorf("cannot parse message content: %w", err)
	}
	var sb strings.Builder
	var toolUseID, toolName string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			sb.WriteString(b.Text)
		case "tool_result":
			toolUseID = b.ToolUseID
			// Content may be string or blocks
			if len(b.Content) > 0 {
				inner, _, _, _ := extractAnthropicContent(b.Content)
				sb.WriteString(inner)
			}
		}
	}
	return sb.String(), toolUseID, toolName, nil
}

// ─── internal StreamChunk → OpenAI SSE ────────────────────────────────────

// MakeOpenAIStreamChunk formats a llm.StreamChunk as an OpenAI SSE data line.
// toolIndex is the zero-based index of the current tool call in this response;
// it is incremented by the caller after each ToolUse chunk.
// choiceIndex is the zero-based index of the completion choice (for n > 1).
// includeUsage controls whether the Done chunk carries a usage object.
// Returns nil when the chunk has nothing to emit.
func MakeOpenAIStreamChunk(chunk llm.StreamChunk, id, model string, choiceIndex, toolIndex int, includeUsage bool) []byte {
	c := &OpenAIStreamChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
	}

	if chunk.Done {
		fr := "stop"
		c.Choices = []OpenAIStreamChoice{{Index: choiceIndex, Delta: OpenAIDelta{}, FinishReason: &fr}}
		if includeUsage {
			c.Usage = &OpenAIUsage{
				PromptTokens:     chunk.InputTokens,
				CompletionTokens: chunk.OutputTokens,
				TotalTokens:      chunk.InputTokens + chunk.OutputTokens,
			}
		}
		return sseData(c)
	}

	if chunk.Content != "" {
		c.Choices = []OpenAIStreamChoice{{Index: choiceIndex, Delta: OpenAIDelta{Content: &chunk.Content}}}
		return sseData(c)
	}

	if chunk.ToolUse != nil {
		argStr := string(chunk.ToolUse.Parameters)
		idx := toolIndex
		c.Choices = []OpenAIStreamChoice{{
			Index: choiceIndex,
			Delta: OpenAIDelta{
				ToolCalls: []OpenAIToolCall{{
					Index: &idx,
					ID:    chunk.ToolUse.ID,
					Type:  "function",
					Function: OpenAIFunctionCall{
						Name:      chunk.ToolUse.Name,
						Arguments: argStr,
					},
				}},
			},
		}}
		return sseData(c)
	}

	return nil
}

// MakeOpenAIResponse builds a complete non-streaming OpenAI response from accumulated content.
func MakeOpenAIResponse(content string, toolCalls []llm.ToolUse, id, model string, inputTokens, outputTokens int) *OpenAIResponse {
	msg := OpenAIMessage{
		Role: "assistant",
	}
	if content != "" {
		raw, _ := json.Marshal(content)
		msg.Content = raw
	}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		for _, tc := range toolCalls {
			msg.ToolCalls = append(msg.ToolCalls, OpenAIToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: OpenAIFunctionCall{
					Name:      tc.Name,
					Arguments: string(tc.Parameters),
				},
			})
		}
	}
	return &OpenAIResponse{
		ID:          id,
		Object:      "chat.completion",
		Created:     time.Now().Unix(),
		Model:       model,
		ServiceTier: "default",
		Choices: []OpenAIChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		}},
		Usage: OpenAIUsage{
			PromptTokens:     inputTokens,
			CompletionTokens: outputTokens,
			TotalTokens:      inputTokens + outputTokens,
		},
	}
}

// ─── internal StreamChunk → Anthropic SSE ─────────────────────────────────

// MakeAnthropicStreamEvents formats a llm.StreamChunk as one or more Anthropic SSE events.
// Each returned []byte is one "event: X\ndata: Y\n\n" block.
func MakeAnthropicStreamEvents(chunk llm.StreamChunk, id, model string, blockIndex *int) [][]byte {
	if chunk.Done {
		stopReason := "end_turn"
		events := [][]byte{
			anthropicSSE("content_block_stop", AnthropicStreamEvent{Type: "content_block_stop", Index: blockIndex}),
			anthropicSSE("message_delta", AnthropicStreamEvent{
				Type:  "message_delta",
				Delta: &AnthropicStreamDelta{Type: "message_delta", StopReason: stopReason},
				Usage: &AnthropicUsage{InputTokens: chunk.InputTokens, OutputTokens: chunk.OutputTokens},
			}),
			anthropicSSE("message_stop", AnthropicStreamEvent{Type: "message_stop"}),
		}
		return events
	}

	if chunk.Content != "" {
		ev := AnthropicStreamEvent{
			Type:  "content_block_delta",
			Index: blockIndex,
			Delta: &AnthropicStreamDelta{Type: "text_delta", Text: chunk.Content},
		}
		return [][]byte{anthropicSSE("content_block_delta", ev)}
	}

	if chunk.Thinking != "" {
		ev := AnthropicStreamEvent{
			Type:  "content_block_delta",
			Index: blockIndex,
			Delta: &AnthropicStreamDelta{Type: "thinking_delta", Thinking: chunk.Thinking},
		}
		return [][]byte{anthropicSSE("content_block_delta", ev)}
	}

	if chunk.ToolUse != nil {
		idx := 0
		if blockIndex != nil {
			idx = *blockIndex
		}
		startEv := anthropicSSE("content_block_start", AnthropicStreamEvent{
			Type:  "content_block_start",
			Index: &idx,
			ContentBlock: &AnthropicStreamBlock{
				Type: "tool_use",
				ID:   chunk.ToolUse.ID,
				Name: chunk.ToolUse.Name,
			},
		})
		deltaEv := anthropicSSE("content_block_delta", AnthropicStreamEvent{
			Type:  "content_block_delta",
			Index: &idx,
			Delta: &AnthropicStreamDelta{Type: "input_json_delta", PartialJSON: string(chunk.ToolUse.Parameters)},
		})
		stopEv := anthropicSSE("content_block_stop", AnthropicStreamEvent{
			Type:  "content_block_stop",
			Index: &idx,
		})
		return [][]byte{startEv, deltaEv, stopEv}
	}

	return nil
}

// MakeAnthropicStartEvents returns the message_start and ping SSE events.
// It does NOT open a content block; block opening is managed by the streaming loop.
func MakeAnthropicStartEvents(id, model string) [][]byte {
	return [][]byte{
		anthropicSSE("message_start", AnthropicStreamEvent{
			Type: "message_start",
			Message: &AnthropicStreamMessage{
				ID:    id,
				Type:  "message",
				Role:  "assistant",
				Model: model,
				Usage: AnthropicUsage{},
			},
		}),
		anthropicSSE("ping", AnthropicStreamEvent{Type: "ping"}),
	}
}

// MakeAnthropicResponse builds a complete non-streaming Anthropic response.
func MakeAnthropicResponse(content string, toolCalls []llm.ToolUse, id, model string, inputTokens, outputTokens int, stopSequence string) *AnthropicResponse {
	blocks := []AnthropicContentBlock{}
	stopReason := "end_turn"
	if stopSequence != "" {
		stopReason = "stop_sequence"
	}
	if content != "" {
		blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: content})
	}
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
		for _, tc := range toolCalls {
			blocks = append(blocks, AnthropicContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Name,
				Input: tc.Parameters,
			})
		}
	}
	var stopSeqPtr *string
	if stopSequence != "" {
		stopSeqPtr = &stopSequence
	}
	return &AnthropicResponse{
		ID:           id,
		Type:         "message",
		Role:         "assistant",
		Model:        model,
		Content:      blocks,
		StopReason:   stopReason,
		StopSequence: stopSeqPtr,
		Usage:        AnthropicUsage{InputTokens: inputTokens, OutputTokens: outputTokens},
	}
}

// ─── SSE helpers ───────────────────────────────────────────────────────────

func sseData(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return fmt.Appendf(nil, "data: %s\n\n", b)
}

func anthropicSSE(eventType string, v interface{}) []byte {
	b, _ := json.Marshal(v)
	return fmt.Appendf(nil, "event: %s\ndata: %s\n\n", eventType, b)
}
