// Package format defines the microfat binary trailer, index structure, integrity verification,
// and serialization logic.
package format

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

	// SizeLen is length in bytes of uint64 size fields in trailer.
	SizeLen = 8

	// HashLen is the byte length of SHA-256 checksums.
	HashLen = 32

	// MagicLen is the length of magic string in bytes.
	MagicLen = 8

	// IndexMagicV2 is the 4-byte magic signature at the start of Format v2 binary indices.
	IndexMagicV2 = "\x00\xFAM2"

	// FormatVersion1 is the legacy JSON manifest format.
	FormatVersion1 = 1

	// FormatVersion2 is the reflection-free compact binary index table format.
	FormatVersion2 = 2

	// FormatVersionCurrent is the default format version used for new fat binaries.
	FormatVersionCurrent = FormatVersion2

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
	EnvCgroupVersion              = "MICROFAT_CGROUP_VERSION"
	EnvCgroupGOMEMLIMIT           = "MICROFAT_CGROUP_GOMEMLIMIT"
	EnvCgroupGOMAXPROCS           = "MICROFAT_CGROUP_GOMAXPROCS"
	EnvCgroupLimitBytes           = "MICROFAT_CGROUP_LIMIT_BYTES"
	EnvCgroupCPUs                 = "MICROFAT_CGROUP_CPUS"
	EnvCgroupGOGC                 = "MICROFAT_CGROUP_GOGC"
	EnvCgroupGCProfile            = "MICROFAT_CGROUP_GC_PROFILE"
	EnvDebug                      = "MICROFAT_DEBUG"
	EnvLog                        = "MICROFAT_LOG"
	EnvAutotune                   = "MICROFAT_AUTOTUNE"
	EnvMemRatio                   = "MICROFAT_MEM_RATIO"
	EnvGCProfile                  = "MICROFAT_GC_PROFILE"
	EnvLiveHeapEstimate           = "MICROFAT_LIVE_HEAP_ESTIMATE"
	EnvForceLevel                 = "MICROFAT_FORCE_LEVEL"
	EnvMaxLevel                   = "MICROFAT_MAX_LEVEL"
	EnvDisableVariants            = "MICROFAT_DISABLE_VARIANTS"
	EnvPolicy                     = "MICROFAT_POLICY"
	EnvAVX512DownclockProtection  = "MICROFAT_AVX512_DOWNCLOCK_PROTECTION"
	EnvCacheDir                   = "MICROFAT_CACHE_DIR"
	EnvVerifyCache                = "MICROFAT_VERIFY_CACHE"
	EnvPolicyApplied              = "MICROFAT_POLICY_APPLIED"
	EnvOverrideReason             = "MICROFAT_OVERRIDE_REASON"

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

// Standard error definitions for binary format parsing and execution.
var (
	ErrBinaryTooSmall      = errors.New("binary size is smaller than trailer size")
	ErrInvalidMagic        = errors.New("invalid microfat magic bytes at EOF")
	ErrUnsupportedVersion  = errors.New("unsupported microfat format version")
	ErrInvalidIndexOffset  = errors.New("invalid index offset in trailer")
	ErrInvalidIndexSize    = errors.New("invalid index size in trailer")
	ErrIndexCorrupted      = errors.New("index SHA-256 checksum mismatch")
	ErrOutOfBounds         = errors.New("variant payload extends beyond binary boundary")
	ErrPayloadTooLarge     = errors.New("variant payload size exceeds safety limit")
	ErrOverlappingVariant  = errors.New("variant payloads overlap or are unsorted")
	ErrTruncatedIndex      = errors.New("truncated index data")
	ErrInvalidJSONSyntax   = errors.New("invalid json syntax in manifest index")
	ErrDictionaryCorrupted = errors.New("shared dictionary SHA-256 checksum mismatch")
	ErrPayloadCorrupted    = errors.New("variant payload SHA-256 checksum mismatch")
	ErrInvalidDictionary   = errors.New("invalid shared dictionary offset or size")
	ErrInvalidChecksum     = errors.New("invalid sha256 checksum format")

	// Launcher execution stage sentinels for typed error diagnostics.
	ErrMemfdCreate  = errors.New("memfd_create failed")
	ErrExecve       = errors.New("execve failed")
	ErrCacheInit    = errors.New("cache directory initialization failed")
	ErrCacheWrite   = errors.New("cache file creation failed")
	ErrCacheExtract = errors.New("cache decompression failed")
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
	Version          int            `json:"version"`
	AppName          string         `json:"app_name,omitempty"`
	TargetOS         string         `json:"os"`
	TargetArch       string         `json:"arch"`
	CreatedUnix      int64          `json:"created_unix"`
	DictionaryOffset int64          `json:"dictionary_offset,omitempty"`
	DictionarySize   int64          `json:"dictionary_size,omitempty"`
	DictionarySHA256 string         `json:"dictionary_sha256,omitempty"`
	DictionaryID     uint32         `json:"dictionary_id,omitempty"`
	Variants         []VariantEntry `json:"variants"`
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
	GOGC                    string  `json:"gogc,omitempty"`
	GCProfile               string  `json:"gc_profile,omitempty"`
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
	AppName          string         `json:"app_name"`
	TargetOS         string         `json:"target_os"`
	TargetArch       string         `json:"target_arch"`
	FatBinarySize    int64          `json:"fat_binary_size"`
	HostOS           string         `json:"host_os"`
	HostArch         string         `json:"host_arch"`
	HostLevel        string         `json:"host_level"`
	SelectedVariant  string         `json:"selected_variant"`
	SelectedSize     int64          `json:"selected_size"`
	ExecMode         string         `json:"exec_mode"`
	PolicyApplied    string         `json:"policy_applied,omitempty"`
	PolicyReason     string         `json:"policy_reason,omitempty"`
	DictionaryOffset int64          `json:"dictionary_offset,omitempty"`
	DictionarySize   int64          `json:"dictionary_size,omitempty"`
	DictionarySHA256 string         `json:"dictionary_sha256,omitempty"`
	DictionaryID     uint32         `json:"dictionary_id,omitempty"`
	Cgroup           *CgroupInfo    `json:"cgroup,omitempty"`
	Variants         []VariantEntry `json:"variants"`
	HostFeatures     []string       `json:"host_features"`
}

