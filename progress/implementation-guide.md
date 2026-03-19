# FastWire — Implementation Guide

Authoritative reference for all implementation phases, design decisions, file structure, and constraints. Read `progress/design.md` for the full protocol specification — this guide covers **how** to build it.

---

## Constraints

- **Go 1.26**, no CGO
- Pure Go dependencies only
- Compression libraries: `github.com/pierrec/lz4/v4` (LZ4), `github.com/klauspost/compress/zstd` (zstd with dictionary support)
- Crypto: Go stdlib only (`crypto/aes`, `crypto/cipher`, `golang.org/x/crypto/chacha20poly1305`, `crypto/ecdh`, `crypto/sha256`, `golang.org/x/crypto/hkdf`)
- Each encrypted packet uses its own AEAD operation (no multi-packet coalescing within a datagram — deferred to future optimization)
- Stateless handshake cookies deferred — implement stateful challenge first
- Send batching applies to tick-flush path only; `SendImmediate()` always sends a standalone datagram

---

## Project Structure

Flat package `fastwire`. All exported types live at the package root.

```
fastwire/
├── go.mod
├── go.sum
├── fastwire.go        # Package doc, protocol version constant
├── config.go          # All configuration types (ServerConfig, ClientConfig, ChannelLayout, CompressionConfig, etc.)
├── errors.go          # Sentinel errors and error types
├── wire.go            # Wire primitives: VarInt, VarLong, String, UUID encoding/decoding
├── packet.go          # Packet header, flags, serialization/deserialization
├── fragment.go        # Fragment header, send-side splitting, receive-side reassembly
├── compress.go        # Compression layer (LZ4, zstd, hurdle point, dictionary)
├── encrypt.go         # AEAD ciphers (AES-128-GCM, ChaCha20-Poly1305), nonce counter, replay protection
├── crypto.go          # X25519 key exchange, HKDF key derivation
├── channel.go         # Channel struct, delivery mode logic, sequence counters, ack tracking
├── reliability.go     # RTT measurement, retransmission queue, RTO calculation
├── congestion.go      # Congestion control (conservative AIMD, aggressive)
├── connection.go      # Connection struct, state machine, per-connection state
├── handshake.go       # 3-way handshake (CONNECT, CHALLENGE, RESPONSE), control packets
├── server.go          # Server struct, read goroutine, worker pool, tick loop, connection table
├── client.go          # Client struct, read goroutine, tick loop, single connection
├── callback.go        # Handler interface (OnConnect, OnDisconnect, OnMessage, OnError)
├── stats.go           # Per-connection metrics (RTT, loss rate, bytes, cwnd, uptime)
├── pool.go            # sync.Pool-based buffer pools for packet allocation
├── *_test.go          # Test files mirror source files
├── DOCS.md
├── EXAMPLES.md
└── progress/
    ├── design.md
    ├── implementation-guide.md
    ├── overview.md
    ├── next.md
    └── phase-*.md
```

---

## Phase 1 — Core Primitives & Wire Format

**Goal:** Establish the byte-level building blocks that every other layer depends on.

### Files

- `fastwire.go` — package doc, `ProtocolVersion` constant (uint16, start at 1)
- `config.go` — configuration structs (stubs for now, filled in as phases progress)
- `errors.go` — base error types
- `wire.go` — VarInt, VarLong, String, UUID encode/decode
- `packet.go` — packet header, flags, serialization
- `fragment.go` — fragment header serialization only (reassembly logic in Phase 5)
- `pool.go` — `sync.Pool` for `[]byte` buffers

### Wire Primitives (wire.go)

All wire primitives live in a single file. Each has a `Put*` (encode into buffer, returns bytes written) and a read function (decode from buffer, returns value + bytes read + error).

**VarInt** — Unsigned LEB128, uint32, max 5 bytes. Identical to Protocol Buffers encoding.

```
PutVarInt(buf []byte, v uint32) int
ReadVarInt(buf []byte) (uint32, int, error)
```

**VarLong** — Unsigned LEB128, uint64, max 10 bytes. Same encoding as VarInt but extended to 64 bits.

```
PutVarLong(buf []byte, v uint64) int
ReadVarLong(buf []byte) (uint64, int, error)
```

**String** — Length-prefixed UTF-8 string. Length is a signed int16 (big-endian), max value 32767. Negative lengths are invalid.

```
PutString(buf []byte, s string) (int, error)
ReadString(buf []byte) (string, int, error)
```

Encoding: `[length int16 big-endian][UTF-8 bytes]`. Returns error if string exceeds 32767 bytes or buffer is too small.

**UUID** — 128-bit identifier encoded as two uint64s in big-endian: most significant 64 bits first, then least significant 64 bits. Fixed 16 bytes on the wire.

```go
type UUID [16]byte

func PutUUID(buf []byte, id UUID) int
func ReadUUID(buf []byte) (UUID, int, error)
func UUIDFromInts(msb, lsb uint64) UUID
func (u UUID) MSB() uint64
func (u UUID) LSB() uint64
func (u UUID) String() string // standard 8-4-4-4-12 hex format
```

