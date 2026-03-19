package fastwire

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// testHandler records events for assertions.
type testHandler struct {
	BaseHandler
	mu           sync.Mutex
	connects     []*Connection
	disconnects  []disconnectEvent
	messages     []messageEvent
	errors       []error
	connectCh    chan *Connection
	messageCh    chan messageEvent
	disconnectCh chan disconnectEvent
}

type disconnectEvent struct {
	conn   *Connection
	reason DisconnectReason
}

type messageEvent struct {
	conn    *Connection
	data    []byte
	channel byte
}

func newTestHandler() *testHandler {
	return &testHandler{
		connectCh:    make(chan *Connection, 16),
		messageCh:    make(chan messageEvent, 64),
		disconnectCh: make(chan disconnectEvent, 16),
	}
}

func (h *testHandler) OnConnect(conn *Connection) {
	h.mu.Lock()
	h.connects = append(h.connects, conn)
	h.mu.Unlock()
	select {
	case h.connectCh <- conn:
	default:
	}
}

func (h *testHandler) OnDisconnect(conn *Connection, reason DisconnectReason) {
	h.mu.Lock()
	h.disconnects = append(h.disconnects, disconnectEvent{conn, reason})
	h.mu.Unlock()
	select {
	case h.disconnectCh <- disconnectEvent{conn, reason}:
	default:
	}
}

func (h *testHandler) OnMessage(conn *Connection, data []byte, channel byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	h.mu.Lock()
	h.messages = append(h.messages, messageEvent{conn, cp, channel})
	h.mu.Unlock()
	select {
	case h.messageCh <- messageEvent{conn, cp, channel}:
	default:
	}
}

func (h *testHandler) OnError(_ *Connection, err error) {
	h.mu.Lock()
	h.errors = append(h.errors, err)
	h.mu.Unlock()
}

func (h *testHandler) connectCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.connects)
}

func (h *testHandler) messageCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.messages)
}

// startTestServer creates and starts a server on a random port.
func startTestServer(t *testing.T, config ServerConfig, handler Handler) *Server {
	t.Helper()
	srv, err := NewServer("127.0.0.1:0", config, handler)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { srv.Stop() })
	return srv
}

// connectTestClient creates a client and connects to the server.
func connectTestClient(t *testing.T, config ClientConfig, handler Handler, serverAddr string) *Client {
	t.Helper()
	cli, err := NewClient(config, handler)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := cli.Connect(serverAddr); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { cli.Close() })
	return cli
}

func TestNewServer(t *testing.T) {
	h := newTestHandler()
	srv, err := NewServer("127.0.0.1:0", ServerConfig{}, h)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	// Defaults should be applied.
	if srv.config.MTU != DefaultMTU {
		t.Errorf("MTU = %d, want %d", srv.config.MTU, DefaultMTU)
	}
	if srv.config.MaxConnections != 1024 {
		t.Errorf("MaxConnections = %d, want 1024", srv.config.MaxConnections)
	}
	if srv.config.TickRate != 100 {
		t.Errorf("TickRate = %d, want 100", srv.config.TickRate)
	}
	srv.conn.Close()
}

func TestServerStartStop(t *testing.T) {
	h := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), h)
	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestServerDoubleStart(t *testing.T) {
	h := newTestHandler()
	srv, err := NewServer("127.0.0.1:0", DefaultServerConfig(), h)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Stop()

	if err := srv.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := srv.Start(); err != ErrAlreadyStarted {
		t.Fatalf("second Start = %v, want ErrAlreadyStarted", err)
	}
}

func TestServerAcceptConnection(t *testing.T) {
	srvHandler := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), srvHandler)

	cliHandler := newTestHandler()
	_ = connectTestClient(t, DefaultClientConfig(), cliHandler, srv.Addr().String())

	// Wait for server to see the connection.
	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server OnConnect")
	}

	if srv.ConnectionCount() != 1 {
		t.Fatalf("ConnectionCount = %d, want 1", srv.ConnectionCount())
	}
}