// CgroupInfo represents container cgroup telemetry and auto-tuning limits.
type CgroupInfo struct {
	Version          int     `json:"version"`
	MemoryLimitBytes int64   `json:"memory_limit_bytes"`
	CPUQuota         float64 `json:"cpu_quota"`
	GOMEMLIMIT       string  `json:"gomemlimit,omitempty"`
	GOMAXPROCS       int     `json:"gomaxprocs,omitempty"`
	GOGC             string  `json:"gogc,omitempty"`
	GCProfile        string  `json:"gc_profile,omitempty"`
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
	if idx.Version != FormatVersion1 && idx.Version != FormatVersion2 {
		return fmt.Errorf("%w: got version %d, expected %d or %d", ErrUnsupportedVersion, idx.Version, FormatVersion1, FormatVersion2)
	}

	var lastEnd int64
	if idx.DictionarySize > 0 {
		if idx.DictionaryOffset < 0 {
			return fmt.Errorf("%w: invalid dictionary offset %d", ErrInvalidDictionary, idx.DictionaryOffset)
		}
		if idx.DictionarySHA256 != "" && !ValidateChecksum(idx.DictionarySHA256) {
			return fmt.Errorf("%w: invalid dictionary sha256 checksum format %q", ErrInvalidChecksum, idx.DictionarySHA256)
		}
		if idx.DictionaryOffset+idx.DictionarySize > indexOffset {
			return fmt.Errorf("%w: dictionary payload extends past index offset %d", ErrOutOfBounds, indexOffset)
		}
		lastEnd = idx.DictionaryOffset + idx.DictionarySize
	}

	for i, v := range idx.Variants {
		if v.SHA256 != "" && !ValidateChecksum(v.SHA256) {
			return fmt.Errorf("%w: invalid sha256 checksum format for variant %s: %q", ErrInvalidChecksum, v.Level, v.SHA256)
		}
		if v.Offset < 0 || v.CompressedSize <= 0 || v.UncompressedSize <= 0 {
			return fmt.Errorf("%w: invalid dimensions for variant %s", ErrOutOfBounds, v.Level)
		}
		if v.UncompressedSize > MaxPayloadSize {
			return fmt.Errorf("%w: variant %s exceeds 1GB limit (%d bytes)", ErrPayloadTooLarge, v.Level, v.UncompressedSize)
		}
		if v.Offset+v.CompressedSize > indexOffset {
			return fmt.Errorf("%w: variant %s payload extends past index offset %d", ErrOutOfBounds, v.Level, indexOffset)
		}
		if (i > 0 || idx.DictionarySize > 0) && v.Offset < lastEnd {
			return fmt.Errorf("%w: variant %s offset %d overlaps with previous data ending at %d",
				ErrOverlappingVariant, v.Level, v.Offset, lastEnd)
		}
		lastEnd = v.Offset + v.CompressedSize
	}
	return nil
}

const (
	maxSHA256HexLen             = 64
	defaultCompressionAlgorithm = "zstd"
	minBinaryHeaderSize         = 34
	binaryHeaderFixedSize       = 34
	initialVariantCap           = 4
	decimalBase                 = 10
	asciiControlCutoff          = 0x20
	uint16LenPrefixSize         = 2
	uint64FieldSize             = 8
)

