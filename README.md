# FastWire

![GitHub Release](https://img.shields.io/github/v/release/marcomoesman/FastWire?style=flat-square) ![GitHub License](https://img.shields.io/github/license/marcomoesman/fastwire?style=flat-square)

Enhanced UDP networking library for Go, built for fast-paced, low-latency applications such as gaming.

## Features

- **Reliable & unreliable delivery** — 4 modes: ReliableOrdered, ReliableUnordered, Unreliable, UnreliableSequenced
- **Encryption** — AES-128-GCM or ChaCha20-Poly1305 with X25519 key exchange and replay protection
- **Compression** — LZ4 or Zstd (with dictionary support) applied transparently before fragmentation
- **Fragmentation** — automatic split/reassemble for messages up to ~294 KB
- **Congestion control** — conservative (AIMD) or aggressive (no gating, fast retransmit)
- **Tick-driven or auto** — integrate with your game loop or let FastWire tick internally
- **Connection stats** — RTT, packet loss, bytes sent/received, congestion window, uptime

## Quick Start

```go
// Server
srv, _ := fastwire.NewServer(":7777", fastwire.DefaultServerConfig(), handler)
srv.Start()
defer srv.Stop()

// Client
cli, _ := fastwire.NewClient(fastwire.DefaultClientConfig(), handler)
cli.Connect("127.0.0.1:7777")
defer cli.Close()

// Send a message on channel 0 (reliable ordered)
cli.Connection().Send([]byte("hello"), 0)
```

## Documentation

- [**Usage Guide**](docs/DOCS.md) — comprehensive API reference covering all types, configuration, and a troubleshooting section
- [**Examples**](docs/EXAMPLES.md) — code examples for common use cases (echo server, game server, compression, tick-driven mode, etc.)
- [**Benchmarks**](docs/BENCHMARKS.md) — performance numbers on Apple M4

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
└── docs/                # Documentation and benchmarks
```

## Requirements

- Go 1.26+, no CGO

## License

See [LICENSE](LICENSE).

## Usage of AI Disclaimer

The FastWire project has largely been written by Claude Code using the Opus 4.6 model, and only features minimal amount of human-written code. It is part of a series of projects made by me for fun, and is not optimized for production. Whilst the goal is to improve and optimize as much as possible, I highly doubt the code in this project is as efficient, neat or optimized as something human-written.

The `progress/` folder contains design decisions and phased implementation reports written by Claude Code, for those who are interested.

When making a pull request, you are free to use Claude Code or Codex to assist with coding, using the included CLAUDE.md file and other files present in `progress/`. Please do consider auditing AI written code yourself before submitting pull requests. 

When using AI to assist with coding a pull request, ensure that an adequate phase progress report is written to `progress/`. Any pull requests without such a report will be rejected.