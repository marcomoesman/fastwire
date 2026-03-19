package fastwire

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcomoesman/fastwire/internal/rtt"
)

// pendingPacket represents a sent-but-unacked packet queued for retransmission.
type pendingPacket struct {
	raw             []byte    // pre-encryption packet bytes
	sendTime        time.Time // when first sent (for RTT measurement)
	retransmitCount int
	nextRetransmit  time.Time
	firstTransmit   bool   // true if this is the original transmission (for Karn's algorithm)
	sequence        uint32 // sequence number assigned to this packet
}

// channel represents a single logical channel with its own sequence numbers,
// ack tracking, and delivery mode behavior.
type channel struct {
	mode        DeliveryMode
	streamIndex byte

	sendSeq atomic.Uint32 // next sequence to assign (starts at 0, first call returns 1)

	// Protected by mu.
	mu              sync.Mutex
	recvAck         uint32            // highest received remote sequence (0 = nothing received)
	recvAckField    uint32            // bitfield of received sequences before recvAck
	pendingSend     []pendingPacket   // sent-but-unacked reliable packets
	recvBuffer      map[uint32][]byte // reorder buffer (reliable ordered only)
	recvNextDeliver uint32            // next expected sequence for ordered delivery
	lastRecvSeq     uint32            // highest received sequence for unreliable sequenced
	needsAck        bool              // set when new data received, cleared when ack sent
}

// newChannels creates the channel slice from a ChannelLayout.
func newChannels(layout ChannelLayout) []*channel {
	chs := make([]*channel, len(layout.channels))
	for i, def := range layout.channels {
		ch := &channel{
			mode:        def.Mode,
			streamIndex: def.StreamIndex,
		}
		if def.Mode == ReliableOrdered {
			ch.recvBuffer = make(map[uint32][]byte)
		}
		// Sequences start at 1: first nextSequence() call returns 1.
		// recvNextDeliver starts at 1 so ordered delivery expects seq 1 first.
		ch.recvNextDeliver = 1
		chs[i] = ch
	}
	return chs
}

// nextSequence returns the next sequence number for this channel (starting at 1).
func (ch *channel) nextSequence() uint32 {
	return ch.sendSeq.Add(1)
}

// recordReceive updates ack tracking for an incoming packet with the given
// sequence number. Returns false if the packet is a duplicate or too old.
func (ch *channel) recordReceive(seq uint32) bool {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if seq == 0 {
		return false // sequence 0 is invalid
	}

	if ch.recvAck == 0 {
		// First packet ever received on this channel.
		ch.recvAck = seq
		ch.recvAckField = 0
		ch.needsAck = true
		return true
	}

	if seq > ch.recvAck {
		// New highest sequence — shift bitfield.
		diff := seq - ch.recvAck
		if diff <= 32 {
			ch.recvAckField <<= diff
			ch.recvAckField |= 1 << (diff - 1) // set bit for old recvAck
		} else {
			ch.recvAckField = 0 // gap too large, clear bitfield
		}
		ch.recvAck = seq
		ch.needsAck = true
		return true
	}

	if seq == ch.recvAck {
		// Duplicate of the highest sequence.
		return false
	}

	// seq < recvAck
	diff := ch.recvAck - seq
	if diff <= 32 {
		bit := uint32(1) << (diff - 1)
		if ch.recvAckField&bit != 0 {
			return false // duplicate
		}
		ch.recvAckField |= bit
		ch.needsAck = true
		return true
	}

	// Too old.
	return false
}

// ackState returns a snapshot of the current ack and ack bitfield for
// populating outgoing packet headers.
func (ch *channel) ackState() (ack, ackField uint32) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return ch.recvAck, ch.recvAckField
}

