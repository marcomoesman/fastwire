package fastwire

import (
	"encoding/binary"
	"fmt"
)

// ---------------------------------------------------------------------------
// VarInt — unsigned LEB128, uint32, max 5 bytes
// ---------------------------------------------------------------------------

// PutVarInt encodes v as an unsigned LEB128 VarInt into buf.
// It returns the number of bytes written. The caller must ensure buf is large
// enough (max 5 bytes).
func PutVarInt(buf []byte, v uint32) int {
	i := 0
	for v >= 0x80 {
		buf[i] = byte(v) | 0x80
		v >>= 7
		i++
	}
	buf[i] = byte(v)
	return i + 1
}

// ReadVarInt decodes an unsigned LEB128 VarInt from buf.
// It returns the decoded value, the number of bytes consumed, and an error.
func ReadVarInt(buf []byte) (uint32, int, error) {
	var val uint32
	for i := 0; i < 5; i++ {
		if i >= len(buf) {
			return 0, 0, ErrBufferTooSmall
		}
		b := buf[i]
		val |= uint32(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			return val, i + 1, nil
		}
	}
	return 0, 0, ErrVarIntOverflow
}

// ---------------------------------------------------------------------------
// VarLong — unsigned LEB128, uint64, max 10 bytes
// ---------------------------------------------------------------------------

// PutVarLong encodes v as an unsigned LEB128 VarLong into buf.
// It returns the number of bytes written. The caller must ensure buf is large
// enough (max 10 bytes).
func PutVarLong(buf []byte, v uint64) int {
	i := 0
	for v >= 0x80 {
		buf[i] = byte(v) | 0x80
		v >>= 7
		i++
	}
	buf[i] = byte(v)
	return i + 1
}

// ReadVarLong decodes an unsigned LEB128 VarLong from buf.
// It returns the decoded value, the number of bytes consumed, and an error.
func ReadVarLong(buf []byte) (uint64, int, error) {
	var val uint64
	for i := 0; i < 10; i++ {
		if i >= len(buf) {
			return 0, 0, ErrBufferTooSmall
		}
		b := buf[i]
		val |= uint64(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			return val, i + 1, nil
		}
	}
	return 0, 0, ErrVarLongOverflow
}

// ---------------------------------------------------------------------------
// String — int16 big-endian length prefix + UTF-8 bytes, max 32767
// ---------------------------------------------------------------------------

const maxStringLength = 32767

// PutString encodes s as a length-prefixed string into buf.
// The length prefix is a signed int16 in big-endian. Returns the total number
// of bytes written and an error if the string is too long or the buffer is too
// small.
func PutString(buf []byte, s string) (int, error) {
	l := len(s)
	if l > maxStringLength {
		return 0, ErrStringTooLong
	}
	need := 2 + l
	if len(buf) < need {
		return 0, ErrBufferTooSmall
	}
	binary.BigEndian.PutUint16(buf, uint16(l))
	copy(buf[2:], s)
	return need, nil
}

// ReadString decodes a length-prefixed string from buf.
// It returns the string, the number of bytes consumed, and an error.
func ReadString(buf []byte) (string, int, error) {
	if len(buf) < 2 {
		return "", 0, ErrBufferTooSmall
	}
	raw := int16(binary.BigEndian.Uint16(buf))
	if raw < 0 {
		return "", 0, ErrNegativeStringLength
	}
	l := int(raw)
	need := 2 + l
	if len(buf) < need {
		return "", 0, ErrBufferTooSmall
	}
	return string(buf[2:need]), need, nil
}

// ---------------------------------------------------------------------------
// UUID — 128-bit identifier, two uint64 big-endian on the wire
// ---------------------------------------------------------------------------

// UUID is a 128-bit universally unique identifier.
type UUID [16]byte

// PutUUID encodes id into buf as two big-endian uint64s (MSB first, then LSB).
// Returns 16 (the number of bytes written). The caller must ensure buf has at
// least 16 bytes.
func PutUUID(buf []byte, id UUID) int {
	copy(buf, id[:])
	return 16
}

// ReadUUID decodes a UUID from buf.
// It returns the UUID, the number of bytes consumed (16), and an error.
func ReadUUID(buf []byte) (UUID, int, error) {
	if len(buf) < 16 {
		return UUID{}, 0, ErrInvalidUUID
	}
	var id UUID
	copy(id[:], buf[:16])
	return id, 16, nil
}

// UUIDFromInts creates a UUID from the most-significant and least-significant
// 64-bit halves.
func UUIDFromInts(msb, lsb uint64) UUID {
	var id UUID
	binary.BigEndian.PutUint64(id[:8], msb)
	binary.BigEndian.PutUint64(id[8:], lsb)
	return id
}

// MSB returns the most-significant 64 bits of the UUID.
func (u UUID) MSB() uint64 {
	return binary.BigEndian.Uint64(u[:8])
}

// LSB returns the least-significant 64 bits of the UUID.
func (u UUID) LSB() uint64 {
	return binary.BigEndian.Uint64(u[8:])
}

// String returns the UUID in standard 8-4-4-4-12 hex format.
func (u UUID) String() string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		u[:4], u[4:6], u[6:8], u[8:10], u[10:16])
}
