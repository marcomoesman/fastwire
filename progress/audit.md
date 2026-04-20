# FastWire — Senior Audit Report

Scope: full package audit focused on correctness, concurrency safety, security, and hot-path performance. Tests and examples were not audited in depth. Line references use `file:line`.

Severity key:
- **[Crit]** — correctness, security, or long-running stability bug.
- **[High]** — meaningful perf regression or a sharp edge that will bite a production user.
- **[Med]** — cleanup/robustness worth scheduling.
- **[Low]** — style / micro-optim / future-proofing.

---

## 1. Correctness & Security

### 1.1 [Crit] Send-path leaks every encryption buffer to the pool
`connection.go:472-507` (`sendSinglePacket`) and `connection.go:539-574` (`sendFragmented`) take `encBuf` from `getSendBuffer`, encrypt into it, and pass the resulting slice to `writeEncrypted`. **On the success path, `putSendBuffer(encBuf)` is never called.** Compare against `sendHeartbeatOnChannel` (`connection.go:670`) and the retransmit path (`server.go:781`, `client.go:633`), which do release the buffer after `sendFramed`.

Consequence: every outgoing data packet consumes a pool buffer that is never returned. `sync.Pool` will GC these during collection cycles, so this is not a process-leak, but the pool degrades to "allocator with a GC-driven backoff" — defeating the entire pooling effort documented in Phase 12/13. Benchmarks for single-packet send will not show this because a single Get/Put is cheap; long-lived server workloads will see elevated GC pressure.

Fix direction: the batching path in `writeEncrypted`/`flushBatch` must own the buffer's lifetime. Two clean options:
- Have `writeEncrypted` copy into pooled batch storage then immediately `putSendBuffer(encBuf)`; or
- Track `encBuf` alongside each queued batch entry and free it in `flushBatch` once the datagram has been sent.

### 1.2 [Crit] Connection migration is unauthenticated and hijackable
`server.go:460` stores `migrationToken → Connection` in `tokens` after the handshake. In `processIncoming` (`server.go:468-483`), any unknown packet whose first 8 bytes match a known token causes the server to rewrite the connection's `remoteAddr` **before decryption is attempted**. The migration token is transmitted in the cleartext `CHALLENGE` packet (`handshake.go:189-192`), so any passive observer can recover it.

Exploit: an attacker sends a single forged packet with the stolen token from any address; the server now routes all server→client traffic to the attacker. The attacker cannot decrypt, but the legitimate client is silently disconnected — a trivial hijack/DoS primitive.

Fix direction: perform the address swap only after a packet decrypts successfully under the stored `recvCipher` for that token. The `next.md` file already notes this as future work ("Connection migration validation") — recommend promoting to a pre-1.0 blocker.

### 1.3 [Crit] Handshake has no source-address validation — trivially flooded
`server.go:565-593` (`handleHandshake`) accepts any `CONNECT`, generates an X25519 key pair, draws 32 bytes of CSPRNG output, derives HKDF keys, and allocates a `pendingHandshake` entry — all without proving the remote is reachable at its claimed source address. UDP source spoofing is universally available, and the CHALLENGE response is larger than the CONNECT request (~80B vs ~40B), giving a ~2× amplification factor.

An attacker with a 1 Gb/s uplink can induce tens of thousands of X25519 keygen+HKDF operations per second on the server and force unbounded growth of the `pending` map (no cap, only 5-second timeout eviction).

Fix direction: the stateless-cookie flow already called out in `next.md` is the right remedy. Before that lands, at minimum bound the `pending` table, apply a per-IP rate limit on `CONNECT`, and consider having the server return a cheap `RETRY` token that must be echoed before expensive work is performed.

### 1.4 [Crit] Reassembly store enables memory exhaustion
`fragment.go:154-212` (`reassemblyStore.addFragment`) will create a new buffer on any `fragmentID` it hasn't seen. There is no cap on the number of concurrent reassembly buffers, and each buffer eagerly allocates `make([][]byte, fh.FragmentCount)` (up to 255 slots) plus a copy of each received chunk (up to `MaxFragmentPayload` ≈ 1155 bytes).

With 65 536 distinct 16-bit IDs, a malicious peer holding a valid session (or any peer at all, post-handshake) can allocate ≈ 19 GB within the 5-second `FragmentTimeout` window, sending only a single fragment per ID.

Fix direction: bound the reassembly store by (a) total bytes in-flight per connection and (b) number of concurrent buffers per connection; reject new IDs when either cap is hit. Also consider deriving the initial `fragments` slice lazily rather than pre-allocating to `FragmentCount`.

