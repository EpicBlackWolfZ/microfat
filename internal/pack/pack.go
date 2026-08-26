// Package pack implements the compression and assembly engine for microfat binaries.
package pack

import (
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
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
	ErrInvalidELF          = errors.New("invalid ELF binary")
)

// Options configures the packaging process.
type Options struct {
	StubPath          string
	OutputPath        string
	AppName           string
	TargetOS          string
	TargetArch        string
	Variants          map[string]string // level -> binary path
	CompressionLevel  zstd.EncoderLevel
	Permissions       os.FileMode
	SkipELFValidation bool // Optional flag to bypass ELF header validation (primarily for testing)
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
	if err := validateOptions(&opts); err != nil {
		return nil, err
	}

	stubBytes, err := os.ReadFile(filepath.Clean(opts.StubPath))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStubMissing, err)
	}

	levels := sortVariantLevels(opts.Variants, opts.TargetArch)

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
		entry, newOffset, err := writeVariantPayload(tmpFile, lvl, opts.Variants[lvl], encLevel, currentOffset)
		if err != nil {
			return nil, err
		}
		currentOffset = newOffset
		idx.Variants = append(idx.Variants, entry)
	}

	// 3. Write Index and Trailer
	if _, err := format.WriteIndexAndTrailer(tmpFile, idx, currentOffset); err != nil {
		return nil, fmt.Errorf("writing index and trailer: %w", err)
	}

	// 4. Sync, chmod and atomically move
	if err := finalizeOutputFile(tmpFile, tmpPath, opts.OutputPath, opts.Permissions); err != nil {
		return nil, err
	}

	return idx, nil
}

func validateOptions(opts *Options) error {
	if len(opts.Variants) == 0 {
		return ErrNoVariantsSpecified
	}
	if opts.StubPath == "" {
		return ErrStubMissing
	}
	if opts.OutputPath == "" {
		return errors.New("output path must not be empty")
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

	if !opts.SkipELFValidation {
		if err := ValidateELFBinary(opts.StubPath, opts.TargetOS, opts.TargetArch); err != nil {
			return fmt.Errorf("validating stub: %w", err)
		}
		for lvl, varPath := range opts.Variants {
			if err := ValidateELFBinary(varPath, opts.TargetOS, opts.TargetArch); err != nil {
				return fmt.Errorf("validating variant %s: %w", lvl, err)
			}
		}
	}
	return nil
}

func sortVariantLevels(variants map[string]string, targetArch string) []string {
	levels := make([]string, 0, len(variants))
	for lvl := range variants {
		levels = append(levels, lvl)
	}
	sort.Slice(levels, func(i, j int) bool {
		return microarch.Compare(targetArch, levels[i], levels[j]) < 0
	})
	return levels
}

func writeVariantPayload(
	tmpFile *os.File,
	lvl string,
	path string,
	encLevel zstd.EncoderLevel,
	currentOffset int64,
) (format.VariantEntry, int64, error) {
	variantPath := filepath.Clean(path)
	variantBytes, err := os.ReadFile(variantPath)
	if err != nil {
		return format.VariantEntry{}, 0, fmt.Errorf("%w: %s (%w)", ErrVariantNotFound, variantPath, err)
	}

	rawHash := sha256.Sum256(variantBytes)
	rawHashHex := hex.EncodeToString(rawHash[:])
	uncompressedSize := int64(len(variantBytes))
	variantOffset := currentOffset

	zstdWriter, err := zstd.NewWriter(tmpFile, zstd.WithEncoderLevel(encLevel))
	if err != nil {
		return format.VariantEntry{}, 0, fmt.Errorf("initializing zstd writer: %w", err)
	}

	if _, err := zstdWriter.Write(variantBytes); err != nil {
		_ = zstdWriter.Close()
		return format.VariantEntry{}, 0, fmt.Errorf("compressing variant %s: %w", lvl, err)
	}
	if err := zstdWriter.Close(); err != nil {
		return format.VariantEntry{}, 0, fmt.Errorf("closing zstd writer for %s: %w", lvl, err)
	}

	newOffset, err := tmpFile.Seek(0, io.SeekCurrent)
	if err != nil {
		return format.VariantEntry{}, 0, fmt.Errorf("seeking current file offset: %w", err)
	}

	entry := format.VariantEntry{
		Level:            lvl,
		Offset:           variantOffset,
		CompressedSize:   newOffset - variantOffset,
		UncompressedSize: uncompressedSize,
		SHA256:           rawHashHex,
		Compression:      "zstd",
	}
	return entry, newOffset, nil
}

func finalizeOutputFile(tmpFile *os.File, tmpPath, outputPath string, perms os.FileMode) error {
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("syncing output file: %w", err)
	}
	if err := tmpFile.Chmod(perms); err != nil {
		return fmt.Errorf("chmodding output file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing output file: %w", err)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return fmt.Errorf("moving temporary file to output %s: %w", outputPath, err)
	}
	return nil
}

