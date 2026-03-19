package fastwire

import (
	"crypto/rand"
	"net/netip"
	"strings"
	"testing"
	"time"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
)

// BenchmarkFullSendPath exercises the full send pipeline:
// compress → fragment check → marshal header → encrypt → sendFunc.
func BenchmarkFullSendPath(b *testing.B) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	sendCS, err := fwcrypto.NewCipherState(key, CipherAES128GCM)
	if err != nil {
		b.Fatal(err)
	}
	recvCS, err := fwcrypto.NewCipherState(key, CipherAES128GCM)
	if err != nil {
		b.Fatal(err)
	}

	conn := newConnection(addr, sendCS, recvCS, CipherAES128GCM, DefaultChannelLayout(), CompressionConfig{
		Algorithm: CompressionLZ4,
		Hurdle:    DefaultCompressionHurdle,
	}, CongestionConservative, 0, 0, MigrationToken{})

	// No-op sendFunc.
	conn.sendFunc = func(data []byte) error { return nil }

	payload := []byte(strings.Repeat("send path benchmark ", 25)) // ~500 bytes
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for b.Loop() {
		_ = conn.sendMessage(payload, 0)
	}
}

// BenchmarkFullRecvPath exercises: decrypt → UnmarshalHeader → decompressPayload.
func BenchmarkFullRecvPath(b *testing.B) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	sendCS, err := fwcrypto.NewCipherState(key, CipherAES128GCM)
	if err != nil {
		b.Fatal(err)
	}

	// Build a typical packet: header + payload.
	payload := make([]byte, 500)
	if _, err := rand.Read(payload); err != nil {
		b.Fatal(err)
	}

	hdr := &PacketHeader{
		Channel:  0,
		Sequence: 1,
		Ack:      0,
		AckField: 0,
	}
	buf := make([]byte, MaxHeaderSize+len(payload))
	n, err := MarshalHeader(buf, hdr)
	if err != nil {
		b.Fatal(err)
	}
	copy(buf[n:], payload)
	plaintext := buf[:n+len(payload)]

	// Pre-encrypt b.N packets with monotonic nonces.
	packets := make([][]byte, b.N)
	for i := range b.N {
		enc, err := fwcrypto.Encrypt(sendCS, plaintext, nil)
		if err != nil {
			b.Fatal(err)
		}
		packets[i] = enc
	}

	recvCS, err := fwcrypto.NewCipherState(key, CipherAES128GCM)
	if err != nil {
		b.Fatal(err)
	}

	cp, err := newCompressorPool(CompressionConfig{Algorithm: CompressionNone})
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(plaintext)))
	b.ResetTimer()
	for i := range b.N {
		decrypted, err := fwcrypto.Decrypt(recvCS, packets[i], nil)
		if err != nil {
			b.Fatal(err)
		}
		hdr, hn, err := UnmarshalHeader(decrypted)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = cp.decompressPayload(decrypted[hn:], 0)
		_ = hdr
	}
}

// BenchmarkServerThroughput exercises a TickDriven server with 10 connected clients.
func BenchmarkServerThroughput(b *testing.B) {
	srvHandler := &benchHandler{messageCh: make(chan struct{}, 100000)}
	srv, err := NewServer("127.0.0.1:0", ServerConfig{
		TickMode: TickDriven,
	}, srvHandler)
	if err != nil {
		b.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		b.Fatal(err)
	}
	defer srv.Stop()

	// Tick the server in the background during client setup so handshakes complete.
	setupDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-setupDone:
				return
			case <-ticker.C:
				srv.Tick()
			}
		}
	}()

	const numClients = 10
	clients := make([]*Client, numClients)
	for i := range numClients {
		cli, err := NewClient(DefaultClientConfig(), &BaseHandler{})
		if err != nil {
			b.Fatal(err)
		}
		if err := cli.Connect(srv.Addr().String()); err != nil {
			b.Fatal(err)
		}
		clients[i] = cli
		defer cli.Close()
	}

	// Wait for all connections.
	for attempts := 0; srv.ConnectionCount() < numClients && attempts < 200; attempts++ {
		time.Sleep(10 * time.Millisecond)
	}
	close(setupDone)

	if srv.ConnectionCount() < numClients {
		b.Fatalf("only %d/%d clients connected", srv.ConnectionCount(), numClients)
	}

	// Cache connections to avoid nil dereferences if a connection drops.
	conns := make([]*Connection, numClients)
	for i, cli := range clients {
		conns[i] = cli.Connection()
		if conns[i] == nil {
			b.Fatalf("client %d has nil connection", i)
		}
	}

	msg := []byte("benchmark")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// All clients queue a message.
		for _, conn := range conns {
			_ = conn.Send(msg, 0)
		}
		// Server tick processes incoming + outgoing.
		srv.Tick()
	}
	b.StopTimer()
}

// benchHandler is a minimal handler for benchmarks.
type benchHandler struct {
	BaseHandler
	messageCh chan struct{}
}

func (h *benchHandler) OnMessage(_ *Connection, _ []byte, _ byte) {
	select {
	case h.messageCh <- struct{}{}:
	default:
	}
}
