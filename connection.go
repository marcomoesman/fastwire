package fastwire

import (
	"cmp"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
	"github.com/marcomoesman/fastwire/internal/bandwidth"
	"github.com/marcomoesman/fastwire/internal/congestion"
	"github.com/marcomoesman/fastwire/internal/rtt"
	"github.com/marcomoesman/fastwire/internal/stats"
)

// ConnState represents the state of a connection.
type ConnState byte

const (
	// StateDisconnected means the connection is not active.
	StateDisconnected ConnState = iota
	// StateConnecting means the handshake is in progress.
	StateConnecting
	// StateConnected means the connection is established and active.
	StateConnected
	// StateDisconnecting means a graceful disconnect is in progress.
	StateDisconnecting
)

func (s ConnState) String() string {
	switch s {
	case StateDisconnected:
		return "Disconnected"
	case StateConnecting:
		return "Connecting"
	case StateConnected:
		return "Connected"
	case StateDisconnecting:
		return "Disconnecting"
	default:
		return "Unknown"
	}
}

// DisconnectReason indicates why a connection was closed.
type DisconnectReason byte

const (
	DisconnectGraceful DisconnectReason = iota
	DisconnectTimeout
	DisconnectError
	DisconnectRejected
	DisconnectKicked
)

func (r DisconnectReason) String() string {
	switch r {
	case DisconnectGraceful:
		return "Graceful"
	case DisconnectTimeout:
		return "Timeout"
	case DisconnectError:
		return "Error"
	case DisconnectRejected:
		return "Rejected"
	case DisconnectKicked:
		return "Kicked"
	default:
		return "Unknown"
	}
}

// outgoingMessage is a message queued for sending on the next tick.
type outgoingMessage struct {
	data    []byte
	channel byte
}

// maxDisconnectRetries is the number of times a disconnect packet is retried.
const maxDisconnectRetries = 3

// maxBatchQueueSize is the maximum number of packets that can accumulate in
// the batch buffer between flushes. Packets beyond this limit are sent immediately.
const maxBatchQueueSize = 256

// Connection represents a FastWire connection to a remote peer.
type Connection struct {
	mu sync.Mutex
	// state is an atomic ConnState. remoteAddr is protected by mu; external
	// code must read it via RemoteAddr() — it changes during migration under mu.
	state      atomic.Uint32
	remoteAddr netip.AddrPort

	sendCipher *fwcrypto.CipherState
	recvCipher *fwcrypto.CipherState
	suite      CipherSuite

	channels   []*channel
	rttState   *rtt.State
	layout     ChannelLayout
	reassembly *reassemblyStore
	compress   *compressorPool
	cc         congestion.Controller

	fragmentID atomic.Uint32 // used as uint16, wrapping

	lastSendNano atomic.Int64 // UnixNano timestamp, lock-free
	lastRecvNano atomic.Int64 // UnixNano timestamp, lock-free
	createdAt    time.Time

	sendQueue []outgoingMessage
	sendFunc  func([]byte) error // injected by Server/Client to write UDP
	closeFunc func()             // injected by Server/Client to remove connection

	// Stats tracking.
	bytesSent     atomic.Uint64
	bytesReceived atomic.Uint64
	loss          *stats.LossTracker
	sendBW        *bandwidth.Estimator
	recvBW        *bandwidth.Estimator

	// Disconnect retry state.
	disconnectRetries   int
	nextDisconnectRetry time.Time
	disconnectPacket    []byte

	// Negotiated features.
	features       byte
	migrationToken MigrationToken

	// Send batching state.
	batchEnabled bool
	batchMu      sync.Mutex // protects batchBuf / batchIdle
	batchBuf     [][]byte
	batchIdle    [][]byte    // recycled empty batchBuf after flush
	skipBatch    atomic.Bool // true during SendImmediate
}

// connSettings groups server-side safety caps that are not negotiated during
// handshake. Zero fields mean "use the package default"; the helpers that
// consume these values apply those defaults.
type connSettings struct {
	maxReorderWindow     int
	maxReassemblyBuffers int
	maxReassemblyBytes   int
	fragmentTimeout      time.Duration
}

// connectionInput groups newConnection arguments to stay under the 2-arg
// limit for function signatures in this package.
type connectionInput struct {
	addr           netip.AddrPort
	sendCipher     *fwcrypto.CipherState
	recvCipher     *fwcrypto.CipherState
	suite          CipherSuite
	layout         ChannelLayout
	compression    CompressionConfig
	congestionMode CongestionMode
	initialCwnd    int
	features       byte
	token          MigrationToken
	settings       connSettings
}

