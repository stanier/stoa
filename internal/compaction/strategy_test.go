package compaction

import (
	"testing"

	"github.com/stanier/stoa/internal/chat"
)

func TestSlidingWindowStrategy_SelectMessagesToCompact(t *testing.T) {
	messages := []chat.Message{
		chat.NewSystemMessage("System message"),
		chat.NewUserMessage("Message 1"),
		chat.NewAssistantMessage("Response 1"),
		chat.NewUserMessage("Message 2"),
		chat.NewAssistantMessage("Response 2"),
		chat.NewUserMessage("Message 3"),
		chat.NewAssistantMessage("Response 3"),
	}

	tests := []struct {
		name       string
		keepRecent int
		wantLen    int // Expected number of messages to compact
	}{
		{
			name:       "Keep 2 recent",
			keepRecent: 2,
			wantLen:    4, // Should compact messages 1-4 (indices 1,2,3,4), skip system and last 2
		},
		{
			name:       "Keep 5 recent",
			keepRecent: 5,
			wantLen:    1, // Should compact only message 1 (index 1)
		},
		{
			name:       "Keep all",
			keepRecent: 10,
			wantLen:    0, // Should not compact anything
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := NewSlidingWindowStrategy(tt.keepRecent)
			indices := strategy.SelectMessagesToCompact(messages, 1000)

			if len(indices) != tt.wantLen {
				t.Errorf("SelectMessagesToCompact() returned %d indices, want %d", len(indices), tt.wantLen)
			}

			// Verify no system messages are selected
			for _, idx := range indices {
				if messages[idx].Role == chat.RoleSystem {
					t.Errorf("SelectMessagesToCompact() selected system message at index %d", idx)
				}
			}
		})
	}
}

func TestImportanceBasedStrategy_SelectMessagesToCompact(t *testing.T) {
	messages := []chat.Message{
		chat.NewSystemMessage("System message"),
		chat.NewUserMessage("Message 1"),
		chat.NewAssistantMessage("Response 1"),
		chat.NewUserMessage("Message 2"),
		chat.NewAssistantMessage("Response 2"),
	}

	strategy := NewImportanceBasedStrategy(2)
	indices := strategy.SelectMessagesToCompact(messages, 1000)

	// Should compact messages except recent 2
	if len(indices) < 1 {
		t.Errorf("SelectMessagesToCompact() returned %d indices, want at least 1", len(indices))
	}

	// Verify no system messages are selected
	for _, idx := range indices {
		if messages[idx].Role == chat.RoleSystem {
			t.Errorf("SelectMessagesToCompact() selected system message at index %d", idx)
		}
	}
}
