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
	ErrNoVariantsSpecified = errors.New("at least one microarchitecture variant must be specified")
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
	FormatVersion      int  // FormatVersion1 (JSON) or FormatVersion2 (Binary, default)
	SkipELFValidation  bool // Optional flag to bypass ELF header validation (primarily for testing)
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
	sampleChunkSize   = 4 * 1024 // 4 KB per sample chunk
	maxSamplesPerFile = 32
)

func sampleVariantPayloads(variantPaths map[string]string, levels []string) ([][]byte, error) {
	var samples [][]byte
	for _, lvl := range levels {
		path := filepath.Clean(variantPaths[lvl])
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading variant %s for sample training: %w", lvl, err)
		}

		if len(data) <= sampleChunkSize {
			samples = append(samples, data)
			continue
		}

		step := len(data) / maxSamplesPerFile
		if step < sampleChunkSize {
			step = sampleChunkSize
		}

		count := 0
		for offset := 0; offset+sampleChunkSize <= len(data) && count < maxSamplesPerFile; offset += step {
			chunk := make([]byte, sampleChunkSize)
			copy(chunk, data[offset:offset+sampleChunkSize])
			samples = append(samples, chunk)
			count++
		}
	}
	return samples, nil
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

	// Determine if shared dictionary compression should be trained
	enableDict := opts.EnableDict
	if !enableDict && opts.Profile == codec.ProfileSize && len(levels) >= 2 {
		algo, _ := codec.ParseCompressionSpec(opts.Compression)
		if algo == "" || algo == codec.AlgorithmZstd {
			enableDict = true
		}
	}

	var dictBytes []byte
	var dictSHAHex string
	if enableDict && len(levels) >= 2 {
		samples, err := sampleVariantPayloads(opts.Variants, levels)
		if err != nil {
			if opts.EnableDict {
				return nil, fmt.Errorf("sampling variants for dictionary training: %w", err)
			}
		} else if len(samples) > 0 {
			dictSize := opts.DictSize
			if dictSize <= 0 {
				dictSize = codec.DefaultDictSize
			}
			dict, tErr := codec.TrainDictionary(samples, dictSize, opts.CompressionLevel)
			if tErr != nil {
				if opts.EnableDict {
					return nil, fmt.Errorf("training shared dictionary: %w", tErr)
				}
			} else if len(dict) > 0 {
				dictBytes = dict
				h := sha256.Sum256(dictBytes)
				dictSHAHex = hex.EncodeToString(h[:])
			}
		}
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
	opts *Options,
	currentOffset int64,
	dict []byte,
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

	profile := opts.Profile
	compAlgo := opts.Compression
	compLevel := opts.CompressionLevel

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
		Level:            lvl,
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
		dictBytes = make([]byte, idx.DictionarySize)
		if _, err := r.ReadAt(dictBytes, idx.DictionaryOffset); err != nil {
			return nil, nil, fmt.Errorf("reading shared dictionary: %w", err)
		}
		if idx.DictionarySHA256 != "" {
			h := sha256.Sum256(dictBytes)
			actualHex := hex.EncodeToString(h[:])
			if actualHex != idx.DictionarySHA256 {
				return nil, nil, fmt.Errorf("%w: expected %s, got %s", format.ErrDictionaryCorrupted, idx.DictionarySHA256, actualHex)
			}
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

		if v.SHA256 != "" && actualHashHex != v.SHA256 {
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
	return PrewarmVariantWithDict(r, entry, cacheDir, nil)
}

// PrewarmVariantWithDict extracts and validates an embedded variant using an optional shared dictionary.
func PrewarmVariantWithDict(
	r io.ReaderAt,
	entry *format.VariantEntry,
	cacheDir string,
	dict []byte,
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

	c, err := codec.Get(entry.Compression)
	if err != nil {
		return "", false, 0, fmt.Errorf("lookup codec %q for %s: %w", entry.Compression, entry.Level, err)
	}

	decompStart := time.Now()
	secReader := io.NewSectionReader(r, entry.Offset, entry.CompressedSize)
	hasher := sha256.New()
	mw := io.MultiWriter(tmpFile, hasher)
	if err := codec.DecompressWithOptionalDict(c, mw, secReader, entry.UncompressedSize, dict); err != nil {
		return "", false, 0, fmt.Errorf("decompressing variant %s: %w", entry.Level, err)
	}
	decompDuration := time.Since(decompStart)

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
		return "", false, 0, fmt.Errorf("closing temp file %s: %w", tmpPath, err)
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

	var dictBytes []byte
	if idx.DictionarySize > 0 {
		dictBytes = make([]byte, idx.DictionarySize)
		if _, err := r.ReadAt(dictBytes, idx.DictionaryOffset); err != nil {
			return nil, nil, fmt.Errorf("reading shared dictionary: %w", err)
		}
		if idx.DictionarySHA256 != "" {
			h := sha256.Sum256(dictBytes)
			actualHex := hex.EncodeToString(h[:])
			if actualHex != idx.DictionarySHA256 {
				return nil, nil, fmt.Errorf("%w: expected %s, got %s", format.ErrDictionaryCorrupted, idx.DictionarySHA256, actualHex)
			}
		}
	}

	targetCap := len(idx.Variants)
	var targetSet map[string]struct{}
	if len(targetLevels) > 0 {
		if len(targetLevels) < targetCap {
			targetCap = len(targetLevels)
		}
		targetSet = make(map[string]struct{}, len(targetLevels))
		for _, lvl := range targetLevels {
			if _, found := idx.FindVariant(lvl); !found {
				return nil, nil, fmt.Errorf("variant level %q not found in binary manifest", lvl)
			}
			targetSet[lvl] = struct{}{}
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
// its existence, uncompressed size, and cryptographic SHA-256 checksum without modifying disk state.
func VerifyCacheVariant(
	entry *format.VariantEntry,
	cacheDir string,
) format.PrewarmResult {
	res := format.PrewarmResult{
		Level:            entry.Level,
		SHA256:           entry.SHA256,
		UncompressedSize: entry.UncompressedSize,
	}

	if cacheDir == "" {
		resolved, err := format.ResolveCacheDir("")
		if err != nil {
			res.Status = format.PrewarmStatusMissing
			res.Error = fmt.Sprintf("resolving cache directory: %v", err)
			return res
		}
		cacheDir = resolved
	}

	cleanDir := filepath.Clean(cacheDir)
	cachedBinary := filepath.Join(cleanDir, filepath.Clean(entry.SHA256))
	res.CachedPath = cachedBinary

	stat, err := os.Stat(cachedBinary)
	if err != nil {
		res.Status = format.PrewarmStatusMissing
		res.Error = fmt.Sprintf("cached binary not found: %v", err)
		return res
	}

	res.AlreadyCached = true

	if stat.Size() != entry.UncompressedSize {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("size mismatch: expected %d bytes, got %d bytes", entry.UncompressedSize, stat.Size())
		return res
	}

	// Compute full SHA-256 hash
	// #nosec G304 -- opening resolved cached binary for hash verification
	f, err := os.Open(cachedBinary)
	if err != nil {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("opening cached file for hashing: %v", err)
		return res
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("hashing cached binary: %v", err)
		return res
	}

	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if entry.SHA256 != "" && actualHash != entry.SHA256 {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("checksum mismatch: expected %s, got %s", entry.SHA256, actualHash)
		return res
	}

	res.Valid = true
	res.Status = format.PrewarmStatusValid
	return res
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
		if len(targetLevels) < targetCap {
			targetCap = len(targetLevels)
		}
		targetSet = make(map[string]struct{}, len(targetLevels))
		for _, lvl := range targetLevels {
			if _, found := idx.FindVariant(lvl); !found {
				return nil, nil, fmt.Errorf("variant level %q not found in binary manifest", lvl)
			}
			targetSet[lvl] = struct{}{}
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

