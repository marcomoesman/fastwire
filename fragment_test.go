package fastwire

import (
	"bytes"
	"crypto/rand"
	"net/netip"
	"testing"
	"time"
)

func TestFragmentHeaderRoundTrip(t *testing.T) {
	cases := []FragmentHeader{
		{FragmentID: 0, FragmentIndex: 0, FragmentCount: 1, FragmentFlags: 0},
		{FragmentID: 0xFFFF, FragmentIndex: 255, FragmentCount: 255, FragmentFlags: FragFlagCompressed | FragFlagZstd},
		{FragmentID: 1234, FragmentIndex: 5, FragmentCount: 10, FragmentFlags: FragFlagCompressed},
	}

	buf := make([]byte, FragmentHeaderSize)
	for i, h := range cases {
		n, err := MarshalFragmentHeader(buf, &h)
		if err != nil {
			t.Fatalf("case %d: MarshalFragmentHeader: %v", i, err)
		}
		if n != FragmentHeaderSize {
			t.Fatalf("case %d: wrote %d bytes, want %d", i, n, FragmentHeaderSize)
		}
		got, m, err := UnmarshalFragmentHeader(buf)
		if err != nil {
			t.Fatalf("case %d: UnmarshalFragmentHeader: %v", i, err)
		}
		if got != h {
			t.Fatalf("case %d: round-trip mismatch:\n  got  %+v\n  want %+v", i, got, h)
		}
		if m != FragmentHeaderSize {
			t.Fatalf("case %d: read %d bytes, want %d", i, m, FragmentHeaderSize)
		}
	}
}

func TestFragmentHeaderAllFlags(t *testing.T) {
	flags := []FragmentFlag{0, FragFlagCompressed, FragFlagZstd, FragFlagCompressed | FragFlagZstd}
	buf := make([]byte, FragmentHeaderSize)
	for _, f := range flags {
		h := FragmentHeader{FragmentID: 1, FragmentIndex: 0, FragmentCount: 1, FragmentFlags: f}
		_, err := MarshalFragmentHeader(buf, &h)
		if err != nil {
			t.Fatalf("MarshalFragmentHeader(flags=%d): %v", f, err)
		}
		got, _, err := UnmarshalFragmentHeader(buf)
		if err != nil {
			t.Fatalf("UnmarshalFragmentHeader(flags=%d): %v", f, err)
		}
		if got.FragmentFlags != f {
			t.Fatalf("flag mismatch: got %d, want %d", got.FragmentFlags, f)
		}
	}
}

func TestFragmentHeaderTruncated(t *testing.T) {
	bufs := [][]byte{nil, {0}, {0, 0}, {0, 0, 0}, {0, 0, 0, 0}}
	for _, buf := range bufs {
		_, _, err := UnmarshalFragmentHeader(buf)
		if err != ErrInvalidFragmentHeader {
			t.Fatalf("len=%d: expected ErrInvalidFragmentHeader, got %v", len(buf), err)
		}
	}
}

func TestFragmentHeaderMarshalBufferTooSmall(t *testing.T) {
	h := FragmentHeader{FragmentID: 1, FragmentCount: 1}
	buf := make([]byte, 4) // too small
	_, err := MarshalFragmentHeader(buf, &h)
	if err != ErrInvalidFragmentHeader {
		t.Fatalf("expected ErrInvalidFragmentHeader, got %v", err)
	}
}

// --- Phase 5 tests ---

func TestSplitReassembleRoundTrip(t *testing.T) {
	// Create a payload that spans 4 fragments.
	payload := make([]byte, MaxFragmentPayload*3+100)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	frags, err := splitMessage(payload, 1, 0)
	if err != nil {
		t.Fatalf("splitMessage: %v", err)
	}
	if len(frags) != 4 {
		t.Fatalf("expected 4 fragments, got %d", len(frags))
	}

	store := newReassemblyStore()
	for _, frag := range frags {
		fh, n, err := UnmarshalFragmentHeader(frag)
		if err != nil {
			t.Fatalf("UnmarshalFragmentHeader: %v", err)
		}
		assembled, complete, err := store.addFragment(fh, frag[n:])
		if err != nil {
			t.Fatalf("addFragment: %v", err)
		}
		if fh.FragmentIndex < fh.FragmentCount-1 {
			if complete {
				t.Fatalf("should not be complete at index %d", fh.FragmentIndex)
			}
		} else {
			if !complete {
				t.Fatal("should be complete after last fragment")
			}
			if !bytes.Equal(assembled, payload) {
				t.Fatal("reassembled payload does not match original")
			}
		}
	}
}

