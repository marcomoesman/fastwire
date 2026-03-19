# FastWire Examples

## Echo Server

```go
package main

import (
    "fmt"
    "log"
    "os"
    "os/signal"

    "github.com/marcomoesman/fastwire"
)

type EchoHandler struct {
    fastwire.BaseHandler
}

func (h *EchoHandler) OnConnect(conn *fastwire.Connection) {
    fmt.Printf("client connected: %s\n", conn.RemoteAddr())
}

func (h *EchoHandler) OnDisconnect(conn *fastwire.Connection, reason fastwire.DisconnectReason) {
    fmt.Printf("client disconnected: %s (%s)\n", conn.RemoteAddr(), reason)
}

func (h *EchoHandler) OnMessage(conn *fastwire.Connection, data []byte, channel byte) {
    fmt.Printf("received from %s: %s\n", conn.RemoteAddr(), data)
    // Echo back on the same channel.
    conn.Send(data, channel)
}

func main() {
    srv, err := fastwire.NewServer(":7777", fastwire.DefaultServerConfig(), &EchoHandler{})
    if err != nil {
        log.Fatal(err)
    }
    if err := srv.Start(); err != nil {
        log.Fatal(err)
    }
    defer srv.Stop()

    fmt.Println("Echo server listening on :7777")

    // Wait for interrupt.
    sig := make(chan os.Signal, 1)
    signal.Notify(sig, os.Interrupt)
    <-sig
    fmt.Println("Shutting down...")
}
```

## Echo Client

```go
package main

import (
    "fmt"
    "log"
    "time"

    "github.com/marcomoesman/fastwire"
)

type ClientHandler struct {
    fastwire.BaseHandler
}

func (h *ClientHandler) OnMessage(conn *fastwire.Connection, data []byte, channel byte) {
    fmt.Printf("echo reply: %s\n", data)
}

func main() {
    cli, err := fastwire.NewClient(fastwire.DefaultClientConfig(), &ClientHandler{})
    if err != nil {
        log.Fatal(err)
    }
    if err := cli.Connect("127.0.0.1:7777"); err != nil {
        log.Fatal(err)
    }
    defer cli.Close()

    // Send a message on channel 0 (reliable ordered).
    cli.Connection().Send([]byte("hello, server!"), 0)

    time.Sleep(1 * time.Second) // wait for echo
}
```

## Game Server with Multiple Channels

```go
package main

import (
    "encoding/binary"
    "fmt"
    "log"

    "github.com/marcomoesman/fastwire"
)

const (
    ChannelGameState byte = 0 // ReliableOrdered
    ChannelEvents    byte = 1 // ReliableUnordered
    ChannelVoice     byte = 2 // Unreliable
    ChannelPosition  byte = 3 // UnreliableSequenced
)

type GameHandler struct {
    fastwire.BaseHandler
}

func (h *GameHandler) OnMessage(conn *fastwire.Connection, data []byte, channel byte) {
    switch channel {
    case ChannelGameState:
        fmt.Printf("game state update from %s\n", conn.RemoteAddr())
    case ChannelEvents:
        fmt.Printf("event from %s\n", conn.RemoteAddr())
    case ChannelVoice:
        fmt.Printf("voice data from %s (%d bytes)\n", conn.RemoteAddr(), len(data))
    case ChannelPosition:
        if len(data) >= 8 {
            x := binary.LittleEndian.Uint32(data[0:4])
            y := binary.LittleEndian.Uint32(data[4:8])
            fmt.Printf("position from %s: (%d, %d)\n", conn.RemoteAddr(), x, y)
        }
    }
}

func main() {
    config := fastwire.DefaultServerConfig()
    // Default layout already provides 4 channels with the modes above.

    srv, err := fastwire.NewServer(":7777", config, &GameHandler{})
    if err != nil {
        log.Fatal(err)
    }
    if err := srv.Start(); err != nil {
        log.Fatal(err)
    }
    defer srv.Stop()

    fmt.Printf("Game server running with %d channels\n", config.ChannelLayout.Len())
    select {} // block forever
}
```

