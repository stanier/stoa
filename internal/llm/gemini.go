package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/stanier/stoa/internal/chat"
)

const defaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GeminiClient implements the Client interface using the Google Gemini API.
// Auth is via an API key passed as a query parameter (?key=...).
// For Vertex AI, set baseURL to the Vertex endpoint and leave apiKey empty;
// the caller is responsible for injecting a Bearer token via the http.Client.
type GeminiClient struct {
	baseURL      string
	apiKey       string
	defaultModel string
	maxTokens    int
	httpClient   *http.Client
}

// NewGeminiClient creates a GeminiClient.
// baseURL defaults to the Google AI Studio endpoint if empty.
func NewGeminiClient(apiKey, baseURL, defaultModel string, maxTokens int) (*GeminiClient, error) {
	if baseURL == "" {
		baseURL = defaultGeminiBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	return &GeminiClient{
		baseURL:      baseURL,
		apiKey:       apiKey,
		defaultModel: defaultModel,
		maxTokens:    maxTokens,
		httpClient:   &http.Client{},
	}, nil
}

// ─── Gemini request options (passed via context) ───────────────────────────

// GeminiSafetySetting overrides the safety threshold for a harm category.
type GeminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// GeminiRequestOptions carries Gemini-specific options that callers can attach
// to a context via WithGeminiOptions.
type GeminiRequestOptions struct {
	SafetySettings   []GeminiSafetySetting
	ResponseMimeType string
	ResponseSchema   interface{}
}

type ctxGeminiOptsKey struct{}

// WithGeminiOptions attaches Gemini-specific request options to ctx.
func WithGeminiOptions(ctx context.Context, opts GeminiRequestOptions) context.Context {
	return context.WithValue(ctx, ctxGeminiOptsKey{}, opts)
}

func geminiOptionsFromContext(ctx context.Context) GeminiRequestOptions {
	v, _ := ctx.Value(ctxGeminiOptsKey{}).(GeminiRequestOptions)
	return v
}

// ─── internal Gemini JSON types ────────────────────────────────────────────

type geminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type geminiRequest struct {
	Contents          []geminiContent      `json:"contents"`
	SystemInstruction *geminiContent       `json:"systemInstruction,omitempty"`
	Tools             []geminiTool         `json:"tools,omitempty"`
	GenerationConfig  *geminiGenConfig     `json:"generationConfig,omitempty"`
	SafetySettings    []geminiSafetySetting `json:"safetySettings,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                 `json:"text,omitempty"`
	Thought          bool                   `json:"thought,omitempty"`
	FunctionCall     *geminiFunctionCall    `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type geminiFunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDecl `json:"functionDeclarations"`
}

type geminiFunctionDecl struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type geminiGenConfig struct {
	Temperature      *float64    `json:"temperature,omitempty"`
	TopP             *float64    `json:"topP,omitempty"`
	TopK             *int        `json:"topK,omitempty"`
	MaxOutputTokens  int         `json:"maxOutputTokens,omitempty"`
	StopSequences    []string    `json:"stopSequences,omitempty"`
	ResponseMimeType string      `json:"responseMimeType,omitempty"`
	ResponseSchema   interface{} `json:"responseSchema,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata geminiUsage         `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content          geminiContent           `json:"content"`
	FinishReason     string                  `json:"finishReason"`
	SafetyRatings    []geminiSafetyRating    `json:"safetyRatings,omitempty"`
	CitationMetadata *geminiCitationMetadata `json:"citationMetadata,omitempty"`
}

type geminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked,omitempty"`
}

type geminiCitationMetadata struct {
	CitationSources []geminiCitationSource `json:"citationSources,omitempty"`
}

