package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm/stats"
)

// mockClient is a test double for Client.
type mockClient struct {
	sendErr   error
	sendResp  string
	streamErr error
	called    int
}

func (m *mockClient) SendMessage(_ context.Context, _ []chat.Message, _ []Tool) (string, error) {
	m.called++
	return m.sendResp, m.sendErr
}

func (m *mockClient) StreamMessage(_ context.Context, _ []chat.Message, _ []Tool) (<-chan StreamChunk, error) {
	m.called++
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	ch := make(chan StreamChunk, 1)
	ch <- StreamChunk{Content: m.sendResp, Done: true}
	close(ch)
	return ch, nil
}

func (m *mockClient) ListModels(_ context.Context) ([]string, error) {
	m.called++
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	return []string{"model-a"}, nil
}

// buildRegistryWithMocks creates a ProviderRegistry pre-populated with named mock clients.
func buildRegistryWithMocks(entries map[string]*mockClient) *ProviderRegistry {
	r := &ProviderRegistry{
		clients: make(map[string]Client),
		models:  make(map[string][]string),
		types:   make(map[string]string),
	}
	for name, mc := range entries {
		r.clients[name] = mc
		r.types[name] = "openai"
		r.order = append(r.order, name)
	}
	return r
}

// retryableErr returns an error whose string contains "500", triggering isRetryable.
func retryableErr() error { return fmt.Errorf("http status 500 internal server error") }

// nonRetryableErr returns a plain error that is not retryable.
func nonRetryableErr() error { return errors.New("bad request 400") }

// ---------------------------------------------------------------------------
// SendMessage tests
// ---------------------------------------------------------------------------

