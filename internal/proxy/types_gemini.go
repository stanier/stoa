package proxy

import "encoding/json"

// ─── Gemini wire types ─────────────────────────────────────────────────────
// These mirror the Google Generative Language API v1beta shapes so the proxy
// can accept Gemini-format requests inbound (not just forward to Gemini upstreams).

// GeminiRequest is the JSON body for POST /v1beta/models/{model}:generateContent.
type GeminiRequest struct {
	Contents          []GeminiContent    `json:"contents"`
	SystemInstruction *GeminiContent     `json:"systemInstruction,omitempty"`
	Tools             []GeminiTool       `json:"tools,omitempty"`
	GenerationConfig  *GeminiGenConfig   `json:"generationConfig,omitempty"`
	SafetySettings    []GeminiSafetySetting `json:"safetySettings,omitempty"`
	Stream            bool               `json:"stream,omitempty"` // not a real API field; used internally
}

// GeminiSafetySetting overrides the safety threshold for a harm category.
type GeminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// GeminiContent is one turn in the conversation.
type GeminiContent struct {
	Role  string       `json:"role,omitempty"` // "user" or "model"
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart is one element of a content's parts array.
type GeminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	FunctionCall     *GeminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *GeminiInlineData       `json:"inlineData,omitempty"`
}

// GeminiFunctionCall is a model-requested tool invocation.
type GeminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// GeminiFunctionResponse is a tool result provided back to the model.
type GeminiFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

// GeminiInlineData carries inline binary data (images, etc.) — parsed but not proxied.
type GeminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

// GeminiTool holds a list of function declarations.
type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDecl `json:"functionDeclarations"`
}

// GeminiFunctionDecl describes one callable function.
type GeminiFunctionDecl struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

// GeminiGenConfig holds generation parameters.
type GeminiGenConfig struct {
	Temperature      *float64    `json:"temperature,omitempty"`
	TopP             *float64    `json:"topP,omitempty"`
	TopK             *int        `json:"topK,omitempty"`
	MaxOutputTokens  *int        `json:"maxOutputTokens,omitempty"`
	StopSequences    []string    `json:"stopSequences,omitempty"`
	ResponseMimeType string      `json:"responseMimeType,omitempty"`
	ResponseSchema   interface{} `json:"responseSchema,omitempty"`
}

// GeminiResponse is the non-streaming response body.
type GeminiResponse struct {
	Candidates    []GeminiCandidate `json:"candidates"`
	UsageMetadata GeminiUsage       `json:"usageMetadata"`
}

// GeminiCandidate is one generated response candidate.
type GeminiCandidate struct {
	Content          GeminiContent           `json:"content"`
	FinishReason     string                  `json:"finishReason"` // "STOP", "MAX_TOKENS", "TOOL_CALLS", etc.
	Index            int                     `json:"index"`
	SafetyRatings    []GeminiSafetyRating    `json:"safetyRatings,omitempty"`
	CitationMetadata *GeminiCitationMetadata `json:"citationMetadata,omitempty"`
	// TODO: wire SafetyRatings and CitationMetadata through from upstream responses
	// (requires surfacing them through the streaming layer or a separate response path)
}

// GeminiSafetyRating is a per-category safety assessment returned by the upstream.
type GeminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked,omitempty"`
}

// GeminiCitationMetadata holds citation sources for grounded responses.
type GeminiCitationMetadata struct {
	CitationSources []GeminiCitationSource `json:"citationSources,omitempty"`
}

// GeminiCitationSource is one source citation from the upstream.
type GeminiCitationSource struct {
	StartIndex *int   `json:"startIndex,omitempty"`
	EndIndex   *int   `json:"endIndex,omitempty"`
	URI        string `json:"uri,omitempty"`
	License    string `json:"license,omitempty"`
}

// GeminiUsage holds token counts.
type GeminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}
