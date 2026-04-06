package fastwire

import (
	"net"
	"sync"
	"time"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
)

// Client connects to a FastWire server.
type Client struct {
	config  ClientConfig
	conn    *net.UDPConn
	handler Handler

	server   *Connection           // single connection to the server
	clientKP fwcrypto.KeyPair      // stored for handshake processing

	incoming  chan incomingPacket
	closeCh   chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	mu              sync.Mutex
	connectDone     chan struct{}
	connectDoneOnce sync.Once
	connectErr      error
	connected       bool
	connectAborted  bool

	// Read buffer pool.
	readPool *sync.Pool

	// Computed client features for handshake.
	clientFeatures byte
}

// NewClient creates a new Client. Call Connect() to connect to a server.
func NewClient(config ClientConfig, handler Handler) (*Client, error) {
	// Apply defaults for zero-value fields.
	if config.MTU == 0 {
		config.MTU = DefaultMTU
	}
	if config.TickRate == 0 {
		config.TickRate = 100
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = 1 * time.Second
	}
	if config.ConnTimeout == 0 {
		config.ConnTimeout = 10 * time.Second
	}
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = 5 * time.Second
	}
	if config.ChannelLayout.Len() == 0 {
		config.ChannelLayout = DefaultChannelLayout()
	}
	if config.MaxRetransmits == 0 {
		config.MaxRetransmits = maxRetransmits
	}
	if config.FragmentTimeout == 0 {
		config.FragmentTimeout = DefaultFragmentTimeout
	}
	if config.InitialCwnd == 0 {
		config.InitialCwnd = DefaultInitialCwnd
	}

	cf := featuresFromConfig(config.SendBatching, config.ConnectionMigration)

	return &Client{
		config:         config,
		handler:        handler,
		closeCh:        make(chan struct{}),
		clientFeatures: cf,
	}, nil
}

// Connect initiates a connection to the given server address.
// Blocks until the handshake completes or times out.
func (c *Client) Connect(addr string) error {
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return ErrAlreadyConnected
	}
	c.mu.Unlock()

	// Resolve and dial.
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	udpConn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return err
	}
	c.conn = udpConn

	// Generate key pair.
	kp, err := fwcrypto.GenerateKeyPair()
	if err != nil {
		c.conn.Close()
		return err
	}
	c.clientKP = kp

	// Build and send CONNECT.
	connectPkt := &connectPacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      ApplicationVersion,
		PublicKey:       kp.Public.Bytes(),
		CipherPref:      c.config.CipherPreference,
		Compression:     c.config.Compression.Algorithm,
		Features:        c.clientFeatures,
	}
	if c.config.Compression.Algorithm == CompressionZstd && c.config.Compression.Dictionary != nil {
		hash := DictionaryHash(c.config.Compression.Dictionary)
		connectPkt.DictHash = hash[:]
	}

	buf := make([]byte, 128)
	n, err := buildConnectPacket(buf, connectPkt)
	if err != nil {
		c.conn.Close()
		return err
	}
	if _, err := c.conn.Write(buf[:n]); err != nil {
		c.conn.Close()
		return err
	}

	// Set up handshake signaling.
	c.incoming = make(chan incomingPacket, 1024)
	c.connectDone = make(chan struct{})
	c.connectDoneOnce = sync.Once{}
	c.connectErr = nil
	c.connectAborted = false

	c.readPool = newReadPool(c.config.MTU + fwcrypto.WireOverhead + MigrationTokenSize + BatchHeaderSize)

	// Start read loop.
	c.wg.Add(1)
	go c.readLoop()

	// Wait for handshake or timeout.
	select {
	case <-c.connectDone:
		if c.connectErr != nil {
			c.closeOnce.Do(func() {
				close(c.closeCh)
				c.conn.Close()
			})
			c.wg.Wait()
			return c.connectErr
		}
	case <-time.After(c.config.ConnectTimeout):
		c.mu.Lock()
		c.connectAborted = true
		c.mu.Unlock()
		c.closeOnce.Do(func() {
			close(c.closeCh)
			c.conn.Close()
		})
		c.wg.Wait()
		return ErrHandshakeTimeout
	}

	// Set up connection callbacks.
	conn := c.server
	c.setupSendFunc(conn)
	conn.closeFunc = func() {
		c.mu.Lock()
		c.connected = false
		c.server = nil
		c.mu.Unlock()
	}

	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()

	// Start tick loop if TickAuto.
	if c.config.TickMode == TickAuto {
		c.wg.Add(1)
		go c.tickLoop()
	}

	c.handler.OnConnect(conn)

	return nil
}

