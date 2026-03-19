# FastWire Documentation

FastWire is an enhanced UDP networking library for Go, designed for fast-paced, low-latency applications such as gaming.

## Wire Primitives

FastWire uses a compact binary encoding for all on-the-wire data. These primitives are the building blocks for packet headers, messages, and control data.

### VarInt (uint32)

Variable-length unsigned integer using LEB128 encoding. Values 0–127 take 1 byte; larger values take up to 5 bytes.

```go
buf := make([]byte, 5)
n := fastwire.PutVarInt(buf, 300)
value, bytesRead, err := fastwire.ReadVarInt(buf[:n])
```

### VarLong (uint64)

Same encoding as VarInt but extended to 64 bits (max 10 bytes).

```go
buf := make([]byte, 10)
n := fastwire.PutVarLong(buf, 1_000_000_000_000)
value, bytesRead, err := fastwire.ReadVarLong(buf[:n])
```

### String

Length-prefixed UTF-8 string. The length is a signed int16 in big-endian (max 32767 bytes).

```go
buf := make([]byte, 2+len(s))
n, err := fastwire.PutString(buf, "hello")
s, bytesRead, err := fastwire.ReadString(buf[:n])
```

### UUID

128-bit identifier stored as two big-endian uint64s (16 bytes on the wire).

```go
id := fastwire.UUIDFromInts(msbValue, lsbValue)
fmt.Println(id.String()) // "01234567-89ab-cdef-fedc-ba9876543210"
fmt.Println(id.MSB(), id.LSB())

buf := make([]byte, 16)
fastwire.PutUUID(buf, id)
decoded, _, err := fastwire.ReadUUID(buf)
```

## Packet Header

Every FastWire packet starts with a header containing flags, channel ID, sequence number, acknowledgment info, and an ack bitfield.

```go
header := fastwire.PacketHeader{
    Flags:    fastwire.FlagFragment,
    Channel:  0,
    Sequence: 42,
    Ack:      40,
    AckField: 0x00000003, // acks for sequences 39 and 38
}

buf := make([]byte, 16) // max header size
n, err := fastwire.MarshalHeader(buf, &header)

parsed, bytesRead, err := fastwire.UnmarshalHeader(buf[:n])
```

### Flags

| Flag | Meaning |
|------|---------|
| `FlagFragment` | Payload is a fragment of a larger message |
| `FlagControl` | Connection control packet (handshake, heartbeat, disconnect) |

### Control Types

Control packets (with `FlagControl` set) use a control type byte as the first byte of the payload:

| Type | Value | Direction |
|------|-------|-----------|
| `ControlConnect` | 0x01 | Client → Server |
| `ControlChallenge` | 0x02 | Server → Client |
| `ControlResponse` | 0x03 | Client → Server |
| `ControlConnected` | 0x04 | Server → Client |
| `ControlDisconnect` | 0x05 | Either |
| `ControlHeartbeat` | 0x06 | Either |
| `ControlVersionMismatch` | 0x07 | Server → Client |
| `ControlReject` | 0x08 | Server → Client |

## Fragment Header

When a message exceeds the MTU, it is split into fragments. Each fragment carries a 5-byte header.

```go
fh := fastwire.FragmentHeader{
    FragmentID:    1,
    FragmentIndex: 0,
    FragmentCount: 3,
    FragmentFlags: fastwire.FragFlagCompressed,
}

buf := make([]byte, fastwire.FragmentHeaderSize)
n, err := fastwire.MarshalFragmentHeader(buf, &fh)

parsed, bytesRead, err := fastwire.UnmarshalFragmentHeader(buf)
```

### Fragment Flags

| Flag | Meaning |
|------|---------|
| `FragFlagCompressed` | Fragment payload is compressed |
| `FragFlagZstd` | Compression algorithm is zstd (otherwise LZ4) |

## Configuration Types

### Delivery Modes

| Mode | Behavior |
|------|----------|
| `ReliableOrdered` | Messages delivered in order, retransmitted if lost |
| `ReliableUnordered` | Messages delivered immediately, retransmitted if lost |
| `Unreliable` | No delivery guarantee |
| `UnreliableSequenced` | Only the latest message is delivered, stale ones are dropped |

### Cipher Suites

