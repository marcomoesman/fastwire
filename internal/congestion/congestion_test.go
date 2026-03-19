package congestion

import (
	"sync"
	"testing"
)

// --- Conservative (AIMD) tests ---

func TestConservativeWindowGrowth(t *testing.T) {
	cc := NewController(Conservative, 4).(*ConservativeCC)

	initial := cc.Cwnd()
	if initial != 4.0 {
		t.Fatalf("initial cwnd = %f, want 4.0", initial)
	}

	// Acking 1 packet at cwnd=4 should increase by 1/4 = 0.25.
	cc.OnAck(1)
	want := 4.0 + 1.0/4.0
	if got := cc.Cwnd(); got != want {
		t.Fatalf("cwnd after 1 ack = %f, want %f", got, want)
	}
}

func TestConservativeWindowShrink(t *testing.T) {
	cc := NewController(Conservative, 10).(*ConservativeCC)

	cc.OnLoss()
	want := 5.0
	if got := cc.Cwnd(); got != want {
		t.Fatalf("cwnd after loss = %f, want %f", got, want)
	}

	// Loss again: 5 * 0.5 = 2.5
	cc.OnLoss()
	want = 2.5
	if got := cc.Cwnd(); got != want {
		t.Fatalf("cwnd after second loss = %f, want %f", got, want)
	}
}

func TestConservativeWindowFloor(t *testing.T) {
	cc := NewController(Conservative, 4).(*ConservativeCC)

	// Repeated losses should never go below minCwnd (2.0).
	for range 20 {
		cc.OnLoss()
	}
	if got := cc.Cwnd(); got != minCwnd {
		t.Fatalf("cwnd after many losses = %f, want %f (minCwnd)", got, minCwnd)
	}
}

func TestConservativeCanSend(t *testing.T) {
	cc := NewController(Conservative, 4).(*ConservativeCC)

	tests := []struct {
		inFlight int
		want     bool
	}{
		{0, true},
		{1, true},
		{3, true},
		{4, false}, // cwnd=4 -> inFlight must be < 4
		{5, false},
	}
	for _, tt := range tests {
		if got := cc.CanSend(tt.inFlight); got != tt.want {
			t.Errorf("CanSend(%d) = %v, want %v", tt.inFlight, got, tt.want)
		}
	}
}

func TestConservativeCanSendBoundary(t *testing.T) {
	cc := NewController(Conservative, 4).(*ConservativeCC)

	// After one ack, cwnd grows to 4.25 -> int(4.25) = 4 -> inFlight=4 still blocked.
	cc.OnAck(1)
	if cc.CanSend(4) {
		t.Fatal("CanSend(4) should be false with cwnd=4.25")
	}

	// After more acks to push cwnd above 5 -> inFlight=4 should be allowed.
	for range 4 {
		cc.OnAck(1)
	}
	if !cc.CanSend(4) {
		t.Fatalf("CanSend(4) should be true with cwnd=%f", cc.Cwnd())
	}
}

func TestConservativeOnDuplicateAck(t *testing.T) {
	cc := NewController(Conservative, 4).(*ConservativeCC)
	// Conservative mode never triggers fast retransmit.
	for range 10 {
		if cc.OnDuplicateAck() {
			t.Fatal("conservative OnDuplicateAck should always return false")
		}
	}
}

func TestConservativeHalvesRTO(t *testing.T) {
	cc := NewController(Conservative, 4)
	if cc.HalvesRTO() {
		t.Fatal("conservative HalvesRTO should return false")
	}
}

// --- Aggressive tests ---

func TestAggressiveCanSendAlways(t *testing.T) {
	cc := NewController(Aggressive, 0)

	for _, inFlight := range []int{0, 1, 100, 10000} {
		if !cc.CanSend(inFlight) {
			t.Fatalf("aggressive CanSend(%d) = false, want true", inFlight)
		}
	}
}

func TestAggressiveFastRetransmit(t *testing.T) {
	cc := NewController(Aggressive, 0)

	// First duplicate ack: no trigger.
	if cc.OnDuplicateAck() {
		t.Fatal("should not trigger after 1 duplicate ack")
	}

	// Second duplicate ack: trigger fast retransmit.
	if !cc.OnDuplicateAck() {
		t.Fatal("should trigger after 2 duplicate acks")
	}

	// Counter should reset -- need 2 more.
	if cc.OnDuplicateAck() {
		t.Fatal("should not trigger after reset + 1 dup ack")
	}
	if !cc.OnDuplicateAck() {
		t.Fatal("should trigger after reset + 2 dup acks")
	}
}