// setupSendFunc configures the connection's send function, wrapping with
// migration token prefix if needed.
func (c *Client) setupSendFunc(conn *Connection) {
	conn.sendFunc = func(data []byte) error {
		var buf []byte
		if conn.features&byte(FeatureConnectionMigration) != 0 {
			buf = make([]byte, MigrationTokenSize+len(data))
			copy(buf, conn.migrationToken[:])
			copy(buf[MigrationTokenSize:], data)
		} else {
			buf = data
		}
		_, err := c.conn.Write(buf)
		if err == nil {
			conn.bytesSent.Add(uint64(len(buf)))
			conn.sendBW.Record(uint64(len(buf)))
		}
		return err
	}
}

// Close disconnects from the server and releases resources.
func (c *Client) Close() error {
	c.mu.Lock()
	conn := c.server
	c.mu.Unlock()

	if conn != nil {
		// Use Connection.Close() which sets StateDisconnecting and sends first disconnect.
		_ = conn.Close()

		// For client teardown, force state to Disconnected and do full cleanup.
		conn.releasePendingBuffers()

		conn.setState(StateDisconnected)
		c.mu.Lock()
		c.connected = false
		c.server = nil
		c.mu.Unlock()
		c.handler.OnDisconnect(conn, DisconnectGraceful)
	}

	c.closeOnce.Do(func() {
		close(c.closeCh)
		if c.conn != nil {
			c.conn.Close()
		}
	})
	c.wg.Wait()
	return nil
}

// Tick performs one tick cycle. Only valid in TickDriven mode.
func (c *Client) Tick() error {
	if c.config.TickMode == TickAuto {
		return ErrTickAutoMode
	}
	c.tick()
	return nil
}

// Connection returns the current server connection, or nil if not connected.
func (c *Client) Connection() *Connection {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.server
}

// --- internal loops ---

func (c *Client) readLoop() {
	defer c.wg.Done()
	consecutiveErrors := 0
	for {
		buf := c.readPool.Get().([]byte)
		n, err := c.conn.Read(buf)
		if err != nil {
			c.readPool.Put(buf)
			select {
			case <-c.closeCh:
				return
			default:
			}
			consecutiveErrors++
			backoff := time.Duration(1<<min(consecutiveErrors, 7)) * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-c.closeCh:
				return
			}
			continue
		}
		consecutiveErrors = 0

		c.mu.Lock()
		connected := c.connected
		c.mu.Unlock()

		if !connected {
			// Still in handshake phase — process inline, return buffer immediately.
			c.processHandshakePacket(buf[:n])
			c.readPool.Put(buf)
			continue
		}

		pkt := incomingPacket{
			data: buf[:n],
			n:    n,
			buf:  buf,
		}
		select {
		case c.incoming <- pkt:
		case <-c.closeCh:
			c.readPool.Put(buf)
			return
		}
	}
}

func (c *Client) processHandshakePacket(data []byte) {
	// Try to parse as unencrypted control packet.
	_, ct, _, err := parseControlPacket(data)
	if err != nil {
		return
	}

	switch ct {
	case ControlChallenge:
		c.handleChallenge(data)
	case ControlVersionMismatch:
		c.connectErr = ErrVersionMismatch
		c.connectDoneOnce.Do(func() { close(c.connectDone) })
	case ControlReject:
		c.connectErr = ErrConnectionClosed
		c.connectDoneOnce.Do(func() { close(c.connectDone) })
	}
}

func (c *Client) handleChallenge(data []byte) {
	sendCipher, recvCipher, suite, encryptedResp, err := clientProcessChallenge(data, c.clientKP)
	if err != nil {
		c.connectErr = err
		c.connectDoneOnce.Do(func() { close(c.connectDone) })
		return
	}

	// Send encrypted RESPONSE.
	if _, err := c.conn.Write(encryptedResp); err != nil {
		c.connectErr = err
		c.connectDoneOnce.Do(func() { close(c.connectDone) })
		return
	}

	// Extract negotiated features from the challenge.
	_, _, ctrlPayload, _ := parseControlPacket(data)
	challenge, _ := unmarshalChallenge(ctrlPayload)

	// Resolve server address.
	serverAddr := c.conn.RemoteAddr().(*net.UDPAddr).AddrPort()

	// Create connection with negotiated features.
	conn := newConnection(serverAddr, sendCipher, recvCipher, suite,
		c.config.ChannelLayout, c.config.Compression, c.config.Congestion, c.config.InitialCwnd,
		challenge.Features, challenge.MigrationToken)

	c.mu.Lock()
	if c.connectAborted {
		c.mu.Unlock()
		return
	}
	c.server = conn
	c.mu.Unlock()

	c.connectDoneOnce.Do(func() { close(c.connectDone) })
}

