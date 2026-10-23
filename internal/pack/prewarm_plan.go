package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/cache"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

var (
	ErrPrewarmPayloadLimit = errors.New("selected logical payload bytes exceed prewarm limit")
	ErrPrewarmAccounting   = errors.New("invalid prewarm payload accounting")
)

// PrewarmOptions controls logical selected-payload admission, independently of cache state.
// A nil MaxPayloadBytes preserves unlimited admission; a nonnil zero admits no payload bytes.
type PrewarmOptions struct {
	MaxPayloadBytes *uint64
}

// PrewarmPlan describes a read-only observation, not a reservation or permission to skip later validation.
// Byte totals deduplicate index-declared SHA-256 identities, without verifying embedded payload contents.
type PrewarmPlan struct {
	Entries                   []PrewarmPlanEntry `json:"entries"`
	UniquePayloadCount        int                `json:"unique_payload_count"`
	UniquePayloadBytes        uint64             `json:"unique_payload_bytes"`
	VerifiedCacheBytes        uint64             `json:"verified_cache_bytes"`
	NeedsMaterializationBytes uint64             `json:"needs_materialization_bytes"`
	BlockedPayloadBytes       uint64             `json:"blocked_payload_bytes"`
}

// PrewarmPlanEntry keeps cache validity separate from the availability of its embedded decoder.
// MaterializationPossible describes known decoder/cache prerequisites, not guaranteed filesystem capacity or success.
// A valid cache hit CanPrewarm even when its embedded payload decoder is unavailable.
type PrewarmPlanEntry struct {
	Level                   string                  `json:"level"`
	SHA256                  string                  `json:"sha256"`
	UncompressedSize        int64                   `json:"uncompressed_size"`
	CachePath               string                  `json:"cache_path"`
	CacheStatus             cache.ObservationStatus `json:"cache_status"`
	CacheError              string                  `json:"cache_error,omitzero"`
	DecoderAvailable        bool                    `json:"decoder_available"`
	DecoderError            string                  `json:"decoder_error,omitzero"`
	MaterializationPossible bool                    `json:"materialization_possible"`
	CanPrewarm              bool                    `json:"can_prewarm"`
	ReuseAfterLevel         string                  `json:"reuse_after_level,omitzero"`
}

// ValidatePrewarmAdmission checks the selected declarations and optional logical-byte cap without cache access.
// The index must have passed format validation, as provided by format.ReadTrailerAndIndex.
func ValidatePrewarmAdmission(idx *format.Index, targetLevels []string, opts PrewarmOptions) error {
	_, _, err := selectPrewarmVariants(idx, targetLevels, opts)
	return err
}

func selectPrewarmVariants(idx *format.Index, targetLevels []string, opts PrewarmOptions) ([]format.VariantEntry, uint64, error) {
	if idx == nil {
		return nil, 0, fmt.Errorf("%w: nil index", ErrPrewarmAccounting)
	}
	var targetSet map[string]struct{}
	if len(targetLevels) > 0 {
		targetSet = make(map[string]struct{}, len(targetLevels))
		for _, level := range targetLevels {
			entry, found := idx.FindVariant(level)
			if !found {
				return nil, 0, fmt.Errorf("variant level %q not found in binary manifest", level)
			}
			targetSet[entry.Level] = struct{}{}
		}
	}

	selected := make([]format.VariantEntry, 0, len(idx.Variants))
	identities := make(map[string]int64, len(idx.Variants))
	var total uint64
	for _, entry := range idx.Variants {
		if targetSet != nil {
			if _, ok := targetSet[entry.Level]; !ok {
				continue
			}
		}
		if entry.SHA256 == "" || !format.ValidateChecksum(entry.SHA256) {
			return nil, 0, fmt.Errorf("%w: invalid checksum for variant %s", format.ErrInvalidChecksum, entry.Level)
		}
		if entry.UncompressedSize <= 0 || entry.UncompressedSize > format.MaxPayloadSize {
			return nil, 0, fmt.Errorf("%w: invalid uncompressed size for variant %s", format.ErrPayloadTooLarge, entry.Level)
		}
		entry.SHA256 = strings.ToLower(entry.SHA256)
		previousSize, seen := identities[entry.SHA256]
		if seen && previousSize != entry.UncompressedSize {
			return nil, 0, fmt.Errorf("%w: SHA-256 %s has conflicting declared sizes %d and %d",
				ErrPrewarmAccounting, entry.SHA256, previousSize, entry.UncompressedSize)
		}
		if !seen {
			var err error
			total, err = addPrewarmBytes(total, uint64(entry.UncompressedSize))
			if err != nil {
				return nil, 0, err
			}
			identities[entry.SHA256] = entry.UncompressedSize
		}
		selected = append(selected, entry)
	}
	if opts.MaxPayloadBytes != nil && total > *opts.MaxPayloadBytes {
		return nil, 0, fmt.Errorf("%w: selected %d bytes, limit %d bytes", ErrPrewarmPayloadLimit, total, *opts.MaxPayloadBytes)
	}
	return selected, total, nil
}

