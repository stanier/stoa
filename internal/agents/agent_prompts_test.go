package agents

import (
	"testing"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/prompts"
	"github.com/stanier/stoa/internal/skills"
)

// TestNewBaseAgentWithoutRegistry verifies agents work without registry (backward compat)
func TestNewBaseAgentWithoutRegistry(t *testing.T) {
	persona := NewBasePersona(
		"test", "Test persona", "Test system prompt",
		[]string{}, []string{},
		1, ThinkingStyleFast, CommunicationVerbose,
	)

	agent := NewBaseAgent("agent-1", persona, "Test Agent", nil, nil)

	if agent == nil {
		t.Fatal("NewBaseAgent returned nil")
	}

	if agent.ID() != "agent-1" {
		t.Errorf("ID = %q, want agent-1", agent.ID())
	}

	// Verify conversation has system message with hardcoded prompt
	if len(agent.conversation) != 1 {
		t.Errorf("Conversation length = %d, want 1", len(agent.conversation))
	}

	if agent.conversation[0].Role != chat.RoleSystem {
		t.Error("First message should be system message")
	}

	if agent.conversation[0].Content != "Test system prompt" {
		t.Error("System message should contain hardcoded prompt")
	}
}

// TestNewBaseAgentWithRegistry verifies agents use registry prompts
func TestNewBaseAgentWithRegistry(t *testing.T) {
	// Create persona
	persona := NewBasePersona(
		"coder", "Coding specialist", "Hardcoded coder prompt",
		[]string{}, []string{},
		1, ThinkingStyleFast, CommunicationVerbose,
	)

	// Create registry with embedded defaults
	reg, err := prompts.NewRegistry(prompts.WithEmbeddedDefaults())
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	// Create agent with registry
	agent := NewBaseAgentWithPrompts("agent-2", persona, "Coder Agent", nil, nil, reg)

	if agent == nil {
		t.Fatal("NewBaseAgentWithPrompts returned nil")
	}

	// Verify conversation has system message
	if len(agent.conversation) != 1 {
		t.Errorf("Conversation length = %d, want 1", len(agent.conversation))
	}

	if agent.conversation[0].Role != chat.RoleSystem {
		t.Error("First message should be system message")
	}

	// Verify it used registry prompt (not hardcoded)
	// Registry should have loaded "coder" persona prompt
	systemMsg := agent.conversation[0].Content
	if systemMsg == "Hardcoded coder prompt" {
		t.Error("Should use registry prompt, not hardcoded")
	}

	if len(systemMsg) == 0 {
		t.Error("System message should not be empty")
	}
}

// TestAgentResetWithRegistry verifies reset uses registry prompts
func TestAgentResetWithRegistry(t *testing.T) {
	persona := NewBasePersona(
		"coder", "Coding specialist", "Hardcoded prompt",
		[]string{}, []string{},
		1, ThinkingStyleFast, CommunicationVerbose,
	)

	reg, _ := prompts.NewRegistry(prompts.WithEmbeddedDefaults())
	agent := NewBaseAgentWithPrompts("agent-3", persona, "Test", nil, nil, reg)

	// Modify conversation
	agent.conversation = append(agent.conversation, chat.NewUserMessage("Test message"))

	// Reset
	err := agent.Reset()
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Verify conversation reset and still uses registry prompt
	if len(agent.conversation) != 1 {
		t.Errorf("After reset, conversation length = %d, want 1", len(agent.conversation))
	}

	if agent.conversation[0].Role != chat.RoleSystem {
		t.Error("After reset, first message should be system")
	}
}

// TestManagerSpawnWithPrompts verifies manager passes registry to agents
func TestManagerSpawnWithPrompts(t *testing.T) {
	skillReg := skills.NewRegistry()
	promptReg, _ := prompts.NewRegistry(prompts.WithEmbeddedDefaults())

	manager := NewManagerWithPrompts(nil, skillReg, promptReg, 5)

	persona := NewBasePersona(
		"coder", "Coding specialist", "Hardcoded",
		[]string{}, []string{},
		1, ThinkingStyleFast, CommunicationVerbose,
	)

	agent, err := manager.Spawn(persona, "Test Coder")
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	if agent == nil {
		t.Fatal("Spawn returned nil agent")
	}

	baseAgent := agent.(*BaseAgent)

	// Verify agent has registry
	if baseAgent.promptRegistry == nil {
		t.Error("Agent should have promptRegistry")
	}

	// Verify conversation has system message
	if len(baseAgent.conversation) != 1 {
		t.Errorf("Conversation length = %d, want 1", len(baseAgent.conversation))
	}
}

// TestAgentPromptFallback verifies fallback when registry unavailable
func TestAgentPromptFallback(t *testing.T) {
	persona := NewBasePersona(
		"analyzer", "Analyzer", "Analyzer prompt",
		[]string{}, []string{},
		1, ThinkingStyleDeep, CommunicationVerbose,
	)

	// Create agent with nil registry (should use fallback)
	agent := NewBaseAgentWithPrompts("agent-4", persona, "Analyzer", nil, nil, nil)

	if len(agent.conversation) != 1 {
		t.Errorf("Conversation length = %d, want 1", len(agent.conversation))
	}

	// Should use hardcoded persona prompt
	if agent.conversation[0].Content != "Analyzer prompt" {
		t.Errorf("Should use hardcoded prompt, got: %q", agent.conversation[0].Content)
	}
}

// TestManagerWithoutPrompts verifies backward compatibility
func TestManagerWithoutPrompts(t *testing.T) {
	manager := NewManager(nil, nil, 5)

	if manager == nil {
		t.Fatal("NewManager returned nil")
	}

	// Verify manager created successfully (even without prompts)
	if manager.maxConcurrent != 5 {
		t.Errorf("maxConcurrent = %d, want 5", manager.maxConcurrent)
	}
}

// TestAgentPersonaNameMatching verifies persona names used for registry lookup
func TestAgentPersonaNameMatching(t *testing.T) {
	tests := []struct {
		name        string
		personaName string
		wantFound   bool
	}{
		{
			name:        "coder persona",
			personaName: "coder",
			wantFound:   true,
		},
		{
			name:        "analyzer persona",
			personaName: "analyzer",
			wantFound:   true,
		},
		{
			name:        "nonexistent persona",
			personaName: "nonexistent",
			wantFound:   false,
		},
	}

	reg, _ := prompts.NewRegistry(prompts.WithEmbeddedDefaults())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			persona := NewBasePersona(
				tt.personaName, "Description", "Hardcoded",
				[]string{}, []string{},
				1, ThinkingStyleFast, CommunicationVerbose,
			)

			agent := NewBaseAgentWithPrompts("id", persona, "Name", nil, nil, reg)

			systemPrompt := agent.conversation[0].Content

			if tt.wantFound {
				if systemPrompt == "Hardcoded" {
					t.Error("Should have used registry prompt")
				}
			} else {
				// For nonexistent personas, should fall back to hardcoded
				if systemPrompt != "Hardcoded" {
					t.Errorf("For nonexistent persona, should use hardcoded, got: %q", systemPrompt)
				}
			}
		})
	}
}

// Helper: newMockLLMClient creates a mock LLM client for testing
func newMockLLMClient() *mockLLMClient {
	return &mockLLMClient{}
}