### Packet Header (packet.go)

The decrypted payload header. Define:

```go
type PacketFlag byte

const (
    FlagFragment   PacketFlag = 1 << 0 // payload is a fragment
    FlagControl    PacketFlag = 1 << 1 // connection control packet (handshake/disconnect/heartbeat)
)
```

Header struct and serialization:

```go
type PacketHeader struct {
    Flags    PacketFlag
    Channel  byte
    Sequence uint32
    Ack      uint32
    AckField uint32 // 32-bit bitfield
}
```

- `MarshalHeader(buf []byte, h *PacketHeader) int` — writes header into buf, returns bytes written. Sequence and Ack are VarInt-encoded; AckField is fixed 4 bytes little-endian.
- `UnmarshalHeader(buf []byte) (PacketHeader, int, error)` — parses header, returns bytes consumed.

Control packets (FlagControl set) still carry Channel/Sequence/Ack/AckField — the unreliable channel (0 overhead since heartbeats use them for piggybacked acks). Define control sub-types via the first byte of the payload:

```go
type ControlType byte

const (
    ControlConnect         ControlType = 0x01
    ControlChallenge       ControlType = 0x02
    ControlResponse        ControlType = 0x03
    ControlConnected       ControlType = 0x04
    ControlDisconnect      ControlType = 0x05
    ControlHeartbeat       ControlType = 0x06
    ControlVersionMismatch ControlType = 0x07
    ControlReject          ControlType = 0x08
)
```

### Fragment Header (fragment.go)

```go
type FragmentHeader struct {
    FragmentID    uint16
    FragmentIndex byte
    FragmentCount byte
    FragmentFlags byte
}

const (
    FragFlagCompressed FragmentFlag = 1 << 0
    FragFlagZstd       FragmentFlag = 1 << 1 // 0=LZ4, 1=zstd (only when Compressed is set)
)
```

Marshal/Unmarshal: fixed 5 bytes (2 + 1 + 1 + 1).

### Buffer Pool (pool.go)

`sync.Pool` for `[]byte` slices sized to MTU. Provide `GetBuffer() []byte` and `PutBuffer([]byte)`. Pre-size to default MTU (1200).

### Config Stubs (config.go)

Define the configuration types referenced by the design doc. These will be populated across phases:

```go
type ChannelLayout struct { ... }
type CompressionConfig struct { ... }
type CongestionMode int

const (
    CongestionConservative CongestionMode = iota
    CongestionAggressive
)
```

### Tests

- `wire_test.go` — VarInt: round-trip for edge values (0, 127, 128, max uint32), truncated buffer, overflow. VarLong: round-trip for edge values (0, 127, 128, max uint64), truncated buffer, overflow. String: round-trip for empty/short/max-length strings, string exceeding 32767 bytes returns error, negative length detection, UTF-8 preservation. UUID: round-trip, MSB/LSB accessors, UUIDFromInts round-trip, String() format. Fuzz targets for VarInt, VarLong, String, and UUID parsers.
- `packet_test.go` — round-trip header marshal/unmarshal, flags combinations
- `fragment_test.go` — round-trip fragment header
- Fuzz targets for header parsers

### Acceptance Criteria

- All encode/decode and marshal/unmarshal functions round-trip cleanly
- Fuzz tests run for 30s with no panics
- `go vet ./...` and `golangci-lint run` pass

---

## Phase 2 — Encryption Layer

**Goal:** Encrypt and decrypt individual packets. Nonce management and replay protection.

### Files

- `encrypt.go` — AEAD abstraction, nonce counter, replay window
- `crypto.go` — X25519 ECDH, HKDF key derivation

### AEAD Abstraction (encrypt.go)

```go
type CipherSuite byte

const (
    CipherNone             CipherSuite = 0
    CipherAES128GCM        CipherSuite = 1
    CipherChaCha20Poly1305 CipherSuite = 2
)
```

A `cipherState` (unexported) per direction (send/recv) holds:
- `aead cipher.AEAD`
- `nonce atomic.Uint64` (send side, monotonically increasing)
- `replayWindow` (recv side)

Functions:
- `encrypt(state *cipherState, plaintext []byte, dst []byte) ([]byte, error)` — increments nonce, prepends 8-byte nonce, appends AEAD tag. The wire format is `[nonce 8B][ciphertext][tag 16B]`.
- `decrypt(state *cipherState, packet []byte, dst []byte) ([]byte, error)` — reads 8-byte nonce, checks replay window, decrypts, returns plaintext.

**Wire overhead per packet:** 8 (nonce) + 16 (tag) = 24 bytes.

### Replay Protection

Sliding window of 1024 packets. Maintain:
- `maxNonce uint64` — highest accepted nonce
- `window [1024/64]uint64` — bitfield (16 × uint64)

Accept if: nonce > maxNonce (advance window), or nonce >= maxNonce-1023 and bit not set. Reject otherwise.

