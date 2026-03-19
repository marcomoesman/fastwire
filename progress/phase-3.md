# Phase 3 — Connection & Handshake

## What was built

### `connection.go` — Connection struct, state machine
- **`ConnState`** enum: `StateDisconnected`, `StateConnecting`, `StateConnected`, `StateDisconnecting`
- **`DisconnectReason`** enum: `Graceful`, `Timeout`, `Error`, `Rejected`, `Kicked`
- **`Connection`** struct (exported, fields unexported): state, remote addr, send/recv cipher states, cipher suite, fragment ID counter, timestamps
- **`newConnection()`** — creates a Connection in StateConnected
- **State accessors**: `State()`, `RemoteAddr()`, `setState()`, `touchSend()`, `touchRecv()`
- **Heartbeat/timeout**: `needsHeartbeat(interval)`, `isTimedOut(timeout)` — checked by tick loop (Phase 8)
- **`nextFragmentID()`** — atomic uint16-wrapping counter

### `handshake.go` — Handshake packet serialization and processing
- **Wire format types**: `connectPacket`, `challengePacket` (unexported parsed structs)
- **`compressionAck`** — server response to compression preference (OK / DictMismatch)
- **`RejectReason`** — exported enum for rejection reasons (ServerFull / Banned)
- **`pendingHandshake`** — lightweight server-side state before RESPONSE verification (challenge token, derived keys, cipher suite, timestamp)
- **Marshal/Unmarshal** for each control type: Connect, Challenge, Response, VersionMismatch, Reject, Heartbeat, Disconnect
- **Packet builders**: `buildControlPacket`, `buildConnectPacket`, `buildChallengePacket`, `buildResponsePacket`, `buildVersionMismatchPacket`, `buildRejectPacket`
- **`parseControlPacket`** — extracts PacketHeader + ControlType + payload from raw bytes
- **Server-side processing**:
  - `serverProcessConnect(data, compression)` → pending + CHALLENGE bytes (or VERSION_MISMATCH on error)
  - `serverProcessResponse(encrypted, pending, addr)` → Connection
- **Client-side processing**:
  - `clientProcessChallenge(data, clientKP)` → send/recv cipher states + encrypted RESPONSE bytes

### `errors.go` — 4 new sentinel errors
- `ErrVersionMismatch` — protocol version mismatch
- `ErrInvalidHandshake` — malformed handshake packet
- `ErrHandshakeTimeout` — handshake didn't complete in time
- `ErrConnectionClosed` — connection is already closed

## Wire formats

**CONNECT**: `[ControlConnect 1B][proto 1B][app 2B BE][pubkey 32B][cipher 1B][comp 1B][dictPresent 1B][dictHash 32B?]` (39-71 bytes)
**CHALLENGE**: `[ControlChallenge 1B][proto 1B][app 2B BE][pubkey 32B][token 32B][cipher 1B][compAck 1B]` (70 bytes)
**RESPONSE**: `[ControlResponse 1B][token 32B]` (33 bytes, encrypted)
**VERSION_MISMATCH**: `[ControlVersionMismatch 1B][proto 1B][app 2B BE]` (4 bytes)
**REJECT**: `[ControlReject 1B][reason 1B]` (2 bytes)

## Design decisions

- **Lightweight pending handshake** — server does NOT allocate full Connection until RESPONSE is verified; only stores derived keys + challenge token
- **Server accepts client cipher preference** — no server-side cipher downgrade; later phases can add server-side preference/negotiation
- **CipherNone handshake** — same flow regardless of cipher suite (key exchange always happens, keys just aren't used with CipherNone)
- **ApplicationVersion in wire format** — added alongside ProtocolVersion for application-level versioning
- **All control payloads include ControlType byte** — consistent format, each unmarshal function validates its own type

## Test checklist

- [x] `TestNewConnection` — initial state, addr
- [x] `TestConnectionStateTransitions` — Connected → Disconnecting → Disconnected
- [x] `TestConnectionStateString` — all states including Unknown
- [x] `TestDisconnectReasonString` — all reasons including Unknown
- [x] `TestConnectionHeartbeatNeeded` — no heartbeat immediately, heartbeat after interval
- [x] `TestConnectionHeartbeatOnlyWhenConnected` — no heartbeat in Disconnecting state
- [x] `TestConnectionTimeout` — no timeout immediately, timeout after period, reset on touchRecv
- [x] `TestNextFragmentIDWraps` — sequential IDs, uint16 wrapping
- [x] `TestMarshalUnmarshalConnect` — round-trip without dict hash
- [x] `TestMarshalUnmarshalConnectWithDictHash` — round-trip with dict hash
- [x] `TestMarshalUnmarshalChallenge` — round-trip
- [x] `TestMarshalUnmarshalResponse` — round-trip
- [x] `TestMarshalUnmarshalVersionMismatch` — round-trip
- [x] `TestMarshalUnmarshalReject` — round-trip
- [x] `TestParseControlPacket` — parse Connect from full packet
- [x] `TestParseControlPacketNotControl` — reject non-control packet
- [x] `TestFullHandshakeAES` — full 3-way handshake + bidirectional encryption
- [x] `TestFullHandshakeChaCha` — full 3-way handshake + bidirectional encryption
- [x] `TestFullHandshakeCipherNone` — full 3-way handshake + pass-through
- [x] `TestHandshakeVersionMismatch` — wrong version → VERSION_MISMATCH packet
- [x] `TestHandshakeCipherNegotiation` — all three suites negotiated correctly
- [x] `TestHandshakeCompressionOK` — matching compression → compressionOK
- [x] `TestHandshakeCompressionDictMismatch` — mismatched compression → compressionDictMismatch
- [x] `TestPendingHandshakeExpiry` — expired and non-expired checks
- [x] `TestServerRejectsTamperedResponse` — tampered ciphertext rejected
- [x] `TestServerRejectsWrongToken` — wrong challenge token rejected
- [x] `FuzzUnmarshalConnect` — 10s, no panics
- [x] `FuzzUnmarshalChallenge` — 10s, no panics
