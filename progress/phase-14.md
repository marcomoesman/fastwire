# Phase 14 — Security & Stability Hardening

Response to the senior audit in `progress/audit.md`. Five of the six critical findings were addressed in this phase; the sixth (stateless retry cookie) is tracked in `progress/phase-15.md` because it requires a protocol bump.

## What Was Done

### 1. Pool-buffer leak on the send path (audit 1.1)
- **Files:** `connection.go`, `batch.go`
- `writeEncrypted` now owns the encrypted buffer's lifetime. Non-batch writes free the buffer after `sendFramed`; batched writes hand ownership to `batchBuf`, and `flushBatch` frees each entry once its bytes have been copied into the outgoing datagram.
- `sendSinglePacket` / `sendFragmented` no longer attempt to free `encBuf` on `writeEncrypted` error (double-free removed).

### 2. Reliable-ordered receive-window bound (audit 1.5)
- **Files:** `channel.go`, `config.go`, `server.go`, `client.go`
- New config field `MaxReorderWindow` (default `DefaultMaxReorderWindow = 1024`).
- `channel.recordReceive` rejects sequences more than `maxReorderWindow` ahead of `recvNextDeliver` on `ReliableOrdered` channels. The rejection is silent (no ack) so the sender retransmits and the reorder map cannot exceed the window.

### 3. Fragment reassembly caps (audit 1.4)
- **Files:** `fragment.go`, `config.go`, `handshake.go`, `connection.go`
- New config fields `MaxReassemblyBuffers` (default 64) and `MaxReassemblyBytes` (default 4 MiB).
- `reassemblyStore` tracks `totalBytes` incrementally, decrementing on completion, eviction, and reset.
- `addFragment` drops silently when a new fragment ID would exceed the buffer cap or when accepting a fragment would exceed the byte cap. Errors are not surfaced to the handler — this is DoS mitigation.

### 4. Authenticated connection migration (audit 1.2)
- **Files:** `server.go`
- `processPacket` split into `decryptPacket` (decrypt only) + `handleDecrypted` (ack / control / fragment / deliver). The split is a pure refactor for the known-address path.
- New `tryMigration` decrypts the first packet under the existing session key before swapping `conn.remoteAddr`. Silent drop on decryption failure — the legitimate client keeps its binding. Additional packets in a batched datagram flow through the normal `processPacket` path after the swap.
- Regression test `TestConnectionMigrationHijackRejected` confirms that a packet prefixed with a known migration token but carrying garbage ciphertext cannot re-route the connection.

### 5. Handshake flood hardening — interim (audit 1.3, partial)
- **Files:** `server.go`, `config.go`
- `pendingTable` now enforces `MaxPendingHandshakes` (default `4 × MaxConnections`); full-table `put` is rejected silently.
- New `handshakeLimiter` — a per-IP leaky bucket (default 10 hs/s steady, burst 20) applied before any crypto work in `handleHandshake`. The limiter itself is bounded to `handshakeLimiterMaxEntries = 8192`; oldest-touched bucket is evicted on overflow.
- Both rejection paths are silent (returning any response would compound the amplification factor).
- This is the interim mitigation. The permanent fix — stateless retry cookies — is deferred to Phase 15 because it requires a protocol-version bump.

## Design Decisions

- **Silent drops over errors for DoS paths.** Rate-limit hits and cap hits do not call `handler.OnError`; surfacing attacker-driven events would itself become an amplification vector for logs and downstream alerting.
- **Input structs for expanded signatures.** `newConnection`, `serverProcessConnect`, and `newReassemblyStore` all crossed the 2-arg threshold (CLAUDE.md) and were refactored to take `*Input` structs. Call sites in tests were updated in bulk.
- **`maxReorderWindow` only applies to `ReliableOrdered`.** Other modes have no reorder buffer; the guard is mode-gated.
- **Replay window stays authoritative for migration auth.** Using `fwcrypto.Decrypt` means a replayed migration packet is already handled correctly — we do not need a separate auth path.

## Test Requirements

- [x] `go test ./...` passes
- [x] `go test -race ./...` passes
- [x] `go vet ./...` passes
- [x] Reorder-window cap unit test
- [x] Reassembly buffer-count cap test
- [x] Reassembly byte cap test
- [x] Reassembly `totalBytes` accounting test
- [x] Migration hijack rejection integration test
- [x] Pending-table cap unit test
- [x] Handshake limiter burst/refill test
- [x] Handshake limiter eviction test

## Remaining Audit Items

See `progress/audit.md`. High-priority non-critical items for follow-up phases:
- Handler `OnMessage` slice-lifetime contract (audit 3.1)
- Unbounded `sendQueue` backpressure (audit 1.6)
- `MaxConnections` admission race (audit 1.8)
- Server coalesced-write copy (audit 2.3)