// ValidateChecksum verifies that a SHA-256 hex string contains only valid hex characters (up to 64 chars).
func ValidateChecksum(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > maxSHA256HexLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// MarshalBinaryIndex serializes an Index struct into a compact Format v2 binary representation.
func MarshalBinaryIndex(idx *Index) ([]byte, error) {
	if len(idx.TargetOS) > 255 || len(idx.TargetArch) > 255 || len(idx.AppName) > 65535 ||
		len(idx.Variants) > 65535 || len(idx.DictionarySHA256) > 255 {
		return nil, errors.New("index metadata field exceeds binary format limits")
	}

	totalSize := binaryHeaderFixedSize + 1 + len(idx.DictionarySHA256) +
		1 + len(idx.TargetOS) + 1 + len(idx.TargetArch) + uint16LenPrefixSize + len(idx.AppName) + uint16LenPrefixSize
	for _, v := range idx.Variants {
		comp := v.Compression
		if comp == "" {
			comp = defaultCompressionAlgorithm
		}
		if len(v.Level) > 255 || len(v.SHA256) > 255 || len(comp) > 255 {
			return nil, fmt.Errorf("variant %s field length exceeds binary format limits", v.Level)
		}
		totalSize += 1 + len(v.Level) + uint64FieldSize + uint64FieldSize + uint64FieldSize + 1 + len(v.SHA256) + 1 + len(comp)
	}

	buf := make([]byte, totalSize)
	copy(buf[0:4], []byte(IndexMagicV2))
	binary.LittleEndian.PutUint16(buf[4:6], uint16(FormatVersion2))
	// #nosec G115 -- timestamps and counts fit in respective integer types
	binary.LittleEndian.PutUint64(buf[6:14], uint64(idx.CreatedUnix))
	// #nosec G115 -- offset fits uint64
	binary.LittleEndian.PutUint64(buf[14:22], uint64(idx.DictionaryOffset))
	// #nosec G115 -- size fits uint64
	binary.LittleEndian.PutUint64(buf[22:30], uint64(idx.DictionarySize))
	binary.LittleEndian.PutUint32(buf[30:34], idx.DictionaryID)

	offset := 34

	// #nosec G115 -- length checked <= 255 above
	buf[offset] = byte(len(idx.DictionarySHA256))
	offset++
	copy(buf[offset:], idx.DictionarySHA256)
	offset += len(idx.DictionarySHA256)

	// #nosec G115 -- length checked <= 255 above
	buf[offset] = byte(len(idx.TargetOS))
	offset++
	copy(buf[offset:], idx.TargetOS)
	offset += len(idx.TargetOS)

	// #nosec G115 -- length checked <= 255 above
	buf[offset] = byte(len(idx.TargetArch))
	offset++
	copy(buf[offset:], idx.TargetArch)
	offset += len(idx.TargetArch)

	// #nosec G115 -- length checked <= 65535 above
	binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(idx.AppName)))
	offset += 2
	copy(buf[offset:], idx.AppName)
	offset += len(idx.AppName)

	// #nosec G115 -- length checked <= 65535 above
	binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(idx.Variants)))
	offset += 2

	for _, v := range idx.Variants {
		// #nosec G115 -- length checked <= 255 above
		buf[offset] = byte(len(v.Level))
		offset++
		copy(buf[offset:], v.Level)
		offset += len(v.Level)

		// #nosec G115 -- offset fits uint64
		binary.LittleEndian.PutUint64(buf[offset:offset+8], uint64(v.Offset))
		offset += 8

		// #nosec G115 -- size fits uint64
		binary.LittleEndian.PutUint64(buf[offset:offset+8], uint64(v.CompressedSize))
		offset += 8

		// #nosec G115 -- size fits uint64
		binary.LittleEndian.PutUint64(buf[offset:offset+8], uint64(v.UncompressedSize))
		offset += 8

		// #nosec G115 -- length checked <= 255 above
		buf[offset] = byte(len(v.SHA256))
		offset++
		copy(buf[offset:], v.SHA256)
		offset += len(v.SHA256)

		comp := v.Compression
		if comp == "" {
			comp = defaultCompressionAlgorithm
		}
		// #nosec G115 -- length checked <= 255 above
		buf[offset] = byte(len(comp))
		offset++
		copy(buf[offset:], comp)
		offset += len(comp)
	}

	return buf, nil
}

