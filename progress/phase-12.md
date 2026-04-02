# Phase 12 — Performance Optimization

## What Was Done

Applied 9 performance optimizations targeting hot per-packet and per-tick paths:

1. **Inline FNV-1a hash** (`server.go`) — replaced `fnv.New32a()` allocation with inline arithmetic in `connectionTable.shardFor`. Eliminates 1 allocation per received packet on the server.

2. **Bitfield ack processing** (`channel.go`) — replaced `map[uint32]bool` allocation in `processAcks` with inline `isAcked()` helper using bitfield arithmetic. Eliminates 1 allocation per ack processing call.

3. **Pooled heartbeat buffer** (`connection.go`) — replaced `make([]byte, 64)` with `getSendBuffer(64)` in `sendHeartbeatOnChannel` and `Close()`. Returns to pool after Encrypt consumes the plaintext.

4. **Removed redundant batch copy** (`connection.go`) — `writeEncrypted` no longer copies the encrypted slice when batching is enabled, since `Encrypt()` always returns a fresh slice.

5. **Atomic timestamps** (`connection.go`) — replaced mutex-guarded `lastSendTime`/`lastRecvTime` with `atomic.Int64` fields (`lastSendNano`/`lastRecvNano`). `touchSend`/`touchRecv` are now lock-free.

6. **Single time.Now() per tick** (`server.go`, `client.go`, `connection.go`) — computed `now := time.Now()` once at tick start and threaded through `tickConnection`, `sendMessage`, `sendSinglePacket`, `sendFragmented`, `checkRetransmissions`, `needsHeartbeatAt`, `isTimedOutAt`. Eliminates 5-10+ redundant `time.Now()` calls per tick per connection.

7. **LossTracker O(1) ack lookup** (`internal/stats/loss.go`) — added `index map[uint32]int` to the ring buffer for O(1) `RecordAck` instead of O(100) linear scan.

8. **Pooled flushBatch buffers** (`batch.go`) — replaced `make([]byte, ...)` with `getSendBuffer`/`putSendBuffer` for datagram buffers in `flushBatch`.

9. **Pooled Encrypt output for retransmissions** (`server.go`, `client.go`, `connection.go`) — provided pre-allocated `dst` buffers from the pool to `Encrypt` on immediate-send paths (retransmissions, heartbeats). Returned to pool after `sendFramed`.

## Design Decisions

- **Fix #3 (pool send queue buffers) was evaluated and rejected**: `getSendBuffer` returns 1200-byte pool buffers even for small messages, causing B/op regression in benchmarks due to `sync.Pool` GC behavior. The original `make([]byte, len(data))` is more appropriate since it allocates exactly the needed size.

- **Encrypt output pooling limited to immediate-send paths**: On the batch path, encrypted buffers are held in `batchBuf` until `flushBatch`. Pooling these would require complex lifetime tracking. Only retransmissions, heartbeats, and disconnect packets use pooled Encrypt output.

- **`needsHeartbeatAt`/`isTimedOutAt` variants added alongside originals**: The `At`-suffixed versions accept a `time.Time` parameter for tick-path use. The original methods remain for non-tick callers (tests, public API).

## Benchmark Results

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| FullSendPath | 4295 ns/op, 6 allocs | 4096 ns/op, 6 allocs | -4.6% latency, -3.4% B/op |
| ServerThroughput | 4358 ns/op, 15 allocs | 2795 ns/op, 15 allocs | **-35.9% latency** |
| FragmentSplit | 588 ns/op | 569 ns/op | -3.2% |
| FragmentReassemble | 1138 ns/op | 1082 ns/op | -4.9% |

## Test Requirements

- [x] All existing tests pass with `-race` flag
- [x] `TestShardForMatchesFNV` verifies inline FNV matches stdlib
- [x] `TestIsAcked` verifies bitfield helper with edge cases
- [x] All 7 LossTracker tests pass with index map
- [x] All heartbeat/disconnect tests pass with pooled buffers
- [x] All batch tests pass with pooled datagram buffers
- [x] All server/client integration tests pass with atomic timestamps and `now` threading