func TestAggressiveAckResetsDupCounter(t *testing.T) {
	cc := NewController(Aggressive, 0)

	// One duplicate ack, then a normal ack resets the counter.
	cc.OnDuplicateAck()
	cc.OnAck(1)

	// Now need 2 more dup acks to trigger.
	if cc.OnDuplicateAck() {
		t.Fatal("should not trigger -- counter was reset by OnAck")
	}
	if !cc.OnDuplicateAck() {
		t.Fatal("should trigger after 2 dup acks from fresh counter")
	}
}

func TestAggressiveLossNoOp(t *testing.T) {
	cc := NewController(Aggressive, 0)
	// OnLoss should not panic or change behavior.
	cc.OnLoss()
	if !cc.CanSend(1000) {
		t.Fatal("aggressive should still allow send after OnLoss")
	}
}

func TestAggressiveHalvesRTO(t *testing.T) {
	cc := NewController(Aggressive, 0)
	if !cc.HalvesRTO() {
		t.Fatal("aggressive HalvesRTO should return true")
	}
}

// --- Window tests ---

func TestConservativeWindow(t *testing.T) {
	cc := NewController(Conservative, 4)
	w := cc.Window()
	if w != 4 {
		t.Fatalf("Window() = %d, want 4", w)
	}

	// After acks, window should grow.
	for range 16 {
		cc.OnAck(1)
	}
	w2 := cc.Window()
	if w2 <= w {
		t.Fatalf("Window() after acks = %d, should be > %d", w2, w)
	}
}

func TestAggressiveWindow(t *testing.T) {
	cc := NewController(Aggressive, 0)
	w := cc.Window()
	if w != 0 {
		t.Fatalf("Window() = %d, want 0 (unlimited)", w)
	}
}

// --- Constructor tests ---

func TestNewCongestionControllerDefaultCwnd(t *testing.T) {
	cc := NewController(Conservative, 0).(*ConservativeCC)
	if got := cc.Cwnd(); got != float64(4) {
		t.Fatalf("default cwnd = %f, want %f", got, float64(4))
	}
}

func TestNewCongestionControllerCustomCwnd(t *testing.T) {
	cc := NewController(Conservative, 8).(*ConservativeCC)
	if got := cc.Cwnd(); got != 8.0 {
		t.Fatalf("cwnd = %f, want 8.0", got)
	}
}

func TestNewCongestionControllerModes(t *testing.T) {
	conservative := NewController(Conservative, 0)
	if _, ok := conservative.(*ConservativeCC); !ok {
		t.Fatal("Conservative should create ConservativeCC")
	}

	aggressive := NewController(Aggressive, 0)
	if _, ok := aggressive.(*AggressiveCC); !ok {
		t.Fatal("Aggressive should create AggressiveCC")
	}
}

// --- Concurrency stress tests ---

func TestConservativeConcurrent(t *testing.T) {
	cc := NewController(Conservative, 4)
	var wg sync.WaitGroup

	for range 100 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			cc.OnAck(1)
		}()
		go func() {
			defer wg.Done()
			cc.OnLoss()
		}()
		go func() {
			defer wg.Done()
			cc.CanSend(2)
		}()
	}
	wg.Wait()

	// Verify cwnd is still valid (>= minCwnd).
	ccc := cc.(*ConservativeCC)
	if got := ccc.Cwnd(); got < minCwnd {
		t.Fatalf("cwnd = %f, should be >= %f after concurrent ops", got, minCwnd)
	}
}

func TestAggressiveConcurrent(t *testing.T) {
	cc := NewController(Aggressive, 0)
	var wg sync.WaitGroup

	for range 100 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			cc.OnAck(1)
		}()
		go func() {
			defer wg.Done()
			cc.OnDuplicateAck()
		}()
		go func() {
			defer wg.Done()
			cc.CanSend(100)
		}()
	}
	wg.Wait()

	// Should still always allow send.
	if !cc.CanSend(999) {
		t.Fatal("aggressive should still allow send after concurrent ops")
	}
}
