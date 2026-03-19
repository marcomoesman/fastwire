package fastwire

import (
	"time"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
	"github.com/marcomoesman/fastwire/internal/congestion"
)

// DeliveryMode specifies how a channel delivers messages.
type DeliveryMode byte

const (
	// ReliableOrdered delivers messages in order, retransmitting lost packets.
	ReliableOrdered DeliveryMode = iota
	// ReliableUnordered delivers messages immediately, retransmitting lost packets.
	ReliableUnordered
	// Unreliable delivers messages with no guarantees.
	Unreliable
	// UnreliableSequenced delivers only the most recent message, dropping stale ones.
	UnreliableSequenced
)

// ChannelLayout defines the set of channels and their delivery modes.
type ChannelLayout struct {
	channels []channelDef
}

// Len returns the number of channels in the layout.
func (cl ChannelLayout) Len() int {
	return len(cl.channels)
}

type channelDef struct {
	Mode        DeliveryMode
	StreamIndex byte
}

// DefaultChannelLayout returns a layout with 4 channels, one per delivery mode.
// Channel 0: ReliableOrdered, Channel 1: ReliableUnordered,
// Channel 2: Unreliable, Channel 3: UnreliableSequenced.
func DefaultChannelLayout() ChannelLayout {
	return ChannelLayout{
		channels: []channelDef{
			{Mode: ReliableOrdered, StreamIndex: 0},
			{Mode: ReliableUnordered, StreamIndex: 0},
			{Mode: Unreliable, StreamIndex: 0},
			{Mode: UnreliableSequenced, StreamIndex: 0},
		},
	}
}

// ChannelLayoutBuilder constructs a custom ChannelLayout.
type ChannelLayoutBuilder struct {
	channels []channelDef
}

// NewChannelLayoutBuilder creates a new empty ChannelLayoutBuilder.
func NewChannelLayoutBuilder() *ChannelLayoutBuilder {
	return &ChannelLayoutBuilder{}
}

// AddChannel appends a channel with the given delivery mode and stream index.
func (b *ChannelLayoutBuilder) AddChannel(mode DeliveryMode, streamIndex byte) *ChannelLayoutBuilder {
	b.channels = append(b.channels, channelDef{Mode: mode, StreamIndex: streamIndex})
	return b
}

// Build validates and returns the ChannelLayout. Returns ErrInvalidChannelLayout
// if the layout has zero channels or more than 256 channels.
func (b *ChannelLayoutBuilder) Build() (ChannelLayout, error) {
	if len(b.channels) == 0 || len(b.channels) > 256 {
		return ChannelLayout{}, ErrInvalidChannelLayout
	}
	chs := make([]channelDef, len(b.channels))
	copy(chs, b.channels)
	return ChannelLayout{channels: chs}, nil
}

// CompressionAlgorithm identifies a compression algorithm.
type CompressionAlgorithm byte

const (
	CompressionNone CompressionAlgorithm = iota
	CompressionLZ4
	CompressionZstd
)

// CompressionConfig holds compression settings.
type CompressionConfig struct {
	Algorithm  CompressionAlgorithm
	Hurdle     uint32 // minimum payload size to attempt compression
	ZstdLevel  int
	Dictionary []byte
}

const (
	// DefaultCompressionHurdle is the minimum payload size (in bytes) to attempt compression.
	DefaultCompressionHurdle uint32 = 128

	// MaxDecompressedSize is the maximum allowed decompressed payload size (4 MB).
	// Protects against decompression bombs.
	MaxDecompressedSize int = 4 * 1024 * 1024
)

// CongestionMode selects a congestion control strategy.
type CongestionMode = congestion.Mode

const (
	// CongestionConservative uses AIMD congestion control.
	CongestionConservative = congestion.Conservative
	// CongestionAggressive disables window gating and uses fast retransmit.
	CongestionAggressive = congestion.Aggressive
)

// CipherSuite identifies an AEAD cipher suite.
type CipherSuite = fwcrypto.CipherSuite

const (
	// CipherNone disables encryption. Packets are sent as plaintext.
	CipherNone = fwcrypto.CipherNone
	// CipherAES128GCM uses AES-128-GCM (16-byte key).
	CipherAES128GCM = fwcrypto.CipherAES128GCM
	// CipherChaCha20Poly1305 uses ChaCha20-Poly1305 (32-byte key).
	CipherChaCha20Poly1305 = fwcrypto.CipherChaCha20Poly1305
)