// UnmarshalBinaryIndex deserializes a Format v2 compact binary index buffer.
func UnmarshalBinaryIndex(data []byte) (*Index, error) {
	if len(data) < minBinaryHeaderSize {
		return nil, fmt.Errorf("%w: binary index too short (%d bytes)", ErrTruncatedIndex, len(data))
	}

	if string(data[0:4]) != IndexMagicV2 {
		return nil, fmt.Errorf("%w: expected binary index magic %q, got %q", ErrInvalidMagic, IndexMagicV2, string(data[0:4]))
	}

	version := int(binary.LittleEndian.Uint16(data[4:6]))
	if version != FormatVersion2 {
		return nil, fmt.Errorf("%w: expected binary index version %d, got %d", ErrUnsupportedVersion, FormatVersion2, version)
	}

	// #nosec G115 -- binary format integer decode
	createdUnix := int64(binary.LittleEndian.Uint64(data[6:14]))
	// #nosec G115 -- binary format integer decode
	dictOffset := int64(binary.LittleEndian.Uint64(data[14:22]))
	// #nosec G115 -- binary format integer decode
	dictSize := int64(binary.LittleEndian.Uint64(data[22:30]))
	dictID := binary.LittleEndian.Uint32(data[30:34])

	offset := 34

	if offset >= len(data) {
		return nil, fmt.Errorf("%w: truncated dictionary sha length", ErrTruncatedIndex)
	}
	dictSHALen := int(data[offset])
	offset++
	if offset+dictSHALen > len(data) {
		return nil, fmt.Errorf("%w: truncated dictionary sha string", ErrTruncatedIndex)
	}
	dictSHA := string(data[offset : offset+dictSHALen])
	offset += dictSHALen

	if offset >= len(data) {
		return nil, fmt.Errorf("%w: truncated os length", ErrTruncatedIndex)
	}
	osLen := int(data[offset])
	offset++
	if offset+osLen > len(data) {
		return nil, fmt.Errorf("%w: truncated os string", ErrTruncatedIndex)
	}
	targetOS := string(data[offset : offset+osLen])
	offset += osLen

	if offset >= len(data) {
		return nil, fmt.Errorf("%w: truncated arch length", ErrTruncatedIndex)
	}
	archLen := int(data[offset])
	offset++
	if offset+archLen > len(data) {
		return nil, fmt.Errorf("%w: truncated arch string", ErrTruncatedIndex)
	}
	targetArch := string(data[offset : offset+archLen])
	offset += archLen

	if offset+2 > len(data) {
		return nil, fmt.Errorf("%w: truncated app name length", ErrTruncatedIndex)
	}
	appLen := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
	offset += 2
	if offset+appLen > len(data) {
		return nil, fmt.Errorf("%w: truncated app name string", ErrTruncatedIndex)
	}
	appName := string(data[offset : offset+appLen])
	offset += appLen

	if offset+2 > len(data) {
		return nil, fmt.Errorf("%w: truncated variant count", ErrTruncatedIndex)
	}
	variantCount := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
	offset += 2

	variants := make([]VariantEntry, 0, variantCount)
	for i := 0; i < variantCount; i++ {
		if offset >= len(data) {
			return nil, fmt.Errorf("%w: truncated variant %d header", ErrTruncatedIndex, i)
		}
		levelLen := int(data[offset])
		offset++
		if offset+levelLen > len(data) {
			return nil, fmt.Errorf("%w: truncated variant %d level string", ErrTruncatedIndex, i)
		}
		level := string(data[offset : offset+levelLen])
		offset += levelLen

		if offset+24 > len(data) {
			return nil, fmt.Errorf("%w: truncated variant %d numeric fields", ErrTruncatedIndex, i)
		}
		// #nosec G115 -- binary format integer decode
		vOffset := int64(binary.LittleEndian.Uint64(data[offset : offset+8]))
		offset += 8
		// #nosec G115 -- binary format integer decode
		compSize := int64(binary.LittleEndian.Uint64(data[offset : offset+8]))
		offset += 8
		// #nosec G115 -- binary format integer decode
		uncompSize := int64(binary.LittleEndian.Uint64(data[offset : offset+8]))
		offset += 8

		if offset >= len(data) {
			return nil, fmt.Errorf("%w: truncated variant %d sha length", ErrTruncatedIndex, i)
		}
		shaLen := int(data[offset])
		offset++
		if offset+shaLen > len(data) {
			return nil, fmt.Errorf("%w: truncated variant %d sha string", ErrTruncatedIndex, i)
		}
		shaStr := string(data[offset : offset+shaLen])
		offset += shaLen

		if offset >= len(data) {
			return nil, fmt.Errorf("%w: truncated variant %d compression length", ErrTruncatedIndex, i)
		}
		compLen := int(data[offset])
		offset++
		if offset+compLen > len(data) {
			return nil, fmt.Errorf("%w: truncated variant %d compression string", ErrTruncatedIndex, i)
		}
		compStr := string(data[offset : offset+compLen])
		offset += compLen

		if compStr == "" {
			compStr = defaultCompressionAlgorithm
		}

		variants = append(variants, VariantEntry{
			Level:            level,
			Offset:           vOffset,
			CompressedSize:   compSize,
			UncompressedSize: uncompSize,
			SHA256:           shaStr,
			Compression:      compStr,
		})
	}

	return &Index{
		Version:          version,
		AppName:          appName,
		TargetOS:         targetOS,
		TargetArch:       targetArch,
		CreatedUnix:      createdUnix,
		DictionaryOffset: dictOffset,
		DictionarySize:   dictSize,
		DictionarySHA256: dictSHA,
		DictionaryID:     dictID,
		Variants:         variants,
	}, nil
}

func skipJSONWhitespace(data []byte, pos int) int {
	for pos < len(data) {
		c := data[pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			pos++
			continue
		}
		break
	}
	return pos
}

