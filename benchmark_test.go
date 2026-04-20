package fastwire

import (
	"crypto/rand"
	"net/netip"
	"strings"
	"testing"
	"time"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
)

// benchSendConfig holds configuration for a send path benchmark variant.
type benchSendConfig struct {
	name        string
	cipher      fwcrypto.CipherSuite
	compression CompressionAlgorithm
}

// benchSendVariants defines the three send path benchmark configurations.
var benchSendVariants = []benchSendConfig{
	{"Plain", fwcrypto.CipherNone, CompressionNone},
	{"AES_LZ4", fwcrypto.CipherAES128GCM, CompressionLZ4},
	{"AES_Zstd", fwcrypto.CipherAES128GCM, CompressionZstd},
}

func benchmarkSendPath(b *testing.B, cfg benchSendConfig) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	var key []byte
	if cfg.cipher != fwcrypto.CipherNone {
		key = make([]byte, 16)
		if _, err := rand.Read(key); err != nil {
			b.Fatal(err)
		}
	}
	sendCS, err := fwcrypto.NewCipherState(key, cfg.cipher)
	if err != nil {
		b.Fatal(err)
	}
	recvCS, err := fwcrypto.NewCipherState(key, cfg.cipher)
	if err != nil {
		b.Fatal(err)
	}

	conn := newConnection(connectionInput{
		addr:       addr,
		sendCipher: sendCS,
		recvCipher: recvCS,
		suite:      cfg.cipher,
		layout:     DefaultChannelLayout(),
		compression: CompressionConfig{
			Algorithm: cfg.compression,
			Hurdle:    DefaultCompressionHurdle,
		},
		congestionMode: CongestionConservative,
	})

	conn.sendFunc = func(data []byte) error { return nil }

	ch := conn.channels[0]
	payload := []byte(strings.Repeat("send path benchmark ", 25)) // ~500 bytes
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()

	var seq uint32
	for b.Loop() {
		_ = conn.sendMessage(payload, 0)
		seq++
		if seq%32 == 0 {
			ch.processAcks(seq, 0xFFFFFFFF, nil)
		}
	}
}

func BenchmarkSendPlain(b *testing.B)    { benchmarkSendPath(b, benchSendVariants[0]) }
func BenchmarkSendAES_LZ4(b *testing.B)  { benchmarkSendPath(b, benchSendVariants[1]) }
func BenchmarkSendAES_Zstd(b *testing.B) { benchmarkSendPath(b, benchSendVariants[2]) }

