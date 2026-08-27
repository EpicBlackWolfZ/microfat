package codec

import (
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// ZstdCodec implements the Codec interface for Zstandard compression.
type ZstdCodec struct{}

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
	encLevel, err := c.MapLevel(level)
	if err != nil {
		return err
	}

	writer, err := zstd.NewWriter(w, zstd.WithEncoderLevel(encLevel))
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
	reader, err := zstd.NewReader(r)
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
