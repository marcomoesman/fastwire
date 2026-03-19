package crypto

import "errors"

// CipherSuite identifies an AEAD cipher suite.
type CipherSuite byte

const (
	// CipherNone disables encryption. Packets are sent as plaintext.
	CipherNone CipherSuite = 0
	// CipherAES128GCM uses AES-128-GCM (16-byte key).
	CipherAES128GCM CipherSuite = 1
	// CipherChaCha20Poly1305 uses ChaCha20-Poly1305 (32-byte key).
	CipherChaCha20Poly1305 CipherSuite = 2
)

var (
	// ErrReplayedPacket is returned when a nonce has already been seen or is too old.
	ErrReplayedPacket = errors.New("fastwire: replayed packet")

	// ErrDecryptionFailed is returned when AEAD decryption fails.
	ErrDecryptionFailed = errors.New("fastwire: decryption failed")
)
