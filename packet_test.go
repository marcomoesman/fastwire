package fastwire

import (
	"math"
	"testing"
)

func TestPacketHeaderRoundTrip(t *testing.T) {
	cases := []PacketHeader{
		{Flags: 0, Channel: 0, Sequence: 0, Ack: 0, AckField: 0},
		{Flags: FlagFragment, Channel: 1, Sequence: 127, Ack: 128, AckField: 0xDEADBEEF},
		{Flags: FlagControl, Channel: 255, Sequence: 16384, Ack: 16383, AckField: 0xFFFFFFFF},
		{Flags: FlagFragment | FlagControl, Channel: 42, Sequence: math.MaxUint32, Ack: math.MaxUint32, AckField: 0x12345678},
		{Flags: 0, Channel: 0, Sequence: 1, Ack: 1, AckField: 1},
	}

	buf := make([]byte, 16) // max header size
	for i, h := range cases {
		n, err := MarshalHeader(buf, &h)
		if err != nil {
			t.Fatalf("case %d: MarshalHeader: %v", i, err)
		}
		got, m, err := UnmarshalHeader(buf[:n])
		if err != nil {
			t.Fatalf("case %d: UnmarshalHeader: %v", i, err)
		}
		if got != h {
			t.Fatalf("case %d: round-trip mismatch:\n  got  %+v\n  want %+v", i, got, h)
		}
		if m != n {
			t.Fatalf("case %d: bytes: wrote %d, read %d", i, n, m)
		}
	}
}

func TestPacketHeaderAllFlags(t *testing.T) {
	flags := []PacketFlag{0, FlagFragment, FlagControl, FlagFragment | FlagControl}
	buf := make([]byte, 16)
	for _, f := range flags {
		h := PacketHeader{Flags: f, Sequence: 42, Ack: 10, AckField: 0xFF}
		n, err := MarshalHeader(buf, &h)
		if err != nil {
			t.Fatalf("MarshalHeader(flags=%d): %v", f, err)
		}
		got, _, err := UnmarshalHeader(buf[:n])
		if err != nil {
			t.Fatalf("UnmarshalHeader(flags=%d): %v", f, err)
		}
		if got.Flags != f {
			t.Fatalf("flag mismatch: got %d, want %d", got.Flags, f)
		}
	}
}

func TestPacketHeaderTruncated(t *testing.T) {
	// Too small to hold even the minimal header.
	_, _, err := UnmarshalHeader([]byte{0, 0, 0})
	if err != ErrInvalidPacketHeader {
		t.Fatalf("expected ErrInvalidPacketHeader, got %v", err)
	}
}

func TestPacketHeaderMarshalBufferTooSmall(t *testing.T) {
	h := PacketHeader{Sequence: 1, Ack: 1}
	buf := make([]byte, 4) // too small
	_, err := MarshalHeader(buf, &h)
	if err != ErrInvalidPacketHeader {
		t.Fatalf("expected ErrInvalidPacketHeader, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkPacketMarshal(b *testing.B) {
	hdr := &PacketHeader{
		Flags:    0,
		Channel:  0,
		Sequence: 42,
		Ack:      40,
		AckField: 0x00000003,
	}
	buf := make([]byte, 16)
	for b.Loop() {
		_, _ = MarshalHeader(buf, hdr)
	}
}

func BenchmarkPacketUnmarshal(b *testing.B) {
	hdr := &PacketHeader{
		Flags:    0,
		Channel:  0,
		Sequence: 42,
		Ack:      40,
		AckField: 0x00000003,
	}
	buf := make([]byte, 16)
	n, _ := MarshalHeader(buf, hdr)
	data := buf[:n]
	for b.Loop() {
		_, _, _ = UnmarshalHeader(data)
	}
}

func FuzzPacketHeader(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{3, 255, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F, 0xFF, 0xFF, 0xFF, 0xFF, 0x0F, 0xEF, 0xBE, 0xAD, 0xDE})
	f.Fuzz(func(t *testing.T, data []byte) {
		h, n, err := UnmarshalHeader(data)
		if err != nil {
			return
		}
		buf := make([]byte, 16)
		m, err := MarshalHeader(buf, &h)
		if err != nil {
			t.Fatalf("re-marshal failed: %v", err)
		}
		h2, _, err := UnmarshalHeader(buf[:m])
		if err != nil {
			t.Fatalf("re-unmarshal failed: %v", err)
		}
		if h2 != h {
			t.Fatalf("round-trip mismatch (consumed %d bytes)", n)
		}
	})
}
