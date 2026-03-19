package fastwire

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"net/netip"
	"strings"
	"sync"
	"testing"

	fwcrypto "github.com/marcomoesman/fastwire/crypto"
)

// --- LZ4 round-trip tests ---

func TestLZ4RoundTripSmall(t *testing.T) {
	c := newLZ4Compressor()
	src := []byte(strings.Repeat("hello world ", 20)) // ~240 bytes, compressible
	compressed, err := c.Compress(nil, src)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if len(compressed) >= len(src) {
		t.Fatalf("compressed size %d >= original %d", len(compressed), len(src))
	}

	decompressed, err := c.Decompress(nil, compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Fatal("round-trip mismatch")
	}
}

func TestLZ4RoundTripMedium(t *testing.T) {
	c := newLZ4Compressor()
	src := []byte(strings.Repeat("abcdefghijklmnopqrstuvwxyz", 100)) // ~2600 bytes
	compressed, err := c.Compress(nil, src)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	decompressed, err := c.Decompress(nil, compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Fatal("round-trip mismatch")
	}
}

func TestLZ4RoundTripMultiFragment(t *testing.T) {
	c := newLZ4Compressor()
	// Larger than MaxFragmentPayload, compressible.
	src := []byte(strings.Repeat("x", MaxFragmentPayload*3))
	compressed, err := c.Compress(nil, src)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	decompressed, err := c.Decompress(nil, compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Fatal("round-trip mismatch")
	}
}

func TestLZ4Empty(t *testing.T) {
	c := newLZ4Compressor()
	compressed, err := c.Compress(nil, nil)
	if err != nil {
		t.Fatalf("Compress empty: %v", err)
	}
	if len(compressed) != 0 {
		t.Fatalf("expected empty compressed, got %d bytes", len(compressed))
	}
}

func TestLZ4DecompressTruncated(t *testing.T) {
	c := newLZ4Compressor()
	_, err := c.Decompress(nil, []byte{1, 2, 3}) // too short for size prefix
	if err != ErrDecompressionFailed {
		t.Fatalf("expected ErrDecompressionFailed, got %v", err)
	}
}

// --- Zstd round-trip tests ---

func TestZstdRoundTripSmall(t *testing.T) {
	c, err := newZstdCompressor(0, nil)
	if err != nil {
		t.Fatalf("newZstdCompressor: %v", err)
	}
	src := []byte(strings.Repeat("hello world ", 20))
	compressed, err := c.Compress(nil, src)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}

	decompressed, err := c.Decompress(nil, compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Fatal("round-trip mismatch")
	}
}

func TestZstdRoundTripMedium(t *testing.T) {
	c, err := newZstdCompressor(0, nil)
	if err != nil {
		t.Fatalf("newZstdCompressor: %v", err)
	}
	src := []byte(strings.Repeat("test data pattern ", 200))
	compressed, err := c.Compress(nil, src)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	decompressed, err := c.Decompress(nil, compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Fatal("round-trip mismatch")
	}
}

func TestZstdRoundTripMultiFragment(t *testing.T) {
	c, err := newZstdCompressor(0, nil)
	if err != nil {
		t.Fatalf("newZstdCompressor: %v", err)
	}
	src := []byte(strings.Repeat("y", MaxFragmentPayload*3))
	compressed, err := c.Compress(nil, src)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	decompressed, err := c.Decompress(nil, compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Fatal("round-trip mismatch")
	}
}

// --- compressorPool tests ---

func TestCompressorPoolHurdleSkip(t *testing.T) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionLZ4,
		Hurdle:    DefaultCompressionHurdle,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Payload below hurdle — should skip compression.
	small := []byte("tiny")
	result, flags, err := pool.compressPayload(small)
	if err != nil {
		t.Fatal(err)
	}
	if flags != 0 {
		t.Fatalf("expected no flags for small payload, got %d", flags)
	}
	if !bytes.Equal(result, small) {
		t.Fatal("payload should be unchanged")
	}
}

