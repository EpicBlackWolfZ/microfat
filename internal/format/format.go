// Package format defines the microfat binary trailer, index structure, integrity verification,
// and serialization logic.
package format

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

// Constants for binary layout and verification.
const (
	// MagicString identifies a microfat-enabled fat executable.
	MagicString = "\x00\xFA\x7FMICRO"

	// TrailerSize is the fixed size of the trailer at EOF:
	// 8 bytes IndexOffset + 8 bytes IndexSize + 32 bytes IndexSHA256 + 8 bytes Magic.
	TrailerSize = 56

	// OffsetLen is length in bytes of uint64 fields in trailer.
	OffsetLen = 8

	// HashLen is the byte length of SHA-256 checksums.
	HashLen = 32

	// MagicLen is the length of magic string in bytes.
	MagicLen = 8

	// FormatVersionCurrent is the current schema version.
	FormatVersionCurrent = 1

	// MaxIndexSize is the maximum allowable index table size (1 MB).
	MaxIndexSize = 1024 * 1024

	// MaxPayloadSize is the maximum allowable single payload uncompressed size (1 GB).
	MaxPayloadSize = 1024 * 1024 * 1024
)

// Standard error definitions for binary format parsing.
var (
	ErrBinaryTooSmall     = errors.New("binary size is smaller than trailer size")
	ErrInvalidMagic       = errors.New("invalid microfat magic bytes at EOF")
	ErrUnsupportedVersion = errors.New("unsupported microfat format version")
	ErrInvalidIndexOffset = errors.New("invalid index offset in trailer")
	ErrInvalidIndexSize   = errors.New("invalid index size in trailer")
	ErrIndexCorrupted     = errors.New("index SHA-256 checksum mismatch")
	ErrOutOfBounds        = errors.New("variant payload extends beyond binary boundary")
	ErrPayloadTooLarge    = errors.New("variant payload size exceeds safety limit")
)

// VariantEntry describes an individual compressed microarchitecture variant payload.
type VariantEntry struct {
	Level            string `json:"level"`             // Microarchitecture level (e.g., "v1", "v2", "v3", "v4")
	Offset           int64  `json:"offset"`            // Absolute byte offset in the fat binary
	CompressedSize   int64  `json:"compressed_size"`   // Compressed zstd byte length
	UncompressedSize int64  `json:"uncompressed_size"` // Raw binary byte length
	SHA256           string `json:"sha256,omitempty"`  // Checksum of uncompressed payload
	Compression      string `json:"compression"`       // Compression algorithm (e.g., "zstd")
}

// Index holds the manifest of all embedded variants and target platform metadata.
type Index struct {
	Version     int            `json:"version"`
	AppName     string         `json:"app_name,omitempty"`
	TargetOS    string         `json:"os"`
	TargetArch  string         `json:"arch"`
	CreatedUnix int64          `json:"created_unix"`
	Variants    []VariantEntry `json:"variants"`
}

// VariantLevels returns a slice of all variant level strings present in the index.
func (idx *Index) VariantLevels() []string {
	levels := make([]string, len(idx.Variants))
	for i, v := range idx.Variants {
		levels[i] = v.Level
	}
	return levels
}

// FindVariant returns the VariantEntry corresponding to the specified level string.
func (idx *Index) FindVariant(level string) (*VariantEntry, bool) {
	for i := range idx.Variants {
		if idx.Variants[i].Level == level {
			return &idx.Variants[i], true
		}
	}
	return nil, false
}

// ValidateBounds verifies that all variants and offsets in the index are within safe boundaries.
func (idx *Index) ValidateBounds(indexOffset int64) error {
	if idx.Version != FormatVersionCurrent {
		return fmt.Errorf("%w: got version %d, expected %d", ErrUnsupportedVersion, idx.Version, FormatVersionCurrent)
	}

	for _, v := range idx.Variants {
		if v.Offset < 0 || v.CompressedSize <= 0 || v.UncompressedSize <= 0 {
			return fmt.Errorf("%w: invalid dimensions for variant %s", ErrOutOfBounds, v.Level)
		}
		if v.UncompressedSize > MaxPayloadSize {
			return fmt.Errorf("%w: variant %s exceeds 1GB limit (%d bytes)", ErrPayloadTooLarge, v.Level, v.UncompressedSize)
		}
		if v.Offset+v.CompressedSize > indexOffset {
			return fmt.Errorf("%w: variant %s payload extends past index offset %d", ErrOutOfBounds, v.Level, indexOffset)
		}
	}
	return nil
}