// TickMode controls how the tick loop is driven.
type TickMode byte

const (
	// TickAuto spawns an internal goroutine that ticks at TickRate.
	TickAuto TickMode = iota
	// TickDriven requires the application to call Tick() manually.
	TickDriven
)

// FeatureFlag represents negotiated protocol features.
type FeatureFlag byte

const (
	// FeatureSendBatching enables packing multiple packets per UDP datagram.
	FeatureSendBatching FeatureFlag = 1 << 0
	// FeatureConnectionMigration enables token-based connection migration.
	FeatureConnectionMigration FeatureFlag = 1 << 1
)

const (
	// DefaultInitialCwnd is the default initial congestion window size in packets.
	DefaultInitialCwnd = 4

	// maxRetransmits is the maximum retransmit attempts before killing connection.
	maxRetransmits = 15
)

// ServerConfig holds configuration for a Server.
type ServerConfig struct {
	MTU               int
	MaxConnections    int
	TickRate          int // ticks per second (TickAuto only)
	TickMode          TickMode
	HeartbeatInterval time.Duration
	ConnTimeout       time.Duration
	HandshakeTimeout  time.Duration
	ChannelLayout     ChannelLayout
	Compression       CompressionConfig
	Congestion        CongestionMode
	CipherPreference  CipherSuite
	MaxRetransmits    int
	FragmentTimeout   time.Duration
	InitialCwnd       int

	// SendBatching enables packing multiple encrypted packets per UDP datagram.
	SendBatching bool
	// ConnectionMigration enables token-based connection identity, allowing
	// clients to change IP/port without reconnecting.
	ConnectionMigration bool
	// CoalesceIO enables multi-goroutine reads and asynchronous writes.
	CoalesceIO bool
	// CoalesceReaders is the number of concurrent read goroutines when
	// CoalesceIO is enabled. Defaults to runtime.NumCPU().
	CoalesceReaders int
}

// DefaultServerConfig returns a ServerConfig with sensible defaults.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		MTU:               DefaultMTU,
		MaxConnections:    1024,
		TickRate:          100,
		TickMode:          TickAuto,
		HeartbeatInterval: 1 * time.Second,
		ConnTimeout:       10 * time.Second,
		HandshakeTimeout:  5 * time.Second,
		ChannelLayout:     DefaultChannelLayout(),
		Congestion:        CongestionConservative,
		CipherPreference:  CipherAES128GCM,
		MaxRetransmits:    maxRetransmits,
		FragmentTimeout:   DefaultFragmentTimeout,
		InitialCwnd:       DefaultInitialCwnd,
	}
}

// ClientConfig holds configuration for a Client.
type ClientConfig struct {
	MTU               int
	TickRate          int // ticks per second (TickAuto only)
	TickMode          TickMode
	HeartbeatInterval time.Duration
	ConnTimeout       time.Duration
	ConnectTimeout    time.Duration // handshake timeout
	ChannelLayout     ChannelLayout
	Compression       CompressionConfig
	Congestion        CongestionMode
	CipherPreference  CipherSuite
	MaxRetransmits    int
	FragmentTimeout   time.Duration
	InitialCwnd       int

	// SendBatching enables packing multiple encrypted packets per UDP datagram.
	SendBatching bool
	// ConnectionMigration enables token-based connection identity, allowing
	// address changes without reconnecting.
	ConnectionMigration bool
}

// featuresFromConfig computes the feature flags from batching/migration booleans.
func featuresFromConfig(batching, migration bool) byte {
	var f byte
	if batching {
		f |= byte(FeatureSendBatching)
	}
	if migration {
		f |= byte(FeatureConnectionMigration)
	}
	return f
}

// DefaultClientConfig returns a ClientConfig with sensible defaults.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		MTU:               DefaultMTU,
		TickRate:          100,
		TickMode:          TickAuto,
		HeartbeatInterval: 1 * time.Second,
		ConnTimeout:       10 * time.Second,
		ConnectTimeout:    5 * time.Second,
		ChannelLayout:     DefaultChannelLayout(),
		Congestion:        CongestionConservative,
		CipherPreference:  CipherAES128GCM,
		MaxRetransmits:    maxRetransmits,
		FragmentTimeout:   DefaultFragmentTimeout,
		InitialCwnd:       DefaultInitialCwnd,
	}
}
