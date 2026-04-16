package compaction

import (
	"context"
	"fmt"
	"strings"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/prompts"
)

// DefaultCompactor implements Compactor using LLM-based summarization
type DefaultCompactor struct {
	client           llm.Client
	tokenCounter     TokenCounter
	strategy         Strategy
	maxTokens        int
	compactThreshold int
	promptRegistry   *prompts.Registry
}

// NewDefaultCompactor creates a new default compactor
func NewDefaultCompactor(
	client llm.Client,
	tokenCounter TokenCounter,
	strategy Strategy,
	maxTokens int,
	compactThreshold int,
) *DefaultCompactor {
	return NewDefaultCompactorWithPrompts(
		client,
		tokenCounter,
		strategy,
		maxTokens,
		compactThreshold,
		nil,
	)
}

// NewDefaultCompactorWithPrompts creates a new default compactor with prompt registry
func NewDefaultCompactorWithPrompts(
	client llm.Client,
	tokenCounter TokenCounter,
	strategy Strategy,
	maxTokens int,
	compactThreshold int,
	promptRegistry *prompts.Registry,
) *DefaultCompactor {
	return &DefaultCompactor{
		client:           client,
		tokenCounter:     tokenCounter,
		strategy:         strategy,
		maxTokens:        maxTokens,
		compactThreshold: compactThreshold,
		promptRegistry:   promptRegistry,
	}
}

// ShouldCompact checks if compaction is needed
func (c *DefaultCompactor) ShouldCompact(messages []chat.Message) (bool, int, error) {
	tokens, err := c.tokenCounter.CountMessages(messages)
	if err != nil {
		return false, 0, err
	}

	return tokens >= c.compactThreshold, tokens, nil
}

// Compact performs the compaction
func (c *DefaultCompactor) Compact(ctx context.Context, messages []chat.Message) ([]chat.Message, error) {
	// Get current token count
	tokens, err := c.tokenCounter.CountMessages(messages)
	if err != nil {
		return nil, err
	}

	// Select messages to compact
	indicesToCompact := c.strategy.SelectMessagesToCompact(messages, tokens)
	if len(indicesToCompact) == 0 {
		// Nothing to compact
		return messages, nil
	}

	// Build list of messages to summarize
	var toSummarize []chat.Message
	for _, idx := range indicesToCompact {
		toSummarize = append(toSummarize, messages[idx])
	}

	// Generate summary
	summary, err := c.summarize(ctx, toSummarize)
	if err != nil {
		return nil, err
	}

	// Build new message list
	var newMessages []chat.Message

	// Add system messages that weren't compacted
	for i := 0; i < len(messages); i++ {
		if messages[i].Role == chat.RoleSystem && !contains(indicesToCompact, i) {
			newMessages = append(newMessages, messages[i])
		}
	}

	// Add summary as a summary message
	newMessages = append(newMessages, chat.NewSummaryMessage(summary))

	// Add remaining messages (not compacted)
	for i := 0; i < len(messages); i++ {
		if !contains(indicesToCompact, i) && messages[i].Role != chat.RoleSystem {
			newMessages = append(newMessages, messages[i])
		}
	}

	return newMessages, nil
}

// summarize generates a summary of the given messages
func (c *DefaultCompactor) summarize(ctx context.Context, messages []chat.Message) (string, error) {
	if len(messages) == 0 {
		return "No messages to summarize.", nil
	}

	// Build conversation text
	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString(fmt.Sprintf("%s: %s\n\n", msg.Role, msg.Content))
	}

	// Create summarization prompt
	var systemPrompt string

	// Try to use prompt from registry if available
	if c.promptRegistry != nil {
		if rendered, err := c.promptRegistry.Render("compaction", nil); err == nil {
			systemPrompt = rendered
		} else {
			// Fallback to default if rendering fails
			systemPrompt = "You are a conversation summarizer. Create a concise but comprehensive summary of the following conversation, preserving key information, decisions, and context."
		}
	} else {
		// Fallback to hardcoded default if no registry
		systemPrompt = "You are a conversation summarizer. Create a concise but comprehensive summary of the following conversation, preserving key information, decisions, and context."
	}

	summaryPrompt := []chat.Message{
		chat.NewSystemMessage(systemPrompt),
		chat.NewUserMessage(fmt.Sprintf("Summarize this conversation:\n\n%s", sb.String())),
	}

	// Get summary from LLM (no tools needed for summarization)
	summary, err := c.client.SendMessage(ctx, summaryPrompt, nil)
	if err != nil {
		return "", fmt.Errorf("failed to generate summary: %w", err)
	}

	return summary, nil
}

// contains checks if slice contains value
func contains(slice []int, value int) bool {
	for _, v := range slice {
		if v == value {
			return true
		}
	}
	return false
}