## Tick-Driven Mode (Game Loop Integration)

```go
package main

import (
    "fmt"
    "log"
    "time"

    "github.com/marcomoesman/fastwire"
)

func main() {
    config := fastwire.DefaultServerConfig()
    config.TickMode = fastwire.TickDriven

    handler := &fastwire.BaseHandler{}
    srv, err := fastwire.NewServer(":7777", config, handler)
    if err != nil {
        log.Fatal(err)
    }
    if err := srv.Start(); err != nil {
        log.Fatal(err)
    }
    defer srv.Stop()

    // Game loop at 60 FPS.
    ticker := time.NewTicker(time.Second / 60)
    defer ticker.Stop()

    for range ticker.C {
        // Process network I/O.
        srv.Tick()

        // ... game logic ...

        fmt.Printf("connections: %d\n", srv.ConnectionCount())
    }
}
```

## Connection Stats Monitoring

```go
package main

import (
    "fmt"
    "log"
    "os"
    "os/signal"
    "time"

    "github.com/marcomoesman/fastwire"
)

func main() {
    srv, err := fastwire.NewServer(":7777", fastwire.DefaultServerConfig(), &fastwire.BaseHandler{})
    if err != nil {
        log.Fatal(err)
    }
    if err := srv.Start(); err != nil {
        log.Fatal(err)
    }
    defer srv.Stop()

    // Print stats for all connections every 5 seconds.
    go func() {
        for range time.Tick(5 * time.Second) {
            srv.ForEachConnection(func(conn *fastwire.Connection) {
                stats := conn.Stats()
                fmt.Printf("[%s] RTT=%v loss=%.1f%% sent=%d recv=%d cwnd=%d uptime=%v\n",
                    conn.RemoteAddr(),
                    stats.RTT,
                    stats.PacketLoss*100,
                    stats.BytesSent,
                    stats.BytesReceived,
                    stats.CongestionWindow,
                    stats.Uptime.Round(time.Second),
                )
            })
        }
    }()

    sig := make(chan os.Signal, 1)
    signal.Notify(sig, os.Interrupt)
    <-sig
}
```

## Using Compression

```go
package main

import (
    "log"

    "github.com/marcomoesman/fastwire"
)

func main() {
    // Server with LZ4 compression.
    srvConfig := fastwire.DefaultServerConfig()
    srvConfig.Compression = fastwire.CompressionConfig{
        Algorithm: fastwire.CompressionLZ4,
        Hurdle:    128, // only compress payloads >= 128 bytes
    }

    srv, err := fastwire.NewServer(":7777", srvConfig, &fastwire.BaseHandler{})
    if err != nil {
        log.Fatal(err)
    }
    srv.Start()
    defer srv.Stop()

    // Client must request the same compression algorithm.
    cliConfig := fastwire.DefaultClientConfig()
    cliConfig.Compression = fastwire.CompressionConfig{
        Algorithm: fastwire.CompressionLZ4,
        Hurdle:    128,
    }

    cli, err := fastwire.NewClient(cliConfig, &fastwire.BaseHandler{})
    if err != nil {
        log.Fatal(err)
    }
    if err := cli.Connect("127.0.0.1:7777"); err != nil {
        log.Fatal(err)
    }
    defer cli.Close()
}
```

## SendImmediate for Low-Latency Messages

```go
// SendImmediate bypasses the tick queue and sends the message immediately.
// Use it for time-critical messages where the next tick is too slow.
conn.SendImmediate([]byte("fire!"), 0)

// Regular Send queues for the next tick (batched with other messages).
conn.Send([]byte("chat: hello"), 0)
```

---

## Encoding and Decoding a VarInt

