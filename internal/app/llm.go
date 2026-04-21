package app

import (
	"context"
	"path/filepath"
	"time"

	"github.com/stanier/stoa/internal/agentskills"
	"github.com/stanier/stoa/internal/agents/personas"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/prompts"
	"github.com/stanier/stoa/internal/skills"
)

// SetupLLMClient creates an LLM client based on the configured provider.
// Returns nil if config is nil or client creation fails (error is logged).
func SetupLLMClient(cfg *config.Config) llm.Client {
	if cfg == nil {
		return nil
	}

	var client llm.Client
	var err error

	switch cfg.LLM.Provider {
	case "anthropic":
		client, err = llm.NewAnthropicClient(cfg)
	case "gemini":
		client, err = llm.NewGeminiClient(cfg.LLM.APIKey, cfg.LLM.APIURL, cfg.LLM.Model, cfg.LLM.MaxTokens, nil)
	case "openai":
		fallthrough // openai and any unrecognized provider use the OpenAI-compatible client
	default:
		client, err = llm.NewOpenAIClient(cfg)
	}

	if err != nil {
		logging.Warnf("Failed to create LLM client (provider %q): %v", cfg.LLM.Provider, err)
	}
	return client
}

// SetupProviderRegistry builds a multi-provider registry and starts background
// model discovery. The "default" entry corresponds to the primary LLMConfig.
// Additional providers come from cfg.LLMProviders.
func SetupProviderRegistry(cfg *config.Config, ctx context.Context, refreshInterval time.Duration) *llm.ProviderRegistry {
	if cfg == nil {
		return nil
	}
	reg := llm.NewProviderRegistry(cfg)
	reg.StartPeriodicRefresh(ctx, refreshInterval)
	return reg
}

// LLMContext holds everything an LLM call needs: a system prompt and a tool list.
// Both fields are derived from the same filtered view of the skill registry, so
// the tools mentioned in the system prompt text exactly match the tools the LLM
// can actually invoke. Never populate the two fields independently.
type LLMContext struct {
	SystemPrompt string
	Tools        []llm.Tool
}

// BuildLLMContext is the single source of truth for constructing LLM call context.
//
// It:
//  1. Determines which tools are allowed based on the persona's AvailableTools()
//     patterns (or all tools when personaName is "").
//  2. Builds the system prompt template data from that filtered tool set so that
//     {{.Tools}} in the template only lists what is actually callable.
//  3. Renders the system prompt (persona template → assistant template → fallback).
//  4. Returns both the system prompt and the ready-to-use []llm.Tool slice as a
//     coherent unit.
//
// Use this everywhere — server message handling, server agentic loop, TUI streaming —
// so that the system prompt text and API tool definitions are always consistent.
func BuildLLMContext(
	cfg *config.Config,
	registry *skills.Registry,
	promptRegistry *prompts.Registry,
	personaRegistry *personas.Registry,
	personaName string,
	additionalFilter []string,
	agentSkillsMeta ...*agentskills.SkillMetadata,
) LLMContext {
	model := ""
	provider := ""
	if cfg != nil {
		model = cfg.LLM.Model
		provider = cfg.LLM.Provider
	}

	// Step 1: resolve allowed tool patterns from persona.
	allowedPatterns := resolveAllowedPatterns(personaName, personaRegistry)

	// Step 2: filter the registry to the allowed subset.
	filteredSkillTools := FilterSkillTools(listTools(registry), allowedPatterns)

	// Apply any additional caller-supplied filter (e.g. role-based AllowedTools).
	if len(additionalFilter) > 0 {
		filteredSkillTools = FilterSkillTools(filteredSkillTools, additionalFilter)
	}

	commands := listCommands(registry)

	// Step 3: build template data from the filtered tool set.
	templateData := prompts.BuildTemplateDataFromSlices(filteredSkillTools, commands, model, provider)
	if len(agentSkillsMeta) > 0 {
		templateData.AgentSkills = agentskills.FormatXML(agentSkillsMeta)
	}

	// Step 4: render system prompt.
	systemPrompt := renderSystemPrompt(personaName, personaRegistry, promptRegistry, templateData)

	// Step 5: convert filtered skills.Tool → llm.Tool for API calls.
	llmTools := skillsToLLMTools(filteredSkillTools)

	return LLMContext{
		SystemPrompt: systemPrompt,
		Tools:        llmTools,
	}
}

