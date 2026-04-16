package stats

import (
	"testing"
)

func TestProviderStats_ZeroSamples(t *testing.T) {
	s := NewProviderStats()
	if got := s.ErrorRate(); got != 0.0 {
		t.Errorf("ErrorRate() = %v, want 0.0", got)
	}
	if got := s.P95LatencyMs(); got != 0 {
		t.Errorf("P95LatencyMs() = %v, want 0", got)
	}
	if got := s.RequestCount(); got != 0 {
		t.Errorf("RequestCount() = %v, want 0", got)
	}
}

func TestProviderStats_SingleSample(t *testing.T) {
	s := NewProviderStats()
	s.Record(100, false)
	if got := s.RequestCount(); got != 1 {
		t.Errorf("RequestCount() = %v, want 1", got)
	}
	if got := s.ErrorRate(); got != 0.0 {
		t.Errorf("ErrorRate() = %v, want 0.0", got)
	}
	if got := s.P95LatencyMs(); got != 100 {
		t.Errorf("P95LatencyMs() = %v, want 100", got)
	}
}

func TestProviderStats_AllErrors(t *testing.T) {
	s := NewProviderStats()
	for i := 0; i < 10; i++ {
		s.Record(50, true)
	}
	if got := s.ErrorRate(); got != 1.0 {
		t.Errorf("ErrorRate() = %v, want 1.0", got)
	}
}

func TestProviderStats_ErrorRate(t *testing.T) {
	s := NewProviderStats()
	// 2 errors out of 10 samples.
	for i := 0; i < 8; i++ {
		s.Record(100, false)
	}
	s.Record(200, true)
	s.Record(200, true)

	rate := s.ErrorRate()
	if rate < 0.19 || rate > 0.21 {
		t.Errorf("ErrorRate() = %v, want ~0.2", rate)
	}
}

func TestProviderStats_P95(t *testing.T) {
	s := NewProviderStats()
	// Record 100 samples with values 1..100.
	for i := int64(1); i <= 100; i++ {
		s.Record(i, false)
	}
	p95 := s.P95LatencyMs()
	// 95th percentile of [1..100]: index = int(99 * 0.95) = 94 → value 95.
	if p95 != 95 {
		t.Errorf("P95LatencyMs() = %v, want 95", p95)
	}
}

func TestProviderStats_RingBuffer(t *testing.T) {
	s := NewProviderStats()
	// Fill beyond ring size: count should be capped at ringSize.
	for i := 0; i < ringSize+500; i++ {
		s.Record(int64(i), false)
	}
	if got := s.RequestCount(); got != ringSize {
		t.Errorf("RequestCount() = %v, want %v", got, ringSize)
	}
}