func TestOutOfOrderReassembly(t *testing.T) {
	payload := make([]byte, MaxFragmentPayload*3+100)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	frags, err := splitMessage(payload, 2, 0)
	if err != nil {
		t.Fatalf("splitMessage: %v", err)
	}

	// Feed in reverse order.
	store := newReassemblyStore()
	for i := len(frags) - 1; i >= 0; i-- {
		fh, n, err := UnmarshalFragmentHeader(frags[i])
		if err != nil {
			t.Fatalf("UnmarshalFragmentHeader: %v", err)
		}
		assembled, complete, err := store.addFragment(fh, frags[i][n:])
		if err != nil {
			t.Fatalf("addFragment(index=%d): %v", fh.FragmentIndex, err)
		}
		if i == 0 {
			if !complete {
				t.Fatal("should be complete after all fragments fed")
			}
			if !bytes.Equal(assembled, payload) {
				t.Fatal("reassembled payload does not match original")
			}
		} else {
			if complete {
				t.Fatalf("should not be complete yet (remaining %d)", i)
			}
		}
	}
}

func TestDuplicateFragment(t *testing.T) {
	payload := make([]byte, MaxFragmentPayload*2)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	frags, err := splitMessage(payload, 3, 0)
	if err != nil {
		t.Fatalf("splitMessage: %v", err)
	}

	store := newReassemblyStore()

	// Feed first fragment.
	fh0, n0, _ := UnmarshalFragmentHeader(frags[0])
	_, complete, err := store.addFragment(fh0, frags[0][n0:])
	if err != nil || complete {
		t.Fatalf("first add: err=%v complete=%v", err, complete)
	}

	// Feed same fragment again — should be silent skip.
	_, complete, err = store.addFragment(fh0, frags[0][n0:])
	if err != nil || complete {
		t.Fatalf("duplicate add: err=%v complete=%v", err, complete)
	}

	// Feed second fragment — should complete.
	fh1, n1, _ := UnmarshalFragmentHeader(frags[1])
	assembled, complete, err := store.addFragment(fh1, frags[1][n1:])
	if err != nil || !complete {
		t.Fatalf("second add: err=%v complete=%v", err, complete)
	}
	if !bytes.Equal(assembled, payload) {
		t.Fatal("reassembled payload mismatch")
	}
}

func TestCountMismatch(t *testing.T) {
	store := newReassemblyStore()

	// First fragment says count=3.
	fh1 := FragmentHeader{FragmentID: 10, FragmentIndex: 0, FragmentCount: 3, FragmentFlags: 0}
	_, _, err := store.addFragment(fh1, []byte("hello"))
	if err != nil {
		t.Fatalf("first add: %v", err)
	}

	// Second fragment with same ID but count=4 — should error.
	fh2 := FragmentHeader{FragmentID: 10, FragmentIndex: 1, FragmentCount: 4, FragmentFlags: 0}
	_, _, err = store.addFragment(fh2, []byte("world"))
	if err != ErrInvalidFragmentHeader {
		t.Fatalf("expected ErrInvalidFragmentHeader, got %v", err)
	}
}

func TestFlagsMismatch(t *testing.T) {
	store := newReassemblyStore()

	fh1 := FragmentHeader{FragmentID: 11, FragmentIndex: 0, FragmentCount: 2, FragmentFlags: FragFlagCompressed}
	_, _, err := store.addFragment(fh1, []byte("data"))
	if err != nil {
		t.Fatalf("first add: %v", err)
	}

	fh2 := FragmentHeader{FragmentID: 11, FragmentIndex: 1, FragmentCount: 2, FragmentFlags: 0}
	_, _, err = store.addFragment(fh2, []byte("more"))
	if err != ErrInvalidFragmentHeader {
		t.Fatalf("expected ErrInvalidFragmentHeader, got %v", err)
	}
}

