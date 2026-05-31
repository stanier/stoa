package compaction

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
)

// --- AnthropicAPITokenCounter ---

func anthropicCfg(apiURL string) *config.Config {
	return &config.Config{
		LLM: config.LLMConfig{
			Providers: []config.LLMProviderConfig{{
				Type:   "anthropic",
				APIKey: "test-key",
				APIURL: apiURL,
				Model:  "claude-3-5-haiku-20241022",
			}},
		},
	}
}

func TestNewAnthropicAPITokenCounter_MissingKey(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Providers: []config.LLMProviderConfig{{Type: "anthropic", APIKey: ""}},
		},
	}
	_, err := NewAnthropicAPITokenCounter(cfg)
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}
}

func TestAnthropicAPITokenCounter_CountMessages_APISuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"input_tokens": 42})
	}))
	defer srv.Close()

	counter, err := NewAnthropicAPITokenCounter(anthropicCfg(srv.URL))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := []chat.Message{
		chat.NewUserMessage("Hello"),
		chat.NewAssistantMessage("Hi"),
	}
	count, err := counter.CountMessages(msgs)
	if err != nil {
		t.Fatalf("CountMessages error: %v", err)
	}
	if count != 42 {
		t.Errorf("got %d, want 42", count)
	}
}

func TestAnthropicAPITokenCounter_CountMessages_APIFailure_FallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	counter, err := NewAnthropicAPITokenCounter(anthropicCfg(srv.URL))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := []chat.Message{
		chat.NewUserMessage("Hello world"),
	}
	count, err := counter.CountMessages(msgs)
	if err != nil {
		t.Fatalf("CountMessages error on fallback: %v", err)
	}
	if count <= 0 {
		t.Errorf("expected positive count from fallback, got %d", count)
	}
}

func TestAnthropicAPITokenCounter_CountMessages_SkipsSystemMessages(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"input_tokens": 10})
	}))
	defer srv.Close()

	counter, err := NewAnthropicAPITokenCounter(anthropicCfg(srv.URL))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := []chat.Message{
		{Role: chat.RoleSystem, Content: "You are a helpful assistant."},
		chat.NewUserMessage("Hello"),
	}
	_, err = counter.CountMessages(msgs)
	if err != nil {
		t.Fatalf("CountMessages error: %v", err)
	}

	apiMsgs, _ := capturedBody["messages"].([]interface{})
	if len(apiMsgs) != 1 {
		t.Errorf("expected 1 message sent to API (system skipped), got %d", len(apiMsgs))
	}
}

