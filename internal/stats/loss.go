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
	index    map[uint32]int // seq → ring buffer index for O(1) ack lookup
}

type lossEntry struct {
	seq   uint32
	acked bool
}

func NewLossTracker() *LossTracker {
	return &LossTracker{
		index: make(map[uint32]int, lossWindowSize),
	}
}

// RecordSend records a sent reliable packet.
func (lt *LossTracker) RecordSend(seq uint32) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	// If overwriting an existing entry, clean up.
	if lt.count == lossWindowSize {
		old := lt.entries[lt.head]
		if old.acked {
			lt.ackCount--
		}
		// Remove old entry from index only if it still points to this slot.
		if idx, ok := lt.index[old.seq]; ok && idx == lt.head {
			delete(lt.index, old.seq)
		}
	}

	lt.entries[lt.head] = lossEntry{seq: seq, acked: false}
	lt.index[seq] = lt.head
	lt.head = (lt.head + 1) % lossWindowSize
	if lt.count < lossWindowSize {
		lt.count++
	}
}

// RecordAck marks a previously sent packet as acked.
func (lt *LossTracker) RecordAck(seq uint32) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	idx, ok := lt.index[seq]
	if !ok {
		return
	}
	if lt.entries[idx].seq == seq && !lt.entries[idx].acked {
		lt.entries[idx].acked = true
		lt.ackCount++
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
