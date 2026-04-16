package app

import (
	"github.com/stanier/stoa/internal/agents/personas"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/prompts"
)

// SetupPersonaRegistry creates a persona registry and loads any user-defined
// personas from the config directory. Returns an initialized (possibly empty)
// registry — never nil.
func SetupPersonaRegistry() *personas.Registry {
	personaRegistry := personas.NewRegistry()
	configDir, err := config.GetConfigDir()
	if err == nil {
		if err := personaRegistry.LoadFromConfig(configDir); err != nil {
			logging.Warnf("Failed to load personas: %v", err)
		}
	}
	return personaRegistry
}

// SetupPromptRegistry creates and initializes a prompt registry from config.
// It loads embedded defaults and, when prompts are enabled, user override files.
func SetupPromptRegistry(cfg *config.Config) *prompts.Registry {
	if cfg == nil || !cfg.Prompts.Enabled {
		reg, err := prompts.NewRegistry(prompts.WithEmbeddedDefaults())
		if err != nil {
			logging.Warnf("Failed to initialize prompt registry: %v", err)
			return nil
		}
		return reg
	}

	reg, err := prompts.NewRegistry(
		prompts.WithEmbeddedDefaults(),
		prompts.WithOverrideDir(cfg.Prompts.OverrideDir),
	)
	if err != nil {
		logging.Warnf("Failed to initialize prompt registry: %v", err)
		return nil
	}
	return reg
}