// newConnection creates a Connection in StateConnected with the given cipher states.
// Zero-valued safety caps (fragment timeout, reassembly caps) default to the
// package defaults so callers that forget them still get bounded behavior.
// The reassembly store itself treats zero as unbounded — only this constructor
// applies defaults.
func newConnection(in connectionInput) *Connection {
	now := time.Now()
	cp, _ := newCompressorPool(in.compression)
	conn := &Connection{
		remoteAddr: in.addr,
		sendCipher: in.sendCipher,
		recvCipher: in.recvCipher,
		suite:      in.suite,
		channels:   newChannels(in.layout, cmp.Or(in.settings.maxReorderWindow, DefaultMaxReorderWindow)),
		rttState:   rtt.New(),
		layout:     in.layout,
		reassembly: newReassemblyStore(reassemblyStoreInput{
			fragmentTimeout: cmp.Or(in.settings.fragmentTimeout, DefaultFragmentTimeout),
			maxBuffers:      cmp.Or(in.settings.maxReassemblyBuffers, DefaultMaxReassemblyBuffers),
			maxBytes:        cmp.Or(in.settings.maxReassemblyBytes, DefaultMaxReassemblyBytes),
		}),
		compress:       cp,
		cc:             congestion.NewController(in.congestionMode, in.initialCwnd),
		createdAt:      now,
		loss:           stats.NewLossTracker(),
		sendBW:         bandwidth.New(),
		recvBW:         bandwidth.New(),
		features:       in.features,
		migrationToken: in.token,
		batchEnabled:   in.features&byte(FeatureSendBatching) != 0,
	}
	conn.state.Store(uint32(StateConnected))
	conn.lastSendNano.Store(now.UnixNano())
	conn.lastRecvNano.Store(now.UnixNano())
	return conn
}

// Send queues a copy of data for delivery on the given channel during the next tick.
// The caller may reuse or mutate data immediately after Send returns.
func (c *Connection) Send(data []byte, channel byte) error {
	return c.enqueueSend(data, channel, true)
}

// SendOwned queues data for delivery without copying. The caller must not
// mutate data until the next tick has drained the send queue (or SendOwned
// returns an error). Prefer Send when the buffer is reused.
func (c *Connection) SendOwned(data []byte, channel byte) error {
	return c.enqueueSend(data, channel, false)
}

func (c *Connection) enqueueSend(data []byte, channel byte, copyData bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ConnState(c.state.Load()) != StateConnected {
		return ErrConnectionClosed
	}
	if int(channel) >= len(c.channels) {
		return ErrInvalidChannel
	}
	if copyData {
		cp := make([]byte, len(data))
		copy(cp, data)
		data = cp
	}
	c.sendQueue = append(c.sendQueue, outgoingMessage{data: data, channel: channel})
	return nil
}

// SendImmediate sends data immediately, bypassing the tick queue.
// It goes through compress -> fragment -> encrypt -> send.
func (c *Connection) SendImmediate(data []byte, channel byte) error {
	c.mu.Lock()
	if ConnState(c.state.Load()) != StateConnected {
		c.mu.Unlock()
		return ErrConnectionClosed
	}
	if int(channel) >= len(c.channels) {
		c.mu.Unlock()
		return ErrInvalidChannel
	}
	sf := c.sendFunc
	c.mu.Unlock()

	if sf == nil {
		return ErrConnectionClosed
	}
	// Bypass batch buffer for immediate sends.
	c.skipBatch.Store(true)
	defer c.skipBatch.Store(false)
	return c.sendMessage(data, channel, time.Now())
}

// RTT returns the current smoothed round-trip time.
func (c *Connection) RTT() time.Duration {
	return c.rttState.SRTT()
}

// Stats returns a snapshot of connection statistics.
func (c *Connection) Stats() ConnectionStats {
	return ConnectionStats{
		RTT:              c.rttState.SRTT(),
		RTTVariance:      c.rttState.RTTVar(),
		PacketLoss:       c.loss.Loss(),
		BytesSent:        c.bytesSent.Load(),
		BytesReceived:    c.bytesReceived.Load(),
		CongestionWindow: c.cc.Window(),
		Uptime:           time.Since(c.createdAt),
		SendBandwidth:    c.sendBW.BytesPerSecond(),
		RecvBandwidth:    c.recvBW.BytesPerSecond(),
	}
}

