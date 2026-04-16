package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewRegistry(t *testing.T) {
	tests := []struct {
		name    string
		opts    []RegistryOption
		wantErr bool
	}{
		{
			name:    "with embedded defaults",
			opts:    []RegistryOption{WithEmbeddedDefaults()},
			wantErr: false,
		},
		{
			name:    "with nonexistent override dir",
			opts:    []RegistryOption{WithEmbeddedDefaults(), WithOverrideDir("/nonexistent/path")},
			wantErr: false, // Should not error, just skip loading overrides
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(tt.opts...)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewRegistry() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRegistryGet(t *testing.T) {
	r, err := NewRegistry(WithEmbeddedDefaults())
	if err != nil {
		t.Fatalf("NewRegistry() failed: %v", err)
	}

	tests := []struct {
		name    string
		prompt  string
		wantErr bool
	}{
		{
			name:    "get assistant prompt",
			prompt:  "assistant",
			wantErr: false,
		},
		{
			name:    "get compaction prompt",
			prompt:  "compaction",
			wantErr: false,
		},
		{
			name:    "get coder persona",
			prompt:  "coder",
			wantErr: false,
		},
		{
			name:    "get analyzer persona",
			prompt:  "analyzer",
			wantErr: false,
		},
		{
			name:    "nonexistent prompt",
			prompt:  "nonexistent",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := r.Get(tt.prompt)
			if (err != nil) != tt.wantErr {
				t.Errorf("Get() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && p == nil {
				t.Error("Get() returned nil prompt")
			}
		})
	}
}

func TestRegistryList(t *testing.T) {
	r, err := NewRegistry(WithEmbeddedDefaults())
	if err != nil {
		t.Fatalf("NewRegistry() failed: %v", err)
	}

	prompts := r.List()
	if len(prompts) == 0 {
		t.Error("List() returned empty list, expected prompts")
	}

	// Check that we have the expected prompts
	expected := map[string]bool{
		"assistant":  false,
		"compaction": false,
		"coder":      false,
		"analyzer":   false,
		"default":    false,
	}

	for _, p := range prompts {
		if _, ok := expected[p.Name]; ok {
			expected[p.Name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("Expected prompt %q not found in list", name)
		}
	}
}

func TestRegistryListByType(t *testing.T) {
	r, err := NewRegistry(WithEmbeddedDefaults())
	if err != nil {
		t.Fatalf("NewRegistry() failed: %v", err)
	}

	tests := []struct {
		name        string
		promptType  PromptType
		minExpected int
	}{
		{
			name:        "list assistant type",
			promptType:  PromptAssistant,
			minExpected: 1,
		},
		{
			name:        "list persona type",
			promptType:  PromptPersona,
			minExpected: 3,
		},
		{
			name:        "list compaction type",
			promptType:  PromptCompaction,
			minExpected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompts := r.ListByType(tt.promptType)
			if len(prompts) < tt.minExpected {
				t.Errorf("ListByType() got %d prompts, expected at least %d", len(prompts), tt.minExpected)
			}
		})
	}
}

func TestPromptRender(t *testing.T) {
	r, err := NewRegistry(WithEmbeddedDefaults())
	if err != nil {
		t.Fatalf("NewRegistry() failed: %v", err)
	}

	tests := []struct {
		name      string
		prompt    string
		data      interface{}
		wantErr   bool
		wantMatch string
	}{
		{
			name:    "render prompt without variables",
			prompt:  "compaction",
			data:    nil,
			wantErr: false,
		},
		{
			name:   "render assistant with template data",
			prompt: "assistant",
			data: map[string]string{
				"model":    "claude-opus",
				"provider": "anthropic",
				"date":     "2025-02-11",
				"tools":    "- calculator",
				"commands": "- /help",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := r.Render(tt.prompt, tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("Render() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && result == "" {
				t.Error("Render() returned empty string")
			}
		})
	}
}

func TestParsePrompt(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantName    string
		wantType    string
		wantContent string
	}{
		{
			name: "prompt with yaml front matter",
			content: `---
name: test
type: system
version: v1.0.0
description: Test prompt
---
This is the content.`,
			wantName:    "test",
			wantType:    "system",
			wantContent: "This is the content.",
		},
		{
			name:        "prompt without yaml front matter",
			content:     `This is a simple prompt without metadata.`,
			wantContent: "This is a simple prompt without metadata.",
		},
		{
			name:        "prompt with template variables",
			content:     `Hello {{.name}}, your model is {{.model}}.`,
			wantContent: "Hello {{.name}}, your model is {{.model}}.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := parsePrompt(tt.content)
			if err != nil {
				t.Errorf("parsePrompt() error = %v", err)
				return
			}

			if tt.wantName != "" && p.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", p.Name, tt.wantName)
			}

			if tt.wantContent != "" && p.Content != tt.wantContent {
				t.Errorf("Content = %q, want %q", p.Content, tt.wantContent)
			}

			if p.template == nil {
				t.Error("template is nil, expected *template.Template")
			}
		})
	}
}

func TestParsePromptErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name: "invalid yaml",
			content: `---
name: [invalid yaml structure
---
Content`,
			wantErr: true,
		},
		{
			name: "invalid template",
			content: `---
name: test
---
{{.name}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parsePrompt(tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("parsePrompt() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() failed: %v", err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "expand home directory",
			path: "~/.config/stoa",
			want: filepath.Join(home, ".config/stoa"),
		},
		{
			name: "absolute path unchanged",
			path: "/absolute/path",
			want: "/absolute/path",
		},
		{
			name: "relative path unchanged",
			path: "relative/path",
			want: "relative/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandPath(tt.path)
			if got != tt.want {
				t.Errorf("expandPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFileOverrides(t *testing.T) {
	// Create a temporary directory for overrides
	tmpDir := t.TempDir()

	// Create an override prompt
	overrideContent := `---
name: assistant
type: assistant
version: v2.0.0
description: Override assistant prompt
---
This is the overridden assistant prompt.`

	overridePath := filepath.Join(tmpDir, "assistant.txt")
	if err := os.WriteFile(overridePath, []byte(overrideContent), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	// Create registry with override dir
	r, err := NewRegistry(
		WithEmbeddedDefaults(),
		WithOverrideDir(tmpDir),
	)
	if err != nil {
		t.Fatalf("NewRegistry() failed: %v", err)
	}

	// Get the assistant prompt (should be overridden version)
	p, err := r.Get("assistant")
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	if p.Version != "v2.0.0" {
		t.Errorf("Version = %q, want v2.0.0", p.Version)
	}

	if !contains(p.Content, "overridden") {
		t.Errorf("Content should contain 'overridden', got: %q", p.Content)
	}
}

func TestTemplateRendering(t *testing.T) {
	content := `Model: {{.model}}
Provider: {{.provider}}
Date: {{.date}}`

	p, err := parsePrompt(content)
	if err != nil {
		t.Fatalf("parsePrompt() failed: %v", err)
	}

	data := map[string]string{
		"model":    "claude-opus",
		"provider": "anthropic",
		"date":     "2025-02-11",
	}

	var buf strings.Builder
	if err := p.template.Execute(&buf, data); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	result := buf.String()
	if !contains(result, "claude-opus") {
		t.Error("Result should contain model name")
	}
	if !contains(result, "anthropic") {
		t.Error("Result should contain provider")
	}
	if !contains(result, "2025-02-11") {
		t.Error("Result should contain date")
	}
}

func TestIntegrationTemplateRenderingWithComplexData(t *testing.T) {
	// Create a prompt with various template features
	content := `---
name: integration-test
type: system
version: v1.0.0
description: Integration test prompt
---

# System Configuration
- Model: {{.Model}}
- Provider: {{.Provider}}
- Date: {{.Date}}
- Environment: {{.Environment}}

## Available Features
{{range .Features}}
- {{.}}
{{end}}

## Capabilities
{{if .Tools}}
{{.Tools}}
{{else}}
No tools available
{{end}}

{{if .Commands}}
{{.Commands}}
{{else}}
No commands available
{{end}}`

	p, err := parsePrompt(content)
	if err != nil {
		t.Fatalf("parsePrompt() failed: %v", err)
	}

	// Create template data with features list
	data := map[string]interface{}{
		"Model":       "claude-opus",
		"Provider":    "anthropic",
		"Date":        "2025-02-11",
		"Environment": "Stoa",
		"Features": []string{
			"Conversation Compaction",
			"Sub-Agents",
			"MCP Support",
		},
		"Tools":    "Available tools: calculator, search",
		"Commands": "Available commands: /help, /clear",
	}

	var buf strings.Builder
	if err := p.template.Execute(&buf, data); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	result := buf.String()

	// Verify all expected content is present
	expectedStrings := []string{
		"claude-opus",
		"anthropic",
		"2025-02-11",
		"Stoa",
		"Conversation Compaction",
		"Sub-Agents",
		"MCP Support",
		"calculator",
		"search",
		"/help",
		"/clear",
	}

	for _, expected := range expectedStrings {
		if !contains(result, expected) {
			t.Errorf("Result missing expected string: %q", expected)
		}
	}

	// Verify structure
	if !contains(result, "# System Configuration") {
		t.Error("Result missing section header")
	}
	if !contains(result, "## Available Features") {
		t.Error("Result missing features header")
	}
}

// Helper functions

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			(len(substr) < len(s) && findSubstring(s, substr))))
}

func findSubstring(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
