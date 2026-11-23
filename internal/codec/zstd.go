package codec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Dictionary size and ID constants for Zstandard.
const (
	// DefaultDictionaryID is the default non-zero dictionary ID used for microfat dictionaries.
	DefaultDictionaryID uint32 = 0x4D464154 // 'MFAT'

	// DefaultDictSize is the default maximum dictionary byte size (112 KB).
	DefaultDictSize = 112 * 1024

	// MaxDictSize is the upper bound for a dictionary size (1 MB).
	MaxDictSize = 1024 * 1024

	// DefaultDecoderMaxMemory is the maximum memory allocation allowed for the zstd decoder (512 MiB).
	DefaultDecoderMaxMemory uint64 = 512 * 1024 * 1024

	// DefaultDecoderMaxWindow is the maximum sliding window size allowed for the zstd decoder (128 MiB).
	DefaultDecoderMaxWindow uint64 = 128 * 1024 * 1024

	// DefaultZstdDecoderOverhead estimates fixed Zstd decoder tables/buffers beyond sliding windows (4 MiB).
	DefaultZstdDecoderOverhead uint64 = 4 * 1024 * 1024

	// historySampleFractionDivisor is the portion of total sample bytes allocated for the dictionary history window.
	historySampleFractionDivisor = 4

	zstdDefaultNumericLevel = 2
	zstdMaxSkippableFrames  = 8

	// BuildDict encodes each training sample as one Zstandard block.
	maxDictTrainingSampleSize = 128 * 1024
)

// ErrWindowSizeExceeded indicates a Zstd frame declares a window larger than DefaultDecoderMaxWindow.
var ErrWindowSizeExceeded = errors.New("zstd frame window exceeds maximum allowed size")

// ZstdCodec implements the Codec and DictCodec interfaces for Zstandard compression.
type ZstdCodec struct{}

var _ DictCodec = (*ZstdCodec)(nil)

// NewZstdCodec returns a new instance of ZstdCodec.
func NewZstdCodec() *ZstdCodec {
	return &ZstdCodec{}
}

// Name returns the unique identifier "zstd".
func (c *ZstdCodec) Name() string {
	return AlgorithmZstd
}

// MapLevel maps a level string to zstd.EncoderLevel.
func (c *ZstdCodec) MapLevel(level string) (zstd.EncoderLevel, error) {
	cleanLevel := strings.ToLower(strings.TrimSpace(level))
	switch cleanLevel {
	case "", "default", "standard":
		return zstd.SpeedDefault, nil
	case "fastest", "fast", "speed":
		return zstd.SpeedFastest, nil
	case "better":
		return zstd.SpeedBetterCompression, nil
	case "best", "max":
		return zstd.SpeedBestCompression, nil
	}

	num, err := ParseLevelInt(level, 0)
	if err != nil {
		return zstd.SpeedDefault, err
	}
	switch {
	case num <= 1:
		return zstd.SpeedFastest, nil
	case num == zstdDefaultNumericLevel:
		return zstd.SpeedDefault, nil
	case num >= 3 && num <= 9:
		return zstd.SpeedBetterCompression, nil
	default:
		return zstd.SpeedBestCompression, nil
	}
}

// Compress compresses src bytes using zstd with the specified level.
func (c *ZstdCodec) Compress(w io.Writer, src []byte, level string) error {
	return c.CompressWithDict(w, src, level, nil)
}

