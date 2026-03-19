# Phase 6 — Compression

## What was built

Added LZ4 and zstd compression to the send/receive pipeline. Compression sits between the application payload and the fragmentation layer: **compress -> fragment -> encrypt** on send, **decrypt -> reassemble -> decompress** on receive.

### New file: `compress.go`

- **`Compressor` interface** — `Compress(dst, src)`, `Decompress(dst, src)`, `Algorithm()`.
- **`lz4Compressor`** — Uses `lz4.CompressBlock` / `lz4.UncompressBlock` (block API). Wire format: `[original_size uint32 LE][lz4 block data]`. Thread-safe via `sync.Pool` of `[]int` hash tables (size 1<<16). Returns `ErrCompressionFailed` for incompressible data (CompressBlock returns 0).
- **`zstdCompressor`** — Uses `sync.Pool` of `*zstd.Encoder` and `*zstd.Decoder`. Zstd frame format is self-describing (no size prefix needed). Dictionary loaded at construction time. `WithDecoderMaxMemory(MaxDecompressedSize)` for bomb protection.
- **`compressorPool`** — High-level wrapper that handles hurdle point, expansion check, and flag setting. `compressPayload` returns the (possibly compressed) payload and the fragment flags to set. `decompressPayload` dispatches based on fragment flags.
- **`DictionaryHash`** — SHA-256 hash of a compression dictionary.

### Modified files

- **`errors.go`** — Added `ErrCompressionFailed` and `ErrDecompressionFailed`.
- **`config.go`** — Added `DefaultCompressionHurdle` (128 bytes) and `MaxDecompressedSize` (4 MB).
- **`connection.go`** — Added `compress *compressorPool` field. `newConnection` takes `CompressionConfig` parameter.
- **`handshake.go`** — Added `compressionConfig CompressionConfig` to `pendingHandshake`. Enhanced `serverProcessConnect` to validate dictionary hashes (SHA-256 comparison when both sides use zstd with dictionaries). `serverProcessResponse` passes negotiated config to `newConnection`.
- **`connection_test.go`** / **`fragment_test.go`** — Updated `newConnection` call sites to pass `CompressionConfig{}`.

### New file: `compress_test.go`

Comprehensive test suite covering all acceptance criteria.

## Design decisions

1. **Block API for LZ4** — Used block-level API (not framed) for minimal overhead. Requires a 4-byte original size prefix since block API doesn't store it.
2. **sync.Pool for thread safety** — Both LZ4 hash tables and zstd encoder/decoder instances are pooled to avoid allocations on the hot path while remaining safe for concurrent use.
3. **Hurdle point default 128 bytes** — Payloads smaller than this are not worth compressing due to overhead.
4. **Expansion check** — If compressed output >= original size, the original is returned uncompressed with no flags set. For LZ4, `CompressBlock` returning 0 also triggers this path.
5. **Decompression bomb protection** — LZ4 checks the 4-byte original size prefix against `MaxDecompressedSize`. Zstd uses `WithDecoderMaxMemory`.
6. **Dictionary hash validation** — During handshake, when both sides use zstd with dictionaries, SHA-256 hashes are compared. Mismatch results in `compressionDictMismatch` ack.

## Test checklist

- [x] LZ4 round-trip (small, medium, multi-fragment sizes)
- [x] Zstd round-trip (small, medium, multi-fragment sizes)
- [x] Hurdle point: payload below 128 bytes skipped
- [x] Expansion check: random/incompressible data returns original
- [x] Fragment flags: LZ4 -> `FragFlagCompressed`, zstd -> `FragFlagCompressed|FragFlagZstd`
- [x] `CompressionNone` passthrough
- [x] Empty payload -> below hurdle -> skip
- [x] Dictionary hash SHA-256 correctness
- [x] Concurrent compression (100 goroutines, validates sync.Pool safety)
- [x] Max decompressed size / bomb protection
- [x] Compress -> fragment -> reassemble -> decompress integration test
- [x] Handshake dictionary hash match
- [x] Handshake dictionary hash mismatch
- [x] Connection has compressor pool
- [x] All tests pass with `-race`
