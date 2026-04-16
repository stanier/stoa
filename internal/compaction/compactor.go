package compaction

import (
	"context"

	"github.com/stanier/stoa/internal/chat"
)

// Compactor handles conversation compaction
type Compactor interface {
	// ShouldCompact returns whether compaction is needed, current token count, and any error
	ShouldCompact(messages []chat.Message) (bool, int, error)

	// Compact performs compaction and returns new message list
	Compact(ctx context.Context, messages []chat.Message) ([]chat.Message, error)
}

// TokenCounter counts tokens in text and messages
type TokenCounter interface {
	Count(text string) (int, error)
	CountMessages(messages []chat.Message) (int, error)
}

// Strategy determines which messages to compact
type Strategy interface {
	SelectMessagesToCompact(messages []chat.Message, tokens int) []int
}
