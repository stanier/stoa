package personas

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stanier/stoa/internal/agents"
	"github.com/stanier/stoa/internal/config"
	"github.com/spf13/viper"
)

// Registry manages loaded personas
type Registry struct {
	mu       sync.RWMutex
	personas map[string]agents.Persona
}

// NewRegistry creates a new persona registry
func NewRegistry() *Registry {
	return &Registry{
		personas: make(map[string]agents.Persona),
	}
}

// GetPersona retrieves a persona by name (case-insensitive)
func (r *Registry) GetPersona(name string) (agents.Persona, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.personas[strings.ToLower(name)]
	return p, ok
}

// Register adds a persona to the registry
func (r *Registry) Register(p agents.Persona) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.personas[strings.ToLower(p.Name())] = p
}

// LoadFromConfig scans the personas directory and loads all persona configs
func (r *Registry) LoadFromConfig(configDir string) error {
	personasDir := filepath.Join(configDir, "personas")

	// Create personas dir if it doesn't exist
	if err := os.MkdirAll(personasDir, 0755); err != nil {
		return fmt.Errorf("failed to create personas directory: %w", err)
	}

	entries, err := os.ReadDir(personasDir)
	if err != nil {
		return fmt.Errorf("failed to read personas directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		personaName := entry.Name()
		personaDir := filepath.Join(personasDir, personaName)
		configPath := filepath.Join(personaDir, "config.yaml")

		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			continue
		}

		// Load config using viper for consistency
		v := viper.New()
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("failed to read persona config for %s: %w", personaName, err)
		}

		var pCfg config.PersonaConfig
		if err := v.Unmarshal(&pCfg); err != nil {
			return fmt.Errorf("failed to unmarshal persona config for %s: %w", personaName, err)
		}

		// Ensure name matches directory name if not set
		if pCfg.Name == "" {
			pCfg.Name = personaName
		}

		r.Register(LoadPersona(pCfg))
	}

	return nil
}

// ListPersonas returns all registered personas
func (r *Registry) ListPersonas() []agents.Persona {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]agents.Persona, 0, len(r.personas))
	for _, p := range r.personas {
		res = append(res, p)
	}
	return res
}
