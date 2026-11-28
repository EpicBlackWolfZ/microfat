package pack

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdvanceLayoutOffset(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name              string
		currentOffset, sz int64
		wantNext          int64
		wantErr           error
	}{
		{"valid_advance", 1024, 512, 1536, nil},
		{"non_positive_offset", 0, 512, 0, ErrInvalidSegmentLayout},
		{"non_positive_segment", 1024, 0, 0, ErrInvalidSegmentLayout},
		{"overflow_guard", math.MaxInt64 - 10, 11, 0, ErrInvalidSegmentLayout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := advanceLayoutOffset(tc.currentOffset, tc.sz)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantNext, got)
			}
		})
	}
}

func TestComputeSegmentExtent(t *testing.T) {
	t.Parallel()

	const limit int64 = 4096

	for _, tc := range []struct {
		name                          string
		start, end, maxUncomp, uncomp int64
		wantCompressed                int64
		wantErr                       error
	}{
		{"valid_extent", 512, 1536, limit, 2048, 1024, nil},
		{"non_positive_start", 0, 100, limit, 100, 0, ErrInvalidSegmentLayout},
		{"end_not_after_start", 100, 100, limit, 100, 0, ErrInvalidSegmentLayout},
		{"non_positive_limit", 100, 200, 0, 100, 0, ErrInvalidSegmentLayout},
		{"non_positive_uncomp", 100, 200, limit, 0, 0, ErrInvalidSegmentLayout},
		{"uncomp_exceeds_limit", 100, 200, limit, limit + 1, 0, ErrInvalidSegmentLayout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := computeSegmentExtent(tc.start, tc.end, tc.maxUncomp, tc.uncomp)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantCompressed, got)
			}
		})
	}
}
