# Phase 4 — Channel Layer & Reliability

## What was built

### New files
- `reliability.go` — RTT/RTO state using Jacobson/Karels algorithm with thread-safe accessors
- `reliability_test.go` — first sample, convergence, jitter, min/max clamp, initial value, concurrency
- `channel.go` — channel struct with four delivery modes, ack tracking, retransmission queue
- `channel_test.go` — ack advance/out-of-order/duplicate/too-old/bitfield/large-gap, all delivery modes, process acks with RTT, Karn's algorithm, retransmission scheduling, max retries kill, layout builder

### Modified files
- `errors.go` — added `ErrInvalidChannelLayout`, `ErrInvalidChannel`, `ErrMaxRetransmits`
- `config.go` — added `DefaultChannelLayout()`, `ChannelLayoutBuilder`, `ChannelLayout.Len()`
- `connection.go` — added `channels`, `rtt`, `layout` fields; `newConnection` accepts `ChannelLayout`; added `channel(id)` and `inFlightCount()` methods
- `handshake.go` — added `layout` to `pendingHandshake`; `serverProcessConnect` accepts `ChannelLayout`; `serverProcessResponse` passes layout to `newConnection`
- `connection_test.go` — updated all `newConnection` calls to pass `DefaultChannelLayout()`
- `handshake_test.go` — updated all `serverProcessConnect` calls to pass `DefaultChannelLayout()`

## Design decisions

- **Sequences start at 1** — matches nonce pattern in encrypt.go. `recvAck=0` unambiguously means "nothing received." Eliminates need for boolean sentinel flags.
- **Per-channel mutex** — each channel has its own `sync.Mutex` protecting mutable state, avoiding contention between channels.
- **Pre-encryption retransmit storage** — pendingPacket stores raw pre-encryption bytes. Retransmitted packets are re-encrypted with fresh nonces (preventing nonce reuse).
- **Karn's algorithm** — RTT is only measured from first-transmit packets, not retransmissions, to avoid ambiguous RTT samples.
- **32-bit ack bitfield** — covers 32 packets behind recvAck. Gaps larger than 32 clear the bitfield.

## Test requirements

- [x] Ack advance — new highest seq updates recvAck correctly
- [x] Ack out-of-order — packets behind recvAck set correct bits
- [x] Ack duplicate — returns false for duplicate packets
- [x] Ack too old — rejects packets more than 32 behind recvAck
- [x] Ack bitfield boundary — seq at exactly recvAck-32 is accepted
- [x] Ack large gap — gaps > 32 clear bitfield
- [x] Ack seq 0 rejected
- [x] Reliable ordered reorder and drain — delivers in sequence despite out-of-order arrival
- [x] Reliable unordered immediate delivery
- [x] Unreliable immediate delivery
- [x] Unreliable sequenced drops stale packets
- [x] Process acks with RTT measurement
- [x] Karn's algorithm — no RTT from retransmissions
- [x] Process acks with ack=0 — no-op
- [x] Retransmission scheduling — due packets returned
- [x] Max retries exceeded — kill flag returned
- [x] Retransmission not yet due — nothing returned
- [x] Next sequence starts at 1
- [x] Default channel layout — 4 channels, correct modes
- [x] Layout builder — build, empty error, too-many error, max 256 ok
- [x] newChannels — correct setup including recvBuffer for ReliableOrdered
- [x] RTT first sample
- [x] RTT convergence (100x 50ms)
- [x] RTT jitter handling
- [x] RTO min clamp (50ms)
- [x] RTO max clamp (5s)
- [x] RTT initial value (1s RTO)
- [x] RTT concurrency safety
- [x] All existing Phase 1-3 tests still pass
- [x] Race detector clean
- [x] Fuzz tests still pass
