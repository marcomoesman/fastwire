package fastwire

import (
	"net"
	"net/netip"
	"runtime"
	"sync"
	"time"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
)

// incomingPacket is a raw datagram received by the read loop.
type incomingPacket struct {
	addr netip.AddrPort
	data []byte
	n    int
	buf  []byte // original full buffer for pool return; nil if not pooled
}

// --- connection table ---

const connShardCount = 64

type connShard struct {
	mu    sync.RWMutex
	conns map[netip.AddrPort]*Connection
}

type connectionTable struct {
	shards [connShardCount]connShard
}

func newConnectionTable() *connectionTable {
	ct := &connectionTable{}
	for i := range ct.shards {
		ct.shards[i].conns = make(map[netip.AddrPort]*Connection)
	}
	return ct
}

func (ct *connectionTable) shardFor(addr netip.AddrPort) *connShard {
	b := addr.Addr().As16()
	h := uint32(2166136261) // FNV-1a offset basis
	for _, c := range b {
		h ^= uint32(c)
		h *= 16777619 // FNV-1a prime
	}
	p := addr.Port()
	h ^= uint32(p >> 8)
	h *= 16777619
	h ^= uint32(p & 0xFF)
	h *= 16777619
	return &ct.shards[h%connShardCount]
}

func (ct *connectionTable) get(addr netip.AddrPort) *Connection {
	s := ct.shardFor(addr)
	s.mu.RLock()
	c := s.conns[addr]
	s.mu.RUnlock()
	return c
}

func (ct *connectionTable) put(addr netip.AddrPort, conn *Connection) {
	s := ct.shardFor(addr)
	s.mu.Lock()
	s.conns[addr] = conn
	s.mu.Unlock()
}

func (ct *connectionTable) remove(addr netip.AddrPort) {
	s := ct.shardFor(addr)
	s.mu.Lock()
	delete(s.conns, addr)
	s.mu.Unlock()
}

func (ct *connectionTable) count() int {
	total := 0
	for i := range ct.shards {
		ct.shards[i].mu.RLock()
		total += len(ct.shards[i].conns)
		ct.shards[i].mu.RUnlock()
	}
	return total
}

func (ct *connectionTable) forEach(fn func(*Connection)) {
	for i := range ct.shards {
		ct.shards[i].mu.RLock()
		for _, c := range ct.shards[i].conns {
			fn(c)
		}
		ct.shards[i].mu.RUnlock()
	}
}

// --- pending table ---

type pendingTable struct {
	mu      sync.Mutex
	pending map[netip.AddrPort]*pendingHandshake
}

func newPendingTable() *pendingTable {
	return &pendingTable{
		pending: make(map[netip.AddrPort]*pendingHandshake),
	}
}

func (pt *pendingTable) get(addr netip.AddrPort) *pendingHandshake {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.pending[addr]
}

func (pt *pendingTable) put(addr netip.AddrPort, ph *pendingHandshake) {
	pt.mu.Lock()
	pt.pending[addr] = ph
	pt.mu.Unlock()
}

func (pt *pendingTable) remove(addr netip.AddrPort) {
	pt.mu.Lock()
	delete(pt.pending, addr)
	pt.mu.Unlock()
}

func (pt *pendingTable) cleanup(timeout time.Duration) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	for addr, ph := range pt.pending {
		if ph.isExpired(timeout) {
			delete(pt.pending, addr)
		}
	}
}

// --- write coalescer ---

type writeRequest struct {
	data []byte
	addr netip.AddrPort
}

// --- Server ---

// Server listens for FastWire connections over UDP.
type Server struct {
	config  ServerConfig
	conn    *net.UDPConn
	handler Handler

	conns   *connectionTable
	pending *pendingTable
	tokens  *tokenTable // migration tokens → connections

	incoming  chan incomingPacket
	closeCh   chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
	started   bool
	mu        sync.Mutex // protects started

	// Write coalescing.
	writeCh chan writeRequest

	// Read buffer pool.
	readPool *sync.Pool

	// Computed server features.
	serverFeatures byte
}

