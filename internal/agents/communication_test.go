package agents

import (
	"testing"
	"time"
)

func TestMessageBus_PublishSubscribe(t *testing.T) {
	bus := NewMessageBus()
	defer bus.Close()

	// Subscribe
	sub1, _ := bus.Subscribe()
	sub2, _ := bus.Subscribe()

	// Publish message
	msg := Message{
		ID:      "msg-1",
		AgentID: "agent-1",
		Type:    MessageTypeProgress,
		Content: "Test message",
	}

	bus.Publish(msg)

	// Both subscribers should receive
	timeout := time.After(1 * time.Second)

	select {
	case received := <-sub1:
		if received.ID != msg.ID {
			t.Errorf("sub1: expected message ID '%s', got '%s'", msg.ID, received.ID)
		}
	case <-timeout:
		t.Error("sub1: timeout waiting for message")
	}

	select {
	case received := <-sub2:
		if received.ID != msg.ID {
			t.Errorf("sub2: expected message ID '%s', got '%s'", msg.ID, received.ID)
		}
	case <-timeout:
		t.Error("sub2: timeout waiting for message")
	}
}

func TestMessageBus_MultipleMessages(t *testing.T) {
	bus := NewMessageBus()
	defer bus.Close()

	sub, _ := bus.Subscribe()

	// Publish multiple messages
	messages := []Message{
		{ID: "msg-1", AgentID: "agent-1", Type: MessageTypeThinking, Content: "Thinking..."},
		{ID: "msg-2", AgentID: "agent-1", Type: MessageTypeProgress, Content: "Progress 50%"},
		{ID: "msg-3", AgentID: "agent-1", Type: MessageTypeResult, Content: "Done!"},
	}

	for _, msg := range messages {
		bus.Publish(msg)
	}

	// Receive all messages
	received := []Message{}
	timeout := time.After(1 * time.Second)

	for i := 0; i < len(messages); i++ {
		select {
		case msg := <-sub:
			received = append(received, msg)
		case <-timeout:
			t.Fatalf("timeout waiting for message %d", i)
		}
	}

	if len(received) != len(messages) {
		t.Errorf("expected %d messages, got %d", len(messages), len(received))
	}

	for i, msg := range received {
		if msg.ID != messages[i].ID {
			t.Errorf("message %d: expected ID '%s', got '%s'", i, messages[i].ID, msg.ID)
		}
	}
}

func TestMessageBus_SubscriberCount(t *testing.T) {
	bus := NewMessageBus()
	defer bus.Close()

	if bus.SubscriberCount() != 0 {
		t.Errorf("expected 0 subscribers initially, got %d", bus.SubscriberCount())
	}

	sub1, _ := bus.Subscribe()
	if bus.SubscriberCount() != 1 {
		t.Errorf("expected 1 subscriber, got %d", bus.SubscriberCount())
	}

	sub2, _ := bus.Subscribe()
	if bus.SubscriberCount() != 2 {
		t.Errorf("expected 2 subscribers, got %d", bus.SubscriberCount())
	}

	// Drain channels to avoid leaks
	go func() {
		for range sub1 {
		}
	}()
	go func() {
		for range sub2 {
		}
	}()
}

func TestMessageBus_Close(t *testing.T) {
	bus := NewMessageBus()

	sub, _ := bus.Subscribe()

	// Close the bus
	bus.Close()

	// Subscriber channel should be closed
	_, ok := <-sub
	if ok {
		t.Error("expected subscriber channel to be closed")
	}

	if bus.SubscriberCount() != 0 {
		t.Errorf("expected 0 subscribers after close, got %d", bus.SubscriberCount())
	}
}

func TestMessageBus_DroppedMessages(t *testing.T) {
	bus := NewMessageBus()
	defer bus.Close()

	sub, _ := bus.Subscribe()

	// Fill the subscriber buffer (100 messages)
	for i := 0; i < 150; i++ {
		msg := Message{
			ID:      "msg",
			AgentID: "agent",
			Type:    MessageTypeProgress,
			Content: "test",
		}
		bus.Publish(msg)
	}

	// Count received messages
	received := 0
	timeout := time.After(500 * time.Millisecond)

drainLoop:
	for {
		select {
		case <-sub:
			received++
		case <-timeout:
			break drainLoop
		}
	}

	// Should have received up to buffer size (100)
	if received > 100 {
		t.Errorf("expected at most 100 messages (buffer size), got %d", received)
	}

	// Note: Some messages should have been dropped
	if received == 150 {
		t.Error("expected some messages to be dropped due to buffer limit")
	}
}
