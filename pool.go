package fastwire

import "sync"

// DefaultMTU is the default maximum transmission unit size in bytes.
const DefaultMTU = 1200

var bufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, DefaultMTU)
		return buf
	},
}

// GetBuffer returns a []byte of length DefaultMTU from the buffer pool.
func GetBuffer() []byte {
	return bufferPool.Get().([]byte)
}

// PutBuffer returns buf to the buffer pool.
func PutBuffer(buf []byte) {
	//nolint:staticcheck // SA6002: we intentionally store []byte in sync.Pool
	bufferPool.Put(buf)
}
