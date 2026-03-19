# Phase 8 — Server & Client Architecture

## What Was Built

Wired all building blocks from Phases 1–7 into working `Server` and `Client` types with UDP read loops, tick-driven processing, and the full send/receive pipeline.

### New Files

- **`callback.go`** — `Handler` interface with `OnConnect`, `OnDisconnect`, `OnMessage`, `OnError` callbacks. `BaseHandler` with no-op implementations for embedding.
- **`server.go`** — Full `Server` implementation with sharded connection table, pending handshake table, read loop, tick loop, and connection lifecycle management.
- **`client.go`** — Full `Client` implementation with single server connection, handshake flow, read loop, tick loop.

### Modified Files

- **`config.go`** — Added `TickMode` (`TickAuto`/`TickDriven`), `ServerConfig`, `ClientConfig` with defaults.
- **`errors.go`** — Added `ErrServerClosed`, `ErrServerNotStarted`, `ErrClientNotConnected`, `ErrTickAutoMode`, `ErrAlreadyStarted`, `ErrAlreadyConnected`.
- **`connection.go`** — Added `sendQueue`, `sendFunc`, `closeFunc`, `outgoingMessage` type. Added methods: `Send`, `SendImmediate`, `RTT`, `Close`, `queueMessage`, `drainSendQueue`, `requeue`, `sendMessage`, `sendSinglePacket`, `sendFragmented`, `sendHeartbeat`, `sendHeartbeatOnChannel`. Removed `//nolint:unused` annotations.
- **`handshake.go`** — Removed `//nolint:unused` annotations from `marshalHeartbeat`, `marshalDisconnect`, `buildControlPacket`, `buildRejectPacket`.
- **`channel.go`** — Added `needsAck` flag for explicit ack flushing. Added `clearNeedsAck` method.

## Design Decisions

1. **No worker pool** — tick processes everything serially. Correct, testable, avoids ordering/callback threading issues. Worker pool is a future optimization.
2. **Unified tick model** — both Server and Client share the same `processPacket` / `tickConnection` logic pattern.
3. **TickAuto / TickDriven** — TickAuto spawns an internal goroutine; TickDriven requires manual `Tick()` calls. Read goroutine runs independently in both modes, pushing to an incoming channel.
4. **sendFunc injection** — `Connection` stores a `sendFunc func([]byte) error` set by Server/Client, decoupling connection logic from UDP socket details.
5. **Sharded connection table** — 64 shards with RWMutex per shard for reduced lock contention. Tick collects connections to a slice before iterating to avoid holding read locks during mutations.
6. **Explicit ack flushing** — Each channel tracks a `needsAck` flag set when data is received. The tick loop sends ack-carrying heartbeat packets for channels with pending acks, ensuring acks flow back within one tick (~10ms at 100 ticks/s).
7. **Fragment nil delivery** — For fragmented messages on reliable ordered channels, intermediate (non-completing) fragments consume their sequence slot with a nil payload via `deliver()`, preventing sequence stalls. The final fragment delivers the full reassembled payload. Nil entries are filtered before calling `OnMessage`.

## Test Requirements

- [x] `TestBaseHandlerNoPanic` — BaseHandler no-op methods don't panic
- [x] `TestBaseHandlerSatisfiesHandler` — BaseHandler satisfies Handler interface
- [x] `TestNewServer` — config defaults applied
- [x] `TestServerStartStop` — lifecycle clean
- [x] `TestServerDoubleStart` — returns ErrAlreadyStarted
- [x] `TestServerAcceptConnection` — full handshake over loopback, OnConnect fires
- [x] `TestServerRejectWhenFull` — MaxConnections=1, second client rejected
- [x] `TestServerConnectionTimeout` — client stops sending, OnDisconnect(DisconnectTimeout) fires
- [x] `TestServerTickDriven` — manual Tick() works; Tick() in TickAuto returns error
- [x] `TestServerMessageRoundTrip` — client sends, server OnMessage receives
- [x] `TestServerBidirectionalMessages` — messages both directions
- [x] `TestServerConcurrentConnections` — 5 clients connect concurrently
- [x] `TestNewClient` — config defaults
- [x] `TestClientConnectDisconnect` — connect to server, close
- [x] `TestClientConnectTimeout` — connect to non-listening addr, ErrHandshakeTimeout
- [x] `TestClientSendReceive` — send and receive messages
- [x] `TestClientSendImmediate` — bypass tick queue
- [x] `TestClientTickDriven` — manual Tick()
- [x] `TestFullRoundTrip` — server + client, handshake, bidirectional messages, graceful disconnect
- [x] `TestFragmentedMessageDelivery` — large message >MTU, verify reassembly
- [x] `TestAllDeliveryModes` — messages on all 4 channel types
- [x] `TestHeartbeatKeepsAlive` — idle connections stay alive via heartbeats
- [x] `TestMultipleMessages` — 20 messages delivered reliably with congestion control
- [x] Race detector clean: `go test -race ./...`
