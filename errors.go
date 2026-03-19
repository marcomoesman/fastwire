package fastwire

import (
	"errors"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
)

var (
	// ErrBufferTooSmall is returned when a buffer is too small for encode/decode.
	ErrBufferTooSmall = errors.New("fastwire: buffer too small")

	// ErrVarIntOverflow is returned when a VarInt exceeds 5 bytes or uint32 range.
	ErrVarIntOverflow = errors.New("fastwire: VarInt overflow")

	// ErrVarLongOverflow is returned when a VarLong exceeds 10 bytes or uint64 range.
	ErrVarLongOverflow = errors.New("fastwire: VarLong overflow")

	// ErrStringTooLong is returned when a string exceeds 32767 bytes.
	ErrStringTooLong = errors.New("fastwire: string exceeds 32767 bytes")

	// ErrNegativeStringLength is returned when a decoded string length is negative.
	ErrNegativeStringLength = errors.New("fastwire: negative string length")

	// ErrInvalidUUID is returned when the buffer is too small for a UUID (< 16 bytes).
	ErrInvalidUUID = errors.New("fastwire: buffer too small for UUID")

	// ErrInvalidPacketHeader is returned when a packet header is too short or malformed.
	ErrInvalidPacketHeader = errors.New("fastwire: invalid packet header")

	// ErrInvalidFragmentHeader is returned when a fragment header is too short (< 5 bytes).
	ErrInvalidFragmentHeader = errors.New("fastwire: invalid fragment header")

	// ErrReplayedPacket is returned when a nonce has already been seen or is too old.
	ErrReplayedPacket = fwcrypto.ErrReplayedPacket

	// ErrDecryptionFailed is returned when AEAD decryption fails.
	ErrDecryptionFailed = fwcrypto.ErrDecryptionFailed

	// ErrVersionMismatch is returned when the remote protocol version does not match.
	ErrVersionMismatch = errors.New("fastwire: protocol version mismatch")

	// ErrInvalidHandshake is returned when a handshake packet is malformed.
	ErrInvalidHandshake = errors.New("fastwire: invalid handshake packet")

	// ErrHandshakeTimeout is returned when a handshake does not complete in time.
	ErrHandshakeTimeout = errors.New("fastwire: handshake timeout")

	// ErrConnectionClosed is returned when operating on a closed connection.
	ErrConnectionClosed = errors.New("fastwire: connection closed")

	// ErrInvalidChannelLayout is returned when a channel layout is invalid.
	ErrInvalidChannelLayout = errors.New("fastwire: invalid channel layout")

	// ErrInvalidChannel is returned when a channel ID is out of range.
	ErrInvalidChannel = errors.New("fastwire: channel ID out of range")

	// ErrMaxRetransmits is returned when the maximum retransmit attempts have been exceeded.
	ErrMaxRetransmits = errors.New("fastwire: max retransmit attempts exceeded")

	// ErrMessageTooLarge is returned when a message exceeds the maximum fragment count (255 fragments).
	ErrMessageTooLarge = errors.New("fastwire: message too large")

	// ErrCompressionFailed is returned when compression fails.
	ErrCompressionFailed = errors.New("fastwire: compression failed")

	// ErrDecompressionFailed is returned when decompression fails.
	ErrDecompressionFailed = errors.New("fastwire: decompression failed")

	// ErrServerClosed is returned when operating on a stopped server.
	ErrServerClosed = errors.New("fastwire: server closed")

	// ErrServerNotStarted is returned when Tick() is called before Start().
	ErrServerNotStarted = errors.New("fastwire: server not started")

	// ErrClientNotConnected is returned when operating on an unconnected client.
	ErrClientNotConnected = errors.New("fastwire: client not connected")

	// ErrTickAutoMode is returned when Tick() is called in TickAuto mode.
	ErrTickAutoMode = errors.New("fastwire: cannot call Tick() in TickAuto mode")

	// ErrAlreadyStarted is returned when Start() is called on an already started server.
	ErrAlreadyStarted = errors.New("fastwire: already started")

	// ErrAlreadyConnected is returned when Connect() is called on an already connected client.
	ErrAlreadyConnected = errors.New("fastwire: already connected")

	// ErrInvalidBatchFrame is returned when a batched datagram is malformed.
	ErrInvalidBatchFrame = errors.New("fastwire: invalid batch frame")
)