### 1.5 [Crit] Reliable-ordered reorder buffer grows without bound
`channel.go:36,203-227` (`deliverReliableOrdered`) stores payloads keyed by raw sequence number in `recvBuffer` and only drains them once a gap is filled. The drain copy is correct (nicely done — reassembly handled via `stillBuffered`), but there is no upper bound. If a single packet at `recvNextDeliver` is dropped and the sender keeps transmitting (up to the congestion window), the map accumulates `cwnd-1` entries. With aggressive-mode congestion control the cap is effectively infinite (`Window()` returns 0, `CanSend` always true).

Fix direction: enforce a max reorder-buffer size per ordered channel (e.g. 512 entries). Any arrival exceeding the cap should be dropped and not acked, forcing the sender to slow down.

### 1.6 [High] `sendQueue` is unbounded
`connection.go:166-179` (`Send`). Callers can queue messages faster than the tick can drain them, with no feedback. On a congestion-gated connection, `drainSendQueue`/`requeue` can shuffle an ever-growing slice back and forth, and the caller receives no backpressure signal.

Fix direction: expose a per-connection send-queue cap in config, return `ErrSendQueueFull` when exceeded, and stop requeueing on `CanSend` failure — drop or coalesce unreliable messages instead.

### 1.7 [High] `pending` handshake table is unbounded
`server.go:101-138`. Pairs with 1.3 but is a concern even without spoofing: a burst of legitimate connects during a cold start can push the map into multi-MB territory. Add a cap with a sensible eviction policy.

### 1.8 [High] `MaxConnections` check is racy
`server.go:573`. `s.conns.count()` is consulted, then pending state is created, and the connection is inserted into `conns` at a later turn. Under load, concurrent `readLoop`/`tick` paths can admit more than `MaxConnections` connections. The count is also a full scan over 64 shards (see 2.6).

Fix direction: maintain an atomic `activeConns` counter updated on insert/remove, and count `pending` against it.

### 1.9 [Med] `ForEachConnection` runs user code under RLock
`server.go:329-331` exposes `ct.forEach`, which holds `shard.mu.RLock()` while invoking the user callback (`server.go:89-97`). A slow or blocking callback serializes that shard. Worst case the callback re-enters `s.Connection*` methods → deadlock.

The internal use inside `tick` is safe because it copies into a local slice first (`server.go:430-433`), but that is not how the exported method behaves. Either document "callback must be non-blocking and must not call back into Server" loudly, or (better) copy to a local slice and iterate without the lock held.

### 1.10 [Med] `CipherNone` bypasses replay detection
`crypto/cipher.go:149-196`. When the AEAD is nil (`CipherNone`), both `Encrypt` and `Decrypt` fall through to plain copy — the sender does not emit a nonce and the receiver does not consult the replay window. Current tests treat this as "plaintext mode", but nothing in the type signature or public docs warns that `CipherNone` also disables the replay window. At minimum, this should be a documented invariant. Better: disallow `CipherNone` for non-test builds behind a build tag, or require an explicit `fastwire.InsecureAllowPlaintext` sentinel in config.

### 1.11 [Med] `client.Client` fields are read across goroutines with mixed synchronization
`client.go:289-297,349-368,520-530`. `c.server`, `c.connected`, `c.connectErr`, `c.connectAborted` are each touched from both the read loop and the caller. Some paths hold `c.mu`; some read bare (e.g. `c.connectErr` at line 150). Happens-before is coincidentally preserved via `connectDone` close, but the invariant is fragile and not documented. Recommend consolidating all handshake state behind `c.mu` or behind a dedicated state machine.

### 1.12 [Med] `client.setupSendFunc` writes `conn.sendFunc` without the connection mutex
`client.go:197-214` sets `conn.sendFunc` directly. Reads of `sendFunc` in `connection.go:193` take `conn.mu`. Today `setupSendFunc` runs before `c.connected = true`, so the race is benign — but the contract is asymmetric and any future caller can break it.

### 1.13 [Med] Graceful shutdown drops in-flight reliable data
`server.Stop` (`server.go:276-300`) closes the socket, waits for loops, then marks every remaining connection `Disconnected`. No attempt is made to flush the send queue or wait for reliable packets to be acked. That's a legitimate policy, but it should be named (e.g. `Stop(ctx)` vs `Shutdown(ctx)`) and callable variants should exist.

### 1.14 [Low] `netip.AddrPort` hash function is weak
`server.go:42-54`. FNV-1a on raw 16-byte representation with the port folded in two bytes at a time. Acceptable, but for a server that places adversary-controlled addresses into the table, consider either `maphash` (keyed by a per-process seed) or Go's built-in map hash via a `map[netip.AddrPort]*Connection` shard.

### 1.15 [Low] `connectPacket.PublicKey = make([]byte, 32)` etc. — every handshake pays 4–5 heap allocations
`handshake.go:142,155,210`. These are trivial volumes at handshake rate, but an attacker-driven path (1.3) makes each cheap alloc add up. Consider fixed-size arrays (`[32]byte`) consistent with `challengeToken` and `MigrationToken`.

