package app

import (
	"github.com/stanier/stoa/internal/agentskills"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/skills"
	"github.com/stanier/stoa/internal/skills/builtin"
)

// SetupSkillRegistry creates a skill registry pre-loaded with all built-in commands
// and any tools listed in cfg.Skills.EnabledBuiltin.
//
// commandPrefix controls how the help command displays invocation syntax — pass ""
// to use the default "/" (TUI), or a custom string for other channels.
func SetupSkillRegistry(cfg *config.Config, commandPrefix string) *skills.Registry {
	registry := skills.NewRegistry()

	if commandPrefix == "" {
		commandPrefix = "/"
	}

	registry.RegisterCommand(builtin.NewHelpCommandWithPrefix(registry, commandPrefix))
	registry.RegisterCommand(builtin.NewClearCommand())
	registry.RegisterCommand(builtin.NewCompactCommand())
	registry.RegisterCommand(builtin.NewTokensCommand())
	registry.RegisterCommand(builtin.NewModelCommand())
	registry.RegisterCommand(builtin.NewPersonaCommand())
	registry.RegisterCommand(builtin.NewExitCommand())

	if cfg != nil {
		for _, toolName := range cfg.Skills.EnabledBuiltin {
			switch toolName {
			case "calculator":
				registry.RegisterTool(builtin.NewCalculatorTool())
			}
		}
	}

	// Register sub-agent stub tools so they appear in the LLM's tool list and
	// system prompt. Real implementations are wired per-session by agentd via
	// perSessionTools; executeToolCall checks per-session tools first so the
	// stubs are never actually executed during an agentd session.
	registerSubAgentStubs(registry)

	return registry
}

// registerSubAgentStubs adds the four sub-agent tool stubs to the registry.
func registerSubAgentStubs(registry *skills.Registry) {
	registry.RegisterTool(builtin.NewSubAgentStubTool(
		"spawn_agent",
		"Spawn an async sub-agent to work on a task. Returns immediately with {agent_id}. "+
			"The agent runs concurrently and its result will be delivered as a user message when complete.",
		skills.ToolSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"task":    map[string]interface{}{"type": "string", "description": "The task description for the sub-agent to work on"},
				"persona": map[string]interface{}{"type": "string", "description": "The persona for the sub-agent (e.g. 'coder', 'analyzer', 'default')"},
			},
			Required: []string{"task"},
		},
	))
	registry.RegisterTool(builtin.NewSubAgentStubTool(
		"message_agent",
		"Send a message to a running sub-agent. The agent must be awaiting input via wait_for_message.",
		skills.ToolSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"agent_id": map[string]interface{}{"type": "string", "description": "The agent ID returned by spawn_agent"},
				"message":  map[string]interface{}{"type": "string", "description": "The message to send to the agent"},
			},
			Required: []string{"agent_id", "message"},
		},
	))
	registry.RegisterTool(builtin.NewSubAgentStubTool(
		"list_agents",
		"List all sub-agents for the current session with their status.",
		skills.ToolSchema{Type: "object", Properties: map[string]interface{}{}},
	))
	registry.RegisterTool(builtin.NewSubAgentStubTool(
		"cancel_agent",
		"Cancel a running sub-agent.",
		skills.ToolSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"agent_id": map[string]interface{}{"type": "string", "description": "The agent ID to cancel"},
			},
			Required: []string{"agent_id"},
		},
	))
}

// SetupAgentSkills discovers Agent Skills from the configured directories.
// Missing directories are silently skipped. Returns nil when no skills are found.
func SetupAgentSkills(cfg *config.Config) []*agentskills.SkillMetadata {
	if cfg == nil || len(cfg.Skills.Directories) == 0 {
		return nil
	}
	skillList, err := agentskills.DiscoverSkills(cfg.Skills.Directories)
	if err != nil {
		logging.Warnf("agentskills: discovery error: %v", err)
	}
	logging.Infof("agentskills: discovered %d skills", len(skillList))
	return skillList
}
