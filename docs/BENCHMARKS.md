# FastWire Benchmarks

Benchmark results from an Apple M4 MacBook Pro (10-core, darwin/arm64), Go 1.26, 6 runs per benchmark.

## Wire Primitives

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| VarIntEncode | 1.70 | — | 0 | 0 |
| VarIntDecode | 1.70 | — | 0 | 0 |

VarInt encode/decode of a 2-byte value (300) runs at sub-2ns with zero allocations.

## Packet Header

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| PacketMarshal | 2.49 | — | 0 | 0 |
| PacketUnmarshal | 2.59 | — | 0 | 0 |

Header marshal/unmarshal operates entirely on caller-supplied buffers — zero allocations.

## Encryption (100-byte payload)

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| EncryptAES | 93.8 | 1067 | 144 | 2 |
| DecryptAES | 105.0 | 953 | 128 | 2 |
| EncryptChaCha | 230.5 | 434 | 144 | 2 |
| DecryptChaCha | 257.5 | 389 | 128 | 2 |

AES-128-GCM is ~2.5x faster than ChaCha20-Poly1305 on M4 (hardware AES-NI). Both achieve sub-microsecond per-packet latency.

## Compression (1000-byte compressible payload)

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| CompressLZ4 | 3957 | 255 | 1199 | 1 |
| CompressZstd | 1968 | 512 | 1030 | 1 |

Zstd achieves ~2x the throughput of LZ4 on this workload. Both use pooled compressor instances (1 alloc for the output buffer).

## Fragmentation (4000-byte payload, ~4 fragments)

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| FragmentSplit | 569 | — | 4512 | 5 |
| FragmentReassemble | 1082 | — | 8896 | 10 |

Splitting is ~2x faster than reassembly. The reassembly store allocates buffers for each fragment plus the final concatenation.

## Full Pipeline

| Benchmark | ns/op | MB/s | B/op | allocs/op | Description |
|-----------|------:|-----:|-----:|----------:|-------------|
| FullSendPath | 4096 | 122 | 875 | 6 | compress → fragment → encrypt → send (500B payload, AES + LZ4, with ack recycling) |
| FullRecvPath | 189 | 2689 | 528 | 2 | decrypt → unmarshal → decompress (500B payload, AES, no compression) |
| ServerThroughput | 2795 | — | 2040 | 15 | 10 clients Send() + server Tick() per iteration |

The send path is dominated by compression and encryption. Buffer pooling eliminates per-packet send buffer and retransmit copy allocations — send buffers are recycled via `sync.Pool` when packets are acknowledged. The receive path is faster because the benchmark skips decompression (no compressed flag set). Read buffers are also pooled in the read loop but this is not reflected in the RecvPath microbenchmark.

Key optimizations:
- **Inline FNV-1a** for connection table sharding eliminates per-packet hash allocation on the server
- **Bitfield-based ack processing** replaces map allocation in the hot ack path
- **Atomic timestamps** remove mutex contention from send/recv touch operations
- **Single `time.Now()` per tick** threaded through all downstream methods
- **Pooled heartbeat/retransmit buffers** via `sync.Pool` for plaintext and encrypted output
- **O(1) loss tracking** via index map in the LossTracker ring buffer
- **Pooled batch datagram buffers** recycle MTU-sized buffers in `flushBatch`

## How to Reproduce

```bash
# Run all benchmarks (skip tests)
go test -run='^$' -bench=. -benchmem ./...

# Run with multiple iterations for stability
go test -run='^$' -bench=. -benchmem -count=6 ./...

# Run a specific benchmark
go test -run='^$' -bench=BenchmarkEncryptAES -benchmem ./...
```
