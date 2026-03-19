# Phase 2 — Encryption Layer

## What was built

### `encrypt.go` — AEAD encryption, nonce management, replay window
- **Constants:** `NonceSize` (8), `TagSize` (16), `WireOverhead` (24)
- **`replayWindow`** — 1024-bit sliding bitfield (16 × uint64) for replay protection
  - `check(nonce)` — pure read, returns whether nonce is acceptable
  - `update(nonce)` — marks nonce as seen, only called after successful decrypt
  - `shiftWindowLeft()` — multi-word left shift helper for window advancement
- **`cipherState`** — holds AEAD instance, atomic nonce counter (send), replay window (recv)
- **`newAEAD(key, suite)`** — creates AES-128-GCM, ChaCha20-Poly1305, or nil (CipherNone)
- **`newCipherState(key, suite)`** — initializes a cipher state
- **`encrypt(state, plaintext, dst)`** — atomically increments nonce, seals with 12-byte AEAD nonce (LE uint64 + 4 zero bytes), writes `[nonce 8B][ciphertext][tag 16B]`
- **`decrypt(state, packet, dst)`** — checks replay window before decrypt, updates window only on success

### `crypto.go` — X25519 key exchange, HKDF derivation
- **`KeyPair`** — exported struct with `*ecdh.PrivateKey` and `*ecdh.PublicKey`
- **`GenerateKeyPair()`** — generates X25519 key pair
- **`DeriveKeys(myPrivate, theirPublic, challengeToken, suite)`** — X25519 ECDH → HKDF-Extract (salt=challengeToken) → HKDF-Expand for `c2s` and `s2c` keys
- **`keySizeForSuite(suite)`** — returns 16 (AES) or 32 (ChaCha)

### `errors.go` — 2 new sentinel errors
- `ErrReplayedPacket` — nonce already seen or too old
- `ErrDecryptionFailed` — AEAD open failed

### `config.go` — Updated CipherSuite constants
- Added `CipherNone = 0` for optional encryption
- Shifted `CipherAES128GCM = 1`, `CipherChaCha20Poly1305 = 2`

## Design decisions

- **Single `cipherState` type** for both send/recv — unused field per direction is negligible overhead
- **Nonce starts at 1** — 0 is reserved sentinel for "nothing sent yet"
- **`check()` before decrypt, `update()` after** — prevents replay window poisoning from forged packets
- **c2s/s2c naming** in DeriveKeys — caller maps to send/recv based on role
- **12-byte AEAD nonce** with 8-byte counter in low bytes — works for both AES-GCM and ChaCha20-Poly1305
- **CipherNone support** — encrypt/decrypt pass through plaintext when `aead` is nil, no nonce/tag overhead
- **`golang.org/x/crypto`** dependency added for `chacha20poly1305` and `hkdf`

## Test checklist

- [x] `TestEncryptDecryptAES128GCM` — basic round-trip
- [x] `TestEncryptDecryptChaCha20Poly1305` — basic round-trip
- [x] `TestEncryptDecryptCipherNone` — pass-through mode
- [x] `TestEncryptDecryptVariousSizes` — table-driven: 0, 1, 100, 1000, MTU-WireOverhead bytes, all suites
- [x] `TestEncryptNonceIncrementing` — nonces are 1, 2, 3, ...
- [x] `TestReplayWindowAcceptsNew` — fresh window accepts nonce 1
- [x] `TestReplayWindowRejectsDuplicate` — same nonce twice → ErrReplayedPacket
- [x] `TestReplayWindowAcceptsOutOfOrder` — nonces 1, 3, 2 all accepted
- [x] `TestReplayWindowRejectsTooOld` — advance to 1025, nonce 1 rejected
- [x] `TestReplayWindowEdgeCases` — boundary at maxNonce-1023 (accept) vs maxNonce-1024 (reject)
- [x] `TestReplayWindowLargeJump` — jump > 1024 resets window cleanly
- [x] `TestDecryptTruncatedPacket` — packet < 24 bytes
- [x] `TestDecryptTamperedCiphertext` — flipped byte → ErrDecryptionFailed
- [x] `TestDecryptWrongKey` — key mismatch → ErrDecryptionFailed
- [x] `FuzzEncryptDecryptAES` — 10s, no failures
- [x] `FuzzEncryptDecryptChaCha` — 10s, no failures
- [x] `FuzzDecryptRandomData` — 10s, no panics
- [x] `TestGenerateKeyPair` — keys non-nil, two pairs differ
- [x] `TestDeriveKeysBothSidesSame` — client/server derive identical keys
- [x] `TestDeriveKeysBothSuites` — key sizes correct (16 for AES, 32 for ChaCha)
- [x] `TestDeriveKeysDeterministic` — same inputs → same outputs
- [x] `TestDeriveKeysDifferentTokens` — different tokens → different keys
- [x] `TestDeriveAndEncryptRoundTrip` — full flow: keygen → derive → encrypt → decrypt
