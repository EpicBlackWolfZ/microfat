package format

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckDictionarySegmentBounds(t *testing.T) {
	t.Parallel()

	const (
		validSize      int64 = 1024
		pastDictOffset int64 = 500
		pastDictSize   int64 = 600
	)

	for _, tc := range []struct {
		name                          string
		indexOffset, dictOffset, size int64
		wantEnd                       int64
		wantErr                       error
	}{
		{"empty_dict", 1000, 0, 0, 0, nil},
		{"exact_index_boundary", 2000, 976, validSize, 2000, nil},
		{"negative_index_offset", -1, 0, 0, 0, ErrOutOfBounds},
		{"negative_dict_size", 1000, 0, -1, 0, ErrInvalidDictionary},
		{"oversized_dict", 10 * 1024 * 1024, 0, MaxDictionarySize + 1, 0, ErrInvalidDictionary},
		{"negative_dict_offset", 1000, -1, validSize, 0, ErrInvalidDictionary},
		{"offset_past_index", 1000, 1001, validSize, 0, ErrOutOfBounds},
		{"dict_extends_past_index", 1000, pastDictOffset, pastDictSize, 0, ErrOutOfBounds},
		{"offset_plus_size_overflow_guard", math.MaxInt64, math.MaxInt64 - 10, validSize, 0, ErrOutOfBounds},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := checkDictionarySegmentBounds(tc.indexOffset, tc.dictOffset, tc.size)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantEnd, got)
			}
		})
	}
}

func TestCheckVariantSegmentBounds(t *testing.T) {
	t.Parallel()

	const (
		validCompressed   int64 = 512
		validUncompressed int64 = 4096
		indexEnd          int64 = 4096
		pastVariantOffset int64 = 3800
	)

	for _, tc := range []struct {
		name                                     string
		indexOffset, lastEnd                     int64
		hasPrev                                  bool
		offset, compressedSize, uncompressedSize int64
		wantEnd                                  int64
		wantErr                                  error
	}{
		{"first_variant_valid", indexEnd, 0, false, 1024, validCompressed, validUncompressed, 1536, nil},
		{"adjacent_to_previous_valid", indexEnd, 1536, true, 1536, validCompressed, validUncompressed, 2048, nil},
		{"exact_index_boundary", indexEnd, 0, false, indexEnd - validCompressed, validCompressed, validUncompressed, indexEnd, nil},
		{"negative_index_offset", -1, 0, false, 0, validCompressed, validUncompressed, 0, ErrOutOfBounds},
		{"negative_offset", indexEnd, 0, false, -1, validCompressed, validUncompressed, 0, ErrOutOfBounds},
		{"zero_compressed", indexEnd, 0, false, 0, 0, validUncompressed, 0, ErrOutOfBounds},
		{"zero_uncompressed", indexEnd, 0, false, 0, validCompressed, 0, 0, ErrOutOfBounds},
		{"payload_too_large", indexEnd, 0, false, 0, validCompressed, MaxPayloadSize + 1, 0, ErrPayloadTooLarge},
		{"offset_past_index", indexEnd, 0, false, indexEnd + 1, validCompressed, validUncompressed, 0, ErrOutOfBounds},
		{"variant_extends_past_index", indexEnd, 0, false, pastVariantOffset, validCompressed, validUncompressed, 0, ErrOutOfBounds},
		{"sum_exceeds_index_overflow_guard", math.MaxInt64, 0, false, math.MaxInt64 - 10, validCompressed, validUncompressed, 0, ErrOutOfBounds},
		{"negative_last_end", indexEnd, -1, true, 1024, validCompressed, validUncompressed, 0, ErrOutOfBounds},
		{"overlap_with_previous", indexEnd, 1536, true, 1535, validCompressed, validUncompressed, 0, ErrOverlappingVariant},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := checkVariantSegmentBounds(
				tc.indexOffset, tc.lastEnd, tc.hasPrev,
				tc.offset, tc.compressedSize, tc.uncompressedSize,
			)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantEnd, got)
			}
		})
	}
}
