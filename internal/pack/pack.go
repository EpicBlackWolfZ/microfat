// Package pack implements the compression and assembly engine for microfat binaries.
package pack

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cache"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

// Standard permissions and buffer sizes.
const (
	defaultFileMode = 0o755
)

// Common error definitions.
var (
	ErrNoVariantsSpecified = format.ErrNoVariantsSpecified
	ErrDuplicateVariant    = format.ErrDuplicateVariant
	ErrInvalidVariant      = format.ErrInvalidVariant
	ErrStubMissing         = errors.New("stub binary path is required and must exist")
	ErrVariantNotFound     = errors.New("variant binary file not found")
	ErrChecksumMismatch    = errors.New("variant payload checksum mismatch")
	ErrSizeMismatch        = codec.ErrSizeMismatch
	ErrInvalidELF          = errors.New("invalid ELF binary")
)

// VariantCompressionOptions configures compression parameters for a specific variant level.
type VariantCompressionOptions struct {
	Profile     string
	Compression string
	Level       string
}

// WarnFunc is a callback invoked when non-fatal diagnostic warnings occur during packaging.
type WarnFunc func(format string, args ...any)

// Options configures the packaging process.
type Options struct {
	StubPath           string
	OutputPath         string
	AppName            string
	TargetOS           string
	TargetArch         string
	Variants           map[string]string // level -> binary path
	Profile            string            // "latency", "balanced", "size"
	Compression        string            // "zstd", "lz4", "none" (or "zstd:best")
	CompressionLevel   string            // level string or numeric
	EnableDict         bool              // Train and embed a shared Zstandard dictionary across variants
	DictSize           int               // Target dictionary size in bytes (default: 112 KB)
	VariantCompression map[string]VariantCompressionOptions
	Permissions        os.FileMode
	FormatVersion      int      // FormatVersion1 (JSON) or FormatVersion2 (Binary, default)
	SkipELFValidation  bool     // Optional flag to bypass ELF header validation (primarily for testing)
	WarnFunc           WarnFunc // Optional diagnostic warning callback for non-fatal assembly telemetry
}

func (o *Options) warnf(format string, args ...any) {
	if o != nil && o.WarnFunc != nil {
		o.WarnFunc(format, args...)
	}
}

// DefaultOptions returns a new Options instance initialized with safe, recommended defaults:
// Format v2 binary table, balanced profile with Zstandard compression, standard 0755 file permissions,
// linux/amd64 target OS/architecture, and an initialized variants map.
func DefaultOptions() Options {
	return Options{
		TargetOS:      "linux",
		TargetArch:    microarch.ArchAMD64,
		Variants:      make(map[string]string),
		Profile:       codec.ProfileBalanced,
		Compression:   codec.AlgorithmZstd,
		Permissions:   defaultFileMode,
		FormatVersion: format.FormatVersionCurrent,
	}
}

// VerificationResult contains the result of verifying an individual embedded variant.
type VerificationResult struct {
	Level            string `json:"level"`
	CompressedSize   int64  `json:"compressed_size"`
	UncompressedSize int64  `json:"uncompressed_size"`
	ExpectedSHA256   string `json:"expected_sha256"`
	ActualSHA256     string `json:"actual_sha256"`
	Valid            bool   `json:"valid"`
	Error            error  `json:"-"`
	ErrorString      string `json:"error,omitzero"`
}

const (
	sampleChunkSize    = 4 * 1024 // 4 KB per sample chunk
	maxSamplesPerFile  = 32
	minVariantsForDict = 2
)

func sampleVariantPayloads(variantPaths map[string]string, levels []string) ([][]byte, error) {
	const maxSampleBytes = 4 * 1024 * 1024
	var samples [][]byte
	remaining := maxSampleBytes
	for _, lvl := range levels {
		chunks, err := sampleInput(variantPaths[lvl], remaining)
		if err != nil {
			return nil, fmt.Errorf("sampling variant %s: %w", lvl, err)
		}
		for _, chunk := range chunks {
			remaining -= len(chunk)
		}
		samples = append(samples, chunks...)
	}
	return samples, nil
}

