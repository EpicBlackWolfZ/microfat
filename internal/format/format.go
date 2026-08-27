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
	"os"
	"path/filepath"
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

	// Environment variable names for telemetry and control.
	EnvSelectedVariant  = "MICROFAT_SELECTED_VARIANT"
	EnvHostArch         = "MICROFAT_HOST_ARCH"
	EnvHostLevel        = "MICROFAT_HOST_LEVEL"
	EnvExecMode         = "MICROFAT_EXEC_MODE"
	EnvDispatchMode     = "MICROFAT_DISPATCH_MODE"
	EnvSelectedSHA256   = "MICROFAT_SELECTED_SHA256"
	EnvSelectedSize     = "MICROFAT_SELECTED_SIZE"
	EnvCgroupVersion    = "MICROFAT_CGROUP_VERSION"
	EnvCgroupGOMEMLIMIT = "MICROFAT_CGROUP_GOMEMLIMIT"
	EnvCgroupGOMAXPROCS = "MICROFAT_CGROUP_GOMAXPROCS"
	EnvCgroupLimitBytes = "MICROFAT_CGROUP_LIMIT_BYTES"
	EnvCgroupCPUs       = "MICROFAT_CGROUP_CPUS"
	EnvDebug                       = "MICROFAT_DEBUG"
	EnvLog                         = "MICROFAT_LOG"
	EnvAutotune                    = "MICROFAT_AUTOTUNE"
	EnvMemRatio                    = "MICROFAT_MEM_RATIO"
	EnvForceLevel                  = "MICROFAT_FORCE_LEVEL"
	EnvMaxLevel                    = "MICROFAT_MAX_LEVEL"
	EnvDisableVariants             = "MICROFAT_DISABLE_VARIANTS"
	EnvPolicy                      = "MICROFAT_POLICY"
	EnvAVX512DownclockProtection   = "MICROFAT_AVX512_DOWNCLOCK_PROTECTION"
	EnvCacheDir                    = "MICROFAT_CACHE_DIR"
	EnvPolicyApplied               = "MICROFAT_POLICY_APPLIED"
	EnvOverrideReason              = "MICROFAT_OVERRIDE_REASON"

	// Execution modes.
	ExecModeMemfd = "memfd"
	ExecModeCache = "cache"

	// Telemetry event types.
	EventDispatch = "dispatch"
	EventError    = "dispatch_error"
	EventPrewarm  = "prewarm"

	// File permissions.
	PrivateCacheDirMode = 0o700
	PrivateExecMode     = 0o700
)

var userHomeDirFunc = os.UserHomeDir

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
	ErrOverlappingVariant = errors.New("variant payloads overlap or are unsorted")
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

// DispatchTelemetry records structured runtime execution metadata and performance timings.
type DispatchTelemetry struct {
	Event                   string  `json:"event"`
	TimestampUnixNano       int64   `json:"timestamp_unix_nano"`
	HostArch                string  `json:"host_arch"`
	HostLevel               string  `json:"host_level"`
	SelectedVariant         string  `json:"selected_variant"`
	SelectedSHA256          string  `json:"selected_sha256,omitempty"`
	SelectedSizeBytes       int64   `json:"selected_size_bytes,omitempty"`
	ExecMode                string  `json:"exec_mode"`
	PolicyApplied           string  `json:"policy_applied,omitempty"`
	PolicyReason            string  `json:"policy_reason,omitempty"`
	CgroupVersion           int     `json:"cgroup_version,omitempty"`
	CgroupMemLimitBytes     int64   `json:"cgroup_mem_limit_bytes,omitempty"`
	CgroupCPUQuota          float64 `json:"cgroup_cpu_quota,omitempty"`
	GOMEMLIMIT              string  `json:"gomemlimit,omitempty"`
	GOMAXPROCS              string  `json:"gomaxprocs,omitempty"`
	DecompressionDurationUs int64   `json:"decompression_duration_us,omitempty"`
	TotalLauncherUs         int64   `json:"total_launcher_us"`
}

// ErrorTelemetry records structured error events during launcher initialization or dispatch.
type ErrorTelemetry struct {
	Event             string `json:"event"`
	TimestampUnixNano int64  `json:"timestamp_unix_nano"`
	HostArch          string `json:"host_arch,omitempty"`
	HostLevel         string `json:"host_level,omitempty"`
	SelectedVariant   string `json:"selected_variant,omitempty"`
	PolicyApplied     string `json:"policy_applied,omitempty"`
	PolicyReason      string `json:"policy_reason,omitempty"`
	Stage             string `json:"stage"`
	Error             string `json:"error"`
	Details           string `json:"details,omitempty"`
	Hint              string `json:"hint,omitempty"`
}

// Prewarm and verify status constants.
const (
	PrewarmStatusExtracted     = "extracted"
	PrewarmStatusAlreadyCached = "already_cached"
	PrewarmStatusValid         = "valid"
	PrewarmStatusMissing       = "missing"
	PrewarmStatusCorrupted     = "corrupted"
)

