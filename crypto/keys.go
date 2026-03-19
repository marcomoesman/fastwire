package crypto

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"io"

	"golang.org/x/crypto/hkdf"
)

// KeyPair holds an X25519 key pair for the ECDH key exchange.
type KeyPair struct {
	Private *ecdh.PrivateKey
	Public  *ecdh.PublicKey
}

// GenerateKeyPair generates a new X25519 key pair.
func GenerateKeyPair() (KeyPair, error) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{
		Private: private,
		Public:  private.PublicKey(),
	}, nil
}

// DeriveKeys performs X25519 ECDH and derives c2s and s2c encryption keys
// using HKDF with the challenge token as salt.
//
// The caller maps c2s/s2c to send/recv based on role:
//   - Client: send = c2sKey, recv = s2cKey
//   - Server: send = s2cKey, recv = c2sKey
func DeriveKeys(myPrivate *ecdh.PrivateKey, theirPublic *ecdh.PublicKey,
	challengeToken []byte, suite CipherSuite) (c2sKey, s2cKey []byte, err error) {

	// X25519 shared secret.
	shared, err := myPrivate.ECDH(theirPublic)
	if err != nil {
		return nil, nil, err
	}

	// HKDF-Extract: PRK from shared secret + challenge token as salt.
	prk := hkdf.Extract(sha256.New, shared, challengeToken)

	keySize := keySizeForSuite(suite)

	// HKDF-Expand for c2s key.
	c2sKey = make([]byte, keySize)
	c2sReader := hkdf.Expand(sha256.New, prk, []byte("c2s"))
	if _, err = io.ReadFull(c2sReader, c2sKey); err != nil {
		return nil, nil, err
	}

	// HKDF-Expand for s2c key.
	s2cKey = make([]byte, keySize)
	s2cReader := hkdf.Expand(sha256.New, prk, []byte("s2c"))
	if _, err = io.ReadFull(s2cReader, s2cKey); err != nil {
		return nil, nil, err
	}

	return c2sKey, s2cKey, nil
}

// keySizeForSuite returns the key size in bytes for the given cipher suite.
func keySizeForSuite(suite CipherSuite) int {
	switch suite {
	case CipherChaCha20Poly1305:
		return 32
	default:
		return 16 // AES-128-GCM
	}
}
