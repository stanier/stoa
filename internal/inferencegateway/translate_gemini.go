package inferencegateway

import (
	"encoding/json"
	"fmt"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
)

// ─── Gemini → internal ─────────────────────────────────────────────────────

// ParseGeminiMessages converts Gemini contents + systemInstruction to internal format.
func ParseGeminiMessages(contents []GeminiContent, system *GeminiContent) ([]chat.Message, error) {
	var result []chat.Message

	if system != nil {
		var sb string
		for _, p := range system.Parts {
			sb += p.Text
		}
		if sb != "" {
			result = append(result, chat.NewSystemMessage(sb))
		}
	}

	for _, c := range contents {
		text, toolUseID, toolName, toolCall, err := extractGeminiParts(c.Parts)
		if err != nil {
			return nil, fmt.Errorf("content role %q: %w", c.Role, err)
		}

		switch c.Role {
		case "user":
			if toolUseID != "" {
				result = append(result, chat.NewToolResultMessage(toolName, toolUseID, text, true))
			} else {
				result = append(result, chat.NewUserMessage(text))
			}
		case "model":
			msg := chat.NewAssistantMessage(text)
			if toolCall != nil {
				_ = toolCall // tool calls in assistant messages are represented as text for now
			}
			result = append(result, msg)
		}
	}
	return result, nil
}

// ParseGeminiTools converts Gemini tool declarations to internal llm.Tool format.
func ParseGeminiTools(tools []GeminiTool) []llm.Tool {
	var result []llm.Tool
	for _, t := range tools {
		for _, decl := range t.FunctionDeclarations {
			result = append(result, llm.Tool{
				Name:        decl.Name,
				Description: decl.Description,
				InputSchema: decl.Parameters,
			})
		}
	}
	return result
}

// extractGeminiParts flattens a parts array into text + optional tool metadata.
// Returns (text, toolUseID, toolName, toolCall, error).
func extractGeminiParts(parts []GeminiPart) (string, string, string, *GeminiFunctionCall, error) {
	var text string
	var toolUseID, toolName string
	var toolCall *GeminiFunctionCall

	for _, p := range parts {
		switch {
		case p.InlineData != nil:
			return "", "", "", nil, fmt.Errorf("inlineData parts (mime type %q) are not yet supported by this proxy", p.InlineData.MimeType)
		case p.FunctionCall != nil:
			toolCall = p.FunctionCall
		case p.FunctionResponse != nil:
			toolName = p.FunctionResponse.Name
			// Use name as ID since Gemini doesn't provide a separate tool use ID
			toolUseID = p.FunctionResponse.Name
			raw, _ := json.Marshal(p.FunctionResponse.Response)
			text += string(raw)
		case p.Text != "":
			text += p.Text
		}
	}
	return text, toolUseID, toolName, toolCall, nil
}

// ─── internal StreamChunk → Gemini SSE ────────────────────────────────────

// MakeGeminiStreamChunk formats a llm.StreamChunk as a Gemini SSE data line.
// Returns nil when there is nothing to emit.
func MakeGeminiStreamChunk(chunk llm.StreamChunk, model string, inputTokens *int) []byte {
	if chunk.Error != nil {
		return nil
	}

	resp := GeminiResponse{}

	if chunk.Done {
		in := chunk.InputTokens
		if inputTokens != nil {
			in = *inputTokens
		}
		resp.UsageMetadata = GeminiUsage{
			PromptTokenCount:     in,
			CandidatesTokenCount: chunk.OutputTokens,
			TotalTokenCount:      in + chunk.OutputTokens,
		}
		resp.Candidates = []GeminiCandidate{{
			Content:      GeminiContent{Role: "model", Parts: []GeminiPart{}},
			FinishReason: "STOP",
		}}
		b, _ := json.Marshal(resp)
		return fmt.Appendf(nil, "data: %s\n\n", b)
	}

	if chunk.Content != "" {
		resp.Candidates = []GeminiCandidate{{
			Content: GeminiContent{
				Role:  "model",
				Parts: []GeminiPart{{Text: chunk.Content}},
			},
		}}
		b, _ := json.Marshal(resp)
		return fmt.Appendf(nil, "data: %s\n\n", b)
	}

	if chunk.Thinking != "" {
		resp.Candidates = []GeminiCandidate{{
			Content: GeminiContent{
				Role:  "model",
				Parts: []GeminiPart{{Text: chunk.Thinking, Thought: true}},
			},
		}}
		b, _ := json.Marshal(resp)
		return fmt.Appendf(nil, "data: %s\n\n", b)
	}

	if chunk.ToolUse != nil {
		var args json.RawMessage = chunk.ToolUse.Parameters
		resp.Candidates = []GeminiCandidate{{
			Content: GeminiContent{
				Role: "model",
				Parts: []GeminiPart{{
					FunctionCall: &GeminiFunctionCall{
						Name: chunk.ToolUse.Name,
						Args: args,
					},
				}},
			},
			FinishReason: "TOOL_CALLS",
		}}
		b, _ := json.Marshal(resp)
		return fmt.Appendf(nil, "data: %s\n\n", b)
	}

	return nil
}

// MakeGeminiResponse builds a complete non-streaming Gemini response.
func MakeGeminiResponse(content string, toolCalls []llm.ToolUse, model string, inputTokens, outputTokens int) *GeminiResponse {
	var parts []GeminiPart
	finishReason := "STOP"

	if content != "" {
		parts = append(parts, GeminiPart{Text: content})
	}
	if len(toolCalls) > 0 {
		finishReason = "TOOL_CALLS"
		for _, tc := range toolCalls {
			parts = append(parts, GeminiPart{
				FunctionCall: &GeminiFunctionCall{
					Name: tc.Name,
					Args: tc.Parameters,
				},
			})
		}
	}

	return &GeminiResponse{
		Candidates: []GeminiCandidate{{
			Content:      GeminiContent{Role: "model", Parts: parts},
			FinishReason: finishReason,
		}},
		UsageMetadata: GeminiUsage{
			PromptTokenCount:     inputTokens,
			CandidatesTokenCount: outputTokens,
			TotalTokenCount:      inputTokens + outputTokens,
		},
	}
}
