package llm

import (
	"fmt"
	"net/http"

	"github.com/stanier/stoa/internal/config"
)

// NewClientFromParams creates an LLM client from flat parameters.
// dialect must be "openai", "anthropic", or "gemini".
// httpClient may be nil to use the SDK default.
func NewClientFromParams(dialect, apiKey, apiURL, model string, maxTokens int, httpClient *http.Client) (Client, error) {
	switch dialect {
	case "anthropic":
		pc := &config.LLMProviderConfig{
			Type:      "anthropic",
			APIKey:    apiKey,
			APIURL:    apiURL,
			Model:     model,
			MaxTokens: maxTokens,
		}
		return newAnthropicClientWithHTTP(pc, httpClient)
	case "gemini":
		return NewGeminiClient(apiKey, apiURL, model, maxTokens, httpClient)
	case "openai":
		pc := &config.LLMProviderConfig{
			Type:      "openai",
			APIKey:    apiKey,
			APIURL:    apiURL,
			Model:     model,
			MaxTokens: maxTokens,
		}
		return newOpenAIClientWithHTTP(pc, httpClient)
	default:
		return nil, fmt.Errorf("llm: unknown dialect %q (want \"openai\", \"anthropic\", or \"gemini\")", dialect)
	}
}
