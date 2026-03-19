package stats

import (
	"sync"
	"testing"
)

func TestLossTrackerEmpty(t *testing.T) {
	lt := NewLossTracker()
	if loss := lt.Loss(); loss != 0.0 {
		t.Fatalf("empty loss = %f, want 0.0", loss)
	}
}

func TestLossTrackerAllAcked(t *testing.T) {
	lt := NewLossTracker()
	for i := uint32(1); i <= 10; i++ {
		lt.RecordSend(i)
	}
	for i := uint32(1); i <= 10; i++ {
		lt.RecordAck(i)
	}
	if loss := lt.Loss(); loss != 0.0 {
		t.Fatalf("all acked loss = %f, want 0.0", loss)
	}
}

func TestLossTrackerHalfLost(t *testing.T) {
	lt := NewLossTracker()
	for i := uint32(1); i <= 10; i++ {
		lt.RecordSend(i)
	}
	// Ack only odd sequences.
	for i := uint32(1); i <= 10; i += 2 {
		lt.RecordAck(i)
	}
	loss := lt.Loss()
	if loss != 0.5 {
		t.Fatalf("half lost loss = %f, want 0.5", loss)
	}
}

func TestLossTrackerWindowRotation(t *testing.T) {
	lt := NewLossTracker()

	// Send 150 packets, ack only the first 50 (which will be evicted).
	for i := uint32(1); i <= 150; i++ {
		lt.RecordSend(i)
		if i <= 50 {
			lt.RecordAck(i)
		}
	}

	// Only the last 100 matter (seqs 51-150), none of which are acked.
	loss := lt.Loss()
	if loss != 1.0 {
		t.Fatalf("rotated loss = %f, want 1.0", loss)
	}
}

func TestLossTrackerWindowRotationPartial(t *testing.T) {
	lt := NewLossTracker()

	// Send 100 packets, ack all.
	for i := uint32(1); i <= 100; i++ {
		lt.RecordSend(i)
		lt.RecordAck(i)
	}
	if loss := lt.Loss(); loss != 0.0 {
		t.Fatalf("initial loss = %f, want 0.0", loss)
	}

	// Send 50 more (seqs 101-150), ack none.
	// Window now holds seqs 51-150: 50 acked (51-100), 50 unacked (101-150).
	for i := uint32(101); i <= 150; i++ {
		lt.RecordSend(i)
	}
	loss := lt.Loss()
	if loss != 0.5 {
		t.Fatalf("partial rotation loss = %f, want 0.5", loss)
	}
}

func TestLossTrackerDuplicateAck(t *testing.T) {
	lt := NewLossTracker()
	lt.RecordSend(1)
	lt.RecordAck(1)
	lt.RecordAck(1) // duplicate ack should be no-op
	if loss := lt.Loss(); loss != 0.0 {
		t.Fatalf("duplicate ack loss = %f, want 0.0", loss)
	}
}

func TestLossTrackerConcurrent(t *testing.T) {
	lt := NewLossTracker()
	var wg sync.WaitGroup

	// Concurrent sends and acks.
	for i := uint32(1); i <= 200; i++ {
		wg.Add(1)
		go func(seq uint32) {
			defer wg.Done()
			lt.RecordSend(seq)
		}(i)
	}
	wg.Wait()

	for i := uint32(1); i <= 200; i++ {
		wg.Add(1)
		go func(seq uint32) {
			defer wg.Done()
			lt.RecordAck(seq)
		}(i)
	}
	wg.Wait()

	// Loss should be valid (between 0 and 1).
	loss := lt.Loss()
	if loss < 0.0 || loss > 1.0 {
		t.Fatalf("concurrent loss = %f, want [0.0, 1.0]", loss)
	}
}
