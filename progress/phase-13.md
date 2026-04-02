# Phase 13 — Performance Optimization Pass 2

## What Was Done

Six targeted hot-path optimizations based on a full codebase audit.

### 1. Encrypt Nonce: Eliminate Double Serialization
- **File:** `crypto/cipher.go`
- Nonce was serialized twice via `binary.LittleEndian.PutUint64` (once into AEAD nonce array, once into output buffer). Reordered to write to `dst` first, then `copy` 8 bytes into the AEAD nonce.

### 2. Decrypt Hot Path: Pooled Output Buffer
- **Files:** `pool.go`, `server.go`, `client.go`, `channel.go`
- Added `decryptPool` (sync.Pool) for AEAD decrypt scratch buffers. Server and client `processPacket` now pass a pooled buffer to `Decrypt` instead of `nil`, eliminating per-packet heap allocation.
- Added safety copy in `deliverReliableOrdered` for out-of-order packets that remain in the reorder buffer after the pool buffer is returned.

### 3. Loss Tracker: Eliminate Map, Lock-Free Loss()
- **File:** `internal/stats/loss.go`
- Removed `index map[uint32]int` — replaced with linear ring buffer scan (100 entries, cache-friendly).
- Made `count`/`ackCount` atomic, so `Loss()` is fully lock-free (0.49 ns/op).
- `RecordSend` now has 0 allocs/op (no map insertion).

### 4. Fragment Split: Single Contiguous Allocation
- **File:** `fragment.go`
- Replaced N per-fragment `make([]byte)` calls with a single contiguous `backing` buffer, sliced into fragments. Reduced allocations from N+1 to 2.

### 5. Fragment Reassembly: Single-Pass Copy
- **File:** `fragment.go`
- Added `totalBytes` field to `reassemblyBuffer`, incremented as fragments arrive. On completion, allocate exact-sized buffer and use single-pass `copy` instead of double-iteration `append`.

### 6. Heartbeat Coalescing: Multi-Channel ACK
- **Files:** `packet.go`, `handshake.go`, `connection.go`, `server.go`, `client.go`
- Added `ControlMultiAck` (0x09) control type with marshal/unmarshal.
- New `sendMultiChannelHeartbeat` method packs ACK state from all pending channels into one encrypted packet.
- Tick loop collects all channels needing ACK, sends a single coalesced packet instead of one per channel.
- Receive side parses multi-ack entries and processes acks for each channel independently.

## Benchmark Results

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| FullRecvPath B/op | 528 | 40 | **-92%** |
| FullRecvPath ns/op | ~196 | ~163 | **-17%** |
| FragmentSplit ns/op | ~722 | ~475 | **-34%** |
| FragmentSplit allocs | 5 | 2 | **-60%** |
| FragmentReassemble ns/op | ~1,189 | ~1,065 | **-10%** |
| Loss() ns/op | (mutex) | 0.49 | **lock-free** |

## Test Requirements

- [x] All existing tests pass (`go test ./...`)
- [x] Multi-ack marshal/unmarshal round-trip tests
- [x] Multi-ack empty entries test
- [x] Multi-ack buffer-too-small test
- [x] Loss tracker benchmarks (RecordSend, RecordAck, Loss)
- [x] Stress test with all features combined passes
- [x] Fragment split/reassembly round-trip tests
- [x] Reliable ordered out-of-order delivery with pooled buffers
