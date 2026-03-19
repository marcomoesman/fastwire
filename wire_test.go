package fastwire

import (
	"math"
	"regexp"
	"testing"
)

// ---------------------------------------------------------------------------
// VarInt tests
// ---------------------------------------------------------------------------

func TestVarIntRoundTrip(t *testing.T) {
	cases := []uint32{0, 1, 127, 128, 255, 16383, 16384, 65535, math.MaxUint32}
	buf := make([]byte, 5)
	for _, v := range cases {
		n := PutVarInt(buf, v)
		got, m, err := ReadVarInt(buf[:n])
		if err != nil {
			t.Fatalf("ReadVarInt(%d): %v", v, err)
		}
		if got != v {
			t.Fatalf("VarInt round-trip: got %d, want %d", got, v)
		}
		if m != n {
			t.Fatalf("VarInt bytes: wrote %d, read %d", n, m)
		}
	}
}

func TestVarIntTruncated(t *testing.T) {
	buf := []byte{0x80} // continuation bit set, but no more bytes
	_, _, err := ReadVarInt(buf)
	if err != ErrBufferTooSmall {
		t.Fatalf("expected ErrBufferTooSmall, got %v", err)
	}
}

func TestVarIntOverflow(t *testing.T) {
	// 6 bytes with continuation bits — exceeds 5-byte limit.
	buf := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x00}
	_, _, err := ReadVarInt(buf)
	if err != ErrVarIntOverflow {
		t.Fatalf("expected ErrVarIntOverflow, got %v", err)
	}
}

func TestVarIntEmpty(t *testing.T) {
	_, _, err := ReadVarInt(nil)
	if err != ErrBufferTooSmall {
		t.Fatalf("expected ErrBufferTooSmall, got %v", err)
	}
}

func FuzzVarInt(f *testing.F) {
	f.Add([]byte{0})
	f.Add([]byte{0x80, 0x01})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x0F})
	f.Fuzz(func(t *testing.T, data []byte) {
		v, n, err := ReadVarInt(data)
		if err != nil {
			return
		}
		buf := make([]byte, 5)
		m := PutVarInt(buf, v)
		v2, _, err2 := ReadVarInt(buf[:m])
		if err2 != nil {
			t.Fatalf("re-encode failed: %v", err2)
		}
		if v2 != v {
			t.Fatalf("round-trip mismatch: %d vs %d (consumed %d bytes)", v, v2, n)
		}
	})
}

// ---------------------------------------------------------------------------
// VarLong tests
// ---------------------------------------------------------------------------

func TestVarLongRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 255, 16383, 16384, math.MaxUint32, math.MaxUint64}
	buf := make([]byte, 10)
	for _, v := range cases {
		n := PutVarLong(buf, v)
		got, m, err := ReadVarLong(buf[:n])
		if err != nil {
			t.Fatalf("ReadVarLong(%d): %v", v, err)
		}
		if got != v {
			t.Fatalf("VarLong round-trip: got %d, want %d", got, v)
		}
		if m != n {
			t.Fatalf("VarLong bytes: wrote %d, read %d", n, m)
		}
	}
}

func TestVarLongTruncated(t *testing.T) {
	buf := []byte{0x80}
	_, _, err := ReadVarLong(buf)
	if err != ErrBufferTooSmall {
		t.Fatalf("expected ErrBufferTooSmall, got %v", err)
	}
}

func TestVarLongOverflow(t *testing.T) {
	// 11 bytes with continuation bits — exceeds 10-byte limit.
	buf := make([]byte, 11)
	for i := range buf {
		buf[i] = 0x80
	}
	buf[10] = 0x00
	_, _, err := ReadVarLong(buf)
	if err != ErrVarLongOverflow {
		t.Fatalf("expected ErrVarLongOverflow, got %v", err)
	}
}

func FuzzVarLong(f *testing.F) {
	f.Add([]byte{0})
	f.Add([]byte{0x80, 0x01})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01})
	f.Fuzz(func(t *testing.T, data []byte) {
		v, n, err := ReadVarLong(data)
		if err != nil {
			return
		}
		buf := make([]byte, 10)
		m := PutVarLong(buf, v)
		v2, _, err2 := ReadVarLong(buf[:m])
		if err2 != nil {
			t.Fatalf("re-encode failed: %v", err2)
		}
		if v2 != v {
			t.Fatalf("round-trip mismatch: %d vs %d (consumed %d bytes)", v, v2, n)
		}
	})
}

// ---------------------------------------------------------------------------
// String tests
// ---------------------------------------------------------------------------

func TestStringRoundTrip(t *testing.T) {
	cases := []string{"", "hello", "日本語テスト"}
	buf := make([]byte, 256)
	for _, s := range cases {
		n, err := PutString(buf, s)
		if err != nil {
			t.Fatalf("PutString(%q): %v", s, err)
		}
		got, m, err := ReadString(buf[:n])
		if err != nil {
			t.Fatalf("ReadString(%q): %v", s, err)
		}
		if got != s {
			t.Fatalf("String round-trip: got %q, want %q", got, s)
		}
		if m != n {
			t.Fatalf("String bytes: wrote %d, read %d", n, m)
		}
	}
}

