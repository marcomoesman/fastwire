# Phase 16 — Performance Optimization Pass 3

## What Was Done

Hot-path follow-up after the senior audit remaining items and a broken mid-edit of `compress.go`.

### 1. LZ4 compressor: drop unused hash-table pool
- **File:** `compress.go`
- The cut-off edit was removing FastWire's 64Ki-entry `[]int` hash table. `pierrec/lz4` v4 ignores the `hashTable` argument on `CompressBlock` and uses its own pooled `Compressor` (bitmap reset, not a 256 KiB zero). Keeping our table was a 256 KiB write per compress with no effect.
- `CompressBlock` is now called with `nil`. The compressor is a zero-size struct.

### 2. Dependency updates
- `github.com/klauspost/compress` v1.18.4 → v1.19.2 (zstd arm64 / dict-encoder fixes)
- `github.com/pierrec/lz4/v4` v4.1.26 → v4.1.28 (arm64 decode)
- `golang.org/x/crypto` v0.49.0 → v0.55.0 (ChaCha20-Poly1305 only; GO-2026-5932 is openpgp and does not apply)

### 3. Send-path allocation cuts
- `sendFramed` and disconnect batch frames use `getSendBuffer` instead of `make`.
- `compressPayload` writes into a pooled destination and always returns that scratch to the pool before returning. If the compressor aliases the scratch (zstd `EncodeAll`, LZ4), the compressed bytes are copied out so callers — including benchmarks — cannot leak a 1200-byte buffer.
- `SendOwned` queues without copying. `Send` still copies for the existing safe API.
- Coalesced server writes copy into a pooled buffer instead of a fresh `make`. Write loop returns the buffer to the pool.
- Client migration-token prefix uses the send pool.
- `flushBatch` double-buffers `batchBuf` / `batchIdle` instead of allocating a copy of the packet slice each flush.
- `putSendBuffer` / `putDecryptBuffer` only recycle `cap == DefaultMTU` buffers so oversized heap slices cannot pollute the pool.

### 4. Tick / ack-path mutex cuts
- `Connection.state` is `atomic.Uint32`; `State()`, `needsHeartbeat*`, and `Close` no longer take `conn.mu` for the state word. `Close` uses `CompareAndSwap`.
- Per-channel `pending atomic.Int32` makes `InFlightCount` / `CanSend` lock-free on the channel mutex.
- `LossTracker.Loss()` is lock-free (atomics only), matching the Phase 13 intent.
- `Server.tick` reuses `tickConns` instead of allocating a pointer slice every cycle.

### 5. Shutdown and timer hygiene
- `Server.Stop` / `Client.Close` drain leftover `incoming` (and server `writeCh`) so pooled read/write buffers are returned.
- Read-loop backoff and client connect timeout use `time.NewTimer` instead of `time.After`.

### 6. API / docs
- `Handler.OnMessage` documents that `data` is invalid after return.
- `docs/DOCS.md` and `docs/EXAMPLES.md` updated for slice lifetime and `SendOwned`.

## Benchmark Results

Same machine, `go test -count=5 -benchmem`, Ryzen 9 9900X / windows/amd64 / Go 1.26. Compared with `benchstat` against HEAD before this phase.

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| SendAES_LZ4 | 2167 ns, 855 B | 401 ns, 256 B | **-81% latency, -70% B/op** |
| CompressLZ4 | 2534 ns, (hash-table zero) | 255 ns, 89 B | **-90%** |
| ServerThroughput | 2719 ns, 15 allocs | 2468 ns, 10 allocs | **-9% latency, -33% allocs** |
| SendAES_Zstd | 2054 ns, 832 B | 2266 ns, 322 B | +10% latency, **-61% B/op** |
| Loss() | 7.76 ns | 0.18 ns | **-98%** (mutex removed) |
| CompressZstd | 2129 ns, 1.0 KiB | 2046 ns, 105 B | ~ latency, **-90% B/op** |

SendPlain / RecvPlain / FragmentSplit moved a few percent with no code changes on those paths — treated as run noise. RecvAES_* unchanged.

`CompressZstd` initially looked like a +19% / +700 B regression. That was `BenchmarkCompressZstd` leaking a pooled 1200-byte buffer every iteration: zstd `EncodeAll` aliases `dst`, and the benchmark discarded the result without returning it to the pool. After `compressPayload` always recycles its scratch (copying aliased output out), CompressZstd is ~2046 ns / 105 B. CompressLZ4's earlier "2013 ns" figure included the same leak; without it the LZ4 path is ~255 ns. SendAES_* pick up one extra small alloc for that copy-out (~60–80 B).

## Design Decisions

- **`compressPayload` owns its scratch.** Returning a pooled buffer to the caller leaked a 1200-byte `sync.Pool` entry whenever the caller discarded the result (the CompressZstd/LZ4 benchmarks). Zstd `EncodeAll` aliases the destination, so a `putSendBuffer` on the send path was easy to get right and easy for everyone else to miss. The scratch is always returned inside `compressPayload`; aliased output is copied out. That adds one small alloc of the compressed frame on the send path (~60 B for the zstd bench payload) in exchange for a leak-proof API.
- **Coalesced writes still copy.** Transferring buffer ownership into `sendFunc` would break wrappers such as the packet-drop test and disconnect retries (the same packet is sent multiple times). Copying into a pooled buffer removes the heap alloc without changing the `sendFunc` contract.
- **Do not reintroduce a LossTracker index map.** Phase 13 removed it for zero-alloc `RecordSend` and a cache-friendly 100-entry scan. Left as-is.
- **Heartbeat cadence unchanged.** Flushing pending acks every tick is load-bearing for latency; suppressing it would be a behavior change, not a free win.

## Test Requirements

- [x] All existing tests pass (`go test ./...`)
- [x] `go vet ./...` passes
- [ ] `go test -race ./...` — not run here (`-race` needs CGO; this repo is no-CGO and the machine has no gcc)
- [x] LZ4 compress/decompress reuse (large → small → large) round-trip
- [x] `Send` copies the caller buffer
- [x] `SendOwned` aliases the caller buffer
- [x] `compressPayload` recycles its scratch (result does not alias the send pool)