func parseJSONString(data []byte, pos int) (string, int, error) {
	pos = skipJSONWhitespace(data, pos)
	if pos >= len(data) || data[pos] != '"' {
		return "", pos, fmt.Errorf("%w: expected string opening quote at byte %d", ErrInvalidJSONSyntax, pos)
	}
	pos++
	start := pos
	var sb strings.Builder
	hasEscapes := false

	for pos < len(data) {
		c := data[pos]
		if c == '"' {
			if !hasEscapes {
				return string(data[start:pos]), pos + 1, nil
			}
			return sb.String(), pos + 1, nil
		}
		if c == '\\' {
			if !hasEscapes {
				sb.Write(data[start:pos])
				hasEscapes = true
			}
			pos++
			if pos >= len(data) {
				return "", pos, fmt.Errorf("%w: unterminated escape sequence", ErrInvalidJSONSyntax)
			}
			switch data[pos] {
			case '"', '\\', '/':
				sb.WriteByte(data[pos])
			case 'b':
				sb.WriteByte('\b')
			case 'f':
				sb.WriteByte('\f')
			case 'n':
				sb.WriteByte('\n')
			case 'r':
				sb.WriteByte('\r')
			case 't':
				sb.WriteByte('\t')
			case 'u':
				if pos+4 >= len(data) {
					return "", pos, fmt.Errorf("%w: invalid unicode escape", ErrInvalidJSONSyntax)
				}
				r, err := strconv.ParseUint(string(data[pos+1:pos+5]), 16, 16)
				if err != nil {
					return "", pos, fmt.Errorf("%w: invalid unicode escape: %v", ErrInvalidJSONSyntax, err)
				}
				sb.WriteRune(rune(r))
				pos += 5
				continue
			default:
				sb.WriteByte(data[pos])
			}
			pos++
			continue
		}
		if hasEscapes {
			sb.WriteByte(c)
		}
		pos++
	}
	return "", pos, fmt.Errorf("%w: unterminated string starting at byte %d", ErrInvalidJSONSyntax, start)
}

func parseJSONInt64(data []byte, pos int) (int64, int, error) {
	pos = skipJSONWhitespace(data, pos)
	if pos >= len(data) {
		return 0, pos, fmt.Errorf("%w: expected number at end of input", ErrInvalidJSONSyntax)
	}
	start := pos
	isNeg := false
	if data[pos] == '-' {
		isNeg = true
		pos++
	}
	if pos >= len(data) || data[pos] < '0' || data[pos] > '9' {
		return 0, pos, fmt.Errorf("%w: expected digit at byte %d", ErrInvalidJSONSyntax, pos)
	}
	var val int64
	for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
		digit := int64(data[pos] - '0')
		val = val*decimalBase + digit
		pos++
	}
	if isNeg {
		val = -val
	}
	if start == pos {
		return 0, pos, fmt.Errorf("%w: no digits parsed at byte %d", ErrInvalidJSONSyntax, start)
	}
	return val, pos, nil
}

func skipJSONValue(data []byte, pos int) (int, error) {
	pos = skipJSONWhitespace(data, pos)
	if pos >= len(data) {
		return pos, fmt.Errorf("%w: unexpected end of json", ErrInvalidJSONSyntax)
	}
	switch data[pos] {
	case '"':
		_, nextPos, err := parseJSONString(data, pos)
		return nextPos, err
	case '{':
		return skipJSONObject(data, pos)
	case '[':
		return skipJSONArray(data, pos)
	default:
		return skipJSONPrimitive(data, pos)
	}
}

func skipJSONObject(data []byte, pos int) (int, error) {
	pos++
	for {
		pos = skipJSONWhitespace(data, pos)
		if pos >= len(data) {
			return pos, fmt.Errorf("%w: unclosed object", ErrInvalidJSONSyntax)
		}
		if data[pos] == '}' {
			return pos + 1, nil
		}
		_, nextPos, err := parseJSONString(data, pos)
		if err != nil {
			return pos, err
		}
		pos = skipJSONWhitespace(data, nextPos)
		if pos >= len(data) || pos < len(data) && data[pos] != ':' {
			return pos, fmt.Errorf("%w: expected ':' in object", ErrInvalidJSONSyntax)
		}
		pos++
		valPos, err := skipJSONValue(data, pos)
		if err != nil {
			return valPos, err
		}
		pos = skipJSONWhitespace(data, valPos)
		if pos < len(data) && data[pos] == ',' {
			pos++
			continue
		}
		if pos < len(data) && data[pos] == '}' {
			return pos + 1, nil
		}
	}
}

func skipJSONArray(data []byte, pos int) (int, error) {
	pos++
	for {
		pos = skipJSONWhitespace(data, pos)
		if pos >= len(data) {
			return pos, fmt.Errorf("%w: unclosed array", ErrInvalidJSONSyntax)
		}
		if data[pos] == ']' {
			return pos + 1, nil
		}
		elemPos, err := skipJSONValue(data, pos)
		if err != nil {
			return elemPos, err
		}
		pos = skipJSONWhitespace(data, elemPos)
		if pos < len(data) && data[pos] == ',' {
			pos++
			continue
		}
		if pos < len(data) && data[pos] == ']' {
			return pos + 1, nil
		}
	}
}

