package llm

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
)

// AnthropicRawMessages carries the original Anthropic-format message JSON so
// that cache_control blocks can be forwarded verbatim to the Anthropic SDK
// without going through the lossy chat.Message representation.
//
// Populate this with WithAnthropicRawMessages and retrieve it inside the
// Anthropic client with anthropicRawFromContext.
type AnthropicRawMessages struct {
	// Messages is the raw JSON for each element of the inbound messages array.
	Messages []json.RawMessage
	// System is the raw JSON for the system field (may be a string or an
	// array of content blocks).  Each element in the slice is one independent
	// JSON value; currently at most one element is expected.
	System []json.RawMessage
}

type ctxAnthropicRawKey struct{}

// WithAnthropicRawMessages attaches raw Anthropic message JSON to ctx so that
// cache_control fields are forwarded to the Anthropic SDK.
func WithAnthropicRawMessages(ctx context.Context, raw AnthropicRawMessages) context.Context {
	return context.WithValue(ctx, ctxAnthropicRawKey{}, raw)
}

// anthropicRawFromContext retrieves the raw messages attached by
// WithAnthropicRawMessages, or returns the zero value and false.
func anthropicRawFromContext(ctx context.Context) (AnthropicRawMessages, bool) {
	v, ok := ctx.Value(ctxAnthropicRawKey{}).(AnthropicRawMessages)
	return v, ok
}

// ─── rebuild helpers ──────────────────────────────────────────────────────────

// rawBlock is the minimal shape of an Anthropic content block for rebuilding.
type rawBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	CacheControl *rawCacheCtrl   `json:"cache_control,omitempty"`
	// tool_use block fields
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result block fields
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // string or []block
	IsError   *bool           `json:"is_error,omitempty"`
}

type rawCacheCtrl struct {
	Type string `json:"type"` // "ephemeral"
}

// rawMessage is the minimal shape of a message for rebuilding.
type rawMessage struct {
	Role    string            `json:"role"`
	Content json.RawMessage   `json:"content"`
}

// rebuildAnthropicMessages converts a slice of raw message JSON into
// []anthropic.MessageParam, preserving cache_control on all handled block types.
func rebuildAnthropicMessages(rawMsgs []json.RawMessage) []anthropic.MessageParam {
	result := make([]anthropic.MessageParam, 0, len(rawMsgs))
	for _, raw := range rawMsgs {
		var m rawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}

		role := anthropic.MessageParamRoleUser
		if m.Role == "assistant" {
			role = anthropic.MessageParamRoleAssistant
		}

		blocks := parseContentBlocks(m.Content)
		if len(blocks) == 0 {
			continue
		}

		result = append(result, anthropic.MessageParam{
			Role:    role,
			Content: blocks,
		})
	}
	return result
}

// rebuildAnthropicSystem converts raw system JSON (string or []block) into
// []anthropic.TextBlockParam, preserving cache_control.
func rebuildAnthropicSystem(rawSystem []json.RawMessage) []anthropic.TextBlockParam {
	var result []anthropic.TextBlockParam
	for _, raw := range rawSystem {
		// Try array of blocks.
		var blocks []json.RawMessage
		if err := json.Unmarshal(raw, &blocks); err == nil {
			for _, b := range blocks {
				var block rawBlock
				if err2 := json.Unmarshal(b, &block); err2 != nil || block.Type != "text" {
					continue
				}
				param := anthropic.TextBlockParam{Text: block.Text}
				if block.CacheControl != nil && block.CacheControl.Type == "ephemeral" {
					param.CacheControl = anthropic.CacheControlEphemeralParam{Type: "ephemeral"}
				}
				result = append(result, param)
			}
			continue
		}
		// Try plain string.
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			result = append(result, anthropic.TextBlockParam{Text: s})
		}
	}
	return result
}

// parseContentBlocks normalises a content JSON value (string or []block) into
// []anthropic.ContentBlockParamUnion, preserving cache_control on text, tool_use,
// and tool_result blocks.
func parseContentBlocks(content json.RawMessage) []anthropic.ContentBlockParamUnion {
	if len(content) == 0 {
		return nil
	}

	// Try array of blocks.
	var rawBlocks []json.RawMessage
	if err := json.Unmarshal(content, &rawBlocks); err == nil {
		result := make([]anthropic.ContentBlockParamUnion, 0, len(rawBlocks))
		for _, b := range rawBlocks {
			var block rawBlock
			if err2 := json.Unmarshal(b, &block); err2 != nil {
				continue
			}
			switch block.Type {
			case "text":
				param := &anthropic.TextBlockParam{Text: block.Text}
				if block.CacheControl != nil && block.CacheControl.Type == "ephemeral" {
					param.CacheControl = anthropic.CacheControlEphemeralParam{Type: "ephemeral"}
				}
				result = append(result, anthropic.ContentBlockParamUnion{OfText: param})
			case "tool_use":
				var input any
				if len(block.Input) > 0 {
					_ = json.Unmarshal(block.Input, &input)
				}
				param := &anthropic.ToolUseBlockParam{
					ID:    block.ID,
					Name:  block.Name,
					Input: input,
				}
				if block.CacheControl != nil && block.CacheControl.Type == "ephemeral" {
					param.CacheControl = anthropic.CacheControlEphemeralParam{Type: "ephemeral"}
				}
				result = append(result, anthropic.ContentBlockParamUnion{OfToolUse: param})
			case "tool_result":
				param := &anthropic.ToolResultBlockParam{
					ToolUseID: block.ToolUseID,
				}
				if block.IsError != nil && *block.IsError {
					param.IsError = anthropic.Bool(true)
				}
				if block.CacheControl != nil && block.CacheControl.Type == "ephemeral" {
					param.CacheControl = anthropic.CacheControlEphemeralParam{Type: "ephemeral"}
				}
				// Resolve inner content: string or []block.
				if len(block.Content) > 0 {
					var s string
					if err2 := json.Unmarshal(block.Content, &s); err2 == nil {
						param.Content = []anthropic.ToolResultBlockParamContentUnion{
							{OfText: &anthropic.TextBlockParam{Text: s}},
						}
					} else {
						var innerBlocks []json.RawMessage
						if err2 := json.Unmarshal(block.Content, &innerBlocks); err2 == nil {
							for _, ib := range innerBlocks {
								var inner rawBlock
								if err3 := json.Unmarshal(ib, &inner); err3 != nil || inner.Type != "text" {
									continue
								}
								tp := &anthropic.TextBlockParam{Text: inner.Text}
								if inner.CacheControl != nil && inner.CacheControl.Type == "ephemeral" {
									tp.CacheControl = anthropic.CacheControlEphemeralParam{Type: "ephemeral"}
								}
								param.Content = append(param.Content, anthropic.ToolResultBlockParamContentUnion{OfText: tp})
							}
						}
					}
				}
				result = append(result, anthropic.ContentBlockParamUnion{OfToolResult: param})
			default:
				// Unknown block types are skipped to avoid malformed params.
				continue
			}
		}
		return result
	}

	// Try plain string.
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return []anthropic.ContentBlockParamUnion{
			{OfText: &anthropic.TextBlockParam{Text: s}},
		}
	}

	return nil
}
