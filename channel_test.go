package fastwire

import (
	"testing"
	"time"

	"github.com/marcomoesman/fastwire/internal/rtt"
)

// --- Ack tracking tests ---

func TestAckAdvance(t *testing.T) {
	ch := &channel{mode: Unreliable}
	ch.recvNextDeliver = 1

	// First packet.
	if !ch.recordReceive(1) {
		t.Fatal("seq 1 should be accepted")
	}
	ack, field := ch.ackState()
	if ack != 1 {
		t.Fatalf("recvAck = %d, want 1", ack)
	}
	if field != 0 {
		t.Fatalf("ackField = %032b, want 0", field)
	}

	// Advance to seq 3 (skip 2).
	if !ch.recordReceive(3) {
		t.Fatal("seq 3 should be accepted")
	}
	ack, field = ch.ackState()
	if ack != 3 {
		t.Fatalf("recvAck = %d, want 3", ack)
	}
	// Bit 1 (diff=2) should be set for old recvAck=1, bit 0 (diff=1) unset for missing seq=2.
	// After shift by 2: old ackField=0 << 2 = 0, then set bit (2-1)=bit 1 for old recvAck=1.
	if field&(1<<1) == 0 {
		t.Fatalf("bit for seq 1 should be set, field = %032b", field)
	}
	if field&(1<<0) != 0 {
		t.Fatalf("bit for seq 2 should not be set, field = %032b", field)
	}
}

func TestAckOutOfOrder(t *testing.T) {
	ch := &channel{mode: Unreliable}
	ch.recvNextDeliver = 1

	ch.recordReceive(5)
	ch.recordReceive(3) // out of order
	ch.recordReceive(4) // out of order

	ack, field := ch.ackState()
	if ack != 5 {
		t.Fatalf("recvAck = %d, want 5", ack)
	}
	// seq 3: diff = 5-3 = 2, bit 1
	// seq 4: diff = 5-4 = 1, bit 0
	if field&(1<<1) == 0 {
		t.Fatalf("bit for seq 3 should be set, field = %032b", field)
	}
	if field&(1<<0) == 0 {
		t.Fatalf("bit for seq 4 should be set, field = %032b", field)
	}
}

func TestAckDuplicate(t *testing.T) {
	ch := &channel{mode: Unreliable}
	ch.recvNextDeliver = 1

	if !ch.recordReceive(1) {
		t.Fatal("first receive of seq 1 should be accepted")
	}
	if ch.recordReceive(1) {
		t.Fatal("duplicate seq 1 should be rejected")
	}

	// Also test duplicate of an old packet.
	ch.recordReceive(5)
	if !ch.recordReceive(3) {
		t.Fatal("first receive of seq 3 should be accepted")
	}
	if ch.recordReceive(3) {
		t.Fatal("duplicate seq 3 should be rejected")
	}
}

func TestAckTooOld(t *testing.T) {
	ch := &channel{mode: Unreliable}
	ch.recvNextDeliver = 1

	ch.recordReceive(50)
	// seq 50 - 32 = 18, so seq 17 is too old.
	if ch.recordReceive(17) {
		t.Fatal("seq 17 should be too old (more than 32 behind recvAck 50)")
	}
	// seq 18 is at the boundary (diff=32), should be accepted.
	if !ch.recordReceive(18) {
		t.Fatal("seq 18 should be accepted (exactly 32 behind recvAck 50)")
	}
}

func TestAckBitfieldBoundary(t *testing.T) {
	ch := &channel{mode: Unreliable}
	ch.recvNextDeliver = 1

	// Set recvAck to 33.
	ch.recordReceive(33)

	// seq 1 is diff=32, should be accepted.
	if !ch.recordReceive(1) {
		t.Fatal("seq 1 at boundary should be accepted")
	}

	ack, field := ch.ackState()
	if ack != 33 {
		t.Fatalf("recvAck = %d, want 33", ack)
	}
	// seq 1: diff = 33-1 = 32, bit 31
	if field&(1<<31) == 0 {
		t.Fatalf("bit 31 for seq 1 should be set, field = %032b", field)
	}
}

