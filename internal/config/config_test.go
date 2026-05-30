package config

import (
	"testing"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid OpenAI config",
			config: Config{
				LLM: LLMConfig{
					Providers: []LLMProviderConfig{{
						Type:      "openai",
						APIKey:    "sk-test-key",
						APIURL:    "https://api.openai.com/v1",
						Model:     "gpt-4o",
						MaxTokens: 4096,
					}},
				},
			},
			wantErr: false,
		},
		{
			name: "valid Anthropic config",
			config: Config{
				LLM: LLMConfig{
					Providers: []LLMProviderConfig{{
						Type:      "anthropic",
						APIKey:    "sk-ant-test-key",
						APIURL:    "https://api.anthropic.com",
						Model:     "claude-sonnet-4-5-20250929",
						MaxTokens: 4096,
					}},
				},
			},
			wantErr: false,
		},
		{
			name: "missing API key for OpenAI",
			config: Config{
				LLM: LLMConfig{
					Providers: []LLMProviderConfig{{
						Type:      "openai",
						APIKey:    "",
						APIURL:    "https://api.openai.com/v1",
						MaxTokens: 4096,
					}},
				},
			},
			wantErr: true,
			errMsg:  "OPENAI_API_KEY",
		},
		{
			name: "missing API key for Anthropic",
			config: Config{
				LLM: LLMConfig{
					Providers: []LLMProviderConfig{{
						Type:      "anthropic",
						APIKey:    "",
						APIURL:    "https://api.anthropic.com",
						MaxTokens: 4096,
					}},
				},
			},
			wantErr: true,
			errMsg:  "ANTHROPIC_API_KEY",
		},
		{
			name: "missing API URL",
			config: Config{
				LLM: LLMConfig{
					Providers: []LLMProviderConfig{{
						Type:      "openai",
						APIKey:    "sk-test-key",
						APIURL:    "",
						MaxTokens: 4096,
					}},
				},
			},
			wantErr: true,
			errMsg:  "API URL is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil {
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					// Check if error message contains expected substring
					if !contains(err.Error(), tt.errMsg) {
						t.Errorf("Config.Validate() error = %v, want error containing %v", err, tt.errMsg)
					}
				}
			}
		})
	}
}

func TestDefaultValues(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"default provider", DefaultProvider, "openai"},
		{"default OpenAI URL", DefaultOpenAIURL, "https://api.openai.com/v1"},
		{"default Anthropic URL", DefaultAnthropicURL, "https://api.anthropic.com"},
		{"default OpenAI model", DefaultOpenAIModel, "gpt-4o"},
		{"default Anthropic model", DefaultAnthropicModel, "claude-sonnet-4-5-20250929"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("constant = %v, want %v", tt.constant, tt.expected)
			}
		})
	}
}

func TestCompactionDefaults(t *testing.T) {
	if DefaultCompactionEnabled != true {
		t.Errorf("DefaultCompactionEnabled = %v, want true", DefaultCompactionEnabled)
	}
	if DefaultCompactionMaxTokens != 100000 {
		t.Errorf("DefaultCompactionMaxTokens = %v, want 100000", DefaultCompactionMaxTokens)
	}
	if DefaultCompactionThreshold != 80000 {
		t.Errorf("DefaultCompactionThreshold = %v, want 80000", DefaultCompactionThreshold)
	}
	if DefaultCompactionStrategy != "sliding_window" {
		t.Errorf("DefaultCompactionStrategy = %v, want 'sliding_window'", DefaultCompactionStrategy)
	}
	if DefaultCompactionKeepRecent != 10 {
		t.Errorf("DefaultCompactionKeepRecent = %v, want 10", DefaultCompactionKeepRecent)
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