---

## 2. Performance

### 2.1 [High] Defensive copy on every `Send` call
`connection.go:175-178`. Every call allocates and copies the payload before enqueueing. For a 60 Hz game client sending 1 KB updates, that is 60 KB/s of garbage — small, but the same pattern appears in the batching `sendFunc` copy (`server.go:539`) and in `client.setupSendFunc:198-206`. Together, a single application `Send` can result in 3 copies before the bytes touch the socket.

Fix direction: document that the caller must not mutate `data` until the next tick returns (the common pattern for UDP APIs), or offer `SendOwned(data []byte)` that takes ownership and skips the copy.

### 2.2 [High] `flushBatch` allocates a fresh frame for every non-batched send
`connection.go:396-405` (`sendFramed`) allocates `make([]byte, …)` on every call when batching is enabled — used for retransmits, disconnects, and heartbeats. These are low-frequency, but under loss or pathological RTT the retransmit path can dominate. Use the pool.

### 2.3 [High] Server's coalesced write path copies every datagram
`server.go:538-549`. Every enqueue to `writeCh` does `cp := make([]byte, len(data))` then `copy`. Used instead of handing the existing pooled buffer to the write goroutine — which would avoid the allocation but would require the writer to call `putSendBuffer`. Re-plumbing this to pass ownership would likely be the single biggest throughput win in coalesced mode.

### 2.4 [High] `sendMessage` path allocates even when the message is small
Each non-fragmented send allocates a 16-byte header region (via pool, OK), then `compressPayload` allocates a fresh compression output (`compress.go:266`), and the pending queue appends a full `pendingPacket` struct by value (`channel.go:263-266`). For unreliable small messages the compression step is gated by `Hurdle`, so that is fine; but `compressPayload` on LZ4/zstd always passes `nil` dst — the compressors allocate internally. Consider threading a pooled `dst` through.

### 2.5 [Med] `tick()` allocates a `conns` slice every cycle
`server.go:430-433`. For a server holding 10 000 connections at 100 Hz, that is 8 MB/s of pointer-slice garbage. Reuse a slice across ticks (it already runs single-threaded from the tick goroutine), or iterate each shard in place after promoting the shards (1.9) to a lock-held copy.

### 2.6 [Med] `connectionTable.count` is O(shards)
`server.go:79-87`. Called on every new-connection admission (1.8). Keeping an `atomic.Int64` alongside would make the check O(1) and fix the race.

### 2.7 [Med] Heartbeat/multi-ack emits one packet per connection per tick
`server.go:807-815`. Independent of traffic volume. At 10 000 connections and `TickRate=100`, that is 1 M heartbeat packets/sec baseline. Consider suppressing heartbeats whenever `lastSendNano` was touched within the interval (the logic half-exists via `needsHeartbeat`, but the pending-ack packet bypasses it). Also consider an upper bound on ack cadence (e.g. emit when ≥ N new sequences have arrived or ≥ M ms elapsed, whichever first).

### 2.8 [Med] `LossTracker.RecordAck` is O(N) per ack
`internal/stats/loss.go:49-64`. Phase 13 traded the map for a linear scan — correct for 100-entry windows, but `RecordAck` is called for every acked sequence (and `processAcks` can produce dozens at a time on recovery). For each, scanning up to 100 entries is meaningful at high packet rates. Consider a sparse map keyed by `seq & (size-1)` (exploiting monotonic sequences).

### 2.9 [Med] `InFlightCount` takes a lock on every channel on every `CanSend`
`connection.go:288-294` iterates all channels, acquiring each channel's mutex. `CanSend` is called in the send-queue drain loop (`server.go:790`). With many channels and a full queue, this grows quadratically. Maintain a per-connection atomic inflight counter updated on `addPending` / ack.

### 2.10 [Med] `time.After` in read-loop backoff leaks timer goroutines
`server.go:349-352`, `client.go:279-281`. The classic pitfall: under bursts of transient errors, `time.After` creates a new `*time.Timer` each iteration, all kept alive until expiry. Use `time.NewTimer` with reset.

### 2.11 [Med] `needsHeartbeat*` and `State()` take `conn.mu` just to read `c.state`
`connection.go:297-345`. Make `state` an `atomic.Uint32`; all transitions are already serialized by `mu`. Eliminates a mutex acquire on every tick.

### 2.12 [Low] `rtt.State` mutex protects purely numeric operations
`internal/rtt/rtt.go`. Could be replaced with a single 64-bit atomic field encoding SRTT/VAR/RTO, or by using `sync/atomic.Value` for the whole snapshot. Very hot on the ack path.

### 2.13 [Low] `LossTracker.RecordSend` reloads `count` twice under the lock
`internal/stats/loss.go:34,43`. Minor, but use a local `c := lt.count.Load()`.

