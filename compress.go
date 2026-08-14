package fastwire

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

// Compressor defines a compression/decompression algorithm.
type Compressor interface {
	Compress(dst, src []byte) ([]byte, error)
	Decompress(dst, src []byte) ([]byte, error)
	Algorithm() CompressionAlgorithm
}

// --- LZ4 implementation ---

// lz4Compressor is stateless. pierrec/lz4 v4 ignores the hash-table argument
// on CompressBlock and uses its own pooled Compressor, so we do not allocate
// or zero a 64Ki-entry table per call.
type lz4Compressor struct{}

func newLZ4Compressor() *lz4Compressor {
	return &lz4Compressor{}
}

func (c *lz4Compressor) Algorithm() CompressionAlgorithm {
	return CompressionLZ4
}

// Compress compresses src using LZ4 block API.
// Wire format: [original_size uint32 LE][lz4 block data].
// Returns dst (possibly re-sliced) with compressed data appended.
// If the data is incompressible, returns nil and ErrCompressionFailed.
func (c *lz4Compressor) Compress(dst, src []byte) ([]byte, error) {
	if len(src) == 0 {
		return dst, nil
	}

	// Reserve space: 4 bytes for original size + worst-case LZ4 output.
	maxOut := lz4.CompressBlockBound(len(src))
	needed := 4 + maxOut
	if cap(dst)-len(dst) < needed {
		newBuf := make([]byte, len(dst), len(dst)+needed)
		copy(newBuf, dst)
		dst = newBuf
	}

	offset := len(dst)
	dst = dst[:offset+needed]

	// Write original size as uint32 LE.
	binary.LittleEndian.PutUint32(dst[offset:], uint32(len(src)))

	n, err := lz4.CompressBlock(src, dst[offset+4:], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: lz4: %v", ErrCompressionFailed, err)
	}
	if n == 0 {
		// Incompressible data.
		return nil, ErrCompressionFailed
	}

	return dst[:offset+4+n], nil
}

// Decompress decompresses LZ4 block data from src.
// Expects wire format: [original_size uint32 LE][lz4 block data].
func (c *lz4Compressor) Decompress(dst, src []byte) ([]byte, error) {
	if len(src) < 4 {
		return nil, ErrDecompressionFailed
	}

	origSize := int(binary.LittleEndian.Uint32(src[:4]))
	if origSize > MaxDecompressedSize {
		return nil, ErrDecompressionFailed
	}
	if origSize == 0 {
		return dst, nil
	}

	// Allocate output buffer.
	offset := len(dst)
	needed := origSize
	if cap(dst)-len(dst) < needed {
		newBuf := make([]byte, len(dst), len(dst)+needed)
		copy(newBuf, dst)
		dst = newBuf
	}
	dst = dst[:offset+needed]

	n, err := lz4.UncompressBlock(src[4:], dst[offset:])
	if err != nil {
		return nil, fmt.Errorf("%w: lz4: %v", ErrDecompressionFailed, err)
	}

	return dst[:offset+n], nil
}

// --- Zstd implementation ---

type zstdCompressor struct {
	encoderPool sync.Pool
	decoderPool sync.Pool
	dict        []byte
}

func newZstdCompressor(level int, dict []byte) (*zstdCompressor, error) {
	c := &zstdCompressor{dict: dict}

	// Validate by creating one encoder and decoder.
	enc, err := c.newEncoder()
	if err != nil {
		return nil, fmt.Errorf("%w: zstd encoder: %v", ErrCompressionFailed, err)
	}
	c.encoderPool.Put(enc)

	dec, err := c.newDecoder()
	if err != nil {
		return nil, fmt.Errorf("%w: zstd decoder: %v", ErrDecompressionFailed, err)
	}
	c.decoderPool.Put(dec)

	return c, nil
}

func (c *zstdCompressor) newEncoder() (*zstd.Encoder, error) {
	opts := []zstd.EOption{
		zstd.WithEncoderConcurrency(1),
	}
	if c.dict != nil {
		d, err := zstd.InspectDictionary(c.dict)
		if err == nil && d.ID() != 0 {
			opts = append(opts, zstd.WithEncoderDict(c.dict))
		}
	}
	return zstd.NewWriter(nil, opts...)
}

func (c *zstdCompressor) newDecoder() (*zstd.Decoder, error) {
	opts := []zstd.DOption{
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(uint64(MaxDecompressedSize)),
	}
	if c.dict != nil {
		d, err := zstd.InspectDictionary(c.dict)
		if err == nil && d.ID() != 0 {
			opts = append(opts, zstd.WithDecoderDicts(c.dict))
		}
	}
	return zstd.NewReader(nil, opts...)
}

func (c *zstdCompressor) getEncoder() (*zstd.Encoder, error) {
	if v := c.encoderPool.Get(); v != nil {
		return v.(*zstd.Encoder), nil
	}
	return c.newEncoder()
}

func (c *zstdCompressor) putEncoder(enc *zstd.Encoder) {
	c.encoderPool.Put(enc)
}