func TestAckLargeGap(t *testing.T) {
	ch := &channel{mode: Unreliable}
	ch.recvNextDeliver = 1

	ch.recordReceive(1)
	// Large gap — should clear bitfield.
	ch.recordReceive(100)
	_, field := ch.ackState()
	// Only bit for the shifted old recvAck might be set, but diff=99 > 32
	// so the shift clears everything. Actually, when diff > 32, we set ackField=0
	// then we don't set any bit for the old recvAck since it's too far back.
	// Wait, re-reading: diff > 32 → ackField = 0, no bit set for old recvAck.
	// The plan says "shift ackField left by diff, set old recvAck bit".
	// But our implementation handles diff > 32 by clearing the field entirely.
	// That's correct because a 32-bit field can't represent gaps > 32.
	if field != 0 {
		t.Fatalf("bitfield should be 0 after large gap, got %032b", field)
	}
}

func TestAckSeqZeroRejected(t *testing.T) {
	ch := &channel{mode: Unreliable}
	ch.recvNextDeliver = 1
	if ch.recordReceive(0) {
		t.Fatal("sequence 0 should be rejected")
	}
}

// --- Delivery mode tests ---

func TestReliableOrderedReorderAndDrain(t *testing.T) {
	ch := &channel{
		mode:            ReliableOrdered,
		recvBuffer:      make(map[uint32][]byte),
		recvNextDeliver: 1,
	}

	// Receive seq 3, 1, 2 out of order.
	result := ch.deliver(3, []byte("three"))
	if len(result) != 0 {
		t.Fatalf("expected 0 deliveries, got %d", len(result))
	}

	result = ch.deliver(1, []byte("one"))
	if len(result) != 1 || string(result[0]) != "one" {
		t.Fatalf("expected [one], got %v", result)
	}

	result = ch.deliver(2, []byte("two"))
	if len(result) != 2 {
		t.Fatalf("expected 2 deliveries (two, three), got %d", len(result))
	}
	if string(result[0]) != "two" || string(result[1]) != "three" {
		t.Fatalf("expected [two, three], got [%s, %s]", result[0], result[1])
	}
}

func TestReliableUnorderedImmediate(t *testing.T) {
	ch := &channel{mode: ReliableUnordered, recvNextDeliver: 1}

	result := ch.deliver(5, []byte("data"))
	if len(result) != 1 || string(result[0]) != "data" {
		t.Fatalf("expected immediate delivery, got %v", result)
	}
}

func TestUnreliableImmediate(t *testing.T) {
	ch := &channel{mode: Unreliable, recvNextDeliver: 1}

	result := ch.deliver(1, []byte("data"))
	if len(result) != 1 || string(result[0]) != "data" {
		t.Fatalf("expected immediate delivery, got %v", result)
	}
}

func TestUnreliableSequencedDropStale(t *testing.T) {
	ch := &channel{mode: UnreliableSequenced, recvNextDeliver: 1}

	// Receive seq 5.
	result := ch.deliver(5, []byte("five"))
	if len(result) != 1 {
		t.Fatal("expected delivery of seq 5")
	}

	// Receive seq 3 (stale) — should be dropped.
	result = ch.deliver(3, []byte("three"))
	if len(result) != 0 {
		t.Fatal("stale seq 3 should be dropped")
	}

	// Receive seq 5 again (equal, not newer) — should be dropped.
	result = ch.deliver(5, []byte("five-again"))
	if len(result) != 0 {
		t.Fatal("equal seq 5 should be dropped")
	}

	// Receive seq 6 (newer) — should be delivered.
	result = ch.deliver(6, []byte("six"))
	if len(result) != 1 || string(result[0]) != "six" {
		t.Fatalf("expected delivery of seq 6, got %v", result)
	}
}

// --- Process acks tests ---