// TrimBinary extracts only the specified targetLevel variant, keeping the launcher stub intact,
// and produces a single-variant trimmed fat binary written to out.
func TrimBinary(r io.ReaderAt, totalSize int64, targetLevel string, out io.Writer) (*format.Index, error) {
	idx, err := format.ReadTrailerAndIndex(r, totalSize)
	if err != nil {
		return nil, fmt.Errorf("reading index: %w", err)
	}

	if len(idx.Variants) == 0 {
		return nil, errors.New("binary index contains no variants")
	}

	selected, found := idx.FindVariant(targetLevel)
	if !found {
		return nil, fmt.Errorf("variant level %q not found in binary manifest", targetLevel)
	}

	stubSize := idx.Variants[0].Offset
	if stubSize <= 0 || stubSize > totalSize {
		return nil, errors.New("invalid stub offset in binary index")
	}

	// 1. Copy Stub Binary Bytes (offset 0 to stubSize)
	stubReader := io.NewSectionReader(r, 0, stubSize)
	if _, err := io.Copy(out, stubReader); err != nil {
		return nil, fmt.Errorf("copying stub binary: %w", err)
	}

	// 2. Copy Compressed Variant Frame
	varReader := io.NewSectionReader(r, selected.Offset, selected.CompressedSize)
	if _, err := io.Copy(out, varReader); err != nil {
		return nil, fmt.Errorf("copying variant frame: %w", err)
	}

	// 3. Assemble New Index with single entry right after the stub
	newIdx := &format.Index{
		Version:     idx.Version,
		AppName:     idx.AppName,
		TargetOS:    idx.TargetOS,
		TargetArch:  idx.TargetArch,
		CreatedUnix: time.Now().Unix(),
		Variants: []format.VariantEntry{
			{
				Level:            selected.Level,
				Offset:           stubSize,
				CompressedSize:   selected.CompressedSize,
				UncompressedSize: selected.UncompressedSize,
				SHA256:           selected.SHA256,
				Compression:      selected.Compression,
			},
		},
	}

	// 4. Write New Index and Trailer
	indexOffset := stubSize + selected.CompressedSize
	if _, err := format.WriteIndexAndTrailer(out, newIdx, indexOffset); err != nil {
		return nil, fmt.Errorf("writing trimmed index and trailer: %w", err)
	}

	return newIdx, nil
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

// ValidateELFBinary checks if the file at path is a valid 64-bit ELF binary matching targetOS and targetArch.
func ValidateELFBinary(path string, targetOS, targetArch string) error {
	f, err := elf.Open(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("%w (%s): %v", ErrInvalidELF, path, err)
	}
	defer func() { _ = f.Close() }()

	if f.Class != elf.ELFCLASS64 {
		return fmt.Errorf("%w (%s): expected 64-bit ELF, got class %v", ErrInvalidELF, path, f.Class)
	}

	switch targetArch {
	case "amd64", "x86_64":
		if f.Machine != elf.EM_X86_64 {
			return fmt.Errorf("%w (%s): machine type %v does not match target architecture %s (expected EM_X86_64)",
				ErrInvalidELF, path, f.Machine, targetArch)
		}
	case "arm64", "aarch64":
		if f.Machine != elf.EM_AARCH64 {
			return fmt.Errorf("%w (%s): machine type %v does not match target architecture %s (expected EM_AARCH64)",
				ErrInvalidELF, path, f.Machine, targetArch)
		}
	}
	return nil
}

// PrewarmVariant extracts and validates an embedded variant into the specified cache directory.
// If the variant payload is already cached with matching size and valid checksum, it skips extraction.
func PrewarmVariant(
	r io.ReaderAt,
	entry *format.VariantEntry,
	cacheDir string,
) (cachedPath string, alreadyCached bool, duration time.Duration, err error) {
	if entry.SHA256 == "" {
		return "", false, 0, errors.New("variant missing SHA-256 checksum")
	}

	cleanDir := filepath.Clean(cacheDir)
	if err := os.MkdirAll(cleanDir, format.PrivateCacheDirMode); err != nil {
		return "", false, 0, fmt.Errorf("creating cache dir %s: %w", cleanDir, err)
	}

	cachedBinary := filepath.Join(cleanDir, filepath.Clean(entry.SHA256))
	if stat, err := os.Stat(cachedBinary); err == nil {
		if stat.Size() == entry.UncompressedSize {
			return cachedBinary, true, 0, nil
		}
	}

	tmpFile, err := os.CreateTemp(cleanDir, ".prewarm-*.tmp")
	if err != nil {
		return "", false, 0, fmt.Errorf("creating temp file in %s: %w", cleanDir, err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	decompStart := time.Now()
	secReader := io.NewSectionReader(r, entry.Offset, entry.CompressedSize)
	zstdReader, err := zstd.NewReader(secReader)
	if err != nil {
		return "", false, 0, fmt.Errorf("creating zstd reader for %s: %w", entry.Level, err)
	}
	defer zstdReader.Close()

	hasher := sha256.New()
	mw := io.MultiWriter(tmpFile, hasher)
	written, err := io.Copy(mw, zstdReader)
	if err != nil {
		return "", false, 0, fmt.Errorf("decompressing variant %s: %w", entry.Level, err)
	}

	decompDuration := time.Since(decompStart)

	if written != entry.UncompressedSize {
		return "", false, 0, fmt.Errorf("%w: expected %d bytes, got %d", ErrSizeMismatch, entry.UncompressedSize, written)
	}

	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != entry.SHA256 {
		return "", false, 0, fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, entry.SHA256, actualHash)
	}

	if err := tmpFile.Chmod(format.PrivateExecMode); err != nil {
		return "", false, 0, fmt.Errorf("setting permissions on %s: %w", tmpPath, err)
	}
	if err := tmpFile.Sync(); err != nil {
		return "", false, 0, fmt.Errorf("syncing temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", false, 0, fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, cachedBinary); err != nil {
		return "", false, 0, fmt.Errorf("atomically renaming cached variant to %s: %w", cachedBinary, err)
	}

	return cachedBinary, false, decompDuration, nil
}

// PrewarmBinary extracts the specified variant levels (or all variants if targetLevels is empty)
// from the fat executable into the resolved cache directory.
func PrewarmBinary(
	r io.ReaderAt,
	totalSize int64,
	targetLevels []string,
	cacheDir string,
) (*format.Index, []format.PrewarmResult, error) {
	idx, err := format.ReadTrailerAndIndex(r, totalSize)
	if err != nil {
		return nil, nil, fmt.Errorf("reading index: %w", err)
	}

	if cacheDir == "" {
		resolved, err := format.ResolveCacheDir("")
		if err != nil {
			return nil, nil, fmt.Errorf("resolving cache directory: %w", err)
		}
		cacheDir = resolved
	}

	// Validate target levels if specified
	if len(targetLevels) > 0 {
		for _, lvl := range targetLevels {
			if _, found := idx.FindVariant(lvl); !found {
				return nil, nil, fmt.Errorf("variant level %q not found in binary manifest", lvl)
			}
		}
	}

	results := make([]format.PrewarmResult, 0, len(idx.Variants))
	for _, v := range idx.Variants {
		if len(targetLevels) > 0 {
			match := false
			for _, lvl := range targetLevels {
				if v.Level == lvl {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}

		path, alreadyCached, duration, err := PrewarmVariant(r, &v, cacheDir)
		if err != nil {
			return nil, nil, fmt.Errorf("prewarming variant %s: %w", v.Level, err)
		}

		results = append(results, format.PrewarmResult{
			Level:            v.Level,
			SHA256:           v.SHA256,
			UncompressedSize: v.UncompressedSize,
			CachedPath:       path,
			AlreadyCached:    alreadyCached,
			DecompressionUs:  duration.Microseconds(),
		})
	}

	return idx, results, nil
}