### Key Exchange (crypto.go)

```go
type KeyPair struct {
    Private *ecdh.PrivateKey
    Public  *ecdh.PublicKey
}

func GenerateKeyPair() (KeyPair, error)
func DeriveKeys(myPrivate *ecdh.PrivateKey, theirPublic *ecdh.PublicKey, challengeToken []byte, suite CipherSuite) (sendKey, recvKey []byte, err error)
```

Key derivation steps:
1. `shared = myPrivate.ECDH(theirPublic)` (X25519)
2. `prk = HKDF-Extract(salt=challengeToken, ikm=shared)`
3. `c2sKey = HKDF-Expand(prk, info="c2s", length=keySize)`
4. `s2cKey = HKDF-Expand(prk, info="s2c", length=keySize)`

Key sizes: 16 for AES-128-GCM, 32 for ChaCha20-Poly1305.

The caller (client or server) maps c2s/s2c to send/recv based on its role.

### Tests

- `encrypt_test.go` — encrypt/decrypt round-trip for both ciphers, replay rejection (duplicate nonce, old nonce, advanced nonce), nonce ordering
- `crypto_test.go` — key pair generation, shared secret derivation (both sides produce same keys), key derivation determinism
- Fuzz: random plaintext sizes through encrypt/decrypt

### Acceptance Criteria

- Both cipher suites encrypt/decrypt correctly
- Replay window rejects duplicates and too-old nonces
- Key derivation produces identical keys on both sides
- No CGO imports

---

## Phase 3 — Connection & Handshake

**Goal:** Establish encrypted connections via the 3-way handshake. Manage connection state, heartbeats, and disconnection.

### Files

- `connection.go` — Connection struct, state machine
- `handshake.go` — handshake packet construction/parsing, handshake flow

### Connection State (connection.go)

```go
type ConnState byte

const (
    StateDisconnected ConnState = iota
    StateConnecting
    StateConnected
    StateDisconnecting
)
```

The `Connection` struct (exported, but fields unexported) holds:
- State
- Remote address (`net.UDPAddr`)
- Send/recv cipher states (from Phase 2)
- Per-channel state (slice of `*channel`, from Phase 4 — stubbed here)
- Send queue (messages waiting for tick flush)
- Fragment ID counter (`atomic.Uint32`, used as uint16)
- RTT stats (Phase 4)
- Congestion state (Phase 7)
- Last send/recv timestamps (for heartbeat/timeout)
- Mutex strategy: one `sync.Mutex` per connection for send queue + state; per-channel locks added in Phase 4

### Handshake Packets (handshake.go)

Each handshake packet uses `FlagControl` and the control type byte. Define serialization for:

**CONNECT (client → server):**
```
[ControlConnect 1B][protocol_version 2B][client_pubkey 32B][cipher_pref 1B][compression_algo 1B][dict_hash_present 1B][dict_hash 32B optional]
```

**VERSION_MISMATCH (server → client):**
```
[ControlVersionMismatch 1B][server_version 2B]
```

**CHALLENGE (server → client):**
```
[ControlChallenge 1B][protocol_version 2B][server_pubkey 32B][challenge_token 32B][selected_cipher 1B][compression_ack 1B]
```

**RESPONSE (client → server):** First encrypted packet.
```
encrypted([ControlResponse 1B][challenge_token 32B])
```

**REJECT (server → client):**
```
[ControlReject 1B][reason 1B]
```

### Handshake Flow

Server side:
1. Receive CONNECT → validate protocol version → generate ephemeral key pair → generate 32-byte random challenge token → derive keys → send CHALLENGE (unencrypted) → store pending connection with `StateConnecting`
2. Receive encrypted packet from same addr → decrypt with derived keys → verify it's a RESPONSE with matching challenge token → transition to `StateConnected` → invoke `OnConnect` callback

Client side:
1. Generate ephemeral key pair → send CONNECT
2. Receive CHALLENGE → validate protocol version → derive keys → encrypt challenge token → send RESPONSE → transition to `StateConnected` on first encrypted data/heartbeat from server

Server does NOT allocate full connection state until RESPONSE is verified — before that, only a lightweight `pendingHandshake` entry exists (key pair + challenge token + timestamp).

### Heartbeat & Timeout

- Heartbeat: if no packet sent to a peer within the heartbeat interval (default 1s), send a heartbeat (empty payload, FlagControl, ControlHeartbeat, on the unreliable channel).
- Timeout: if no packet received from a peer within the timeout window (default 10s), drop the connection and invoke `OnDisconnect`.
- Both are checked during the tick loop (Phase 8), but the logic lives here.

### Disconnect

- Graceful: send `ControlDisconnect` on the reliable unordered channel. Retry up to 3 times with RTO spacing. On receipt, ack and close.
- Timeout: as above.
- Both paths invoke `OnDisconnect` with a reason enum.

### Tests

- `handshake_test.go` — full 3-way handshake in-memory (loopback), version mismatch rejection, cipher negotiation, compression ack (OK and DICT_MISMATCH)
- `connection_test.go` — state transitions, heartbeat timing, timeout detection