func skipJSONPrimitive(data []byte, pos int) (int, error) {
	for pos < len(data) {
		c := data[pos]
		if c == ',' || c == '}' || c == ']' || c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			return pos, nil
		}
		pos++
	}
	return pos, nil
}

// unmarshalJSONIndex parses a Format v1 JSON index manifest without using Go reflection.
func unmarshalJSONIndex(data []byte) (*Index, error) {
	pos := skipJSONWhitespace(data, 0)
	if pos >= len(data) || data[pos] != '{' {
		return nil, fmt.Errorf("%w: root must be object", ErrInvalidJSONSyntax)
	}
	pos++

	idx := &Index{
		Version:  FormatVersion1,
		Variants: make([]VariantEntry, 0, initialVariantCap),
	}

	for {
		pos = skipJSONWhitespace(data, pos)
		if pos >= len(data) {
			return nil, fmt.Errorf("%w: unclosed root object", ErrInvalidJSONSyntax)
		}
		if data[pos] == '}' {
			break
		}

		key, nextPos, err := parseJSONString(data, pos)
		if err != nil {
			return nil, err
		}
		pos = skipJSONWhitespace(data, nextPos)
		if pos >= len(data) || data[pos] != ':' {
			return nil, fmt.Errorf("%w: expected ':' after key %s", ErrInvalidJSONSyntax, key)
		}
		pos++
		pos = skipJSONWhitespace(data, pos)

		nPos, fErr := parseJSONIndexField(key, data, pos, idx)
		if fErr != nil {
			return nil, fErr
		}
		pos = nPos

		pos = skipJSONWhitespace(data, pos)
		if pos < len(data) && data[pos] == ',' {
			pos++
		}
	}

	return idx, nil
}

func parseJSONIndexField(key string, data []byte, pos int, idx *Index) (int, error) {
	switch key {
	case "version":
		v, nPos, err := parseJSONInt64(data, pos)
		if err != nil {
			return pos, err
		}
		idx.Version = int(v)
		return nPos, nil
	case "app_name":
		s, nPos, err := parseJSONString(data, pos)
		if err != nil {
			return pos, err
		}
		idx.AppName = s
		return nPos, nil
	case "os":
		s, nPos, err := parseJSONString(data, pos)
		if err != nil {
			return pos, err
		}
		idx.TargetOS = s
		return nPos, nil
	case "arch":
		s, nPos, err := parseJSONString(data, pos)
		if err != nil {
			return pos, err
		}
		idx.TargetArch = s
		return nPos, nil
	case "created_unix":
		v, nPos, err := parseJSONInt64(data, pos)
		if err != nil {
			return pos, err
		}
		idx.CreatedUnix = v
		return nPos, nil
	case "dictionary_offset":
		v, nPos, err := parseJSONInt64(data, pos)
		if err != nil {
			return pos, err
		}
		idx.DictionaryOffset = v
		return nPos, nil
	case "dictionary_size":
		v, nPos, err := parseJSONInt64(data, pos)
		if err != nil {
			return pos, err
		}
		idx.DictionarySize = v
		return nPos, nil
	case "dictionary_sha256":
		s, nPos, err := parseJSONString(data, pos)
		if err != nil {
			return pos, err
		}
		idx.DictionarySHA256 = s
		return nPos, nil
	case "dictionary_id":
		v, nPos, err := parseJSONInt64(data, pos)
		if err != nil {
			return pos, err
		}
		// #nosec G115 -- integer conversion for dictionary_id
		idx.DictionaryID = uint32(v)
		return nPos, nil
	case "variants":
		variants, nPos, err := parseJSONVariantArray(data, pos)
		if err != nil {
			return pos, err
		}
		idx.Variants = variants
		return nPos, nil
	default:
		return skipJSONValue(data, pos)
	}
}

func parseJSONVariantArray(data []byte, pos int) ([]VariantEntry, int, error) {
	if pos >= len(data) || data[pos] != '[' {
		return nil, pos, fmt.Errorf("%w: expected '[' for variants array", ErrInvalidJSONSyntax)
	}
	pos++
	variants := make([]VariantEntry, 0, initialVariantCap)
	for {
		pos = skipJSONWhitespace(data, pos)
		if pos >= len(data) {
			return nil, pos, fmt.Errorf("%w: unclosed variants array", ErrInvalidJSONSyntax)
		}
		if data[pos] == ']' {
			return variants, pos + 1, nil
		}
		if data[pos] != '{' {
			return nil, pos, fmt.Errorf("%w: expected variant object '{'", ErrInvalidJSONSyntax)
		}
		entry, nPos, err := parseJSONVariantObject(data, pos)
		if err != nil {
			return nil, pos, err
		}
		variants = append(variants, entry)
		pos = skipJSONWhitespace(data, nPos)
		if pos < len(data) && data[pos] == ',' {
			pos++
		}
	}
}