type geminiCitationSource struct {
	StartIndex *int   `json:"startIndex,omitempty"`
	EndIndex   *int   `json:"endIndex,omitempty"`
	URI        string `json:"uri,omitempty"`
	License    string `json:"license,omitempty"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}

type geminiModelList struct {
	Models []struct {
		Name string `json:"name"` // e.g. "models/gemini-2.0-flash"
	} `json:"models"`
}

// ─── conversion helpers ────────────────────────────────────────────────────

func (c *GeminiClient) buildRequest(ctx context.Context, messages []chat.Message, tools []Tool) geminiRequest {
	req := geminiRequest{}
	opts := geminiOptionsFromContext(ctx)
	if len(opts.SafetySettings) > 0 {
		req.SafetySettings = make([]geminiSafetySetting, len(opts.SafetySettings))
		for i, s := range opts.SafetySettings {
			req.SafetySettings[i] = geminiSafetySetting{Category: s.Category, Threshold: s.Threshold}
		}
	}

	for _, msg := range messages {
		switch msg.Role {
		case chat.RoleSystem:
			req.SystemInstruction = &geminiContent{
				Parts: []geminiPart{{Text: msg.Content}},
			}
		case chat.RoleUser:
			req.Contents = append(req.Contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: msg.Content}},
			})
		case chat.RoleAssistant:
			req.Contents = append(req.Contents, geminiContent{
				Role:  "model",
				Parts: []geminiPart{{Text: msg.Content}},
			})
		case chat.RoleTool:
			// Tool results go as user-role functionResponse parts
			var respMap map[string]interface{}
			_ = json.Unmarshal([]byte(msg.Content), &respMap)
			if respMap == nil {
				respMap = map[string]interface{}{"output": msg.Content}
			}
			req.Contents = append(req.Contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResponse: &geminiFunctionResponse{
						Name:     msg.ToolName,
						Response: respMap,
					},
				}},
			})
		}
	}

	if len(tools) > 0 {
		decls := make([]geminiFunctionDecl, len(tools))
		for i, t := range tools {
			decls[i] = geminiFunctionDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			}
		}
		req.Tools = []geminiTool{{FunctionDeclarations: decls}}
	}

	return req
}

func (c *GeminiClient) applyOverrides(ctx context.Context, req *geminiRequest, maxTokens int, o *Overrides) (model string) {
	model = c.defaultModel
	if o != nil && o.Model != nil {
		model = *o.Model
	}

	mt := maxTokens
	if o != nil && o.MaxTokens != nil {
		mt = *o.MaxTokens
	}

	cfg := &geminiGenConfig{MaxOutputTokens: mt}
	if o != nil && o.Temperature != nil {
		cfg.Temperature = o.Temperature
	}
	if o != nil && o.TopP != nil {
		cfg.TopP = o.TopP
	}
	if o != nil && o.TopK != nil {
		cfg.TopK = o.TopK
	}

	opts := geminiOptionsFromContext(ctx)
	if opts.ResponseMimeType != "" {
		cfg.ResponseMimeType = opts.ResponseMimeType
	}
	if opts.ResponseSchema != nil {
		cfg.ResponseSchema = opts.ResponseSchema
	}

	req.GenerationConfig = cfg
	return model
}

// ─── HTTP helpers ──────────────────────────────────────────────────────────

func (c *GeminiClient) buildURL(model, action string) string {
	url := fmt.Sprintf("%s/models/%s:%s", c.baseURL, model, action)
	if c.apiKey != "" {
		url += "?key=" + c.apiKey
	}
	return url
}

func (c *GeminiClient) buildStreamURL(model string) string {
	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", c.baseURL, model)
	if c.apiKey != "" {
		url += "&key=" + c.apiKey
	}
	return url
}

func (c *GeminiClient) post(ctx context.Context, url string, body interface{}) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}

// ─── Client interface ──────────────────────────────────────────────────────

func (c *GeminiClient) SendMessage(ctx context.Context, messages []chat.Message, tools []Tool) (string, error) {
	o := OverridesFromContext(ctx)
	req := c.buildRequest(ctx, messages, tools)
	model := c.applyOverrides(ctx, &req, c.maxTokens, o)

	resp, err := c.post(ctx, c.buildURL(model, "generateContent"), req)
	if err != nil {
		return "", fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini %d: %s", resp.StatusCode, body)
	}

	var gr geminiResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return "", fmt.Errorf("gemini parse: %w", err)
	}
	if len(gr.Candidates) == 0 {
		return "", fmt.Errorf("gemini: no candidates")
	}

	var sb strings.Builder
	for _, part := range gr.Candidates[0].Content.Parts {
		if !part.Thought && part.FunctionCall == nil {
			sb.WriteString(part.Text)
		}
	}
	return sb.String(), nil
}

func (c *GeminiClient) StreamMessage(ctx context.Context, messages []chat.Message, tools []Tool) (<-chan StreamChunk, error) {
	o := OverridesFromContext(ctx)
	req := c.buildRequest(ctx, messages, tools)
	model := c.applyOverrides(ctx, &req, c.maxTokens, o)

	resp, err := c.post(ctx, c.buildStreamURL(model), req)
	if err != nil {
		return nil, fmt.Errorf("gemini stream request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("gemini %d: %s", resp.StatusCode, body)
	}

	out := make(chan StreamChunk, 10)

	go func() {
		defer close(out)
		defer resp.Body.Close()

		var inputTokens, outputTokens int
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "" || data == "[DONE]" {
				continue
			}

			var gr geminiResponse
			if err := json.Unmarshal([]byte(data), &gr); err != nil {
				out <- StreamChunk{Error: fmt.Errorf("gemini parse chunk: %w", err)}
				return
			}

			inputTokens = gr.UsageMetadata.PromptTokenCount
			outputTokens += gr.UsageMetadata.CandidatesTokenCount

			for _, cand := range gr.Candidates {
				for _, part := range cand.Content.Parts {
					select {
					case <-ctx.Done():
						out <- StreamChunk{Error: ctx.Err()}
						return
					default:
					}

					if part.FunctionCall != nil {
						args, _ := json.Marshal(part.FunctionCall.Args)
						out <- StreamChunk{ToolUse: &ToolUse{
							ID:         makeGeminiToolID(part.FunctionCall.Name),
							Name:       part.FunctionCall.Name,
							Parameters: args,
						}}
					} else if part.Thought {
						out <- StreamChunk{Thinking: part.Text}
					} else if part.Text != "" {
						out <- StreamChunk{Content: part.Text}
					}
				}
			}
		}

		if err := scanner.Err(); err != nil {
			out <- StreamChunk{Error: fmt.Errorf("gemini stream read: %w", err)}
			return
		}

		out <- StreamChunk{Done: true, InputTokens: inputTokens, OutputTokens: outputTokens}
	}()

	return out, nil
}

func (c *GeminiClient) ListModels(ctx context.Context) ([]string, error) {
	url := c.baseURL + "/models"
	if c.apiKey != "" {
		url += "?key=" + c.apiKey
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini list models: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini list models %d: %s", resp.StatusCode, body)
	}
	var list geminiModelList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("gemini list models parse: %w", err)
	}
	ids := make([]string, 0, len(list.Models))
	for _, m := range list.Models {
		// Strip "models/" prefix to return bare IDs like "gemini-2.0-flash"
		ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
	}
	return ids, nil
}

func makeGeminiToolID(name string) string {
	return fmt.Sprintf("call_%s_%d", name, uniqueCounter())
}

var geminiCounter atomic.Int64

func uniqueCounter() int64 {
	return geminiCounter.Add(1)
}
