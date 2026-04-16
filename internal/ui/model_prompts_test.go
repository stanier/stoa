package ui

import (
	"testing"

	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/prompts"
)

// TestSetupPromptRegistry verifies prompt registry initialization
func TestSetupPromptRegistry(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.Config
		wantErr bool
	}{
		{
			name:    "setup with nil config",
			cfg:     nil,
			wantErr: false,
		},
		{
			name: "setup with prompts disabled",
			cfg: &config.Config{
				Prompts: config.PromptsConfig{
					Enabled: false,
				},
			},
			wantErr: false,
		},
		{
			name: "setup with prompts enabled",
			cfg: &config.Config{
				Prompts: config.PromptsConfig{
					Enabled:     true,
					OverrideDir: "",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := app.SetupPromptRegistry(tt.cfg)
			if registry == nil && !tt.wantErr {
				t.Error("app.SetupPromptRegistry() returned nil registry")
			}
		})
	}
}

// TestPromptRegistryLoadsDefaults verifies default prompts are available
func TestPromptRegistryLoadsDefaults(t *testing.T) {
	cfg := &config.Config{
		Prompts: config.PromptsConfig{
			Enabled: true,
		},
	}

	registry := app.SetupPromptRegistry(cfg)
	if registry == nil {
		t.Fatal("app.SetupPromptRegistry() returned nil")
	}

	// Check that default prompts are loaded
	expectedPrompts := []string{"assistant", "compaction", "coder", "analyzer", "default"}

	for _, promptName := range expectedPrompts {
		p, err := registry.Get(promptName)
		if err != nil {
			t.Errorf("Expected prompt %q not found: %v", promptName, err)
		}
		if p == nil {
			t.Errorf("Prompt %q is nil", promptName)
		}
	}
}

// TestTemplateDataWithSkillRegistry verifies template data creation with skills
func TestTemplateDataWithSkillRegistry(t *testing.T) {
	cfg := &config.Config{Skills: config.SkillsConfig{EnabledBuiltin: []string{"calculator"}}}
	registry := app.SetupSkillRegistry(cfg, "")
	if registry == nil {
		t.Fatal("SetupSkillRegistry() returned nil")
	}

	// Create template data with the skills registry
	templateData := prompts.BuildTemplateData(
		registry,
		"claude-opus",
		"anthropic",
	)

	if templateData == nil {
		t.Fatal("BuildTemplateData() returned nil")
	}

	// Verify model and provider are set
	if templateData.Model != "claude-opus" {
		t.Errorf("Model = %q, want claude-opus", templateData.Model)
	}

	if templateData.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", templateData.Provider)
	}

	// Verify tools and commands are populated
	if templateData.Tools == "" {
		t.Error("Tools should not be empty")
	}

	if templateData.Commands == "" {
		t.Error("Commands should not be empty")
	}

	// Verify tools list is populated
	if len(templateData.ToolsList) == 0 {
		t.Error("ToolsList should not be empty")
	}

	// Verify commands list is populated
	if len(templateData.CommandsList) == 0 {
		t.Error("CommandsList should not be empty")
	}
}

// TestSystemPromptRendering verifies system prompt can be rendered
func TestSystemPromptRendering(t *testing.T) {
	// Create registry and skills
	skillReg := app.SetupSkillRegistry(nil, "")
	promptReg := app.SetupPromptRegistry(&config.Config{
		Prompts: config.PromptsConfig{Enabled: true},
	})

	if skillReg == nil || promptReg == nil {
		t.Fatal("Failed to setup registries")
	}

	// Build template data
	templateData := prompts.BuildTemplateData(skillReg, "claude-opus", "anthropic")

	// Render assistant prompt
	rendered, err := promptReg.Render("assistant", templateData)
	if err != nil {
		t.Fatalf("Failed to render assistant prompt: %v", err)
	}

	if rendered == "" {
		t.Error("Rendered prompt is empty")
	}

	// Verify rendered content includes expected elements
	expectedContent := []string{
		"claude-opus",
		"anthropic",
		"Available Tools",
		"Available Commands",
	}

	for _, content := range expectedContent {
		if !contains(rendered, content) {
			t.Errorf("Rendered prompt missing %q", content)
		}
	}
}

// TestModelInitializationWithPrompts verifies Model initializes with prompt system
func TestModelInitializationWithPrompts(t *testing.T) {
	cfg := &config.Config{
		Prompts: config.PromptsConfig{
			Enabled: true,
		},
		Compaction: config.CompactionConfig{
			Enabled: false,
		},
		Agents: config.AgentsConfig{
			Enabled: false,
		},
		MCP: config.MCPConfig{
			Enabled: false,
		},
		Zulip: config.ZulipConfig{
			Enabled: false,
		},
	}

	model := NewWithConfig(cfg)

	// Verify prompt registry is initialized
	if model.promptRegistry == nil {
		t.Error("Model.promptRegistry should not be nil")
	}

	// Verify messages contain system message
	if len(model.messages) == 0 {
		t.Error("Model.messages should contain system message")
	}

	// Verify first message is system message
	firstMsg := model.messages[0]
	if firstMsg.Role != "system" {
		t.Errorf("First message role = %q, want system", firstMsg.Role)
	}

	// Verify system message is not empty
	if firstMsg.Content == "" {
		t.Error("System message content should not be empty")
	}
}

// Helper function
func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