// PrewarmResult records the cache status for an individual prewarmed or verified variant.
type PrewarmResult struct {
	Level            string `json:"level"`
	SHA256           string `json:"sha256"`
	UncompressedSize int64  `json:"uncompressed_size"`
	CachedPath       string `json:"cached_path"`
	AlreadyCached    bool   `json:"already_cached"`
	DecompressionUs  int64  `json:"decompression_us,omitempty"`
	Valid            bool   `json:"valid"`
	Status           string `json:"status,omitempty"`
	Error            string `json:"error,omitempty"`
}

// PrewarmTelemetry records structured telemetry for a prewarm operation.
type PrewarmTelemetry struct {
	Event             string          `json:"event"`
	TimestampUnixNano int64           `json:"timestamp_unix_nano"`
	AppName           string          `json:"app_name,omitempty"`
	CacheDir          string          `json:"cache_dir"`
	Results           []PrewarmResult `json:"results"`
}

// BinaryInfo represents the structured JSON output for launcher inspection.
type BinaryInfo struct {
	AppName         string         `json:"app_name"`
	TargetOS        string         `json:"target_os"`
	TargetArch      string         `json:"target_arch"`
	FatBinarySize   int64          `json:"fat_binary_size"`
	HostOS          string         `json:"host_os"`
	HostArch        string         `json:"host_arch"`
	HostLevel       string         `json:"host_level"`
	SelectedVariant string         `json:"selected_variant"`
	SelectedSize    int64          `json:"selected_size"`
	ExecMode        string         `json:"exec_mode"`
	PolicyApplied   string         `json:"policy_applied,omitempty"`
	PolicyReason    string         `json:"policy_reason,omitempty"`
	Cgroup          *CgroupInfo    `json:"cgroup,omitempty"`
	Variants        []VariantEntry `json:"variants"`
	HostFeatures    []string       `json:"host_features"`
}

// CgroupInfo represents container cgroup telemetry and auto-tuning limits.
type CgroupInfo struct {
	Version          int     `json:"version"`
	MemoryLimitBytes int64   `json:"memory_limit_bytes"`
	CPUQuota         float64 `json:"cpu_quota"`
	GOMEMLIMIT       string  `json:"gomemlimit,omitempty"`
	GOMAXPROCS       int     `json:"gomaxprocs,omitempty"`
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

	var lastEnd int64
	for i, v := range idx.Variants {
		if v.Offset < 0 || v.CompressedSize <= 0 || v.UncompressedSize <= 0 {
			return fmt.Errorf("%w: invalid dimensions for variant %s", ErrOutOfBounds, v.Level)
		}
		if v.UncompressedSize > MaxPayloadSize {
			return fmt.Errorf("%w: variant %s exceeds 1GB limit (%d bytes)", ErrPayloadTooLarge, v.Level, v.UncompressedSize)
		}
		if v.Offset+v.CompressedSize > indexOffset {
			return fmt.Errorf("%w: variant %s payload extends past index offset %d", ErrOutOfBounds, v.Level, indexOffset)
		}
		if i > 0 && v.Offset < lastEnd {
			return fmt.Errorf("%w: variant %s offset %d overlaps with previous variant ending at %d",
				ErrOverlappingVariant, v.Level, v.Offset, lastEnd)
		}
		lastEnd = v.Offset + v.CompressedSize
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

// ResolveCacheDir resolves and creates the microfat cache directory with 0700 permissions.
// Precedence:
//  1. customDir argument (if non-empty)
//  2. MICROFAT_CACHE_DIR environment variable (if set)
//  3. $XDG_CACHE_HOME/microfat (or ~/.cache/microfat)
//  4. Fallback: /tmp/.microfat-<uid>
func ResolveCacheDir(customDir string) (string, error) {
	if customDir != "" {
		cleanDir := filepath.Clean(customDir)
		if err := os.MkdirAll(cleanDir, PrivateCacheDirMode); err != nil {
			return "", fmt.Errorf("creating custom cache directory %s: %w", cleanDir, err)
		}
		return cleanDir, nil
	}

	if envDir := os.Getenv(EnvCacheDir); envDir != "" {
		cleanDir := filepath.Clean(envDir)
		if err := os.MkdirAll(cleanDir, PrivateCacheDirMode); err != nil {
			return "", fmt.Errorf("creating cache directory from %s (%s): %w", EnvCacheDir, cleanDir, err)
		}
		return cleanDir, nil
	}

	var primaryDir string
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		primaryDir = filepath.Join(xdg, "microfat")
	} else if home, err := userHomeDirFunc(); err == nil {
		primaryDir = filepath.Join(home, ".cache", "microfat")
	} else {
		primaryDir = filepath.Join(os.TempDir(), "microfat")
	}

	// #nosec G703 -- cache directory creation with private permissions
	if err := os.MkdirAll(primaryDir, PrivateCacheDirMode); err == nil {
		return primaryDir, nil
	}

	fallbackDir := filepath.Join(os.TempDir(), fmt.Sprintf(".microfat-%d", os.Getuid()))
	// #nosec G703 -- fallback cache directory creation
	if err := os.MkdirAll(fallbackDir, PrivateCacheDirMode); err == nil {
		return fallbackDir, nil
	}

	return "", fmt.Errorf("unable to initialize microfat cache directories (tried %s, %s)", primaryDir, fallbackDir)
}