// CompressWithDict compresses src bytes using zstd with the specified level and shared dictionary.
func (c *ZstdCodec) CompressWithDict(w io.Writer, src []byte, level string, dict []byte) error {
	encLevel, err := c.MapLevel(level)
	if err != nil {
		return err
	}

	hasDict := len(dict) > 0
	plan := resolveZstdEncodePlan(len(src), encLevel, hasDict)

	opts := []zstd.EOption{
		zstd.WithEncoderLevel(encLevel),
		zstd.WithEncoderConcurrency(plan.concurrency),
	}
	if hasDict {
		opts = append(opts, zstd.WithEncoderDict(dict))
	}

	if plan.singleShot {
		encoder, initErr := zstd.NewWriter(nil, opts...)
		if initErr != nil {
			return fmt.Errorf("initializing zstd encoder: %w", initErr)
		}
		initialCap := len(src)/singleShotCapDivisor + singleShotOverheadBytes
		encoded := encoder.EncodeAll(src, make([]byte, 0, initialCap))
		if _, writeErr := w.Write(encoded); writeErr != nil {
			return fmt.Errorf("compressing zstd payload: %w", writeErr)
		}
		return nil
	}

	writer, err := zstd.NewWriter(w, opts...)
	if err != nil {
		return fmt.Errorf("initializing zstd writer: %w", err)
	}

	if _, err := writer.Write(src); err != nil {
		_ = writer.Close()
		return fmt.Errorf("compressing zstd payload: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("closing zstd writer: %w", err)
	}

	return nil
}

// InspectZstdFrameWindow reads the Zstd frame header at [offset, offset+compressedSize)
// (skipping up to zstdMaxSkippableFrames leading skippable frames) and returns the declared window size.
func InspectZstdFrameWindow(r io.ReaderAt, offset, compressedSize int64) (uint64, error) {
	if r == nil || offset < 0 || compressedSize <= 0 || offset > math.MaxInt64-compressedSize {
		return 0, fmt.Errorf("%w: invalid zstd header range [%d, +%d)", ErrDecompressionFailed, offset, compressedSize)
	}

	curOffset := offset
	endOffset := offset + compressedSize
	var headerBuf [zstd.HeaderMaxSize]byte

	for range zstdMaxSkippableFrames + 1 {
		remaining := endOffset - curOffset
		if remaining <= 0 {
			return 0, fmt.Errorf("%w: zstd stream truncated before frame header", ErrDecompressionFailed)
		}
		readLen := min(remaining, int64(zstd.HeaderMaxSize))
		n, readErr := r.ReadAt(headerBuf[:readLen], curOffset)
		if readErr != nil && (!errors.Is(readErr, io.EOF) || n == 0) {
			return 0, fmt.Errorf("%w: reading zstd frame header: %w", ErrDecompressionFailed, readErr)
		}

		var hdr zstd.Header
		if decErr := hdr.Decode(headerBuf[:n]); decErr != nil {
			return 0, fmt.Errorf("%w: decoding zstd frame header: %w", ErrDecompressionFailed, decErr)
		}

		if hdr.Skippable {
			skipTotal := int64(hdr.HeaderSize) + int64(hdr.SkippableSize)
			if skipTotal <= 0 || skipTotal > remaining {
				return 0, fmt.Errorf("%w: zstd skippable frame exceeds compressed bounds", ErrDecompressionFailed)
			}
			curOffset += skipTotal
			continue
		}

		win := hdr.WindowSize
		if hdr.SingleSegment {
			win = max(hdr.FrameContentSize, zstd.MinWindowSize)
		}
		if win < zstd.MinWindowSize {
			return 0, fmt.Errorf("%w: zstd frame window %d below minimum %d", ErrDecompressionFailed, win, zstd.MinWindowSize)
		}
		if win > DefaultDecoderMaxWindow {
			return win, fmt.Errorf("%w: frame window %d exceeds limit %d", ErrWindowSizeExceeded, win, DefaultDecoderMaxWindow)
		}
		return win, nil
	}

	return 0, fmt.Errorf("%w: excessive consecutive skippable frames", ErrDecompressionFailed)
}

// Decompress decompresses a zstd stream from r into w.
func (c *ZstdCodec) Decompress(w io.Writer, r io.Reader, uncompressedSize int64) error {
	return c.DecompressWithDictAndWindow(w, r, uncompressedSize, nil, DefaultDecoderMaxWindow)
}

// DecompressWithDict decompresses a zstd stream from r into w using a shared dictionary.
func (c *ZstdCodec) DecompressWithDict(w io.Writer, r io.Reader, uncompressedSize int64, dict []byte) error {
	return c.DecompressWithDictAndWindow(w, r, uncompressedSize, dict, DefaultDecoderMaxWindow)
}

// DecompressWithDictAndWindow decompresses a zstd stream from r into w using an optional
// shared dictionary and enforcing a per-frame maximum window size of maxWindow.
func (c *ZstdCodec) DecompressWithDictAndWindow(
	w io.Writer,
	r io.Reader,
	uncompressedSize int64,
	dict []byte,
	maxWindow uint64,
) error {
	if uncompressedSize < 0 {
		return fmt.Errorf("%w: invalid negative uncompressed size %d", ErrSizeMismatch, uncompressedSize)
	}
	if uncompressedSize > DefaultMaxPayloadSize {
		return fmt.Errorf("%w: uncompressed size %d exceeds safety limit %d", ErrSizeMismatch, uncompressedSize, DefaultMaxPayloadSize)
	}
	if maxWindow == 0 {
		maxWindow = DefaultDecoderMaxWindow
	}
	if maxWindow > DefaultDecoderMaxWindow {
		return fmt.Errorf("%w: requested window cap %d exceeds limit %d", ErrWindowSizeExceeded, maxWindow, DefaultDecoderMaxWindow)
	}
	effectiveWindow := max(maxWindow, zstd.MinWindowSize)

	opts := []zstd.DOption{
		zstd.WithDecoderMaxMemory(DefaultDecoderMaxMemory),
		zstd.WithDecoderMaxWindow(effectiveWindow),
	}
	if len(dict) > 0 {
		opts = append(opts, zstd.WithDecoderDicts(dict))
	}

	reader, err := zstd.NewReader(r, opts...)
	if err != nil {
		return fmt.Errorf("%w: initializing zstd reader: %w", ErrDecompressionFailed, err)
	}
	defer reader.Close()

	bw := newBoundedWriter(w, uncompressedSize)
	_, copyErr := io.Copy(bw, reader)
	if copyErr != nil {
		if errors.Is(copyErr, ErrSizeMismatch) {
			return copyErr
		}
		if errors.Is(copyErr, zstd.ErrWindowSizeExceeded) || errors.Is(copyErr, zstd.ErrDecoderSizeExceeded) {
			return fmt.Errorf("%w: %w: %w", ErrDecompressionFailed, ErrWindowSizeExceeded, copyErr)
		}
		return fmt.Errorf("%w: %w", ErrDecompressionFailed, copyErr)
	}

	if bw.written != uncompressedSize {
		return fmt.Errorf("%w: expected %d bytes, got %d", ErrSizeMismatch, uncompressedSize, bw.written)
	}

	return nil
}

// TrainDictionary trains a Zstandard dictionary from candidate sample slices.
func TrainDictionary(samples [][]byte, targetDictSize int, level string) ([]byte, error) {
	if len(samples) == 0 {
		return nil, errors.New("no samples provided for dictionary training")
	}

	totalBytes := 0
	for _, s := range samples {
		totalBytes += len(s)
	}
	if totalBytes < 8 {
		return nil, errors.New("sample data too small for dictionary training (< 8 bytes)")
	}

	if targetDictSize <= 0 {
		targetDictSize = DefaultDictSize
	}
	if targetDictSize > MaxDictSize {
		targetDictSize = MaxDictSize
	}

	// Cap history buffer to at most targetDictSize and totalBytes/historySampleFractionDivisor
	// to ensure subsequent sample contents provide literals and dictionary statistics.
	maxHistBytes := max(min(targetDictSize, totalBytes/historySampleFractionDivisor), 8)

	var historyBuf bytes.Buffer
	for _, s := range samples {
		if historyBuf.Len() >= maxHistBytes {
			break
		}
		remaining := maxHistBytes - historyBuf.Len()
		if len(s) <= remaining {
			historyBuf.Write(s)
		} else {
			historyBuf.Write(s[:remaining])
		}
	}
	hist := historyBuf.Bytes()
	if len(hist) < 8 {
		return nil, errors.New("history buffer too small (< 8 bytes)")
	}

	zCodec := NewZstdCodec()
	encLevel, err := zCodec.MapLevel(level)
	if err != nil {
		encLevel = zstd.SpeedDefault
	}

	// The upstream dictionary builder does not split oversized samples before
	// passing them to its block encoder. Keep each sample within the block limit.
	trainingSamples := make([][]byte, 0, len(samples))
	for _, sample := range samples {
		for len(sample) > maxDictTrainingSampleSize {
			trainingSamples = append(trainingSamples, sample[:maxDictTrainingSampleSize])
			sample = sample[maxDictTrainingSampleSize:]
		}
		trainingSamples = append(trainingSamples, sample)
	}

	dict, err := zstd.BuildDict(zstd.BuildDictOptions{
		ID:       DefaultDictionaryID,
		History:  hist,
		Contents: trainingSamples,
		Level:    encLevel,
	})
	if err != nil {
		return nil, fmt.Errorf("building zstd dictionary: %w", err)
	}

	return dict, nil
}