| Suite | Description |
|-------|-------------|
| `CipherNone` | No encryption (plaintext) |
| `CipherAES128GCM` | AES-128-GCM (16-byte key) |
| `CipherChaCha20Poly1305` | ChaCha20-Poly1305 (32-byte key) |

### Compression Algorithms

| Algorithm | Description |
|-----------|-------------|
| `CompressionNone` | No compression |
| `CompressionLZ4` | LZ4 compression |
| `CompressionZstd` | Zstandard compression (supports dictionaries) |

## Encryption

FastWire provides per-packet AEAD encryption with replay protection. Encryption is optional — set the cipher suite to `CipherNone` to disable it.

### Wire Format

Each encrypted packet has the format: `[nonce 8B][ciphertext][tag 16B]`, adding 24 bytes of overhead per packet (`WireOverhead`).

With `CipherNone`, packets are sent as plaintext with no overhead.

### Cipher Suites

- **AES-128-GCM** (`CipherAES128GCM`) — fast on hardware with AES-NI, 16-byte key
- **ChaCha20-Poly1305** (`CipherChaCha20Poly1305`) — fast in software, 32-byte key
- **None** (`CipherNone`) — no encryption, packets sent as plaintext

### Replay Protection

A sliding window of 1024 packets prevents replay attacks. Packets with duplicate or too-old nonces are rejected with `ErrReplayedPacket`. The window is checked before decryption and updated only after successful decryption, preventing window poisoning from forged packets.

### Key Exchange

FastWire uses X25519 ECDH for key exchange during the handshake. Both sides derive symmetric keys using HKDF-SHA256 with the challenge token as salt. Two keys are derived:
- `c2s` — client-to-server direction
- `s2c` — server-to-client direction

```go
// Generate a key pair for the handshake.
kp, err := fastwire.GenerateKeyPair()

// After exchanging public keys, derive symmetric keys.
c2sKey, s2cKey, err := fastwire.DeriveKeys(
    myKeyPair.Private,
    theirPublicKey,
    challengeToken,
    fastwire.CipherAES128GCM,
)
```

## Connection & Handshake

FastWire establishes connections via a 3-way handshake with X25519 key exchange.

### Handshake Flow

1. **Client → Server: CONNECT** — sends protocol version, public key, cipher preference, compression preference
2. **Server → Client: CHALLENGE** — sends server public key, challenge token, selected cipher, compression ack
3. **Client → Server: RESPONSE** — encrypted packet echoing the challenge token (proves key derivation worked)

After the RESPONSE is verified, the connection is established and bidirectional encrypted communication begins.

If the protocol versions don't match, the server sends a `VERSION_MISMATCH` packet instead of a CHALLENGE. If the server is full or rejects the client for another reason, it sends a `REJECT` packet.

### Connection State

Connections progress through a state machine:

| State | Description |
|-------|-------------|
| `StateDisconnected` | Connection is not active |
| `StateConnecting` | Handshake in progress |
| `StateConnected` | Connection established and active |
| `StateDisconnecting` | Graceful disconnect in progress |

### Heartbeat & Timeout

- **Heartbeat**: if no packet is sent within the heartbeat interval (default 1s), a heartbeat control packet is sent automatically
- **Timeout**: if no packet is received within the timeout window (default 10s), the connection is dropped

### Disconnect

- **Graceful**: a `ControlDisconnect` packet is sent and the connection transitions to `StateDisconnecting`. The disconnect packet is retried up to 3 times with RTO spacing to ensure reliable delivery. The tick loop handles retries and final cleanup — `Close()` returns immediately.
- **Timeout**: detected when no packets are received within the timeout window

### Disconnect Reasons

| Reason | Description |
|--------|-------------|
| `DisconnectGraceful` | Clean shutdown initiated by either side |
| `DisconnectTimeout` | No packets received within timeout window |
| `DisconnectError` | Connection closed due to an error |
| `DisconnectRejected` | Server rejected the connection |
| `DisconnectKicked` | Server kicked the client |

## Channels & Reliability

FastWire uses a channel-based architecture where each channel has its own delivery mode, sequence numbers, and ack tracking. This allows mixing reliability guarantees within the same connection.

### Channel Layout

A `ChannelLayout` defines the set of channels available on a connection. The default layout provides one channel for each delivery mode:

