package fastwire

import "encoding/binary"

const (
	// BatchHeaderSize is the size of the packet count byte in a batched datagram.
	BatchHeaderSize = 1
	// BatchLenSize is the size of each packet's length prefix.
	BatchLenSize = 2
)

// MarshalBatch packs multiple encrypted packets into a single datagram.
// Format: [count:1B][len1:2B LE][pkt1][len2:2B LE][pkt2]...
func MarshalBatch(dst []byte, packets [][]byte) (int, error) {
	if len(packets) == 0 || len(packets) > 255 {
		return 0, ErrInvalidBatchFrame
	}

	needed := BatchHeaderSize
	for _, p := range packets {
		needed += BatchLenSize + len(p)
	}
	if len(dst) < needed {
		return 0, ErrBufferTooSmall
	}

	n := 0
	dst[n] = byte(len(packets))
	n++

	for _, p := range packets {
		binary.LittleEndian.PutUint16(dst[n:], uint16(len(p)))
		n += BatchLenSize
		copy(dst[n:], p)
		n += len(p)
	}

	return n, nil
}

// UnmarshalBatch unpacks a batched datagram into individual encrypted packets.
func UnmarshalBatch(data []byte) ([][]byte, error) {
	if len(data) < BatchHeaderSize {
		return nil, ErrInvalidBatchFrame
	}

	count := int(data[0])
	if count == 0 {
		return nil, ErrInvalidBatchFrame
	}

	packets := make([][]byte, 0, count)
	offset := BatchHeaderSize

	for range count {
		if offset+BatchLenSize > len(data) {
			return nil, ErrInvalidBatchFrame
		}
		pktLen := int(binary.LittleEndian.Uint16(data[offset:]))
		offset += BatchLenSize
		if offset+pktLen > len(data) {
			return nil, ErrInvalidBatchFrame
		}
		packets = append(packets, data[offset:offset+pktLen])
		offset += pktLen
	}

	return packets, nil
}

// BatchOverhead returns the wire overhead in bytes for a batch of n packets.
func BatchOverhead(n int) int {
	return BatchHeaderSize + n*BatchLenSize
}

// flushBatch packs the connection's batch buffer into batched datagrams and sends them.
func (c *Connection) flushBatch(mtu int) error {
	if len(c.batchBuf) == 0 {
		return nil
	}
	defer func() { c.batchBuf = c.batchBuf[:0] }()

	var dgBuf []byte
	pktCount := 0

	flush := func() error {
		if pktCount == 0 {
			return nil
		}
		dgBuf[0] = byte(pktCount)
		err := c.sendFunc(dgBuf)
		dgBuf = nil
		pktCount = 0
		return err
	}

	for _, pkt := range c.batchBuf {
		entrySize := BatchLenSize + len(pkt)

		// If a single packet doesn't fit in a fresh datagram, send as a single-packet batch.
		if BatchHeaderSize+entrySize > mtu {
			if err := flush(); err != nil {
				return err
			}
			single := make([]byte, BatchHeaderSize+entrySize)
			single[0] = 1
			binary.LittleEndian.PutUint16(single[1:], uint16(len(pkt)))
			copy(single[3:], pkt)
			if err := c.sendFunc(single); err != nil {
				return err
			}
			continue
		}

		if dgBuf == nil {
			dgBuf = make([]byte, 1, mtu)
		}

		// Check if adding this packet would exceed MTU or max count.
		if len(dgBuf)+entrySize > mtu || pktCount >= 255 {
			if err := flush(); err != nil {
				return err
			}
			dgBuf = make([]byte, 1, mtu)
		}

		dgBuf = binary.LittleEndian.AppendUint16(dgBuf, uint16(len(pkt)))
		dgBuf = append(dgBuf, pkt...)
		pktCount++
	}

	return flush()
}
