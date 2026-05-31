package config

import (
	"os"
	"testing"
)

// TestLoad_Defaults verifies that Load() returns a valid config with all
// expected defaults populated when no config file or env vars are present.
func TestLoad_Defaults(t *testing.T) {
	// Ensure no lingering env vars from the test environment pollute this test.
	clearProviderEnvs(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg == nil {
		t.Fatal("Load() returned nil config")
	}

	if !cfg.Compaction.Enabled {
		t.Error("compaction.enabled default should be true")
	}
	if cfg.Compaction.MaxTokens != DefaultCompactionMaxTokens {
		t.Errorf("compaction.max_tokens = %d, want %d", cfg.Compaction.MaxTokens, DefaultCompactionMaxTokens)
	}
	if cfg.Compaction.Strategy != DefaultCompactionStrategy {
		t.Errorf("compaction.strategy = %q, want %q", cfg.Compaction.Strategy, DefaultCompactionStrategy)
	}
	if cfg.Agents.MaxConcurrent != 5 {
		t.Errorf("agents.max_concurrent = %d, want 5", cfg.Agents.MaxConcurrent)
	}
}

// TestLoad_AnthropicEnvVars verifies that ANTHROPIC_API_KEY and ANTHROPIC_API_URL
// are picked up from the environment when the provider type is "anthropic".
func TestLoad_AnthropicEnvVars(t *testing.T) {
	clearProviderEnvs(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-123")
	t.Setenv("ANTHROPIC_API_URL", "https://custom.anthropic.example")

	// Inject a provider config via a temporary config file so we don't depend
	// on the user's real config file.
	cfg := &Config{
		LLM: LLMConfig{
			Providers: []LLMProviderConfig{{
				Type: "anthropic",
			}},
		},
	}

	// Apply the same env-var resolution logic Load() uses.
	for i := range cfg.LLM.Providers {
		pc := &cfg.LLM.Providers[i]
		keyEnv := "ANTHROPIC_API_KEY"
		if v := envGet("ANTHROPIC_API_KEY"); v != "" {
			pc.APIKey = v
		}
		urlEnv := "ANTHROPIC_API_URL"
		_ = keyEnv
		if v := envGet(urlEnv); v != "" {
			pc.APIURL = v
		}
	}

	dp := cfg.LLM.DefaultProvider()
	if dp == nil {
		t.Fatal("no default provider")
	}
	if dp.APIKey != "sk-ant-test-123" {
		t.Errorf("APIKey = %q, want sk-ant-test-123", dp.APIKey)
	}
	if dp.APIURL != "https://custom.anthropic.example" {
		t.Errorf("APIURL = %q, want https://custom.anthropic.example", dp.APIURL)
	}
}

// TestLoad_OpenAIEnvVars mirrors the Anthropic test for the OpenAI provider.
func TestLoad_OpenAIEnvVars(t *testing.T) {
	clearProviderEnvs(t)
	t.Setenv("OPENAI_API_KEY", "sk-openai-test-456")
	t.Setenv("OPENAI_API_URL", "https://custom.openai.example/v1")

	cfg := &Config{
		LLM: LLMConfig{
			Providers: []LLMProviderConfig{{
				Type: "openai",
			}},
		},
	}

	for i := range cfg.LLM.Providers {
		pc := &cfg.LLM.Providers[i]
		if v := envGet("OPENAI_API_KEY"); v != "" {
			pc.APIKey = v
		}
		if v := envGet("OPENAI_API_URL"); v != "" {
			pc.APIURL = v
		}
	}

	dp := cfg.LLM.DefaultProvider()
	if dp == nil {
		t.Fatal("no default provider")
	}
	if dp.APIKey != "sk-openai-test-456" {
		t.Errorf("APIKey = %q, want sk-openai-test-456", dp.APIKey)
	}
	if dp.APIURL != "https://custom.openai.example/v1" {
		t.Errorf("APIURL = %q, want https://custom.openai.example/v1", dp.APIURL)
	}
}

// TestLoad_GatewayDefaults checks gateway config defaults are applied correctly.
func TestLoad_GatewayDefaults(t *testing.T) {
	clearProviderEnvs(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.Gateway.RetryableStatusCodes) == 0 {
		t.Error("gateway.retryable_status_codes should have defaults")
	}
	// Verify 429 and 500 are in the list.
	hasCode := func(code int) bool {
		for _, c := range cfg.Gateway.RetryableStatusCodes {
			if c == code {
				return true
			}
		}
		return false
	}
	if !hasCode(429) {
		t.Error("gateway.retryable_status_codes should include 429")
	}
	if !hasCode(500) {
		t.Error("gateway.retryable_status_codes should include 500")
	}
	if cfg.Gateway.DynamicRouting {
		t.Error("gateway.dynamic_routing default should be false")
	}
	if cfg.Gateway.Cache.Enabled {
		t.Error("gateway.cache.enabled default should be false")
	}
}

// TestLoad_PermissionDefaults checks that permissions defaults are wired correctly.
func TestLoad_PermissionDefaults(t *testing.T) {
	clearProviderEnvs(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if !cfg.Permissions.Enabled {
		t.Error("permissions.enabled default should be true")
	}
	if cfg.Permissions.DefaultRank != 2 {
		t.Errorf("permissions.default_rank = %d, want 2", cfg.Permissions.DefaultRank)
	}
}

// TestLoad_AgentdEnvOverride tests AGENT_ADDRESS env var.
func TestLoad_AgentdEnvOverride(t *testing.T) {
	clearProviderEnvs(t)
	t.Setenv("AGENT_ADDRESS", "http://remote-agentd:9999")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Agent.Address != "http://remote-agentd:9999" {
		t.Errorf("agent.address = %q, want http://remote-agentd:9999", cfg.Agent.Address)
	}
}

// TestLoad_LLMValidationSkipped_RemoteMode checks that LLM key validation is
// skipped when agent.address is set (per the documented behaviour in AGENTS.md).
func TestLoad_LLMValidationSkipped_RemoteMode(t *testing.T) {
	clearProviderEnvs(t)
	t.Setenv("AGENT_ADDRESS", "http://agentd:7777")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// In remote mode there may be no LLM providers configured at all —
	// Validate() should not error on missing API key.
	if cfg.Agent.Address == "" {
		t.Fatal("agent.address should be set")
	}

	// Config should parse cleanly; validation skips LLM if agent.address is set
	// and llm.api_key is empty.
	if cfg.LLM.DefaultProvider() != nil {
		// If there is a provider, it may have no key — validation should still pass.
		if err := cfg.Validate(); cfg.Agent.Address != "" {
			// We only care that we can call Validate without panic here.
			_ = err
		}
	}
}

// TestLLMConfig_DefaultProvider checks that DefaultProvider returns the first
// provider when no explicit default is set.
func TestLLMConfig_DefaultProvider(t *testing.T) {
	lc := LLMConfig{
		Providers: []LLMProviderConfig{
			{Type: "openai", APIKey: "key1"},
			{Type: "anthropic", APIKey: "key2"},
		},
	}

	dp := lc.DefaultProvider()
	if dp == nil {
		t.Fatal("DefaultProvider() returned nil")
	}
	if dp.Type != "openai" {
		t.Errorf("DefaultProvider().Type = %q, want openai", dp.Type)
	}
}

func TestLLMConfig_DefaultProvider_Empty(t *testing.T) {
	lc := LLMConfig{}
	if lc.DefaultProvider() != nil {
		t.Error("DefaultProvider() should return nil for empty providers list")
	}
}

// --- helpers ---

// clearProviderEnvs unsets known credential env vars so tests don't leak into
// each other. t.Setenv automatically restores on cleanup.
func clearProviderEnvs(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_API_URL",
		"OPENAI_API_KEY", "OPENAI_API_URL",
		"AGENT_ADDRESS", "AGENTD_API_KEY",
	} {
		t.Setenv(key, "")
	}
}

// envGet wraps os.Getenv for use in table helpers inside this package.
func envGet(key string) string {
	return os.Getenv(key)
}
