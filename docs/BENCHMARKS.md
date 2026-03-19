# FastWire Benchmarks

Benchmark results from an Apple M4 MacBook Pro (10-core, darwin/arm64), Go 1.26, 3 runs per benchmark.

## Wire Primitives

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| VarIntEncode | 1.73 | — | 0 | 0 |
| VarIntDecode | 1.73 | — | 0 | 0 |

VarInt encode/decode of a 2-byte value (300) runs at sub-2ns with zero allocations.

## Packet Header

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| PacketMarshal | 2.31 | — | 0 | 0 |
| PacketUnmarshal | 2.60 | — | 0 | 0 |

Header marshal/unmarshal operates entirely on caller-supplied buffers — zero allocations.

## Encryption (100-byte payload)

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| EncryptAES | 98.4 | 1016 | 144 | 2 |
| DecryptAES | 106.2 | 941 | 128 | 2 |
| EncryptChaCha | 245.3 | 408 | 144 | 2 |
| DecryptChaCha | 261.9 | 382 | 128 | 2 |

AES-128-GCM is ~2.5x faster than ChaCha20-Poly1305 on M4 (hardware AES-NI). Both achieve sub-microsecond per-packet latency.

## Compression (1000-byte compressible payload)

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| CompressLZ4 | 4009 | 251 | 1184 | 1 |
| CompressZstd | 1975 | 510 | 1029 | 1 |

Zstd achieves ~2x the throughput of LZ4 on this workload. Both use pooled compressor instances (1 alloc for the output buffer).

## Fragmentation (4000-byte payload, ~4 fragments)

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| FragmentSplit | 583 | — | 4512 | 5 |
| FragmentReassemble | 1090 | — | 8896 | 10 |

Splitting is ~2x faster than reassembly. The reassembly store allocates buffers for each fragment plus the final concatenation.

## Full Pipeline

| Benchmark | ns/op | MB/s | B/op | allocs/op | Description |
|-----------|------:|-----:|-----:|----------:|-------------|
| FullSendPath | 4236 | 118 | 1422 | 7 | compress → fragment → encrypt → send (500B payload, AES + LZ4) |
| FullRecvPath | 294 | 1861 | 528 | 2 | decrypt → unmarshal → decompress (500B payload, AES, no compression) |
| ServerThroughput | 3560 | — | 2298 | 15 | 10 clients Send() + server Tick() per iteration |

The send path is dominated by compression and encryption. The receive path is faster because the benchmark skips decompression (no compressed flag set).

## How to Reproduce

```bash
# Run all benchmarks (skip tests)
go test -run='^$' -bench=. -benchmem ./...

# Run with multiple iterations for stability
go test -run='^$' -bench=. -benchmem -count=3 ./...

# Run a specific benchmark
go test -run='^$' -bench=BenchmarkEncryptAES -benchmem ./...
```
