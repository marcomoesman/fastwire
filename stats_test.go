package fastwire

import (
	"testing"
	"time"
)

// --- ConnectionStats integration tests ---

func TestConnectionStats(t *testing.T) {
	srvHandler := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), srvHandler)

	cliHandler := newTestHandler()
	cli := connectTestClient(t, DefaultClientConfig(), cliHandler, srv.Addr().String())

	var srvConn *Connection
	select {
	case srvConn = <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Send messages in both directions to generate traffic.
	for i := range 5 {
		msg := []byte("ping")
		if err := cli.Connection().Send(msg, 0); err != nil {
			t.Fatalf("client Send %d: %v", i, err)
		}
		if err := srvConn.Send([]byte("pong"), 0); err != nil {
			t.Fatalf("server Send %d: %v", i, err)
		}
	}

	// Wait for messages to be processed.
	for range 5 {
		select {
		case <-srvHandler.messageCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for server messages")
		}
	}
	for range 5 {
		select {
		case <-cliHandler.messageCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for client messages")
		}
	}

	// Allow time for acks to be processed.
	time.Sleep(200 * time.Millisecond)

	// Check server-side connection stats.
	srvStats := srvConn.Stats()
	if srvStats.BytesSent == 0 {
		t.Error("server BytesSent = 0, want > 0")
	}
	if srvStats.BytesReceived == 0 {
		t.Error("server BytesReceived = 0, want > 0")
	}
	if srvStats.Uptime <= 0 {
		t.Error("server Uptime <= 0, want > 0")
	}
	if srvStats.CongestionWindow <= 0 {
		t.Errorf("server CongestionWindow = %d, want > 0", srvStats.CongestionWindow)
	}

	// Check client-side connection stats.
	cliStats := cli.Connection().Stats()
	if cliStats.BytesSent == 0 {
		t.Error("client BytesSent = 0, want > 0")
	}
	if cliStats.BytesReceived == 0 {
		t.Error("client BytesReceived = 0, want > 0")
	}
	if cliStats.Uptime <= 0 {
		t.Error("client Uptime <= 0, want > 0")
	}
}

func TestConnectionStatsUptime(t *testing.T) {
	srvHandler := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), srvHandler)

	cliHandler := newTestHandler()
	cli := connectTestClient(t, DefaultClientConfig(), cliHandler, srv.Addr().String())

	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	uptime1 := cli.Connection().Stats().Uptime
	time.Sleep(100 * time.Millisecond)
	uptime2 := cli.Connection().Stats().Uptime

	if uptime2 <= uptime1 {
		t.Fatalf("uptime did not increase: %v -> %v", uptime1, uptime2)
	}
}

func TestConnectionStatsPacketLoss(t *testing.T) {
	srvHandler := newTestHandler()
	srv := startTestServer(t, DefaultServerConfig(), srvHandler)

	cliHandler := newTestHandler()
	cli := connectTestClient(t, DefaultClientConfig(), cliHandler, srv.Addr().String())

	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Send messages and wait for acks.
	for range 10 {
		if err := cli.Connection().Send([]byte("test"), 0); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	// Wait for delivery and ack processing.
	for range 10 {
		select {
		case <-srvHandler.messageCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for messages")
		}
	}
	time.Sleep(200 * time.Millisecond)

	// With all messages delivered and acked, loss should be 0 or very low.
	stats := cli.Connection().Stats()
	if stats.PacketLoss > 0.5 {
		t.Errorf("PacketLoss = %f, expected low loss", stats.PacketLoss)
	}
}
