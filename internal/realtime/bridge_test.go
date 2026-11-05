package realtime

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type fakeSession struct {
	mu     sync.Mutex
	sent   []ClientEvent
	out    chan ServerEvent
	closed chan struct{}
	once   sync.Once
}

func newFakeSession() *fakeSession {
	return &fakeSession{out: make(chan ServerEvent, 8), closed: make(chan struct{})}
}

func (s *fakeSession) Send(ev ClientEvent) error {
	s.mu.Lock()
	s.sent = append(s.sent, ev)
	s.mu.Unlock()
	return nil
}

func (s *fakeSession) Recv() (ServerEvent, error) {
	select {
	case ev := <-s.out:
		return ev, nil
	case <-s.closed:
		return ServerEvent{}, io.EOF
	}
}

func (s *fakeSession) Close() error { s.once.Do(func() { close(s.closed) }); return nil }

type fakeClient struct {
	in      chan ClientEvent
	mu      sync.Mutex
	written []ServerEvent
}

func (c *fakeClient) ReadEvent() (ClientEvent, error) {
	ev, ok := <-c.in
	if !ok {
		return ClientEvent{}, io.EOF
	}
	return ev, nil
}

func (c *fakeClient) WriteEvent(ev ServerEvent) error {
	c.mu.Lock()
	c.written = append(c.written, ev)
	c.mu.Unlock()
	return nil
}

func TestBridge(t *testing.T) {
	sess := newFakeSession()
	client := &fakeClient{in: make(chan ClientEvent, 8)}
	done := make(chan error, 1)
	go func() { done <- Bridge(context.Background(), client, sess) }()

	sess.out <- ServerEvent{Type: ServerReady, InputRate: 16000}
	client.in <- ClientEvent{Type: ClientAudio, Audio: "AA=="}
	client.in <- ClientEvent{Type: "session.update"} // not allowed through
	client.in <- ClientEvent{Type: ClientTurnEnd}
	time.Sleep(50 * time.Millisecond)
	close(client.in)

	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Bridge returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not end when the browser closed")
	}

	sess.mu.Lock()
	sent := sess.sent
	sess.mu.Unlock()
	if len(sent) != 2 || sent[0].Type != ClientAudio || sent[1].Type != ClientTurnEnd {
		t.Fatalf("forwarded %+v", sent)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	var sawReady, sawRejected bool
	for _, ev := range client.written {
		sawReady = sawReady || ev.Type == ServerReady
		sawRejected = sawRejected || (ev.Type == ServerError && !ev.Fatal)
	}
	if !sawReady || !sawRejected {
		t.Fatalf("client got %+v", client.written)
	}
}

func TestBridge_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Bridge(ctx, &fakeClient{in: make(chan ClientEvent)}, newFakeSession()) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bridge ignored cancel")
	}
}