| Channel | Mode | Use case |
|---------|------|----------|
| 0 | `ReliableOrdered` | Game state, chat messages |
| 1 | `ReliableUnordered` | Events that must arrive but order doesn't matter |
| 2 | `Unreliable` | Voice data, fire-and-forget telemetry |
| 3 | `UnreliableSequenced` | Player position updates (only latest matters) |

```go
// Use the default layout (4 channels, one per delivery mode).
layout := fastwire.DefaultChannelLayout()
layout.Len() // 4
```

### Custom Channel Layouts

Use `ChannelLayoutBuilder` to create custom layouts with 1–256 channels:

```go
layout, err := fastwire.NewChannelLayoutBuilder().
    AddChannel(fastwire.ReliableOrdered, 0).   // channel 0: game state
    AddChannel(fastwire.ReliableOrdered, 1).   // channel 1: chat (separate stream)
    AddChannel(fastwire.Unreliable, 0).        // channel 2: voice
    AddChannel(fastwire.UnreliableSequenced, 0). // channel 3: position updates
    Build()
```

The `streamIndex` parameter allows grouping channels for future stream-level features. For most use cases, set it to 0.

### Delivery Modes in Detail

**ReliableOrdered** — Messages are buffered and delivered in sequence order. If packet 3 arrives before packet 2, packet 3 is held until packet 2 arrives. Lost packets are retransmitted.

**ReliableUnordered** — Messages are delivered immediately on arrival. Lost packets are retransmitted, but there is no reorder buffering.

**Unreliable** — Messages are delivered immediately with no retransmission. Ack/ackField fields in outgoing headers still reflect what was received (used for piggybacked acknowledgments).

**UnreliableSequenced** — Only the most recent message is delivered. If a packet with a lower sequence number arrives after a higher one, it is silently dropped. No retransmission.

### Ack Tracking

Each channel maintains a 32-bit ack bitfield alongside the highest received sequence number. Every outgoing packet piggybacks this ack state, allowing the remote side to confirm delivery without dedicated ack packets.

### RTT & Retransmission

FastWire measures round-trip time using the Jacobson/Karels algorithm (RFC 6298). Karn's algorithm is applied: RTT is only measured from original transmissions, not retransmissions.

Retransmission timeout (RTO) is clamped to [50ms, 5s]. After 15 failed retransmission attempts on any packet, the connection is terminated.

## Fragmentation

FastWire automatically splits messages that exceed the MTU into fragments and reassembles them on the receiving side. This is transparent to the application — you send a message of any size (up to the maximum) and the receiver gets the complete message back.

### How it works

- **Send side**: Messages larger than `MaxFragmentPayload` (1155 bytes) are split into up to 255 fragments. Each fragment is sent as an independent packet with `FlagFragment` set and a `FragmentHeader` prepended to the payload.
- **Receive side**: Fragments are collected in a per-connection reassembly store keyed by fragment ID. Once all fragments of a message arrive (in any order), they are concatenated and delivered as the complete message.

### Limits

| Constant | Value | Description |
|----------|-------|-------------|
| `MaxFragmentPayload` | 1155 bytes | Maximum payload per fragment |
| `MaxFragments` | 255 | Maximum fragments per message |
| `DefaultFragmentTimeout` | 5s | Incomplete reassembly buffer expiry |

The maximum message size is `MaxFragmentPayload * MaxFragments` = ~294 KB. Attempting to send a larger message returns `ErrMessageTooLarge`.

### Fragment header

Each fragment carries a 5-byte header (`FragmentHeaderSize`):

| Field | Size | Description |
|-------|------|-------------|
| FragmentID | 2 bytes | Identifies which message this fragment belongs to |
| FragmentIndex | 1 byte | Position of this fragment (0-based) |
| FragmentCount | 1 byte | Total number of fragments in the message |
| FragmentFlags | 1 byte | Compression flags (used by the compression layer) |

### Reliability

On reliable channels, each fragment gets its own sequence number and is retransmitted independently if lost. On unreliable channels, losing any fragment means the entire message is lost (incomplete reassembly buffers are cleaned up after `DefaultFragmentTimeout`).

## Compression

