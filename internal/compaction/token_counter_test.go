package compaction

import (
	"testing"

	"github.com/stanier/stoa/internal/chat"
)

func TestTikTokenCounter_Count(t *testing.T) {
	counter := newTikTokenOrSkip(t, "gpt-4o")

	tests := []struct {
		name string
		text string
		// We'll just check that we get a positive count
		wantPositive bool
	}{
		{
			name:         "Empty string",
			text:         "",
			wantPositive: false,
		},
		{
			name:         "Simple text",
			text:         "Hello, world!",
			wantPositive: true,
		},
		{
			name:         "Long text",
			text:         "This is a longer piece of text that should contain multiple tokens.",
			wantPositive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, err := counter.Count(tt.text)
			if err != nil {
				t.Errorf("Count() error = %v", err)
				return
			}

			if tt.wantPositive && count <= 0 {
				t.Errorf("Count() = %d, want positive count", count)
			}
			if !tt.wantPositive && count > 0 {
				t.Errorf("Count() = %d, want zero count for empty string", count)
			}
		})
	}
}

func TestTikTokenCounter_CountMessages(t *testing.T) {
	counter := newTikTokenOrSkip(t, "gpt-4o")

	messages := []chat.Message{
		chat.NewUserMessage("Hello"),
		chat.NewAssistantMessage("Hi there!"),
		chat.NewUserMessage("How are you?"),
	}

	count, err := counter.CountMessages(messages)
	if err != nil {
		t.Errorf("CountMessages() error = %v", err)
		return
	}

	if count <= 0 {
		t.Errorf("CountMessages() = %d, want positive count", count)
	}

	// Each message has overhead (4 tokens), so minimum should be 3*4 = 12
	if count < 12 {
		t.Errorf("CountMessages() = %d, want at least 12 (3 messages * 4 overhead)", count)
	}
}

// newTikTokenOrSkip skips the test when tiktoken can't load its encoding,
// which it downloads at runtime (offline or egress-restricted hosts).
func newTikTokenOrSkip(t *testing.T, model string) *TikTokenCounter {
	t.Helper()
	counter, err := NewTikTokenCounter(model)
	if err != nil {
		t.Skipf("tiktoken encoding unavailable on this host: %v", err)
	}
	return counter
}
