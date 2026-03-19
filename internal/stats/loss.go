package stats

import "sync"

const lossWindowSize = 100

// LossTracker tracks packet loss using a ring buffer of the last 100 reliable packets.
type LossTracker struct {
	mu       sync.Mutex
	entries  [lossWindowSize]lossEntry
	head     int // next write position
	count    int // number of entries written (capped at lossWindowSize)
	ackCount int // number of acked entries in the ring
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

	// If overwriting an acked entry, adjust ackCount.
	if lt.count == lossWindowSize && lt.entries[lt.head].acked {
		lt.ackCount--
	}

	lt.entries[lt.head] = lossEntry{seq: seq, acked: false}
	lt.head = (lt.head + 1) % lossWindowSize
	if lt.count < lossWindowSize {
		lt.count++
	}
}

// RecordAck marks a previously sent packet as acked.
func (lt *LossTracker) RecordAck(seq uint32) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	for i := range lt.count {
		idx := (lt.head - lt.count + i + lossWindowSize) % lossWindowSize
		if lt.entries[idx].seq == seq && !lt.entries[idx].acked {
			lt.entries[idx].acked = true
			lt.ackCount++
			return
		}
	}
}

// Loss returns the current packet loss ratio (0.0-1.0).
func (lt *LossTracker) Loss() float64 {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	if lt.count == 0 {
		return 0.0
	}
	return 1.0 - float64(lt.ackCount)/float64(lt.count)
}