// resolveAllowedPatterns returns the tool name glob patterns for the given persona.
// Returns nil (all tools allowed) when no matching persona is found.
func resolveAllowedPatterns(personaName string, personaRegistry *personas.Registry) []string {
	if personaName == "" || personaRegistry == nil {
		return nil
	}
	if persona, ok := personaRegistry.GetPersona(personaName); ok {
		patterns := persona.AvailableTools()
		logging.Debugf("Using persona '%s' with %d tool patterns", personaName, len(patterns))
		return patterns
	}
	logging.Warnf("Persona '%s' not found in registry; falling back to 'default' persona.", personaName)
	if defaultPersona, ok := personaRegistry.GetPersona("default"); ok {
		return defaultPersona.AvailableTools()
	}
	logging.Warnf("No persona configs found; all tools will be available. Add persona YAML files to ~/.config/stoa/ to enable tool filtering.")
	return nil
}

// skillsToLLMTools converts a slice of skills.Tool to the llm.Tool format
// required by LLM API calls.
func skillsToLLMTools(tools []skills.Tool) []llm.Tool {
	out := make([]llm.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, llm.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
		})
	}
	return out
}

// listTools safely returns all tools from a registry, handling nil.
func listTools(registry *skills.Registry) []skills.Tool {
	if registry == nil {
		return nil
	}
	return registry.ListTools()
}

// listCommands safely returns all commands from a registry, handling nil.
func listCommands(registry *skills.Registry) []skills.Command {
	if registry == nil {
		return nil
	}
	return registry.ListCommands()
}

// FilterSkillTools returns the subset of tools matching any of the glob patterns.
// When patterns is empty, all tools are returned (no restriction).
// This is exported so callers can inspect the filtered set independently when needed.
func FilterSkillTools(tools []skills.Tool, patterns []string) []skills.Tool {
	if len(patterns) == 0 {
		return tools
	}
	filtered := make([]skills.Tool, 0, len(tools))
	for _, t := range tools {
		for _, pattern := range patterns {
			if t.Name() == pattern {
				filtered = append(filtered, t)
				break
			}
			if matched, _ := filepath.Match(pattern, t.Name()); matched {
				filtered = append(filtered, t)
				break
			}
		}
	}
	return filtered
}

// renderSystemPrompt renders the system prompt using persona → built-in template → fallback.
func renderSystemPrompt(
	personaName string,
	personaRegistry *personas.Registry,
	promptRegistry *prompts.Registry,
	templateData *prompts.TemplateData,
) string {
	if personaName != "" && personaRegistry != nil {
		if persona, ok := personaRegistry.GetPersona(personaName); ok {
			if rendered, err := persona.RenderSystemPrompt(templateData); err == nil {
				logging.Infof("Using persona '%s' for system prompt", personaName)
				return rendered
			} else {
				logging.Warnf("Failed to render persona '%s': %v — using static prompt", personaName, err)
				return persona.SystemPrompt()
			}
		}
	}

	// Fall back to built-in persona template from the prompt registry.
	if personaName != "" && promptRegistry != nil {
		if rendered, err := promptRegistry.Render(personaName, templateData); err == nil {
			logging.Infof("Using built-in persona template '%s' for system prompt", personaName)
			return rendered
		}
	}

	if promptRegistry != nil {
		if systemPrompt, err := promptRegistry.Render("default", templateData); err == nil {
			return systemPrompt
		} else {
			logging.Warnf("Failed to render default prompt: %v", err)
		}
	}

	return "You are a helpful AI assistant. You can use tools when needed to help answer questions."
}