func TestFallbackSendMessage_PrimarySucceeds(t *testing.T) {
	primary := &mockClient{sendResp: "hello"}
	secondary := &mockClient{sendResp: "world"}
	reg := buildRegistryWithMocks(map[string]*mockClient{
		"primary":   primary,
		"secondary": secondary,
	})
	fc := NewFallbackClient(reg, []string{"primary", "secondary"})

	resp, err := fc.SendMessage(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "hello" {
		t.Errorf("got %q, want %q", resp, "hello")
	}
	if secondary.called != 0 {
		t.Errorf("secondary should not have been called, got called=%d", secondary.called)
	}
}

func TestFallbackSendMessage_PrimaryRetryable_SecondarySucceeds(t *testing.T) {
	primary := &mockClient{sendErr: retryableErr()}
	secondary := &mockClient{sendResp: "fallback-response"}
	reg := buildRegistryWithMocks(map[string]*mockClient{
		"primary":   primary,
		"secondary": secondary,
	})
	fc := NewFallbackClient(reg, []string{"primary", "secondary"})

	resp, err := fc.SendMessage(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "fallback-response" {
		t.Errorf("got %q, want %q", resp, "fallback-response")
	}
	if primary.called != 1 {
		t.Errorf("primary called=%d, want 1", primary.called)
	}
	if secondary.called != 1 {
		t.Errorf("secondary called=%d, want 1", secondary.called)
	}
}

func TestFallbackSendMessage_PrimaryNonRetryable_SecondaryNotTried(t *testing.T) {
	primary := &mockClient{sendErr: nonRetryableErr()}
	secondary := &mockClient{sendResp: "should not reach"}
	reg := buildRegistryWithMocks(map[string]*mockClient{
		"primary":   primary,
		"secondary": secondary,
	})
	fc := NewFallbackClient(reg, []string{"primary", "secondary"})

	_, err := fc.SendMessage(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if secondary.called != 0 {
		t.Errorf("secondary should not have been called, got called=%d", secondary.called)
	}
}

func TestFallbackSendMessage_AllFail_LastErrorReturned(t *testing.T) {
	p1 := &mockClient{sendErr: retryableErr()}
	p2 := &mockClient{sendErr: fmt.Errorf("service 503 unavailable")}
	reg := buildRegistryWithMocks(map[string]*mockClient{
		"p1": p1,
		"p2": p2,
	})
	fc := NewFallbackClient(reg, []string{"p1", "p2"})

	_, err := fc.SendMessage(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The wrapped error chain should mention the last provider's error.
	if !errors.Is(err, p2.sendErr) {
		t.Errorf("expected last error to be wrapped, got: %v", err)
	}
}

func TestFallbackSendMessage_EmptyChain(t *testing.T) {
	reg := buildRegistryWithMocks(map[string]*mockClient{})
	fc := NewFallbackClient(reg, []string{})

	_, err := fc.SendMessage(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for empty chain, got nil")
	}
}

// ---------------------------------------------------------------------------
// StreamMessage tests
// ---------------------------------------------------------------------------

func TestFallbackStreamMessage_PrimarySucceeds(t *testing.T) {
	primary := &mockClient{sendResp: "streamed"}
	secondary := &mockClient{sendResp: "should not stream"}
	reg := buildRegistryWithMocks(map[string]*mockClient{
		"primary":   primary,
		"secondary": secondary,
	})
	fc := NewFallbackClient(reg, []string{"primary", "secondary"})

	ch, err := fc.StreamMessage(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var content string
	for chunk := range ch {
		content += chunk.Content
	}
	if content != "streamed" {
		t.Errorf("got %q, want %q", content, "streamed")
	}
	if secondary.called != 0 {
		t.Errorf("secondary should not have been called")
	}
}

func TestFallbackStreamMessage_PrimaryRetryable_SecondarySucceeds(t *testing.T) {
	primary := &mockClient{streamErr: retryableErr()}
	secondary := &mockClient{sendResp: "fallback-stream"}
	reg := buildRegistryWithMocks(map[string]*mockClient{
		"primary":   primary,
		"secondary": secondary,
	})
	fc := NewFallbackClient(reg, []string{"primary", "secondary"})

	ch, err := fc.StreamMessage(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var content string
	for chunk := range ch {
		content += chunk.Content
	}
	if content != "fallback-stream" {
		t.Errorf("got %q, want %q", content, "fallback-stream")
	}
}

func TestFallbackStreamMessage_EmptyChain(t *testing.T) {
	reg := buildRegistryWithMocks(map[string]*mockClient{})
	fc := NewFallbackClient(reg, []string{})

	_, err := fc.StreamMessage(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for empty chain, got nil")
	}
}

// ---------------------------------------------------------------------------
// orderedChain / dynamic routing tests
// ---------------------------------------------------------------------------

func TestOrderedChain_DynamicRoutingDisabled(t *testing.T) {
	reg := buildRegistryWithMocks(map[string]*mockClient{"a": {}, "b": {}, "c": {}})
	fc := NewFallbackClient(reg, []string{"a", "b", "c"})
	// dynamicRouting is false by default; orderedChain must return original order.
	got := fc.orderedChain(stats.NewRegistry())
	want := []string{"a", "b", "c"}
	if !stringSliceEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOrderedChain_DynamicRouting_NilRegistry(t *testing.T) {
	reg := buildRegistryWithMocks(map[string]*mockClient{"a": {}, "b": {}, "c": {}})
	fc := NewFallbackClient(reg, []string{"a", "b", "c"}).WithDynamicRouting(true)
	// nil stats registry → return original order unchanged.
	got := fc.orderedChain(nil)
	want := []string{"a", "b", "c"}
	if !stringSliceEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOrderedChain_DynamicRouting_NoStats_OriginalOrder(t *testing.T) {
	reg := buildRegistryWithMocks(map[string]*mockClient{"a": {}, "b": {}, "c": {}})
	fc := NewFallbackClient(reg, []string{"a", "b", "c"}).WithDynamicRouting(true)
	// Empty stats registry: all providers have zero samples → original order preserved.
	got := fc.orderedChain(stats.NewRegistry())
	want := []string{"a", "b", "c"}
	if !stringSliceEqual(got, want) {
		t.Errorf("got %v, want %v (all zero samples should keep original order)", got, want)
	}
}

func TestOrderedChain_DynamicRouting_LowerErrorRateFirst(t *testing.T) {
	// "b" is second in chain but has a lower error rate than "a".
	// With dynamic routing, "b" should be tried first.
	reg := buildRegistryWithMocks(map[string]*mockClient{"a": {}, "b": {}})
	statsReg := stats.NewRegistry()
	// Record 5 errors out of 5 for "a" (100% error rate).
	for i := 0; i < 5; i++ {
		statsReg.Get("a").Record(100, true)
	}
	// Record 5 successes for "b" (0% error rate).
	for i := 0; i < 5; i++ {
		statsReg.Get("b").Record(100, false)
	}

	fc := NewFallbackClient(reg, []string{"a", "b"}).WithDynamicRouting(true)
	fc.statsReg = statsReg

	got := fc.orderedChain(statsReg)
	if got[0] != "b" {
		t.Errorf("expected \"b\" first (lower error rate), got %v", got)
	}
}

func TestOrderedChain_DynamicRouting_EqualErrorRate_LowerP95First(t *testing.T) {
	// "c" is third in chain but has equal error rate and lower P95.
	reg := buildRegistryWithMocks(map[string]*mockClient{"a": {}, "b": {}, "c": {}})
	statsReg := stats.NewRegistry()

	// All providers: 0% errors but different latencies.
	statsReg.Get("a").Record(300, false)
	statsReg.Get("b").Record(200, false)
	statsReg.Get("c").Record(100, false)

	fc := NewFallbackClient(reg, []string{"a", "b", "c"}).WithDynamicRouting(true)
	fc.statsReg = statsReg

	got := fc.orderedChain(statsReg)
	want := []string{"c", "b", "a"}
	if !stringSliceEqual(got, want) {
		t.Errorf("got %v, want %v (lower P95 should sort first)", got, want)
	}
}

func TestOrderedChain_DynamicRouting_ZeroSamples_SortedAfterHealthy(t *testing.T) {
	// "c" has no samples; "a" and "b" are healthy. "c" should come last.
	reg := buildRegistryWithMocks(map[string]*mockClient{"a": {}, "b": {}, "c": {}})
	statsReg := stats.NewRegistry()

	statsReg.Get("a").Record(100, false)
	statsReg.Get("b").Record(150, false)
	// "c" intentionally has no samples recorded.

	fc := NewFallbackClient(reg, []string{"a", "b", "c"}).WithDynamicRouting(true)
	fc.statsReg = statsReg

	got := fc.orderedChain(statsReg)
	if got[len(got)-1] != "c" {
		t.Errorf("expected zero-sample provider \"c\" to be last, got %v", got)
	}
}

// stringSliceEqual returns true if a and b contain the same elements in the same order.
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
