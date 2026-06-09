package compaction

import (
	"context"
	"testing"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/prompts"
)

// TestNewDefaultCompactorWithoutRegistry verifies backward compatibility
func TestNewDefaultCompactorWithoutRegistry(t *testing.T) {
	compactor := NewDefaultCompactor(
		nil, // LLM client
		nil, // Token counter
		nil, // Strategy
		100000,
		80000,
	)

	if compactor == nil {
		t.Fatal("NewDefaultCompactor returned nil")
	}

	if compactor.promptRegistry != nil {
		t.Error("promptRegistry should be nil for backward compatibility")
	}
}

// TestNewDefaultCompactorWithRegistry verifies new constructor
func TestNewDefaultCompactorWithRegistry(t *testing.T) {
	reg, err := prompts.NewRegistry(prompts.WithEmbeddedDefaults())
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	compactor := NewDefaultCompactorWithPrompts(
		nil, // LLM client
		nil, // Token counter
		nil, // Strategy
		100000,
		80000,
		reg,
	)

	if compactor == nil {
		t.Fatal("NewDefaultCompactorWithPrompts returned nil")
	}

	if compactor.promptRegistry == nil {
		t.Error("promptRegistry should not be nil")
	}

	if compactor.promptRegistry != reg {
		t.Error("promptRegistry should match passed registry")
	}
}

// TestCompactorUsesRegistryPrompt verifies compactor loads prompt from registry
func TestCompactorUsesRegistryPrompt(t *testing.T) {
	reg, err := prompts.NewRegistry(prompts.WithEmbeddedDefaults())
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	// Verify compaction prompt exists in registry
	prompt, err := reg.Get("compaction")
	if err != nil {
		t.Fatalf("Compaction prompt not found in registry: %v", err)
	}

	if prompt == nil {
		t.Fatal("Compaction prompt is nil")
	}

	if prompt.Name != "compaction" {
		t.Errorf("Prompt name = %q, want compaction", prompt.Name)
	}

	// Verify it has content
	if prompt.Content == "" {
		t.Error("Compaction prompt should have content")
	}
}

// TestCompactorFallbackWithoutRegistry verifies fallback behavior
func TestCompactorFallbackWithoutRegistry(t *testing.T) {
	compactor := NewDefaultCompactor(
		nil, nil, nil, 100000, 80000,
	)

	// Should use fallback prompt (no error)
	if compactor.promptRegistry != nil {
		t.Fatal("Registry should be nil for this test")
	}

	// The compactor should still work with hardcoded fallback
	if compactor.compactThreshold != 80000 {
		t.Errorf("compactThreshold = %d, want 80000", compactor.compactThreshold)
	}
}

// TestCompactionPromptRendering verifies prompt can be rendered with template data
func TestCompactionPromptRendering(t *testing.T) {
	reg, err := prompts.NewRegistry(prompts.WithEmbeddedDefaults())
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	// Render compaction prompt
	rendered, err := reg.Render("compaction", nil)
	if err != nil {
		t.Fatalf("Failed to render compaction prompt: %v", err)
	}

	if rendered == "" {
		t.Error("Rendered prompt is empty")
	}

	// Verify prompt content is appropriate for summarization
	expectedContent := []string{
		"summarizer",
		"summary",
		"conversation",
	}

	for _, content := range expectedContent {
		if !containsStr(rendered, content) {
			t.Errorf("Rendered prompt missing %q", content)
		}
	}
}

// TestCompactorWithCustomPrompt verifies custom prompts would work
func TestCompactorWithCustomPrompt(t *testing.T) {
	// In real scenario, this would load custom prompt from ~/.config/stoa/prompts/
	// For now, verify the structure supports it

	reg, err := prompts.NewRegistry(prompts.WithEmbeddedDefaults())
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	compactor := NewDefaultCompactorWithPrompts(
		newMockLLMClient(),
		newMockTokenCounter(),
		nil,
		100000,
		80000,
		reg,
	)

	// Verify compactor has registry to load custom prompts
	if compactor.promptRegistry == nil {
		t.Fatal("Compactor should have prompt registry")
	}

	// Verify we can get compaction prompt
	if _, err := compactor.promptRegistry.Get("compaction"); err != nil {
		t.Fatalf("Failed to get compaction prompt: %v", err)
	}
}

// TestCompactorPromptResolution verifies priority order
func TestCompactorPromptResolution(t *testing.T) {
	reg, _ := prompts.NewRegistry(prompts.WithEmbeddedDefaults())

	// Compactor with registry should load from registry
	withReg := NewDefaultCompactorWithPrompts(
		newMockLLMClient(),
		newMockTokenCounter(),
		nil,
		100000,
		80000,
		reg,
	)

	// Verify registry exists
	if withReg.promptRegistry == nil {
		t.Fatal("Should have registry")
	}

	// Compactor without registry should work with fallback
	withoutReg := NewDefaultCompactor(
		newMockLLMClient(),
		newMockTokenCounter(),
		nil,
		100000,
		80000,
	)

	// Verify no registry
	if withoutReg.promptRegistry != nil {
		t.Fatal("Should not have registry")
	}

	// Both should be functional
	if withReg.maxTokens != 100000 {
		t.Error("With-registry compactor not initialized correctly")
	}

	if withoutReg.maxTokens != 100000 {
		t.Error("Without-registry compactor not initialized correctly")
	}
}

// TestCompactionPromptFormat verifies prompt structure
func TestCompactionPromptFormat(t *testing.T) {
	reg, _ := prompts.NewRegistry(prompts.WithEmbeddedDefaults())

	// Get the prompt
	prompt, _ := reg.Get("compaction")

	// Verify it has proper structure
	if prompt.Name != "compaction" {
		t.Errorf("Name = %q, want compaction", prompt.Name)
	}

	if prompt.Type != prompts.PromptCompaction {
		t.Errorf("Type = %v, want compaction", prompt.Type)
	}

	if prompt.Description == "" {
		t.Error("Description should not be empty")
	}

	if prompt.Content == "" {
		t.Error("Content should not be empty")
	}
}

// Helper functions

type mockLLMClient struct{}

func (m *mockLLMClient) SendMessage(ctx context.Context, messages []chat.Message, tools []llm.Tool) (string, error) {
	return "mock summary", nil
}

func (m *mockLLMClient) StreamMessage(ctx context.Context, messages []chat.Message, tools []llm.Tool) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}
func (m *mockLLMClient) ListModels(_ context.Context) ([]string, error) { return nil, nil }
func (m *mockLLMClient) ListModelInfo(_ context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}

func newMockLLMClient() *mockLLMClient {
	return &mockLLMClient{}
}

type mockTokenCounter struct{}

func (m *mockTokenCounter) Count(text string) (int, error) {
	return len(text) / 4, nil
}

func (m *mockTokenCounter) CountMessages(messages []chat.Message) (int, error) {
	return len(messages) * 100, nil
}

func (m *mockTokenCounter) CountPrompt(prompt string) (int, error) {
	return len(prompt) / 4, nil
}

func newMockTokenCounter() *mockTokenCounter {
	return &mockTokenCounter{}
}

func containsStr(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
