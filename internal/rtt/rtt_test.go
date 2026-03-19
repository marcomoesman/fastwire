package rtt

import (
	"sync"
	"testing"
	"time"
)

func TestRTTStateInitial(t *testing.T) {
	r := New()
	if got := r.RTO(); got != 1*time.Second {
		t.Fatalf("initial RTO = %v, want 1s", got)
	}
	if got := r.SRTT(); got != 0 {
		t.Fatalf("initial SRTT = %v, want 0", got)
	}
	if got := r.RTTVar(); got != 0 {
		t.Fatalf("initial RTTVar = %v, want 0", got)
	}
}

func TestRTTStateFirstSample(t *testing.T) {
	r := New()
	r.AddSample(100 * time.Millisecond)

	// After first sample: srtt = 0.1, rttvar = 0.05
	// rto = 0.1 + 4*0.05 = 0.3s
	srtt := r.SRTT()
	if srtt < 99*time.Millisecond || srtt > 101*time.Millisecond {
		t.Fatalf("SRTT after first sample = %v, want ~100ms", srtt)
	}

	rttvar := r.RTTVar()
	if rttvar < 49*time.Millisecond || rttvar > 51*time.Millisecond {
		t.Fatalf("RTTVar after first sample = %v, want ~50ms", rttvar)
	}

	rto := r.RTO()
	if rto < 299*time.Millisecond || rto > 301*time.Millisecond {
		t.Fatalf("RTO after first sample = %v, want ~300ms", rto)
	}
}

func TestRTTStateConvergence(t *testing.T) {
	r := New()

	// Feed 100 samples of 50ms -- SRTT should converge close to 50ms.
	for range 100 {
		r.AddSample(50 * time.Millisecond)
	}

	srtt := r.SRTT()
	if srtt < 49*time.Millisecond || srtt > 51*time.Millisecond {
		t.Fatalf("SRTT after 100x50ms = %v, want ~50ms", srtt)
	}

	// RTTVar should be very small since all samples are identical.
	rttvar := r.RTTVar()
	if rttvar > 5*time.Millisecond {
		t.Fatalf("RTTVar should be near zero with constant samples, got %v", rttvar)
	}
}

func TestRTTStateJitter(t *testing.T) {
	r := New()

	// Alternate between 40ms and 60ms -- SRTT should converge to ~50ms.
	for i := range 200 {
		if i%2 == 0 {
			r.AddSample(40 * time.Millisecond)
		} else {
			r.AddSample(60 * time.Millisecond)
		}
	}

	srtt := r.SRTT()
	if srtt < 45*time.Millisecond || srtt > 55*time.Millisecond {
		t.Fatalf("SRTT with jitter = %v, want ~50ms", srtt)
	}

	// RTTVar should reflect the jitter.
	rttvar := r.RTTVar()
	if rttvar < 1*time.Millisecond {
		t.Fatalf("RTTVar should reflect jitter, got %v", rttvar)
	}
}

func TestRTTStateMinClamp(t *testing.T) {
	r := New()

	// Very small RTT -- RTO should be clamped to rtoMin (50ms).
	r.AddSample(1 * time.Microsecond)
	rto := r.RTO()
	if rto < 50*time.Millisecond {
		t.Fatalf("RTO = %v, should be >= 50ms (rtoMin)", rto)
	}
}

func TestRTTStateMaxClamp(t *testing.T) {
	r := New()

	// Very large RTT -- RTO should be clamped to rtoMax (5s).
	r.AddSample(10 * time.Second)
	rto := r.RTO()
	if rto > 5*time.Second {
		t.Fatalf("RTO = %v, should be <= 5s (rtoMax)", rto)
	}
}

func TestRTTStateConcurrency(t *testing.T) {
	r := New()

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				r.AddSample(50 * time.Millisecond)
				_ = r.RTO()
				_ = r.SRTT()
				_ = r.RTTVar()
			}
		}()
	}
	wg.Wait()

	// Just verify it didn't panic or deadlock.
	srtt := r.SRTT()
	if srtt < 40*time.Millisecond || srtt > 60*time.Millisecond {
		t.Fatalf("SRTT after concurrent access = %v, want ~50ms", srtt)
	}
}
