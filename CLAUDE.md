# FastWire

## Project Overview

FastWire is an enhanced UDP networking library for Go, made for fast-paced, low latency applications such as gaming.

Read `progress/implementation-guide.md` for the full specification — it is the authoritative reference for all design decisions, data sources, rule templates, and implementation phases.

## Tech Stack

- Go 1.26, **no CGO**

## Tests & Run

Tests: `go test ./...`
Lint: `golangci-lint run`

You should always write comprehensive tests for complex components, taking care to write tests for Phase requirements.

## Project Structure

```
fastwire/
├── fastwire.go          # Package doc, protocol version constants
├── errors.go            # Sentinel errors
├── config.go            # Configuration types, delivery modes, channel layout
├── wire.go              # Wire primitives: VarInt, VarLong, String, UUID
├── packet.go            # Packet header marshal/unmarshal
├── fragment.go          # Fragmentation and reassembly
├── compress.go          # LZ4/Zstd compression with pooling
├── connection.go        # Connection state machine, send/recv pipeline
├── channel.go           # Per-channel sequencing, acks, delivery modes
├── handshake.go         # 3-way handshake with key exchange
├── server.go            # Server: listen, accept, tick loop
├── client.go            # Client: connect, tick loop
├── callback.go          # Handler interface
├── pool.go              # sync.Pool buffer pool
├── stats.go             # ConnectionStats type
├── crypto/              # AEAD encryption, X25519 key exchange, replay window
├── internal/rtt/        # RTT measurement (Jacobson/Karels)
├── internal/congestion/ # Congestion control (AIMD, aggressive)
├── internal/stats/      # Packet loss tracking
├── examples/chat/       # Chat server & client example
├── docs/                # Documentation, examples, benchmarks
└── progress/            # Implementation tracking
    ├── implementation-guide.md
    ├── overview.md
    ├── next.md
    └── phase-*.md
```

## Key Design Decisions

**Read the implementation guide before making changes.** It documents critical decisions.

## Progress Tracking

The `progress/` folder contains files documenting what has be done, what design decisions were made, and what still needs to be done.
You must maintain several files:
- `progress/overview.md` - Keep an overview of all design Phases, and the next design Phase.
- `progress/phase-*.md` - Write short and clear documentation of what has been done in the Phase, and what design decisions were made. Also include requirements to test for and check off when passed before moving to the next phase.
- `progress/next.md` - Maintain a short guide on what needs to be done in the next Phase, knowing what you learned from previous Phases.

## Documentation & Examples

Given that this library is to be used by people who have not written it, include a collection of documentation and examples.
You must maintain two files:
- `docs/DOCS.md` - An understandable and comprehensive documentation guide on how to use the FastWire library.
- `docs/EXAMPLES.md` - A short and understandable document containing some basic examples on how to use the FastWire library.