```go
package main

import (
    "fmt"
    "github.com/marcomoesman/fastwire"
)

func main() {
    buf := make([]byte, 5)

    // Encode
    n := fastwire.PutVarInt(buf, 300)
    fmt.Printf("Encoded 300 in %d bytes: %x\n", n, buf[:n])

    // Decode
    value, bytesRead, err := fastwire.ReadVarInt(buf[:n])
    if err != nil {
        panic(err)
    }
    fmt.Printf("Decoded: %d (read %d bytes)\n", value, bytesRead)
}
```

## Working with UUIDs

```go
package main

import (
    "fmt"
    "github.com/marcomoesman/fastwire"
)

func main() {
    // Create a UUID from two 64-bit halves
    id := fastwire.UUIDFromInts(0x0123456789ABCDEF, 0xFEDCBA9876543210)
    fmt.Println("UUID:", id.String())
    fmt.Printf("MSB: %016x, LSB: %016x\n", id.MSB(), id.LSB())

    // Encode to wire format
    buf := make([]byte, 16)
    fastwire.PutUUID(buf, id)

    // Decode from wire format
    decoded, _, err := fastwire.ReadUUID(buf)
    if err != nil {
        panic(err)
    }
    fmt.Println("Decoded UUID:", decoded.String())
}
```

## Marshaling a Packet Header

```go
package main

import (
    "fmt"
    "github.com/marcomoesman/fastwire"
)

func main() {
    header := fastwire.PacketHeader{
        Flags:    0,
        Channel:  0,
        Sequence: 42,
        Ack:      40,
        AckField: 0x00000003,
    }

    buf := make([]byte, 16)
    n, err := fastwire.MarshalHeader(buf, &header)
    if err != nil {
        panic(err)
    }
    fmt.Printf("Header: %d bytes: %x\n", n, buf[:n])

    // Parse it back
    parsed, bytesRead, err := fastwire.UnmarshalHeader(buf[:n])
    if err != nil {
        panic(err)
    }
    fmt.Printf("Parsed: seq=%d ack=%d (%d bytes)\n",
        parsed.Sequence, parsed.Ack, bytesRead)
}
```

## Using the Buffer Pool

```go
package main

import (
    "fmt"
    "github.com/marcomoesman/fastwire"
)

func main() {
    buf := fastwire.GetBuffer()
    defer fastwire.PutBuffer(buf)

    fmt.Printf("Buffer length: %d (MTU: %d)\n", len(buf), fastwire.DefaultMTU)

    // Use buf for packet encoding...
    n, err := fastwire.MarshalHeader(buf, &fastwire.PacketHeader{
        Sequence: 1,
        Ack:      0,
    })
    if err != nil {
        panic(err)
    }
    fmt.Printf("Wrote %d header bytes into pooled buffer\n", n)
}
```

## Key Exchange and Key Derivation

```go
package main

import (
    "crypto/rand"
    "fmt"
    "github.com/marcomoesman/fastwire"
)

func main() {
    // Both sides generate ephemeral key pairs.
    clientKP, err := fastwire.GenerateKeyPair()
    if err != nil {
        panic(err)
    }
    serverKP, err := fastwire.GenerateKeyPair()
    if err != nil {
        panic(err)
    }

    // The server generates a random challenge token.
    challengeToken := make([]byte, 32)
    if _, err := rand.Read(challengeToken); err != nil {
        panic(err)
    }

    // Both sides derive the same symmetric keys.
    suite := fastwire.CipherAES128GCM

    clientC2S, clientS2C, err := fastwire.DeriveKeys(
        clientKP.Private, serverKP.Public, challengeToken, suite,
    )
    if err != nil {
        panic(err)
    }

    serverC2S, serverS2C, err := fastwire.DeriveKeys(
        serverKP.Private, clientKP.Public, challengeToken, suite,
    )
    if err != nil {
        panic(err)
    }

    fmt.Printf("Keys match: c2s=%v, s2c=%v\n",
        fmt.Sprintf("%x", clientC2S) == fmt.Sprintf("%x", serverC2S),
        fmt.Sprintf("%x", clientS2C) == fmt.Sprintf("%x", serverS2C),
    )
    // Output: Keys match: c2s=true, s2c=true
}
```