// Close initiates a graceful disconnect with retry.
// The disconnect packet is retried by the tick loop; Close returns immediately.
func (c *Connection) Close() error {
	if !c.state.CompareAndSwap(uint32(StateConnected), uint32(StateDisconnecting)) {
		return ErrConnectionClosed
	}
	c.mu.Lock()
	sf := c.sendFunc
	c.mu.Unlock()

	// Build and store encrypted disconnect packet for retries.
	if sf != nil {
		buf := getSendBuffer(64)
		var ctrlBuf [1]byte
		n, err := marshalDisconnect(ctrlBuf[:])
		if err != nil {
			putSendBuffer(buf)
		} else {
			hdr := &PacketHeader{Flags: FlagControl}
			pn, err2 := buildControlPacket(buf, hdr, ctrlBuf[:n])
			if err2 != nil {
				putSendBuffer(buf)
			} else {
				encrypted, err3 := fwcrypto.Encrypt(c.sendCipher, buf[:pn], nil)
				putSendBuffer(buf) // plaintext consumed by Encrypt
				if err3 == nil {
					// Wrap in batch frame if needed.
					pktData := encrypted
					if c.batchEnabled {
						frame := getSendBuffer(BatchHeaderSize + BatchLenSize + len(encrypted))
						n := BatchHeaderSize + BatchLenSize + len(encrypted)
						frame[0] = 1
						frame[1] = byte(len(encrypted))
						frame[2] = byte(len(encrypted) >> 8)
						copy(frame[3:], encrypted)
						pktData = frame[:n]
					}
					c.mu.Lock()
					c.disconnectPacket = pktData
					c.disconnectRetries = 0
					c.nextDisconnectRetry = time.Now().Add(c.rttState.RTO())
					c.mu.Unlock()

					// Send first disconnect packet.
					_ = sf(pktData)
				}
			}
		}
	}

	return nil
}

// channel returns the channel for the given ID, or nil if out of range.
func (c *Connection) channel(id byte) *channel {
	if int(id) >= len(c.channels) {
		return nil
	}
	return c.channels[id]
}

// InFlightCount returns the total number of unacked reliable packets across all channels.
func (c *Connection) InFlightCount() int {
	count := 0
	for _, ch := range c.channels {
		count += ch.pendingCount()
	}
	return count
}

// State returns the current connection state.
func (c *Connection) State() ConnState {
	return ConnState(c.state.Load())
}

// RemoteAddr returns the remote address of the connection.
func (c *Connection) RemoteAddr() netip.AddrPort {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.remoteAddr
}

func (c *Connection) setState(s ConnState) {
	c.state.Store(uint32(s))
}

func (c *Connection) touchSend() {
	c.lastSendNano.Store(time.Now().UnixNano())
}

func (c *Connection) touchRecv() {
	c.lastRecvNano.Store(time.Now().UnixNano())
}

// needsHeartbeat reports whether no packet has been sent within the interval.
func (c *Connection) needsHeartbeat(interval time.Duration) bool {
	return ConnState(c.state.Load()) == StateConnected &&
		time.Duration(time.Now().UnixNano()-c.lastSendNano.Load()) >= interval
}

// isTimedOut reports whether no packet has been received within the timeout.
func (c *Connection) isTimedOut(timeout time.Duration) bool {
	return time.Duration(time.Now().UnixNano()-c.lastRecvNano.Load()) >= timeout
}

// needsHeartbeatAt is like needsHeartbeat but uses the provided time instead of time.Now().
func (c *Connection) needsHeartbeatAt(now time.Time, interval time.Duration) bool {
	return ConnState(c.state.Load()) == StateConnected &&
		time.Duration(now.UnixNano()-c.lastSendNano.Load()) >= interval
}

// isTimedOutAt is like isTimedOut but uses the provided time instead of time.Now().
func (c *Connection) isTimedOutAt(now time.Time, timeout time.Duration) bool {
	return time.Duration(now.UnixNano()-c.lastRecvNano.Load()) >= timeout
}

// nextFragmentID returns the next fragment ID, wrapping at uint16 max.
func (c *Connection) nextFragmentID() uint16 {
	return uint16(c.fragmentID.Add(1))
}

