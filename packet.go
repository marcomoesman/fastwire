package fastwire

import "encoding/binary"

// PacketFlag represents flags in a packet header.
type PacketFlag byte

const (
	// FlagFragment indicates the payload is a fragment.
	FlagFragment PacketFlag = 1 << 0
	// FlagControl indicates a connection control packet (handshake/disconnect/heartbeat).
	FlagControl PacketFlag = 1 << 1
)

// PacketHeader is the decrypted payload header.
type PacketHeader struct {
	Flags    PacketFlag
	Channel  byte
	Sequence uint32
	Ack      uint32
	AckField uint32 // 32-bit bitfield, fixed 4 bytes little-endian
}

// ControlType identifies the type of a control packet.
type ControlType byte

const (
	ControlConnect         ControlType = 0x01
	ControlChallenge       ControlType = 0x02
	ControlResponse        ControlType = 0x03
	ControlConnected       ControlType = 0x04
	ControlDisconnect      ControlType = 0x05
	ControlHeartbeat       ControlType = 0x06
	ControlVersionMismatch ControlType = 0x07
	ControlReject          ControlType = 0x08
)

// MarshalHeader encodes h into buf.
// Format: Flags (1B) + Channel (1B) + Sequence (VarInt) + Ack (VarInt) + AckField (4B LE).
// Returns the number of bytes written and an error if buf is too small.
func MarshalHeader(buf []byte, h *PacketHeader) (int, error) {
	// Minimum size: 1 + 1 + 1 + 1 + 4 = 8 bytes (smallest VarInts).
	// Maximum size: 1 + 1 + 5 + 5 + 4 = 16 bytes.
	if len(buf) < 8 {
		return 0, ErrInvalidPacketHeader
	}

	buf[0] = byte(h.Flags)
	buf[1] = h.Channel
	n := 2

	w := PutVarInt(buf[n:], h.Sequence)
	n += w

	if len(buf[n:]) < 5+4 { // worst case VarInt + AckField
		return 0, ErrInvalidPacketHeader
	}
	w = PutVarInt(buf[n:], h.Ack)
	n += w

	if len(buf[n:]) < 4 {
		return 0, ErrInvalidPacketHeader
	}
	binary.LittleEndian.PutUint32(buf[n:], h.AckField)
	n += 4

	return n, nil
}

// UnmarshalHeader parses a PacketHeader from buf.
// Returns the parsed header, the number of bytes consumed, and an error.
func UnmarshalHeader(buf []byte) (PacketHeader, int, error) {
	if len(buf) < 8 {
		return PacketHeader{}, 0, ErrInvalidPacketHeader
	}

	var h PacketHeader
	h.Flags = PacketFlag(buf[0])
	h.Channel = buf[1]
	n := 2

	seq, w, err := ReadVarInt(buf[n:])
	if err != nil {
		return PacketHeader{}, 0, ErrInvalidPacketHeader
	}
	h.Sequence = seq
	n += w

	ack, w, err := ReadVarInt(buf[n:])
	if err != nil {
		return PacketHeader{}, 0, ErrInvalidPacketHeader
	}
	h.Ack = ack
	n += w

	if len(buf[n:]) < 4 {
		return PacketHeader{}, 0, ErrInvalidPacketHeader
	}
	h.AckField = binary.LittleEndian.Uint32(buf[n:])
	n += 4

	return h, n, nil
}
