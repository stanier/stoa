package compaction

import (
	"fmt"

	"github.com/stanier/stoa/internal/chat"
	"github.com/pkoukk/tiktoken-go"
)

// TikTokenCounter implements TokenCounter using tiktoken
type TikTokenCounter struct {
	encoding *tiktoken.Tiktoken
}

// NewTikTokenCounter creates a new token counter for the given model
func NewTikTokenCounter(model string) (*TikTokenCounter, error) {
	encoding, err := tiktoken.EncodingForModel(model)
	if err != nil {
		// Fall back to cl100k_base (GPT-4, Claude)
		encoding, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			return nil, fmt.Errorf("failed to get encoding: %w", err)
		}
	}

	return &TikTokenCounter{
		encoding: encoding,
	}, nil
}

// Count returns the number of tokens in the text
func (c *TikTokenCounter) Count(text string) (int, error) {
	tokens := c.encoding.Encode(text, nil, nil)
	return len(tokens), nil
}

// CountMessages returns the total tokens across all messages
func (c *TikTokenCounter) CountMessages(messages []chat.Message) (int, error) {
	total := 0

	for _, msg := range messages {
		// Count role (approximately 4 tokens per message for formatting)
		total += 4

		// Count content
		count, err := c.Count(msg.Content)
		if err != nil {
			return 0, err
		}
		total += count
	}

	return total, nil
}

// CharEstimateCounter is a fast, zero-dependency token counter that estimates
// token count as (bytes / 4). This is a rough heuristic — English prose averages
// ~4 bytes per token for GPT/Claude tokenizers.
//
// TODO: Replace with server-returned token counts once the LLM client propagates
// usage statistics (input_tokens / output_tokens) from API responses. Until then,
// this estimate avoids tiktoken's first-call latency (encoding file load/download)
// without sacrificing the compaction threshold heuristic.
type CharEstimateCounter struct{}

// NewCharEstimateCounter returns a CharEstimateCounter. It never fails.
func NewCharEstimateCounter() *CharEstimateCounter {
	return &CharEstimateCounter{}
}

// Count estimates token count for a string.
func (c *CharEstimateCounter) Count(text string) (int, error) {
	return len(text)/4 + 1, nil
}

// CountMessages estimates total token count across a message slice.
func (c *CharEstimateCounter) CountMessages(messages []chat.Message) (int, error) {
	total := 0
	for _, msg := range messages {
		total += 4 // per-message overhead
		total += len(msg.Content)/4 + 1
	}
	return total, nil
}