func TestCompressorPoolEmptyPayload(t *testing.T) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionLZ4,
		Hurdle:    DefaultCompressionHurdle,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, flags, err := pool.compressPayload(nil)
	if err != nil {
		t.Fatal(err)
	}
	if flags != 0 {
		t.Fatalf("expected no flags for empty payload, got %d", flags)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty result, got %d bytes", len(result))
	}
}

func TestCompressorPoolExpansionSkip(t *testing.T) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionLZ4,
		Hurdle:    DefaultCompressionHurdle,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Random data is incompressible.
	random := make([]byte, 256)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}

	result, flags, err := pool.compressPayload(random)
	if err != nil {
		t.Fatal(err)
	}
	if flags != 0 {
		t.Fatalf("expected no flags for incompressible data, got %d", flags)
	}
	if !bytes.Equal(result, random) {
		t.Fatal("incompressible data should be returned unchanged")
	}
}

func TestCompressorPoolLZ4Flags(t *testing.T) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionLZ4,
		Hurdle:    DefaultCompressionHurdle,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Compressible data above hurdle.
	payload := []byte(strings.Repeat("hello world ", 30))
	_, flags, err := pool.compressPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if flags != FragFlagCompressed {
		t.Fatalf("expected FragFlagCompressed, got %d", flags)
	}
}

func TestCompressorPoolZstdFlags(t *testing.T) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionZstd,
		Hurdle:    DefaultCompressionHurdle,
	})
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte(strings.Repeat("hello world ", 30))
	_, flags, err := pool.compressPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	expected := FragFlagCompressed | FragFlagZstd
	if flags != expected {
		t.Fatalf("expected FragFlagCompressed|FragFlagZstd (%d), got %d", expected, flags)
	}
}

func TestCompressorPoolNonePassthrough(t *testing.T) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionNone,
	})
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte(strings.Repeat("test", 100))
	result, flags, err := pool.compressPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if flags != 0 {
		t.Fatalf("expected no flags, got %d", flags)
	}
	if !bytes.Equal(result, payload) {
		t.Fatal("payload should be unchanged")
	}
}

func TestCompressorPoolLZ4RoundTrip(t *testing.T) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionLZ4,
		Hurdle:    DefaultCompressionHurdle,
	})
	if err != nil {
		t.Fatal(err)
	}

	original := []byte(strings.Repeat("compress me please ", 30))
	compressed, flags, err := pool.compressPayload(original)
	if err != nil {
		t.Fatal(err)
	}
	if flags == 0 {
		t.Fatal("expected compression to happen")
	}

	decompressed, err := pool.decompressPayload(compressed, flags)
	if err != nil {
		t.Fatalf("decompressPayload: %v", err)
	}
	if !bytes.Equal(decompressed, original) {
		t.Fatal("round-trip mismatch")
	}
}

func TestCompressorPoolZstdRoundTrip(t *testing.T) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionZstd,
		Hurdle:    DefaultCompressionHurdle,
	})
	if err != nil {
		t.Fatal(err)
	}

	original := []byte(strings.Repeat("compress me please ", 30))
	compressed, flags, err := pool.compressPayload(original)
	if err != nil {
		t.Fatal(err)
	}
	if flags == 0 {
		t.Fatal("expected compression to happen")
	}

	decompressed, err := pool.decompressPayload(compressed, flags)
	if err != nil {
		t.Fatalf("decompressPayload: %v", err)
	}
	if !bytes.Equal(decompressed, original) {
		t.Fatal("round-trip mismatch")
	}
}

func TestDecompressPayloadNoFlagPassthrough(t *testing.T) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionLZ4,
		Hurdle:    DefaultCompressionHurdle,
	})
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("raw data")
	result, err := pool.decompressPayload(payload, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, payload) {
		t.Fatal("should passthrough when no compressed flag set")
	}
}

// --- DictionaryHash ---

func TestDictionaryHash(t *testing.T) {
	dict := []byte("my custom dictionary data")
	hash := DictionaryHash(dict)
	expected := sha256.Sum256(dict)
	if hash != expected {
		t.Fatal("DictionaryHash does not match sha256.Sum256")
	}
}

func TestDictionaryHashEmpty(t *testing.T) {
	hash := DictionaryHash(nil)
	expected := sha256.Sum256(nil)
	if hash != expected {
		t.Fatal("DictionaryHash(nil) does not match sha256.Sum256(nil)")
	}
}

