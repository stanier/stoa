package personas

import (
	"testing"

	"github.com/stanier/stoa/internal/agents"
	"github.com/stanier/stoa/internal/config"
)

func TestDefaultPersona(t *testing.T) {
	cfg := config.PersonaConfig{
		Name:                    "Default",
		Description:             "General-purpose conversational agent",
		SystemPrompt:            "You are a helpful AI assistant.",
		Capabilities:            []string{"conversation"},
		Tools:                   []string{"calculator"},
		MaxConcurrentTasks:      1,
		ThinkingStyle:           "fast",
		CommunicationPreference: "terse",
	}
	persona := LoadPersona(cfg)

	if persona.Name() != "Default" {
		t.Errorf("expected name 'Default', got '%s'", persona.Name())
	}

	if persona.SystemPrompt() == "" {
		t.Error("expected non-empty system prompt")
	}

	if persona.MaxConcurrentTasks() != 1 {
		t.Errorf("expected max concurrent tasks 1, got %d", persona.MaxConcurrentTasks())
	}

	if persona.ThinkingStyle() != agents.ThinkingStyleFast {
		t.Errorf("expected thinking style Fast, got %v", persona.ThinkingStyle())
	}

	if persona.CommunicationPreference() != agents.CommunicationTerse {
		t.Errorf("expected communication preference Terse, got %v", persona.CommunicationPreference())
	}

	caps := persona.Capabilities()
	if len(caps) == 0 {
		t.Error("expected non-empty capabilities")
	}
}

func TestCoderPersona(t *testing.T) {
	cfg := config.PersonaConfig{
		Name:                    "Coder",
		Description:             "Specialized in software development",
		SystemPrompt:            "You are an expert coder.",
		Capabilities:            []string{"code_generation"},
		Tools:                   []string{"calculator"},
		MaxConcurrentTasks:      1,
		ThinkingStyle:           "deep",
		CommunicationPreference: "verbose",
	}
	persona := LoadPersona(cfg)

	if persona.Name() != "Coder" {
		t.Errorf("expected name 'Coder', got '%s'", persona.Name())
	}

	if persona.Description() == "" {
		t.Error("expected non-empty description")
	}

	systemPrompt := persona.SystemPrompt()
	if systemPrompt == "" {
		t.Error("expected non-empty system prompt")
	}

	// Check for code-specific keywords in prompt
	if !contains(systemPrompt, "code") && !contains(systemPrompt, "coding") {
		t.Error("expected system prompt to mention coding")
	}

	if persona.ThinkingStyle() != agents.ThinkingStyleDeep {
		t.Errorf("expected thinking style Deep for coder, got %v", persona.ThinkingStyle())
	}

	if persona.CommunicationPreference() != agents.CommunicationVerbose {
		t.Errorf("expected communication preference Verbose for coder, got %v", persona.CommunicationPreference())
	}

	caps := persona.Capabilities()
	if !containsStr(caps, "code_generation") {
		t.Error("expected Coder to have code_generation capability")
	}
}

func TestAnalyzerPersona(t *testing.T) {
	cfg := config.PersonaConfig{
		Name:                    "Analyzer",
		Description:             "Specialized in code analysis",
		SystemPrompt:            "You are an analyzer specializing in review.",
		Capabilities:            []string{"code_review"},
		Tools:                   []string{"calculator"},
		MaxConcurrentTasks:      1,
		ThinkingStyle:           "deep",
		CommunicationPreference: "verbose",
	}
	persona := LoadPersona(cfg)

	if persona.Name() != "Analyzer" {
		t.Errorf("expected name 'Analyzer', got '%s'", persona.Name())
	}

	systemPrompt := persona.SystemPrompt()
	if !contains(systemPrompt, "analysis") && !contains(systemPrompt, "review") {
		t.Error("expected system prompt to mention analysis or review")
	}

	caps := persona.Capabilities()
	if !containsStr(caps, "code_review") {
		t.Error("expected Analyzer to have code_review capability")
	}

	if persona.ThinkingStyle() != agents.ThinkingStyleDeep {
		t.Errorf("expected thinking style Deep for analyzer, got %v", persona.ThinkingStyle())
	}
}

func TestResearcherPersona(t *testing.T) {
	cfg := config.PersonaConfig{
		Name:                    "Researcher",
		Description:             "Specialized in research",
		SystemPrompt:            "You are a researcher.",
		Capabilities:            []string{"research"},
		Tools:                   []string{"calculator"},
		MaxConcurrentTasks:      2,
		ThinkingStyle:           "deep",
		CommunicationPreference: "verbose",
	}
	persona := LoadPersona(cfg)

	if persona.Name() != "Researcher" {
		t.Errorf("expected name 'Researcher', got '%s'", persona.Name())
	}

	systemPrompt := persona.SystemPrompt()
	if !contains(systemPrompt, "research") {
		t.Error("expected system prompt to mention research")
	}

	caps := persona.Capabilities()
	if !containsStr(caps, "research") {
		t.Error("expected Researcher to have research capability")
	}

	// Researcher should allow more concurrent tasks
	if persona.MaxConcurrentTasks() < 2 {
		t.Errorf("expected Researcher to support at least 2 concurrent tasks, got %d", persona.MaxConcurrentTasks())
	}
}

