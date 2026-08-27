package codec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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

	// historySampleFractionDivisor is the portion of total sample bytes allocated for the dictionary history window.
	historySampleFractionDivisor = 4
)

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
	case "fastest", "fast", "speed", "1":
		return zstd.SpeedFastest, nil
	case "better", "3":
		return zstd.SpeedBetterCompression, nil
	case "best", "max", "11", "19":
		return zstd.SpeedBestCompression, nil
	}

	num, err := ParseLevelInt(level, 0)
	if err != nil {
		return zstd.SpeedDefault, err
	}
	switch {
	case num <= 1:
		return zstd.SpeedFastest, nil
	case num == 2 || num == 3:
		return zstd.SpeedDefault, nil
	case num >= 4 && num <= 9:
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

	opts := []zstd.EOption{
		zstd.WithEncoderLevel(encLevel),
	}
	if len(dict) > 0 {
		opts = append(opts, zstd.WithEncoderDict(dict))
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

// Decompress decompresses a zstd stream from r into w.
func (c *ZstdCodec) Decompress(w io.Writer, r io.Reader, uncompressedSize int64) error {
	return c.DecompressWithDict(w, r, uncompressedSize, nil)
}

// DecompressWithDict decompresses a zstd stream from r into w using a shared dictionary.
func (c *ZstdCodec) DecompressWithDict(w io.Writer, r io.Reader, uncompressedSize int64, dict []byte) error {
	var opts []zstd.DOption
	if len(dict) > 0 {
		opts = append(opts, zstd.WithDecoderDicts(dict))
	}

	reader, err := zstd.NewReader(r, opts...)
	if err != nil {
		return fmt.Errorf("initializing zstd reader: %w", err)
	}
	defer reader.Close()

	written, err := io.Copy(w, reader)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDecompressionFailed, err)
	}

	if uncompressedSize > 0 && written != uncompressedSize {
		return fmt.Errorf("%w: expected %d bytes, got %d", ErrSizeMismatch, uncompressedSize, written)
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
	maxHistBytes := targetDictSize
	if maxHistBytes > totalBytes/historySampleFractionDivisor {
		maxHistBytes = totalBytes / historySampleFractionDivisor
	}
	if maxHistBytes < 8 {
		maxHistBytes = 8
	}

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

	dict, err := zstd.BuildDict(zstd.BuildDictOptions{
		ID:       DefaultDictionaryID,
		History:  hist,
		Contents: samples,
		Level:    encLevel,
	})
	if err != nil {
		return nil, fmt.Errorf("building zstd dictionary: %w", err)
	}

	return dict, nil
}