// NewServer creates a new Server bound to the given address.
// The server is not started until Start() is called.
func NewServer(addr string, config ServerConfig, handler Handler) (*Server, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}

	// Apply defaults for zero-value fields.
	if config.MTU == 0 {
		config.MTU = DefaultMTU
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = 1024
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
	if config.HandshakeTimeout == 0 {
		config.HandshakeTimeout = 5 * time.Second
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
	if config.CoalesceIO && config.CoalesceReaders <= 0 {
		config.CoalesceReaders = runtime.NumCPU()
	}

	sf := featuresFromConfig(config.SendBatching, config.ConnectionMigration)

	return &Server{
		config:         config,
		conn:           udpConn,
		handler:        handler,
		conns:          newConnectionTable(),
		pending:        newPendingTable(),
		tokens:         newTokenTable(),
		incoming:       make(chan incomingPacket, 4096),
		closeCh:        make(chan struct{}),
		serverFeatures: sf,
	}, nil
}

// Start launches the read loop and (if TickAuto) the tick loop.
func (s *Server) Start() error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return ErrAlreadyStarted
	}
	s.started = true
	s.mu.Unlock()

	s.readPool = newReadPool(s.config.MTU + fwcrypto.WireOverhead + MigrationTokenSize + BatchHeaderSize)

	// Start read goroutine(s).
	readers := 1
	if s.config.CoalesceIO && s.config.CoalesceReaders > 1 {
		readers = s.config.CoalesceReaders
	}
	for range readers {
		s.wg.Add(1)
		go s.readLoop()
	}

	// Start write coalescer if enabled.
	if s.config.CoalesceIO {
		s.writeCh = make(chan writeRequest, 4096)
		s.wg.Add(1)
		go s.writeLoop()
	}

	if s.config.TickMode == TickAuto {
		s.wg.Add(1)
		go s.tickLoop()
	}

	return nil
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return ErrServerNotStarted
	}
	s.mu.Unlock()

	s.closeOnce.Do(func() {
		close(s.closeCh)
		s.conn.Close()
	})

	s.wg.Wait()

	// Disconnect all remaining connections.
	s.conns.forEach(func(conn *Connection) {
		conn.releasePendingBuffers()

		conn.setState(StateDisconnected)
		s.handler.OnDisconnect(conn, DisconnectGraceful)
	})

	return nil
}

// Tick performs one tick cycle. Only valid in TickDriven mode.
func (s *Server) Tick() error {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()

	if !started {
		return ErrServerNotStarted
	}
	if s.config.TickMode == TickAuto {
		return ErrTickAutoMode
	}
	s.tick()
	return nil
}

// ConnectionCount returns the number of active connections.
func (s *Server) ConnectionCount() int {
	return s.conns.count()
}

// Addr returns the local address the server is listening on.
func (s *Server) Addr() net.Addr {
	return s.conn.LocalAddr()
}

// ForEachConnection calls fn for each active connection.
func (s *Server) ForEachConnection(fn func(*Connection)) {
	s.conns.forEach(fn)
}

// --- internal loops ---

func (s *Server) readLoop() {
	defer s.wg.Done()
	consecutiveErrors := 0
	for {
		buf := s.readPool.Get().([]byte)
		n, addr, err := s.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			s.readPool.Put(buf)
			select {
			case <-s.closeCh:
				return
			default:
			}
			consecutiveErrors++
			backoff := time.Duration(1<<min(consecutiveErrors, 7)) * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-s.closeCh:
				return
			}
			continue
		}
		consecutiveErrors = 0
		pkt := incomingPacket{
			addr: addr,
			data: buf[:n],
			n:    n,
			buf:  buf,
		}
		select {
		case s.incoming <- pkt:
		case <-s.closeCh:
			s.readPool.Put(buf)
			return
		}
	}
}

func (s *Server) writeLoop() {
	defer s.wg.Done()
	for {
		select {
		case req := <-s.writeCh:
			_, _ = s.conn.WriteToUDPAddrPort(req.data, req.addr)
			// Drain queued writes without blocking.
			s.drainWriteCh()
		case <-s.closeCh:
			return
		}
	}
}

func (s *Server) drainWriteCh() {
	for {
		select {
		case req := <-s.writeCh:
			_, _ = s.conn.WriteToUDPAddrPort(req.data, req.addr)
		default:
			return
		}
	}
}

func (s *Server) tickLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second / time.Duration(s.config.TickRate))
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.tick()
		case <-s.closeCh:
			return
		}
	}
}

