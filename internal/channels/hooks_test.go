package channels

import (
	"testing"
	"time"
)

// --- HookEvent construction ---

func TestNewHookEvent_Fields(t *testing.T) {
	before := time.Now()
	e := newHookEvent("slack")
	after := time.Now()

	if e.ChannelName != "slack" {
		t.Errorf("ChannelName = %q, want slack", e.ChannelName)
	}
	if e.At.Before(before) || e.At.After(after) {
		t.Errorf("At = %v, expected between %v and %v", e.At, before, after)
	}
}

func TestNewHookEvent_Exported(t *testing.T) {
	e := NewHookEvent("zulip")
	if e.ChannelName != "zulip" {
		t.Errorf("ChannelName = %q, want zulip", e.ChannelName)
	}
	if e.At.IsZero() {
		t.Error("At should not be zero")
	}
}

// --- fire helper ---

func TestFire_CallsCallback(t *testing.T) {
	called := false
	e := newHookEvent("discord")
	fire(func(ev HookEvent) { called = true }, e)
	if !called {
		t.Error("fire should have called the callback")
	}
}

func TestFire_NilCallback_NoOp(t *testing.T) {
	// Should not panic.
	e := newHookEvent("matrix")
	fire(nil, e)
}

func TestFire_PassesEvent(t *testing.T) {
	var got HookEvent
	e := newHookEvent("slack")
	e.SessionKey = "dm:U123"
	e.SessionID = "sess-abc"

	fire(func(ev HookEvent) { got = ev }, e)

	if got.ChannelName != "slack" {
		t.Errorf("ChannelName = %q, want slack", got.ChannelName)
	}
	if got.SessionKey != "dm:U123" {
		t.Errorf("SessionKey = %q, want dm:U123", got.SessionKey)
	}
	if got.SessionID != "sess-abc" {
		t.Errorf("SessionID = %q, want sess-abc", got.SessionID)
	}
}

// --- Hooks.Fire ---

func TestHooks_Fire_CallsSet(t *testing.T) {
	called := false
	h := &Hooks{
		OnMessageReceived: func(e HookEvent) { called = true },
	}
	e := newHookEvent("slack")
	h.Fire(h.OnMessageReceived, e)
	if !called {
		t.Error("Hooks.Fire should have called OnMessageReceived")
	}
}

func TestHooks_Fire_NilHooks_NoOp(t *testing.T) {
	var h *Hooks
	e := newHookEvent("slack")
	// Should not panic even when the Hooks pointer itself is nil.
	h.Fire(func(_ HookEvent) { panic("should not be called") }, e)
}

func TestHooks_Fire_NilField_NoOp(t *testing.T) {
	h := &Hooks{} // all fields nil
	e := newHookEvent("slack")
	// Should not panic.
	h.Fire(h.OnError, e)
}

// --- Hooks fields coverage ---

func TestHooks_AllCallbacks(t *testing.T) {
	counts := make(map[string]int)

	h := &Hooks{
		OnMessageReceived: func(e HookEvent) { counts["OnMessageReceived"]++ },
		OnResponseSent:    func(e HookEvent) { counts["OnResponseSent"]++ },
		OnToolInvoked:     func(e HookEvent) { counts["OnToolInvoked"]++ },
		OnPermRequest:     func(e HookEvent) { counts["OnPermRequest"]++ },
		OnPermResponse:    func(e HookEvent) { counts["OnPermResponse"]++ },
		OnSessionCreated:  func(e HookEvent) { counts["OnSessionCreated"]++ },
		OnSessionEvicted:  func(e HookEvent) { counts["OnSessionEvicted"]++ },
		OnError:           func(e HookEvent) { counts["OnError"]++ },
	}

	e := newHookEvent("test")

	h.Fire(h.OnMessageReceived, e)
	h.Fire(h.OnResponseSent, e)
	h.Fire(h.OnToolInvoked, e)
	h.Fire(h.OnPermRequest, e)
	h.Fire(h.OnPermResponse, e)
	h.Fire(h.OnSessionCreated, e)
	h.Fire(h.OnSessionEvicted, e)
	h.Fire(h.OnError, e)

	for name, count := range counts {
		if count != 1 {
			t.Errorf("%s called %d times, want 1", name, count)
		}
	}
}

// --- Message struct ---

func TestMessage_ZeroValue(t *testing.T) {
	var m Message
	if m.ID != "" || m.Content != "" || m.SenderID != "" {
		t.Error("zero-value Message should have empty string fields")
	}
	if m.Raw != nil {
		t.Error("zero-value Message.Raw should be nil")
	}
	if len(m.Attachments) != 0 {
		t.Error("zero-value Message.Attachments should be empty")
	}
}

func TestMessage_Fields(t *testing.T) {
	raw := struct{ X int }{X: 42}
	m := Message{
		ID:          "msg-1",
		Content:     "hello",
		SenderID:    "user-123",
		SenderName:  "Alice",
		SenderEmail: "alice@example.com",
		Type:        "direct",
		Recipient:   "#general",
		Topic:       "topic",
		Raw:         raw,
		Attachments: []Attachment{{Name: "file.txt", Size: 100}},
	}

	if m.ID != "msg-1" {
		t.Errorf("ID = %q", m.ID)
	}
	if m.SenderEmail != "alice@example.com" {
		t.Errorf("SenderEmail = %q", m.SenderEmail)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].Size != 100 {
		t.Error("Attachments not preserved correctly")
	}
}

// --- baseAdapter ---

func TestBaseAdapter_IsConnected(t *testing.T) {
	b := newBaseAdapter(5)
	if !b.IsConnected() {
		t.Error("baseAdapter.IsConnected should always return true")
	}
}

func TestBaseAdapter_Messages(t *testing.T) {
	b := newBaseAdapter(2)
	ch := b.Messages()
	if ch == nil {
		t.Error("Messages() should return a non-nil channel")
	}

	// Verify the channel can carry messages.
	msg := Message{ID: "x", Content: "test"}
	b.msgChan <- msg
	got := <-ch
	if got.ID != "x" {
		t.Errorf("got ID %q, want x", got.ID)
	}
}