func addPrewarmBytes(total, size uint64) (uint64, error) {
	if size > math.MaxUint64-total {
		return 0, fmt.Errorf("%w: logical payload byte total overflows uint64", ErrPrewarmAccounting)
	}
	return total + size, nil
}

func readPrewarmDictionary(r io.ReaderAt, idx *format.Index) ([]byte, error) {
	if idx.DictionarySize == 0 {
		return nil, nil
	}
	if idx.DictionarySize < 0 || idx.DictionarySize > format.MaxDictionarySize || idx.DictionaryOffset < 0 {
		return nil, fmt.Errorf("%w: dictionary size %d or offset %d out of bounds",
			format.ErrInvalidDictionary, idx.DictionarySize, idx.DictionaryOffset)
	}
	if idx.DictionarySHA256 == "" || !format.ValidateChecksum(idx.DictionarySHA256) {
		return nil, fmt.Errorf("%w: dictionary missing or invalid sha256 checksum", format.ErrInvalidChecksum)
	}
	dictBytes := make([]byte, idx.DictionarySize)
	if _, err := r.ReadAt(dictBytes, idx.DictionaryOffset); err != nil {
		return nil, fmt.Errorf("reading shared dictionary: %w", err)
	}
	hash := sha256.Sum256(dictBytes)
	actual := hex.EncodeToString(hash[:])
	if actual != strings.ToLower(idx.DictionarySHA256) {
		return nil, fmt.Errorf("%w: expected %s, got %s", format.ErrDictionaryCorrupted, idx.DictionarySHA256, actual)
	}
	return dictBytes, nil
}

// PlanPrewarmBinary validates the source index and shared dictionary, then observes selected cache entries read-only.
// It never decodes embedded payloads or materializes cache entries. All observations may become stale immediately.
func PlanPrewarmBinary(
	r io.ReaderAt,
	totalSize int64,
	targetLevels []string,
	cacheDir string,
	opts PrewarmOptions,
) (*format.Index, *PrewarmPlan, error) {
	idx, err := format.ReadTrailerAndIndex(r, totalSize)
	if err != nil {
		return nil, nil, fmt.Errorf("reading index: %w", err)
	}
	selected, total, err := selectPrewarmVariants(idx, targetLevels, opts)
	if err != nil {
		return nil, nil, err
	}
	if _, err := readPrewarmDictionary(r, idx); err != nil {
		return nil, nil, err
	}

	plan := &PrewarmPlan{Entries: make([]PrewarmPlanEntry, 0, len(selected)), UniquePayloadBytes: total}
	observations := make(map[string]cache.VariantObservation, len(selected))
	preparedLevels := make(map[string]string, len(selected))
	for _, entry := range selected {
		observation, seen := observations[entry.SHA256]
		if !seen {
			observation = cache.ObserveVariant(&entry, cacheDir)
			observations[entry.SHA256] = observation
			plan.UniquePayloadCount++
			// #nosec G115 -- selectPrewarmVariants requires a positive size bounded by format.MaxPayloadSize.
			size := uint64(entry.UncompressedSize)
			switch observation.Status {
			case cache.ObservationValid:
				plan.VerifiedCacheBytes += size
			case cache.ObservationMissing, cache.ObservationCorruptSafe:
				plan.NeedsMaterializationBytes += size
			default:
				plan.BlockedPayloadBytes += size
			}
		}
		result := describePrewarmEntry(entry, observation)
		if priorLevel := preparedLevels[entry.SHA256]; priorLevel != "" && observation.Status != cache.ObservationValid {
			result.CanPrewarm = true
			result.ReuseAfterLevel = priorLevel
		}
		if result.MaterializationPossible && preparedLevels[entry.SHA256] == "" {
			preparedLevels[entry.SHA256] = entry.Level
		}
		plan.Entries = append(plan.Entries, result)
	}
	return idx, plan, nil
}

func describePrewarmEntry(entry format.VariantEntry, observation cache.VariantObservation) PrewarmPlanEntry {
	_, decoderErr := codec.Get(entry.Compression)
	result := PrewarmPlanEntry{
		Level: entry.Level, SHA256: entry.SHA256, UncompressedSize: entry.UncompressedSize,
		CachePath: observation.CachedPath, CacheStatus: observation.Status, CacheError: observation.Error,
		DecoderAvailable: decoderErr == nil,
	}
	if decoderErr != nil {
		result.DecoderError = decoderErr.Error()
	}
	safe := observation.Status == cache.ObservationValid || observation.Status == cache.ObservationMissing ||
		observation.Status == cache.ObservationCorruptSafe
	result.MaterializationPossible = result.DecoderAvailable && safe
	result.CanPrewarm = observation.Status == cache.ObservationValid || result.MaterializationPossible
	return result
}
