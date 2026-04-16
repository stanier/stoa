//go:build ignore

// Package prompts provides centralized prompt management
// This is a SKETCH showing the API design - not production code
package prompts

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Embed default prompts at compile time
//go:embed defaults/*.txt defaults/personas/*.txt
var defaultPrompts embed.FS

// PromptType categorizes prompts
type PromptType string

const (
	PromptAssistant  PromptType = "assistant"
	PromptCompaction PromptType = "compaction"
	PromptPersona    PromptType = "persona"
)

// Prompt represents a system prompt with metadata
type Prompt struct {
	// Metadata (from YAML front matter)
	Name         string                 `yaml:"name"`
	Type         PromptType             `yaml:"type"`
	Version      string                 `yaml:"version"`
	Description  string                 `yaml:"description"`
	Capabilities []string               `yaml:"capabilities,omitempty"`
	Metadata     map[string]interface{} `yaml:",inline"`

	// Content (after YAML front matter)
	Content string

	// Parsed template
	template *template.Template
}

// Registry manages all prompts
type Registry struct {
	mu          sync.RWMutex
	prompts     map[string]*Prompt
	overrideDir string
	useEmbedded bool
	hotReload   bool
}

// RegistryOption configures the registry
type RegistryOption func(*Registry)

// WithEmbeddedDefaults enables embedded default prompts
func WithEmbeddedDefaults() RegistryOption {
	return func(r *Registry) {
		r.useEmbedded = true
	}
}

// WithOverrideDir sets the directory for prompt overrides
func WithOverrideDir(dir string) RegistryOption {
	return func(r *Registry) {
		r.overrideDir = expandPath(dir)
	}
}

// WithHotReload enables hot reloading of prompt files
func WithHotReload(enabled bool) RegistryOption {
	return func(r *Registry) {
		r.hotReload = enabled
	}
}

// NewRegistry creates a new prompt registry
func NewRegistry(opts ...RegistryOption) (*Registry, error) {
	r := &Registry{
		prompts:     make(map[string]*Prompt),
		useEmbedded: true,
	}

	// Apply options
	for _, opt := range opts {
		opt(r)
	}

	// Load embedded defaults
	if r.useEmbedded {
		if err := r.loadEmbedded(); err != nil {
			return nil, fmt.Errorf("failed to load embedded prompts: %w", err)
		}
	}

	// Load overrides
	if r.overrideDir != "" {
		if err := r.loadOverrides(); err != nil {
			// Don't fail if override dir doesn't exist
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to load prompt overrides: %w", err)
			}
		}
	}

	return r, nil
}

// Get retrieves a prompt by name
func (r *Registry) Get(name string) (*Prompt, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.prompts[name]
	if !ok {
		return nil, fmt.Errorf("prompt not found: %s", name)
	}

	return p, nil
}

// Render renders a prompt with template variables
func (r *Registry) Render(name string, data interface{}) (string, error) {
	p, err := r.Get(name)
	if err != nil {
		return "", err
	}

	// If no template, return content as-is
	if p.template == nil {
		return p.Content, nil
	}

	// Render template
	var buf strings.Builder
	if err := p.template.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render template: %w", err)
	}

	return buf.String(), nil
}

// List returns all available prompts
func (r *Registry) List() []*Prompt {
	r.mu.RLock()
	defer r.mu.RUnlock()

	prompts := make([]*Prompt, 0, len(r.prompts))
	for _, p := range r.prompts {
		prompts = append(prompts, p)
	}

	return prompts
}

// Watch watches for prompt file changes and reloads
func (r *Registry) Watch(ctx context.Context) error {
	if !r.hotReload || r.overrideDir == "" {
		return nil
	}

	// Implementation would use fsnotify to watch directory
	// For now, this is just a placeholder
	return nil
}

// loadEmbedded loads prompts from embedded filesystem
func (r *Registry) loadEmbedded() error {
	return fs.WalkDir(defaultPrompts, "defaults", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, ".txt") {
			return nil
		}

		data, err := defaultPrompts.ReadFile(path)
		if err != nil {
			return err
		}

		prompt, err := parsePrompt(string(data))
		if err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}

		// Derive name from path if not specified
		if prompt.Name == "" {
			prompt.Name = strings.TrimSuffix(filepath.Base(path), ".txt")
		}

		r.prompts[prompt.Name] = prompt
		return nil
	})
}

// loadOverrides loads prompt overrides from filesystem
func (r *Registry) loadOverrides() error {
	if r.overrideDir == "" {
		return nil
	}

	return filepath.WalkDir(r.overrideDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, ".txt") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		prompt, err := parsePrompt(string(data))
		if err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}

		if prompt.Name == "" {
			prompt.Name = strings.TrimSuffix(filepath.Base(path), ".txt")
		}

		// Override existing prompt
		r.prompts[prompt.Name] = prompt
		return nil
	})
}

// parsePrompt parses a prompt file with optional YAML front matter
func parsePrompt(content string) (*Prompt, error) {
	prompt := &Prompt{}

	// Check for YAML front matter
	if strings.HasPrefix(content, "---\n") {
		parts := strings.SplitN(content, "---\n", 3)
		if len(parts) >= 3 {
			// Parse YAML
			if err := yaml.Unmarshal([]byte(parts[1]), prompt); err != nil {
				return nil, fmt.Errorf("failed to parse YAML front matter: %w", err)
			}

			// Content is after second ---
			prompt.Content = strings.TrimSpace(parts[2])
		} else {
			prompt.Content = content
		}
	} else {
		prompt.Content = content
	}

	// Parse as template
	tmpl, err := template.New("prompt").Parse(prompt.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}
	prompt.template = tmpl

	return prompt, nil
}

// expandPath expands ~ to home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	return path
}

// Example usage in UI model initialization:
/*
func NewModel(cfg *config.Config, ...) *Model {
	// Create prompt registry
	promptRegistry, err := prompts.NewRegistry(
		prompts.WithEmbeddedDefaults(),
		prompts.WithOverrideDir("~/.config/stoa/prompts"),
		prompts.WithHotReload(true),
	)
	if err != nil {
		log.Fatalf("Failed to initialize prompts: %v", err)
	}

	// Render system prompt with dynamic data
	systemPrompt, err := promptRegistry.Render("assistant", map[string]interface{}{
		"model":    cfg.LLM.Model,
		"provider": cfg.LLM.Provider,
		"date":     time.Now().Format("2006-01-02"),
		"tools":    getToolList(skillRegistry),
		"commands": getCommandList(skillRegistry),
	})
	if err != nil {
		log.Fatalf("Failed to render system prompt: %v", err)
	}

	// Initialize messages with system prompt
	messages := []chat.Message{
		chat.NewSystemMessage(systemPrompt),
	}

	return &Model{
		messages:       messages,
		promptRegistry: promptRegistry,
		// ... other fields
	}
}
*/
