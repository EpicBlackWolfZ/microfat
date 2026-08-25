// Package pack implements the compression and assembly engine for microfat binaries.
package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ghostnetorg/microfat/internal/format"
	"github.com/ghostnetorg/pkg/microarch"
	"github.com/klauspost/compress/zstd"
)

// Standard permissions and buffer sizes.
const (
	defaultFileMode = 0o755
)

// Common error definitions.
var (
	ErrNoVariantsSpecified = errors.New("at least one microarchitecture variant must be specified")
	ErrStubMissing         = errors.New("stub binary path is required and must exist")
	ErrVariantNotFound     = errors.New("variant binary file not found")
	ErrChecksumMismatch    = errors.New("variant payload checksum mismatch")
	ErrSizeMismatch        = errors.New("decompressed variant size mismatch")
)

// Options configures the packaging process.
type Options struct {
	StubPath         string
	OutputPath       string
	AppName          string
	TargetOS         string
	TargetArch       string
	Variants         map[string]string // level -> binary path
	CompressionLevel zstd.EncoderLevel
	Permissions      os.FileMode
}

// VerificationResult contains the result of verifying an individual embedded variant.
type VerificationResult struct {
	Level            string
	CompressedSize   int64
	UncompressedSize int64
	ExpectedSHA256   string
	ActualSHA256     string
	Valid            bool
	Error            error
}

// Pack stitches the stub and compressed variant binaries into a complete microfat fat executable.
func Pack(opts Options) (*format.Index, error) {
	if len(opts.Variants) == 0 {
		return nil, ErrNoVariantsSpecified
	}
	if opts.StubPath == "" {
		return nil, ErrStubMissing
	}
	if opts.OutputPath == "" {
		return nil, errors.New("output path must not be empty")
	}
	if opts.TargetOS == "" {
		opts.TargetOS = "linux"
	}
	if opts.TargetArch == "" {
		opts.TargetArch = "amd64"
	}
	if opts.Permissions == 0 {
		opts.Permissions = defaultFileMode
	}

	stubBytes, err := os.ReadFile(filepath.Clean(opts.StubPath))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStubMissing, err)
	}

	// Sort variant levels ascending by rank
	levels := make([]string, 0, len(opts.Variants))
	for lvl := range opts.Variants {
		levels = append(levels, lvl)
	}
	sort.Slice(levels, func(i, j int) bool {
		return microarch.Compare(opts.TargetArch, levels[i], levels[j]) < 0
	})

	// Create temporary file in the destination directory for atomic replacement
	outDir := filepath.Dir(opts.OutputPath)
	tmpFile, err := os.CreateTemp(outDir, ".microfat-pack-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("creating temporary output file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	// 1. Write Stub Binary
	if _, err := tmpFile.Write(stubBytes); err != nil {
		return nil, fmt.Errorf("writing stub binary: %w", err)
	}

	currentOffset := int64(len(stubBytes))
	idx := &format.Index{
		Version:     format.FormatVersionCurrent,
		AppName:     opts.AppName,
		TargetOS:    opts.TargetOS,
		TargetArch:  opts.TargetArch,
		CreatedUnix: time.Now().Unix(),
		Variants:    make([]format.VariantEntry, 0, len(levels)),
	}

	encLevel := zstd.SpeedBetterCompression
	if opts.CompressionLevel != 0 {
		encLevel = opts.CompressionLevel
	}

	// 2. Compress and write each variant payload
	for _, lvl := range levels {
		variantPath := filepath.Clean(opts.Variants[lvl])
		variantBytes, err := os.ReadFile(variantPath)
		if err != nil {
			return nil, fmt.Errorf("%w: %s (%w)", ErrVariantNotFound, variantPath, err)
		}

		rawHash := sha256.Sum256(variantBytes)
		rawHashHex := hex.EncodeToString(rawHash[:])
		uncompressedSize := int64(len(variantBytes))

		variantOffset := currentOffset

		zstdWriter, err := zstd.NewWriter(tmpFile, zstd.WithEncoderLevel(encLevel))
		if err != nil {
			return nil, fmt.Errorf("initializing zstd writer: %w", err)
		}

		if _, err := zstdWriter.Write(variantBytes); err != nil {
			_ = zstdWriter.Close()
			return nil, fmt.Errorf("compressing variant %s: %w", lvl, err)
		}
		if err := zstdWriter.Close(); err != nil {
			return nil, fmt.Errorf("closing zstd writer for %s: %w", lvl, err)
		}

		newOffset, err := tmpFile.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, fmt.Errorf("seeking current file offset: %w", err)
		}
		compressedSize := newOffset - variantOffset
		currentOffset = newOffset

		idx.Variants = append(idx.Variants, format.VariantEntry{
			Level:            lvl,
			Offset:           variantOffset,
			CompressedSize:   compressedSize,
			UncompressedSize: uncompressedSize,
			SHA256:           rawHashHex,
			Compression:      "zstd",
		})
	}

	// 3. Write Index and Trailer
	if _, err := format.WriteIndexAndTrailer(tmpFile, idx, currentOffset); err != nil {
		return nil, fmt.Errorf("writing index and trailer: %w", err)
	}

	// 4. Sync, chmod and atomically move
	if err := tmpFile.Sync(); err != nil {
		return nil, fmt.Errorf("syncing output file: %w", err)
	}
	if err := tmpFile.Chmod(opts.Permissions); err != nil {
		return nil, fmt.Errorf("chmodding output file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("closing output file: %w", err)
	}

	if err := os.Rename(tmpPath, opts.OutputPath); err != nil {
		return nil, fmt.Errorf("moving temporary file to output %s: %w", opts.OutputPath, err)
	}

	return idx, nil
}

// VerifyBinary reads the index and decompresses each variant to verify SHA-256 checksums and boundaries.
func VerifyBinary(r io.ReaderAt, totalSize int64) (*format.Index, []VerificationResult, error) {
	idx, err := format.ReadTrailerAndIndex(r, totalSize)
	if err != nil {
		return nil, nil, fmt.Errorf("reading index: %w", err)
	}

	results := make([]VerificationResult, len(idx.Variants))
	for i, v := range idx.Variants {
		res := VerificationResult{
			Level:            v.Level,
			CompressedSize:   v.CompressedSize,
			UncompressedSize: v.UncompressedSize,
			ExpectedSHA256:   v.SHA256,
		}

		secReader := io.NewSectionReader(r, v.Offset, v.CompressedSize)
		zstdReader, err := zstd.NewReader(secReader)
		if err != nil {
			res.Error = fmt.Errorf("creating zstd reader: %w", err)
			results[i] = res
			continue
		}

		hasher := sha256.New()
		written, err := io.Copy(hasher, zstdReader)
		zstdReader.Close()
		if err != nil {
			res.Error = fmt.Errorf("decompressing variant payload: %w", err)
			results[i] = res
			continue
		}

		actualHashHex := hex.EncodeToString(hasher.Sum(nil))
		res.ActualSHA256 = actualHashHex

		if written != v.UncompressedSize {
			res.Error = fmt.Errorf("%w: expected %d bytes, got %d", ErrSizeMismatch, v.UncompressedSize, written)
			results[i] = res
			continue
		}

		if v.SHA256 != "" && actualHashHex != v.SHA256 {
			res.Error = fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, v.SHA256, actualHashHex)
			results[i] = res
			continue
		}

		res.Valid = true
		results[i] = res
	}

	return idx, results, nil
}