func (s *Server) tick() {
	now := time.Now()

	// Drain incoming packets.
	for {
		select {
		case pkt := <-s.incoming:
			s.processIncoming(pkt)
			if pkt.buf != nil {
				s.readPool.Put(pkt.buf)
			}
		default:
			goto doneIncoming
		}
	}
doneIncoming:

	// Collect connections to tick (avoid holding read lock during mutations).
	var conns []*Connection
	s.conns.forEach(func(conn *Connection) {
		conns = append(conns, conn)
	})
	for _, conn := range conns {
		s.tickConnection(conn, now)
	}

	// Cleanup expired pending handshakes.
	s.pending.cleanup(s.config.HandshakeTimeout)
}

func (s *Server) processIncoming(pkt incomingPacket) {
	// 1. Known connection by address.
	conn := s.conns.get(pkt.addr)
	if conn != nil {
		s.processConnData(conn, pkt.data)
		return
	}

	// 2. Pending handshake (RESPONSE).
	ph := s.pending.get(pkt.addr)
	if ph != nil {
		newConn, err := serverProcessResponse(pkt.data, ph, pkt.addr)
		if err != nil {
			return
		}
		s.setupConnection(newConn, pkt.addr)
		s.pending.remove(pkt.addr)
		s.conns.put(pkt.addr, newConn)
		if newConn.features&byte(FeatureConnectionMigration) != 0 {
			s.tokens.put(newConn.migrationToken, newConn)
		}
		s.handler.OnConnect(newConn)
		return
	}

	// 3. Connection migration: token lookup.
	if s.config.ConnectionMigration && len(pkt.data) >= MigrationTokenSize {
		var token MigrationToken
		copy(token[:], pkt.data[:MigrationTokenSize])
		conn = s.tokens.get(token)
		if conn != nil {
			// Migration detected — update address.
			oldAddr := conn.RemoteAddr()
			s.conns.remove(oldAddr)
			conn.mu.Lock()
			conn.remoteAddr = pkt.addr
			conn.mu.Unlock()
			s.conns.put(pkt.addr, conn)
			s.setupSendFunc(conn, pkt.addr)
			s.processConnData(conn, pkt.data)
			return
		}
	}

	// 4. New connection (CONNECT).
	s.handleHandshake(pkt.addr, pkt.data)
}

// processConnData handles a raw datagram for an established connection.
// It strips the migration token prefix and dispatches to single or batched processing.
func (s *Server) processConnData(conn *Connection, data []byte) {
	// Strip migration token.
	if conn.features&byte(FeatureConnectionMigration) != 0 {
		if len(data) < MigrationTokenSize {
			return
		}
		data = data[MigrationTokenSize:]
	}

	conn.bytesReceived.Add(uint64(len(data)))
	conn.recvBW.Record(uint64(len(data)))

	// Handle batching.
	if conn.batchEnabled {
		packets, err := UnmarshalBatch(data)
		if err != nil {
			s.handler.OnError(conn, err)
			return
		}
		for _, pkt := range packets {
			s.processPacket(conn, pkt)
		}
	} else {
		s.processPacket(conn, data)
	}
}

func (s *Server) setupConnection(conn *Connection, addr netip.AddrPort) {
	s.setupSendFunc(conn, addr)
	conn.closeFunc = func() {
		s.removeConnection(conn)
	}
}

// removeConnection removes a connection from all server tables (address map + token table).
// Uses conn.RemoteAddr() so it always references the current address, even after migration.
func (s *Server) removeConnection(conn *Connection) {
	s.conns.remove(conn.RemoteAddr())
	if conn.features&byte(FeatureConnectionMigration) != 0 {
		s.tokens.remove(conn.migrationToken)
	}
}

func (s *Server) setupSendFunc(conn *Connection, addr netip.AddrPort) {
	if s.config.CoalesceIO && s.writeCh != nil {
		conn.mu.Lock()
		conn.sendFunc = func(data []byte) error {
			cp := make([]byte, len(data))
			copy(cp, data)
			conn.bytesSent.Add(uint64(len(data)))
			conn.sendBW.Record(uint64(len(data)))
			select {
			case s.writeCh <- writeRequest{data: cp, addr: conn.RemoteAddr()}:
				return nil
			case <-s.closeCh:
				return ErrServerClosed
			}
		}
		conn.mu.Unlock()
	} else {
		conn.mu.Lock()
		conn.sendFunc = func(data []byte) error {
			_, err := s.conn.WriteToUDPAddrPort(data, conn.RemoteAddr())
			if err == nil {
				conn.bytesSent.Add(uint64(len(data)))
				conn.sendBW.Record(uint64(len(data)))
			}
			return err
		}
		conn.mu.Unlock()
	}
}