### 2.14 [Low] `unmarshalMultiAck` calls `make` for `entries`
`handshake.go:336`. Count is known (≤ 255); consider a fixed-size array and iterate with index, especially since the caller loops over it once and discards.

### 2.15 [Low] `compressPayload` returns a fresh slice even when it decides not to compress
`compress.go:261-286`. Consider returning the original slice with a flag — the current code already does that for the hurdle path. The incompressible/expansion path is fine as-is.

---

## 3. API & Design

### 3.1 [Med] Handler contract around slice lifetimes is undocumented
`callback.go:12` — `OnMessage(conn *Connection, data []byte, channel byte)`. Nothing in the docstring tells the user whether `data` is safe to retain beyond the callback. In reality (see `channel.go:203-227` and `server.go:702-707`), `data` often aliases a buffer from `decryptPool` and becomes invalid as soon as `processPacket` returns. This is an easy bug for downstream users to write. Either document the constraint prominently or copy in `OnMessage` (kills a pool win that Phase 13 just introduced).

### 3.2 [Med] `Connection.Close` and the disconnect retry loop duplicate state
`connection.go:227-277`, `server.go:714-741`, `client.go:557-587`. The client and server tick loops contain near-identical blocks handling disconnect retry. Extracting a shared `func (c *Connection) tickDisconnect(now time.Time) (done bool)` would remove a known-divergence risk — the client path, for example, has subtly different state-cleanup order (`connected=false`, `server=nil`) interleaved with connection bookkeeping.

### 3.3 [Med] Client/server tick logic is near-duplicated
`server.go:710-827` vs `client.go:553-679`. Roughly 100 lines differ only in who holds the mutex and what "handler" means. Cleaner would be to hoist the per-connection logic onto `*Connection` and have both client and server call it.

### 3.4 [Low] `variadic now ...time.Time` in `sendMessage`
`connection.go:425-431`. Use of variadic to simulate an optional `time.Time` is a minor wart — a regular parameter with `time.Time{}` sentinel or two named methods reads more clearly.

### 3.5 [Low] `ConnectionStats` is returned by value but constructed each call
`connection.go:211-223`. Each read grabs several locks (`rttState.SRTT`, `rttState.RTTVar`, etc.) with repeated mutex acquisition. Once 2.12 lands, snapshot in one go.

### 3.6 [Low] `ChannelLayout` hides channel count from users
`config.go:25-51`. Users can't introspect mode/streamIndex of a channel they constructed. Not broken, but ergonomics for tooling (dashboards, tests) are poor.

### 3.7 [Low] `features byte` used in several places where `FeatureFlag` would be clearer
`connection.go:126,158`, `server.go:460`. Cast and mask littered through the code. Prefer `has(FeatureConnectionMigration)` helper methods on `*Connection`.

---

## 4. Minor Nits

- `fastwire.go:16` — `ApplicationVersion` is a package-level mutable `var`. Fine for a library but invite users to surprise themselves. Consider `SetApplicationVersion` once and panic on re-set, or thread through config.
- `wire.go:38` — `for i := 0; i < 5; i++` — use `range 5` to match newer style already used elsewhere (`handshake.go:337`, `fragment.go:102`).
- `errors.go:9-90` — 20+ sentinel errors at package scope. Consider grouping by category (`ErrWire*`, `ErrHandshake*`) in separate files.
- `compress.go:303-309` — on-demand construction of a temporary `zstdCompressor` for decode-only use is slow and can hide a misconfiguration; return `ErrDecompressionFailed` explicitly when a zstd-flagged fragment arrives on a connection that didn't negotiate zstd.
- `fragment.go:80-99` — `splitMessage` computes `lastChunkLen` with two branches; the initial single-fragment case is easier to read as an early return.
- `packet.go:75` — `UnmarshalHeader` returns a value type `PacketHeader` and copies. Low traffic volume, but consider `UnmarshalHeaderInto(buf, *PacketHeader)` to permit stack allocation at callers.
- `crypto/cipher.go:91-98` — comment says "high index = older bits"; the loop direction and indexing are correct but the shift-by-zero guard (line 99) hides a real bug class — worth a dedicated test for `shift == 0` and `shift == 1023`.

---

## 5. Recommended Prioritization

1. **Ship security fixes before any release**: 1.1 (perf/stability) plus 1.2, 1.3, 1.4, 1.5 (all blast-radius attacks).
2. **Tighten backpressure**: 1.6, 1.7, 1.8, 2.7.
3. **Reclaim the pooling win**: 1.1, 2.1, 2.2, 2.3, then 2.5, 2.9.
4. **Mutex reductions**: 2.11, 2.12, 1.9.
5. **API hardening**: 3.1 (document slice lifetime — a footgun for every future user).

Everything in sections 3 and 4 is worth a separate PR once the above lands.