func TestProcessAcksWithRTT(t *testing.T) {
	ch := &channel{mode: ReliableOrdered, recvBuffer: make(map[uint32][]byte), recvNextDeliver: 1}
	rs := rtt.New()

	now := time.Now()
	ch.addPending(pendingPacket{
		raw:            []byte("packet1"),
		sendTime:       now.Add(-50 * time.Millisecond),
		firstTransmit:  true,
		sequence:       1,
		nextRetransmit: now.Add(1 * time.Second),
	})
	ch.addPending(pendingPacket{
		raw:            []byte("packet2"),
		sendTime:       now.Add(-30 * time.Millisecond),
		firstTransmit:  true,
		sequence:       2,
		nextRetransmit: now.Add(1 * time.Second),
	})

	// Ack seq 2, with bitfield bit 0 set -> also acks seq 1.
	acked := ch.processAcks(2, 0x00000001, rs)

	if len(acked) != 2 {
		t.Fatalf("expected 2 acked, got %d", len(acked))
	}
	if ch.pendingCount() != 0 {
		t.Fatalf("pending count = %d, want 0", ch.pendingCount())
	}

	// RTT should have samples.
	if rs.SRTT() == 0 {
		t.Fatal("SRTT should be > 0 after ack processing")
	}
}

func TestProcessAcksKarnsAlgorithm(t *testing.T) {
	ch := &channel{mode: ReliableOrdered, recvBuffer: make(map[uint32][]byte), recvNextDeliver: 1}
	rs := rtt.New()

	now := time.Now()
	// Retransmitted packet -- firstTransmit = false.
	ch.addPending(pendingPacket{
		raw:            []byte("retransmitted"),
		sendTime:       now.Add(-100 * time.Millisecond),
		firstTransmit:  false,
		sequence:       1,
		nextRetransmit: now.Add(1 * time.Second),
	})

	ch.processAcks(1, 0, rs)

	// RTT should NOT have been updated (Karn's algorithm skips retransmissions).
	if rs.SRTT() != 0 {
		t.Fatalf("SRTT should be 0 (no sample from retransmit), got %v", rs.SRTT())
	}
}

func TestProcessAcksZeroAck(t *testing.T) {
	ch := &channel{mode: ReliableOrdered, recvBuffer: make(map[uint32][]byte), recvNextDeliver: 1}
	rs := rtt.New()

	ch.addPending(pendingPacket{sequence: 1, firstTransmit: true, nextRetransmit: time.Now().Add(time.Second)})

	// ack=0 means "nothing received" -- should not remove anything.
	acked := ch.processAcks(0, 0, rs)
	if len(acked) != 0 {
		t.Fatalf("expected 0 acked with ack=0, got %d", len(acked))
	}
	if ch.pendingCount() != 1 {
		t.Fatalf("pending count = %d, want 1", ch.pendingCount())
	}
}

// --- Retransmission tests ---

func TestRetransmissionScheduling(t *testing.T) {
	ch := &channel{mode: ReliableOrdered, recvBuffer: make(map[uint32][]byte), recvNextDeliver: 1}

	past := time.Now().Add(-100 * time.Millisecond)
	ch.addPending(pendingPacket{
		raw:            []byte("data"),
		sendTime:       past,
		firstTransmit:  true,
		sequence:       1,
		nextRetransmit: past, // already due
	})

	rto := 200 * time.Millisecond
	retransmit, kill := ch.checkRetransmissions(time.Now(), rto, maxRetransmits)
	if kill {
		t.Fatal("should not kill")
	}
	if len(retransmit) != 1 {
		t.Fatalf("expected 1 retransmit, got %d", len(retransmit))
	}
	if retransmit[0].firstTransmit {
		t.Fatal("retransmit should have firstTransmit=false")
	}
	if retransmit[0].retransmitCount != 1 {
		t.Fatalf("retransmitCount = %d, want 1", retransmit[0].retransmitCount)
	}
}