// releasePendingBuffers returns all pooled send buffers held by pending packets
// across all channels. Called during connection teardown.
func (c *Connection) releasePendingBuffers() {
	for _, ch := range c.channels {
		ch.releasePendingBuffers()
	}
	c.reassembly.reset()
	c.mu.Lock()
	pkt := c.disconnectPacket
	c.disconnectPacket = nil
	c.mu.Unlock()
	putSendBuffer(pkt)
	c.batchMu.Lock()
	for _, p := range c.batchBuf {
		putSendBuffer(p)
	}
	c.batchBuf = nil
	c.batchIdle = nil
	c.batchMu.Unlock()
}

// drainSendQueue returns and clears the current send queue.
func (c *Connection) drainSendQueue() []outgoingMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sendQueue) == 0 {
		return nil
	}
	q := c.sendQueue
	c.sendQueue = nil
	return q
}

// requeue puts unsent messages back at the front of the send queue.
func (c *Connection) requeue(msgs []outgoingMessage) {
	c.mu.Lock()
	c.sendQueue = append(msgs, c.sendQueue...)
	c.mu.Unlock()
}

// sendFramed wraps a single encrypted packet in batch frame format (when
// batching is enabled) and sends it via sendFunc. Use for non-batched sends
// (retransmits, heartbeats, disconnect, SendImmediate).
func (c *Connection) sendFramed(encrypted []byte) error {
	if c.batchEnabled {
		n := BatchHeaderSize + BatchLenSize + len(encrypted)
		frame := getSendBuffer(n)
		frame[0] = 1 // count = 1
		frame[1] = byte(len(encrypted))
		frame[2] = byte(len(encrypted) >> 8)
		copy(frame[3:], encrypted)
		err := c.sendFunc(frame[:n])
		putSendBuffer(frame)
		return err
	}
	return c.sendFunc(encrypted)
}

// writeEncrypted takes ownership of the encrypted buffer and either queues it
// in the batch buffer (freed later by flushBatch) or sends it synchronously
// and returns the buffer to the pool. Callers must not free encrypted after
// this returns, regardless of error.
func (c *Connection) writeEncrypted(encrypted []byte) error {
	if c.batchEnabled && !c.skipBatch.Load() {
		c.batchMu.Lock()
		if len(c.batchBuf) < maxBatchQueueSize {
			c.batchBuf = append(c.batchBuf, encrypted)
			c.batchMu.Unlock()
			return nil
		}
		c.batchMu.Unlock()
	}
	err := c.sendFramed(encrypted)
	putSendBuffer(encrypted[:cap(encrypted)])
	return err
}

// sendMessage sends a single message through the full send pipeline:
// compress -> fragment -> encrypt -> send.
func (c *Connection) sendMessage(data []byte, channelID byte, now ...time.Time) error {
	var ts time.Time
	if len(now) > 0 {
		ts = now[0]
	} else {
		ts = time.Now()
	}
	ch := c.channel(channelID)
	if ch == nil {
		return ErrInvalidChannel
	}

	compressed, fragFlags, err := c.compress.compressPayload(data)
	if err != nil {
		return err
	}

	needsFragment := len(compressed) > MaxFragmentPayload || fragFlags != 0

	if !needsFragment {
		return c.sendSinglePacket(ch, channelID, compressed, ts)
	}

	return c.sendFragmented(ch, channelID, compressed, fragFlags, ts)
}

// sendSinglePacket sends a non-fragmented packet.
func (c *Connection) sendSinglePacket(ch *channel, channelID byte, payload []byte, now time.Time) error {
	seq := ch.nextSequence()
	ack, ackField := ch.ackState()

	hdr := &PacketHeader{
		Channel:  channelID,
		Sequence: seq,
		Ack:      ack,
		AckField: ackField,
	}

	buf := getSendBuffer(MaxHeaderSize + len(payload))
	n, err := MarshalHeader(buf, hdr)
	if err != nil {
		putSendBuffer(buf)
		return err
	}
	copy(buf[n:], payload)
	plaintext := buf[:n+len(payload)]

	encBuf := getSendBuffer(fwcrypto.NonceSize + len(plaintext) + fwcrypto.TagSize)
	encrypted, err := fwcrypto.Encrypt(c.sendCipher, plaintext, encBuf)
	if err != nil {
		putSendBuffer(encBuf)
		putSendBuffer(buf)
		return err
	}

	// writeEncrypted takes ownership of encrypted regardless of error.
	if err := c.writeEncrypted(encrypted); err != nil {
		putSendBuffer(buf)
		return err
	}

	// Queue for retransmission if reliable; otherwise return buf to pool.
	if ch.mode == ReliableOrdered || ch.mode == ReliableUnordered {
		rto := c.rttState.RTO()
		if c.cc.HalvesRTO() {
			rto /= 2
		}
		ch.addPending(pendingPacket{
			raw:            plaintext,
			poolBuf:        buf[:cap(buf)],
			sendTime:       now,
			firstTransmit:  true,
			sequence:       seq,
			nextRetransmit: now.Add(rto),
		})
		c.loss.RecordSend(seq)
	} else {
		putSendBuffer(buf)
	}

	c.lastSendNano.Store(now.UnixNano())
	return nil
}

