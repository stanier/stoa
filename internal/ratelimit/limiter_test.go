package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestUnderLimit(t *testing.T) {
	l := New(time.Minute, 5)
	defer l.Close()

	for i := 0; i < 5; i++ {
		ok, reason := l.Allow("user1")
		if !ok {
			t.Fatalf("request %d should be allowed, got denied: %s", i+1, reason)
		}
	}
}

func TestAtLimitDenied(t *testing.T) {
	l := New(time.Minute, 3)
	defer l.Close()

	for i := 0; i < 3; i++ {
		ok, _ := l.Allow("user1")
		if !ok {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	ok, reason := l.Allow("user1")
	if ok {
		t.Fatal("4th request should be denied but was allowed")
	}
	if reason == "" {
		t.Fatal("expected non-empty denial reason")
	}
}

func TestWindowSlides(t *testing.T) {
	// Use a very short window so we can observe it sliding.
	l := New(100*time.Millisecond, 2)
	defer l.Close()

	ok, _ := l.Allow("user1")
	if !ok {
		t.Fatal("1st request should be allowed")
	}
	ok, _ = l.Allow("user1")
	if !ok {
		t.Fatal("2nd request should be allowed")
	}

	// 3rd request should be denied (at limit).
	ok, _ = l.Allow("user1")
	if ok {
		t.Fatal("3rd request should be denied")
	}

	// Wait for the window to expire.
	time.Sleep(120 * time.Millisecond)

	// Now requests should be allowed again.
	ok, reason := l.Allow("user1")
	if !ok {
		t.Fatalf("request after window expiry should be allowed, got: %s", reason)
	}
}

func TestKeyIsolation(t *testing.T) {
	l := New(time.Minute, 2)
	defer l.Close()

	// Fill up user1.
	l.Allow("user1")
	l.Allow("user1")
	ok, _ := l.Allow("user1")
	if ok {
		t.Fatal("user1 should be rate limited")
	}

	// user2 should still be allowed.
	ok, reason := l.Allow("user2")
	if !ok {
		t.Fatalf("user2 should not be affected by user1 limit, got: %s", reason)
	}
}

func TestUnlimited(t *testing.T) {
	l := New(time.Minute, 0)
	defer l.Close()

	for i := 0; i < 1000; i++ {
		ok, reason := l.Allow("user1")
		if !ok {
			t.Fatalf("limit=0 should always allow, denied on request %d: %s", i+1, reason)
		}
	}
}

func TestConcurrentSafety(t *testing.T) {
	l := New(time.Minute, 100)
	defer l.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Allow("concurrent-user")
		}()
	}
	wg.Wait()
}

func TestStats(t *testing.T) {
	l := New(time.Minute, 10)
	defer l.Close()

	if n := l.Stats("user1"); n != 0 {
		t.Fatalf("expected 0 stats for new key, got %d", n)
	}

	l.Allow("user1")
	l.Allow("user1")
	l.Allow("user1")

	if n := l.Stats("user1"); n != 3 {
		t.Fatalf("expected 3 stats, got %d", n)
	}
}
