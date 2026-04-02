package fastwire

import (
	"sync"
	"testing"
)

func TestGetBufferLength(t *testing.T) {
	buf := GetBuffer()
	if len(buf) != DefaultMTU {
		t.Fatalf("GetBuffer() length: got %d, want %d", len(buf), DefaultMTU)
	}
}

func TestGetPutCycle(t *testing.T) {
	buf := GetBuffer()
	PutBuffer(buf)
	// Get again — should not panic.
	buf2 := GetBuffer()
	if len(buf2) != DefaultMTU {
		t.Fatalf("GetBuffer() after Put: length %d, want %d", len(buf2), DefaultMTU)
	}
	PutBuffer(buf2)
}

func TestBufferReusable(t *testing.T) {
	buf := GetBuffer()
	// Write some data.
	for i := range buf {
		buf[i] = byte(i)
	}
	PutBuffer(buf)

	// Get a buffer and verify it has the right length (contents may vary).
	buf2 := GetBuffer()
	if len(buf2) != DefaultMTU {
		t.Fatalf("reused buffer length: got %d, want %d", len(buf2), DefaultMTU)
	}
	PutBuffer(buf2)
}

func TestGetSendBufferSmall(t *testing.T) {
	buf := getSendBuffer(100)
	if len(buf) != 100 {
		t.Fatalf("getSendBuffer(100) len = %d, want 100", len(buf))
	}
	if cap(buf) < DefaultMTU {
		t.Fatalf("getSendBuffer(100) cap = %d, want >= %d", cap(buf), DefaultMTU)
	}
	putSendBuffer(buf)
}

func TestGetSendBufferExactMTU(t *testing.T) {
	buf := getSendBuffer(DefaultMTU)
	if len(buf) != DefaultMTU {
		t.Fatalf("getSendBuffer(%d) len = %d", DefaultMTU, len(buf))
	}
	putSendBuffer(buf)
}

func TestGetSendBufferLarge(t *testing.T) {
	size := DefaultMTU + 500
	buf := getSendBuffer(size)
	if len(buf) != size {
		t.Fatalf("getSendBuffer(%d) len = %d", size, len(buf))
	}
	// Should not panic on put (buffer is silently dropped, not pooled).
	putSendBuffer(buf)
}

func TestPutSendBufferPooled(t *testing.T) {
	buf := getSendBuffer(200)
	buf[0] = 0xAA
	putSendBuffer(buf)

	// Get again — pool may return the same backing array.
	buf2 := getSendBuffer(200)
	if cap(buf2) < DefaultMTU {
		t.Fatalf("expected pooled buffer, got cap %d", cap(buf2))
	}
	putSendBuffer(buf2)
}

func TestPutSendBufferNonPooled(t *testing.T) {
	// Small buffer not from pool — should not panic.
	small := make([]byte, 10)
	putSendBuffer(small) // cap < DefaultMTU, silently dropped
}

func TestNewReadPool(t *testing.T) {
	size := 1233
	pool := newReadPool(size)

	buf := pool.Get().([]byte)
	if len(buf) != size {
		t.Fatalf("read pool Get() len = %d, want %d", len(buf), size)
	}

	pool.Put(buf)

	buf2 := pool.Get().([]byte)
	if len(buf2) != size {
		t.Fatalf("read pool Get() after Put len = %d, want %d", len(buf2), size)
	}
	pool.Put(buf2)
}

func TestPoolConcurrency(t *testing.T) {
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				buf := getSendBuffer(500)
				buf[0] = 1
				putSendBuffer(buf)
			}
		}()
	}
	wg.Wait()
}

func TestReadPoolConcurrency(t *testing.T) {
	pool := newReadPool(1233)
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				buf := pool.Get().([]byte)
				buf[0] = 1
				pool.Put(buf)
			}
		}()
	}
	wg.Wait()
}