### Acceptance Criteria

- Handshake completes and both sides derive identical keys
- Version mismatch is caught before crypto work
- Pending handshakes that never complete are cleaned up (timeout)
- Heartbeats keep connections alive; missing heartbeats trigger timeout

---

## Phase 4 — Channel Layer & Reliability

**Goal:** Implement the four delivery modes with per-channel sequence numbers, ack tracking, and retransmission.

### Files

- `channel.go` — channel struct, delivery mode dispatch, ack building/processing
- `reliability.go` — RTT measurement, retransmission queue, RTO calculation

### Channel Layout (channel.go)

```go
type DeliveryMode byte

const (
    ReliableOrdered    DeliveryMode = iota
    ReliableUnordered
    Unreliable
    UnreliableSequenced
)

type ChannelLayout struct {
    channels []channelDef // indexed by channel ID
}

type channelDef struct {
    Mode        DeliveryMode
    StreamIndex byte
}
```

Provide `DefaultChannelLayout()` (4 channels: one per mode) and a builder for custom layouts. Validate that channel count fits in a byte (max 256).

### Channel State

Each channel (unexported `channel` struct) maintains:
- `sendSeq atomic.Uint32` — next sequence to assign
- `recvAck uint32` — highest received remote sequence
- `recvAckField uint32` — bitfield of received sequences before recvAck
- `mu sync.Mutex` — protects mutable state below
- `pendingSend []pendingPacket` — sent but unacked (reliable channels only)
- `recvBuffer map[uint32][]byte` — reorder buffer (reliable ordered only)
- `recvNextDeliver uint32` — next expected sequence for ordered delivery
- `lastRecvSeq uint32` — for unreliable sequenced drop logic

### Ack Tracking

**On receive:** When a packet arrives on channel C with sequence S:
1. If S > recvAck: shift the bitfield left by (S - recvAck), set the old recvAck position in the bitfield, set recvAck = S.
2. If S < recvAck and S >= recvAck - 32: set the corresponding bit in recvAckField.
3. If S < recvAck - 32: too old, ignore (already implicitly acked or lost).

**On send:** Every outgoing packet on channel C includes the current recvAck and recvAckField for that channel.

**Processing incoming acks:** When we receive ack A and ackField F from the remote for our channel:
- Our packet with sequence A is acked → remove from pendingSend, measure RTT.
- For each set bit N in F: our packet with sequence (A - N - 1) is acked → remove from pendingSend.

### Delivery Mode Behavior on Receive

- **Reliable Ordered:** Buffer the payload at sequence S in recvBuffer. Then deliver all consecutive payloads starting from recvNextDeliver. This handles out-of-order arrival.
- **Reliable Unordered:** Deliver immediately to application callback. Ack tracking still happens for retransmission.
- **Unreliable:** Deliver immediately. No ack tracking needed on send side. Ack/AckField in outgoing packets are still populated (they reflect what *we* received).
- **Unreliable Sequenced:** If S > lastRecvSeq, deliver and update lastRecvSeq. Otherwise drop (stale).

### RTT Measurement (reliability.go)

```go
type rttState struct {
    srtt    float64 // smoothed RTT in seconds
    rttvar  float64 // RTT variance
    rto     float64 // retransmission timeout
    mu      sync.Mutex
}
```

