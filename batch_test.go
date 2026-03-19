package fastwire

import (
	"bytes"
	"testing"
)

func TestBatchMarshalUnmarshalSingle(t *testing.T) {
	pkt := []byte("hello encrypted packet")
	dst := make([]byte, 256)
	n, err := MarshalBatch(dst, [][]byte{pkt})
	if err != nil {
		t.Fatalf("MarshalBatch: %v", err)
	}

	packets, err := UnmarshalBatch(dst[:n])
	if err != nil {
		t.Fatalf("UnmarshalBatch: %v", err)
	}
	if len(packets) != 1 {
		t.Fatalf("count = %d, want 1", len(packets))
	}
	if !bytes.Equal(packets[0], pkt) {
		t.Fatalf("packet mismatch")
	}
}

func TestBatchMarshalUnmarshalMultiple(t *testing.T) {
	pkts := [][]byte{
		[]byte("packet-one"),
		[]byte("packet-two"),
		[]byte("packet-three-is-longer"),
	}
	dst := make([]byte, 512)
	n, err := MarshalBatch(dst, pkts)
	if err != nil {
		t.Fatalf("MarshalBatch: %v", err)
	}

	// Verify header byte.
	if dst[0] != 3 {
		t.Fatalf("count byte = %d, want 3", dst[0])
	}

	result, err := UnmarshalBatch(dst[:n])
	if err != nil {
		t.Fatalf("UnmarshalBatch: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("count = %d, want 3", len(result))
	}
	for i, p := range pkts {
		if !bytes.Equal(result[i], p) {
			t.Fatalf("packet %d mismatch: got %q, want %q", i, result[i], p)
		}
	}
}

func TestBatchMarshalBufferTooSmall(t *testing.T) {
	pkt := []byte("data")
	dst := make([]byte, 2) // too small
	_, err := MarshalBatch(dst, [][]byte{pkt})
	if err != ErrBufferTooSmall {
		t.Fatalf("err = %v, want ErrBufferTooSmall", err)
	}
}

func TestBatchMarshalEmpty(t *testing.T) {
	dst := make([]byte, 64)
	_, err := MarshalBatch(dst, nil)
	if err != ErrInvalidBatchFrame {
		t.Fatalf("err = %v, want ErrInvalidBatchFrame", err)
	}
}

func TestBatchMarshalTooMany(t *testing.T) {
	pkts := make([][]byte, 256)
	for i := range pkts {
		pkts[i] = []byte{byte(i)}
	}
	dst := make([]byte, 4096)
	_, err := MarshalBatch(dst, pkts)
	if err != ErrInvalidBatchFrame {
		t.Fatalf("err = %v, want ErrInvalidBatchFrame", err)
	}
}

func TestBatchUnmarshalTruncated(t *testing.T) {
	// Count says 2 packets but only 1 is present.
	dst := make([]byte, 64)
	n, _ := MarshalBatch(dst, [][]byte{[]byte("a"), []byte("b")})
	// Truncate after first packet.
	_, err := UnmarshalBatch(dst[:n-1])
	if err != ErrInvalidBatchFrame {
		t.Fatalf("err = %v, want ErrInvalidBatchFrame", err)
	}
}

func TestBatchUnmarshalZeroCount(t *testing.T) {
	data := []byte{0}
	_, err := UnmarshalBatch(data)
	if err != ErrInvalidBatchFrame {
		t.Fatalf("err = %v, want ErrInvalidBatchFrame", err)
	}
}

func TestBatchOverhead(t *testing.T) {
	if got := BatchOverhead(1); got != 3 {
		t.Fatalf("overhead(1) = %d, want 3", got)
	}
	if got := BatchOverhead(5); got != 11 {
		t.Fatalf("overhead(5) = %d, want 11", got)
	}
}

func TestBatchRoundTripEmptyPackets(t *testing.T) {
	pkts := [][]byte{{}, {}, {}}
	dst := make([]byte, 64)
	n, err := MarshalBatch(dst, pkts)
	if err != nil {
		t.Fatalf("MarshalBatch: %v", err)
	}
	result, err := UnmarshalBatch(dst[:n])
	if err != nil {
		t.Fatalf("UnmarshalBatch: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("count = %d, want 3", len(result))
	}
	for i, p := range result {
		if len(p) != 0 {
			t.Fatalf("packet %d len = %d, want 0", i, len(p))
		}
	}
}