func sampleInput(path string, budget int) ([][]byte, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() <= 0 {
		return nil, ErrInvalidELF
	}
	if stat.Size() > format.MaxPayloadSize {
		return nil, format.ErrPayloadTooLarge
	}
	step := max(stat.Size()/maxSamplesPerFile, sampleChunkSize)
	var samples [][]byte
	for offset := int64(0); offset < stat.Size() && len(samples) < maxSamplesPerFile; offset += step {
		size := int(min(int64(sampleChunkSize), stat.Size()-offset))
		if size > budget {
			return nil, fmt.Errorf("%w: dictionary sample budget exceeded", format.ErrPayloadTooLarge)
		}
		chunk := make([]byte, size)
		if _, err := f.ReadAt(chunk, offset); err != nil {
			return nil, err
		}
		samples = append(samples, chunk)
		budget -= size
	}
	return samples, nil
}

func prepareSharedDictionary(opts *Options, levels []string) ([]byte, string, error) {
	enableDict := opts.EnableDict
	if !enableDict && strings.EqualFold(strings.TrimSpace(opts.Profile), codec.ProfileSize) && len(levels) >= minVariantsForDict {
		algo, _ := codec.ParseCompressionSpec(opts.Compression)
		if algo == "" || algo == codec.AlgorithmZstd {
			enableDict = true
		}
	}

	if !enableDict {
		return nil, "", nil
	}

	if len(levels) < minVariantsForDict {
		return nil, "", errors.New("training shared dictionary: requires at least two variants")
	}

	samples, err := sampleVariantPayloads(opts.Variants, levels)
	if err != nil {
		if opts.EnableDict {
			return nil, "", fmt.Errorf("sampling variants for dictionary training: %w", err)
		}
		opts.warnf("shared dictionary training failed (sampling variants: %v); proceeding with independent variant compression", err)
		return nil, "", nil
	}

	if len(samples) == 0 {
		if opts.EnableDict {
			return nil, "", errors.New("training shared dictionary: no sample data")
		}
		opts.warnf("shared dictionary training failed (no sample data); proceeding with independent variant compression")
		return nil, "", nil
	}

	dictSize := opts.DictSize
	if dictSize <= 0 {
		dictSize = codec.DefaultDictSize
	}

	dict, tErr := codec.TrainDictionary(samples, dictSize, opts.CompressionLevel)
	if tErr != nil {
		if opts.EnableDict {
			return nil, "", fmt.Errorf("training shared dictionary: %w", tErr)
		}
		opts.warnf("shared dictionary training failed (%v); proceeding with independent variant compression", tErr)
		return nil, "", nil
	}

	if len(dict) == 0 {
		if opts.EnableDict {
			return nil, "", errors.New("training shared dictionary: empty dictionary produced")
		}
		opts.warnf("shared dictionary training failed (empty dictionary produced); proceeding with independent variant compression")
		return nil, "", nil
	}

	h := sha256.Sum256(dict)
	return dict, hex.EncodeToString(h[:]), nil
}