func (c *zstdCompressor) getDecoder() (*zstd.Decoder, error) {
	if v := c.decoderPool.Get(); v != nil {
		return v.(*zstd.Decoder), nil
	}
	return c.newDecoder()
}

func (c *zstdCompressor) putDecoder(dec *zstd.Decoder) {
	c.decoderPool.Put(dec)
}

func (c *zstdCompressor) Algorithm() CompressionAlgorithm {
	return CompressionZstd
}

// Compress compresses src using zstd. The frame format is self-describing.
func (c *zstdCompressor) Compress(dst, src []byte) ([]byte, error) {
	enc, err := c.getEncoder()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCompressionFailed, err)
	}
	defer c.putEncoder(enc)

	return enc.EncodeAll(src, dst), nil
}

// Decompress decompresses zstd frame data from src.
func (c *zstdCompressor) Decompress(dst, src []byte) ([]byte, error) {
	dec, err := c.getDecoder()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecompressionFailed, err)
	}
	defer c.putDecoder(dec)

	result, err := dec.DecodeAll(src, dst)
	if err != nil {
		return nil, fmt.Errorf("%w: zstd: %v", ErrDecompressionFailed, err)
	}
	return result, nil
}

// --- compressorPool (high-level wrapper) ---

type compressorPool struct {
	config     CompressionConfig
	compressor Compressor // nil when Algorithm == CompressionNone
}

func newCompressorPool(config CompressionConfig) (*compressorPool, error) {
	p := &compressorPool{config: config}

	switch config.Algorithm {
	case CompressionNone:
		// No compressor needed.
	case CompressionLZ4:
		p.compressor = newLZ4Compressor()
	case CompressionZstd:
		c, err := newZstdCompressor(config.ZstdLevel, config.Dictionary)
		if err != nil {
			return nil, err
		}
		p.compressor = c
	default:
		return nil, fmt.Errorf("%w: unknown algorithm %d", ErrCompressionFailed, config.Algorithm)
	}

	// Apply default hurdle if not set.
	if p.config.Hurdle == 0 && p.config.Algorithm != CompressionNone {
		p.config.Hurdle = DefaultCompressionHurdle
	}

	return p, nil
}

// compressPayload compresses the payload if conditions are met.
// Returns the (possibly compressed) payload and the fragment flags to set.
// The scratch buffer used for compression is always returned to the pool
// before this function returns; the caller owns any compressed result.
func (p *compressorPool) compressPayload(payload []byte) ([]byte, FragmentFlag, error) {
	if p.compressor == nil || uint32(len(payload)) < p.config.Hurdle {
		return payload, 0, nil
	}

	bound := 4 + lz4.CompressBlockBound(len(payload))
	dst := getSendBuffer(bound)
	compressed, err := p.compressor.Compress(dst[:0], payload)
	if err != nil {
		putSendBuffer(dst)
		// Incompressible data — return original uncompressed.
		if err == ErrCompressionFailed {
			return payload, 0, nil
		}
		return nil, 0, err
	}

	// Expansion check: if compressed >= original, skip.
	if len(compressed) >= len(payload) {
		putSendBuffer(dst)
		return payload, 0, nil
	}

	// EncodeAll/LZ4 write into dst. Copy the result out so dst can go
	// back to the pool even if the caller discards the return (benchmarks).
	if sameBacking(compressed, dst) {
		compressed = bytes.Clone(compressed)
	}
	putSendBuffer(dst)

	flags := FragFlagCompressed
	if p.compressor.Algorithm() == CompressionZstd {
		flags |= FragFlagZstd
	}
	return compressed, flags, nil
}

// sameBacking reports whether a and b share the same backing array.
func sameBacking(a, b []byte) bool {
	return cap(a) > 0 && cap(a) == cap(b) && &a[:1][0] == &b[:1][0]
}

// decompressPayload decompresses the payload based on fragment flags.
func (p *compressorPool) decompressPayload(payload []byte, flags FragmentFlag) ([]byte, error) {
	if flags&FragFlagCompressed == 0 {
		return payload, nil
	}

	// Determine algorithm from flags.
	if flags&FragFlagZstd != 0 {
		// Zstd decompression.
		dec := &zstdCompressor{}
		if p.compressor != nil {
			if zc, ok := p.compressor.(*zstdCompressor); ok {
				dec = zc
			}
		}
		// If we don't have a zstd compressor configured, create a temporary one.
		if dec.decoderPool.New == nil && p.compressor == nil {
			tmp, err := newZstdCompressor(0, nil)
			if err != nil {
				return nil, err
			}
			dec = tmp
		}
		return dec.Decompress(nil, payload)
	}

	// LZ4 decompression. The compressor is stateless, so a zero value is fine
	// when this connection did not negotiate LZ4.
	var lz lz4Compressor
	if p.compressor != nil {
		if lc, ok := p.compressor.(*lz4Compressor); ok {
			lz = *lc
		}
	}
	return lz.Decompress(nil, payload)
}

// DictionaryHash returns the SHA-256 hash of a compression dictionary.
func DictionaryHash(dict []byte) [32]byte {
	return sha256.Sum256(dict)
}
