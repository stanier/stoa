package inferencegateway

import (
	"context"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
)

type contextKey int

const ctxRequestID contextKey = iota

// HookRequest carries normalized inbound request data to pre-request hooks.
type HookRequest struct {
	RequestID string
	Model     string
	Messages  []chat.Message
	Tools     []llm.Tool
}

// HookResponse carries aggregated response data to post-response hooks.
// For streaming responses this is populated after the stream drains completely
// (the response has already been written to the client); use it for logging and
// auditing, not response modification.
type HookResponse struct {
	RequestID    string
	Model        string
	Content      string
	ToolCalls    []llm.ToolUse
	InputTokens  int
	OutputTokens int
	Err          error
}

// PreRequestHook is called after request parsing, before the upstream LLM call.
// It returns a (possibly enriched) context and an optional error. Returning a
// non-nil error aborts the request with HTTP 400. The returned context is
// threaded through all subsequent hooks (ResponseTransform, PostResponse),
// making it the idiomatic way to pass per-request state — e.g. an
// anonymization map built here and consumed by a ResponseTransformHook.
type PreRequestHook func(ctx context.Context, req *HookRequest) (context.Context, error)

// ResponseTransformHook is called after the full response is collected but
// BEFORE it is written to the client. It may mutate resp.Content and
// resp.ToolCalls in place; the gateway writes those (possibly rewritten)
// values to the client. Returning a non-nil error sends HTTP 500 instead.
//
// For streaming requests, registering this hook activates a buffer-drain path:
// the entire upstream response is collected before sending anything downstream,
// and the client receives the transformed content as a compact set of synthetic
// SSE events rather than incremental chunks. Register this hook only when
// content rewriting is required (e.g. de-anonymizing PII substitutions made in
// a PreRequestHook). Use PostResponseHook for audit/logging that does not need
// to alter the response.
type ResponseTransformHook func(ctx context.Context, resp *HookResponse) error

// PostResponseHook is called after the response is fully assembled.
// For streaming responses it fires after the stream is drained.
type PostResponseHook func(ctx context.Context, resp *HookResponse)

// Hooks holds optional hook slices for the proxy server.
type Hooks struct {
	PreRequest        []PreRequestHook
	ResponseTransform []ResponseTransformHook
	PostResponse      []PostResponseHook
}

// WithHooks attaches a Hooks bundle to the server. Must be called before Handler().
func (s *Server) WithHooks(h Hooks) {
	s.hooks = h
}

func (s *Server) runPreRequest(ctx context.Context, req *HookRequest) (context.Context, error) {
	for _, h := range s.hooks.PreRequest {
		var err error
		ctx, err = h(ctx, req)
		if err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

func (s *Server) runResponseTransform(ctx context.Context, resp *HookResponse) error {
	for _, h := range s.hooks.ResponseTransform {
		if err := h(ctx, resp); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) runPostResponse(ctx context.Context, resp *HookResponse) {
	for _, h := range s.hooks.PostResponse {
		h(ctx, resp)
	}
}

func requestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxRequestID).(string)
	return v
}