FastWire supports optional per-connection compression using LZ4 or zstd. Compression sits between the application payload and the fragmentation layer: **compress -> fragment -> encrypt** on send, **decrypt -> reassemble -> decompress** on receive.

### Algorithms

| Algorithm | Description |
|-----------|-------------|
| `CompressionNone` | No compression (default) |
| `CompressionLZ4` | LZ4 block compression — fast, low CPU overhead |
| `CompressionZstd` | Zstandard compression — better ratio, supports dictionaries |

### Configuration

```go
config := fastwire.CompressionConfig{
    Algorithm:  fastwire.CompressionLZ4,
    Hurdle:     128,  // minimum payload size to attempt compression (bytes)
    ZstdLevel:  0,    // zstd compression level (0 = default, only used with zstd)
    Dictionary: nil,  // optional zstd dictionary (only used with zstd)
}
```

### Hurdle Point

Payloads smaller than `Hurdle` bytes (default `DefaultCompressionHurdle` = 128) are sent uncompressed. This avoids wasting CPU on tiny payloads where compression overhead exceeds the savings.

### Expansion Check

If compression produces output that is equal to or larger than the original payload (e.g. for random or already-compressed data), FastWire automatically sends the original uncompressed data. This is transparent — the receiver sees no difference.

### Dictionary Support (Zstd)

Zstd supports pre-shared dictionaries for better compression of small, repetitive payloads (e.g. game state updates). Both client and server must use the same dictionary. During the handshake, dictionary hashes (SHA-256) are compared; a mismatch is reported via the compression ack.

```go
dict, _ := os.ReadFile("game_dict.zstd")
config := fastwire.CompressionConfig{
    Algorithm:  fastwire.CompressionZstd,
    Dictionary: dict,
}

// Compute a dictionary hash for verification.
hash := fastwire.DictionaryHash(dict)
```

### Decompression Bomb Protection

Decompressed payloads are limited to `MaxDecompressedSize` (4 MB). Payloads that would exceed this limit are rejected with `ErrDecompressionFailed`.

### Constants

| Constant | Value | Description |
|----------|-------|-------------|
| `DefaultCompressionHurdle` | 128 bytes | Minimum payload size to attempt compression |
| `MaxDecompressedSize` | 4 MB | Maximum decompressed payload size |

### Thread Safety

All compression operations are thread-safe. LZ4 hash tables and zstd encoder/decoder instances are pooled using `sync.Pool` for efficient concurrent use.

## Congestion Control

FastWire provides two congestion control modes that govern how aggressively packets are sent. The mode is configured per connection.

### Modes

| Mode | Description |
|------|-------------|
| `CongestionConservative` | AIMD (Additive Increase Multiplicative Decrease) — standard TCP-like window. Reduces send rate on loss, grows on successful acks. Good for general-purpose or bandwidth-constrained links. |
| `CongestionAggressive` | No window gating — sends as fast as possible. Uses fast retransmit (after 2 duplicate acks) instead of waiting for RTO. Halves RTO on retransmit instead of doubling. Ideal for real-time gaming where latency matters more than bandwidth fairness. |

### Conservative (AIMD)

The conservative controller maintains a congestion window (`cwnd`) that limits the number of in-flight reliable packets.

- **Initial window**: configurable (default `DefaultInitialCwnd` = 4 packets)
- **On ack**: `cwnd += ackedPackets / cwnd` (additive increase — roughly 1 packet per RTT)
- **On loss**: `cwnd = max(cwnd * 0.5, 2.0)` (multiplicative decrease, floor of 2 packets)
- **Send gating**: a packet is only sent if `inFlight < cwnd`

### Aggressive

The aggressive controller never blocks sends and instead relies on fast retransmit for loss recovery.

- **Send gating**: always allows sending (no window)
- **Fast retransmit**: after 2 duplicate acks, triggers immediate retransmission
- **RTO behavior**: halves RTO on retransmit instead of doubling (faster recovery)
- **Loss handling**: no-op (no send rate reduction)

### Constants

| Constant | Value | Description |
|----------|-------|-------------|
| `DefaultInitialCwnd` | 4 | Default initial congestion window (packets) |

## Server

The `Server` type listens for incoming connections over UDP. It manages a sharded connection table, handles the 3-way handshake, and runs the tick loop.

### Creating and Starting