// sendFragmented splits a message into fragments and sends each.
func (c *Connection) sendFragmented(ch *channel, channelID byte, compressed []byte, fragFlags FragmentFlag, now time.Time) error {
	fragments, err := splitMessage(compressed, c.nextFragmentID(), fragFlags)
	if err != nil {
		return err
	}

	reliable := ch.mode == ReliableOrdered || ch.mode == ReliableUnordered

	for _, frag := range fragments {
		seq := ch.nextSequence()
		ack, ackField := ch.ackState()

		hdr := &PacketHeader{
			Flags:    FlagFragment,
			Channel:  channelID,
			Sequence: seq,
			Ack:      ack,
			AckField: ackField,
		}

		buf := getSendBuffer(MaxHeaderSize + len(frag))
		n, err := MarshalHeader(buf, hdr)
		if err != nil {
			putSendBuffer(buf)
			return err
		}
		copy(buf[n:], frag)
		plaintext := buf[:n+len(frag)]

		encBuf := getSendBuffer(fwcrypto.NonceSize + len(plaintext) + fwcrypto.TagSize)
		encrypted, err := fwcrypto.Encrypt(c.sendCipher, plaintext, encBuf)
		if err != nil {
			putSendBuffer(encBuf)
			putSendBuffer(buf)
			return err
		}

		// writeEncrypted takes ownership of encrypted regardless of error.
		if err := c.writeEncrypted(encrypted); err != nil {
			putSendBuffer(buf)
			return err
		}

		if reliable {
			rto := c.rttState.RTO()
			if c.cc.HalvesRTO() {
				rto /= 2
			}
			ch.addPending(pendingPacket{
				raw:            plaintext,
				poolBuf:        buf[:cap(buf)],
				sendTime:       now,
				firstTransmit:  true,
				sequence:       seq,
				nextRetransmit: now.Add(rto),
			})
			c.loss.RecordSend(seq)
		} else {
			putSendBuffer(buf)
		}
	}

	c.lastSendNano.Store(now.UnixNano())
	return nil
}

// sendHeartbeat sends a heartbeat control packet on channel 0.
func (c *Connection) sendHeartbeat() error {
	return c.sendMultiChannelHeartbeat([]byte{0})
}

// sendMultiChannelHeartbeat sends a single control packet carrying the ACK
// state for all specified channels.
func (c *Connection) sendMultiChannelHeartbeat(channelIDs []byte) error {
	entries := make([]multiAckEntry, 0, len(channelIDs))
	for _, chID := range channelIDs {
		ch := c.channel(chID)
		if ch == nil {
			continue
		}
		ack, ackField := ch.ackState()
		entries = append(entries, multiAckEntry{Channel: chID, Ack: ack, AckField: ackField})
	}
	if len(entries) == 0 {
		return nil
	}

	var ctrlBuf [256]byte
	n, err := marshalMultiAck(ctrlBuf[:], entries)
	if err != nil {
		return err
	}

	hdr := &PacketHeader{
		Flags:   FlagControl,
		Channel: 0,
	}

	buf := getSendBuffer(64 + n)
	pn, err := buildControlPacket(buf, hdr, ctrlBuf[:n])
	if err != nil {
		putSendBuffer(buf)
		return err
	}

	encBuf := getSendBuffer(fwcrypto.NonceSize + pn + fwcrypto.TagSize)
	encrypted, err := fwcrypto.Encrypt(c.sendCipher, buf[:pn], encBuf)
	putSendBuffer(buf)
	if err != nil {
		putSendBuffer(encBuf)
		return err
	}

	err = c.sendFramed(encrypted)
	putSendBuffer(encrypted[:cap(encrypted)])
	if err != nil {
		return err
	}
	c.touchSend()
	return nil
}

// tickBandwidth updates bandwidth estimators. Call once per tick.
func (c *Connection) tickBandwidth() {
	c.sendBW.Tick()
	c.recvBW.Tick()
}
