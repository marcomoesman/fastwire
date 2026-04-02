package stats

import (
	"sync"
	"sync/atomic"
)

const lossWindowSize = 100

// LossTracker tracks packet loss using a ring buffer of the last 100 reliable packets.
type LossTracker struct {
	mu       sync.Mutex
	entries  [lossWindowSize]lossEntry
	head     int // next write position
	count    atomic.Int32
	ackCount atomic.Int32
}

type lossEntry struct {
	seq   uint32
	acked bool
}

func NewLossTracker() *LossTracker {
	return &LossTracker{}
}

// RecordSend records a sent reliable packet.
func (lt *LossTracker) RecordSend(seq uint32) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	// If overwriting an existing entry, clean up.
	if lt.count.Load() == lossWindowSize {
		old := lt.entries[lt.head]
		if old.acked {
			lt.ackCount.Add(-1)
		}
	}

	lt.entries[lt.head] = lossEntry{seq: seq, acked: false}
	lt.head = (lt.head + 1) % lossWindowSize
	if lt.count.Load() < lossWindowSize {
		lt.count.Add(1)
	}
}

// RecordAck marks a previously sent packet as acked.
func (lt *LossTracker) RecordAck(seq uint32) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	c := int(lt.count.Load())
	for i := range c {
		idx := (lt.head - 1 - i + lossWindowSize) % lossWindowSize
		if lt.entries[idx].seq == seq {
			if !lt.entries[idx].acked {
				lt.entries[idx].acked = true
				lt.ackCount.Add(1)
			}
			return
		}
	}
}

// Loss returns the current packet loss ratio (0.0-1.0). Lock-free.
func (lt *LossTracker) Loss() float64 {
	c := lt.count.Load()
	if c == 0 {
		return 0.0
	}
	return 1.0 - float64(lt.ackCount.Load())/float64(c)
}