func TestRetransmissionMaxRetriesKill(t *testing.T) {
	ch := &channel{mode: ReliableOrdered, recvBuffer: make(map[uint32][]byte), recvNextDeliver: 1}

	past := time.Now().Add(-100 * time.Millisecond)
	ch.addPending(pendingPacket{
		raw:             []byte("data"),
		sendTime:        past,
		firstTransmit:   false,
		sequence:        1,
		retransmitCount: maxRetransmits, // already at max
		nextRetransmit:  past,
	})

	_, kill := ch.checkRetransmissions(time.Now(), 200*time.Millisecond, maxRetransmits)
	if !kill {
		t.Fatal("should kill when max retransmits exceeded")
	}
}

func TestRetransmissionNotYetDue(t *testing.T) {
	ch := &channel{mode: ReliableOrdered, recvBuffer: make(map[uint32][]byte), recvNextDeliver: 1}

	future := time.Now().Add(10 * time.Second)
	ch.addPending(pendingPacket{
		raw:            []byte("data"),
		sendTime:       time.Now(),
		firstTransmit:  true,
		sequence:       1,
		nextRetransmit: future, // not due yet
	})

	retransmit, kill := ch.checkRetransmissions(time.Now(), 200*time.Millisecond, maxRetransmits)
	if kill {
		t.Fatal("should not kill")
	}
	if len(retransmit) != 0 {
		t.Fatalf("expected 0 retransmits, got %d", len(retransmit))
	}
}

// --- Next sequence tests ---

func TestNextSequenceStartsAtOne(t *testing.T) {
	ch := &channel{mode: Unreliable, recvNextDeliver: 1}
	if seq := ch.nextSequence(); seq != 1 {
		t.Fatalf("first sequence = %d, want 1", seq)
	}
	if seq := ch.nextSequence(); seq != 2 {
		t.Fatalf("second sequence = %d, want 2", seq)
	}
}

// --- Channel layout builder tests ---

func TestDefaultChannelLayout(t *testing.T) {
	layout := DefaultChannelLayout()
	if layout.Len() != 4 {
		t.Fatalf("default layout len = %d, want 4", layout.Len())
	}
	modes := []DeliveryMode{ReliableOrdered, ReliableUnordered, Unreliable, UnreliableSequenced}
	for i, want := range modes {
		if layout.channels[i].Mode != want {
			t.Fatalf("channel %d mode = %d, want %d", i, layout.channels[i].Mode, want)
		}
	}
}

func TestChannelLayoutBuilder(t *testing.T) {
	layout, err := NewChannelLayoutBuilder().
		AddChannel(ReliableOrdered, 0).
		AddChannel(Unreliable, 1).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if layout.Len() != 2 {
		t.Fatalf("layout len = %d, want 2", layout.Len())
	}
}

func TestChannelLayoutBuilderEmpty(t *testing.T) {
	_, err := NewChannelLayoutBuilder().Build()
	if err != ErrInvalidChannelLayout {
		t.Fatalf("expected ErrInvalidChannelLayout, got %v", err)
	}
}

func TestChannelLayoutBuilderTooMany(t *testing.T) {
	b := NewChannelLayoutBuilder()
	for range 257 {
		b.AddChannel(Unreliable, 0)
	}
	_, err := b.Build()
	if err != ErrInvalidChannelLayout {
		t.Fatalf("expected ErrInvalidChannelLayout, got %v", err)
	}
}

func TestChannelLayoutBuilderMax(t *testing.T) {
	b := NewChannelLayoutBuilder()
	for range 256 {
		b.AddChannel(Unreliable, 0)
	}
	layout, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	if layout.Len() != 256 {
		t.Fatalf("layout len = %d, want 256", layout.Len())
	}
}

// --- newChannels test ---

func TestNewChannels(t *testing.T) {
	layout := DefaultChannelLayout()
	chs := newChannels(layout)
	if len(chs) != 4 {
		t.Fatalf("len(channels) = %d, want 4", len(chs))
	}

	// ReliableOrdered should have a recvBuffer.
	if chs[0].recvBuffer == nil {
		t.Fatal("channel 0 (ReliableOrdered) should have recvBuffer")
	}
	// Others should not.
	for i := 1; i < 4; i++ {
		if chs[i].recvBuffer != nil {
			t.Fatalf("channel %d should not have recvBuffer", i)
		}
	}
}
