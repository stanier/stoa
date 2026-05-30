package agents

import (
	"context"
	"testing"
	"time"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/prompts"
	"github.com/stanier/stoa/internal/skills"
)

// mockLLMClient for testing
type mockLLMClient struct {
	streamFunc func(ctx context.Context, messages []chat.Message, tools []llm.Tool) (<-chan llm.StreamChunk, error)
}

func (m *mockLLMClient) SendMessage(ctx context.Context, messages []chat.Message, tools []llm.Tool) (string, error) {
	return "mock response", nil
}

func (m *mockLLMClient) ListModels(_ context.Context) ([]string, error) { return nil, nil }
func (m *mockLLMClient) ListModelInfo(_ context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}

func (m *mockLLMClient) StreamMessage(ctx context.Context, messages []chat.Message, tools []llm.Tool) (<-chan llm.StreamChunk, error) {
	if m.streamFunc != nil {
		return m.streamFunc(ctx, messages, tools)
	}

	ch := make(chan llm.StreamChunk, 1)
	go func() {
		ch <- llm.StreamChunk{
			Content: "Mock agent response",
			Done:    true,
		}
		close(ch)
	}()
	return ch, nil
}

// mockPersona for testing
type mockPersona struct {
	name string
}

func (m *mockPersona) Name() string                                     { return m.name }
func (m *mockPersona) Description() string                              { return "Mock persona" }
func (m *mockPersona) SystemPrompt() string                             { return "You are a test agent" }
func (m *mockPersona) SystemPromptTemplate() string                     { return "You are a test agent" }
func (m *mockPersona) RenderSystemPrompt(_ *prompts.TemplateData) (string, error) {
	return "You are a test agent", nil
}
func (m *mockPersona) Capabilities() []string                           { return []string{"test"} }
func (m *mockPersona) AvailableTools() []string                         { return []string{} }
func (m *mockPersona) MaxConcurrentTasks() int                          { return 1 }
func (m *mockPersona) ThinkingStyle() ThinkingStyle                     { return ThinkingStyleFast }
func (m *mockPersona) CommunicationPreference() CommunicationPreference { return CommunicationTerse }

func TestBaseAgent_Lifecycle(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestAgent"}

	agent := NewBaseAgent("test-1", persona, "TestAgent-1", mockLLM, mockReg)

	// Test initial state
	if agent.Status() != StatusIdle {
		t.Errorf("expected initial status to be Idle, got %v", agent.Status())
	}

	if agent.Progress() != 0.0 {
		t.Errorf("expected initial progress to be 0.0, got %f", agent.Progress())
	}

	// Test ID and name
	if agent.ID() != "test-1" {
		t.Errorf("expected ID 'test-1', got '%s'", agent.ID())
	}

	if agent.Name() != "TestAgent-1" {
		t.Errorf("expected name 'TestAgent-1', got '%s'", agent.Name())
	}

	// Test persona
	if agent.Persona().Name() != "TestAgent" {
		t.Errorf("expected persona 'TestAgent', got '%s'", agent.Persona().Name())
	}
}

