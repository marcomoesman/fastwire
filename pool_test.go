package fastwire

import "testing"

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