func (c *Client) tickLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(time.Second / time.Duration(c.config.TickRate))
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.tick()
		case <-c.closeCh:
			return
		}
	}
}

func (c *Client) tick() {
	now := time.Now()

	// Drain incoming packets.
	for {
		select {
		case pkt := <-c.incoming:
			c.mu.Lock()
			conn := c.server
			c.mu.Unlock()
			if conn != nil {
				c.processConnData(conn, pkt.data)
			}
			if pkt.buf != nil {
				c.readPool.Put(pkt.buf)
			}
		default:
			goto doneIncoming
		}
	}
doneIncoming:

	c.mu.Lock()
	conn := c.server
	c.mu.Unlock()
	if conn != nil {
		c.tickConnection(conn, now)
	}
}

// processConnData handles a raw datagram for an established connection.
// Server→Client datagrams don't have migration tokens (only client→server does).
func (c *Client) processConnData(conn *Connection, data []byte) {
	conn.bytesReceived.Add(uint64(len(data)))
	conn.recvBW.Record(uint64(len(data)))

	// Handle batching.
	if conn.batchEnabled {
		packets, err := UnmarshalBatch(data)
		if err != nil {
			c.handler.OnError(conn, err)
			return
		}
		for _, pkt := range packets {
			c.processPacket(conn, pkt)
		}
	} else {
		c.processPacket(conn, data)
	}
}

// processPacket is the receive pipeline for a single encrypted packet.
func (c *Client) processPacket(conn *Connection, data []byte) {
	dstBuf := getDecryptBuffer()
	defer putDecryptBuffer(dstBuf)

	decrypted, err := fwcrypto.Decrypt(conn.recvCipher, data, dstBuf)
	if err != nil {
		c.handler.OnError(conn, err)
		return
	}

	hdr, n, err := UnmarshalHeader(decrypted)
	if err != nil {
		c.handler.OnError(conn, err)
		return
	}

	conn.touchRecv()

	ch := conn.channel(hdr.Channel)
	if ch == nil {
		return
	}

	// Process acks.
	acked := ch.processAcks(hdr.Ack, hdr.AckField, conn.rttState)
	if len(acked) > 0 {
		conn.cc.OnAck(len(acked))
		for _, seq := range acked {
			conn.loss.RecordAck(seq)
		}
	}

	// Control packet.
	if hdr.Flags&FlagControl != 0 {
		if len(decrypted[n:]) < 1 {
			return
		}
		ct := ControlType(decrypted[n])
		switch ct {
		case ControlHeartbeat:
			return
		case ControlMultiAck:
			entries, _, err := unmarshalMultiAck(decrypted[n+1:])
			if err != nil {
				return
			}
			for _, entry := range entries {
				aCh := conn.channel(entry.Channel)
				if aCh == nil {
					continue
				}
				acked := aCh.processAcks(entry.Ack, entry.AckField, conn.rttState)
				if len(acked) > 0 {
					conn.cc.OnAck(len(acked))
					for _, seq := range acked {
						conn.loss.RecordAck(seq)
					}
				}
			}
			return
		case ControlDisconnect:
			conn.releasePendingBuffers()
	
			conn.setState(StateDisconnected)
			c.mu.Lock()
			c.connected = false
			c.server = nil
			c.mu.Unlock()
			c.handler.OnDisconnect(conn, DisconnectGraceful)
			return
		default:
			return
		}
	}

	// Duplicate check.
	if !ch.recordReceive(hdr.Sequence) {
		return
	}

	payload := decrypted[n:]

	// Fragment handling.
	if hdr.Flags&FlagFragment != 0 {
		fh, fn, err := UnmarshalFragmentHeader(payload)
		if err != nil {
			c.handler.OnError(conn, err)
			return
		}
		assembled, complete, err := conn.reassembly.addFragment(fh, payload[fn:])
		if err != nil {
			c.handler.OnError(conn, err)
			return
		}
		if !complete {
			// Consume sequence slot for ordered delivery (nil = fragment placeholder).
			ch.deliver(hdr.Sequence, nil)
			return
		}
		decompressed, err := conn.compress.decompressPayload(assembled, fh.FragmentFlags)
		if err != nil {
			c.handler.OnError(conn, err)
			return
		}
		payload = decompressed
	}

	// Deliver.
	msgs := ch.deliver(hdr.Sequence, payload)
	for _, msg := range msgs {
		if msg != nil {
			c.handler.OnMessage(conn, msg, hdr.Channel)
		}
	}
}

