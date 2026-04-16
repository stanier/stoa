package compaction

import (
	"github.com/stanier/stoa/internal/chat"
)

// SlidingWindowStrategy keeps the most recent N messages
type SlidingWindowStrategy struct {
	keepRecent int
}

// NewSlidingWindowStrategy creates a new sliding window strategy
func NewSlidingWindowStrategy(keepRecent int) *SlidingWindowStrategy {
	if keepRecent < 1 {
		keepRecent = 10
	}
	return &SlidingWindowStrategy{
		keepRecent: keepRecent,
	}
}

// SelectMessagesToCompact returns indices of messages to compact
func (s *SlidingWindowStrategy) SelectMessagesToCompact(messages []chat.Message, tokens int) []int {
	if len(messages) <= s.keepRecent {
		return nil
	}

	// Compact everything except the most recent N messages
	// Also preserve system messages
	var indices []int
	compactUntil := len(messages) - s.keepRecent

	for i := 0; i < compactUntil; i++ {
		// Skip system messages
		if messages[i].Role != chat.RoleSystem {
			indices = append(indices, i)
		}
	}

	return indices
}

// ImportanceBasedStrategy scores messages and keeps the most important ones
type ImportanceBasedStrategy struct {
	keepRecent int
}

// NewImportanceBasedStrategy creates a new importance-based strategy
func NewImportanceBasedStrategy(keepRecent int) *ImportanceBasedStrategy {
	if keepRecent < 1 {
		keepRecent = 10
	}
	return &ImportanceBasedStrategy{
		keepRecent: keepRecent,
	}
}

// SelectMessagesToCompact returns indices of messages to compact based on importance
func (s *ImportanceBasedStrategy) SelectMessagesToCompact(messages []chat.Message, tokens int) []int {
	if len(messages) <= s.keepRecent {
		return nil
	}

	// For now, this is similar to sliding window
	// In a more sophisticated implementation, we could:
	// - Score messages based on length, keywords, code blocks
	// - Keep messages that are referenced later
	// - Preserve question-answer pairs

	var indices []int
	compactUntil := len(messages) - s.keepRecent

	for i := 0; i < compactUntil; i++ {
		// Skip system messages
		if messages[i].Role != chat.RoleSystem {
			indices = append(indices, i)
		}
	}

	return indices
}
