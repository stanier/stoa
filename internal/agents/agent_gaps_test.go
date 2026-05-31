package agents

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/skills"
)

// stubTool satisfies the skills.Tool interface for testing executeTool.
type stubTool struct {
	name    string
	result  skills.ToolResult
	execErr error
}

func (s *stubTool) Name() string              { return s.name }
func (s *stubTool) Description() string       { return "stub" }
func (s *stubTool) Schema() skills.ToolSchema { return skills.ToolSchema{Type: "object"} }
func (s *stubTool) Execute(_ context.Context, _ json.RawMessage) (skills.ToolResult, error) {
	return s.result, s.execErr
}

func newAgentForTest(llmClient llm.Client) *BaseAgent {
	reg := skills.NewRegistry()
	return NewBaseAgent("test-id", &mockPersona{name: "test"}, "test-agent", llmClient, reg)
}

// --- SetConversation ---

func TestBaseAgent_SetConversation(t *testing.T) {
	a := newAgentForTest(&mockLLMClient{})
	msgs := []chat.Message{
		chat.NewUserMessage("hello"),
		chat.NewAssistantMessage("world"),
	}
	a.SetConversation(msgs)

	a.mu.RLock()
	got := len(a.conversation)
	a.mu.RUnlock()

	if got != 2 {
		t.Errorf("conversation len = %d, want 2", got)
	}
}

func TestBaseAgent_SetConversation_Nil(t *testing.T) {
	a := newAgentForTest(&mockLLMClient{})
	a.SetConversation(nil)

	a.mu.RLock()
	got := a.conversation
	a.mu.RUnlock()

	if got != nil {
		t.Errorf("expected nil conversation, got %v", got)
	}
}

// --- executeTool ---

func TestBaseAgent_executeTool_KnownTool(t *testing.T) {
	reg := skills.NewRegistry()
	tool := &stubTool{name: "mytool", result: skills.ToolResult{Success: true, Content: "done"}}
	reg.RegisterTool(tool)

	a := NewBaseAgent("x", &mockPersona{name: "p"}, "agent", &mockLLMClient{}, reg)

	result, err := a.executeTool(context.Background(), llm.ToolUse{
		Name:       "mytool",
		Parameters: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected Success=true")
	}
	if result.Content != "done" {
		t.Errorf("Content = %q, want \"done\"", result.Content)
	}
}

func TestBaseAgent_executeTool_UnknownTool(t *testing.T) {
	a := newAgentForTest(&mockLLMClient{})
	_, err := a.executeTool(context.Background(), llm.ToolUse{Name: "no_such_tool"})
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

// --- Pause / Resume ---

func TestBaseAgent_PauseResume(t *testing.T) {
	// pauseRead signals that the inner chunk loop received the pause signal.
	pauseRead := make(chan struct{})

	dripLLM := &mockLLMClient{
		streamFunc: func(ctx context.Context, _ []chat.Message, _ []llm.Tool) (<-chan llm.StreamChunk, error) {
			ch := make(chan llm.StreamChunk)
			go func() {
				defer close(ch)
				// Drip chunks slowly so the inner loop runs and can receive pauseChan.
				for i := 0; i < 20; i++ {
					select {
					case <-ctx.Done():
						return
					case <-time.After(50 * time.Millisecond):
					}
					select {
					case ch <- llm.StreamChunk{Content: "word"}:
					case <-ctx.Done():
						return
					}
				}
				ch <- llm.StreamChunk{Content: "done", Done: true}
			}()
			return ch, nil
		},
	}
	_ = pauseRead

	a := newAgentForTest(dripLLM)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := a.Execute(ctx, Task{ID: "t1", Description: "drip task"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	// Wait for the first chunk to flow so the inner select is live.
	time.Sleep(100 * time.Millisecond)

	if err := a.Pause(); err != nil {
		t.Fatalf("Pause error: %v", err)
	}
	if a.Status() != StatusPaused {
		t.Errorf("status after Pause = %v, want Paused", a.Status())
	}

	if err := a.Resume(); err != nil {
		t.Fatalf("Resume error: %v", err)
	}
	if a.Status() != StatusRunning {
		t.Errorf("status after Resume = %v, want Running", a.Status())
	}

	_ = a.Stop()
}

func TestBaseAgent_Pause_WhenIdle_Errors(t *testing.T) {
	a := newAgentForTest(&mockLLMClient{})
	err := a.Pause()
	if err == nil {
		t.Error("expected error when pausing idle agent")
	}
}

func TestBaseAgent_Resume_WhenNotPaused_Errors(t *testing.T) {
	a := newAgentForTest(&mockLLMClient{})
	err := a.Resume()
	if err == nil {
		t.Error("expected error when resuming non-paused agent")
	}
}

// --- processIncomingMessage ---

func TestBaseAgent_SendMessage_QueuesFull(t *testing.T) {
	a := newAgentForTest(&mockLLMClient{})

	// Fill the incoming message queue (buffer size is defined in NewBaseAgent).
	for i := 0; i < 100; i++ {
		_ = a.SendMessage(Message{Type: MessageTypeQuestion, Content: "ping"})
	}

	// The next send to a full queue should return an error.
	err := a.SendMessage(Message{Type: MessageTypeQuestion, Content: "overflow"})
	if err == nil {
		t.Error("expected error when incoming message queue is full")
	}
}

func TestBaseAgent_processIncomingMessage_NonQuestion(t *testing.T) {
	a := newAgentForTest(&mockLLMClient{})
	ctx := context.Background()

	beforeLen := len(a.conversation)
	// A non-question message should not append to conversation.
	a.processIncomingMessage(ctx, Message{Type: MessageTypeProgress, Content: "status"})

	if len(a.conversation) != beforeLen {
		t.Error("non-question message should not modify conversation")
	}
}

// --- GetID / GetName / GetPersona / String ---

func TestBaseAgent_Accessors(t *testing.T) {
	a := NewBaseAgent("my-id", &mockPersona{name: "solver"}, "solver-1", &mockLLMClient{}, skills.NewRegistry())

	if a.GetID() != "my-id" {
		t.Errorf("GetID = %q, want \"my-id\"", a.GetID())
	}
	if a.GetName() != "solver-1" {
		t.Errorf("GetName = %q, want \"solver-1\"", a.GetName())
	}
	if a.GetPersona() == nil {
		t.Error("GetPersona returned nil")
	}
	if a.String() == "" {
		t.Error("String() returned empty string")
	}
}