func parseJSONVariantObject(data []byte, pos int) (VariantEntry, int, error) {
	pos++
	var entry VariantEntry
	for {
		pos = skipJSONWhitespace(data, pos)
		if pos >= len(data) {
			return entry, pos, fmt.Errorf("%w: unclosed variant object", ErrInvalidJSONSyntax)
		}
		if data[pos] == '}' {
			if entry.Compression == "" {
				entry.Compression = defaultCompressionAlgorithm
			}
			return entry, pos + 1, nil
		}
		vKey, vnPos, err := parseJSONString(data, pos)
		if err != nil {
			return entry, pos, err
		}
		pos = skipJSONWhitespace(data, vnPos)
		if pos >= len(data) || data[pos] != ':' {
			return entry, pos, fmt.Errorf("%w: expected ':' after variant key %s", ErrInvalidJSONSyntax, vKey)
		}
		pos++
		pos = skipJSONWhitespace(data, pos)

		nPos, fErr := parseJSONVariantField(vKey, data, pos, &entry)
		if fErr != nil {
			return entry, pos, fErr
		}
		pos = nPos

		pos = skipJSONWhitespace(data, pos)
		if pos < len(data) && data[pos] == ',' {
			pos++
		}
	}
}

func parseJSONVariantField(vKey string, data []byte, pos int, entry *VariantEntry) (int, error) {
	switch vKey {
	case "level":
		s, snPos, err := parseJSONString(data, pos)
		if err != nil {
			return pos, err
		}
		entry.Level = s
		return snPos, nil
	case "offset":
		v, vnPos, err := parseJSONInt64(data, pos)
		if err != nil {
			return pos, err
		}
		entry.Offset = v
		return vnPos, nil
	case "compressed_size":
		v, vnPos, err := parseJSONInt64(data, pos)
		if err != nil {
			return pos, err
		}
		entry.CompressedSize = v
		return vnPos, nil
	case "uncompressed_size":
		v, vnPos, err := parseJSONInt64(data, pos)
		if err != nil {
			return pos, err
		}
		entry.UncompressedSize = v
		return vnPos, nil
	case "sha256":
		s, snPos, err := parseJSONString(data, pos)
		if err != nil {
			return pos, err
		}
		entry.SHA256 = s
		return snPos, nil
	case "compression":
		s, snPos, err := parseJSONString(data, pos)
		if err != nil {
			return pos, err
		}
		entry.Compression = s
		return snPos, nil
	default:
		return skipJSONValue(data, pos)
	}
}

