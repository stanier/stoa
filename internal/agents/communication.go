package agents

import (
	"sync"
)

// busSubscriber holds a subscriber channel and a once-guard so the channel
// can be closed safely by either Unsubscribe or Close, whichever comes first.
type busSubscriber struct {
	ch   chan Message
	once sync.Once
}

func (s *busSubscriber) close() {
	s.once.Do(func() { close(s.ch) })
}

// MessageBus implements pub/sub for agent messages
type MessageBus struct {
	subscribers []*busSubscriber
	mu          sync.RWMutex
}

// NewMessageBus creates a new message bus
func NewMessageBus() *MessageBus {
	return &MessageBus{
		subscribers: make([]*busSubscriber, 0),
	}
}

// Subscribe registers a new subscriber and returns their message channel and
// an unsubscribe function. Callers must call the unsubscribe function when
// they are done to remove the subscriber from the bus and close its channel.
// Failing to unsubscribe causes the subscriber slot to linger in memory and
// every Publish call to attempt a send to a dead channel.
func (mb *MessageBus) Subscribe() (<-chan Message, func()) {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	sub := &busSubscriber{ch: make(chan Message, 100)}
	mb.subscribers = append(mb.subscribers, sub)

	unsubscribe := func() {
		mb.mu.Lock()
		for i, s := range mb.subscribers {
			if s == sub {
				mb.subscribers = append(mb.subscribers[:i], mb.subscribers[i+1:]...)
				break
			}
		}
		mb.mu.Unlock()
		sub.close() // sync.Once ensures channel is only closed once
	}

	return sub.ch, unsubscribe
}

// Publish sends a message to all subscribers
func (mb *MessageBus) Publish(msg Message) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()

	for _, sub := range mb.subscribers {
		select {
		case sub.ch <- msg:
		default:
			// Drop message if subscriber is full
		}
	}
}

// Close shuts down the message bus, closing all subscriber channels.
// Uses the per-subscriber sync.Once so it is safe to call even if some
// subscribers have already unsubscribed.
func (mb *MessageBus) Close() {
	mb.mu.Lock()
	subs := mb.subscribers
	mb.subscribers = nil
	mb.mu.Unlock()

	for _, sub := range subs {
		sub.close()
	}
}

// SubscriberCount returns the number of active subscribers
func (mb *MessageBus) SubscriberCount() int {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	return len(mb.subscribers)
}
