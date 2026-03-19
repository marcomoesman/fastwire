package bandwidth

import (
	"sync"
	"testing"
	"time"
)

func TestEstimatorEmpty(t *testing.T) {
	e := New()
	if bps := e.BytesPerSecond(); bps != 0 {
		t.Fatalf("empty estimator = %f, want 0", bps)
	}
}

func TestEstimatorFirstSample(t *testing.T) {
	e := New()
	e.Record(1000)

	// Simulate time passing.
	e.mu.Lock()
	e.lastTick = time.Now().Add(-100 * time.Millisecond)
	e.mu.Unlock()

	e.Tick()

	bps := e.BytesPerSecond()
	// 1000 bytes / 0.1s = 10000 bps.
	if bps < 8000 || bps > 12000 {
		t.Fatalf("first sample = %f, expected ~10000", bps)
	}
}

func TestEstimatorConvergence(t *testing.T) {
	e := New()

	// Simulate 20 ticks at 100ms intervals, 500 bytes per tick.
	for range 20 {
		e.Record(500)

		e.mu.Lock()
		e.lastTick = time.Now().Add(-100 * time.Millisecond)
		e.mu.Unlock()

		e.Tick()
	}

	bps := e.BytesPerSecond()
	// 500 bytes / 0.1s = 5000 bps.
	if bps < 4000 || bps > 6000 {
		t.Fatalf("converged estimate = %f, expected ~5000", bps)
	}
}

func TestEstimatorZeroDuration(t *testing.T) {
	e := New()
	e.Record(1000)
	// Tick immediately — dt ≈ 0, should not update.
	e.Tick()
	// Should still be 0 or close to it (first sample with tiny dt).
	// The exact behavior depends on timing; just verify no panic.
}

func TestEstimatorConcurrent(t *testing.T) {
	e := New()
	var wg sync.WaitGroup

	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				e.Record(64)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 50 {
			e.Tick()
		}
	}()

	wg.Wait()
	// Just verify no races or panics.
	_ = e.BytesPerSecond()
}