- On first sample: `srtt = sample`, `rttvar = sample / 2`.
- Subsequent: `rttvar = (1 - 0.25) * rttvar + 0.25 * |srtt - sample|`, `srtt = (1 - 0.125) * srtt + 0.125 * sample`.
- `rto = srtt + 4 * rttvar`, clamped to `[rtoMin, rtoMax]` (defaults: 50ms, 5s).
- Only measure on first transmissions (Karn's algorithm — skip retransmitted packets).

### Retransmission

Each `pendingPacket` in the retransmission queue records:
- Serialized packet bytes (pre-encryption — will be re-encrypted with a new nonce on retransmit)
- Send timestamp (for RTT, only valid on first transmit)
- Retransmit count
- Next retransmit time

During tick (Phase 8), iterate pending queues. If `now >= nextRetransmitTime`:
- Re-encrypt and resend.
- Increment retransmit count. If count exceeds max retries (default 15), kill the connection.
- Set next retransmit time = now + rto.

### Tests

- `channel_test.go` — ack tracking (advance, bitfield, old packet), reliable ordered reordering (deliver in order despite out-of-order arrival), unreliable sequenced drop logic, reliable unordered immediate delivery
- `reliability_test.go` — RTT calculation convergence, RTO bounds, retransmission scheduling

### Acceptance Criteria

- Reliable ordered delivers messages in sequence even when received out of order
- Unreliable sequenced drops stale packets
- Ack bitfield correctly reflects received packet history
- RTT converges to stable values under simulated latency
- Max retransmit exceeded kills the connection

---

## Phase 5 — Fragmentation

**Goal:** Split oversized messages into fragments on send, reassemble on receive.

### Files

- `fragment.go` — extend with splitting and reassembly logic

### Send-Side Splitting

When a message (post-compression, if applicable) exceeds `MTU - wireOverhead - headerSize - fragmentHeaderSize`:

1. Allocate next `fragmentID` (per-connection uint16, wrapping).
2. Compute `fragmentCount = ceil(len(payload) / maxFragmentPayload)`. Cap at 255 (if exceeded, return error — message too large).
3. For each chunk: construct `FragmentHeader` with the shared fragment flags, wrap in a packet with `FlagFragment` set, assign a sequence number on the target channel.
4. For unreliable channels: log a warning — fragmented unreliable messages are usually a design issue.

Constants:
- `DefaultMTU = 1200`
- `WireOverhead = 24` (8 nonce + 16 tag)
- `MaxHeaderSize = 14` (1 flags + 1 channel + 5 seq + 5 ack + 4 ackfield, worst case VarInt)
- `FragmentHeaderSize = 5`
- `MaxFragmentPayload = DefaultMTU - WireOverhead - MaxHeaderSize - FragmentHeaderSize` (~1157 bytes)

### Receive-Side Reassembly

```go
type reassemblyBuffer struct {
    fragments  [][]byte // indexed by fragment index
    received   int      // count of received fragments
    count      int      // expected total (from header)
    flags      byte     // fragment flags (compression info)
    createdAt  time.Time
}
```

Per-connection map: `map[uint16]*reassemblyBuffer` (keyed by fragment ID).

On receiving a fragment:
1. Look up or create the reassembly buffer.
2. If creating: set count and flags from the fragment header.
3. If buffer already exists and count/flags don't match: discard (corrupted).
4. Store fragment payload at the index. If slot already occupied, skip (duplicate fragment).
5. If `received == count`: concatenate in index order, deliver completed message, delete buffer.

### Stale Buffer Cleanup

During tick (Phase 8), iterate reassembly buffers. Delete any with `time.Since(createdAt) > fragmentTimeout` (default 5s). For reliable channels, individual fragments are retransmitted so stale buffers indicate something is very wrong. For unreliable channels, stale buffers are expected (lost fragments).

### Single-Fragment Compressed Envelope

When compression is active but the compressed message fits in one packet, still set `FlagFragment` with `FragmentCount = 1, FragmentIndex = 0`. This reuses the fragment header to carry the compression flags. Cost: 5 bytes overhead. The reassembly path handles `count == 1` naturally (immediate completion).

### Tests

- `fragment_test.go` — split and reassemble round-trip, out-of-order fragment arrival, duplicate fragment handling, stale buffer cleanup, single-fragment envelope, max fragment count exceeded error, fragment count mismatch detection

### Acceptance Criteria

- Messages up to 64 KB round-trip through fragment/reassemble
- Out-of-order fragments reassemble correctly
- Stale incomplete buffers are cleaned up
- Single-fragment compressed messages work

---

## Phase 6 — Compression

**Goal:** Compress messages before fragmentation using LZ4 or zstd. Dictionary support for zstd.

### Dependencies

```
github.com/pierrec/lz4/v4
github.com/klauspost/compress/zstd
```

### Files

- `compress.go` — compressor interface, LZ4/zstd implementations, hurdle point

### Design

```go
type Compressor interface {
    Compress(dst, src []byte) ([]byte, error)
    Decompress(dst, src []byte) ([]byte, error)
    Algorithm() CompressionAlgorithm
}

type CompressionAlgorithm byte

const (
    CompressionNone CompressionAlgorithm = iota
    CompressionLZ4
    CompressionZstd
)
```

Configuration:

```go
type CompressionConfig struct {
    Algorithm  CompressionAlgorithm
    Hurdle     uint32 // minimum payload size to attempt compression (default: 128)
    ZstdLevel  int    // zstd compression level (default: 1, range: 1-19)
    Dictionary []byte // zstd dictionary (nil = none)
}
```

### Compress Flow (send side)

1. If `Algorithm == None` or `len(payload) < Hurdle`: skip, return payload unchanged, fragment flags = 0.
2. Compress payload.
3. If `len(compressed) >= len(payload)`: skip (compression unhelpful), return original, fragment flags = 0.
4. Return compressed payload with fragment flags set: `FragFlagCompressed | (FragFlagZstd if zstd)`.

### Decompress Flow (receive side)

After reassembly, check fragment flags from any fragment in the group:
1. If `FragFlagCompressed` not set: deliver as-is.
2. If `FragFlagCompressed` set: decompress using the algorithm indicated by `FragFlagZstd`.

### Dictionary Support (zstd)

- Load dictionary bytes into the zstd encoder/decoder at initialization.
- Compute SHA-256 hash of dictionary for handshake verification.
- During handshake: client sends dict_hash if dictionary is configured. Server compares. On mismatch: `compression_ack = DICT_MISMATCH` in CHALLENGE → client treats as fatal error.

### Thread Safety

LZ4 and zstd encoder/decoder instances are NOT goroutine-safe. Options:
- **Per-worker instances**: each worker in the pool holds its own encoder/decoder. Avoids synchronization entirely. Preferred approach.
- Alternative: `sync.Pool` of encoders/decoders.

### Tests

- `compress_test.go` — LZ4 round-trip, zstd round-trip, zstd with dictionary, hurdle point (skip small payloads), compression-expansion check (incompressible data returns original), fragment flags correctness, dictionary hash computation

### Acceptance Criteria

- Both algorithms compress/decompress correctly
- Hurdle point prevents compressing small payloads
- Incompressible data is sent uncompressed (no expansion)
- Dictionary mismatch is detected during handshake
- Thread-safe usage from multiple workers

---

## Phase 7 — Congestion Control

**Goal:** Gate send rate to avoid overwhelming the network.

### Files

- `congestion.go` — congestion controller interface + two implementations

### Design

```go
type CongestionController interface {
    OnAck(ackedPackets int)
    OnLoss()
    CanSend(inFlight int) bool
    OnDuplicateAck() // for aggressive fast retransmit
}
```

### Conservative Mode (AIMD)

```go
type conservativeCC struct {
    cwnd    float64 // congestion window in packets
    mu      sync.Mutex
}
```

- Initial cwnd: configurable (default: 4).
- On ack: `cwnd += 1.0 / cwnd` (additive increase, ~1 packet per RTT).
- On loss: `cwnd = max(cwnd * 0.5, 2.0)` (multiplicative decrease, floor of 2).
- `CanSend`: return `inFlight < int(cwnd)`.

### Aggressive Mode

```go
type aggressiveCC struct{}
```

- `CanSend` always returns true (no window gating).
- `OnDuplicateAck`: after 2 duplicate acks, trigger fast retransmit (signal to reliability layer).
- RTO halved on each retransmit instead of doubled (handled in reliability layer, controlled by a flag from the congestion controller).

Provide a method: `HalvesRTO() bool` — returns true for aggressive, false for conservative.

### Integration

The congestion controller is per-connection. The tick loop checks `CanSend(inFlight)` before flushing a packet from the send queue. `inFlight` is the count of sent-but-unacked reliable packets across all channels.

### Tests

- `congestion_test.go` — AIMD window growth/shrink, floor enforcement, aggressive mode always allows send, fast retransmit trigger count

### Acceptance Criteria

- Conservative mode converges to stable throughput under simulated loss
- Window never drops below floor
- Aggressive mode never blocks sends

---

## Phase 8 — Server & Client Architecture

**Goal:** Wire everything together into working Server and Client types.

**Important:** See `progress/design-tick-modes.md` for the dual-mode tick system design (TickAuto / TickDriven). The tick goroutine is only spawned in autonomous mode. In driven mode the application calls `Tick()` manually. This affects Server, Client, and their lifecycle methods.

### Files

- `server.go` — Server struct, lifecycle
- `client.go` — Client struct, lifecycle

### Server Architecture

```go
type Server struct {
    config     ServerConfig
    conn       *net.UDPConn
    handler    Handler           // callback interface
    conns      *connectionTable  // sharded map[netip.AddrPort]*Connection
    pending    *pendingHandshakes
    jobQueue   chan job
    pool       []*worker
    tickDone   chan struct{}
    closeCh    chan struct{}
}
```

**Goroutines:**

1. **Read goroutine** (1): `ReadFromUDPAddrPort()` in a loop. Look up connection by address. If found, push `recvJob{conn, data}` into job queue. If not found and data looks like CONNECT, push `handshakeJob{addr, data}`.

2. **Worker pool** (N, default: `runtime.NumCPU()`): Pull jobs from queue. Two job types:
   - `recvJob`: decrypt → check if fragment → if fragment: store in reassembly buffer, check completion → if complete or non-fragment: decompress if flagged → process acks (update channel state, remove from pending queue, measure RTT) → dispatch by delivery mode → call `OnMessage` if delivering.
   - `sendJob`: serialize header → compress if enabled → fragment if needed → encrypt each packet → write to UDP socket.

3. **Tick goroutine** (1): `time.Ticker` at configured rate (default: 100 ticks/s). Each tick, iterate all connections:
   - Flush send queue: dequeue messages, create `sendJob`s, submit to worker pool. Respect congestion window (`CanSend`). Batch small messages into one datagram where possible (each still individually encrypted).
   - Check retransmission timers on all reliable channels.
   - Send heartbeats if needed.
   - Check connection timeouts.
   - Clean up stale reassembly buffers.

**Send batching (tick-flush only):** Multiple small encrypted packets can be concatenated into a single UDP write (up to MTU). Since each is independently encrypted, the receiver decrypts each by parsing nonce boundaries (each encrypted packet starts with 8-byte nonce). The receiver reads the datagram and splits by trying to decrypt at offset 0, then at offset 0 + len(first encrypted packet), etc.

Actually, simpler approach: each `sendJob` produces one or more encrypted packets. The tick goroutine collects them and writes as many as fit into a single `WriteTo` call. Each encrypted packet is self-delimiting (the receiver knows the ciphertext length from `total - 8 nonce - 16 tag` if it's a single packet, but with batching this breaks).

**Revised batching approach:** Don't batch at the UDP level. Each encrypted packet is one UDP datagram. This is simpler and avoids parsing ambiguity. Batching optimization (multiple packets per datagram) is deferred alongside multi-packet coalescing. The tick loop still batches *work* (processes all pending messages per connection per tick), just writes them as separate datagrams.

**Connection table:** Sharded by address hash for reduced lock contention. `sync.RWMutex` per shard.

```go
type connectionTable struct {
    shards    [64]connShard
}

type connShard struct {
    mu    sync.RWMutex
    conns map[netip.AddrPort]*Connection
}
```

### Client Architecture

```go
type Client struct {
    config   ClientConfig
    conn     *net.UDPConn
    handler  Handler
    server   *Connection     // single connection to the server
    closeCh  chan struct{}
}
```

**Goroutines:**

1. **Read goroutine** (1): `ReadFromUDP()` in a loop. All packets go to the single server connection. Process inline (no worker pool needed — single connection means no contention benefit from pooling). Decrypt → defragment → decompress → process acks → deliver.

2. **Tick goroutine** (1): Same as server tick but for one connection only. Flush send queue, retransmit, heartbeat, timeout check.

No worker pool — the client has one connection, so the read goroutine processes everything directly.

### Lifecycle

**Server:**
- `NewServer(addr string, config ServerConfig, handler Handler) (*Server, error)` — bind socket, don't start yet.
- `(*Server) Start() error` — launch read goroutine, worker pool, tick goroutine.
- `(*Server) Stop() error` — signal close, send disconnect to all connections, drain workers, close socket.

**Client:**
- `NewClient(config ClientConfig, handler Handler) (*Client, error)` — create socket, don't connect yet.
- `(*Client) Connect(addr string) error` — resolve address, initiate handshake, block until connected or timeout.
- `(*Client) Close() error` — send disconnect, wait for ack or timeout, close socket.

### Tests

- `server_test.go` — start/stop lifecycle, accept connection, reject when full, concurrent connections
- `client_test.go` — connect/disconnect lifecycle, reconnect after disconnect
- Integration: client ↔ server handshake over loopback UDP, send/receive messages across all delivery modes

### Acceptance Criteria

- Server handles concurrent connections
- Client connects and exchanges messages with server
- Graceful shutdown drains connections
- Tick loop runs at configured rate (within tolerance)

---

## Phase 9 — API Surface, Callbacks & Polish

**Goal:** Clean public API, documentation, and ergonomic usage patterns.

### Files

- `callback.go` — Handler interface
- `stats.go` — per-connection stats
- Refine all exported types across existing files

### Handler Interface (callback.go)

```go
type Handler interface {
    OnConnect(conn *Connection)
    OnDisconnect(conn *Connection, reason DisconnectReason)
    OnMessage(conn *Connection, data []byte, channel byte)
    OnError(conn *Connection, err error)
}
```

Provide a `BaseHandler` with no-op implementations for embedding:

```go
type BaseHandler struct{}
func (BaseHandler) OnConnect(*Connection)                          {}
func (BaseHandler) OnDisconnect(*Connection, DisconnectReason)     {}
func (BaseHandler) OnMessage(*Connection, []byte, byte)            {}
func (BaseHandler) OnError(*Connection, error)                     {}
```

### Connection API

```go
func (c *Connection) Send(data []byte, channel byte) error
func (c *Connection) SendImmediate(data []byte, channel byte) error
func (c *Connection) Close() error
func (c *Connection) RemoteAddr() netip.AddrPort
func (c *Connection) Stats() ConnectionStats
func (c *Connection) RTT() time.Duration
```

`Send` queues for the next tick flush. `SendImmediate` bypasses the queue and writes directly (still goes through compress → fragment → encrypt).

### Connection Stats (stats.go)

```go
type ConnectionStats struct {
    RTT           time.Duration
    RTTVariance   time.Duration
    PacketLoss    float64       // 0.0–1.0, over sliding window
    BytesSent     uint64
    BytesReceived uint64
    CongestionWindow int
    Uptime        time.Duration
}
```

### DisconnectReason

```go
type DisconnectReason byte

const (
    DisconnectGraceful  DisconnectReason = iota
    DisconnectTimeout
    DisconnectError
    DisconnectRejected
    DisconnectKicked    // server-initiated
)
```

### Configuration Finalization

Finalize `ServerConfig` and `ClientConfig` with all options and sensible defaults:

```go
type ServerConfig struct {
    MTU              int                // default: 1200
    MaxConnections   int                // default: 1024
    TickRate         int                // default: 100 (ticks/sec)
    WorkerCount      int                // default: runtime.NumCPU()
    HeartbeatInterval time.Duration     // default: 1s
    ConnTimeout      time.Duration      // default: 10s
    ChannelLayout    ChannelLayout      // default: DefaultChannelLayout()
    Compression      CompressionConfig  // default: none
    Congestion       CongestionMode     // default: Conservative
    CipherPreference CipherSuite       // default: AES-128-GCM
    MaxRetransmits   int                // default: 15
    RTOMin           time.Duration      // default: 50ms
    RTOMax           time.Duration      // default: 5s
    FragmentTimeout  time.Duration      // default: 5s
    InitialCwnd      int                // default: 4
}

type ClientConfig struct {
    MTU              int
    TickRate         int
    HeartbeatInterval time.Duration
    ConnTimeout      time.Duration
    ConnectTimeout   time.Duration      // handshake timeout, default: 5s
    ChannelLayout    ChannelLayout
    Compression      CompressionConfig
    Congestion       CongestionMode
    CipherPreference CipherSuite
    MaxRetransmits   int
    RTOMin           time.Duration
    RTOMax           time.Duration
    FragmentTimeout  time.Duration
    InitialCwnd      int
}
```

Provide `DefaultServerConfig()` and `DefaultClientConfig()` constructors.

### Tests

- `callback_test.go` — verify all callbacks fire at the right times (connect, message, disconnect, error)
- `stats_test.go` — verify stats update correctly during traffic

### Acceptance Criteria

- API is clean and intuitive (no stuttering like `fastwire.FastWireServer`)
- All configuration has sensible defaults — zero-config usage works
- BaseHandler allows partial implementation
- Stats are accurate under load

---

## Phase 10 — Testing, Benchmarks & Documentation

**Goal:** Comprehensive test coverage, performance validation, and user-facing documentation.

### Testing Strategy

**Unit tests (per file):** Already covered in each phase. Ensure ≥80% line coverage.

**Integration tests:**
- Full client ↔ server exchange over loopback for each delivery mode
- Fragmented message delivery (reliable + unreliable)
- Compressed + fragmented message delivery
- Connection timeout and reconnect
- Multiple concurrent clients
- Graceful shutdown under load

**Stress tests:**
- 100+ concurrent connections sending at 60 pps
- Large messages (64 KB) fragmented and reassembled
- Packet loss simulation (drop random % of UDP writes) — verify reliable delivery still works

**Fuzz tests:**
- VarInt parser
- Packet header parser
- Fragment header parser
- Decrypt with random data (must not panic)

### Benchmarks

```go
BenchmarkVarIntEncode
BenchmarkVarIntDecode
BenchmarkPacketMarshal
BenchmarkPacketUnmarshal
BenchmarkEncryptAES
BenchmarkEncryptChaCha
BenchmarkDecryptAES
BenchmarkDecryptChaCha
BenchmarkCompressLZ4
BenchmarkCompressZstd
BenchmarkFragmentSplit
BenchmarkFragmentReassemble
BenchmarkFullSendPath        // serialize → compress → fragment → encrypt
BenchmarkFullRecvPath        // decrypt → reassemble → decompress → deliver
BenchmarkServerThroughput    // end-to-end with N clients at target pps
```

### Documentation

**DOCS.md:** Comprehensive usage guide covering:
- Quick start (minimal server + client)
- Configuration options and defaults
- Delivery modes explained with use cases
- Channel layouts (default + custom)
- Compression setup (LZ4, zstd, dictionaries)
- Encryption (automatic, cipher selection)
- Connection lifecycle and callbacks
- Performance tuning (tick rate, worker count, congestion mode, MTU)
- Thread safety guarantees

**EXAMPLES.md:** Focused code examples:
- Echo server/client
- Game server with multiple channel types
- Using compression with zstd dictionary
- Custom channel layout
- Connection stats monitoring

### Acceptance Criteria

- All tests pass: `go test ./...`
- Lint clean: `golangci-lint run`
- Benchmarks establish baseline performance numbers
- Fuzz tests run 60s with no crashes
- DOCS.md and EXAMPLES.md are complete and accurate

---

## Decision Log

| Decision | Rationale |
|----------|-----------|
| Individual encryption per packet (no coalescing) | Simpler parsing, each packet independently verifiable. Coalescing deferred. |
| Stateful handshake (no stateless cookies) | Simpler implementation. Stateless cookies deferred. |
| No client worker pool | Single connection doesn't benefit from pooling; read goroutine processes inline. |
| One UDP datagram per encrypted packet | Avoids ambiguous packet boundary parsing. Batching multiple encrypted packets into one datagram deferred. |
| Per-worker compressor instances | Avoids sync overhead. Each worker holds its own LZ4/zstd encoder/decoder. |
| Sharded connection table | Reduces lock contention vs. single sync.Map for high connection counts. |
| Pre-encryption retransmit storage | Retransmitted packets are re-encrypted with fresh nonces, preventing nonce reuse. |
| `pierrec/lz4` + `klauspost/compress/zstd` | Best pure-Go options. No CGO. Both well-maintained. |
