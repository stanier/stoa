package personas

import (
	"github.com/stanier/stoa/internal/agents"
	"github.com/stanier/stoa/internal/config"
)

// LoadPersona converts a config.PersonaConfig into an agents.Persona
func LoadPersona(cfg config.PersonaConfig) agents.Persona {
	return agents.NewBasePersona(
		cfg.Name,
		cfg.Description,
		cfg.SystemPrompt,
		cfg.Capabilities,
		cfg.Tools,
		cfg.MaxConcurrentTasks,
		parseThinkingStyle(cfg.ThinkingStyle),
		parseCommunicationPreference(cfg.CommunicationPreference),
	)
}

func parseThinkingStyle(s string) agents.ThinkingStyle {
	switch s {
	case "fast":
		return agents.ThinkingStyleFast
	case "deep":
		return agents.ThinkingStyleDeep
	case "creative":
		return agents.ThinkingStyleCreative
	case "precise":
		return agents.ThinkingStylePrecise
	default:
		return agents.ThinkingStyleFast
	}
}

func parseCommunicationPreference(s string) agents.CommunicationPreference {
	switch s {
	case "verbose":
		return agents.CommunicationVerbose
	case "terse":
		return agents.CommunicationTerse
	case "silent":
		return agents.CommunicationSilent
	default:
		return agents.CommunicationTerse
	}
}
