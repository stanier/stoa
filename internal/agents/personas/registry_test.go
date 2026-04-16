package personas

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistry_LoadFromConfig(t *testing.T) {
	// Create a temporary config directory
	tmpDir, err := os.MkdirTemp("", "persona-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	personasDir := filepath.Join(tmpDir, "personas")
	testPersonaDir := filepath.Join(personasDir, "testpersona")
	if err := os.MkdirAll(testPersonaDir, 0755); err != nil {
		t.Fatalf("failed to create persona dir: %v", err)
	}

	configPath := filepath.Join(testPersonaDir, "config.yaml")
	configContent := `
name: Test Persona
description: A test persona
system_prompt: You are a test persona.
capabilities:
  - testing
tools:
  - test_tool
max_concurrent_tasks: 3
thinking_style: creative
communication_preference: silent
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	registry := NewRegistry()
	if err := registry.LoadFromConfig(tmpDir); err != nil {
		t.Fatalf("failed to load from config: %v", err)
	}

	persona, ok := registry.GetPersona("Test Persona")
	if !ok {
		t.Fatal("expected persona 'Test Persona' to be registered")
	}

	if persona.Name() != "Test Persona" {
		t.Errorf("expected name 'Test Persona', got '%s'", persona.Name())
	}

	if persona.Description() != "A test persona" {
		t.Errorf("expected description 'A test persona', got '%s'", persona.Description())
	}

	if persona.MaxConcurrentTasks() != 3 {
		t.Errorf("expected max concurrent tasks 3, got %d", persona.MaxConcurrentTasks())
	}

	// Case-insensitive check
	persona2, ok := registry.GetPersona("test persona")
	if !ok || persona2 != persona {
		t.Fatal("expected case-insensitive retrieval to work")
	}
}
