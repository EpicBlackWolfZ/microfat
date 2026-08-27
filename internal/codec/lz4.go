package codec

import (
	"fmt"
	"io"
	"strings"

	"github.com/pierrec/lz4/v4"
)

// LZ4Codec implements the Codec interface for pure-Go LZ4 frame compression.
type LZ4Codec struct{}

// NewLZ4Codec returns a new instance of LZ4Codec.
func NewLZ4Codec() *LZ4Codec {
	return &LZ4Codec{}
}

// Name returns the unique identifier "lz4".
func (c *LZ4Codec) Name() string {
	return AlgorithmLZ4
}

var lz4Levels = [...]lz4.CompressionLevel{
	lz4.Fast,
	lz4.Level1,
	lz4.Level2,
	lz4.Level3,
	lz4.Level4,
	lz4.Level5,
	lz4.Level6,
	lz4.Level7,
	lz4.Level8,
	lz4.Level9,
}

// MapLevel maps a level string to lz4.CompressionLevel.
func (c *LZ4Codec) MapLevel(level string) (lz4.CompressionLevel, error) {
	cleanLevel := strings.ToLower(strings.TrimSpace(level))
	switch cleanLevel {
	case "", "default", "fast", "fastest":
		return lz4.Fast, nil
	case "better":
		return lz4.Level5, nil
	case "best", "max":
		return lz4.Level9, nil
	}

	num, err := ParseLevelInt(level, 0)
	if err != nil {
		return lz4.Fast, err
	}
	if num <= 0 {
		return lz4.Fast, nil
	}
	if num < len(lz4Levels) {
		return lz4Levels[num], nil
	}
	return lz4.Level9, nil
}

// Compress compresses src bytes using LZ4 with the specified level.
func (c *LZ4Codec) Compress(w io.Writer, src []byte, level string) error {
	encLevel, err := c.MapLevel(level)
	if err != nil {
		return err
	}

	writer := lz4.NewWriter(w)
	if err := writer.Apply(lz4.CompressionLevelOption(encLevel)); err != nil {
		return fmt.Errorf("applying lz4 compression level: %w", err)
	}

	if _, err := writer.Write(src); err != nil {
		_ = writer.Close()
		return fmt.Errorf("compressing lz4 payload: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("closing lz4 writer: %w", err)
	}

	return nil
}

// Decompress decompresses an LZ4 stream from r into w.
func (c *LZ4Codec) Decompress(w io.Writer, r io.Reader, uncompressedSize int64) error {
	reader := lz4.NewReader(r)

	written, err := io.Copy(w, reader)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDecompressionFailed, err)
	}

	if uncompressedSize > 0 && written != uncompressedSize {
		return fmt.Errorf("%w: expected %d bytes, got %d", ErrSizeMismatch, uncompressedSize, written)
	}

	return nil
}