## Using the Default Channel Layout

```go
package main

import (
    "fmt"
    "github.com/marcomoesman/fastwire"
)

func main() {
    layout := fastwire.DefaultChannelLayout()
    fmt.Printf("Default layout has %d channels\n", layout.Len())
    // Output: Default layout has 4 channels
    // Channel 0: ReliableOrdered
    // Channel 1: ReliableUnordered
    // Channel 2: Unreliable
    // Channel 3: UnreliableSequenced
}
```

## Custom Channel Layout

```go
package main

import (
    "fmt"
    "github.com/marcomoesman/fastwire"
)

func main() {
    // Build a custom layout for a game server:
    // - Channel 0: reliable ordered for game state
    // - Channel 1: reliable ordered for chat (separate ordering stream)
    // - Channel 2: unreliable for voice/audio
    // - Channel 3: unreliable sequenced for position updates
    layout, err := fastwire.NewChannelLayoutBuilder().
        AddChannel(fastwire.ReliableOrdered, 0).
        AddChannel(fastwire.ReliableOrdered, 1).
        AddChannel(fastwire.Unreliable, 0).
        AddChannel(fastwire.UnreliableSequenced, 0).
        Build()
    if err != nil {
        panic(err)
    }
    fmt.Printf("Custom layout has %d channels\n", layout.Len())
    // Output: Custom layout has 4 channels
}
```

## Configuring Compression

```go
package main

import (
    "fmt"
    "github.com/marcomoesman/fastwire"
)

func main() {
    // LZ4 compression — fast, good for real-time games.
    lz4Config := fastwire.CompressionConfig{
        Algorithm: fastwire.CompressionLZ4,
        Hurdle:    128, // skip compression for payloads < 128 bytes
    }
    fmt.Printf("LZ4 config: algorithm=%d, hurdle=%d\n",
        lz4Config.Algorithm, lz4Config.Hurdle)

    // Zstd compression — better ratio, supports dictionaries.
    zstdConfig := fastwire.CompressionConfig{
        Algorithm: fastwire.CompressionZstd,
        Hurdle:    128,
    }
    fmt.Printf("Zstd config: algorithm=%d, hurdle=%d\n",
        zstdConfig.Algorithm, zstdConfig.Hurdle)

    // No compression (default).
    noCompression := fastwire.CompressionConfig{
        Algorithm: fastwire.CompressionNone,
    }
    fmt.Printf("No compression: algorithm=%d\n", noCompression.Algorithm)
}
```

## Dictionary Hash Verification

```go
package main

import (
    "fmt"
    "github.com/marcomoesman/fastwire"
)

func main() {
    // Both sides must use the same dictionary for zstd.
    dict := []byte("repeated game state patterns go here...")
    hash := fastwire.DictionaryHash(dict)
    fmt.Printf("Dictionary SHA-256: %x\n", hash)

    // During the handshake, FastWire compares dictionary hashes
    // to ensure both sides are using the same dictionary.
    // A mismatch is reported via the compression ack.
}
```

## Choosing a Congestion Control Mode

```go
package main

import (
    "fmt"
    "github.com/marcomoesman/fastwire"
)

func main() {
    // Conservative (AIMD) — standard TCP-like congestion window.
    // Good for general-purpose or bandwidth-constrained links.
    conservative := fastwire.CongestionConservative
    fmt.Printf("Conservative mode: %d\n", conservative)

    // Aggressive — no window gating, fast retransmit.
    // Ideal for real-time games where latency > bandwidth fairness.
    aggressive := fastwire.CongestionAggressive
    fmt.Printf("Aggressive mode: %d\n", aggressive)

    // The default initial congestion window is 4 packets.
    fmt.Printf("Default initial cwnd: %d\n", fastwire.DefaultInitialCwnd)
}
```