func (s *Server) handleHandshake(addr netip.AddrPort, data []byte) {
	// Verify it's a control packet with CONNECT type.
	_, ct, _, err := parseControlPacket(data)
	if err != nil || ct != ControlConnect {
		return
	}

	// Check MaxConnections.
	if s.conns.count() >= s.config.MaxConnections {
		buf := make([]byte, 64)
		n, err := buildRejectPacket(buf, RejectServerFull)
		if err == nil {
			_, _ = s.conn.WriteToUDPAddrPort(buf[:n], addr)
		}
		return
	}

	ph, challengeData, err := serverProcessConnect(data, s.config.Compression, s.config.ChannelLayout, s.config.Congestion, s.config.InitialCwnd, s.serverFeatures)
	if err != nil {
		// If version mismatch, challengeData contains the VERSION_MISMATCH packet.
		if challengeData != nil {
			_, _ = s.conn.WriteToUDPAddrPort(challengeData, addr)
		}
		return
	}

	s.pending.put(addr, ph)
	_, _ = s.conn.WriteToUDPAddrPort(challengeData, addr)
}

// processPacket is the receive pipeline for a single encrypted packet.
func (s *Server) processPacket(conn *Connection, data []byte) {
	dstBuf := getDecryptBuffer()
	defer putDecryptBuffer(dstBuf)

	decrypted, err := fwcrypto.Decrypt(conn.recvCipher, data, dstBuf)
	if err != nil {
		s.handler.OnError(conn, err)
		return
	}

	hdr, n, err := UnmarshalHeader(decrypted)
	if err != nil {
		s.handler.OnError(conn, err)
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
			// Multi-channel ACK: parse entries and process acks for each channel.
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
			s.removeConnection(conn)
			s.handler.OnDisconnect(conn, DisconnectGraceful)
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
			s.handler.OnError(conn, err)
			return
		}
		assembled, complete, err := conn.reassembly.addFragment(fh, payload[fn:])
		if err != nil {
			s.handler.OnError(conn, err)
			return
		}
		if !complete {
			// Consume sequence slot for ordered delivery (nil = fragment placeholder).
			ch.deliver(hdr.Sequence, nil)
			return
		}
		decompressed, err := conn.compress.decompressPayload(assembled, fh.FragmentFlags)
		if err != nil {
			s.handler.OnError(conn, err)
			return
		}
		payload = decompressed
	}

	// Deliver.
	msgs := ch.deliver(hdr.Sequence, payload)
	for _, msg := range msgs {
		if msg != nil {
			s.handler.OnMessage(conn, msg, hdr.Channel)
		}
	}
}

// tickConnection runs per-connection tick logic.
func (s *Server) tickConnection(conn *Connection, now time.Time) {
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
			s.removeConnection(conn)
			s.handler.OnDisconnect(conn, DisconnectGraceful)
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
	if conn.isTimedOutAt(now, s.config.ConnTimeout) {
		conn.releasePendingBuffers()

		conn.setState(StateDisconnected)
		s.removeConnection(conn)
		s.handler.OnDisconnect(conn, DisconnectTimeout)
		return
	}

	// Retransmission check.
	rto := conn.rttState.RTO()
	if conn.cc.HalvesRTO() {
		rto /= 2
	}
	for _, ch := range conn.channels {
		retransmits, kill := ch.checkRetransmissions(now, rto, s.config.MaxRetransmits)
		if kill {
			conn.releasePendingBuffers()

			conn.setState(StateDisconnected)
			s.removeConnection(conn)
			s.handler.OnError(conn, ErrMaxRetransmits)
			s.handler.OnDisconnect(conn, DisconnectError)
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
			s.handler.OnError(conn, err)
		}
	}

	// Flush batch buffer.
	if conn.batchEnabled {
		if err := conn.flushBatch(s.config.MTU); err != nil {
			s.handler.OnError(conn, err)
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
	if conn.needsHeartbeatAt(now, s.config.HeartbeatInterval) {
		_ = conn.sendHeartbeat()
	}

	// Cleanup stale reassembly buffers.
	conn.reassembly.cleanup(s.config.FragmentTimeout)

	// Update bandwidth estimates.
	conn.tickBandwidth()
}