// benchmarkRecvPath exercises the full receive pipeline matching processPacket:
// decrypt → unmarshal → touchRecv → ACK processing → recordReceive → deliver →
// decompress → handler callback.
// A sender connection produces realistic encrypted packets via sendMessage, and
// the receiver connection processes them through the full pipeline.
func benchmarkRecvPath(b *testing.B, cfg benchSendConfig) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	var key []byte
	if cfg.cipher != fwcrypto.CipherNone {
		key = make([]byte, 16)
		if _, err := rand.Read(key); err != nil {
			b.Fatal(err)
		}
	}

	// Sender connection — produces encrypted packets.
	sendCS, err := fwcrypto.NewCipherState(key, cfg.cipher)
	if err != nil {
		b.Fatal(err)
	}
	sendRecvCS, err := fwcrypto.NewCipherState(key, cfg.cipher)
	if err != nil {
		b.Fatal(err)
	}
	sender := newConnection(connectionInput{
		addr:       addr,
		sendCipher: sendCS,
		recvCipher: sendRecvCS,
		suite:      cfg.cipher,
		layout:     DefaultChannelLayout(),
		compression: CompressionConfig{
			Algorithm: cfg.compression,
			Hurdle:    DefaultCompressionHurdle,
		},
		congestionMode: CongestionConservative,
	})

	// Capture encrypted packets from sendFunc.
	const maxPrealloc = 8192
	packets := make([][]byte, 0, maxPrealloc)
	sender.sendFunc = func(data []byte) error {
		if len(packets) < maxPrealloc {
			cp := make([]byte, len(data))
			copy(cp, data)
			packets = append(packets, cp)
		}
		return nil
	}

	// Generate packets by sending through the full send pipeline.
	payload := []byte(strings.Repeat("recv path benchmark ", 25)) // ~500 bytes
	for range maxPrealloc {
		_ = sender.sendMessage(payload, 0)
	}

	// Receiver connection — processes encrypted packets.
	recvSendCS, err := fwcrypto.NewCipherState(key, cfg.cipher)
	if err != nil {
		b.Fatal(err)
	}
	recvCS, err := fwcrypto.NewCipherState(key, cfg.cipher)
	if err != nil {
		b.Fatal(err)
	}
	receiver := newConnection(connectionInput{
		addr:       addr,
		sendCipher: recvSendCS,
		recvCipher: recvCS,
		suite:      cfg.cipher,
		layout:     DefaultChannelLayout(),
		compression: CompressionConfig{
			Algorithm: cfg.compression,
			Hurdle:    DefaultCompressionHurdle,
		},
		congestionMode: CongestionConservative,
	})
	receiver.setState(StateConnected)
	receiver.sendFunc = func(data []byte) error { return nil }

	numPackets := len(packets)
	if numPackets == 0 {
		b.Fatal("no packets captured from sender")
	}

	// No-op handler for delivery callbacks.
	var msgSink []byte

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		idx := i % numPackets
		// Reset receiver state when cycling to avoid replay/duplicate rejection.
		if idx == 0 && i > 0 {
			recvCS, err = fwcrypto.NewCipherState(key, cfg.cipher)
			if err != nil {
				b.Fatal(err)
			}
			receiver.recvCipher = recvCS
			receiver.reassembly = newReassemblyStore(reassemblyStoreInput{fragmentTimeout: 5 * time.Second})
			for _, ch := range receiver.channels {
				ch.mu.Lock()
				ch.recvAck = 0
				ch.recvAckField = 0
				ch.recvNextDeliver = 1
				ch.mu.Unlock()
			}
		}

		data := packets[idx]

		// Full receive pipeline (mirrors processPacket).
		dstBuf := getDecryptBuffer()
		decrypted, err := fwcrypto.Decrypt(receiver.recvCipher, data, dstBuf)
		if err != nil {
			putDecryptBuffer(dstBuf)
			b.Fatal(err)
		}

		hdr, n, err := UnmarshalHeader(decrypted)
		if err != nil {
			putDecryptBuffer(dstBuf)
			b.Fatal(err)
		}

		receiver.touchRecv()

		ch := receiver.channel(hdr.Channel)

		// ACK processing.
		acked := ch.processAcks(hdr.Ack, hdr.AckField, receiver.rttState)
		if len(acked) > 0 {
			receiver.cc.OnAck(len(acked))
			for _, seq := range acked {
				receiver.loss.RecordAck(seq)
			}
		}

		// Duplicate check.
		ch.recordReceive(hdr.Sequence)

		recvPayload := decrypted[n:]

		// Fragment handling + decompression (mirrors processPacket).
		if hdr.Flags&FlagFragment != 0 {
			fh, fn, err := UnmarshalFragmentHeader(recvPayload)
			if err != nil {
				putDecryptBuffer(dstBuf)
				b.Fatal(err)
			}
			assembled, complete, err := receiver.reassembly.addFragment(fh, recvPayload[fn:])
			if err != nil {
				putDecryptBuffer(dstBuf)
				b.Fatal(err)
			}
			if !complete {
				ch.deliver(hdr.Sequence, nil)
				putDecryptBuffer(dstBuf)
				continue
			}
			decompressed, err := receiver.compress.decompressPayload(assembled, fh.FragmentFlags)
			if err != nil {
				putDecryptBuffer(dstBuf)
				b.Fatal(err)
			}
			recvPayload = decompressed
		}

		// Deliver + handler callback.
		msgs := ch.deliver(hdr.Sequence, recvPayload)
		for _, msg := range msgs {
			if msg != nil {
				msgSink = msg
			}
		}

		putDecryptBuffer(dstBuf)
	}
	_ = msgSink
}

func BenchmarkRecvPlain(b *testing.B)    { benchmarkRecvPath(b, benchSendVariants[0]) }
func BenchmarkRecvAES_LZ4(b *testing.B)  { benchmarkRecvPath(b, benchSendVariants[1]) }
func BenchmarkRecvAES_Zstd(b *testing.B) { benchmarkRecvPath(b, benchSendVariants[2]) }

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
	defer func() { _ = srv.Stop() }()

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
				_ = srv.Tick()
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
		defer func() { _ = cli.Close() }()
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
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		// All clients queue a message.
		for _, conn := range conns {
			_ = conn.Send(msg, 0)
		}
		// Server tick processes incoming + outgoing.
		_ = srv.Tick()
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
