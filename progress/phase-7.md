# Phase 7 — Congestion Control

## What was built

### `congestion.go`
- `CongestionController` interface with five methods: `OnAck`, `OnLoss`, `CanSend`, `OnDuplicateAck`, `HalvesRTO`
- `conservativeCC` — AIMD (Additive Increase Multiplicative Decrease) congestion control:
  - Additive increase: `cwnd += ackedPackets / cwnd` per ack
  - Multiplicative decrease: `cwnd = max(cwnd * 0.5, 2.0)` on loss
  - Window gating: `CanSend` returns `inFlight < int(cwnd)`
  - No fast retransmit, no RTO halving
- `aggressiveCC` — no window gating, fast retransmit:
  - `CanSend` always returns true
  - Fast retransmit triggers after 2 duplicate acks (resets counter)
  - `OnAck` resets duplicate ack counter
  - `HalvesRTO` returns true
- Constructor `newCongestionController(mode, initialCwnd)` — defaults to `DefaultInitialCwnd` (4) when `initialCwnd <= 0`

### Integration
- `Connection` struct gained `congestion CongestionController` field
- `newConnection` extended with `congestionMode CongestionMode` and `initialCwnd int` parameters
- `pendingHandshake` carries `congestionMode` and `initialCwnd` through the handshake
- `serverProcessConnect` extended with congestion parameters, passed through to `serverProcessResponse` → `newConnection`

## Design decisions

| Decision | Rationale |
|----------|-----------|
| `OnDuplicateAck` returns bool | Cleaner than a separate query method — caller can act on the return value directly |
| Threshold = 2 duplicate acks (not 3) | Gaming workloads are latency-sensitive; faster retransmit is preferred |
| `cwnd` is `float64` | Allows sub-packet granularity in AIMD increase; `int(cwnd)` used for gating |
| `minCwnd = 2.0` | Ensures at least 2 packets in flight even under heavy loss |
| `aggressiveCC.OnLoss` is a no-op | Aggressive mode trades reliability for speed — no send rate reduction |
| `sync.Mutex` per controller | Both implementations have mutable state; mutex ensures thread safety |

## Test checklist

- [x] AIMD window growth on ack
- [x] AIMD window shrink on loss
- [x] Window floor enforcement (never below 2.0)
- [x] CanSend boundary checks (conservative)
- [x] CanSend boundary after window growth
- [x] Conservative OnDuplicateAck always returns false
- [x] Conservative HalvesRTO returns false
- [x] Aggressive CanSend always returns true
- [x] Aggressive fast retransmit at 2 duplicate acks
- [x] Aggressive ack resets duplicate counter
- [x] Aggressive OnLoss is a no-op
- [x] Aggressive HalvesRTO returns true
- [x] Constructor with default cwnd (0 → DefaultInitialCwnd)
- [x] Constructor with custom cwnd
- [x] Constructor creates correct type per mode
- [x] Concurrent goroutine stress (conservative)
- [x] Concurrent goroutine stress (aggressive)
- [x] All existing tests pass with updated call sites
- [x] Race detector clean
- [x] Linter clean
