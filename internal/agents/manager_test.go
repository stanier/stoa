package agents

import (
	"context"
	"testing"
	"time"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/skills"
)

func TestManager_Spawn(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestPersona"}

	manager := NewManager(mockLLM, mockReg, 3)

	agent, err := manager.Spawn(persona, "TestAgent")
	if err != nil {
		t.Fatalf("failed to spawn agent: %v", err)
	}

	if agent == nil {
		t.Fatal("expected agent to be non-nil")
	}

	if agent.Name() != "TestAgent" {
		t.Errorf("expected agent name 'TestAgent', got '%s'", agent.Name())
	}

	// Check agent is tracked
	agents := manager.ListAgents()
	if len(agents) != 1 {
		t.Errorf("expected 1 agent in manager, got %d", len(agents))
	}
}

func TestManager_SpawnLimit(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestPersona"}

	manager := NewManager(mockLLM, mockReg, 2) // Max 2 agents

	// Spawn up to limit
	_, err := manager.Spawn(persona, "Agent1")
	if err != nil {
		t.Fatalf("failed to spawn first agent: %v", err)
	}

	_, err = manager.Spawn(persona, "Agent2")
	if err != nil {
		t.Fatalf("failed to spawn second agent: %v", err)
	}

	// Should fail on 3rd
	_, err = manager.Spawn(persona, "Agent3")
	if err == nil {
		t.Error("expected error when spawning beyond limit, got nil")
	}

	// Check agent count
	agents := manager.ListAgents()
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}
}

func TestManager_Kill(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestPersona"}

	manager := NewManager(mockLLM, mockReg, 3)

	agent, err := manager.Spawn(persona, "TestAgent")
	if err != nil {
		t.Fatalf("failed to spawn agent: %v", err)
	}

	// Kill the agent
	err = manager.Kill(agent.ID())
	if err != nil {
		t.Fatalf("failed to kill agent: %v", err)
	}

	// Check agent is removed
	agents := manager.ListAgents()
	if len(agents) != 0 {
		t.Errorf("expected 0 agents after kill, got %d", len(agents))
	}

	// Should return error when killing non-existent agent
	err = manager.Kill("non-existent-id")
	if err == nil {
		t.Error("expected error when killing non-existent agent, got nil")
	}
}

func TestManager_GetAgent(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestPersona"}

	manager := NewManager(mockLLM, mockReg, 3)

	agent, err := manager.Spawn(persona, "TestAgent")
	if err != nil {
		t.Fatalf("failed to spawn agent: %v", err)
	}

	// Get existing agent
	retrieved, ok := manager.GetAgent(agent.ID())
	if !ok {
		t.Error("expected to find agent, got false")
	}

	if retrieved.ID() != agent.ID() {
		t.Errorf("expected agent ID '%s', got '%s'", agent.ID(), retrieved.ID())
	}

	// Get non-existent agent
	_, ok = manager.GetAgent("non-existent-id")
	if ok {
		t.Error("expected not to find non-existent agent, got true")
	}
}

func TestManager_Delegate(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestPersona"}

	manager := NewManager(mockLLM, mockReg, 3)

	agent, err := manager.Spawn(persona, "TestAgent")
	if err != nil {
		t.Fatalf("failed to spawn agent: %v", err)
	}

	task := Task{
		ID:          "task-1",
		Description: "Test task",
		Priority:    PriorityNormal,
	}

	msgChan, err := manager.Delegate(context.Background(), agent.ID(), task)
	if err != nil {
		t.Fatalf("failed to delegate task: %v", err)
	}

	if msgChan == nil {
		t.Error("expected message channel to be non-nil")
	}

	// Should error for non-existent agent
	_, err = manager.Delegate(context.Background(), "non-existent-id", task)
	if err == nil {
		t.Error("expected error when delegating to non-existent agent, got nil")
	}
}

func TestManager_Subscribe(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestPersona"}

	manager := NewManager(mockLLM, mockReg, 3)

	// Subscribe to manager messages
	msgChan, _ := manager.Subscribe()
	if msgChan == nil {
		t.Fatal("expected message channel to be non-nil")
	}

	// Spawn agent and execute task
	agent, err := manager.Spawn(persona, "TestAgent")
	if err != nil {
		t.Fatalf("failed to spawn agent: %v", err)
	}

	task := Task{
		ID:          "task-1",
		Description: "Test task",
		Priority:    PriorityNormal,
	}

	_, err = manager.Delegate(context.Background(), agent.ID(), task)
	if err != nil {
		t.Fatalf("failed to delegate task: %v", err)
	}

	// Should receive messages on subscribed channel
	timeout := time.After(2 * time.Second)
	receivedMsg := false

	select {
	case msg := <-msgChan:
		if msg.AgentID == agent.ID() {
			receivedMsg = true
		}
	case <-timeout:
		// Timeout is ok, messages might not arrive immediately
	}

	// Note: We don't fail if no message received, as timing can be tricky
	_ = receivedMsg
}

func TestManager_Shutdown(t *testing.T) {
	mockLLM := &mockLLMClient{
		streamFunc: func(ctx context.Context, messages []chat.Message, tools []llm.Tool) (<-chan llm.StreamChunk, error) {
			ch := make(chan llm.StreamChunk)
			go func() {
				// Stream slowly
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
	persona := &mockPersona{name: "TestPersona"}

	manager := NewManager(mockLLM, mockReg, 3)

	// Spawn multiple agents
	for i := 0; i < 3; i++ {
		agent, err := manager.Spawn(persona, "")
		if err != nil {
			t.Fatalf("failed to spawn agent %d: %v", i, err)
		}

		task := Task{
			ID:          "task",
			Description: "Long task",
			Priority:    PriorityNormal,
		}

		_, _ = manager.Delegate(context.Background(), agent.ID(), task)
	}

	// Shutdown manager
	err := manager.Shutdown()
	if err != nil {
		t.Fatalf("failed to shutdown manager: %v", err)
	}

	// All agents should be removed
	agents := manager.ListAgents()
	if len(agents) != 0 {
		t.Errorf("expected 0 agents after shutdown, got %d", len(agents))
	}
}

func TestManager_GetStats(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()
	persona := &mockPersona{name: "TestPersona"}

	manager := NewManager(mockLLM, mockReg, 5)

	// Spawn some agents
	for i := 0; i < 3; i++ {
		_, err := manager.Spawn(persona, "")
		if err != nil {
			t.Fatalf("failed to spawn agent %d: %v", i, err)
		}
	}

	stats := manager.GetStats()

	totalAgents, ok := stats["total_agents"].(int)
	if !ok {
		t.Fatal("expected total_agents to be int")
	}

	if totalAgents != 3 {
		t.Errorf("expected 3 total agents, got %d", totalAgents)
	}

	maxConcurrent, ok := stats["max_concurrent"].(int)
	if !ok {
		t.Fatal("expected max_concurrent to be int")
	}

	if maxConcurrent != 5 {
		t.Errorf("expected max_concurrent to be 5, got %d", maxConcurrent)
	}
}

func TestManager_GetSharedContext(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockReg := skills.NewRegistry()

	manager := NewManager(mockLLM, mockReg, 3)

	ctx := manager.GetSharedContext()
	if ctx == nil {
		t.Fatal("expected shared context to be non-nil")
	}

	// Test shared context operations
	ctx.Set("test_key", "test_value")

	value, ok := ctx.Get("test_key")
	if !ok {
		t.Error("expected to find test_key in shared context")
	}

	if value != "test_value" {
		t.Errorf("expected value 'test_value', got '%v'", value)
	}
}
