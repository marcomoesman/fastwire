# Phase 1 — Core Primitives & Wire Format

## What was done

- Created the `fastwire` package with protocol version constant (`ProtocolVersion = 1`).
- Defined all sentinel errors in `errors.go`.
- Implemented wire primitives in `wire.go`:
  - **VarInt**: unsigned LEB128 uint32 encoding (max 5 bytes).
  - **VarLong**: unsigned LEB128 uint64 encoding (max 10 bytes).
  - **String**: int16 big-endian length-prefixed UTF-8 strings (max 32767 bytes).
  - **UUID**: 128-bit identifier as two big-endian uint64s, with `UUIDFromInts`, `MSB()`, `LSB()`, `String()`.
- Implemented packet header serialization in `packet.go`:
  - `PacketFlag`, `PacketHeader`, `ControlType` types.
  - `MarshalHeader` / `UnmarshalHeader` with VarInt-encoded Sequence/Ack and 4-byte LE AckField.
- Implemented fragment header serialization in `fragment.go`:
  - `FragmentFlag`, `FragmentHeader` types.
  - `MarshalFragmentHeader` / `UnmarshalFragmentHeader` (fixed 5 bytes).
- Created config stubs in `config.go`: `DeliveryMode`, `ChannelLayout`, `CompressionConfig`, `CongestionMode`, `CipherSuite`.
- Created buffer pool in `pool.go`: `sync.Pool` with `DefaultMTU = 1200`.

## Design decisions

- All `Put*` functions write into caller-provided buffers and return bytes written (no allocation).
- All `Read*` functions return (value, bytesConsumed, error) for composability.
- `PutVarInt`/`PutVarLong` do not return errors — the caller must ensure sufficient buffer space. This matches the pattern of encoding functions that are always called with known-good buffers internally.
- String length prefix is `int16` (signed) to detect negative length corruption.
- UUID is stored as raw `[16]byte` on the wire — `PutUUID` just copies bytes, `UUIDFromInts` handles the big-endian layout.
- `MarshalHeader` returns error (unlike `PutVarInt`) because the variable-length VarInts make buffer size validation non-trivial.
- `CipherSuite` is defined in `config.go` as a stub for Phase 2.

## Test coverage

- [x] VarInt: round-trip (0, 127, 128, 16383, 16384, max uint32), truncated buffer, overflow, empty buffer, fuzz
- [x] VarLong: round-trip (0, 127, 128, max uint32, max uint64), truncated buffer, overflow, fuzz
- [x] String: round-trip (empty, short, multi-byte UTF-8), max length (32767), too long, negative length, buffer too small, UTF-8 preservation, fuzz
- [x] UUID: round-trip, MSB/LSB correctness, UUIDFromInts round-trip, String() format regex, buffer too small, fuzz
- [x] PacketHeader: round-trip (various Sequence/Ack values), all flag combos, truncated buffer, marshal buffer too small, fuzz
- [x] FragmentHeader: round-trip, all flag combos, truncated buffer (0-4 bytes), marshal buffer too small
- [x] Pool: GetBuffer returns DefaultMTU length, Get/Put cycle, buffer reusability