// processAcks processes an incoming ack and ack bitfield from the remote peer.
// It removes acknowledged packets from pendingSend, measures RTT for
// first-transmit packets (Karn's algorithm), and returns the list of
// acknowledged sequence numbers.
func (ch *channel) processAcks(ack, ackField uint32, r *rtt.State) []uint32 {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ack == 0 || len(ch.pendingSend) == 0 {
		return nil
	}

	now := time.Now()
	var acked []uint32

	// Build set of acked sequences.
	ackedSet := make(map[uint32]bool)
	ackedSet[ack] = true
	for i := uint32(0); i < 32; i++ {
		if ackField&(1<<i) != 0 {
			if ack > i+1 {
				ackedSet[ack-i-1] = true
			}
		}
	}

	// Remove acked packets from pendingSend.
	n := 0
	for _, p := range ch.pendingSend {
		if ackedSet[p.sequence] {
			acked = append(acked, p.sequence)
			// Measure RTT only for first-transmit packets (Karn's algorithm).
			if p.firstTransmit && r != nil {
				r.AddSample(now.Sub(p.sendTime))
			}
		} else {
			ch.pendingSend[n] = p
			n++
		}
	}
	// Clear trailing references to allow GC.
	for i := n; i < len(ch.pendingSend); i++ {
		ch.pendingSend[i] = pendingPacket{}
	}
	ch.pendingSend = ch.pendingSend[:n]

	return acked
}

// deliver processes an incoming payload according to the channel's delivery mode.
// Returns the payloads ready for application delivery (may be zero, one, or many).
func (ch *channel) deliver(seq uint32, payload []byte) [][]byte {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	switch ch.mode {
	case ReliableOrdered:
		return ch.deliverReliableOrdered(seq, payload)
	case ReliableUnordered:
		return ch.deliverReliableUnordered(payload)
	case Unreliable:
		return ch.deliverUnreliable(payload)
	case UnreliableSequenced:
		return ch.deliverUnreliableSequenced(seq, payload)
	default:
		return nil
	}
}

// deliverReliableOrdered buffers the payload and drains consecutive packets.
// Must be called with ch.mu held.
func (ch *channel) deliverReliableOrdered(seq uint32, payload []byte) [][]byte {
	// Store in reorder buffer.
	ch.recvBuffer[seq] = payload

	// Drain consecutive payloads starting from recvNextDeliver.
	var result [][]byte
	for {
		p, ok := ch.recvBuffer[ch.recvNextDeliver]
		if !ok {
			break
		}
		result = append(result, p)
		delete(ch.recvBuffer, ch.recvNextDeliver)
		ch.recvNextDeliver++
	}
	return result
}

// deliverReliableUnordered delivers immediately.
// Must be called with ch.mu held.
func (ch *channel) deliverReliableUnordered(payload []byte) [][]byte {
	return [][]byte{payload}
}

// deliverUnreliable delivers immediately.
// Must be called with ch.mu held.
func (ch *channel) deliverUnreliable(payload []byte) [][]byte {
	return [][]byte{payload}
}

// deliverUnreliableSequenced delivers only if the sequence is newer than the last seen.
// Must be called with ch.mu held.
func (ch *channel) deliverUnreliableSequenced(seq uint32, payload []byte) [][]byte {
	if seq > ch.lastRecvSeq {
		ch.lastRecvSeq = seq
		return [][]byte{payload}
	}
	return nil // stale, drop
}

// clearNeedsAck checks if this channel has pending acks and clears the flag.
func (ch *channel) clearNeedsAck() bool {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.needsAck {
		ch.needsAck = false
		return true
	}
	return false
}

// addPending appends a packet to the retransmission queue.
func (ch *channel) addPending(p pendingPacket) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.pendingSend = append(ch.pendingSend, p)
}

// pendingCount returns the number of unacked packets in the retransmission queue.
func (ch *channel) pendingCount() int {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return len(ch.pendingSend)
}

// checkRetransmissions returns packets that need retransmission and a kill flag
// indicating whether the connection should be terminated (max retries exceeded).
func (ch *channel) checkRetransmissions(now time.Time, rto time.Duration, maxRetries int) ([]pendingPacket, bool) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	var retransmit []pendingPacket
	for i := range ch.pendingSend {
		p := &ch.pendingSend[i]
		if now.Before(p.nextRetransmit) {
			continue
		}
		p.retransmitCount++
		if p.retransmitCount > maxRetries {
			return nil, true // kill connection
		}
		p.firstTransmit = false
		p.nextRetransmit = now.Add(rto)
		// Copy for the caller — raw is shared (immutable pre-encryption bytes).
		retransmit = append(retransmit, *p)
	}
	return retransmit, false
}