func TestStaleBufferCleanup(t *testing.T) {
	store := newReassemblyStore()

	fh := FragmentHeader{FragmentID: 20, FragmentIndex: 0, FragmentCount: 3, FragmentFlags: 0}
	_, _, err := store.addFragment(fh, []byte("data"))
	if err != nil {
		t.Fatalf("addFragment: %v", err)
	}
	if store.pending() != 1 {
		t.Fatalf("expected 1 pending, got %d", store.pending())
	}

	// Manually set createdAt to the past.
	store.mu.Lock()
	store.buffers[20].createdAt = time.Now().Add(-10 * time.Second)
	store.mu.Unlock()

	removed := store.cleanup(5 * time.Second)
	if removed != 1 {
		t.Fatalf("expected 1 removed, got %d", removed)
	}
	if store.pending() != 0 {
		t.Fatalf("expected 0 pending after cleanup, got %d", store.pending())
	}
}

func TestSingleFragmentEnvelope(t *testing.T) {
	payload := []byte("small message")

	frags, err := splitMessage(payload, 5, FragFlagCompressed|FragFlagZstd)
	if err != nil {
		t.Fatalf("splitMessage: %v", err)
	}
	if len(frags) != 1 {
		t.Fatalf("expected 1 fragment, got %d", len(frags))
	}

	fh, n, err := UnmarshalFragmentHeader(frags[0])
	if err != nil {
		t.Fatalf("UnmarshalFragmentHeader: %v", err)
	}
	if fh.FragmentCount != 1 {
		t.Fatalf("expected count=1, got %d", fh.FragmentCount)
	}
	if fh.FragmentFlags != FragFlagCompressed|FragFlagZstd {
		t.Fatalf("flags mismatch: got %d", fh.FragmentFlags)
	}

	store := newReassemblyStore()
	assembled, complete, err := store.addFragment(fh, frags[0][n:])
	if err != nil || !complete {
		t.Fatalf("single fragment: err=%v complete=%v", err, complete)
	}
	if !bytes.Equal(assembled, payload) {
		t.Fatal("payload mismatch")
	}
}

func TestMessageTooLarge(t *testing.T) {
	// 256 fragments would be needed.
	payload := make([]byte, MaxFragmentPayload*256)
	_, err := splitMessage(payload, 1, 0)
	if err != ErrMessageTooLarge {
		t.Fatalf("expected ErrMessageTooLarge, got %v", err)
	}
}

func TestEmptyPayload(t *testing.T) {
	frags, err := splitMessage(nil, 6, 0)
	if err != nil {
		t.Fatalf("splitMessage(nil): %v", err)
	}
	if len(frags) != 1 {
		t.Fatalf("expected 1 fragment, got %d", len(frags))
	}

	fh, n, err := UnmarshalFragmentHeader(frags[0])
	if err != nil {
		t.Fatalf("UnmarshalFragmentHeader: %v", err)
	}
	if fh.FragmentCount != 1 {
		t.Fatalf("expected count=1, got %d", fh.FragmentCount)
	}
	if len(frags[0][n:]) != 0 {
		t.Fatalf("expected empty chunk, got %d bytes", len(frags[0][n:]))
	}

	store := newReassemblyStore()
	assembled, complete, err := store.addFragment(fh, frags[0][n:])
	if err != nil || !complete {
		t.Fatalf("empty reassembly: err=%v complete=%v", err, complete)
	}
	if len(assembled) != 0 {
		t.Fatalf("expected empty assembled, got %d bytes", len(assembled))
	}
}

