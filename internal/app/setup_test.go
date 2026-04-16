package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stanier/stoa/internal/skills"
)

func TestFilterSkillTools(t *testing.T) {
	// Create mock tools
	mockTools := []skills.Tool{
		&mockTool{name: "calculator"},
		&mockTool{name: "mcp_filesystem_read_file"},
		&mockTool{name: "mcp_filesystem_write_file"},
		&mockTool{name: "mcp_memory_store"},
		&mockTool{name: "web_search"},
	}

	tests := []struct {
		name            string
		tools           []skills.Tool
		allowedPatterns []string
		expectedCount   int
		expectedNames   []string
	}{
		{
			name:            "nil patterns returns all tools",
			tools:           mockTools,
			allowedPatterns: nil,
			expectedCount:   5,
			expectedNames:   []string{"calculator", "mcp_filesystem_read_file", "mcp_filesystem_write_file", "mcp_memory_store", "web_search"},
		},
		{
			name:            "empty patterns returns all tools",
			tools:           mockTools,
			allowedPatterns: []string{},
			expectedCount:   5,
			expectedNames:   []string{"calculator", "mcp_filesystem_read_file", "mcp_filesystem_write_file", "mcp_memory_store", "web_search"},
		},
		{
			name:            "single exact match",
			tools:           mockTools,
			allowedPatterns: []string{"calculator"},
			expectedCount:   1,
			expectedNames:   []string{"calculator"},
		},
		{
			name:            "wildcard pattern matches multiple",
			tools:           mockTools,
			allowedPatterns: []string{"mcp_filesystem_*"},
			expectedCount:   2,
			expectedNames:   []string{"mcp_filesystem_read_file", "mcp_filesystem_write_file"},
		},
		{
			name:            "multiple patterns",
			tools:           mockTools,
			allowedPatterns: []string{"calculator", "mcp_memory_*"},
			expectedCount:   2,
			expectedNames:   []string{"calculator", "mcp_memory_store"},
		},
		{
			name:            "pattern with no matches",
			tools:           mockTools,
			allowedPatterns: []string{"nonexistent_*"},
			expectedCount:   0,
			expectedNames:   []string{},
		},
		{
			name:            "broad wildcard",
			tools:           mockTools,
			allowedPatterns: []string{"mcp_*"},
			expectedCount:   3,
			expectedNames:   []string{"mcp_filesystem_read_file", "mcp_filesystem_write_file", "mcp_memory_store"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterSkillTools(tt.tools, tt.allowedPatterns)

			if len(result) != tt.expectedCount {
				t.Errorf("FilterSkillTools() returned %d tools, want %d", len(result), tt.expectedCount)
			}

			// Check that all expected tools are present
			resultNames := make(map[string]bool)
			for _, tool := range result {
				resultNames[tool.Name()] = true
			}

			for _, expectedName := range tt.expectedNames {
				if !resultNames[expectedName] {
					t.Errorf("FilterSkillTools() missing expected tool %s", expectedName)
				}
			}
		})
	}
}

func TestPermissionChannelBufferConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant int
		expected int
	}{
		{"permission request buffer", permissionRequestChannelBuffer, 10},
		{"permission mention buffer", permissionMentionChannelBuffer, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("constant = %v, want %v", tt.constant, tt.expected)
			}
		})
	}
}

func TestDefaultPermissionPolicies(t *testing.T) {
	policies := DefaultPermissionPolicies()

	if len(policies) == 0 {
		t.Error("DefaultPermissionPolicies() returned empty slice")
	}

	// Check that we have some expected policies
	hasFileReadPolicy := false
	hasFileWritePolicy := false

	for _, policy := range policies {
		if policy.ToolPattern == "mcp_*_file_read" || policy.ToolPattern == "mcp_*_read_file" {
			hasFileReadPolicy = true
		}
		if policy.ToolPattern == "mcp_*_file_write" || policy.ToolPattern == "mcp_*_write_file" {
			hasFileWritePolicy = true
		}
	}

	if !hasFileReadPolicy {
		t.Error("DefaultPermissionPolicies() missing file read policy")
	}
	if !hasFileWritePolicy {
		t.Error("DefaultPermissionPolicies() missing file write policy")
	}
}

// Mock tool for testing
type mockTool struct {
	name string
}

func (m *mockTool) Name() string {
	return m.name
}

func (m *mockTool) Description() string {
	return "Mock tool for testing"
}

func (m *mockTool) Schema() skills.ToolSchema {
	return skills.ToolSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"input": map[string]interface{}{
				"type":        "string",
				"description": "Test input",
			},
		},
	}
}

func (m *mockTool) InputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"input": map[string]interface{}{
				"type":        "string",
				"description": "Test input",
			},
		},
	}
}

func (m *mockTool) Execute(ctx context.Context, params json.RawMessage) (skills.ToolResult, error) {
	return skills.ToolResult{
		Success: true,
		Content: "mock result",
	}, nil
}
