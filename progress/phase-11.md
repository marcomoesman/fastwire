# Phase 11 — Send Batching, Connection Migration, I/O Coalescing & Bandwidth Estimation

## What Was Built

### 1. Send Batching
- Packs multiple encrypted packets into a single UDP datagram during tick flush
- Wire format: `[count:1B][len1:2B LE][pkt1][len2:2B LE][pkt2]...`
- Reduces UDP overhead and syscalls for applications sending many small messages per tick
- `SendImmediate()` bypasses the batch buffer but still wraps in batch frame format
- Retransmits, heartbeats, and disconnect packets are individually framed when batching is active
- Configurable via `SendBatching bool` on `ServerConfig` and `ClientConfig`
- Negotiated during handshake — both sides must enable for it to be active

### 2. Connection Migration
- Assigns an 8-byte random `MigrationToken` during handshake
- Client prepends token to every outgoing datagram (client→server only)
- Server maintains a `tokenTable` mapping tokens to connections
- When a packet arrives from an unknown address but with a known token, the server migrates the connection to the new address
- Configurable via `ConnectionMigration bool` on `ServerConfig` and `ClientConfig`
- Negotiated during handshake — both sides must enable

### 3. Multi-packet I/O Coalescing
- Server can spawn multiple concurrent read goroutines (configurable `CoalesceReaders`)
- Dedicated write goroutine batches socket writes through a channel, reducing contention
- Configurable via `CoalesceIO bool` and `CoalesceReaders int` on `ServerConfig`
- Falls back to single goroutine I/O when disabled (default)

### 4. Bandwidth Estimation
- EWMA-based estimator in `internal/bandwidth/` package
- Each connection tracks send and receive bandwidth independently
- Updated once per tick via `tickBandwidth()`
- Exposed in `ConnectionStats` as `SendBandwidth` and `RecvBandwidth` (bytes/sec)
- Smoothing factor α = 0.2 for responsive yet stable estimates

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| Handshake feature negotiation (AND of client & server flags) | Both sides must agree on features; prevents format mismatch |
| Batch frame wraps ALL packets when active (including single-packet sends) | Receiver can always use a single parsing path |
| Migration token on client→server only | Server doesn't migrate; client only has one connection |
| `skipBatch` atomic for SendImmediate | Thread-safe flag to bypass batch buffer without changing sendFunc |
| EWMA for bandwidth (not sliding window) | Simpler, lower memory, smooth estimates |
| Write coalescer via channel (not sendmmsg) | Portable, no x/net dependency, reduces contention |

## Files Created
- `internal/bandwidth/bandwidth.go` — EWMA bandwidth estimator
- `internal/bandwidth/bandwidth_test.go` — Tests
- `batch.go` — Batched datagram marshal/unmarshal, flushBatch
- `batch_test.go` — Tests
- `migrate.go` — MigrationToken type, tokenTable
- `migrate_test.go` — Tests
- `features_test.go` — Integration tests for all new features

## Files Modified
- `config.go` — Added FeatureFlag, SendBatching, ConnectionMigration, CoalesceIO, CoalesceReaders
- `errors.go` — Added ErrInvalidBatchFrame
- `stats.go` — Added SendBandwidth, RecvBandwidth fields
- `connection.go` — Added batch buffer, migration token, bandwidth estimators, sendFramed()
- `handshake.go` — Features byte in CONNECT/CHALLENGE, migration token in CHALLENGE
- `server.go` — Batch receive, migration lookup, write coalescer, bandwidth ticking
- `client.go` — Batch send, migration token prefix, bandwidth ticking

## Test Requirements

- [x] Send batching: round-trip, bidirectional, SendImmediate, large message, one-side-disabled
- [x] Connection migration: negotiation, message round-trip, disabled when not negotiated
- [x] Combined batching + migration: works together
- [x] I/O coalescing: round-trip with multiple readers
- [x] Bandwidth estimation: non-zero after traffic
- [x] Feature flags: config to features, handshake negotiation
- [x] Stress test: all features combined with 5 clients
- [x] All existing tests pass (backward compatible)
- [x] Race detector clean
