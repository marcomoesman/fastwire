# Phase 9 — API Surface, Callbacks & Polish

## What was built

### Connection Stats (`stats.go`)
- `ConnectionStats` struct with RTT, RTTVariance, PacketLoss, BytesSent, BytesReceived, CongestionWindow, Uptime
- `lossTracker` — ring buffer of last 100 reliable packets for loss measurement
  - `recordSend(seq)` writes to head, adjusts ackCount when overwriting acked entries
  - `recordAck(seq)` linear scans and flips acked flag
  - `loss()` returns `1 - ackCount/count`
- `Connection.Stats()` method returns a snapshot

### Congestion Window API (`congestion.go`)
- Added `Window() int` to `CongestionController` interface
- `conservativeCC.Window()` returns `int(c.cwnd)` under lock
- `aggressiveCC.Window()` returns `0` (unlimited)

### Byte Counting
- `bytesSent` and `bytesReceived` as `atomic.Uint64` on Connection
- Server: sendFunc wrapper counts bytes on write; processPacket counts on receive
- Client: same pattern

### Loss Ack Tracking
- Server and client `processPacket`: after `processAcks`, each acked sequence is passed to `conn.loss.recordAck(seq)`
- `sendSinglePacket` and `sendFragmented`: call `c.loss.recordSend(seq)` after `addPending`

### Disconnect Retry
- `Close()` now sets `StateDisconnecting`, builds and stores encrypted disconnect packet, sends first one, returns immediately
- Tick loop handles retry: up to 3 retries with RTO spacing, then force-close
- `maxDisconnectRetries = 3`

### ForEachConnection
- `Server.ForEachConnection(fn func(*Connection))` delegates to `connectionTable.forEach`

### Bug Fix
- Fixed pre-existing data race in `Client.Connect()` — `c.server` read for `OnConnect` was unsynchronized with tick loop writes

## Design decisions

- **PacketLoss**: Ring buffer of 100 entries, not a time window. Simple, deterministic, low overhead.
- **BytesSent/BytesReceived**: Wire bytes (socket level), not application bytes. Counted in sendFunc wrapper and at top of processPacket.
- **Disconnect retry**: Async via tick loop. Close() returns immediately. Client.Close() still does full teardown (calls conn.Close() then forces Disconnected).
- **ForEachConnection**: Iterates all shards with read locks, same as internal forEach.

## Test checklist

- [x] `TestLossTrackerEmpty` — 0.0 loss on empty tracker
- [x] `TestLossTrackerAllAcked` — 0.0 loss when all 10 packets acked
- [x] `TestLossTrackerHalfLost` — 0.5 loss when 5 of 10 acked
- [x] `TestLossTrackerWindowRotation` — only last 100 matter after 150 sends
- [x] `TestLossTrackerWindowRotationPartial` — partial rotation correctness
- [x] `TestLossTrackerDuplicateAck` — duplicate ack is no-op
- [x] `TestLossTrackerConcurrent` — goroutine safety
- [x] `TestConnectionStats` — integration: non-zero BytesSent, BytesReceived, Uptime, CongestionWindow
- [x] `TestConnectionStatsUptime` — uptime increases over time
- [x] `TestConnectionStatsPacketLoss` — loss is reasonable after traffic
- [x] `TestConservativeWindow` — returns cwnd as int, grows after acks
- [x] `TestAggressiveWindow` — returns 0 (unlimited)
- [x] `TestServerForEachConnection` — iterates connected clients
- [x] `TestDisconnectRetry` — disconnect packet retried, OnDisconnect fires
- [x] All existing tests pass
- [x] Race detector clean