func TestBaseAgent_Execute(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestAgent"}

	agent := NewBaseAgent("test-1", persona, "TestAgent-1", mockLLM, mockReg)

	task := Task{
		ID:          "task-1",
		Description: "Test task",
		Priority:    PriorityNormal,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	msgChan, err := agent.Execute(ctx, task)
	if err != nil {
		t.Fatalf("failed to execute task: %v", err)
	}

	// Check status changed to running
	if agent.Status() != StatusRunning {
		t.Errorf("expected status to be Running after Execute, got %v", agent.Status())
	}

	// Collect messages
	messages := []Message{}
	timeout := time.After(3 * time.Second)
	done := false

	for !done {
		select {
		case msg, ok := <-msgChan:
			if !ok {
				done = true
				break
			}
			messages = append(messages, msg)
			if msg.Type == MessageTypeResult {
				done = true
			}
		case <-timeout:
			t.Fatal("timeout waiting for messages")
		}
	}

	// Should have at least thinking and result messages
	if len(messages) < 2 {
		t.Errorf("expected at least 2 messages, got %d", len(messages))
	}

	// Check for thinking message
	foundThinking := false
	foundResult := false
	for _, msg := range messages {
		if msg.Type == MessageTypeThinking {
			foundThinking = true
		}
		if msg.Type == MessageTypeResult {
			foundResult = true
		}
	}

	if !foundThinking {
		t.Error("expected to find a thinking message")
	}

	if !foundResult {
		t.Error("expected to find a result message")
	}

	// Give time for goroutine to finish
	time.Sleep(100 * time.Millisecond)

	// Check final status
	if agent.Status() != StatusCompleted {
		t.Errorf("expected final status to be Completed, got %v", agent.Status())
	}
}

func TestBaseAgent_Stop(t *testing.T) {
	mockLLM := &mockLLMClient{
		streamFunc: func(ctx context.Context, messages []chat.Message, tools []llm.Tool) (<-chan llm.StreamChunk, error) {
			ch := make(chan llm.StreamChunk)
			go func() {
				// Stream slowly to allow stop
				for i := 0; i < 10; i++ {
					time.Sleep(100 * time.Millisecond)
					ch <- llm.StreamChunk{Content: "chunk"}
				}
				close(ch)
			}()
			return ch, nil
		},
	}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestAgent"}

	agent := NewBaseAgent("test-1", persona, "TestAgent-1", mockLLM, mockReg)

	task := Task{
		ID:          "task-1",
		Description: "Long running task",
		Priority:    PriorityNormal,
	}

	ctx := context.Background()
	_, err := agent.Execute(ctx, task)
	if err != nil {
		t.Fatalf("failed to execute task: %v", err)
	}

	// Wait a bit for task to start
	time.Sleep(50 * time.Millisecond)

	// Stop the agent
	err = agent.Stop()
	if err != nil {
		t.Fatalf("failed to stop agent: %v", err)
	}

	// Check status
	if agent.Status() != StatusKilled {
		t.Errorf("expected status to be Killed after Stop, got %v", agent.Status())
	}
}

func TestBaseAgent_CurrentTask(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestAgent"}

	agent := NewBaseAgent("test-1", persona, "TestAgent-1", mockLLM, mockReg)

	// Initially no task
	if agent.CurrentTask() != nil {
		t.Error("expected CurrentTask to be nil initially")
	}

	task := Task{
		ID:          "task-1",
		Description: "Test task",
		Priority:    PriorityHigh,
	}

	ctx := context.Background()
	_, err := agent.Execute(ctx, task)
	if err != nil {
		t.Fatalf("failed to execute task: %v", err)
	}

	// Should have current task
	currentTask := agent.CurrentTask()
	if currentTask == nil {
		t.Fatal("expected CurrentTask to be set after Execute")
	}

	if currentTask.ID != "task-1" {
		t.Errorf("expected task ID 'task-1', got '%s'", currentTask.ID)
	}

	if currentTask.Priority != PriorityHigh {
		t.Errorf("expected priority High, got %v", currentTask.Priority)
	}
}

func TestBaseAgent_SendReceiveMessage(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestAgent"}

	agent := NewBaseAgent("test-1", persona, "TestAgent-1", mockLLM, mockReg)

	// Test sending message
	testMsg := Message{
		ID:      "msg-1",
		AgentID: "test-1",
		Type:    MessageTypePing,
		Content: "test message",
	}

	err := agent.SendMessage(testMsg)
	if err != nil {
		t.Fatalf("failed to send message: %v", err)
	}

	// Note: Receiving from ReceiveMessages() channel would require
	// the agent to be actively sending messages via sendMessage()
	// which happens during task execution
}

func TestBaseAgent_GetConversation(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestAgent"}

	agent := NewBaseAgent("test-1", persona, "TestAgent-1", mockLLM, mockReg)

	// Should have initial system message
	conv := agent.GetConversation()
	if len(conv) != 1 {
		t.Errorf("expected 1 initial message (system prompt), got %d", len(conv))
	}

	if conv[0].Role != chat.RoleSystem {
		t.Errorf("expected first message to be system, got %v", conv[0].Role)
	}
}

func TestBaseAgent_Reset(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestAgent"}

	agent := NewBaseAgent("test-1", persona, "TestAgent-1", mockLLM, mockReg)

	task := Task{
		ID:          "task-1",
		Description: "Test task",
		Priority:    PriorityNormal,
	}

	ctx := context.Background()
	_, _ = agent.Execute(ctx, task)

	// Wait for completion
	time.Sleep(500 * time.Millisecond)

	// Reset the agent
	err := agent.Reset()
	if err != nil {
		t.Fatalf("failed to reset agent: %v", err)
	}

	// Check state
	if agent.Status() != StatusIdle {
		t.Errorf("expected status to be Idle after Reset, got %v", agent.Status())
	}

	if agent.Progress() != 0.0 {
		t.Errorf("expected progress to be 0.0 after Reset, got %f", agent.Progress())
	}

	if agent.CurrentTask() != nil {
		t.Error("expected CurrentTask to be nil after Reset")
	}
}
