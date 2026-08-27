// Package codec defines the compression abstraction, registry, algorithms (zstd, lz4, none),
// and profile resolution for microfat binaries.
package codec

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Supported compression algorithms.
const (
	AlgorithmZstd = "zstd"
	AlgorithmLZ4  = "lz4"
	AlgorithmNone = "none"
)

// Supported compression profiles.
const (
	ProfileLatency  = "latency"
	ProfileBalanced = "balanced"
	ProfileSize     = "size"
)

// DefaultLatencyUncompressedThreshold is the maximum uncompressed payload size (512 KB)
// that will be automatically promoted to uncompressed (none) under the latency profile.
const (
	DefaultLatencyUncompressedThreshold = 512 * 1024
	specParts                           = 2
)

// Standard error definitions for codec operations.
var (
	ErrUnsupportedCodec        = errors.New("unsupported compression algorithm")
	ErrUnsupportedProfile      = errors.New("unsupported compression profile")
	ErrInvalidCompressionLevel = errors.New("invalid compression level")
	ErrDecompressionFailed     = errors.New("decompression failed")
	ErrSizeMismatch            = errors.New("decompressed payload size mismatch")
)

// Codec defines the interface for payload compression and decompression.
type Codec interface {
	// Name returns the unique identifier for the compression algorithm (e.g., "zstd", "lz4", "none").
	Name() string

	// Compress compresses src bytes and writes the compressed stream to w with the specified level.
	Compress(w io.Writer, src []byte, level string) error

	// Decompress reads the compressed stream from r, decompresses it into w, and verifies uncompressedSize.
	Decompress(w io.Writer, r io.Reader, uncompressedSize int64) error
}

// DictCodec extends Codec with dictionary-aware compression and decompression capabilities.
type DictCodec interface {
	Codec

	// CompressWithDict compresses src bytes using a shared dictionary and writes to w with the specified level.
	CompressWithDict(w io.Writer, src []byte, level string, dict []byte) error

	// DecompressWithDict decompresses stream from r using a shared dictionary into w and verifies uncompressedSize.
	DecompressWithDict(w io.Writer, r io.Reader, uncompressedSize int64, dict []byte) error
}

// DecompressWithOptionalDict decompresses stream from r into w. If dict is non-empty and c implements DictCodec,
// it uses DecompressWithDict; otherwise it calls Decompress.
func DecompressWithOptionalDict(c Codec, w io.Writer, r io.Reader, uncompressedSize int64, dict []byte) error {
	if len(dict) > 0 {
		if dc, ok := c.(DictCodec); ok {
			return dc.DecompressWithDict(w, r, uncompressedSize, dict)
		}
	}
	return c.Decompress(w, r, uncompressedSize)
}

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Codec)
)

func init() {
	Register(NewZstdCodec())
	Register(NewLZ4Codec())
	Register(NewNoneCodec())
}

// Register registers a Codec instance in the global registry.
func Register(c Codec) {
	if c == nil {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[strings.ToLower(strings.TrimSpace(c.Name()))] = c
}

// Get retrieves a registered Codec by name. If name is empty, it defaults to "zstd".
func Get(name string) (Codec, error) {
	cleanName := strings.ToLower(strings.TrimSpace(name))
	if cleanName == "" {
		cleanName = AlgorithmZstd
	}

	registryMu.RLock()
	defer registryMu.RUnlock()

	c, ok := registry[cleanName]
	if !ok {
		return nil, fmt.Errorf("%w: %q (supported: %s)", ErrUnsupportedCodec, name, strings.Join(List(), ", "))
	}
	return c, nil
}

// List returns a sorted list of registered codec names.
func List() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ParseCompressionSpec parses a compression string in "algo[:level]" format (e.g. "zstd:best" or "lz4").
func ParseCompressionSpec(spec string) (string, string) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", ""
	}

	parts := strings.SplitN(spec, ":", specParts)
	algo := strings.ToLower(strings.TrimSpace(parts[0]))
	var level string
	if len(parts) == specParts {
		level = strings.TrimSpace(parts[1])
	}
	return algo, level
}

// ResolveCompression resolves the target Codec, algorithm name, and level string based on profile,
// explicit algorithm/level flags, and payload size.
func ResolveCompression(profile, algorithm, level string, payloadSize int64) (Codec, string, error) {
	profile = strings.ToLower(strings.TrimSpace(profile))
	algo, specLevel := ParseCompressionSpec(algorithm)
	if specLevel != "" && level == "" {
		level = specLevel
	}
	level = strings.TrimSpace(level)

	// Validate profile if specified
	if profile != "" {
		switch profile {
		case ProfileLatency, ProfileBalanced, ProfileSize:
		default:
			return nil, "", fmt.Errorf("%w: %q (expected %s, %s, or %s)",
				ErrUnsupportedProfile, profile, ProfileLatency, ProfileBalanced, ProfileSize)
		}
	}

	// 1. Profile default resolution
	targetAlgo := algo
	targetLevel := level

	switch profile {
	case ProfileLatency:
		if targetAlgo == "" {
			if payloadSize > 0 && payloadSize < DefaultLatencyUncompressedThreshold {
				targetAlgo = AlgorithmNone
			} else {
				targetAlgo = AlgorithmLZ4
			}
		}
	case ProfileSize:
		if targetAlgo == "" {
			targetAlgo = AlgorithmZstd
		}
		if targetLevel == "" && targetAlgo == AlgorithmZstd {
			targetLevel = "best"
		}
	case ProfileBalanced:
		if targetAlgo == "" {
			targetAlgo = AlgorithmZstd
		}
		if targetLevel == "" && targetAlgo == AlgorithmZstd {
			targetLevel = "better"
		}
	default:
		// Default when no profile is given: balanced zstd
		if targetAlgo == "" {
			targetAlgo = AlgorithmZstd
		}
		if targetLevel == "" && targetAlgo == AlgorithmZstd {
			targetLevel = "better"
		}
	}

	c, err := Get(targetAlgo)
	if err != nil {
		return nil, "", err
	}

	return c, targetLevel, nil
}

// ParseLevelInt parses an integer string or returns defaultVal if empty or unparseable.
func ParseLevelInt(level string, defaultVal int) (int, error) {
	trimmed := strings.TrimSpace(level)
	if trimmed == "" {
		return defaultVal, nil
	}
	val, err := strconv.Atoi(trimmed)
	if err != nil {
		return defaultVal, fmt.Errorf("%w: %q", ErrInvalidCompressionLevel, level)
	}
	return val, nil
}
