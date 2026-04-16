package ui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/skills"
)

// MockTool implements the skills.Tool interface for testing
type MockTool struct {
	name        string
	description string
	schema      skills.ToolSchema
}

func (m *MockTool) Name() string        { return m.name }
func (m *MockTool) Description() string { return m.description }
func (m *MockTool) Schema() skills.ToolSchema {
	if m.schema.Type == "" {
		return skills.ToolSchema{Type: "object"}
	}
	return m.schema
}
func (m *MockTool) Execute(ctx context.Context, params json.RawMessage) (skills.ToolResult, error) {
	return skills.ToolResult{Success: true, Content: ""}, nil
}

func TestToolFiltering(t *testing.T) {
	allTools := []skills.Tool{
		&MockTool{name: "calculator", description: "Calculator tool"},
		&MockTool{name: "mcp_filesystem_read_file", description: "Read file"},
		&MockTool{name: "mcp_filesystem_write_file", description: "Write file"},
		&MockTool{name: "mcp_filesystem_list_directory", description: "List directory"},
		&MockTool{name: "mcp_web_search", description: "Web search"},
		&MockTool{name: "mcp_web_fetch", description: "Web fetch"},
	}

	tests := []struct {
		name            string
		patterns        []string
		expectedCount   int
		expectedNames   []string
		unexpectedNames []string
	}{
		{
			name:          "no filter - all tools",
			patterns:      []string{},
			expectedCount: 6,
			expectedNames: []string{
				"calculator",
				"mcp_filesystem_read_file",
				"mcp_filesystem_write_file",
				"mcp_filesystem_list_directory",
				"mcp_web_search",
				"mcp_web_fetch",
			},
		},
		{
			name:          "calculator only",
			patterns:      []string{"calculator"},
			expectedCount: 1,
			expectedNames: []string{"calculator"},
			unexpectedNames: []string{
				"mcp_filesystem_read_file",
				"mcp_web_search",
			},
		},
		{
			name:     "filesystem tools only (wildcard)",
			patterns: []string{"mcp_filesystem_*"},
			expectedCount: 3,
			expectedNames: []string{
				"mcp_filesystem_read_file",
				"mcp_filesystem_write_file",
				"mcp_filesystem_list_directory",
			},
			unexpectedNames: []string{
				"calculator",
				"mcp_web_search",
			},
		},
		{
			name:     "calculator + filesystem (default persona)",
			patterns: []string{"calculator", "mcp_filesystem_*"},
			expectedCount: 4,
			expectedNames: []string{
				"calculator",
				"mcp_filesystem_read_file",
				"mcp_filesystem_write_file",
				"mcp_filesystem_list_directory",
			},
			unexpectedNames: []string{
				"mcp_web_search",
				"mcp_web_fetch",
			},
		},
		{
			name:     "multiple wildcards",
			patterns: []string{"mcp_filesystem_*", "mcp_web_*"},
			expectedCount: 5,
			expectedNames: []string{
				"mcp_filesystem_read_file",
				"mcp_web_search",
				"mcp_web_fetch",
			},
			unexpectedNames: []string{
				"calculator",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := app.FilterSkillTools(allTools, tt.patterns)

			if len(tools) != tt.expectedCount {
				t.Errorf("expected %d tools, got %d", tt.expectedCount, len(tools))
			}

			toolMap := make(map[string]bool)
			for _, tool := range tools {
				toolMap[tool.Name()] = true
			}

			for _, name := range tt.expectedNames {
				if !toolMap[name] {
					t.Errorf("expected tool %q to be present, but it wasn't", name)
				}
			}

			for _, name := range tt.unexpectedNames {
				if toolMap[name] {
					t.Errorf("expected tool %q to be absent, but it was present", name)
				}
			}
		})
	}
}

func TestGlobPatternMatching(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		match   bool
	}{
		{"calculator", "calculator", true},
		{"calculator", "calc", false},
		{"mcp_filesystem_*", "mcp_filesystem_read_file", true},
		{"mcp_filesystem_*", "mcp_filesystem_write_file", true},
		{"mcp_filesystem_*", "mcp_web_search", false},
		{"mcp_*_read_file", "mcp_filesystem_read_file", true},
		{"mcp_*_read_file", "mcp_database_read_file", true},
		{"mcp_*_read_file", "mcp_filesystem_write_file", false},
		{"*_search", "mcp_web_search", true},
		{"*_search", "google_search", true},
		{"*_search", "mcp_web_fetch", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_vs_"+tt.name, func(t *testing.T) {
			matched, err := filepath.Match(tt.pattern, tt.name)
			if err != nil {
				t.Fatalf("filepath.Match error: %v", err)
			}
			if matched != tt.match {
				t.Errorf("pattern %q vs name %q: expected match=%v, got %v",
					tt.pattern, tt.name, tt.match, matched)
			}
		})
	}
}

func TestToolConversion(t *testing.T) {
	allTools := []skills.Tool{
		&MockTool{
			name:        "test_tool",
			description: "Test description",
			schema:      skills.ToolSchema{Type: "object"},
		},
	}

	filtered := app.FilterSkillTools(allTools, []string{"test_tool"})
	if len(filtered) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(filtered))
	}

	tool := filtered[0]
	if tool.Name() != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", tool.Name())
	}
	if tool.Description() != "Test description" {
		t.Errorf("expected description 'Test description', got %q", tool.Description())
	}
	if tool.Schema().Type != "object" {
		t.Errorf("expected schema type 'object', got %q", tool.Schema().Type)
	}
}

func TestEmptyToolSet(t *testing.T) {
	filtered := app.FilterSkillTools(nil, []string{"calculator"})
	if len(filtered) != 0 {
		t.Errorf("expected 0 tools with nil input, got %d", len(filtered))
	}
}