func TestStringMaxLength(t *testing.T) {
	s := string(make([]byte, 32767))
	buf := make([]byte, 2+32767)
	n, err := PutString(buf, s)
	if err != nil {
		t.Fatalf("PutString(max): %v", err)
	}
	got, m, err := ReadString(buf[:n])
	if err != nil {
		t.Fatalf("ReadString(max): %v", err)
	}
	if len(got) != 32767 {
		t.Fatalf("max string length: got %d, want 32767", len(got))
	}
	if m != n {
		t.Fatalf("max string bytes: wrote %d, read %d", n, m)
	}
}

func TestStringTooLong(t *testing.T) {
	s := string(make([]byte, 32768))
	buf := make([]byte, 2+32768)
	_, err := PutString(buf, s)
	if err != ErrStringTooLong {
		t.Fatalf("expected ErrStringTooLong, got %v", err)
	}
}

func TestStringNegativeLength(t *testing.T) {
	// int16(-1) = 0xFFFF in big-endian
	buf := []byte{0xFF, 0xFF}
	_, _, err := ReadString(buf)
	if err != ErrNegativeStringLength {
		t.Fatalf("expected ErrNegativeStringLength, got %v", err)
	}
}

func TestStringBufferTooSmall(t *testing.T) {
	buf := make([]byte, 3)
	_, err := PutString(buf, "hello")
	if err != ErrBufferTooSmall {
		t.Fatalf("expected ErrBufferTooSmall, got %v", err)
	}
}

func TestStringUTF8Preservation(t *testing.T) {
	s := "こんにちは🌍"
	buf := make([]byte, 2+len(s))
	n, err := PutString(buf, s)
	if err != nil {
		t.Fatalf("PutString: %v", err)
	}
	got, _, err := ReadString(buf[:n])
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if got != s {
		t.Fatalf("UTF-8 mismatch: got %q, want %q", got, s)
	}
}

func FuzzString(f *testing.F) {
	f.Add([]byte{0, 0})
	f.Add([]byte{0, 5, 'h', 'e', 'l', 'l', 'o'})
	f.Fuzz(func(t *testing.T, data []byte) {
		s, _, err := ReadString(data)
		if err != nil {
			return
		}
		buf := make([]byte, 2+len(s))
		n, err := PutString(buf, s)
		if err != nil {
			return // buffer too small for re-encode is fine
		}
		s2, _, err := ReadString(buf[:n])
		if err != nil {
			t.Fatalf("re-encode failed: %v", err)
		}
		if s2 != s {
			t.Fatalf("round-trip mismatch: %q vs %q", s, s2)
		}
	})
}

// ---------------------------------------------------------------------------
// UUID tests
// ---------------------------------------------------------------------------

func TestUUIDRoundTrip(t *testing.T) {
	id := UUIDFromInts(0x0123456789ABCDEF, 0xFEDCBA9876543210)
	buf := make([]byte, 16)
	PutUUID(buf, id)
	got, n, err := ReadUUID(buf)
	if err != nil {
		t.Fatalf("ReadUUID: %v", err)
	}
	if got != id {
		t.Fatalf("UUID round-trip: got %v, want %v", got, id)
	}
	if n != 16 {
		t.Fatalf("UUID bytes: %d, want 16", n)
	}
}

func TestUUIDMSBLSB(t *testing.T) {
	msb := uint64(0x0123456789ABCDEF)
	lsb := uint64(0xFEDCBA9876543210)
	id := UUIDFromInts(msb, lsb)
	if id.MSB() != msb {
		t.Fatalf("MSB: got %016x, want %016x", id.MSB(), msb)
	}
	if id.LSB() != lsb {
		t.Fatalf("LSB: got %016x, want %016x", id.LSB(), lsb)
	}
}

func TestUUIDFromIntsRoundTrip(t *testing.T) {
	msb := uint64(0xDEADBEEFCAFEBABE)
	lsb := uint64(0x1234567890ABCDEF)
	id := UUIDFromInts(msb, lsb)
	if id.MSB() != msb || id.LSB() != lsb {
		t.Fatalf("UUIDFromInts round-trip failed")
	}
}

func TestUUIDString(t *testing.T) {
	id := UUIDFromInts(0x0123456789ABCDEF, 0xFEDCBA9876543210)
	s := id.String()
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	if !re.MatchString(s) {
		t.Fatalf("UUID.String() format invalid: %q", s)
	}
}

func TestUUIDBufferTooSmall(t *testing.T) {
	buf := make([]byte, 15)
	_, _, err := ReadUUID(buf)
	if err != ErrInvalidUUID {
		t.Fatalf("expected ErrInvalidUUID, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkVarIntEncode(b *testing.B) {
	buf := make([]byte, 5)
	for b.Loop() {
		PutVarInt(buf, 300)
	}
}

func BenchmarkVarIntDecode(b *testing.B) {
	buf := make([]byte, 5)
	n := PutVarInt(buf, 300)
	encoded := buf[:n]
	for b.Loop() {
		_, _, _ = ReadVarInt(encoded)
	}
}

func FuzzUUID(f *testing.F) {
	f.Add(make([]byte, 16))
	f.Fuzz(func(t *testing.T, data []byte) {
		id, _, err := ReadUUID(data)
		if err != nil {
			return
		}
		buf := make([]byte, 16)
		PutUUID(buf, id)
		id2, _, err := ReadUUID(buf)
		if err != nil {
			t.Fatalf("re-encode failed: %v", err)
		}
		if id2 != id {
			t.Fatalf("round-trip mismatch")
		}
	})
}
