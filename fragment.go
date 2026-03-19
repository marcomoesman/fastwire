package fastwire

import (
	"encoding/binary"
	"sync"
	"time"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
)

// FragmentFlag represents flags in a fragment header.
type FragmentFlag byte

const (
	// FragFlagCompressed indicates the fragment payload is compressed.
	FragFlagCompressed FragmentFlag = 1 << 0
	// FragFlagZstd indicates zstd compression (when Compressed is set). 0 = LZ4.
	FragFlagZstd FragmentFlag = 1 << 1
)

// FragmentHeader describes a fragment within a fragmented message.
type FragmentHeader struct {
	FragmentID    uint16
	FragmentIndex byte
	FragmentCount byte
	FragmentFlags FragmentFlag
}

const (
	// FragmentHeaderSize is the fixed size of a serialized fragment header.
	FragmentHeaderSize = 5

	// MaxHeaderSize is the maximum size of a serialized packet header.
	// 1 (flags) + 1 (channel) + 5 (sequence VarInt) + 5 (ack VarInt) + 4 (ackField) = 16.
	MaxHeaderSize = 16

	// MaxFragments is the maximum number of fragments a single message can be split into.
	MaxFragments = 255

	// MaxFragmentPayload is the maximum payload per fragment.
	// DefaultMTU - WireOverhead - MaxHeaderSize - FragmentHeaderSize = 1200 - 24 - 16 - 5 = 1155.
	MaxFragmentPayload = DefaultMTU - fwcrypto.WireOverhead - MaxHeaderSize - FragmentHeaderSize

	// DefaultFragmentTimeout is the default time after which an incomplete reassembly buffer is discarded.
	DefaultFragmentTimeout = 5 * time.Second
)

// MarshalFragmentHeader encodes h into buf as a fixed 5-byte sequence:
// ID (2B big-endian) + Index (1B) + Count (1B) + Flags (1B).
// Returns the number of bytes written and an error if buf is too small.
func MarshalFragmentHeader(buf []byte, h *FragmentHeader) (int, error) {
	if len(buf) < FragmentHeaderSize {
		return 0, ErrInvalidFragmentHeader
	}
	binary.BigEndian.PutUint16(buf, h.FragmentID)
	buf[2] = h.FragmentIndex
	buf[3] = h.FragmentCount
	buf[4] = byte(h.FragmentFlags)
	return FragmentHeaderSize, nil
}

// UnmarshalFragmentHeader parses a FragmentHeader from buf.
// Returns the parsed header, the number of bytes consumed (5), and an error.
func UnmarshalFragmentHeader(buf []byte) (FragmentHeader, int, error) {
	if len(buf) < FragmentHeaderSize {
		return FragmentHeader{}, 0, ErrInvalidFragmentHeader
	}
	h := FragmentHeader{
		FragmentID:    binary.BigEndian.Uint16(buf),
		FragmentIndex: buf[2],
		FragmentCount: buf[3],
		FragmentFlags: FragmentFlag(buf[4]),
	}
	return h, FragmentHeaderSize, nil
}

// splitMessage splits payload into fragments, each prefixed with a serialized FragmentHeader.
// Returns a slice of independently allocated buffers (FragmentHeader + chunk).
// Empty payload produces a single fragment with count=1 and zero-length chunk.
func splitMessage(payload []byte, fragmentID uint16, flags FragmentFlag) ([][]byte, error) {
	fragmentCount := 1
	if len(payload) > MaxFragmentPayload {
		fragmentCount = (len(payload) + MaxFragmentPayload - 1) / MaxFragmentPayload
	}
	if fragmentCount > MaxFragments {
		return nil, ErrMessageTooLarge
	}

	fragments := make([][]byte, fragmentCount)
	for i := range fragmentCount {
		start := i * MaxFragmentPayload
		end := start + MaxFragmentPayload
		if end > len(payload) {
			end = len(payload)
		}
		chunk := payload[start:end]

		buf := make([]byte, FragmentHeaderSize+len(chunk))
		h := FragmentHeader{
			FragmentID:    fragmentID,
			FragmentIndex: byte(i),
			FragmentCount: byte(fragmentCount),
			FragmentFlags: flags,
		}
		if _, err := MarshalFragmentHeader(buf, &h); err != nil {
			return nil, err
		}
		copy(buf[FragmentHeaderSize:], chunk)
		fragments[i] = buf
	}
	return fragments, nil
}

// reassemblyBuffer holds fragments for a single message being reassembled.
type reassemblyBuffer struct {
	fragments [][]byte // indexed by FragmentIndex; nil = not yet received
	received  int
	count     int
	flags     FragmentFlag
	createdAt time.Time
}

// reassemblyStore manages in-progress fragment reassembly keyed by fragment ID.
type reassemblyStore struct {
	mu      sync.Mutex
	buffers map[uint16]*reassemblyBuffer
}

func newReassemblyStore() *reassemblyStore {
	return &reassemblyStore{
		buffers: make(map[uint16]*reassemblyBuffer),
	}
}

// addFragment processes an incoming fragment. If it completes a message, the
// assembled payload is returned with complete=true. Duplicate fragments are
// silently skipped (nil, false, nil).
func (s *reassemblyStore) addFragment(fh FragmentHeader, payload []byte) (assembled []byte, complete bool, err error) {
	if fh.FragmentCount == 0 {
		return nil, false, ErrInvalidFragmentHeader
	}
	if fh.FragmentIndex >= fh.FragmentCount {
		return nil, false, ErrInvalidFragmentHeader
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	buf, exists := s.buffers[fh.FragmentID]
	if exists {
		// Validate consistency with existing buffer.
		if buf.count != int(fh.FragmentCount) || buf.flags != fh.FragmentFlags {
			return nil, false, ErrInvalidFragmentHeader
		}
	} else {
		buf = &reassemblyBuffer{
			fragments: make([][]byte, fh.FragmentCount),
			count:     int(fh.FragmentCount),
			flags:     fh.FragmentFlags,
			createdAt: time.Now(),
		}
		s.buffers[fh.FragmentID] = buf
	}

	// Duplicate fragment — silent skip.
	if buf.fragments[fh.FragmentIndex] != nil {
		return nil, false, nil
	}

	// Copy payload so caller can reuse their buffer.
	data := make([]byte, len(payload))
	copy(data, payload)
	buf.fragments[fh.FragmentIndex] = data
	buf.received++

	if buf.received < buf.count {
		return nil, false, nil
	}

	// All fragments received — concatenate in order.
	total := 0
	for _, f := range buf.fragments {
		total += len(f)
	}
	assembled = make([]byte, 0, total)
	for _, f := range buf.fragments {
		assembled = append(assembled, f...)
	}
	delete(s.buffers, fh.FragmentID)
	return assembled, true, nil
}

// cleanup removes reassembly buffers older than timeout. Returns count removed.
func (s *reassemblyStore) cleanup(timeout time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	now := time.Now()
	for id, buf := range s.buffers {
		if now.Sub(buf.createdAt) >= timeout {
			delete(s.buffers, id)
			removed++
		}
	}
	return removed
}

// pending returns the number of incomplete reassembly buffers.
func (s *reassemblyStore) pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buffers)
}