func TestExactBoundary(t *testing.T) {
	// Exactly MaxFragmentPayload → 1 fragment.
	payload1 := make([]byte, MaxFragmentPayload)
	frags1, err := splitMessage(payload1, 7, 0)
	if err != nil {
		t.Fatalf("splitMessage(exact): %v", err)
	}
	if len(frags1) != 1 {
		t.Fatalf("expected 1 fragment for exact boundary, got %d", len(frags1))
	}

	// MaxFragmentPayload + 1 → 2 fragments.
	payload2 := make([]byte, MaxFragmentPayload+1)
	frags2, err := splitMessage(payload2, 8, 0)
	if err != nil {
		t.Fatalf("splitMessage(exact+1): %v", err)
	}
	if len(frags2) != 2 {
		t.Fatalf("expected 2 fragments for exact+1, got %d", len(frags2))
	}
}

func TestMaxFragments255(t *testing.T) {
	// Exactly 255 fragments should succeed.
	payload := make([]byte, MaxFragmentPayload*255)
	frags, err := splitMessage(payload, 9, 0)
	if err != nil {
		t.Fatalf("splitMessage(255 frags): %v", err)
	}
	if len(frags) != 255 {
		t.Fatalf("expected 255 fragments, got %d", len(frags))
	}

	// Verify all have correct count.
	for i, f := range frags {
		fh, _, err := UnmarshalFragmentHeader(f)
		if err != nil {
			t.Fatalf("fragment %d: %v", i, err)
		}
		if fh.FragmentCount != 255 {
			t.Fatalf("fragment %d: count=%d, want 255", i, fh.FragmentCount)
		}
		if fh.FragmentIndex != byte(i) {
			t.Fatalf("fragment %d: index=%d, want %d", i, fh.FragmentIndex, i)
		}
	}
}

func TestInvalidIndex(t *testing.T) {
	store := newReassemblyStore()
	fh := FragmentHeader{FragmentID: 30, FragmentIndex: 5, FragmentCount: 3, FragmentFlags: 0}
	_, _, err := store.addFragment(fh, []byte("data"))
	if err != ErrInvalidFragmentHeader {
		t.Fatalf("expected ErrInvalidFragmentHeader for index >= count, got %v", err)
	}
}

func TestZeroCount(t *testing.T) {
	store := newReassemblyStore()
	fh := FragmentHeader{FragmentID: 31, FragmentIndex: 0, FragmentCount: 0, FragmentFlags: 0}
	_, _, err := store.addFragment(fh, []byte("data"))
	if err != ErrInvalidFragmentHeader {
		t.Fatalf("expected ErrInvalidFragmentHeader for count=0, got %v", err)
	}
}

// --- Benchmarks ---

func BenchmarkFragmentSplit(b *testing.B) {
	payload := make([]byte, 4000)
	for i := range payload {
		payload[i] = byte(i)
	}
	for b.Loop() {
		_, _ = splitMessage(payload, 1, 0)
	}
}

func BenchmarkFragmentReassemble(b *testing.B) {
	payload := make([]byte, 4000)
	for i := range payload {
		payload[i] = byte(i)
	}
	frags, err := splitMessage(payload, 1, 0)
	if err != nil {
		b.Fatal(err)
	}
	// Parse fragment headers once.
	type parsedFrag struct {
		hdr     FragmentHeader
		payload []byte
	}
	parsed := make([]parsedFrag, len(frags))
	for i, frag := range frags {
		fh, n, err := UnmarshalFragmentHeader(frag)
		if err != nil {
			b.Fatal(err)
		}
		parsed[i] = parsedFrag{hdr: fh, payload: frag[n:]}
	}
	b.ResetTimer()
	for b.Loop() {
		store := newReassemblyStore()
		for _, pf := range parsed {
			_, _, _ = store.addFragment(pf.hdr, pf.payload)
		}
	}
}

func TestConnectionHasReassemblyStore(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9999")
	layout := DefaultChannelLayout()
	conn := newConnection(addr, nil, nil, CipherNone, layout, CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})
	if conn.reassembly == nil {
		t.Fatal("newConnection should initialize reassembly store")
	}
	if conn.reassembly.pending() != 0 {
		t.Fatal("new reassembly store should have 0 pending")
	}
}
