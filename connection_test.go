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

	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})

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
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})

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
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})

	// Just created — no heartbeat needed with a 1s interval.
	if conn.needsHeartbeat(1 * time.Second) {
		t.Fatal("should not need heartbeat immediately after creation")
	}

	// Force lastSendNano into the past.
	conn.lastSendNano.Store(time.Now().Add(-2 * time.Second).UnixNano())

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
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})

	conn.lastSendNano.Store(time.Now().Add(-2 * time.Second).UnixNano())

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
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})

	// Just created — not timed out with a 10s timeout.
	if conn.isTimedOut(10 * time.Second) {
		t.Fatal("should not be timed out immediately")
	}

	// Force lastRecvNano into the past.
	conn.lastRecvNano.Store(time.Now().Add(-15 * time.Second).UnixNano())

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
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})
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
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})
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
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})

	// Default layout has 4 channels (0-3). Channel 5 should fail.
	if err := conn.Send([]byte("hello"), 5); err != ErrInvalidChannel {
		t.Fatalf("Send on channel 5 = %v, want ErrInvalidChannel", err)
	}
}

func TestSendCopiesBuffer(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})

	buf := []byte("hello")
	if err := conn.Send(buf, 0); err != nil {
		t.Fatalf("Send: %v", err)
	}
	buf[0] = 'x'
	q := conn.drainSendQueue()
	if len(q) != 1 || string(q[0].data) != "hello" {
		t.Fatalf("Send did not copy: got %q", q)
	}
}

func TestSendOwnedTakesCallerBuffer(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})

	buf := []byte("hello")
	if err := conn.SendOwned(buf, 0); err != nil {
		t.Fatalf("SendOwned: %v", err)
	}
	buf[0] = 'x'
	q := conn.drainSendQueue()
	if len(q) != 1 || string(q[0].data) != "xello" {
		t.Fatalf("SendOwned should alias caller buffer: got %q", q)
	}
}

func TestCloseDuringSend(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})
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

// --- Pool buffer tests ---

func TestSendPooledUnreliable(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})
	conn.sendFunc = func(data []byte) error { return nil }

	// Channel 2 is Unreliable — buffer should be returned immediately.
	err := conn.sendMessage([]byte("unreliable data"), 2)
	if err != nil {
		t.Fatalf("sendMessage: %v", err)
	}
	// No pending packets on unreliable channel.
	if cnt := conn.channels[2].pendingCount(); cnt != 0 {
		t.Fatalf("unreliable pendingCount = %d, want 0", cnt)
	}
}

func TestSendPooledReliable(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})
	conn.sendFunc = func(data []byte) error { return nil }

	// Channel 0 is ReliableOrdered — buffer should be stored in pendingPacket.
	err := conn.sendMessage([]byte("reliable data"), 0)
	if err != nil {
		t.Fatalf("sendMessage: %v", err)
	}

	if cnt := conn.channels[0].pendingCount(); cnt != 1 {
		t.Fatalf("reliable pendingCount = %d, want 1", cnt)
	}

	// Verify the pending packet has a poolBuf.
	conn.channels[0].mu.Lock()
	p := conn.channels[0].pendingSend[0]
	conn.channels[0].mu.Unlock()

	if p.poolBuf == nil {
		t.Fatal("reliable pendingPacket should have non-nil poolBuf")
	}
	if len(p.raw) == 0 {
		t.Fatal("reliable pendingPacket should have non-empty raw")
	}
}

func TestSendPooledReliableAcked(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})
	conn.sendFunc = func(data []byte) error { return nil }

	// Send a reliable message.
	err := conn.sendMessage([]byte("ack me"), 0)
	if err != nil {
		t.Fatalf("sendMessage: %v", err)
	}

	// Verify pending.
	if cnt := conn.channels[0].pendingCount(); cnt != 1 {
		t.Fatalf("pendingCount = %d, want 1", cnt)
	}

	// Ack the packet (seq=1).
	acked := conn.channels[0].processAcks(1, 0, nil)
	if len(acked) != 1 {
		t.Fatalf("acked = %d, want 1", len(acked))
	}
	if conn.channels[0].pendingCount() != 0 {
		t.Fatal("pendingCount should be 0 after ack")
	}
}

func TestReleasePendingBuffersConnection(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})
	conn.sendFunc = func(data []byte) error { return nil }

	// Send messages on both reliable channels.
	for range 5 {
		_ = conn.sendMessage([]byte("data"), 0) // ReliableOrdered
		_ = conn.sendMessage([]byte("data"), 1) // ReliableUnordered
	}

	if conn.channels[0].pendingCount() != 5 {
		t.Fatalf("ch0 pendingCount = %d, want 5", conn.channels[0].pendingCount())
	}
	if conn.channels[1].pendingCount() != 5 {
		t.Fatalf("ch1 pendingCount = %d, want 5", conn.channels[1].pendingCount())
	}

	conn.releasePendingBuffers()

	if conn.channels[0].pendingCount() != 0 {
		t.Fatalf("ch0 pendingCount after release = %d", conn.channels[0].pendingCount())
	}
	if conn.channels[1].pendingCount() != 0 {
		t.Fatalf("ch1 pendingCount after release = %d", conn.channels[1].pendingCount())
	}
}

func TestNextFragmentIDWraps(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9000")
	send, _ := fwcrypto.NewCipherState(nil, CipherNone)
	recv, _ := fwcrypto.NewCipherState(nil, CipherNone)
	conn := newConnection(connectionInput{addr: addr, sendCipher: send, recvCipher: recv, suite: CipherNone, layout: DefaultChannelLayout(), congestionMode: CongestionConservative})

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
