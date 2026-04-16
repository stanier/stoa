package agentd

import (
	"context"
	"encoding/json"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/chat"
)

// LoopHooks holds optional callbacks invoked at key points in the agentic loop.
// All hooks are called synchronously in the loop goroutine; keep them fast.
// Any nil slice entry is silently skipped.
type LoopHooks struct {
	// PreLLM is called before each LLM call with the current message snapshot.
	PreLLM []func(ctx context.Context, sessionID string, messages []chat.Message)

	// PostLLM is called after each complete LLM response, before tool dispatch.
	// content and thinking are the accumulated text for this iteration.
	PostLLM []func(ctx context.Context, sessionID string, content string, thinking string)

	// PreTool is called before each tool execution.
	PreTool []func(ctx context.Context, sessionID string, toolName string, toolUseID string, params json.RawMessage)

	// PostTool is called after each tool execution.
	// result is the (possibly paged) text returned to the LLM; err is non-nil
	// only on fatal errors (not tool-level error strings).
	PostTool []func(ctx context.Context, sessionID string, toolName string, toolUseID string, result string, err error)
}

// WithLoopHooks attaches a LoopHooks bundle to the server.
func (s *Server) WithLoopHooks(h LoopHooks) {
	s.loopHooks = h
}

func (s *Server) runPreLLM(ctx context.Context, sessionID string, messages []chat.Message) {
	for _, h := range s.loopHooks.PreLLM {
		h(ctx, sessionID, messages)
	}
}

func (s *Server) runPostLLM(ctx context.Context, sessionID string, content, thinking string) {
	for _, h := range s.loopHooks.PostLLM {
		h(ctx, sessionID, content, thinking)
	}
}

func (s *Server) runPreTool(ctx context.Context, sessionID string, toolName, toolUseID string, params json.RawMessage) {
	for _, h := range s.loopHooks.PreTool {
		h(ctx, sessionID, toolName, toolUseID, params)
	}
}

func (s *Server) runPostTool(ctx context.Context, sessionID string, toolName, toolUseID string, result string, err error) {
	for _, h := range s.loopHooks.PostTool {
		h(ctx, sessionID, toolName, toolUseID, result, err)
	}
}

// CompactionHooks holds optional callbacks invoked around compaction events.
// All hooks are called synchronously; keep them fast.
// Any nil slice entry is silently skipped.
type CompactionHooks struct {
	// PostCompact is called after a successful auto-compaction.
	// tokensBefore is the count that triggered compaction; tokensAfter is the
	// estimated count of the compacted context.
	PostCompact []func(ctx context.Context, sessionID string, userID string, tokensBefore int, tokensAfter int)
}

// WithCompactionHooks attaches a CompactionHooks bundle to the server.
func (s *Server) WithCompactionHooks(h CompactionHooks) {
	s.compactionHooks = h
}

func (s *Server) runPostCompact(ctx context.Context, sessionID string, userID string, tokensBefore int, tokensAfter int) {
	for _, h := range s.compactionHooks.PostCompact {
		h(ctx, sessionID, userID, tokensBefore, tokensAfter)
	}
}

// SessionHooks holds optional callbacks invoked at session lifecycle events.
// All hooks are called synchronously; keep them fast.
type SessionHooks struct {
	// PostCreate is called after a session is successfully created and persisted.
	PostCreate []func(ctx context.Context, sessionID string, userID string, source string)

	// PostDelete is called after a session is deleted.
	PostDelete []func(ctx context.Context, sessionID string, userID string)

	// PostClear is called after a session's message history is cleared.
	PostClear []func(ctx context.Context, sessionID string, userID string)
}

// WithSessionHooks attaches a SessionHooks bundle to the server.
func (s *Server) WithSessionHooks(h SessionHooks) {
	s.sessionHooks = h
}

func (s *Server) runPostCreate(ctx context.Context, sessionID string, userID string, source string) {
	for _, h := range s.sessionHooks.PostCreate {
		h(ctx, sessionID, userID, source)
	}
}

func (s *Server) runPostDelete(ctx context.Context, sessionID string, userID string) {
	for _, h := range s.sessionHooks.PostDelete {
		h(ctx, sessionID, userID)
	}
}

func (s *Server) runPostClear(ctx context.Context, sessionID string, userID string) {
	for _, h := range s.sessionHooks.PostClear {
		h(ctx, sessionID, userID)
	}
}

// EventHooks holds optional callbacks invoked before each outbound WebSocket event.
// Returning a non-nil error from any PreSend hook suppresses the event.
type EventHooks struct {
	// PreSend is called before each event is written to the client.
	// Returning a non-nil error suppresses the event.
	PreSend []func(ctx context.Context, sessionID string, event *agentproto.Event) error
}

// WithEventHooks attaches an EventHooks bundle to the server.
func (s *Server) WithEventHooks(h EventHooks) {
	s.eventHooks = h
}

// runPreSend calls all PreSend hooks and returns the first non-nil error,
// signaling the caller to suppress the event.
func (s *Server) runPreSend(ctx context.Context, sessionID string, event *agentproto.Event) error {
	for _, h := range s.eventHooks.PreSend {
		if err := h(ctx, sessionID, event); err != nil {
			return err
		}
	}
	return nil
}