func TestServerRejectWhenFull(t *testing.T) {
	config := DefaultServerConfig()
	config.MaxConnections = 1
	srvHandler := newTestHandler()
	srv := startTestServer(t, config, srvHandler)

	// First client — should connect.
	cliHandler1 := newTestHandler()
	_ = connectTestClient(t, DefaultClientConfig(), cliHandler1, srv.Addr().String())

	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first connect")
	}

	// Second client — should fail.
	cliConfig := DefaultClientConfig()
	cliConfig.ConnectTimeout = 1 * time.Second
	cli2, err := NewClient(cliConfig, newTestHandler())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = cli2.Connect(srv.Addr().String())
	if err == nil {
		cli2.Close()
		t.Fatal("expected second Connect to fail, but succeeded")
	}
}

func TestServerConnectionTimeout(t *testing.T) {
	config := DefaultServerConfig()
	config.ConnTimeout = 200 * time.Millisecond
	config.HeartbeatInterval = 10 * time.Second // disable heartbeats from server
	srvHandler := newTestHandler()
	srv := startTestServer(t, config, srvHandler)

	// Connect a client with heartbeats disabled.
	cliConfig := DefaultClientConfig()
	cliConfig.HeartbeatInterval = 10 * time.Second
	cliConfig.ConnTimeout = 10 * time.Second
	cliHandler := newTestHandler()
	cli := connectTestClient(t, cliConfig, cliHandler, srv.Addr().String())

	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Force the server connection's lastRecvTime into the past.
	srv.conns.forEach(func(conn *Connection) {
		conn.mu.Lock()
		conn.lastRecvTime = time.Now().Add(-1 * time.Second)
		conn.mu.Unlock()
	})

	// Wait for timeout to be detected.
	select {
	case ev := <-srvHandler.disconnectCh:
		if ev.reason != DisconnectTimeout {
			t.Fatalf("reason = %v, want DisconnectTimeout", ev.reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for disconnect")
	}

	_ = cli // keep alive
}

func TestServerTickDriven(t *testing.T) {
	config := DefaultServerConfig()
	config.TickMode = TickDriven
	srvHandler := newTestHandler()
	srv := startTestServer(t, config, srvHandler)

	// Tick should work.
	if err := srv.Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Auto mode server should reject Tick().
	autoConfig := DefaultServerConfig()
	autoConfig.TickMode = TickAuto
	autoHandler := newTestHandler()
	autoSrv := startTestServer(t, autoConfig, autoHandler)

	if err := autoSrv.Tick(); err != ErrTickAutoMode {
		t.Fatalf("Tick on TickAuto = %v, want ErrTickAutoMode", err)
	}
}

func TestServerMessageRoundTrip(t *testing.T) {
	srvHandler := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), srvHandler)

	cliHandler := newTestHandler()
	cli := connectTestClient(t, DefaultClientConfig(), cliHandler, srv.Addr().String())

	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Client sends a message on channel 0 (reliable ordered).
	msg := []byte("hello server")
	if err := cli.Connection().Send(msg, 0); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Wait for server to receive.
	select {
	case ev := <-srvHandler.messageCh:
		if string(ev.data) != "hello server" {
			t.Fatalf("message = %q, want %q", ev.data, "hello server")
		}
		if ev.channel != 0 {
			t.Fatalf("channel = %d, want 0", ev.channel)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestServerBidirectionalMessages(t *testing.T) {
	srvHandler := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), srvHandler)

	cliHandler := newTestHandler()
	cli := connectTestClient(t, DefaultClientConfig(), cliHandler, srv.Addr().String())

	// Wait for connection.
	var srvConn *Connection
	select {
	case srvConn = <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Client → Server.
	if err := cli.Connection().Send([]byte("c2s"), 0); err != nil {
		t.Fatalf("Send c2s: %v", err)
	}

	select {
	case ev := <-srvHandler.messageCh:
		if string(ev.data) != "c2s" {
			t.Fatalf("c2s message = %q", ev.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for c2s message")
	}

	// Server → Client.
	if err := srvConn.Send([]byte("s2c"), 0); err != nil {
		t.Fatalf("Send s2c: %v", err)
	}

	select {
	case ev := <-cliHandler.messageCh:
		if string(ev.data) != "s2c" {
			t.Fatalf("s2c message = %q", ev.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for s2c message")
	}
}

func TestServerForEachConnection(t *testing.T) {
	srvHandler := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), srvHandler)

	cliHandler := newTestHandler()
	_ = connectTestClient(t, DefaultClientConfig(), cliHandler, srv.Addr().String())

	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	count := 0
	srv.ForEachConnection(func(conn *Connection) {
		count++
		if conn.State() != StateConnected {
			t.Errorf("connection state = %v, want StateConnected", conn.State())
		}
	})

	if count != 1 {
		t.Fatalf("ForEachConnection count = %d, want 1", count)
	}
}

func TestDisconnectRetry(t *testing.T) {
	srvHandler := newTestHandler()
	config := DefaultServerConfig()
	srv := startTestServer(t, config, srvHandler)

	cliHandler := newTestHandler()
	cli := connectTestClient(t, DefaultClientConfig(), cliHandler, srv.Addr().String())

	var srvConn *Connection
	select {
	case srvConn = <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Initiate graceful disconnect from server side.
	if err := srvConn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// State should be Disconnecting.
	if srvConn.State() != StateDisconnecting {
		t.Fatalf("state = %v, want StateDisconnecting", srvConn.State())
	}

	// Wait for the disconnect to complete via retry loop.
	select {
	case ev := <-srvHandler.disconnectCh:
		if ev.reason != DisconnectGraceful {
			t.Fatalf("reason = %v, want DisconnectGraceful", ev.reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for disconnect completion")
	}

	// Client should also see the disconnect.
	select {
	case ev := <-cliHandler.disconnectCh:
		if ev.reason != DisconnectGraceful {
			t.Fatalf("client reason = %v, want DisconnectGraceful", ev.reason)
		}
	case <-time.After(5 * time.Second):
		// Client might already have been cleaned up.
	}

	_ = cli // keep alive
}

// --- Stress tests ---

func TestStressConcurrentConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	srvHandler := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), srvHandler)

	const numClients = 20
	const msgsPerClient = 10
	clients := make([]*Client, numClients)
	for i := range numClients {
		h := newTestHandler()
		clients[i] = connectTestClient(t, DefaultClientConfig(), h, srv.Addr().String())
	}

	// Wait for all connections using connection count.
	deadline := time.After(10 * time.Second)
	for srv.ConnectionCount() < numClients {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for connections, got %d/%d", srv.ConnectionCount(), numClients)
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Send messages with a small stagger to avoid overwhelming localhost UDP buffers.
	for round := range msgsPerClient {
		for _, cli := range clients {
			if err := cli.Connection().Send([]byte(strings.Repeat("S", round+1)), 0); err != nil {
				t.Fatalf("Send: %v", err)
			}
		}
		time.Sleep(10 * time.Millisecond) // brief stagger between rounds
	}

	// Wait for all messages.
	total := numClients * msgsPerClient
	received := srvHandler.messageCount()
	timeout := time.After(30 * time.Second)
	for received < total {
		select {
		case <-srvHandler.messageCh:
			received++
		case <-timeout:
			t.Fatalf("timed out: received %d/%d messages", received, total)
		}
	}
}

func TestStressLargeMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	srvHandler := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), srvHandler)

	cliHandler := newTestHandler()
	cli := connectTestClient(t, DefaultClientConfig(), cliHandler, srv.Addr().String())

	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// 64 KB message = ~56 fragments on reliable ordered.
	largeMsg := make([]byte, 64*1024)
	for i := range largeMsg {
		largeMsg[i] = byte(i % 256)
	}
	if err := cli.Connection().Send(largeMsg, 0); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case ev := <-srvHandler.messageCh:
		if !bytes.Equal(ev.data, largeMsg) {
			t.Fatalf("message mismatch: got %d bytes, want %d", len(ev.data), len(largeMsg))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for large message")
	}
}

func TestStressPacketLoss(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	srvHandler := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), srvHandler)

	cliHandler := newTestHandler()
	_ = connectTestClient(t, DefaultClientConfig(), cliHandler, srv.Addr().String())

	var srvConn *Connection
	select {
	case srvConn = <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Wrap the server connection's sendFunc with a 10% random drop.
	srvConn.mu.Lock()
	origSendFunc := srvConn.sendFunc
	dropCounter := uint32(0)
	srvConn.sendFunc = func(data []byte) error {
		dropCounter++
		if dropCounter%10 == 0 {
			return nil // simulate drop
		}
		return origSendFunc(data)
	}
	srvConn.mu.Unlock()

	const numMessages = 50
	for i := range numMessages {
		msg := []byte(strings.Repeat("L", i+1))
		if err := srvConn.Send(msg, 0); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	received := 0
	timeout := time.After(30 * time.Second)
	for received < numMessages {
		select {
		case <-cliHandler.messageCh:
			received++
		case <-timeout:
			t.Fatalf("timed out: received %d/%d messages (packet loss recovery failed)", received, numMessages)
		}
	}
}

func TestStressHighThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	srvConfig := DefaultServerConfig()
	srvConfig.Congestion = CongestionAggressive
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	const numClients = 5
	const msgsPerChannel = 100
	cliConfig := DefaultClientConfig()
	cliConfig.Congestion = CongestionAggressive
	clients := make([]*Client, numClients)
	for i := range numClients {
		h := newTestHandler()
		clients[i] = connectTestClient(t, cliConfig, h, srv.Addr().String())
	}

	for range numClients {
		select {
		case <-srvHandler.connectCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for connect")
		}
	}

	// Each client sends 100 messages across all 4 channels.
	for _, cli := range clients {
		for ch := byte(0); ch < 4; ch++ {
			for range msgsPerChannel {
				if err := cli.Connection().Send([]byte("throughput"), ch); err != nil {
					t.Fatalf("Send: %v", err)
				}
			}
		}
	}

	// Total: 5 clients * 4 channels * 100 = 2000 messages.
	// Only count reliable channels for guaranteed delivery (channels 0, 1).
	// Unreliable channels may drop messages.
	reliableTotal := numClients * msgsPerChannel * 2 // channels 0 and 1
	received := 0
	timeout := time.After(30 * time.Second)
	for received < reliableTotal {
		select {
		case <-srvHandler.messageCh:
			received++
		case <-timeout:
			// Accept if we got at least the reliable messages.
			if received >= reliableTotal {
				return
			}
			t.Fatalf("timed out: received %d messages, want at least %d", received, reliableTotal)
		}
	}
	// Drain any remaining unreliable messages.
	drainTimeout := time.After(2 * time.Second)
	for {
		select {
		case <-srvHandler.messageCh:
			received++
		case <-drainTimeout:
			return
		}
	}
}

func TestServerConcurrentConnections(t *testing.T) {
	srvHandler := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), srvHandler)

	const numClients = 5
	clients := make([]*Client, numClients)
	for i := range numClients {
		h := newTestHandler()
		clients[i] = connectTestClient(t, DefaultClientConfig(), h, srv.Addr().String())
	}

	// Wait for all connections.
	for range numClients {
		select {
		case <-srvHandler.connectCh:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for connections, got %d", srvHandler.connectCount())
		}
	}

	if srv.ConnectionCount() != numClients {
		t.Fatalf("ConnectionCount = %d, want %d", srv.ConnectionCount(), numClients)
	}
}