// --- Concurrent compression safety ---

func TestConcurrentLZ4(t *testing.T) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionLZ4,
		Hurdle:    DefaultCompressionHurdle,
	})
	if err != nil {
		t.Fatal(err)
	}

	original := []byte(strings.Repeat("concurrent test data ", 30))
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			compressed, flags, err := pool.compressPayload(original)
			if err != nil {
				t.Errorf("compress: %v", err)
				return
			}
			if flags == 0 {
				return // expansion skip is valid
			}
			decompressed, err := pool.decompressPayload(compressed, flags)
			if err != nil {
				t.Errorf("decompress: %v", err)
				return
			}
			if !bytes.Equal(decompressed, original) {
				t.Errorf("round-trip mismatch in goroutine")
			}
		}()
	}
	wg.Wait()
}

func TestConcurrentZstd(t *testing.T) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionZstd,
		Hurdle:    DefaultCompressionHurdle,
	})
	if err != nil {
		t.Fatal(err)
	}

	original := []byte(strings.Repeat("concurrent zstd data ", 30))
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			compressed, flags, err := pool.compressPayload(original)
			if err != nil {
				t.Errorf("compress: %v", err)
				return
			}
			if flags == 0 {
				return
			}
			decompressed, err := pool.decompressPayload(compressed, flags)
			if err != nil {
				t.Errorf("decompress: %v", err)
				return
			}
			if !bytes.Equal(decompressed, original) {
				t.Errorf("round-trip mismatch in goroutine")
			}
		}()
	}
	wg.Wait()
}

// --- Max decompressed size / bomb protection ---

func TestLZ4MaxDecompressedSize(t *testing.T) {
	c := newLZ4Compressor()
	// Craft a fake LZ4 block with a huge original size.
	fakeData := make([]byte, 8)
	// Set original size to MaxDecompressedSize + 1.
	origSize := uint32(MaxDecompressedSize + 1)
	fakeData[0] = byte(origSize)
	fakeData[1] = byte(origSize >> 8)
	fakeData[2] = byte(origSize >> 16)
	fakeData[3] = byte(origSize >> 24)

	_, err := c.Decompress(nil, fakeData)
	if err != ErrDecompressionFailed {
		t.Fatalf("expected ErrDecompressionFailed for oversized decompression, got %v", err)
	}
}

// --- Compress → fragment → reassemble → decompress integration ---

func TestCompressFragmentReassembleDecompress(t *testing.T) {
	algorithms := []CompressionAlgorithm{CompressionLZ4, CompressionZstd}

	for _, algo := range algorithms {
		t.Run(algoName(algo), func(t *testing.T) {
			pool, err := newCompressorPool(CompressionConfig{
				Algorithm: algo,
				Hurdle:    DefaultCompressionHurdle,
			})
			if err != nil {
				t.Fatal(err)
			}

			// Create a large compressible payload (will require fragmentation).
			original := []byte(strings.Repeat("integration test data pattern ", 100))

			// Step 1: Compress.
			compressed, flags, err := pool.compressPayload(original)
			if err != nil {
				t.Fatalf("compressPayload: %v", err)
			}
			if flags == 0 {
				t.Fatal("expected compression to happen")
			}

			// Step 2: Fragment.
			frags, err := splitMessage(compressed, 42, flags)
			if err != nil {
				t.Fatalf("splitMessage: %v", err)
			}

			// Step 3: Reassemble.
			store := newReassemblyStore()
			var assembled []byte
			var complete bool
			for _, frag := range frags {
				fh, n, err := UnmarshalFragmentHeader(frag)
				if err != nil {
					t.Fatalf("UnmarshalFragmentHeader: %v", err)
				}
				assembled, complete, err = store.addFragment(fh, frag[n:])
				if err != nil {
					t.Fatalf("addFragment: %v", err)
				}
			}
			if !complete {
				t.Fatal("reassembly should be complete")
			}

			// Verify the reassembled flags match.
			fh0, _, _ := UnmarshalFragmentHeader(frags[0])

			// Step 4: Decompress.
			decompressed, err := pool.decompressPayload(assembled, fh0.FragmentFlags)
			if err != nil {
				t.Fatalf("decompressPayload: %v", err)
			}
			if !bytes.Equal(decompressed, original) {
				t.Fatal("end-to-end mismatch")
			}
		})
	}
}

