package pack

import (
	"errors"
	"math"
)

// ErrInvalidSegmentLayout indicates an invalid, non-positive, overlapping, or overflowing segment offset.
var ErrInvalidSegmentLayout = errors.New("invalid packed segment layout")

/*@
ghost
decreases
pure func validLayoutAdvance(currentOffset, segmentSize int64) bool {
	return 0 < currentOffset && 0 < segmentSize &&
		integer(currentOffset)+integer(segmentSize) <= math.MaxInt64
}

ghost
decreases
pure func validSegmentExtent(startOffset, endOffset, maxUncompressed, uncompressedSize int64) bool {
	return 0 < startOffset && startOffset < endOffset &&
		0 < maxUncompressed && 0 < uncompressedSize && uncompressedSize <= maxUncompressed
}
@*/

// advanceLayoutOffset advances a contiguous packed container write cursor by segmentSize
// without signed integer overflow, guaranteeing strict forward progress.
// @ requires ErrInvalidSegmentLayout != nil && isComparable(ErrInvalidSegmentLayout)
// @ ensures (err == nil) == validLayoutAdvance(currentOffset, segmentSize)
// @ ensures err == nil ==> integer(nextOffset) == integer(currentOffset) + integer(segmentSize)
// @ ensures err == nil ==> currentOffset < nextOffset && 0 < nextOffset
// @ ensures err != nil ==> nextOffset == 0 && isComparable(err) && err == ErrInvalidSegmentLayout
// @ decreases
func advanceLayoutOffset(currentOffset, segmentSize int64) (
	/*@ nextOffset @*/ int64,
	/*@ err @*/ error,
) {
	if currentOffset <= 0 || segmentSize <= 0 {
		return 0, ErrInvalidSegmentLayout
	}
	if segmentSize > math.MaxInt64-currentOffset {
		return 0, ErrInvalidSegmentLayout
	}
	return currentOffset + segmentSize, nil
}

// computeSegmentExtent computes the positive byte length of a packed segment from [startOffset, endOffset)
// while checking uncompressed payload bounds against maxUncompressed.
// @ requires ErrInvalidSegmentLayout != nil && isComparable(ErrInvalidSegmentLayout)
// @ ensures (err == nil) == validSegmentExtent(startOffset, endOffset, maxUncompressed, uncompressedSize)
// @ ensures err == nil ==> integer(compressedSize) == integer(endOffset) - integer(startOffset)
// @ ensures err == nil ==> 0 < compressedSize && integer(startOffset)+integer(compressedSize) == integer(endOffset)
// @ ensures err != nil ==> compressedSize == 0 && isComparable(err) && err == ErrInvalidSegmentLayout
// @ decreases
func computeSegmentExtent(startOffset, endOffset, maxUncompressed, uncompressedSize int64) (
	/*@ compressedSize @*/ int64,
	/*@ err @*/ error,
) {
	if startOffset <= 0 || endOffset <= startOffset {
		return 0, ErrInvalidSegmentLayout
	}
	if maxUncompressed <= 0 || uncompressedSize <= 0 || uncompressedSize > maxUncompressed {
		return 0, ErrInvalidSegmentLayout
	}
	return endOffset - startOffset, nil
}