func TestPlannerPersona(t *testing.T) {
	cfg := config.PersonaConfig{
		Name:                    "Planner",
		Description:             "Specialized in planning",
		SystemPrompt:            "You are a planner specializing in architecture.",
		Capabilities:            []string{"task_planning"},
		Tools:                   []string{"calculator"},
		MaxConcurrentTasks:      1,
		ThinkingStyle:           "deep",
		CommunicationPreference: "verbose",
	}
	persona := LoadPersona(cfg)

	if persona.Name() != "Planner" {
		t.Errorf("expected name 'Planner', got '%s'", persona.Name())
	}

	systemPrompt := persona.SystemPrompt()
	if !contains(systemPrompt, "plan") && !contains(systemPrompt, "architecture") {
		t.Error("expected system prompt to mention planning or architecture")
	}

	caps := persona.Capabilities()
	if !containsStr(caps, "task_planning") {
		t.Error("expected Planner to have task_planning capability")
	}

	if persona.ThinkingStyle() != agents.ThinkingStyleDeep {
		t.Errorf("expected thinking style Deep for planner, got %v", persona.ThinkingStyle())
	}
}

func TestAllPersonas_HaveRequiredFields(t *testing.T) {
	loadedCfg := []config.PersonaConfig{
		{Name: "Default", Description: "d", SystemPrompt: "p", Capabilities: []string{"c"}, Tools: []string{"t"}, MaxConcurrentTasks: 1, ThinkingStyle: "fast", CommunicationPreference: "terse"},
		{Name: "Coder", Description: "d", SystemPrompt: "p", Capabilities: []string{"c"}, Tools: []string{"t"}, MaxConcurrentTasks: 1, ThinkingStyle: "fast", CommunicationPreference: "terse"},
		{Name: "Analyzer", Description: "d", SystemPrompt: "p", Capabilities: []string{"c"}, Tools: []string{"t"}, MaxConcurrentTasks: 1, ThinkingStyle: "fast", CommunicationPreference: "terse"},
		{Name: "Researcher", Description: "d", SystemPrompt: "p", Capabilities: []string{"c"}, Tools: []string{"t"}, MaxConcurrentTasks: 1, ThinkingStyle: "fast", CommunicationPreference: "terse"},
		{Name: "Planner", Description: "d", SystemPrompt: "p", Capabilities: []string{"c"}, Tools: []string{"t"}, MaxConcurrentTasks: 1, ThinkingStyle: "fast", CommunicationPreference: "terse"},
	}

	var personas []agents.Persona
	for _, c := range loadedCfg {
		personas = append(personas, LoadPersona(c))
	}

	for _, persona := range personas {
		t.Run(persona.Name(), func(t *testing.T) {
			// Name should not be empty
			if persona.Name() == "" {
				t.Error("persona name should not be empty")
			}

			// Description should not be empty
			if persona.Description() == "" {
				t.Error("persona description should not be empty")
			}

			// System prompt should not be empty
			if persona.SystemPrompt() == "" {
				t.Error("persona system prompt should not be empty")
			}

			// Should have at least one capability
			if len(persona.Capabilities()) == 0 {
				t.Error("persona should have at least one capability")
			}

			// Should have at least one available tool
			if len(persona.AvailableTools()) == 0 {
				t.Error("persona should have at least one available tool")
			}

			// Max concurrent tasks should be positive
			if persona.MaxConcurrentTasks() <= 0 {
				t.Error("persona max concurrent tasks should be positive")
			}

			// Thinking style should be valid
			validThinkingStyles := map[agents.ThinkingStyle]bool{
				agents.ThinkingStyleFast:     true,
				agents.ThinkingStyleDeep:     true,
				agents.ThinkingStyleCreative: true,
				agents.ThinkingStylePrecise:  true,
			}
			if !validThinkingStyles[persona.ThinkingStyle()] {
				t.Errorf("invalid thinking style: %v", persona.ThinkingStyle())
			}

			// Communication preference should be valid
			validCommPrefs := map[agents.CommunicationPreference]bool{
				agents.CommunicationVerbose: true,
				agents.CommunicationTerse:   true,
				agents.CommunicationSilent:  true,
			}
			if !validCommPrefs[persona.CommunicationPreference()] {
				t.Errorf("invalid communication preference: %v", persona.CommunicationPreference())
			}
		})
	}
}

func TestBasePersona_Getters(t *testing.T) {
	persona := agents.NewBasePersona(
		"TestPersona",
		"Test description",
		"Test system prompt",
		[]string{"capability1", "capability2"},
		[]string{"tool1", "tool2"},
		3,
		agents.ThinkingStyleCreative,
		agents.CommunicationVerbose,
	)

	if persona.Name() != "TestPersona" {
		t.Errorf("expected name 'TestPersona', got '%s'", persona.Name())
	}

	if persona.Description() != "Test description" {
		t.Errorf("expected description 'Test description', got '%s'", persona.Description())
	}

	if persona.SystemPrompt() != "Test system prompt" {
		t.Errorf("expected system prompt 'Test system prompt', got '%s'", persona.SystemPrompt())
	}

	caps := persona.Capabilities()
	if len(caps) != 2 || caps[0] != "capability1" || caps[1] != "capability2" {
		t.Errorf("unexpected capabilities: %v", caps)
	}

	tools := persona.AvailableTools()
	if len(tools) != 2 || tools[0] != "tool1" || tools[1] != "tool2" {
		t.Errorf("unexpected tools: %v", tools)
	}

	if persona.MaxConcurrentTasks() != 3 {
		t.Errorf("expected max concurrent tasks 3, got %d", persona.MaxConcurrentTasks())
	}

	if persona.ThinkingStyle() != agents.ThinkingStyleCreative {
		t.Errorf("expected thinking style Creative, got %v", persona.ThinkingStyle())
	}

	if persona.CommunicationPreference() != agents.CommunicationVerbose {
		t.Errorf("expected communication preference Verbose, got %v", persona.CommunicationPreference())
	}
}

// Helper functions
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func containsStr(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
