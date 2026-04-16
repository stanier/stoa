package prompts

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stanier/stoa/internal/skills"
)

// MockCommand implements the Command interface for testing
type MockCommand struct {
	name        string
	description string
	usage       string
}

func (m *MockCommand) Name() string {
	return m.name
}

func (m *MockCommand) Description() string {
	return m.description
}

func (m *MockCommand) Usage() string {
	return m.usage
}

func (m *MockCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	return skills.CommandResult{Success: true, Message: "mock executed"}, nil
}

// MockTool implements the Tool interface for testing
type MockTool struct {
	name        string
	description string
	schema      skills.ToolSchema
}

func (m *MockTool) Name() string {
	return m.name
}

func (m *MockTool) Description() string {
	return m.description
}

func (m *MockTool) Schema() skills.ToolSchema {
	return m.schema
}

func (m *MockTool) Execute(ctx context.Context, params json.RawMessage) (skills.ToolResult, error) {
	return skills.ToolResult{Success: true, Content: "mock result"}, nil
}

func TestBuildTemplateData(t *testing.T) {
	// Create a registry with test tools and commands
	reg := skills.NewRegistry()

	cmd := &MockCommand{
		name:        "help",
		description: "Show help information",
		usage:       "/help [command]",
	}
	reg.RegisterCommand(cmd)

	tool := &MockTool{
		name:        "calculator",
		description: "Perform arithmetic operations",
		schema: skills.ToolSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"operation": map[string]string{"type": "string"},
				"a":         map[string]string{"type": "number"},
				"b":         map[string]string{"type": "number"},
			},
			Required: []string{"operation", "a", "b"},
		},
	}
	reg.RegisterTool(tool)

	// Build template data
	data := BuildTemplateData(reg, "claude-opus", "anthropic")

	// Verify basic fields
	if data.Model != "claude-opus" {
		t.Errorf("Model = %q, want claude-opus", data.Model)
	}

	if data.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", data.Provider)
	}

	if data.Environment != "Stoa Application" {
		t.Errorf("Environment = %q, want Stoa Application", data.Environment)
	}

	// Verify date format
	if len(data.Date) != 10 || data.Date[4] != '-' {
		t.Errorf("Date format invalid: %q", data.Date)
	}

	// Verify tools and commands are formatted
	if data.Tools == "" {
		t.Error("Tools should not be empty")
	}
	if data.Commands == "" {
		t.Error("Commands should not be empty")
	}

	// Verify features
	if len(data.Features) == 0 {
		t.Error("Features should not be empty")
	}

	// Verify structured data
	if len(data.ToolsList) != 1 {
		t.Errorf("ToolsList length = %d, want 1", len(data.ToolsList))
	}

	if len(data.CommandsList) != 1 {
		t.Errorf("CommandsList length = %d, want 1", len(data.CommandsList))
	}
}

func TestFormatTools(t *testing.T) {
	reg := skills.NewRegistry()

	tool1 := &MockTool{
		name:        "calculator",
		description: "Arithmetic operations",
	}
	tool2 := &MockTool{
		name:        "search",
		description: "Search the web",
	}

	reg.RegisterTool(tool1)
	reg.RegisterTool(tool2)

	result := formatTools(reg)

	tests := []string{
		"Available Tools",
		"calculator",
		"Arithmetic operations",
		"search",
		"Search the web",
	}

	for _, test := range tests {
		if !contains(result, test) {
			t.Errorf("formatTools() missing %q", test)
		}
	}
}

func TestFormatTools_Empty(t *testing.T) {
	reg := skills.NewRegistry()
	result := formatTools(reg)

	if result != "No tools available" {
		t.Errorf("formatTools(empty) = %q, want 'No tools available'", result)
	}
}

func TestFormatCommands(t *testing.T) {
	reg := skills.NewRegistry()

	cmd1 := &MockCommand{
		name:        "clear",
		description: "Clear the conversation",
		usage:       "/clear",
	}
	cmd2 := &MockCommand{
		name:        "help",
		description: "Show help",
		usage:       "/help",
	}

	reg.RegisterCommand(cmd1)
	reg.RegisterCommand(cmd2)

	result := formatCommands(reg)

	tests := []string{
		"Available Commands",
		"/clear",
		"Clear the conversation",
		"/help",
		"Show help",
	}

	for _, test := range tests {
		if !contains(result, test) {
			t.Errorf("formatCommands() missing %q", test)
		}
	}
}

func TestFormatCommands_Empty(t *testing.T) {
	reg := skills.NewRegistry()
	result := formatCommands(reg)

	if result != "No commands available" {
		t.Errorf("formatCommands(empty) = %q, want 'No commands available'", result)
	}
}

