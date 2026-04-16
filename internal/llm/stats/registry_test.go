package stats

import (
	"sync"
	"testing"
)

func TestRegistry_GetCreatesEntry(t *testing.T) {
	r := NewRegistry()
	ps := r.Get("provider-a")
	if ps == nil {
		t.Fatal("Get() returned nil")
	}
	// Second call must return the same instance.
	ps2 := r.Get("provider-a")
	if ps != ps2 {
		t.Error("Get() returned different instance for same provider")
	}
}

func TestRegistry_GetConcurrent(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	const goroutines = 50
	results := make([]*ProviderStats, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = r.Get("shared")
		}(i)
	}
	wg.Wait()
	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Errorf("concurrent Get() returned different instances at index %d", i)
		}
	}
}

func TestRegistry_Snapshot(t *testing.T) {
	r := NewRegistry()
	r.Get("provider-a").Record(100, false)
	r.Get("provider-b").Record(200, true)

	snap := r.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Snapshot() len = %d, want 2", len(snap))
	}
	a, ok := snap["provider-a"]
	if !ok {
		t.Fatal("Snapshot missing provider-a")
	}
	if a.RequestCount != 1 {
		t.Errorf("provider-a RequestCount = %d, want 1", a.RequestCount)
	}
	if a.ErrorRate != 0.0 {
		t.Errorf("provider-a ErrorRate = %v, want 0.0", a.ErrorRate)
	}

	b, ok := snap["provider-b"]
	if !ok {
		t.Fatal("Snapshot missing provider-b")
	}
	if b.ErrorRate != 1.0 {
		t.Errorf("provider-b ErrorRate = %v, want 1.0", b.ErrorRate)
	}
}

func TestRegistry_SnapshotIsACopy(t *testing.T) {
	r := NewRegistry()
	r.Get("p").Record(100, false)
	snap := r.Snapshot()

	// Record another sample after the snapshot was taken.
	r.Get("p").Record(200, false)

	// The snapshot should still show the old count.
	if snap["p"].RequestCount != 1 {
		t.Errorf("Snapshot was not a copy: RequestCount = %d, want 1", snap["p"].RequestCount)
	}
}
