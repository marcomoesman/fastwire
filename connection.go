package fastwire

import (
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

// Connection represents a FastWire connection to a remote peer.
type Connection struct {
	mu         sync.Mutex
	state      ConnState
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

	lastSendTime time.Time
	lastRecvTime time.Time
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
	batchBuf     [][]byte
	skipBatch    atomic.Bool // true during SendImmediate
}

// newConnection creates a Connection in StateConnected with the given cipher states.
func newConnection(addr netip.AddrPort, sendCipher, recvCipher *fwcrypto.CipherState, suite CipherSuite, layout ChannelLayout, compression CompressionConfig, congestionMode CongestionMode, initialCwnd int, features byte, token MigrationToken) *Connection {
	now := time.Now()
	cp, _ := newCompressorPool(compression)
	return &Connection{
		state:          StateConnected,
		remoteAddr:     addr,
		sendCipher:     sendCipher,
		recvCipher:     recvCipher,
		suite:          suite,
		channels:       newChannels(layout),
		rttState:       rtt.New(),
		layout:         layout,
		reassembly:     newReassemblyStore(),
		compress:       cp,
		cc:             congestion.NewController(congestionMode, initialCwnd),
		lastSendTime:   now,
		lastRecvTime:   now,
		createdAt:      now,
		loss:           stats.NewLossTracker(),
		sendBW:         bandwidth.New(),
		recvBW:         bandwidth.New(),
		features:       features,
		migrationToken: token,
		batchEnabled:   features&byte(FeatureSendBatching) != 0,
	}
}

// Send queues data for delivery on the given channel during the next tick.
func (c *Connection) Send(data []byte, channel byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StateConnected {
		return ErrConnectionClosed
	}
	if int(channel) >= len(c.channels) {
		return ErrInvalidChannel
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	c.sendQueue = append(c.sendQueue, outgoingMessage{data: cp, channel: channel})
	return nil
}

// SendImmediate sends data immediately, bypassing the tick queue.
// It goes through compress -> fragment -> encrypt -> send.
func (c *Connection) SendImmediate(data []byte, channel byte) error {
	c.mu.Lock()
	if c.state != StateConnected {
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
	return c.sendMessage(data, channel)
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
	c.mu.Lock()
	if c.state != StateConnected {
		c.mu.Unlock()
		return ErrConnectionClosed
	}
	c.state = StateDisconnecting
	sf := c.sendFunc
	c.mu.Unlock()

	// Build and store encrypted disconnect packet for retries.
	if sf != nil {
		buf := make([]byte, 64)
		var ctrlBuf [1]byte
		n, err := marshalDisconnect(ctrlBuf[:])
		if err == nil {
			hdr := &PacketHeader{Flags: FlagControl}
			pn, err2 := buildControlPacket(buf, hdr, ctrlBuf[:n])
			if err2 == nil {
				encrypted, err3 := fwcrypto.Encrypt(c.sendCipher, buf[:pn], nil)
				if err3 == nil {
					// Wrap in batch frame if needed.
					pktData := encrypted
					if c.batchEnabled {
						frame := make([]byte, BatchHeaderSize+BatchLenSize+len(encrypted))
						frame[0] = 1
						frame[1] = byte(len(encrypted))
						frame[2] = byte(len(encrypted) >> 8)
						copy(frame[3:], encrypted)
						pktData = frame
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

// inFlightCount returns the total number of unacked reliable packets across all channels.
func (c *Connection) inFlightCount() int {
	count := 0
	for _, ch := range c.channels {
		count += ch.pendingCount()
	}
	return count
}

// State returns the current connection state.
func (c *Connection) State() ConnState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// RemoteAddr returns the remote address of the connection.
func (c *Connection) RemoteAddr() netip.AddrPort {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.remoteAddr
}

func (c *Connection) setState(s ConnState) {
	c.mu.Lock()
	c.state = s
	c.mu.Unlock()
}

func (c *Connection) touchSend() {
	c.mu.Lock()
	c.lastSendTime = time.Now()
	c.mu.Unlock()
}

func (c *Connection) touchRecv() {
	c.mu.Lock()
	c.lastRecvTime = time.Now()
	c.mu.Unlock()
}

// needsHeartbeat reports whether no packet has been sent within the interval.
func (c *Connection) needsHeartbeat(interval time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == StateConnected && time.Since(c.lastSendTime) >= interval
}

// isTimedOut reports whether no packet has been received within the timeout.
func (c *Connection) isTimedOut(timeout time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.lastRecvTime) >= timeout
}

// nextFragmentID returns the next fragment ID, wrapping at uint16 max.
func (c *Connection) nextFragmentID() uint16 {
	return uint16(c.fragmentID.Add(1))
}

// queueMessage adds a message to the send queue (caller holds no lock).
func (c *Connection) queueMessage(data []byte, ch byte) {
	c.mu.Lock()
	c.sendQueue = append(c.sendQueue, outgoingMessage{data: data, channel: ch})
	c.mu.Unlock()
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
		frame := make([]byte, BatchHeaderSize+BatchLenSize+len(encrypted))
		frame[0] = 1 // count = 1
		frame[1] = byte(len(encrypted))
		frame[2] = byte(len(encrypted) >> 8)
		copy(frame[3:], encrypted)
		return c.sendFunc(frame)
	}
	return c.sendFunc(encrypted)
}

// writeEncrypted sends an encrypted packet, either adding to the batch buffer
// or sending immediately (wrapped in frame if batching is active).
func (c *Connection) writeEncrypted(encrypted []byte) error {
	if c.batchEnabled && !c.skipBatch.Load() {
		cp := make([]byte, len(encrypted))
		copy(cp, encrypted)
		c.batchBuf = append(c.batchBuf, cp)
		return nil
	}
	return c.sendFramed(encrypted)
}

// sendMessage sends a single message through the full send pipeline:
// compress -> fragment -> encrypt -> send.
func (c *Connection) sendMessage(data []byte, channelID byte) error {
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
		return c.sendSinglePacket(ch, channelID, compressed)
	}

	return c.sendFragmented(ch, channelID, compressed, fragFlags)
}

// sendSinglePacket sends a non-fragmented packet.
func (c *Connection) sendSinglePacket(ch *channel, channelID byte, payload []byte) error {
	seq := ch.nextSequence()
	ack, ackField := ch.ackState()

	hdr := &PacketHeader{
		Channel:  channelID,
		Sequence: seq,
		Ack:      ack,
		AckField: ackField,
	}

	buf := make([]byte, MaxHeaderSize+len(payload))
	n, err := MarshalHeader(buf, hdr)
	if err != nil {
		return err
	}
	copy(buf[n:], payload)
	plaintext := buf[:n+len(payload)]

	encrypted, err := fwcrypto.Encrypt(c.sendCipher, plaintext, nil)
	if err != nil {
		return err
	}

	if err := c.writeEncrypted(encrypted); err != nil {
		return err
	}

	// Queue for retransmission if reliable.
	if ch.mode == ReliableOrdered || ch.mode == ReliableUnordered {
		now := time.Now()
		rto := c.rttState.RTO()
		if c.cc.HalvesRTO() {
			rto /= 2
		}
		raw := make([]byte, len(plaintext))
		copy(raw, plaintext)
		ch.addPending(pendingPacket{
			raw:            raw,
			sendTime:       now,
			firstTransmit:  true,
			sequence:       seq,
			nextRetransmit: now.Add(rto),
		})
		c.loss.RecordSend(seq)
	}

	c.touchSend()
	return nil
}

// sendFragmented splits a message into fragments and sends each.
func (c *Connection) sendFragmented(ch *channel, channelID byte, compressed []byte, fragFlags FragmentFlag) error {
	fragments, err := splitMessage(compressed, c.nextFragmentID(), fragFlags)
	if err != nil {
		return err
	}

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

		buf := make([]byte, MaxHeaderSize+len(frag))
		n, err := MarshalHeader(buf, hdr)
		if err != nil {
			return err
		}
		copy(buf[n:], frag)
		plaintext := buf[:n+len(frag)]

		encrypted, err := fwcrypto.Encrypt(c.sendCipher, plaintext, nil)
		if err != nil {
			return err
		}

		if err := c.writeEncrypted(encrypted); err != nil {
			return err
		}

		if ch.mode == ReliableOrdered || ch.mode == ReliableUnordered {
			now := time.Now()
			rto := c.rttState.RTO()
			if c.cc.HalvesRTO() {
				rto /= 2
			}
			raw := make([]byte, len(plaintext))
			copy(raw, plaintext)
			ch.addPending(pendingPacket{
				raw:            raw,
				sendTime:       now,
				firstTransmit:  true,
				sequence:       seq,
				nextRetransmit: now.Add(rto),
			})
			c.loss.RecordSend(seq)
		}
	}

	c.touchSend()
	return nil
}

// sendHeartbeat sends a heartbeat control packet on channel 0.
func (c *Connection) sendHeartbeat() error {
	return c.sendHeartbeatOnChannel(0)
}

// sendHeartbeatOnChannel sends a heartbeat control packet carrying the ack state
// for the specified channel.
func (c *Connection) sendHeartbeatOnChannel(channelID byte) error {
	ch := c.channel(channelID)
	if ch == nil {
		return ErrInvalidChannel
	}

	var ctrlBuf [1]byte
	n, err := marshalHeartbeat(ctrlBuf[:])
	if err != nil {
		return err
	}

	ack, ackField := ch.ackState()
	hdr := &PacketHeader{
		Flags:    FlagControl,
		Channel:  channelID,
		Ack:      ack,
		AckField: ackField,
	}

	buf := make([]byte, 64)
	pn, err := buildControlPacket(buf, hdr, ctrlBuf[:n])
	if err != nil {
		return err
	}

	encrypted, err := fwcrypto.Encrypt(c.sendCipher, buf[:pn], nil)
	if err != nil {
		return err
	}

	if err := c.sendFramed(encrypted); err != nil {
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
