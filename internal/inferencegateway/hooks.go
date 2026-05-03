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
// Returning a non-nil error aborts the request; the proxy returns a 400 to the
// caller with the error message.
type PreRequestHook func(ctx context.Context, req *HookRequest) error

// PostResponseHook is called after the response is fully assembled.
// For streaming responses it fires after the stream is drained.
type PostResponseHook func(ctx context.Context, resp *HookResponse)

// Hooks holds optional pre/post hook slices for the proxy server.
type Hooks struct {
	PreRequest   []PreRequestHook
	PostResponse []PostResponseHook
}

// WithHooks attaches a Hooks bundle to the server. Must be called before Handler().
func (s *Server) WithHooks(h Hooks) {
	s.hooks = h
}

func (s *Server) runPreRequest(ctx context.Context, req *HookRequest) error {
	for _, h := range s.hooks.PreRequest {
		if err := h(ctx, req); err != nil {
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