// --- Connection has compressorPool ---

func TestConnectionHasCompressorPool(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9999")
	conn := newConnection(addr, nil, nil, CipherNone, DefaultChannelLayout(), CompressionConfig{
		Algorithm: CompressionLZ4,
		Hurdle:    DefaultCompressionHurdle,
	}, CongestionConservative, 0, 0, MigrationToken{})
	if conn.compress == nil {
		t.Fatal("connection should have a compressor pool")
	}
}

func TestConnectionNoneCompression(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:9999")
	conn := newConnection(addr, nil, nil, CipherNone, DefaultChannelLayout(), CompressionConfig{}, CongestionConservative, 0, 0, MigrationToken{})
	if conn.compress == nil {
		t.Fatal("connection should have a compressor pool even for CompressionNone")
	}
}

// --- Handshake dictionary hash validation ---

func TestHandshakeDictHashMatch(t *testing.T) {
	clientKP, _ := fwcrypto.GenerateKeyPair()
	dict := []byte("shared dictionary data for testing purposes")
	dictHash := DictionaryHash(dict)

	connectBuf := make([]byte, 128)
	cn, _ := buildConnectPacket(connectBuf, &connectPacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      0,
		PublicKey:       clientKP.Public.Bytes(),
		CipherPref:      CipherAES128GCM,
		Compression:     CompressionZstd,
		DictHash:        dictHash[:],
	})

	pending, _, err := serverProcessConnect(connectBuf[:cn], CompressionConfig{
		Algorithm:  CompressionZstd,
		Dictionary: dict,
	}, DefaultChannelLayout(), CongestionConservative, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending.compression != compressionOK {
		t.Fatalf("expected compressionOK, got %d", pending.compression)
	}
	if pending.compressionConfig.Algorithm != CompressionZstd {
		t.Fatal("negotiated compression should be zstd")
	}
}

func TestHandshakeDictHashMismatch(t *testing.T) {
	clientKP, _ := fwcrypto.GenerateKeyPair()
	clientDict := []byte("client dictionary")
	clientHash := DictionaryHash(clientDict)

	connectBuf := make([]byte, 128)
	cn, _ := buildConnectPacket(connectBuf, &connectPacket{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      0,
		PublicKey:       clientKP.Public.Bytes(),
		CipherPref:      CipherAES128GCM,
		Compression:     CompressionZstd,
		DictHash:        clientHash[:],
	})

	serverDict := []byte("different server dictionary")
	pending, _, err := serverProcessConnect(connectBuf[:cn], CompressionConfig{
		Algorithm:  CompressionZstd,
		Dictionary: serverDict,
	}, DefaultChannelLayout(), CongestionConservative, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending.compression != compressionDictMismatch {
		t.Fatalf("expected compressionDictMismatch, got %d", pending.compression)
	}
}

// --- Benchmarks ---

func BenchmarkCompressLZ4(b *testing.B) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionLZ4,
		Hurdle:    0,
	})
	if err != nil {
		b.Fatal(err)
	}
	payload := []byte(strings.Repeat("benchmark compressible data pattern ", 28)) // ~1000 bytes
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		_, _, _ = pool.compressPayload(payload)
	}
}

func BenchmarkCompressZstd(b *testing.B) {
	pool, err := newCompressorPool(CompressionConfig{
		Algorithm: CompressionZstd,
		Hurdle:    0,
	})
	if err != nil {
		b.Fatal(err)
	}
	payload := []byte(strings.Repeat("benchmark compressible data pattern ", 28)) // ~1000 bytes
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		_, _, _ = pool.compressPayload(payload)
	}
}

func algoName(a CompressionAlgorithm) string {
	switch a {
	case CompressionNone:
		return "None"
	case CompressionLZ4:
		return "LZ4"
	case CompressionZstd:
		return "Zstd"
	default:
		return "Unknown"
	}
}
