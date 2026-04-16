package agentd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/llm"
)

// captureClient records every SendMessage call for inspection.
type captureClient struct {
	captured [][]chat.Message
	response string
}

func (c *captureClient) SendMessage(_ context.Context, msgs []chat.Message, _ []llm.Tool) (string, error) {
	cp := make([]chat.Message, len(msgs))
	copy(cp, msgs)
	c.captured = append(c.captured, cp)
	return c.response, nil
}

func (c *captureClient) StreamMessage(_ context.Context, _ []chat.Message, _ []llm.Tool) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

func (c *captureClient) ListModels(_ context.Context) ([]string, error) { return nil, nil }

func newTitlingServer(client llm.Client, model string) *Server {
	return &Server{
		cfg: &config.Config{
			Titling: config.TitlingConfig{
				Enabled: true,
				Model:   model,
			},
		},
		llmClient: client,
	}
}

func TestGenerateTitle_SimpleConversation(t *testing.T) {
	cap := &captureClient{response: "Hello World"}
	s := newTitlingServer(cap, "")

	msgs := []chat.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "What is the capital of France?"},
		{Role: "assistant", Content: "The capital of France is Paris."},
	}

	title := s.generateTitle(context.Background(), msgs)
	if title != "Hello World" {
		t.Errorf("unexpected title: %q", title)
	}
	if len(cap.captured) != 1 {
		t.Fatalf("expected 1 SendMessage call, got %d", len(cap.captured))
	}

	printMessages(t, "simple conversation", cap.captured[0])
}

func TestGenerateTitle_FiltersToolMessages(t *testing.T) {
	cap := &captureClient{response: "File Upload Test"}
	s := newTitlingServer(cap, "")

	msgs := []chat.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Please read my file"},
		{Role: "user", Content: "[Tool result for mcp_filesystem_read_file]: " + strings.Repeat("x", 10000)},
		{Role: "assistant", Content: "I read the file."},
		{Role: "user", Content: "[Tool error for mcp_filesystem_read_file]: path escapes root directory\nReview your parameters and try again."},
		{Role: "user", Content: "Can you try again?"},
		{Role: "assistant", Content: "Sure, let me retry."},
	}

	s.generateTitle(context.Background(), msgs)

	if len(cap.captured) != 1 {
		t.Fatalf("expected 1 call, got %d", len(cap.captured))
	}

	sent := cap.captured[0]
	printMessages(t, "conversation with tool messages", sent)

	// Verify no tool result/error content was sent.
	for _, m := range sent {
		if strings.HasPrefix(m.Content, "[Tool result for") || strings.HasPrefix(m.Content, "[Tool error for") {
			t.Errorf("tool message leaked into titling payload: role=%s content=%.80q", m.Role, m.Content)
		}
	}
}

func TestGenerateTitle_MaxContextWindow(t *testing.T) {
	cap := &captureClient{response: "Long Chat"}
	s := newTitlingServer(cap, "")

	// Build a 20-turn conversation.
	var msgs []chat.Message
	msgs = append(msgs, chat.Message{Role: "system", Content: "System prompt."})
	for i := 0; i < 10; i++ {
		msgs = append(msgs,
			chat.Message{Role: "user", Content: fmt.Sprintf("User turn %d", i+1)},
			chat.Message{Role: "assistant", Content: fmt.Sprintf("Assistant turn %d", i+1)},
		)
	}

	s.generateTitle(context.Background(), msgs)

	sent := cap.captured[0]
	printMessages(t, "long conversation (20 turns, expecting ≤8 messages sent)", sent)

	// system + up to 6 non-system + optional trailing user prompt = 8 max
	if len(sent) > 8 {
		t.Errorf("expected ≤8 messages sent, got %d", len(sent))
	}
}

func TestGenerateTitle_AttachmentMessagesIncluded(t *testing.T) {
	cap := &captureClient{response: "Attachment Chat"}
	s := newTitlingServer(cap, "")

	msgs := []chat.Message{
		{Role: "system", Content: "System prompt."},
		{Role: "user", Content: "[Attachments — use mcp_filesystem_read_file to read them]\n- report.pdf (path: /tmp/report.pdf, size: 1.2 MB)\n\nPlease summarize this report."},
		{Role: "assistant", Content: "I'll summarize the report for you."},
	}

	s.generateTitle(context.Background(), msgs)

	sent := cap.captured[0]
	printMessages(t, "conversation with attachment", sent)
}

// printMessages logs the full payload that would be sent to the LLM for titling.
func printMessages(t *testing.T, label string, msgs []chat.Message) {
	t.Helper()
	t.Logf("=== Titling payload: %s (%d messages) ===", label, len(msgs))
	totalBytes := 0
	for i, m := range msgs {
		t.Logf("  [%d] role=%-12s len=%-6d content=%.120q", i, m.Role, len(m.Content), m.Content)
		totalBytes += len(m.Content)
	}
	t.Logf("  total content bytes: %d", totalBytes)
}
