package compaction

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
)

// AnthropicAPITokenCounter uses Anthropic's native token counting API
type AnthropicAPITokenCounter struct {
	apiKey     string
	apiURL     string
	model      string
	fallback   TokenCounter // Fallback to tiktoken if API fails
	httpClient *http.Client
}

// NewAnthropicAPITokenCounter creates a new Anthropic API token counter
func NewAnthropicAPITokenCounter(cfg *config.Config) (*AnthropicAPITokenCounter, error) {
	if cfg == nil || cfg.LLM.APIKey == "" {
		return nil, fmt.Errorf("config and API key required")
	}

	// Get fallback tiktoken counter
	fallback, err := NewTikTokenCounter(cfg.LLM.Model)
	if err != nil {
		// Fallback can be nil if tiktoken fails
		fallback = nil
	}

	apiURL := cfg.LLM.APIURL
	if apiURL == "" {
		apiURL = "https://api.anthropic.com"
	}

	return &AnthropicAPITokenCounter{
		apiKey:     cfg.LLM.APIKey,
		apiURL:     apiURL,
		model:      cfg.LLM.Model,
		fallback:   fallback,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}, nil
}

// CountMessages counts tokens using Anthropic's API
func (c *AnthropicAPITokenCounter) CountMessages(messages []chat.Message) (int, error) {
	// Convert messages to Anthropic format
	apiMessages := make([]map[string]interface{}, 0)

	for _, msg := range messages {
		// Skip system messages (they use separate system parameter)
		if msg.Role == chat.RoleSystem {
			continue
		}

		role := "user"
		if msg.Role == chat.RoleAssistant {
			role = "assistant"
		}

		apiMessages = append(apiMessages, map[string]interface{}{
			"role": role,
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": msg.Content,
				},
			},
		})
	}

	// Create request
	reqBody := map[string]interface{}{
		"model":    c.model,
		"messages": apiMessages,
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return c.fallbackCount(messages)
	}

	// Make API call with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", c.apiURL+"/v1/messages/count_tokens", bytes.NewReader(reqBytes))
	if err != nil {
		return c.fallbackCount(messages)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.fallbackCount(messages)
	}
	defer resp.Body.Close()

	// Parse response
	if resp.StatusCode != http.StatusOK {
		return c.fallbackCount(messages)
	}

	var result struct {
		InputTokens int `json:"input_tokens"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return c.fallbackCount(messages)
	}

	return result.InputTokens, nil
}

// Count counts tokens in text (fallback to tiktoken)
func (c *AnthropicAPITokenCounter) Count(text string) (int, error) {
	if c.fallback != nil {
		return c.fallback.Count(text)
	}
	// Rough estimate: ~4 characters per token
	return len(text) / 4, nil
}

// fallbackCount uses tiktoken fallback
func (c *AnthropicAPITokenCounter) fallbackCount(messages []chat.Message) (int, error) {
	if c.fallback != nil {
		return c.fallback.CountMessages(messages)
	}
	// Rough estimate
	total := 0
	for _, msg := range messages {
		total += 4 + len(msg.Content)/4 // 4 tokens for overhead + text estimate
	}
	return total, nil
}

// OpenAIAPITokenCounter extracts token usage from OpenAI API responses
// This is a passive counter that collects usage data from responses
type OpenAIAPITokenCounter struct {
	fallback TokenCounter
	lastUsage map[string]int // Track last known usage
}

// NewOpenAIAPITokenCounter creates a new OpenAI API token counter
func NewOpenAIAPITokenCounter(cfg *config.Config) (*OpenAIAPITokenCounter, error) {
	// Get fallback tiktoken counter
	fallback, err := NewTikTokenCounter(cfg.LLM.Model)
	if err != nil {
		fallback = nil
	}

	return &OpenAIAPITokenCounter{
		fallback:  fallback,
		lastUsage: make(map[string]int),
	}, nil
}

// CountMessages uses fallback for pre-counting (OpenAI doesn't have a count endpoint)
func (c *OpenAIAPITokenCounter) CountMessages(messages []chat.Message) (int, error) {
	if c.fallback != nil {
		return c.fallback.CountMessages(messages)
	}

	// Rough estimate
	total := 0
	for _, msg := range messages {
		total += 4 + len(msg.Content)/4
	}
	return total, nil
}

// Count counts tokens in text
func (c *OpenAIAPITokenCounter) Count(text string) (int, error) {
	if c.fallback != nil {
		return c.fallback.Count(text)
	}
	return len(text) / 4, nil
}

// RecordUsage records token usage from OpenAI API response
// Usage format: { "prompt_tokens": N, "completion_tokens": M }
func (c *OpenAIAPITokenCounter) RecordUsage(usage map[string]int) {
	if usage != nil {
		c.lastUsage = usage
	}
}

// GetLastUsage returns the last recorded token usage
func (c *OpenAIAPITokenCounter) GetLastUsage() map[string]int {
	return c.lastUsage
}

// HybridTokenCounter chains multiple counters with fallback
// Tries API first, falls back to tiktoken if API unavailable
type HybridTokenCounter struct {
	primary   TokenCounter
	fallback  TokenCounter
}

// NewHybridTokenCounter creates a hybrid counter with primary and fallback
func NewHybridTokenCounter(primary, fallback TokenCounter) *HybridTokenCounter {
	return &HybridTokenCounter{
		primary:  primary,
		fallback: fallback,
	}
}

// CountMessages tries primary counter first, falls back on error
func (c *HybridTokenCounter) CountMessages(messages []chat.Message) (int, error) {
	// Try primary first
	if c.primary != nil {
		if count, err := c.primary.CountMessages(messages); err == nil {
			return count, nil
		}
		// If primary fails, continue to fallback
	}

	// Use fallback
	if c.fallback != nil {
		return c.fallback.CountMessages(messages)
	}

	// Last resort: rough estimate
	total := 0
	for _, msg := range messages {
		total += 4 + len(msg.Content)/4
	}
	return total, nil
}

// Count tries primary first, falls back on error
func (c *HybridTokenCounter) Count(text string) (int, error) {
	if c.primary != nil {
		if count, err := c.primary.Count(text); err == nil {
			return count, nil
		}
	}

	if c.fallback != nil {
		return c.fallback.Count(text)
	}

	return len(text) / 4, nil
}