```go
config := fastwire.DefaultServerConfig()
config.MaxConnections = 100

srv, err := fastwire.NewServer(":7777", config, myHandler)
if err != nil {
    log.Fatal(err)
}
if err := srv.Start(); err != nil {
    log.Fatal(err)
}
defer srv.Stop()
```

### Server Configuration

| Field | Default | Description |
|-------|---------|-------------|
| `MTU` | 1200 | Maximum transmission unit |
| `MaxConnections` | 1024 | Maximum concurrent connections |
| `TickRate` | 100 | Ticks per second (TickAuto only) |
| `TickMode` | `TickAuto` | `TickAuto` or `TickDriven` |
| `HeartbeatInterval` | 1s | Time between heartbeat packets |
| `ConnTimeout` | 10s | Connection timeout for inactivity |
| `HandshakeTimeout` | 5s | Pending handshake expiry |
| `ChannelLayout` | Default (4 channels) | Channel configuration |
| `Compression` | None | Compression settings |
| `Congestion` | Conservative | Congestion control mode |
| `CipherPreference` | AES-128-GCM | Encryption cipher |
| `MaxRetransmits` | 15 | Max retransmit attempts per packet |
| `FragmentTimeout` | 5s | Incomplete reassembly buffer expiry |
| `InitialCwnd` | 4 | Initial congestion window (packets) |

### Tick Modes

- **`TickAuto`** (default): The server spawns an internal goroutine that ticks at `TickRate` per second. Calling `Tick()` returns `ErrTickAutoMode`.
- **`TickDriven`**: No internal tick goroutine. The application calls `Tick()` manually, typically once per game frame.

```go
// TickDriven mode — call Tick() from your game loop.
config := fastwire.DefaultServerConfig()
config.TickMode = fastwire.TickDriven

srv, _ := fastwire.NewServer(":7777", config, handler)
srv.Start()

for {
    srv.Tick()
    // ...game logic...
}
```

### Server Methods

| Method | Description |
|--------|-------------|
| `Start()` | Launch read loop and tick goroutine (if TickAuto) |
| `Stop()` | Graceful shutdown: disconnect all, close socket |
| `Tick()` | Manual tick (TickDriven only) |
| `ConnectionCount()` | Number of active connections |
| `ForEachConnection(fn)` | Call `fn` for each active connection |
| `Addr()` | Local address the server is listening on |

## Client

The `Client` type connects to a FastWire server over UDP.

### Connecting

```go
config := fastwire.DefaultClientConfig()
cli, err := fastwire.NewClient(config, myHandler)
if err != nil {
    log.Fatal(err)
}
if err := cli.Connect("server.example.com:7777"); err != nil {
    log.Fatal(err)
}
defer cli.Close()
```

`Connect()` blocks until the handshake completes or times out (`ConnectTimeout`, default 5s).

### Client Configuration

| Field | Default | Description |
|-------|---------|-------------|
| `MTU` | 1200 | Maximum transmission unit |
| `TickRate` | 100 | Ticks per second (TickAuto only) |
| `TickMode` | `TickAuto` | `TickAuto` or `TickDriven` |
| `HeartbeatInterval` | 1s | Time between heartbeat packets |
| `ConnTimeout` | 10s | Connection timeout for inactivity |
| `ConnectTimeout` | 5s | Handshake timeout |
| `ChannelLayout` | Default (4 channels) | Channel configuration |
| `Compression` | None | Compression settings |
| `Congestion` | Conservative | Congestion control mode |
| `CipherPreference` | AES-128-GCM | Encryption cipher |
| `MaxRetransmits` | 15 | Max retransmit attempts per packet |
| `FragmentTimeout` | 5s | Incomplete reassembly buffer expiry |
| `InitialCwnd` | 4 | Initial congestion window (packets) |

### Client Methods

| Method | Description |
|--------|-------------|
| `Connect(addr)` | Connect to server (blocking) |
| `Close()` | Disconnect and release resources |
| `Tick()` | Manual tick (TickDriven only) |
| `Connection()` | Returns the server `*Connection`, or nil |

## Connection

The `Connection` type represents an established connection to a remote peer. Obtained from `Handler.OnConnect` (server side) or `Client.Connection()` (client side).

### Sending Messages

