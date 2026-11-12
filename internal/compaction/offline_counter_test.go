package compaction

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
)

// withoutTikToken simulates a host where tiktoken can't download its encoding.
func withoutTikToken(t *testing.T) {
	t.Helper()
	orig := loadTikToken
	loadTikToken = func(string) (*TikTokenCounter, error) {
		return nil, errors.New("encoding download blocked")
	}
	t.Cleanup(func() { loadTikToken = orig })
}

func TestNewBestEffortCounter_NoTikToken_UsesEstimate(t *testing.T) {
	withoutTikToken(t)

	c := NewBestEffortCounter("gpt-4o")
	if c == nil {
		t.Fatal("NewBestEffortCounter returned nil")
	}
	if _, ok := c.(*CharEstimateCounter); !ok {
		t.Fatalf("got %T, want *CharEstimateCounter", c)
	}
	n, err := c.Count("hello world, this is a test")
	if err != nil || n <= 0 {
		t.Fatalf("Count = %d, %v; want a positive estimate", n, err)
	}
}

// Regression: the Anthropic counter used to store a nil *TikTokenCounter in
// its fallback interface, so a failed API call panicked instead of estimating.
func TestAnthropicAPITokenCounter_NoTikToken_APIFailure_Estimates(t *testing.T) {
	withoutTikToken(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	counter, err := NewAnthropicAPITokenCounter(anthropicCfg(srv.URL))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := []chat.Message{chat.NewUserMessage("Hello world")}
	count, err := counter.CountMessages(msgs)
	if err != nil {
		t.Fatalf("CountMessages: %v", err)
	}
	if count <= 0 {
		t.Errorf("expected a positive estimate, got %d", count)
	}
	if n, err := counter.Count("Hello world"); err != nil || n <= 0 {
		t.Errorf("Count = %d, %v; want a positive estimate", n, err)
	}
}

func TestOpenAIAPITokenCounter_NoTikToken_Estimates(t *testing.T) {
	withoutTikToken(t)

	counter, err := NewOpenAIAPITokenCounter(&config.Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n, err := counter.CountMessages([]chat.Message{chat.NewUserMessage("Hello world")}); err != nil || n <= 0 {
		t.Errorf("CountMessages = %d, %v; want a positive estimate", n, err)
	}
	if n, err := counter.Count("Hello world"); err != nil || n <= 0 {
		t.Errorf("Count = %d, %v; want a positive estimate", n, err)
	}
}

func TestNewAnthropicAPITokenCounter_NilConfig(t *testing.T) {
	if _, err := NewAnthropicAPITokenCounter(nil); err == nil {
		t.Fatal("expected an error for a nil config")
	}
}

func TestNewOpenAIAPITokenCounter_NilConfig(t *testing.T) {
	if _, err := NewOpenAIAPITokenCounter(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