func TestAnthropicAPITokenCounter_Count_FallsBackToTiktoken(t *testing.T) {
	counter, err := NewAnthropicAPITokenCounter(anthropicCfg("http://localhost:0"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	count, err := counter.Count("Hello, world!")
	if err != nil {
		t.Fatalf("Count error: %v", err)
	}
	if count <= 0 {
		t.Errorf("expected positive count, got %d", count)
	}
}

// --- OpenAIAPITokenCounter ---

func openAICfg() *config.Config {
	return &config.Config{
		LLM: config.LLMConfig{
			Providers: []config.LLMProviderConfig{{
				Type:  "openai",
				Model: "gpt-4o",
			}},
		},
	}
}

func TestNewOpenAIAPITokenCounter(t *testing.T) {
	c, err := NewOpenAIAPITokenCounter(openAICfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil counter")
	}
}

func TestOpenAIAPITokenCounter_CountMessages(t *testing.T) {
	c, _ := NewOpenAIAPITokenCounter(openAICfg())
	msgs := []chat.Message{
		chat.NewUserMessage("Hello"),
		chat.NewAssistantMessage("Hi there!"),
	}
	count, err := c.CountMessages(msgs)
	if err != nil {
		t.Fatalf("CountMessages error: %v", err)
	}
	if count <= 0 {
		t.Errorf("expected positive count, got %d", count)
	}
}

func TestOpenAIAPITokenCounter_Count(t *testing.T) {
	c, _ := NewOpenAIAPITokenCounter(openAICfg())
	count, err := c.Count("short text")
	if err != nil {
		t.Fatalf("Count error: %v", err)
	}
	if count <= 0 {
		t.Errorf("expected positive count, got %d", count)
	}
}

func TestOpenAIAPITokenCounter_RecordAndGetUsage(t *testing.T) {
	c, _ := NewOpenAIAPITokenCounter(openAICfg())

	if got := c.GetLastUsage(); len(got) != 0 {
		t.Errorf("expected empty usage initially, got %v", got)
	}

	usage := map[string]int{"prompt_tokens": 100, "completion_tokens": 50}
	c.RecordUsage(usage)

	got := c.GetLastUsage()
	if got["prompt_tokens"] != 100 || got["completion_tokens"] != 50 {
		t.Errorf("got %v, want %v", got, usage)
	}
}

func TestOpenAIAPITokenCounter_RecordUsage_NilNoOp(t *testing.T) {
	c, _ := NewOpenAIAPITokenCounter(openAICfg())
	c.RecordUsage(map[string]int{"prompt_tokens": 5})
	c.RecordUsage(nil) // should not overwrite
	if c.GetLastUsage()["prompt_tokens"] != 5 {
		t.Error("nil RecordUsage should not clear prior usage")
	}
}

// --- HybridTokenCounter ---

type alwaysFailCounter struct{}

func (a *alwaysFailCounter) Count(_ string) (int, error) {
	return 0, errTest
}
func (a *alwaysFailCounter) CountMessages(_ []chat.Message) (int, error) {
	return 0, errTest
}

type fixedCounter struct{ n int }

func (f *fixedCounter) Count(_ string) (int, error)              { return f.n, nil }
func (f *fixedCounter) CountMessages(_ []chat.Message) (int, error) { return f.n, nil }

var errTest = fmt.Errorf("test error")

func TestHybridTokenCounter_PrimarySucceeds(t *testing.T) {
	h := NewHybridTokenCounter(&fixedCounter{n: 77}, &fixedCounter{n: 99})
	count, err := h.CountMessages(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 77 {
		t.Errorf("got %d, want 77 (primary)", count)
	}
}

func TestHybridTokenCounter_FallsBackWhenPrimaryFails(t *testing.T) {
	h := NewHybridTokenCounter(&alwaysFailCounter{}, &fixedCounter{n: 55})
	count, err := h.CountMessages(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 55 {
		t.Errorf("got %d, want 55 (fallback)", count)
	}
}

func TestHybridTokenCounter_NilPrimaryUsesFallback(t *testing.T) {
	h := NewHybridTokenCounter(nil, &fixedCounter{n: 33})
	count, _ := h.CountMessages(nil)
	if count != 33 {
		t.Errorf("got %d, want 33", count)
	}
}

func TestHybridTokenCounter_BothNilReturnsEstimate(t *testing.T) {
	h := NewHybridTokenCounter(nil, nil)
	msgs := []chat.Message{chat.NewUserMessage("hello")}
	count, err := h.CountMessages(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// overhead (4) + len("hello")/4 (1) = 5
	if count <= 0 {
		t.Errorf("expected positive rough estimate, got %d", count)
	}
}

func TestHybridTokenCounter_Count_PrimarySucceeds(t *testing.T) {
	h := NewHybridTokenCounter(&fixedCounter{n: 10}, &fixedCounter{n: 20})
	count, _ := h.Count("anything")
	if count != 10 {
		t.Errorf("got %d, want 10", count)
	}
}

func TestHybridTokenCounter_Count_FallsBack(t *testing.T) {
	h := NewHybridTokenCounter(&alwaysFailCounter{}, &fixedCounter{n: 20})
	count, _ := h.Count("anything")
	if count != 20 {
		t.Errorf("got %d, want 20", count)
	}
}