```go
// Queue for next tick (batched).
conn.Send([]byte("hello"), 0)

// Send immediately, bypassing tick queue.
conn.SendImmediate([]byte("urgent"), 0)
```

The second argument is the channel ID. Channel 0 is `ReliableOrdered` in the default layout.

### Connection Methods

| Method | Description |
|--------|-------------|
| `Send(data, channel)` | Queue message for next tick |
| `SendImmediate(data, channel)` | Send immediately (bypass queue) |
| `Close()` | Initiate graceful disconnect (non-blocking, retries via tick loop) |
| `State()` | Current `ConnState` |
| `RemoteAddr()` | Remote `netip.AddrPort` |
| `RTT()` | Current smoothed round-trip time |
| `Stats()` | Returns a `ConnectionStats` snapshot |

### Send Pipeline

When a message is sent (via `Send` or `SendImmediate`), it goes through:

1. **Compress** — if compression is configured and payload exceeds the hurdle
2. **Fragment** — if compressed payload exceeds `MaxFragmentPayload` (or compression flags are set)
3. **Encrypt** — AEAD encryption with nonce counter
4. **Send** — UDP write via the injected `sendFunc`

For reliable channels, each packet is also queued for retransmission.

## Connection Stats

The `Stats()` method returns a `ConnectionStats` snapshot with per-connection metrics.

```go
stats := conn.Stats()
fmt.Printf("RTT: %v, Loss: %.1f%%, Sent: %d bytes\n",
    stats.RTT, stats.PacketLoss*100, stats.BytesSent)
```

### ConnectionStats Fields

| Field | Type | Description |
|-------|------|-------------|
| `RTT` | `time.Duration` | Smoothed round-trip time |
| `RTTVariance` | `time.Duration` | RTT variance |
| `PacketLoss` | `float64` | Packet loss ratio (0.0–1.0), based on last 100 reliable packets |
| `BytesSent` | `uint64` | Total wire bytes sent (socket level) |
| `BytesReceived` | `uint64` | Total wire bytes received (socket level) |
| `CongestionWindow` | `int` | Current congestion window in packets (0 = unlimited) |
| `Uptime` | `time.Duration` | Time since connection was established |

### Packet Loss Tracking

Packet loss is measured using a sliding ring buffer of the last 100 reliable packets. Each reliable packet sent is recorded; when the remote peer acknowledges it, the entry is marked as acked. The loss ratio is `1 - acked/total` over the current window.

## Handler

The `Handler` interface defines callbacks for connection events.

```go
type Handler interface {
    OnConnect(conn *Connection)
    OnDisconnect(conn *Connection, reason DisconnectReason)
    OnMessage(conn *Connection, data []byte, channel byte)
    OnError(conn *Connection, err error)
}
```

Use `BaseHandler` for partial implementations:

```go
type MyHandler struct {
    fastwire.BaseHandler
}

func (h *MyHandler) OnMessage(conn *fastwire.Connection, data []byte, channel byte) {
    fmt.Printf("received: %s\n", data)
}
```

### Callback Threading

- In **TickAuto** mode, callbacks fire on the tick goroutine.
- In **TickDriven** mode, callbacks fire on the caller's goroutine (the game loop).
- The read goroutine never fires callbacks directly — all processing happens during tick.

## Buffer Pool

FastWire provides a buffer pool to reduce garbage collection pressure.

```go
buf := fastwire.GetBuffer()  // returns []byte of length DefaultMTU (1200)
defer fastwire.PutBuffer(buf)
// use buf...
```

## Protocol Version

FastWire has two version fields:

