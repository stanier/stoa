package compaction

import (
	"context"
	"testing"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
)

// mockLLMClientSum is a mock LLM client for testing
type mockLLMClientSum struct {
	sendMessageFunc func(ctx context.Context, messages []chat.Message, tools []llm.Tool) (string, error)
}

func (m *mockLLMClientSum) SendMessage(ctx context.Context, messages []chat.Message, tools []llm.Tool) (string, error) {
	if m.sendMessageFunc != nil {
		return m.sendMessageFunc(ctx, messages, tools)
	}
	return "This is a summary of the conversation.", nil
}

func (m *mockLLMClientSum) StreamMessage(ctx context.Context, messages []chat.Message, tools []llm.Tool) (<-chan llm.StreamChunk, error) {
	return nil, nil
}
func (m *mockLLMClientSum) ListModels(_ context.Context) ([]string, error) { return nil, nil }
func (m *mockLLMClientSum) ListModelInfo(_ context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}

// mockTokenCounterSum is a mock token counter for testing
type mockTokenCounterSum struct {
	countFunc         func(text string) (int, error)
	countMessagesFunc func(messages []chat.Message) (int, error)
}

func (m *mockTokenCounterSum) Count(text string) (int, error) {
	if m.countFunc != nil {
		return m.countFunc(text)
	}
	// Simple approximation: 1 token per ~4 characters
	tokens := len(text) / 4
	if tokens < 1 && len(text) > 0 {
		tokens = 1
	}
	return tokens, nil
}

func (m *mockTokenCounterSum) CountMessages(messages []chat.Message) (int, error) {
	if m.countMessagesFunc != nil {
		return m.countMessagesFunc(messages)
	}
	total := 0
	for _, msg := range messages {
		tokens, _ := m.Count(msg.Content)
		total += tokens + 4 // Add overhead
	}
	return total, nil
}

// mockStrategy is a mock strategy for testing
type mockStrategy struct {
	selectFunc func(messages []chat.Message, tokens int) []int
}

func (m *mockStrategy) SelectMessagesToCompact(messages []chat.Message, tokens int) []int {
	if m.selectFunc != nil {
		return m.selectFunc(messages, tokens)
	}
	// Default: compact first half
	half := len(messages) / 2
	indices := make([]int, 0, half)
	for i := 0; i < half; i++ {
		if messages[i].Role != chat.RoleSystem {
			indices = append(indices, i)
		}
	}
	return indices
}

func TestDefaultCompactor_ShouldCompact(t *testing.T) {
	tests := []struct {
		name            string
		maxTokens       int
		threshold       int
		messageCount    int
		expectedCompact bool
	}{
		{
			name:            "below threshold",
			maxTokens:       1000,
			threshold:       800,
			messageCount:    10,
			expectedCompact: false,
		},
		{
			name:            "above threshold",
			maxTokens:       1000,
			threshold:       200,
			messageCount:    100,
			expectedCompact: true,
		},
		{
			name:            "at threshold",
			maxTokens:       1000,
			threshold:       100,
			messageCount:    50,
			expectedCompact: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockLLM := &mockLLMClientSum{}
			mockCounter := &mockTokenCounterSum{}
			mockStrat := &mockStrategy{}

			compactor := NewDefaultCompactor(mockLLM, mockCounter, mockStrat, tt.maxTokens, tt.threshold)

			messages := make([]chat.Message, tt.messageCount)
			for i := 0; i < tt.messageCount; i++ {
				messages[i] = chat.NewUserMessage("test message")
			}

			shouldCompact, tokens, err := compactor.ShouldCompact(messages)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if shouldCompact != tt.expectedCompact {
				t.Errorf("expected shouldCompact=%v, got %v (tokens=%d, threshold=%d)",
					tt.expectedCompact, shouldCompact, tokens, tt.threshold)
			}
		})
	}
}

func TestDefaultCompactor_Compact(t *testing.T) {
	mockLLM := &mockLLMClientSum{
		sendMessageFunc: func(ctx context.Context, messages []chat.Message, tools []llm.Tool) (string, error) {
			return "Summary: User asked questions, assistant provided answers.", nil
		},
	}
	mockCounter := &mockTokenCounterSum{}
	mockStrat := &mockStrategy{
		selectFunc: func(messages []chat.Message, tokens int) []int {
			// Select first 3 non-system messages
			return []int{1, 2, 3}
		},
	}

	compactor := NewDefaultCompactor(mockLLM, mockCounter, mockStrat, 1000, 800)

	messages := []chat.Message{
		chat.NewSystemMessage("System prompt"),
		chat.NewUserMessage("Hello"),
		chat.NewAssistantMessage("Hi there"),
		chat.NewUserMessage("How are you?"),
		chat.NewAssistantMessage("I'm doing well"),
		chat.NewUserMessage("Recent message"),
	}

	newMessages, err := compactor.Compact(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have: system message + summary + recent messages
	if len(newMessages) < 2 {
		t.Errorf("expected at least 2 messages after compaction, got %d", len(newMessages))
	}

	// First should still be system message
	if newMessages[0].Role != chat.RoleSystem {
		t.Errorf("expected first message to be system, got %v", newMessages[0].Role)
	}

	// Should contain a summary
	foundSummary := false
	for _, msg := range newMessages {
		if msg.Role == chat.RoleSummary {
			foundSummary = true
			break
		}
	}
	if !foundSummary {
		t.Error("expected to find a summary message after compaction")
	}
}

func TestDefaultCompactor_PreservesSystemMessages(t *testing.T) {
	mockLLM := &mockLLMClientSum{}
	mockCounter := &mockTokenCounterSum{}
	mockStrat := &mockStrategy{}

	compactor := NewDefaultCompactor(mockLLM, mockCounter, mockStrat, 1000, 800)

	messages := []chat.Message{
		chat.NewSystemMessage("System prompt 1"),
		chat.NewUserMessage("User message"),
		chat.NewSystemMessage("System prompt 2"),
		chat.NewAssistantMessage("Assistant message"),
	}

	newMessages, err := compactor.Compact(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Count system messages
	systemCount := 0
	for _, msg := range newMessages {
		if msg.Role == chat.RoleSystem {
			systemCount++
		}
	}

	if systemCount != 2 {
		t.Errorf("expected 2 system messages to be preserved, got %d", systemCount)
	}
}
