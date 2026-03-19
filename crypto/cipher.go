package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"sync/atomic"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// NonceSize is the number of bytes used for the wire nonce.
	NonceSize = 8
	// TagSize is the AEAD authentication tag size in bytes.
	TagSize = 16
	// WireOverhead is the per-packet encryption overhead (NonceSize + TagSize).
	WireOverhead = NonceSize + TagSize
)

// replayWindow implements a 1024-bit sliding window for replay protection.
type replayWindow struct {
	maxNonce uint64
	window   [16]uint64 // 1024-bit bitfield
}

// check returns true if the nonce is acceptable (not replayed, not too old).
// Pure read — does not mutate state.
func (rw *replayWindow) check(nonce uint64) bool {
	if nonce == 0 {
		return false
	}
	if rw.maxNonce == 0 {
		// No nonces seen yet; accept anything.
		return true
	}
	if nonce > rw.maxNonce {
		return true
	}
	diff := rw.maxNonce - nonce
	if diff >= 1024 {
		return false // too old
	}
	// Check if bit is already set in the window.
	wordIdx := diff / 64
	bitIdx := diff % 64
	return rw.window[wordIdx]&(1<<bitIdx) == 0
}

// update marks a nonce as seen. Call only after successful decryption.
func (rw *replayWindow) update(nonce uint64) {
	if nonce == 0 {
		return
	}
	if rw.maxNonce == 0 {
		// First nonce ever seen.
		rw.maxNonce = nonce
		rw.window[0] = 1 // mark nonce as seen at position 0
		return
	}
	if nonce > rw.maxNonce {
		shift := nonce - rw.maxNonce
		shiftWindowLeft(&rw.window, shift)
		rw.maxNonce = nonce
		// Mark current nonce as seen (bit 0).
		rw.window[0] |= 1
	} else {
		diff := rw.maxNonce - nonce
		if diff < 1024 {
			wordIdx := diff / 64
			bitIdx := diff % 64
			rw.window[wordIdx] |= 1 << bitIdx
		}
	}
}

// shiftWindowLeft shifts the entire 1024-bit window left by shift positions.
func shiftWindowLeft(window *[16]uint64, shift uint64) {
	if shift >= 1024 {
		*window = [16]uint64{}
		return
	}

	wordShift := shift / 64
	bitShift := shift % 64

	if wordShift > 0 {
		// Shift whole words.
		for i := uint64(15); i >= wordShift; i-- {
			window[i] = window[i-wordShift]
		}
		for i := uint64(0); i < wordShift; i++ {
			window[i] = 0
		}
	}

	if bitShift > 0 {
		// Shift bits within words (high index = older bits).
		for i := 15; i > 0; i-- {
			window[i] = (window[i] << bitShift) | (window[i-1] >> (64 - bitShift))
		}
		window[0] <<= bitShift
	}
}

// CipherState holds AEAD state for one direction of a connection.
// Send side uses nonce (atomic counter). Recv side uses replay window.
type CipherState struct {
	aead   cipher.AEAD
	nonce  atomic.Uint64 // send side: monotonic counter
	replay replayWindow  // recv side: sliding window
}

// newAEAD creates a cipher.AEAD for the given key and suite.
// Returns nil for CipherNone (no encryption).
func newAEAD(key []byte, suite CipherSuite) (cipher.AEAD, error) {
	switch suite {
	case CipherNone:
		return nil, nil
	case CipherAES128GCM:
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		return cipher.NewGCM(block)
	case CipherChaCha20Poly1305:
		return chacha20poly1305.New(key)
	default:
		return nil, ErrDecryptionFailed
	}
}

// NewCipherState creates an initialized CipherState.
// For CipherNone, returns a state with nil aead (pass-through mode).
func NewCipherState(key []byte, suite CipherSuite) (*CipherState, error) {
	aead, err := newAEAD(key, suite)
	if err != nil {
		return nil, err
	}
	return &CipherState{aead: aead}, nil
}

// Encrypt encrypts plaintext using the send-side nonce counter.
// If the cipher state has no AEAD (CipherNone), plaintext is copied as-is.
// Otherwise dst must have capacity for NonceSize + len(plaintext) + TagSize.
// Returns the encrypted wire bytes: [nonce 8B][ciphertext][tag 16B].
func Encrypt(state *CipherState, plaintext, dst []byte) ([]byte, error) {
	// CipherNone: pass-through.
	if state.aead == nil {
		if cap(dst) < len(plaintext) {
			dst = make([]byte, len(plaintext))
		} else {
			dst = dst[:len(plaintext)]
		}
		copy(dst, plaintext)
		return dst, nil
	}

	nonce := state.nonce.Add(1)

	// Build 12-byte AEAD nonce: LE uint64 in bytes 0-7, bytes 8-11 zero.
	var aeadNonce [12]byte
	binary.LittleEndian.PutUint64(aeadNonce[:8], nonce)

	// Write 8-byte wire nonce to dst.
	needed := NonceSize + len(plaintext) + TagSize
	if cap(dst) < needed {
		dst = make([]byte, needed)
	} else {
		dst = dst[:needed]
	}
	binary.LittleEndian.PutUint64(dst[:NonceSize], nonce)

	// Seal appends ciphertext+tag to dst[8:8].
	state.aead.Seal(dst[NonceSize:NonceSize], aeadNonce[:], plaintext, nil)

	return dst[:needed], nil
}

// Decrypt decrypts a wire packet using the recv-side replay window.
// If the cipher state has no AEAD (CipherNone), packet is copied as-is.
// Wire format: [nonce 8B][ciphertext][tag 16B].
// dst is used as scratch space for the plaintext output.
func Decrypt(state *CipherState, packet, dst []byte) ([]byte, error) {
	// CipherNone: pass-through.
	if state.aead == nil {
		if cap(dst) < len(packet) {
			dst = make([]byte, len(packet))
		} else {
			dst = dst[:len(packet)]
		}
		copy(dst, packet)
		return dst, nil
	}

	if len(packet) < NonceSize+TagSize {
		return nil, ErrDecryptionFailed
	}

	// Read wire nonce.
	nonce := binary.LittleEndian.Uint64(packet[:NonceSize])

	// Check replay window.
	if !state.replay.check(nonce) {
		return nil, ErrReplayedPacket
	}

	// Build 12-byte AEAD nonce.
	var aeadNonce [12]byte
	binary.LittleEndian.PutUint64(aeadNonce[:8], nonce)

	// Decrypt.
	plaintext, err := state.aead.Open(dst[:0], aeadNonce[:], packet[NonceSize:], nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	// Update replay window only after successful decryption.
	state.replay.update(nonce)

	return plaintext, nil
}