// Pack stitches the stub and compressed variant binaries into a complete microfat fat executable.
func Pack(opts Options) (*format.Index, error) {
	// Validate configuration first; validate ELF bytes only after pinning the inputs.
	validateELF := !opts.SkipELFValidation
	opts.SkipELFValidation = true
	if err := validateOptions(&opts); err != nil {
		return nil, err
	}
	opts.SkipELFValidation = !validateELF
	cleanup, err := snapshotInputs(&opts)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	stubBytes, err := readBoundedInput(opts.StubPath, format.MaxPayloadSize)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStubMissing, err)
	}

	levels := sortVariantLevels(opts.Variants, opts.TargetArch)

	dictBytes, dictSHAHex, err := prepareSharedDictionary(&opts, levels)
	if err != nil {
		return nil, err
	}

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
		Version:     opts.FormatVersion,
		AppName:     opts.AppName,
		TargetOS:    opts.TargetOS,
		TargetArch:  opts.TargetArch,
		CreatedUnix: time.Now().Unix(),
		Variants:    make([]format.VariantEntry, 0, len(levels)),
	}

	// 2. Write Shared Dictionary (if trained)
	if len(dictBytes) > 0 {
		dictOffset := currentOffset
		dictSize := int64(len(dictBytes))
		if _, err := tmpFile.Write(dictBytes); err != nil {
			return nil, fmt.Errorf("writing dictionary payload: %w", err)
		}
		currentOffset += dictSize
		idx.DictionaryOffset = dictOffset
		idx.DictionarySize = dictSize
		idx.DictionarySHA256 = dictSHAHex
		idx.DictionaryID = codec.DefaultDictionaryID
	}

	// 3. Compress and write each variant payload
	for _, lvl := range levels {
		entry, newOffset, err := writeVariantPayload(tmpFile, lvl, opts.Variants[lvl], &opts, currentOffset, dictBytes)
		if err != nil {
			return nil, err
		}
		currentOffset = newOffset
		idx.Variants = append(idx.Variants, entry)
	}

	// 4. Write Index and Trailer
	if _, err := format.WriteIndexAndTrailerWithVersion(tmpFile, idx, currentOffset, opts.FormatVersion); err != nil {
		return nil, fmt.Errorf("writing index and trailer: %w", err)
	}

	// 5. Sync, chmod and atomically move
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
	if opts.FormatVersion != 0 && opts.FormatVersion != format.FormatVersion1 && opts.FormatVersion != format.FormatVersion2 {
		return fmt.Errorf("%w: got version %d, expected %d or %d",
			format.ErrUnsupportedVersion, opts.FormatVersion, format.FormatVersion1, format.FormatVersion2)
	}
	if opts.FormatVersion == 0 {
		opts.FormatVersion = format.FormatVersionCurrent
	}

	seenLevels := make(map[string]string, len(opts.Variants))
	for lvl, varPath := range opts.Variants {
		if strings.TrimSpace(lvl) == "" {
			return fmt.Errorf("%w: variant level must not be empty", ErrInvalidVariant)
		}
		normLvl := format.NormalizeVariant(lvl)
		if microarch.Rank(opts.TargetArch, normLvl) < 0 {
			return fmt.Errorf("%w: unrecognized or invalid microarchitecture level %q for arch %s",
				ErrInvalidVariant, lvl, opts.TargetArch)
		}
		if prev, exists := seenLevels[normLvl]; exists {
			return fmt.Errorf("%w: variant %q conflicts with %q (both normalize to %q)",
				ErrDuplicateVariant, lvl, prev, normLvl)
		}
		seenLevels[normLvl] = lvl

		if !opts.SkipELFValidation {
			if err := ValidateELFBinary(varPath, opts.TargetOS, opts.TargetArch); err != nil {
				return fmt.Errorf("validating variant %s: %w", lvl, err)
			}
		}
	}

	for lvl := range opts.VariantCompression {
		if strings.TrimSpace(lvl) == "" {
			return fmt.Errorf("%w: variant compression level must not be empty", ErrInvalidVariant)
		}
		normLvl := format.NormalizeVariant(lvl)
		if microarch.Rank(opts.TargetArch, normLvl) < 0 {
			return fmt.Errorf("%w: unrecognized or invalid variant compression level %q for arch %s",
				ErrInvalidVariant, lvl, opts.TargetArch)
		}
	}

	if !opts.SkipELFValidation {
		if err := ValidateELFBinary(opts.StubPath, opts.TargetOS, opts.TargetArch); err != nil {
			return fmt.Errorf("validating stub: %w", err)
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
	opts *Options,
	currentOffset int64,
	dict []byte,
) (format.VariantEntry, int64, error) {
	variantPath := filepath.Clean(path)
	variantBytes, err := readBoundedInput(variantPath, format.MaxPayloadSize)
	if err != nil {
		return format.VariantEntry{}, 0, fmt.Errorf("%w: %s (%w)", ErrVariantNotFound, variantPath, err)
	}

	rawHash := sha256.Sum256(variantBytes)
	rawHashHex := hex.EncodeToString(rawHash[:])
	uncompressedSize := int64(len(variantBytes))
	variantOffset := currentOffset

	profile := opts.Profile
	compAlgo := opts.Compression
	compLevel := opts.CompressionLevel

	normLvl := format.NormalizeVariant(lvl)
	if varComp, ok := opts.VariantCompression[lvl]; ok {
		if varComp.Profile != "" {
			profile = varComp.Profile
		}
		if varComp.Compression != "" {
			compAlgo = varComp.Compression
		}
		if varComp.Level != "" {
			compLevel = varComp.Level
		}
	} else if varComp, ok := opts.VariantCompression[normLvl]; ok {
		if varComp.Profile != "" {
			profile = varComp.Profile
		}
		if varComp.Compression != "" {
			compAlgo = varComp.Compression
		}
		if varComp.Level != "" {
			compLevel = varComp.Level
		}
	}

	c, resolvedLevel, err := codec.ResolveCompression(profile, compAlgo, compLevel, uncompressedSize)
	if err != nil {
		return format.VariantEntry{}, 0, fmt.Errorf("resolving compression for variant %s: %w", lvl, err)
	}

	if len(dict) > 0 {
		if dc, ok := c.(codec.DictCodec); ok {
			if err := dc.CompressWithDict(tmpFile, variantBytes, resolvedLevel, dict); err != nil {
				return format.VariantEntry{}, 0, fmt.Errorf("compressing variant %s with dict: %w", lvl, err)
			}
		} else {
			if err := c.Compress(tmpFile, variantBytes, resolvedLevel); err != nil {
				return format.VariantEntry{}, 0, fmt.Errorf("compressing variant %s with codec %s: %w", lvl, c.Name(), err)
			}
		}
	} else {
		if err := c.Compress(tmpFile, variantBytes, resolvedLevel); err != nil {
			return format.VariantEntry{}, 0, fmt.Errorf("compressing variant %s with codec %s: %w", lvl, c.Name(), err)
		}
	}

	newOffset, err := tmpFile.Seek(0, io.SeekCurrent)
	if err != nil {
		return format.VariantEntry{}, 0, fmt.Errorf("seeking current file offset: %w", err)
	}

	entry := format.VariantEntry{
		Level:            normLvl,
		Offset:           variantOffset,
		CompressedSize:   newOffset - variantOffset,
		UncompressedSize: uncompressedSize,
		SHA256:           rawHashHex,
		Compression:      c.Name(),
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
	if idx.DictionarySize > 0 {
		stubSize = idx.DictionaryOffset
	}
	if stubSize <= 0 || stubSize > totalSize {
		return nil, errors.New("invalid stub offset in binary index")
	}

	// 1. Copy Stub Binary Bytes (offset 0 to stubSize)
	stubReader := io.NewSectionReader(r, 0, stubSize)
	if _, err := io.Copy(out, stubReader); err != nil {
		return nil, fmt.Errorf("copying stub binary: %w", err)
	}

	currentOffset := stubSize
	var newIdx *format.Index

	if idx.DictionarySize > 0 {
		// 2. Copy Shared Dictionary
		dictReader := io.NewSectionReader(r, idx.DictionaryOffset, idx.DictionarySize)
		if _, err := io.Copy(out, dictReader); err != nil {
			return nil, fmt.Errorf("copying dictionary frame: %w", err)
		}
		dictOffset := currentOffset
		currentOffset += idx.DictionarySize

		// 3. Copy Compressed Variant Frame
		varReader := io.NewSectionReader(r, selected.Offset, selected.CompressedSize)
		if _, err := io.Copy(out, varReader); err != nil {
			return nil, fmt.Errorf("copying variant frame: %w", err)
		}
		variantOffset := currentOffset
		currentOffset += selected.CompressedSize

		newIdx = &format.Index{
			Version:          idx.Version,
			AppName:          idx.AppName,
			TargetOS:         idx.TargetOS,
			TargetArch:       idx.TargetArch,
			CreatedUnix:      time.Now().Unix(),
			DictionaryOffset: dictOffset,
			DictionarySize:   idx.DictionarySize,
			DictionarySHA256: idx.DictionarySHA256,
			DictionaryID:     idx.DictionaryID,
			Variants: []format.VariantEntry{
				{
					Level:            selected.Level,
					Offset:           variantOffset,
					CompressedSize:   selected.CompressedSize,
					UncompressedSize: selected.UncompressedSize,
					SHA256:           selected.SHA256,
					Compression:      selected.Compression,
				},
			},
		}
	} else {
		// 2. Copy Compressed Variant Frame
		varReader := io.NewSectionReader(r, selected.Offset, selected.CompressedSize)
		if _, err := io.Copy(out, varReader); err != nil {
			return nil, fmt.Errorf("copying variant frame: %w", err)
		}
		variantOffset := currentOffset
		currentOffset += selected.CompressedSize

		newIdx = &format.Index{
			Version:     idx.Version,
			AppName:     idx.AppName,
			TargetOS:    idx.TargetOS,
			TargetArch:  idx.TargetArch,
			CreatedUnix: time.Now().Unix(),
			Variants: []format.VariantEntry{
				{
					Level:            selected.Level,
					Offset:           variantOffset,
					CompressedSize:   selected.CompressedSize,
					UncompressedSize: selected.UncompressedSize,
					SHA256:           selected.SHA256,
					Compression:      selected.Compression,
				},
			},
		}
	}

	// 4. Write New Index and Trailer
	if _, err := format.WriteIndexAndTrailer(out, newIdx, currentOffset); err != nil {
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

	var dictBytes []byte
	if idx.DictionarySize > 0 {
		if idx.DictionarySize > format.MaxDictionarySize || idx.DictionaryOffset < 0 {
			return nil, nil, fmt.Errorf("%w: dictionary size %d or offset %d out of bounds",
				format.ErrInvalidDictionary, idx.DictionarySize, idx.DictionaryOffset)
		}
		if idx.DictionarySHA256 == "" || !format.ValidateChecksum(idx.DictionarySHA256) {
			return nil, nil, fmt.Errorf("%w: dictionary missing or invalid sha256 checksum", format.ErrInvalidChecksum)
		}
		dictBytes = make([]byte, idx.DictionarySize)
		if _, err := r.ReadAt(dictBytes, idx.DictionaryOffset); err != nil {
			return nil, nil, fmt.Errorf("reading shared dictionary: %w", err)
		}
		h := sha256.Sum256(dictBytes)
		actualHex := hex.EncodeToString(h[:])
		if actualHex != idx.DictionarySHA256 {
			return nil, nil, fmt.Errorf("%w: expected %s, got %s", format.ErrDictionaryCorrupted, idx.DictionarySHA256, actualHex)
		}
	}

	results := make([]VerificationResult, 0, len(idx.Variants))
	for _, v := range idx.Variants {
		res := VerificationResult{
			Level:            v.Level,
			CompressedSize:   v.CompressedSize,
			UncompressedSize: v.UncompressedSize,
			ExpectedSHA256:   v.SHA256,
		}

		c, err := codec.Get(v.Compression)
		if err != nil {
			res.Error = fmt.Errorf("lookup codec %q for variant %s: %w", v.Compression, v.Level, err)
			res.ErrorString = res.Error.Error()
			results = append(results, res)
			continue
		}

		secReader := io.NewSectionReader(r, v.Offset, v.CompressedSize)
		hasher := sha256.New()
		if err := codec.DecompressWithOptionalDict(c, hasher, secReader, v.UncompressedSize, dictBytes); err != nil {
			res.Error = fmt.Errorf("decompressing variant payload: %w", err)
			res.ErrorString = res.Error.Error()
			results = append(results, res)
			continue
		}

		actualHashHex := hex.EncodeToString(hasher.Sum(nil))
		res.ActualSHA256 = actualHashHex

		if v.SHA256 == "" || !format.ValidateChecksum(v.SHA256) {
			res.Error = fmt.Errorf("%w: missing or invalid expected sha256 checksum for variant %s", format.ErrInvalidChecksum, v.Level)
			res.ErrorString = res.Error.Error()
			results = append(results, res)
			continue
		} else if actualHashHex != v.SHA256 {
			res.Error = fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, v.SHA256, actualHashHex)
			res.ErrorString = res.Error.Error()
			results = append(results, res)
			continue
		}

		res.Valid = true
		results = append(results, res)
	}

	return idx, results, nil
}

// ValidateELFBinary checks if the file at path is a valid 64-bit ELF binary matching targetOS and targetArch.
func ValidateELFBinary(path string, targetOS, targetArch string) error {
	data, err := readBoundedInput(path, format.MaxPayloadSize)
	if err != nil {
		return err
	}
	f, err := elf.NewFile(bytes.NewReader(data))
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
	return PrewarmVariantWithDict(r, entry, cacheDir, nil)
}

// PrewarmVariantWithDict extracts and validates an embedded variant using an optional shared dictionary.
func PrewarmVariantWithDict(
	r io.ReaderAt,
	entry *format.VariantEntry,
	cacheDir string,
	dict []byte,
) (cachedPath string, alreadyCached bool, duration time.Duration, err error) {
	if entry == nil {
		return "", false, 0, errors.New("nil variant entry")
	}
	if entry.SHA256 == "" || !format.ValidateChecksum(entry.SHA256) {
		return "", false, 0, fmt.Errorf("%w: invalid or missing variant checksum %q", format.ErrInvalidChecksum, entry.SHA256)
	}
	if entry.UncompressedSize <= 0 || entry.UncompressedSize > format.MaxPayloadSize {
		return "", false, 0, fmt.Errorf("%w: invalid variant uncompressed size %d", format.ErrPayloadTooLarge, entry.UncompressedSize)
	}

	dirFD, cleanDir, err := format.ResolveCacheDirFD(cacheDir)
	if err != nil {
		return "", false, 0, fmt.Errorf("resolving cache dir %s: %w", cacheDir, err)
	}
	if dirFD >= 0 {
		defer func() { _ = cache.CloseFD(dirFD) }()
	}

	cachedName := filepath.Clean(entry.SHA256)
	cachedBinary := filepath.Join(cleanDir, cachedName)

	if dirFD >= 0 {
		vfd, openErr := cache.OpenAndValidateVariantAtFD(dirFD, cachedName, entry, false)
		if openErr == nil {
			_ = cache.CloseFD(vfd)
			return cachedBinary, true, 0, nil
		}
		if cache.IsSymlinkErr(openErr) || errors.Is(openErr, cache.ErrNonRegularFile) || errors.Is(openErr, cache.ErrUnsafeFile) {
			return "", false, 0, fmt.Errorf("%w: refusal to prewarm over symlink or non-regular file at %s: %w",
				format.ErrCacheWrite, cachedBinary, openErr)
		}
	} else {
		fi, lstatErr := os.Lstat(cachedBinary)
		if lstatErr == nil {
			if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
				return "", false, 0, fmt.Errorf("%w: refusal to prewarm over symlink or non-regular file at %s",
					format.ErrCacheWrite, cachedBinary)
			}
			if verifyCachedBinary(cachedBinary, entry.UncompressedSize, entry.SHA256) {
				return cachedBinary, true, 0, nil
			}
		}
	}

	c, err := codec.Get(entry.Compression)
	if err != nil {
		return "", false, 0, fmt.Errorf("lookup codec %q for %s: %w", entry.Compression, entry.Level, err)
	}

	secReader := io.NewSectionReader(r, entry.Offset, entry.CompressedSize)
	decompStart := time.Now()
	cachedBinary, err = cache.MaterializeVariantAtFD(dirFD, cleanDir, entry, func(w io.Writer) error {
		return codec.DecompressWithOptionalDict(c, w, secReader, entry.UncompressedSize, dict)
	})
	if err != nil {
		return "", false, 0, err
	}
	decompDuration := time.Since(decompStart)

	return cachedBinary, false, decompDuration, nil
}

// verifyCachedBinary validates that a cached binary exists, is a regular file (not a symlink),
// matches the expected size, and strictly matches the expected SHA-256 checksum over the open descriptor.
func verifyCachedBinary(path string, expectedSize int64, expectedSHA256 string) bool {
	return cache.VerifyBinary(path, expectedSize, expectedSHA256)
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

	var dictBytes []byte
	if idx.DictionarySize > 0 {
		if idx.DictionarySize > format.MaxDictionarySize || idx.DictionaryOffset < 0 {
			return nil, nil, fmt.Errorf("%w: dictionary size %d or offset %d out of bounds",
				format.ErrInvalidDictionary, idx.DictionarySize, idx.DictionaryOffset)
		}
		if idx.DictionarySHA256 == "" || !format.ValidateChecksum(idx.DictionarySHA256) {
			return nil, nil, fmt.Errorf("%w: dictionary missing or invalid sha256 checksum", format.ErrInvalidChecksum)
		}
		dictBytes = make([]byte, idx.DictionarySize)
		if _, err := r.ReadAt(dictBytes, idx.DictionaryOffset); err != nil {
			return nil, nil, fmt.Errorf("reading shared dictionary: %w", err)
		}
		h := sha256.Sum256(dictBytes)
		actualHex := hex.EncodeToString(h[:])
		if actualHex != idx.DictionarySHA256 {
			return nil, nil, fmt.Errorf("%w: expected %s, got %s", format.ErrDictionaryCorrupted, idx.DictionarySHA256, actualHex)
		}
	}

	targetCap := len(idx.Variants)
	var targetSet map[string]struct{}
	if len(targetLevels) > 0 {
		targetSet = make(map[string]struct{}, len(targetLevels))
		for _, lvl := range targetLevels {
			entry, found := idx.FindVariant(lvl)
			if !found {
				return nil, nil, fmt.Errorf("variant level %q not found in binary manifest", lvl)
			}
			targetSet[entry.Level] = struct{}{}
		}
		if len(targetSet) < targetCap {
			targetCap = len(targetSet)
		}
	}

	results := make([]format.PrewarmResult, 0, targetCap)
	for _, v := range idx.Variants {
		if targetSet != nil {
			if _, ok := targetSet[v.Level]; !ok {
				continue
			}
		}

		path, alreadyCached, duration, err := PrewarmVariantWithDict(r, &v, cacheDir, dictBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("prewarming variant %s: %w", v.Level, err)
		}

		status := format.PrewarmStatusExtracted
		if alreadyCached {
			status = format.PrewarmStatusAlreadyCached
		}

		results = append(results, format.PrewarmResult{
			Level:            v.Level,
			SHA256:           v.SHA256,
			UncompressedSize: v.UncompressedSize,
			CachedPath:       path,
			AlreadyCached:    alreadyCached,
			DecompressionUs:  duration.Microseconds(),
			Valid:            true,
			Status:           status,
		})
	}

	return idx, results, nil
}

// VerifyCacheVariant inspects the cache directory for an existing variant binary, validating
// its existence, uncompressed size, and SHA-256 checksum without modifying disk state.
func VerifyCacheVariant(
	entry *format.VariantEntry,
	cacheDir string,
) format.PrewarmResult {
	return cache.VerifyVariant(entry, cacheDir)
}

// VerifyCacheBinary inspects the cache directory for the specified variant levels (or all variants if targetLevels is empty)
// and validates their presence, size, and SHA-256 integrity without modifying disk state.
func VerifyCacheBinary(
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

	targetCap := len(idx.Variants)
	var targetSet map[string]struct{}
	if len(targetLevels) > 0 {
		targetSet = make(map[string]struct{}, len(targetLevels))
		for _, lvl := range targetLevels {
			entry, found := idx.FindVariant(lvl)
			if !found {
				return nil, nil, fmt.Errorf("variant level %q not found in binary manifest", lvl)
			}
			targetSet[entry.Level] = struct{}{}
		}
		if len(targetSet) < targetCap {
			targetCap = len(targetSet)
		}
	}

	results := make([]format.PrewarmResult, 0, targetCap)
	for _, v := range idx.Variants {
		if targetSet != nil {
			if _, ok := targetSet[v.Level]; !ok {
				continue
			}
		}

		res := VerifyCacheVariant(&v, cacheDir)
		results = append(results, res)
	}

	return idx, results, nil
}