// ReadTrailerAndIndex reads the trailing 56 bytes of the binary, verifies magic and index SHA-256 hash,
// deserializes the index table (binary Format v2 or JSON Format v1), and enforces bounds checks.
func ReadTrailerAndIndex(r io.ReaderAt, totalSize int64) (*Index, error) {
	if totalSize < TrailerSize {
		return nil, fmt.Errorf("%w: file size %d bytes is smaller than trailer size %d bytes",
			ErrBinaryTooSmall, totalSize, TrailerSize)
	}

	trailerBuf := make([]byte, TrailerSize)
	if _, err := r.ReadAt(trailerBuf, totalSize-TrailerSize); err != nil {
		return nil, fmt.Errorf("reading trailer from file: %w", err)
	}

	offset := OffsetLen + SizeLen
	hashOffset := offset + HashLen
	if string(trailerBuf[hashOffset:hashOffset+MagicLen]) != MagicString {
		return nil, ErrInvalidMagic
	}

	// #nosec G115 -- trailer format offset and size
	indexOffset := int64(binary.LittleEndian.Uint64(trailerBuf[0:OffsetLen]))
	// #nosec G115 -- trailer format offset and size
	indexSize := int64(binary.LittleEndian.Uint64(trailerBuf[OffsetLen:offset]))

	trailerOffset := totalSize - TrailerSize
	if indexOffset < 0 || indexOffset >= trailerOffset {
		return nil, fmt.Errorf("%w: offset %d beyond trailer %d",
			ErrInvalidIndexOffset, indexOffset, trailerOffset)
	}
	if indexSize <= 0 || indexSize > MaxIndexSize || indexOffset+indexSize != trailerOffset {
		return nil, fmt.Errorf("%w: index size %d with offset %d does not match trailer boundary %d",
			ErrInvalidIndexSize, indexSize, indexOffset, trailerOffset)
	}

	expectedHash := trailerBuf[offset : offset+HashLen]
	idxBytes := make([]byte, indexSize)
	if _, err := r.ReadAt(idxBytes, indexOffset); err != nil {
		return nil, fmt.Errorf("reading index payload at offset %d: %w", indexOffset, err)
	}

	actualHash := sha256.Sum256(idxBytes)
	if !bytes.Equal(actualHash[:], expectedHash) {
		return nil, fmt.Errorf("%w: expected %x, got %x", ErrIndexCorrupted, expectedHash, actualHash)
	}

	var idx *Index
	var err error
	if len(idxBytes) >= 4 && string(idxBytes[0:4]) == IndexMagicV2 {
		idx, err = UnmarshalBinaryIndex(idxBytes)
	} else {
		idx, err = unmarshalJSONIndex(idxBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("deserializing index: %w", err)
	}

	for i := range idx.Variants {
		if idx.Variants[i].Compression == "" {
			idx.Variants[i].Compression = defaultCompressionAlgorithm
		}
	}

	if err := idx.ValidateBounds(totalSize); err != nil {
		return nil, fmt.Errorf("validating variant boundaries: %w", err)
	}

	return idx, nil
}

// WriteIndexAndTrailer serializes the index in Format v2 (binary) and writes it followed by the 56-byte trailer.
func WriteIndexAndTrailer(w io.Writer, idx *Index, currentOffset int64) (int64, error) {
	version := idx.Version
	if version == 0 {
		version = FormatVersionCurrent
	}
	return WriteIndexAndTrailerWithVersion(w, idx, currentOffset, version)
}

// marshalJSONIndex serializes an Index struct to JSON without using Go reflection.
func marshalJSONIndex(idx *Index) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString(`{"version":`)
	sb.WriteString(strconv.Itoa(idx.Version))
	if idx.AppName != "" {
		sb.WriteString(`,"app_name":"`)
		sb.WriteString(escapeJSONString(idx.AppName))
		sb.WriteString(`"`)
	}
	sb.WriteString(`,"os":"`)
	sb.WriteString(escapeJSONString(idx.TargetOS))
	sb.WriteString(`","arch":"`)
	sb.WriteString(escapeJSONString(idx.TargetArch))
	sb.WriteString(`","created_unix":`)
	sb.WriteString(strconv.FormatInt(idx.CreatedUnix, 10))
	if idx.DictionarySize > 0 {
		sb.WriteString(`,"dictionary_offset":`)
		sb.WriteString(strconv.FormatInt(idx.DictionaryOffset, 10))
		sb.WriteString(`,"dictionary_size":`)
		sb.WriteString(strconv.FormatInt(idx.DictionarySize, 10))
		if idx.DictionarySHA256 != "" {
			sb.WriteString(`,"dictionary_sha256":"`)
			sb.WriteString(escapeJSONString(idx.DictionarySHA256))
			sb.WriteString(`"`)
		}
		if idx.DictionaryID > 0 {
			sb.WriteString(`,"dictionary_id":`)
			sb.WriteString(strconv.FormatInt(int64(idx.DictionaryID), 10))
		}
	}
	sb.WriteString(`,"variants":[`)
	for i, v := range idx.Variants {
		if i > 0 {
			sb.WriteString(`,`)
		}
		sb.WriteString(`{"level":"`)
		sb.WriteString(escapeJSONString(v.Level))
		sb.WriteString(`","offset":`)
		sb.WriteString(strconv.FormatInt(v.Offset, 10))
		sb.WriteString(`,"compressed_size":`)
		sb.WriteString(strconv.FormatInt(v.CompressedSize, 10))
		sb.WriteString(`,"uncompressed_size":`)
		sb.WriteString(strconv.FormatInt(v.UncompressedSize, 10))
		if v.SHA256 != "" {
			sb.WriteString(`,"sha256":"`)
			sb.WriteString(escapeJSONString(v.SHA256))
			sb.WriteString(`"`)
		}
		comp := v.Compression
		if comp == "" {
			comp = defaultCompressionAlgorithm
		}
		sb.WriteString(`,"compression":"`)
		sb.WriteString(escapeJSONString(comp))
		sb.WriteString(`"}`)
	}
	sb.WriteString(`]}`)
	return []byte(sb.String()), nil
}

func escapeJSONString(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if c < asciiControlCutoff {
				fmt.Fprintf(&sb, `\u%04x`, c)
			} else {
				sb.WriteByte(c)
			}
		}
	}
	return sb.String()
}

// WriteIndexAndTrailerWithVersion writes the serialized index in the specified format version followed by the 56-byte trailer.
func WriteIndexAndTrailerWithVersion(w io.Writer, idx *Index, currentOffset int64, version int) (int64, error) {
	if currentOffset < 0 {
		return 0, fmt.Errorf("invalid negative offset %d", currentOffset)
	}

	var idxBytes []byte
	var err error

	idx.Version = version
	switch version {
	case FormatVersion1:
		idxBytes, err = marshalJSONIndex(idx)
		if err != nil {
			return 0, fmt.Errorf("marshaling index json: %w", err)
		}
	case FormatVersion2:
		idxBytes, err = MarshalBinaryIndex(idx)
		if err != nil {
			return 0, fmt.Errorf("marshaling binary index: %w", err)
		}
	default:
		return 0, fmt.Errorf("%w: %d", ErrUnsupportedVersion, version)
	}

	indexSize := int64(len(idxBytes))
	if indexSize > MaxIndexSize {
		return 0, fmt.Errorf("index size %d exceeds maximum %d", indexSize, MaxIndexSize)
	}

	n, err := w.Write(idxBytes)
	if err != nil {
		return 0, fmt.Errorf("writing index: %w", err)
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

