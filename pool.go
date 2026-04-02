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

// getSendBuffer returns a buffer of at least size bytes. Buffers that fit
// within DefaultMTU come from the shared pool; larger ones are heap-allocated.
func getSendBuffer(size int) []byte {
	if size <= DefaultMTU {
		return bufferPool.Get().([]byte)[:size]
	}
	return make([]byte, size)
}

// putSendBuffer returns a send buffer to the pool. Only buffers with
// cap >= DefaultMTU (i.e. those originally obtained from the pool) are recycled.
func putSendBuffer(buf []byte) {
	if cap(buf) >= DefaultMTU {
		//nolint:staticcheck // SA6002: we intentionally store []byte in sync.Pool
		bufferPool.Put(buf[:cap(buf)])
	}
}

// newReadPool creates a sync.Pool for read buffers of the given size.
func newReadPool(size int) *sync.Pool {
	return &sync.Pool{New: func() any { return make([]byte, size) }}
}