// ReadTrailerAndIndex reads the trailing 56 bytes of the binary, verifies magic and index SHA-256 hash,
// deserializes the JSON index table, and enforces bounds checks.
func ReadTrailerAndIndex(r io.ReaderAt, totalSize int64) (*Index, error) {
	if totalSize < TrailerSize {
		return nil, ErrBinaryTooSmall
	}

	trailerBuf := make([]byte, TrailerSize)
	trailerOffset := totalSize - TrailerSize
	if _, err := r.ReadAt(trailerBuf, trailerOffset); err != nil {
		return nil, fmt.Errorf("reading trailer at offset %d: %w", trailerOffset, err)
	}

	// 1. Verify Magic Bytes (last 8 bytes)
	magic := trailerBuf[TrailerSize-MagicLen:]
	if !bytes.Equal(magic, []byte(MagicString)) {
		return nil, ErrInvalidMagic
	}

	// 2. Parse Trailer Fields
	indexOffset := binary.LittleEndian.Uint64(trailerBuf[0:OffsetLen])
	indexSize := binary.LittleEndian.Uint64(trailerBuf[OffsetLen : OffsetLen*2])
	expectedHash := trailerBuf[OffsetLen*2 : OffsetLen*2+HashLen]

	if indexOffset > math.MaxInt64 || int64(indexOffset) < 0 || int64(indexOffset) >= trailerOffset {
		return nil, fmt.Errorf("%w: offset %d beyond trailer %d", ErrInvalidIndexOffset, indexOffset, trailerOffset)
	}
	if indexSize == 0 || indexSize > MaxIndexSize || indexSize > math.MaxInt64 ||
		int64(indexOffset)+int64(indexSize) != trailerOffset {
		return nil, fmt.Errorf("%w: size %d with offset %d does not match %d",
			ErrInvalidIndexSize, indexSize, indexOffset, trailerOffset)
	}

	// 3. Read Index Bytes
	indexBuf := make([]byte, indexSize)
	if _, err := r.ReadAt(indexBuf, int64(indexOffset)); err != nil {
		return nil, fmt.Errorf("reading index at offset %d: %w", indexOffset, err)
	}

	// 4. Verify Index SHA-256 Hash
	actualHash := sha256.Sum256(indexBuf)
	if !bytes.Equal(actualHash[:], expectedHash) {
		return nil, fmt.Errorf("%w: expected %x, got %x", ErrIndexCorrupted, expectedHash, actualHash)
	}

	// 5. Unmarshal JSON
	var idx Index
	if err := json.Unmarshal(indexBuf, &idx); err != nil {
		return nil, fmt.Errorf("unmarshaling index json: %w", err)
	}

	// 6. Validate Bounds
	if err := idx.ValidateBounds(int64(indexOffset)); err != nil {
		return nil, fmt.Errorf("validating index bounds: %w", err)
	}

	return &idx, nil
}

// WriteIndexAndTrailer writes the serialized index JSON followed by the 56-byte trailer to w.
func WriteIndexAndTrailer(w io.Writer, idx *Index, currentOffset int64) (int64, error) {
	if currentOffset < 0 {
		return 0, fmt.Errorf("invalid negative offset %d", currentOffset)
	}

	idx.Version = FormatVersionCurrent
	idxBytes, err := json.Marshal(idx)
	if err != nil {
		return 0, fmt.Errorf("marshaling index json: %w", err)
	}

	indexSize := int64(len(idxBytes))
	if indexSize > MaxIndexSize {
		return 0, fmt.Errorf("index json size %d exceeds maximum %d", indexSize, MaxIndexSize)
	}

	n, err := w.Write(idxBytes)
	if err != nil {
		return 0, fmt.Errorf("writing index json: %w", err)
	}

	indexHash := sha256.Sum256(idxBytes)

	trailer := make([]byte, TrailerSize)
	binary.LittleEndian.PutUint64(trailer[0:OffsetLen], uint64(currentOffset))
	binary.LittleEndian.PutUint64(trailer[OffsetLen:OffsetLen*2], uint64(indexSize))
	copy(trailer[OffsetLen*2:OffsetLen*2+HashLen], indexHash[:])
	copy(trailer[OffsetLen*2+HashLen:], []byte(MagicString))

	tn, err := w.Write(trailer)
	if err != nil {
		return 0, fmt.Errorf("writing trailer: %w", err)
	}

	return int64(n + tn), nil
}

// IsFatBinary returns true if the reader contains valid microfat magic bytes at EOF.
func IsFatBinary(r io.ReaderAt, totalSize int64) bool {
	if totalSize < TrailerSize {
		return false
	}
	buf := make([]byte, MagicLen)
	if _, err := r.ReadAt(buf, totalSize-MagicLen); err != nil {
		return false
	}
	return bytes.Equal(buf, []byte(MagicString))
}