func TestExtractToolInfo(t *testing.T) {
	reg := skills.NewRegistry()

	tool := &MockTool{
		name:        "calculator",
		description: "Math operations",
		schema: skills.ToolSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"op": "string",
			},
		},
	}
	reg.RegisterTool(tool)

	infos := extractToolInfo(reg)

	if len(infos) != 1 {
		t.Fatalf("extractToolInfo() got %d infos, want 1", len(infos))
	}

	info := infos[0]
	if info.Name != "calculator" {
		t.Errorf("Name = %q, want calculator", info.Name)
	}

	if info.Description != "Math operations" {
		t.Errorf("Description = %q, want Math operations", info.Description)
	}

	if info.Parameters == "" {
		t.Error("Parameters should not be empty")
	}
}

func TestExtractCommandInfo(t *testing.T) {
	reg := skills.NewRegistry()

	cmd := &MockCommand{
		name:        "help",
		description: "Show help",
		usage:       "/help [command]",
	}
	reg.RegisterCommand(cmd)

	infos := extractCommandInfo(reg)

	if len(infos) != 1 {
		t.Fatalf("extractCommandInfo() got %d infos, want 1", len(infos))
	}

	info := infos[0]
	if info.Name != "help" {
		t.Errorf("Name = %q, want help", info.Name)
	}

	if info.Description != "Show help" {
		t.Errorf("Description = %q, want Show help", info.Description)
	}

	if info.Usage != "/help [command]" {
		t.Errorf("Usage = %q, want /help [command]", info.Usage)
	}
}

func TestMarshalSchema(t *testing.T) {
	tests := []struct {
		name    string
		schema  skills.ToolSchema
		wantStr string
	}{
		{
			name:    "empty schema",
			schema:  skills.ToolSchema{},
			wantStr: "{}",
		},
		{
			name: "simple schema",
			schema: skills.ToolSchema{
				Type: "object",
			},
			wantStr: "object",
		},
		{
			name: "schema with required fields",
			schema: skills.ToolSchema{
				Type:     "object",
				Required: []string{"name", "age"},
			},
			wantStr: "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := marshalSchema(tt.schema)
			if err != nil {
				t.Errorf("marshalSchema() error = %v", err)
				return
			}

			if !contains(result, tt.wantStr) {
				t.Errorf("marshalSchema() missing %q in %q", tt.wantStr, result)
			}
		})
	}
}

func TestSimpleTemplateData(t *testing.T) {
	data := SimpleTemplateData("gpt-4", "openai")

	if data.Model != "gpt-4" {
		t.Errorf("Model = %q, want gpt-4", data.Model)
	}

	if data.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", data.Provider)
	}

	// Should have default values when registry is nil
	if data.Tools != "No tools available" {
		t.Errorf("Tools = %q, want 'No tools available'", data.Tools)
	}

	if data.Commands != "No commands available" {
		t.Errorf("Commands = %q, want 'No commands available'", data.Commands)
	}
}

func TestTemplateDataWithNilRegistry(t *testing.T) {
	data := BuildTemplateData(nil, "claude", "anthropic")

	if data.Tools != "No tools available" {
		t.Errorf("Tools with nil registry = %q, want 'No tools available'", data.Tools)
	}

	if data.Commands != "No commands available" {
		t.Errorf("Commands with nil registry = %q, want 'No commands available'", data.Commands)
	}

	if len(data.ToolsList) != 0 {
		t.Errorf("ToolsList with nil registry length = %d, want 0", len(data.ToolsList))
	}

	if len(data.CommandsList) != 0 {
		t.Errorf("CommandsList with nil registry length = %d, want 0", len(data.CommandsList))
	}
}

func TestTemplateDataRendering(t *testing.T) {
	// Create a registry with tools and commands
	reg := skills.NewRegistry()

	reg.RegisterCommand(&MockCommand{
		name:        "help",
		description: "Show help",
		usage:       "/help",
	})

	reg.RegisterTool(&MockTool{
		name:        "calc",
		description: "Calculator",
	})

	// Build template data
	data := BuildTemplateData(reg, "test-model", "test-provider")

	// Verify the rendered strings contain expected content
	if !contains(data.Tools, "calc") || !contains(data.Tools, "Calculator") {
		t.Error("Tools rendering missing calculator")
	}

	if !contains(data.Commands, "help") || !contains(data.Commands, "Show help") {
		t.Error("Commands rendering missing help")
	}

	// Verify custom map is initialized
	if data.Custom == nil {
		t.Error("Custom map should be initialized")
	}
}

func TestTemplateDataCustomMap(t *testing.T) {
	data := BuildTemplateData(nil, "model", "provider")

	// Add custom data
	data.Custom["key1"] = "value1"
	data.Custom["key2"] = 42

	if data.Custom["key1"] != "value1" {
		t.Error("Custom map not storing values correctly")
	}

	if data.Custom["key2"] != 42 {
		t.Error("Custom map not storing numeric values")
	}
}
