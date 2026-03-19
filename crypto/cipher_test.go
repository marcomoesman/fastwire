package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"testing"
)

func makeTestCipherStates(t *testing.T, suite CipherSuite) (send, recv *CipherState) {
	t.Helper()
	keySize := keySizeForSuite(suite)
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	send, err := NewCipherState(key, suite)
	if err != nil {
		t.Fatal(err)
	}
	recv, err = NewCipherState(key, suite)
	if err != nil {
		t.Fatal(err)
	}
	return send, recv
}

func TestEncryptDecryptAES128GCM(t *testing.T) {
	send, recv := makeTestCipherStates(t, CipherAES128GCM)
	plaintext := []byte("hello fastwire")
	dst := make([]byte, 0, NonceSize+len(plaintext)+TagSize)

	encrypted, err := Encrypt(send, plaintext, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(encrypted) != NonceSize+len(plaintext)+TagSize {
		t.Fatalf("encrypted length = %d, want %d", len(encrypted), NonceSize+len(plaintext)+TagSize)
	}

	decrypted, err := Decrypt(recv, encrypted, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDecryptChaCha20Poly1305(t *testing.T) {
	send, recv := makeTestCipherStates(t, CipherChaCha20Poly1305)
	plaintext := []byte("hello fastwire chacha")
	dst := make([]byte, 0, NonceSize+len(plaintext)+TagSize)

	encrypted, err := Encrypt(send, plaintext, dst)
	if err != nil {
		t.Fatal(err)
	}

	decrypted, err := Decrypt(recv, encrypted, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDecryptCipherNone(t *testing.T) {
	send, err := NewCipherState(nil, CipherNone)
	if err != nil {
		t.Fatal(err)
	}
	recv, err := NewCipherState(nil, CipherNone)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("unencrypted data")
	encrypted, err := Encrypt(send, plaintext, nil)
	if err != nil {
		t.Fatal(err)
	}
	// CipherNone: no overhead.
	if !bytes.Equal(encrypted, plaintext) {
		t.Fatalf("CipherNone encrypt should pass through, got %x", encrypted)
	}

	decrypted, err := Decrypt(recv, encrypted, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("CipherNone decrypt should pass through, got %q", decrypted)
	}
}

func TestEncryptDecryptVariousSizes(t *testing.T) {
	sizes := []int{0, 1, 100, 1000, 1200 - WireOverhead}
	suites := []struct {
		name  string
		suite CipherSuite
	}{
		{"AES128GCM", CipherAES128GCM},
		{"ChaCha20Poly1305", CipherChaCha20Poly1305},
		{"None", CipherNone},
	}

	for _, s := range suites {
		for _, size := range sizes {
			t.Run(s.name+"/"+itoa(size), func(t *testing.T) {
				send, recv := makeTestCipherStatesForSuite(t, s.suite)
				plaintext := make([]byte, size)
				if size > 0 {
					if _, err := rand.Read(plaintext); err != nil {
						t.Fatal(err)
					}
				}

				encrypted, err := Encrypt(send, plaintext, nil)
				if err != nil {
					t.Fatal(err)
				}

				decrypted, err := Decrypt(recv, encrypted, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(decrypted, plaintext) {
					t.Fatalf("round-trip failed for size %d", size)
				}
			})
		}
	}
}

func makeTestCipherStatesForSuite(t *testing.T, suite CipherSuite) (send, recv *CipherState) {
	t.Helper()
	if suite == CipherNone {
		s, _ := NewCipherState(nil, CipherNone)
		r, _ := NewCipherState(nil, CipherNone)
		return s, r
	}
	return makeTestCipherStates(t, suite)
}

// itoa is a simple int-to-string for test names.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

func TestEncryptNonceIncrementing(t *testing.T) {
	send, _ := makeTestCipherStates(t, CipherAES128GCM)
	plaintext := []byte("test")

	for i := uint64(1); i <= 10; i++ {
		encrypted, err := Encrypt(send, plaintext, nil)
		if err != nil {
			t.Fatal(err)
		}
		nonce := binary.LittleEndian.Uint64(encrypted[:NonceSize])
		if nonce != i {
			t.Fatalf("nonce = %d, want %d", nonce, i)
		}
	}
}

// --- Replay window tests ---

func TestReplayWindowAcceptsNew(t *testing.T) {
	var rw replayWindow
	if !rw.check(1) {
		t.Fatal("fresh window should accept nonce 1")
	}
}

func TestReplayWindowRejectsDuplicate(t *testing.T) {
	send, recv := makeTestCipherStates(t, CipherAES128GCM)
	plaintext := []byte("test")

	encrypted, _ := Encrypt(send, plaintext, nil)

	// First decrypt succeeds.
	_, err := Decrypt(recv, encrypted, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Second decrypt with same packet is rejected.
	_, err = Decrypt(recv, encrypted, nil)
	if !errors.Is(err, ErrReplayedPacket) {
		t.Fatalf("expected ErrReplayedPacket, got %v", err)
	}
}

func TestReplayWindowAcceptsOutOfOrder(t *testing.T) {
	send, recv := makeTestCipherStates(t, CipherAES128GCM)
	plaintext := []byte("test")

	// Encrypt 3 packets.
	enc1, _ := Encrypt(send, plaintext, nil)
	_, _ = Encrypt(send, plaintext, nil) // enc2 -- skip decryption
	enc3, _ := Encrypt(send, plaintext, nil)

	// Decrypt 1, then 3, then 2 should all work.
	if _, err := Decrypt(recv, enc1, nil); err != nil {
		t.Fatalf("nonce 1: %v", err)
	}
	if _, err := Decrypt(recv, enc3, nil); err != nil {
		t.Fatalf("nonce 3: %v", err)
	}
	// Nonce 2 is within window but not yet seen -- re-encrypt it to get a fresh packet for nonce 2.
	// Actually we need the original encrypted packet for nonce 2.
	// Let's redo this test properly.

	send2, recv2 := makeTestCipherStates(t, CipherAES128GCM)
	packets := make([][]byte, 4)
	for i := 1; i <= 3; i++ {
		packets[i], _ = Encrypt(send2, plaintext, nil)
	}

	// Decrypt out of order: 1, 3, 2
	if _, err := Decrypt(recv2, packets[1], nil); err != nil {
		t.Fatalf("nonce 1: %v", err)
	}
	if _, err := Decrypt(recv2, packets[3], nil); err != nil {
		t.Fatalf("nonce 3: %v", err)
	}
	if _, err := Decrypt(recv2, packets[2], nil); err != nil {
		t.Fatalf("nonce 2: %v", err)
	}
}

func TestReplayWindowRejectsTooOld(t *testing.T) {
	var rw replayWindow
	// Advance window to nonce 1025.
	for i := uint64(1); i <= 1025; i++ {
		rw.update(i)
	}
	// Nonce 1 is now too old (1025 - 1 = 1024, window is 0..1023).
	if rw.check(1) {
		t.Fatal("nonce 1 should be rejected as too old")
	}
}

func TestReplayWindowEdgeCases(t *testing.T) {
	var rw replayWindow
	// Set maxNonce to 1024.
	rw.update(1024)

	// maxNonce - 1023 = 1 -> should accept.
	if !rw.check(1) {
		t.Fatal("nonce at exact boundary (maxNonce-1023) should be accepted")
	}

	// Advance to 1025.
	rw.update(1025)
	// maxNonce - 1024 = 1 -> should reject (off by one).
	if rw.check(1) {
		t.Fatal("nonce at maxNonce-1024 should be rejected")
	}

	// maxNonce - 1023 = 2 -> should accept.
	if !rw.check(2) {
		t.Fatal("nonce at exact boundary (maxNonce-1023=2) should be accepted")
	}
}

func TestReplayWindowLargeJump(t *testing.T) {
	var rw replayWindow
	rw.update(1)
	rw.update(2)
	rw.update(3)

	// Jump by more than 1024.
	rw.update(2000)

	// Old nonces should be rejected.
	if rw.check(1) {
		t.Fatal("nonce 1 should be rejected after large jump")
	}
	if rw.check(3) {
		t.Fatal("nonce 3 should be rejected after large jump")
	}

	// New nonces should be accepted.
	if !rw.check(2001) {
		t.Fatal("nonce 2001 should be accepted")
	}
	// Nonce within window range of new max should also work.
	if !rw.check(1999) {
		t.Fatal("nonce 1999 should be accepted (within window of 2000)")
	}
}

// --- Failure cases ---

func TestDecryptTruncatedPacket(t *testing.T) {
	_, recv := makeTestCipherStates(t, CipherAES128GCM)
	short := make([]byte, NonceSize+TagSize-1) // 23 bytes, need at least 24
	_, err := Decrypt(recv, short, nil)
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("expected ErrDecryptionFailed, got %v", err)
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	send, recv := makeTestCipherStates(t, CipherAES128GCM)
	plaintext := []byte("secret data")

	encrypted, _ := Encrypt(send, plaintext, nil)

	// Flip a byte in the ciphertext.
	encrypted[NonceSize+2] ^= 0xFF

	_, err := Decrypt(recv, encrypted, nil)
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("expected ErrDecryptionFailed, got %v", err)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	send, _ := makeTestCipherStates(t, CipherAES128GCM)
	_, recv := makeTestCipherStates(t, CipherAES128GCM) // different key

	plaintext := []byte("secret data")
	encrypted, _ := Encrypt(send, plaintext, nil)

	_, err := Decrypt(recv, encrypted, nil)
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("expected ErrDecryptionFailed, got %v", err)
	}
}

// --- Benchmarks ---

func BenchmarkEncryptAES(b *testing.B) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	cs, err := NewCipherState(key, CipherAES128GCM)
	if err != nil {
		b.Fatal(err)
	}
	payload := make([]byte, 100)
	if _, err := rand.Read(payload); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(100)
	for b.Loop() {
		_, _ = Encrypt(cs, payload, nil)
	}
}

func BenchmarkEncryptChaCha(b *testing.B) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	cs, err := NewCipherState(key, CipherChaCha20Poly1305)
	if err != nil {
		b.Fatal(err)
	}
	payload := make([]byte, 100)
	if _, err := rand.Read(payload); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(100)
	for b.Loop() {
		_, _ = Encrypt(cs, payload, nil)
	}
}

func BenchmarkDecryptAES(b *testing.B) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	sendCS, err := NewCipherState(key, CipherAES128GCM)
	if err != nil {
		b.Fatal(err)
	}
	payload := make([]byte, 100)
	if _, err := rand.Read(payload); err != nil {
		b.Fatal(err)
	}
	// Pre-encrypt b.N packets with monotonic nonces.
	packets := make([][]byte, b.N)
	for i := range b.N {
		enc, err := Encrypt(sendCS, payload, nil)
		if err != nil {
			b.Fatal(err)
		}
		packets[i] = enc
	}
	recvCS, err := NewCipherState(key, CipherAES128GCM)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(100)
	b.ResetTimer()
	for i := range b.N {
		_, _ = Decrypt(recvCS, packets[i], nil)
	}
}

func BenchmarkDecryptChaCha(b *testing.B) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	sendCS, err := NewCipherState(key, CipherChaCha20Poly1305)
	if err != nil {
		b.Fatal(err)
	}
	payload := make([]byte, 100)
	if _, err := rand.Read(payload); err != nil {
		b.Fatal(err)
	}
	packets := make([][]byte, b.N)
	for i := range b.N {
		enc, err := Encrypt(sendCS, payload, nil)
		if err != nil {
			b.Fatal(err)
		}
		packets[i] = enc
	}
	recvCS, err := NewCipherState(key, CipherChaCha20Poly1305)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(100)
	b.ResetTimer()
	for i := range b.N {
		_, _ = Decrypt(recvCS, packets[i], nil)
	}
}

// --- Fuzz tests ---

func FuzzEncryptDecryptAES(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte(""))
	f.Add(make([]byte, 1000))

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		f.Fatal(err)
	}
	send, _ := NewCipherState(key, CipherAES128GCM)
	recv, _ := NewCipherState(key, CipherAES128GCM)

	f.Fuzz(func(t *testing.T, plaintext []byte) {
		encrypted, err := Encrypt(send, plaintext, nil)
		if err != nil {
			t.Fatal(err)
		}
		decrypted, err := Decrypt(recv, encrypted, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decrypted, plaintext) {
			t.Fatal("round-trip mismatch")
		}
	})
}

func FuzzEncryptDecryptChaCha(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte(""))
	f.Add(make([]byte, 1000))

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		f.Fatal(err)
	}
	send, _ := NewCipherState(key, CipherChaCha20Poly1305)
	recv, _ := NewCipherState(key, CipherChaCha20Poly1305)

	f.Fuzz(func(t *testing.T, plaintext []byte) {
		encrypted, err := Encrypt(send, plaintext, nil)
		if err != nil {
			t.Fatal(err)
		}
		decrypted, err := Decrypt(recv, encrypted, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decrypted, plaintext) {
			t.Fatal("round-trip mismatch")
		}
	})
}

func FuzzDecryptRandomData(f *testing.F) {
	f.Add([]byte("random garbage"))
	f.Add(make([]byte, 24))
	f.Add(make([]byte, 0))

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		f.Fatal(err)
	}
	recv, _ := NewCipherState(key, CipherAES128GCM)

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic regardless of input.
		_, _ = Decrypt(recv, data, nil)
	})
}
