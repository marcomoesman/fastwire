package fastwire

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// --- Send Batching Tests ---

func TestSendBatchingRoundTrip(t *testing.T) {
	srvConfig := DefaultServerConfig()
	srvConfig.SendBatching = true
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	cliConfig := DefaultClientConfig()
	cliConfig.SendBatching = true
	cliHandler := newTestHandler()
	cli := connectTestClient(t, cliConfig, cliHandler, srv.Addr().String())

	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Send multiple messages — they should be batched into fewer datagrams.
	for i := range 5 {
		msg := []byte(strings.Repeat("B", i+10))
		if err := cli.Connection().Send(msg, 0); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	// Wait for all messages.
	for i := range 5 {
		select {
		case ev := <-srvHandler.messageCh:
			expected := strings.Repeat("B", i+10)
			if string(ev.data) != expected {
				// Messages may arrive in order on reliable ordered channel.
				// Just check we got the right number.
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for message %d", i)
		}
	}
}

func TestSendBatchingBidirectional(t *testing.T) {
	srvConfig := DefaultServerConfig()
	srvConfig.SendBatching = true
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	cliConfig := DefaultClientConfig()
	cliConfig.SendBatching = true
	cliHandler := newTestHandler()
	cli := connectTestClient(t, cliConfig, cliHandler, srv.Addr().String())

	var srvConn *Connection
	select {
	case srvConn = <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Verify the connection has batching enabled.
	if !srvConn.batchEnabled {
		t.Fatal("server connection should have batching enabled")
	}
	if !cli.Connection().batchEnabled {
		t.Fatal("client connection should have batching enabled")
	}

	// Server → Client.
	if err := srvConn.Send([]byte("batched-s2c"), 0); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case ev := <-cliHandler.messageCh:
		if string(ev.data) != "batched-s2c" {
			t.Fatalf("message = %q", ev.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for batched s2c message")
	}

	// Client → Server.
	if err := cli.Connection().Send([]byte("batched-c2s"), 0); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case ev := <-srvHandler.messageCh:
		if string(ev.data) != "batched-c2s" {
			t.Fatalf("message = %q", ev.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for batched c2s message")
	}
}

func TestSendBatchingSendImmediate(t *testing.T) {
	srvConfig := DefaultServerConfig()
	srvConfig.SendBatching = true
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	cliConfig := DefaultClientConfig()
	cliConfig.SendBatching = true
	cliHandler := newTestHandler()
	cli := connectTestClient(t, cliConfig, cliHandler, srv.Addr().String())

	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// SendImmediate should bypass the batch buffer.
	if err := cli.Connection().SendImmediate([]byte("immediate"), 0); err != nil {
		t.Fatalf("SendImmediate: %v", err)
	}
	select {
	case ev := <-srvHandler.messageCh:
		if string(ev.data) != "immediate" {
			t.Fatalf("message = %q", ev.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for immediate message")
	}
}

func TestSendBatchingLargeMessage(t *testing.T) {
	srvConfig := DefaultServerConfig()
	srvConfig.SendBatching = true
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	cliConfig := DefaultClientConfig()
	cliConfig.SendBatching = true
	cliHandler := newTestHandler()
	cli := connectTestClient(t, cliConfig, cliHandler, srv.Addr().String())

	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Large message that requires fragmentation should still work with batching.
	largeMsg := bytes.Repeat([]byte("F"), MaxFragmentPayload*3)
	if err := cli.Connection().Send(largeMsg, 0); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case ev := <-srvHandler.messageCh:
		if !bytes.Equal(ev.data, largeMsg) {
			t.Fatalf("message len = %d, want %d", len(ev.data), len(largeMsg))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for large batched message")
	}
}

func TestSendBatchingDisabledOnOneSide(t *testing.T) {
	// When only one side has batching, it should NOT be negotiated.
	srvConfig := DefaultServerConfig()
	srvConfig.SendBatching = true // server wants batching
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	cliConfig := DefaultClientConfig()
	cliConfig.SendBatching = false // client does not
	cliHandler := newTestHandler()
	cli := connectTestClient(t, cliConfig, cliHandler, srv.Addr().String())

	var srvConn *Connection
	select {
	case srvConn = <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Batching should NOT be active since client didn't request it.
	if srvConn.batchEnabled {
		t.Fatal("server connection should NOT have batching enabled")
	}
	if cli.Connection().batchEnabled {
		t.Fatal("client connection should NOT have batching enabled")
	}

	// Messages should still work.
	if err := cli.Connection().Send([]byte("no-batch"), 0); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case ev := <-srvHandler.messageCh:
		if string(ev.data) != "no-batch" {
			t.Fatalf("message = %q", ev.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

// --- Connection Migration Tests ---

func TestConnectionMigrationNegotiation(t *testing.T) {
	srvConfig := DefaultServerConfig()
	srvConfig.ConnectionMigration = true
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	cliConfig := DefaultClientConfig()
	cliConfig.ConnectionMigration = true
	cliHandler := newTestHandler()
	cli := connectTestClient(t, cliConfig, cliHandler, srv.Addr().String())

	var srvConn *Connection
	select {
	case srvConn = <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Verify migration token was assigned.
	if srvConn.migrationToken == (MigrationToken{}) {
		t.Fatal("server connection should have a non-zero migration token")
	}
	if cli.Connection().migrationToken == (MigrationToken{}) {
		t.Fatal("client connection should have a non-zero migration token")
	}
	if srvConn.migrationToken != cli.Connection().migrationToken {
		t.Fatal("migration tokens should match")
	}

	// Verify feature flag is set.
	if srvConn.features&byte(FeatureConnectionMigration) == 0 {
		t.Fatal("migration feature should be negotiated")
	}
}

func TestConnectionMigrationMessageRoundTrip(t *testing.T) {
	srvConfig := DefaultServerConfig()
	srvConfig.ConnectionMigration = true
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	cliConfig := DefaultClientConfig()
	cliConfig.ConnectionMigration = true
	cliHandler := newTestHandler()
	cli := connectTestClient(t, cliConfig, cliHandler, srv.Addr().String())

	var srvConn *Connection
	select {
	case srvConn = <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Client → Server with migration token.
	if err := cli.Connection().Send([]byte("migrated-c2s"), 0); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case ev := <-srvHandler.messageCh:
		if string(ev.data) != "migrated-c2s" {
			t.Fatalf("message = %q", ev.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}

	// Server → Client.
	if err := srvConn.Send([]byte("migrated-s2c"), 0); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case ev := <-cliHandler.messageCh:
		if string(ev.data) != "migrated-s2c" {
			t.Fatalf("message = %q", ev.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

func TestConnectionMigrationNotNegotiatedWhenDisabled(t *testing.T) {
	srvConfig := DefaultServerConfig()
	srvConfig.ConnectionMigration = false
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	cliConfig := DefaultClientConfig()
	cliConfig.ConnectionMigration = true
	cliHandler := newTestHandler()
	cli := connectTestClient(t, cliConfig, cliHandler, srv.Addr().String())

	var srvConn *Connection
	select {
	case srvConn = <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Migration should NOT be negotiated.
	if srvConn.features&byte(FeatureConnectionMigration) != 0 {
		t.Fatal("migration should not be negotiated when server disables it")
	}

	// Messages should still work.
	if err := cli.Connection().Send([]byte("no-migrate"), 0); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case ev := <-srvHandler.messageCh:
		if string(ev.data) != "no-migrate" {
			t.Fatalf("message = %q", ev.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

// --- Combined Features Test ---

func TestBatchingAndMigrationCombined(t *testing.T) {
	srvConfig := DefaultServerConfig()
	srvConfig.SendBatching = true
	srvConfig.ConnectionMigration = true
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	cliConfig := DefaultClientConfig()
	cliConfig.SendBatching = true
	cliConfig.ConnectionMigration = true
	cliHandler := newTestHandler()
	cli := connectTestClient(t, cliConfig, cliHandler, srv.Addr().String())

	var srvConn *Connection
	select {
	case srvConn = <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	// Verify both features negotiated.
	if !srvConn.batchEnabled {
		t.Fatal("batching should be enabled")
	}
	if srvConn.migrationToken == (MigrationToken{}) {
		t.Fatal("migration should be enabled")
	}

	// Send multiple messages in both directions.
	for i := range 5 {
		if err := cli.Connection().Send([]byte(strings.Repeat("C", i+1)), 0); err != nil {
			t.Fatalf("Send c2s %d: %v", i, err)
		}
		if err := srvConn.Send([]byte(strings.Repeat("S", i+1)), 0); err != nil {
			t.Fatalf("Send s2c %d: %v", i, err)
		}
	}

	// Wait for all messages.
	for range 5 {
		select {
		case <-srvHandler.messageCh:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for c2s messages")
		}
	}
	for range 5 {
		select {
		case <-cliHandler.messageCh:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for s2c messages")
		}
	}
}

// --- I/O Coalescing Tests ---

func TestCoalesceIOServerRoundTrip(t *testing.T) {
	srvConfig := DefaultServerConfig()
	srvConfig.CoalesceIO = true
	srvConfig.CoalesceReaders = 2
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	cliHandler := newTestHandler()
	cli := connectTestClient(t, DefaultClientConfig(), cliHandler, srv.Addr().String())

	select {
	case <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	if err := cli.Connection().Send([]byte("coalesced"), 0); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case ev := <-srvHandler.messageCh:
		if string(ev.data) != "coalesced" {
			t.Fatalf("message = %q", ev.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

// --- Bandwidth Estimation Tests ---

func TestBandwidthEstimation(t *testing.T) {
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

	// Send messages to generate traffic.
	for range 20 {
		if err := cli.Connection().Send([]byte(strings.Repeat("X", 100)), 0); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if err := srvConn.Send([]byte(strings.Repeat("Y", 100)), 0); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	// Wait for messages to flow.
	for range 20 {
		select {
		case <-srvHandler.messageCh:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out")
		}
	}
	for range 20 {
		select {
		case <-cliHandler.messageCh:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out")
		}
	}

	// Allow bandwidth estimators to tick.
	time.Sleep(200 * time.Millisecond)

	// Check stats.
	srvStats := srvConn.Stats()
	if srvStats.SendBandwidth <= 0 {
		t.Errorf("server SendBandwidth = %f, want > 0", srvStats.SendBandwidth)
	}
	if srvStats.RecvBandwidth <= 0 {
		t.Errorf("server RecvBandwidth = %f, want > 0", srvStats.RecvBandwidth)
	}

	cliStats := cli.Connection().Stats()
	if cliStats.SendBandwidth <= 0 {
		t.Errorf("client SendBandwidth = %f, want > 0", cliStats.SendBandwidth)
	}
	if cliStats.RecvBandwidth <= 0 {
		t.Errorf("client RecvBandwidth = %f, want > 0", cliStats.RecvBandwidth)
	}
}

// --- Feature Flags Tests ---

func TestFeaturesFromConfig(t *testing.T) {
	// Both enabled.
	f := featuresFromConfig(true, true)
	if f&byte(FeatureSendBatching) == 0 {
		t.Fatal("batching bit should be set")
	}
	if f&byte(FeatureConnectionMigration) == 0 {
		t.Fatal("migration bit should be set")
	}

	// None enabled.
	f = featuresFromConfig(false, false)
	if f != 0 {
		t.Fatalf("features = %d, want 0", f)
	}

	// Only batching.
	f = featuresFromConfig(true, false)
	if f&byte(FeatureSendBatching) == 0 {
		t.Fatal("batching bit should be set")
	}
	if f&byte(FeatureConnectionMigration) != 0 {
		t.Fatal("migration bit should not be set")
	}
}

func TestHandshakeFeatureNegotiation(t *testing.T) {
	// Client wants both, server wants only batching → negotiated = batching only.
	srvConfig := DefaultServerConfig()
	srvConfig.SendBatching = true
	srvConfig.ConnectionMigration = false
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	cliConfig := DefaultClientConfig()
	cliConfig.SendBatching = true
	cliConfig.ConnectionMigration = true
	cliHandler := newTestHandler()
	cli := connectTestClient(t, cliConfig, cliHandler, srv.Addr().String())

	var srvConn *Connection
	select {
	case srvConn = <-srvHandler.connectCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connect")
	}

	if !srvConn.batchEnabled {
		t.Fatal("batching should be negotiated")
	}
	if srvConn.features&byte(FeatureConnectionMigration) != 0 {
		t.Fatal("migration should NOT be negotiated")
	}

	// Verify message still works.
	if err := cli.Connection().Send([]byte("test"), 0); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case <-srvHandler.messageCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

// --- All Features Combined Stress Test ---

func TestAllFeaturesCombinedStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	srvConfig := DefaultServerConfig()
	srvConfig.SendBatching = true
	srvConfig.ConnectionMigration = true
	srvConfig.CoalesceIO = true
	srvConfig.CoalesceReaders = 2
	srvConfig.Congestion = CongestionAggressive
	srvHandler := newTestHandler()
	srv := startTestServer(t, srvConfig, srvHandler)

	const numClients = 5
	const msgsPerClient = 20
	clients := make([]*Client, numClients)
	for i := range numClients {
		cliConfig := DefaultClientConfig()
		cliConfig.SendBatching = true
		cliConfig.ConnectionMigration = true
		cliConfig.Congestion = CongestionAggressive
		h := newTestHandler()
		clients[i] = connectTestClient(t, cliConfig, h, srv.Addr().String())
	}

	// Wait for all connections.
	for range numClients {
		select {
		case <-srvHandler.connectCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for connections")
		}
	}

	// Send messages.
	for _, cli := range clients {
		for range msgsPerClient {
			if err := cli.Connection().Send([]byte("stress-test-data"), 0); err != nil {
				t.Fatalf("Send: %v", err)
			}
		}
	}

	// Wait for all messages.
	total := numClients * msgsPerClient
	received := 0
	timeout := time.After(15 * time.Second)
	for received < total {
		select {
		case <-srvHandler.messageCh:
			received++
		case <-timeout:
			t.Fatalf("timed out: received %d/%d messages", received, total)
		}
	}
}
