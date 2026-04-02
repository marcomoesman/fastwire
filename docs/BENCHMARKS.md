# FastWire Benchmarks

Benchmark results from an Apple M4 MacBook Pro (10-core, darwin/arm64), Go 1.26, 5 runs per benchmark.

## Wire Primitives

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| VarIntEncode | 1.72 | 0 | 0 |
| VarIntDecode | 1.73 | 0 | 0 |

VarInt encode/decode of a 2-byte value (300) runs at sub-2ns with zero allocations.

## Packet Header

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| PacketMarshal | 2.26 | 0 | 0 |
| PacketUnmarshal | 2.59 | 0 | 0 |

Header marshal/unmarshal operates entirely on caller-supplied buffers — zero allocations.

## Encryption (100-byte payload)

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| EncryptAES | 95.2 | 1051 | 144 | 2 |
| DecryptAES | 111.8 | 896 | 128 | 2 |
| EncryptChaCha | 235.2 | 425 | 144 | 2 |
| DecryptChaCha | 261.0 | 383 | 128 | 2 |

AES-128-GCM is ~2.5x faster than ChaCha20-Poly1305 on M4 (hardware AES-NI). Both achieve sub-microsecond per-packet latency.

## Compression (1000-byte compressible payload)

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| CompressLZ4 | 3929 | 257 | 1179 | 1 |
| CompressZstd | 1933 | 521 | 1029 | 1 |

Zstd achieves ~2x the throughput of LZ4 on this workload. Both use pooled compressor instances (1 alloc for the output buffer).

## Fragmentation (4000-byte payload, ~4 fragments)

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| FragmentSplit | 343 | 4192 | 2 |
| FragmentReassemble | 807 | 8896 | 10 |

Splitting uses a single contiguous backing allocation (2 allocs total). Reassembly uses single-pass copy with tracked byte totals.

## Full Send Pipeline (500-byte compressible payload)

Exercises the complete `sendMessage` pipeline: compress → marshal header → sequence tracking → encrypt → writeEncrypted, with ack recycling every 32 packets.

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| SendPlain (no crypto, no compression) | 225 | 2,228 | 1335 | 3 |
| SendAES_LZ4 (AES-128-GCM + LZ4) | 4,138 | 121 | 2,224 | 7 |
| SendAES_Zstd (AES-128-GCM + Zstd) | 2,071 | 241 | 2,401 | 7 |

Without compression or encryption, the raw send pipeline achieves ~2.2 GB/s. LZ4 compression dominates the encrypted send path (~3,930 ns/op for compression alone). Zstd provides ~2x higher throughput than LZ4 for compressible payloads.

## Full Receive Pipeline (500-byte compressible payload)

Exercises the complete receive pipeline matching `processPacket`: decrypt → unmarshal → touchRecv → ACK processing → recordReceive → fragment reassembly → decompress → channel deliver → handler callback.

| Benchmark | ns/op | MB/s | B/op | allocs/op |
|-----------|------:|-----:|-----:|----------:|
| RecvPlain (no crypto, no compression) | 104.6 | 4,779 | 48 | 2 |
| RecvAES_LZ4 (AES-128-GCM + LZ4) | 363.9 | 1,374 | 808 | 8 |
| RecvAES_Zstd (AES-128-GCM + Zstd) | 649.6 | 770 | 957 | 9 |

The receive path is faster than send because decompression is inherently cheaper than compression (LZ4 decompression is essentially memcpy with offsets, while compression requires hash table lookups). Decrypt output uses pooled buffers via `sync.Pool`. Compressed payloads go through fragment reassembly even for single-fragment messages (compression flags force fragmentation in the wire format).

## Server Throughput

| Benchmark | ns/op | B/op | allocs/op | Description |
|-----------|------:|-----:|----------:|-------------|
| ServerThroughput | 2,806 | 2,193 | 15 | 10 clients Send() + server Tick() per iteration |

## Loss Tracker

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| LossRecordSend | 5.07 | 0 | 0 |
| LossRecordAck | 48.1 | 0 | 0 |
| LossLoss | 0.48 | 0 | 0 |

Loss ratio reads are fully lock-free via atomics. The ring buffer scan (100 entries max) has better cache locality than a map-based approach.

## How to Reproduce

```bash
# Run all benchmarks (skip tests)
go test -run='^$' -bench=. -benchmem ./...

# Run with multiple iterations for stability
go test -run='^$' -bench=. -benchmem -count=5 ./...

# Run a specific benchmark
go test -run='^$' -bench=BenchmarkSendAES_LZ4 -benchmem ./...
```