## Zstd Dictionary Compression

```go
package main

import (
    "fmt"
    "log"
    "os"

    "github.com/marcomoesman/fastwire"
)

func main() {
    // Load the pre-trained dictionary (same file on both client and server).
    dict, err := os.ReadFile("game_state.dict")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Dictionary loaded: %d bytes, hash: %x\n",
        len(dict), fastwire.DictionaryHash(dict))

    // Server config with zstd + dictionary.
    srvConfig := fastwire.DefaultServerConfig()
    srvConfig.Compression = fastwire.CompressionConfig{
        Algorithm:  fastwire.CompressionZstd,
        Hurdle:     64,  // compress payloads >= 64 bytes
        Dictionary: dict,
    }

    srv, err := fastwire.NewServer(":7777", srvConfig, &fastwire.BaseHandler{})
    if err != nil {
        log.Fatal(err)
    }
    srv.Start()
    defer srv.Stop()

    // Client config — must use the same dictionary.
    cliConfig := fastwire.DefaultClientConfig()
    cliConfig.Compression = fastwire.CompressionConfig{
        Algorithm:  fastwire.CompressionZstd,
        Hurdle:     64,
        Dictionary: dict,
    }

    cli, err := fastwire.NewClient(cliConfig, &fastwire.BaseHandler{})
    if err != nil {
        log.Fatal(err)
    }
    if err := cli.Connect("127.0.0.1:7777"); err != nil {
        log.Fatal(err)
    }
    defer cli.Close()

    // Dictionary hashes are compared during the handshake.
    // If they match, zstd uses the dictionary for better compression
    // of small, repetitive payloads like game state updates.
    fmt.Println("Connected with zstd dictionary compression")
}
```

## Setting the Application Protocol Version

```go
package main

import (
    "fmt"
    "github.com/marcomoesman/fastwire"
)

func main() {
    // Set your game's protocol version before creating server/client
    fastwire.ApplicationVersion = 3

    fmt.Printf("FastWire protocol: v%d\n", fastwire.ProtocolVersion)
    fmt.Printf("Application protocol: v%d\n", fastwire.ApplicationVersion)
}
```

## Send Batching

```go
// Enable send batching on both server and client.
srvConfig := fastwire.DefaultServerConfig()
srvConfig.SendBatching = true

cliConfig := fastwire.DefaultClientConfig()
cliConfig.SendBatching = true

// Usage is transparent — Send() and SendImmediate() work the same way.
// The tick loop automatically packs small messages into fewer UDP datagrams.
conn.Send([]byte("small msg 1"), 0)
conn.Send([]byte("small msg 2"), 0)
// Both messages may be packed into a single UDP datagram on the next tick.
```

## Connection Migration

```go
// Enable connection migration to survive IP/port changes.
srvConfig := fastwire.DefaultServerConfig()
srvConfig.ConnectionMigration = true

cliConfig := fastwire.DefaultClientConfig()
cliConfig.ConnectionMigration = true

// After connecting, the migration token is automatically managed.
// If the client's address changes (e.g., Wi-Fi → cellular), the
// server detects the token in incoming packets and migrates the connection.
```

## I/O Coalescing (Server)

```go
// Enable multi-goroutine reads and async writes for high-throughput servers.
config := fastwire.DefaultServerConfig()
config.CoalesceIO = true
config.CoalesceReaders = 8 // 8 concurrent read goroutines
```

## Bandwidth Monitoring

```go
// Read bandwidth estimates from connection stats.
stats := conn.Stats()
fmt.Printf("Upload:   %.1f KB/s\n", stats.SendBandwidth/1024)
fmt.Printf("Download: %.1f KB/s\n", stats.RecvBandwidth/1024)
fmt.Printf("RTT:      %v\n", stats.RTT)
fmt.Printf("Loss:     %.1f%%\n", stats.PacketLoss*100)
```
