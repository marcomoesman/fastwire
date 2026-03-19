package fastwire

import (
	"net/netip"
	"sync"
	"testing"
	"time"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
)

func TestNewConnection(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)

	conn := newConnection(addr, send, recv, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})

	if conn.State() != StateConnected {
		t.Fatalf("state = %v, want StateConnected", conn.State())
	}
	if conn.RemoteAddr() != addr {
		t.Fatalf("addr = %v, want %v", conn.RemoteAddr(), addr)
	}
}

func TestConnectionStateTransitions(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(addr, send, recv, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})

	// Connected → Disconnecting
	conn.setState(StateDisconnecting)
	if conn.State() != StateDisconnecting {
		t.Fatalf("state = %v, want StateDisconnecting", conn.State())
	}

	// Disconnecting → Disconnected
	conn.setState(StateDisconnected)
	if conn.State() != StateDisconnected {
		t.Fatalf("state = %v, want StateDisconnected", conn.State())
	}
}

func TestConnectionStateString(t *testing.T) {
	tests := []struct {
		state ConnState
		want  string
	}{
		{StateDisconnected, "Disconnected"},
		{StateConnecting, "Connecting"},
		{StateConnected, "Connected"},
		{StateDisconnecting, "Disconnecting"},
		{ConnState(99), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("ConnState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestDisconnectReasonString(t *testing.T) {
	tests := []struct {
		reason DisconnectReason
		want   string
	}{
		{DisconnectGraceful, "Graceful"},
		{DisconnectTimeout, "Timeout"},
		{DisconnectError, "Error"},
		{DisconnectRejected, "Rejected"},
		{DisconnectKicked, "Kicked"},
		{DisconnectReason(99), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.reason.String(); got != tt.want {
			t.Errorf("DisconnectReason(%d).String() = %q, want %q", tt.reason, got, tt.want)
		}
	}
}

func TestConnectionHeartbeatNeeded(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(addr, send, recv, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})

	// Just created — no heartbeat needed with a 1s interval.
	if conn.needsHeartbeat(1 * time.Second) {
		t.Fatal("should not need heartbeat immediately after creation")
	}

	// Force lastSendTime into the past.
	conn.mu.Lock()
	conn.lastSendTime = time.Now().Add(-2 * time.Second)
	conn.mu.Unlock()

	if !conn.needsHeartbeat(1 * time.Second) {
		t.Fatal("should need heartbeat after 2s with 1s interval")
	}

	// Touch send — no longer needs heartbeat.
	conn.touchSend()
	if conn.needsHeartbeat(1 * time.Second) {
		t.Fatal("should not need heartbeat after touchSend")
	}
}

func TestConnectionHeartbeatOnlyWhenConnected(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(addr, send, recv, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})

	conn.mu.Lock()
	conn.lastSendTime = time.Now().Add(-2 * time.Second)
	conn.mu.Unlock()

	// Disconnecting state should not need heartbeat.
	conn.setState(StateDisconnecting)
	if conn.needsHeartbeat(1 * time.Second) {
		t.Fatal("should not need heartbeat in Disconnecting state")
	}
}

func TestConnectionTimeout(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(addr, send, recv, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})

	// Just created — not timed out with a 10s timeout.
	if conn.isTimedOut(10 * time.Second) {
		t.Fatal("should not be timed out immediately")
	}

	// Force lastRecvTime into the past.
	conn.mu.Lock()
	conn.lastRecvTime = time.Now().Add(-15 * time.Second)
	conn.mu.Unlock()

	if !conn.isTimedOut(10 * time.Second) {
		t.Fatal("should be timed out after 15s with 10s timeout")
	}

	// Touch recv — no longer timed out.
	conn.touchRecv()
	if conn.isTimedOut(10 * time.Second) {
		t.Fatal("should not be timed out after touchRecv")
	}
}

// --- Edge case tests ---

func TestDoubleClose(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(addr, send, recv, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})
	conn.sendFunc = func(data []byte) error { return nil }

	// First close should succeed.
	if err := conn.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second close should return ErrConnectionClosed.
	if err := conn.Close(); err != ErrConnectionClosed {
		t.Fatalf("second Close = %v, want ErrConnectionClosed", err)
	}
}

func TestSendOnClosedConnection(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(addr, send, recv, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})
	conn.sendFunc = func(data []byte) error { return nil }

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := conn.Send([]byte("hello"), 0); err != ErrConnectionClosed {
		t.Fatalf("Send after Close = %v, want ErrConnectionClosed", err)
	}
}

func TestSendOnInvalidChannel(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(addr, send, recv, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})

	// Default layout has 4 channels (0-3). Channel 5 should fail.
	if err := conn.Send([]byte("hello"), 5); err != ErrInvalidChannel {
		t.Fatalf("Send on channel 5 = %v, want ErrInvalidChannel", err)
	}
}

func TestCloseDuringSend(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(addr, send, recv, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})
	conn.sendFunc = func(data []byte) error { return nil }

	var wg sync.WaitGroup
	// Run Send() and Close() concurrently — should not panic.
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 100 {
			_ = conn.Send([]byte("data"), 0)
		}
	}()
	go func() {
		defer wg.Done()
		_ = conn.Close()
	}()
	wg.Wait()
}

func TestConcurrentSend(t *testing.T) {
	srvHandler := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), srvHandler)

	cliHandler := newTestHandler()
	cli := connectTestClient(t, DefaultClientConfig(), cliHandler, srv.Addr().String())

	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// 10 goroutines send concurrently — verifies no data races or panics.
	const goroutines = 10
	const msgsPerGoroutine = 50
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range msgsPerGoroutine {
				_ = cli.Connection().Send([]byte("msg"), 0)
				time.Sleep(time.Millisecond) // stagger to avoid UDP buffer overflow
			}
		}()
	}
	wg.Wait()

	// Wait for all messages to arrive.
	total := goroutines * msgsPerGoroutine
	received := 0
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

func TestNextFragmentIDWraps(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(addr, send, recv, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})

	// First ID should be 1.
	id := conn.nextFragmentID()
	if id != 1 {
		t.Fatalf("first fragment ID = %d, want 1", id)
	}

	// Force the counter near uint16 max to test wrapping.
	conn.fragmentID.Store(0xFFFE)
	id = conn.nextFragmentID()
	if id != 0xFFFF {
		t.Fatalf("fragment ID = %d, want %d", id, 0xFFFF)
	}
	id = conn.nextFragmentID()
	if id != 0 {
		t.Fatalf("wrapped fragment ID = %d, want 0", id)
	}
}