- `ProtocolVersion` (byte) — the FastWire wire protocol version. Checked during handshake.
- `ApplicationVersion` (uint16) — set by the application to identify its own protocol version (e.g. a game's network protocol). Defaults to 0.

```go
fastwire.ApplicationVersion = 3 // set before creating server/client
```

## Error Handling

All errors are sentinel values that can be compared with `errors.Is`:

| Error | When |
|-------|------|
| `ErrBufferTooSmall` | Buffer too small for encode/decode |
| `ErrVarIntOverflow` | VarInt exceeds 5 bytes |
| `ErrVarLongOverflow` | VarLong exceeds 10 bytes |
| `ErrStringTooLong` | String exceeds 32767 bytes |
| `ErrNegativeStringLength` | Decoded string length is negative |
| `ErrInvalidUUID` | Buffer too small for UUID |
| `ErrInvalidPacketHeader` | Packet header too short or malformed |
| `ErrInvalidFragmentHeader` | Fragment header too short |
| `ErrReplayedPacket` | Nonce already seen or too old |
| `ErrDecryptionFailed` | AEAD decryption failed |
| `ErrVersionMismatch` | Remote protocol version does not match |
| `ErrInvalidHandshake` | Handshake packet is malformed |
| `ErrHandshakeTimeout` | Handshake did not complete in time |
| `ErrConnectionClosed` | Operating on a closed connection |
| `ErrInvalidChannelLayout` | Channel layout has 0 or >256 channels |
| `ErrInvalidChannel` | Channel ID is out of range for the layout |
| `ErrMaxRetransmits` | A packet exceeded the max retransmit attempts (15) |
| `ErrMessageTooLarge` | Message exceeds the maximum fragment count (255 fragments) |
| `ErrCompressionFailed` | Compression failed (incompressible data or algorithm error) |
| `ErrDecompressionFailed` | Decompression failed (corrupt data, bomb protection, or algorithm error) |
| `ErrServerClosed` | Operating on a stopped server |
| `ErrServerNotStarted` | Tick() called before Start() |
| `ErrClientNotConnected` | Operating on an unconnected client |
| `ErrTickAutoMode` | Tick() called in TickAuto mode |
| `ErrAlreadyStarted` | Start() called on already started server |
| `ErrAlreadyConnected` | Connect() called on already connected client |

## Send Batching

Send batching packs multiple encrypted packets into a single UDP datagram during tick flush, reducing per-packet overhead and syscall count.

### Configuration

```go
config := fastwire.DefaultServerConfig()
config.SendBatching = true // enable on server

cliConfig := fastwire.DefaultClientConfig()
cliConfig.SendBatching = true // enable on client
```

Both sides must enable send batching for it to activate. The feature is negotiated during the handshake — if either side does not enable it, packets are sent in the legacy one-packet-per-datagram format.

### Wire Format

Batched datagrams use a simple framing format:
```
[count:1B][len1:2B LE][pkt1:len1 bytes][len2:2B LE][pkt2:len2 bytes]...
```

Each `pkt` is a complete encrypted packet. Packets are packed until the datagram reaches the MTU limit.

### Behavior

- `Send()` queues messages for the tick; the tick flush collects encrypted packets and packs them into batched datagrams
- `SendImmediate()` bypasses the batch buffer but still uses the batch frame format (single-packet batch)
- Retransmits and heartbeats are sent as single-packet batches
- Large messages requiring fragmentation work normally with batching

## Connection Migration

Connection migration allows clients to change IP address or port without reconnecting (e.g., when switching from Wi-Fi to cellular).

### Configuration

```go
config := fastwire.DefaultServerConfig()
config.ConnectionMigration = true

cliConfig := fastwire.DefaultClientConfig()
cliConfig.ConnectionMigration = true
```

Both sides must enable migration. An 8-byte migration token is assigned during the handshake and stored in the `Connection`.

### How It Works

1. During handshake, the server generates a random 8-byte `MigrationToken` and sends it in the CHALLENGE
2. The client prepends the token to every outgoing datagram: `[token:8B][payload]`
3. The server normally looks up connections by source address
4. If a packet arrives from an unknown address but carries a known token, the server migrates the connection to the new address
5. Server→client packets do not carry the token (the client has only one connection)

### Accessing the Token

```go
token := conn.MigrationToken // [8]byte
```

## I/O Coalescing

I/O coalescing uses multiple goroutines and asynchronous writes to improve server throughput.

### Configuration

```go
config := fastwire.DefaultServerConfig()
config.CoalesceIO = true        // enable coalescing
config.CoalesceReaders = 4      // 4 concurrent read goroutines (default: runtime.NumCPU())
```

When enabled:
- Multiple goroutines read from the UDP socket concurrently, reducing read latency under high packet rates
- A dedicated write goroutine batches socket writes, reducing contention on the socket
- On systems with multiple CPU cores, this significantly improves throughput

### Client

I/O coalescing is server-only. The client has a single connection and does not benefit from multiple read goroutines.

## Bandwidth Estimation

FastWire tracks send and receive throughput per connection using an EWMA (Exponentially Weighted Moving Average) estimator.

### Reading Bandwidth

```go
stats := conn.Stats()
fmt.Printf("Send: %.0f bytes/sec\n", stats.SendBandwidth)
fmt.Printf("Recv: %.0f bytes/sec\n", stats.RecvBandwidth)
```

The estimator updates once per tick. With the default tick rate of 100/s and smoothing factor α=0.2, the estimate responds to changes within ~500ms while filtering out noise.

### Use Cases

- Adaptive quality-of-service: reduce send rate when bandwidth drops
- Display network statistics to users
- Server-side monitoring and alerting

## Troubleshooting

### Connection Timeouts

If connections time out frequently, check that:
- Both client and server can reach each other over UDP (firewalls, NAT)
- `ConnTimeout` is large enough for the network conditions (default 10s)
- `HeartbeatInterval` is smaller than `ConnTimeout` to keep the connection alive
- The tick loop is running — in `TickDriven` mode, you must call `Tick()` regularly

### Messages Not Arriving

- Verify you are sending on a valid channel ID (0 to `layout.Len()-1`). Sending on an out-of-range channel returns `ErrInvalidChannel`.
- On unreliable channels (`Unreliable`, `UnreliableSequenced`), packet loss means messages are silently dropped. Use `ReliableOrdered` or `ReliableUnordered` for guaranteed delivery.
- Check `conn.Stats().PacketLoss` — high loss may indicate network issues. FastWire retransmits on reliable channels, but extreme loss can exhaust the retransmit limit (15 attempts).
- Ensure the receiver's `OnMessage` handler is not blocking. Long-running handlers delay tick processing.

### High Packet Loss

- Use `conn.Stats()` to monitor `PacketLoss`. Values above 10% indicate a degraded network.
- Consider using `CongestionConservative` mode, which reduces send rate on loss (AIMD).
- `CongestionAggressive` mode does not reduce send rate — it relies on fast retransmit. This can flood a lossy link.
- The congestion window in conservative mode starts at `DefaultInitialCwnd` (4 packets). If your application bursts many messages, the window may throttle delivery initially.

### Large Message Failures

- Messages are split into up to 255 fragments of `MaxFragmentPayload` (1155 bytes) each. The maximum message size is ~294 KB.
- Sending a larger message returns `ErrMessageTooLarge`.
- On unreliable channels, losing any single fragment causes the entire message to be lost (the remaining fragments expire after `FragmentTimeout`).
- For large messages, use a reliable channel to ensure all fragments are retransmitted if lost.

### Dictionary Mismatch

- When using zstd compression with a dictionary, both client and server must use the same dictionary bytes.
- During the handshake, FastWire compares SHA-256 hashes of the dictionaries. A mismatch causes compression to fall back to no-dictionary mode.
- Use `fastwire.DictionaryHash(dict)` to verify the hash matches on both sides before deployment.

### TickAutoMode Errors

- Calling `Tick()` on a server or client in `TickAuto` mode returns `ErrTickAutoMode`.
- If you want manual control over ticking (e.g., integrating with a game loop), set `TickMode: TickDriven` in the config.
- In `TickAuto` mode, the tick loop runs automatically at `TickRate` ticks per second.

### Connection Drops Under Load

- Under heavy load, the OS UDP socket buffer may overflow, causing packet drops. FastWire retransmits on reliable channels, but this increases latency.
- In `CongestionConservative` mode, the congestion window limits in-flight packets. If the window is too small, messages queue up. The window grows by ~1 packet per RTT.
- In `CongestionAggressive` mode, there is no send gating — all messages are sent immediately. This can overwhelm the receiver or intermediate network.
- Monitor `conn.Stats().CongestionWindow` and `conn.Stats().RTT` to diagnose bottlenecks.

### Thread Safety Guarantees

- `Connection.Send()` and `Connection.SendImmediate()` are safe to call from any goroutine.
- `Connection.Close()` is safe to call concurrently with `Send()`.
- `Handler` callbacks fire on the tick goroutine (`TickAuto`) or the caller's goroutine (`TickDriven`). Handlers should avoid blocking.
- All compression, encryption, and fragmentation operations are internally synchronized.