// tickConnection runs per-connection tick logic.
func (c *Client) tickConnection(conn *Connection, now time.Time) {
	state := conn.State()

	// Handle disconnect retry.
	if state == StateDisconnecting {
		conn.mu.Lock()
		retries := conn.disconnectRetries
		nextRetry := conn.nextDisconnectRetry
		pkt := conn.disconnectPacket
		sf := conn.sendFunc
		conn.mu.Unlock()

		if retries >= maxDisconnectRetries {
			// Max retries reached — force close.
			conn.releasePendingBuffers()
	
			conn.setState(StateDisconnected)
			c.mu.Lock()
			c.connected = false
			c.server = nil
			c.mu.Unlock()
			c.handler.OnDisconnect(conn, DisconnectGraceful)
			return
		}

		if now.After(nextRetry) && pkt != nil && sf != nil {
			_ = sf(pkt)
			conn.mu.Lock()
			conn.disconnectRetries++
			conn.nextDisconnectRetry = now.Add(conn.rttState.RTO())
			conn.mu.Unlock()
		}
		return
	}

	if state != StateConnected {
		return
	}

	// Timeout check.
	if conn.isTimedOutAt(now, c.config.ConnTimeout) {
		conn.releasePendingBuffers()

		conn.setState(StateDisconnected)
		c.mu.Lock()
		c.connected = false
		c.server = nil
		c.mu.Unlock()
		c.handler.OnDisconnect(conn, DisconnectTimeout)
		return
	}

	// Retransmission check.
	rto := conn.rttState.RTO()
	if conn.cc.HalvesRTO() {
		rto /= 2
	}
	for _, ch := range conn.channels {
		retransmits, kill := ch.checkRetransmissions(now, rto, c.config.MaxRetransmits)
		if kill {
			conn.releasePendingBuffers()
	
			conn.setState(StateDisconnected)
			c.mu.Lock()
			c.connected = false
			c.server = nil
			c.mu.Unlock()
			c.handler.OnError(conn, ErrMaxRetransmits)
			c.handler.OnDisconnect(conn, DisconnectError)
			return
		}
		for _, p := range retransmits {
			encBuf := getSendBuffer(fwcrypto.NonceSize + len(p.raw) + fwcrypto.TagSize)
			encrypted, err := fwcrypto.Encrypt(conn.sendCipher, p.raw, encBuf)
			if err != nil {
				putSendBuffer(encBuf)
				continue
			}
			_ = conn.sendFramed(encrypted)
			putSendBuffer(encrypted[:cap(encrypted)])
			conn.lastSendNano.Store(now.UnixNano())
			conn.cc.OnLoss()
		}
	}

	// Drain send queue.
	msgs := conn.drainSendQueue()
	for i, msg := range msgs {
		if !conn.cc.CanSend(conn.InFlightCount()) {
			conn.requeue(msgs[i:])
			break
		}
		if err := conn.sendMessage(msg.data, msg.channel, now); err != nil {
			c.handler.OnError(conn, err)
		}
	}

	// Flush batch buffer.
	if conn.batchEnabled {
		if err := conn.flushBatch(c.config.MTU); err != nil {
			c.handler.OnError(conn, err)
		}
	}

	// Flush pending acks — coalesce all channels into a single multi-ack packet.
	var ackChannels []byte
	for i, ch := range conn.channels {
		if ch.clearNeedsAck() {
			ackChannels = append(ackChannels, byte(i))
		}
	}
	if len(ackChannels) > 0 {
		_ = conn.sendMultiChannelHeartbeat(ackChannels)
	}

	// Heartbeat.
	if conn.needsHeartbeatAt(now, c.config.HeartbeatInterval) {
		_ = conn.sendHeartbeat()
	}

	// Cleanup stale reassembly buffers.
	conn.reassembly.cleanup(c.config.FragmentTimeout)

	// Update bandwidth estimates.
	conn.tickBandwidth()
}
