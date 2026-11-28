package format

import (
	"errors"
	"math"
)

const (
	// MaxPayloadSize is the maximum allowable single payload uncompressed size (1 GB).
	MaxPayloadSize = 1024 * 1024 * 1024

	// MaxDictionarySize is the maximum allowable shared dictionary size (1 MB).
	MaxDictionarySize = 1024 * 1024
)

// Bounds and layout error sentinels shared across Format v2 parsing, validation, and launcher dispatch.
var (
	ErrBinaryTooSmall     = errors.New("binary size is smaller than trailer size")
	ErrInvalidIndexOffset = errors.New("invalid index offset in trailer")
	ErrInvalidIndexSize   = errors.New("invalid index size in trailer")
	ErrOutOfBounds        = errors.New("variant payload extends beyond binary boundary")
	ErrPayloadTooLarge    = errors.New("variant payload size exceeds safety limit")
	ErrOverlappingVariant = errors.New("variant payloads overlap or are unsorted")
	ErrInvalidDictionary  = errors.New("invalid shared dictionary offset or size")
)

/*@
ghost
decreases
pure func trailerAndIndexBoundsValid(totalSize, indexOffset, indexSize, trailerSize, maxIndexSize int64) bool {
	return trailerSize <= totalSize &&
		0 <= indexOffset &&
		integer(indexOffset) < integer(totalSize)-integer(trailerSize) &&
		0 < indexSize &&
		indexSize <= maxIndexSize &&
		integer(indexOffset)+integer(indexSize) == integer(totalSize)-integer(trailerSize)
}
ghost
decreases
pure func dictBoundsValid(indexOffset, dictOffset, dictSize int64) bool {
	return 0 <= indexOffset &&
		0 <= dictSize && dictSize <= MaxDictionarySize &&
		(dictSize == 0 || (0 <= dictOffset &&
			integer(dictOffset)+integer(dictSize) <= math.MaxInt64 &&
			integer(dictOffset)+integer(dictSize) <= integer(indexOffset)))
}

ghost
decreases
pure func expectedDictEnd(dictOffset, dictSize int64) integer {
	return dictSize == 0 ? 0 : integer(dictOffset) + integer(dictSize)
}

ghost
decreases
pure func variantBoundsValid(
	indexOffset, lastEnd int64,
	hasPrev bool,
	offset, compressedSize, uncompressedSize int64,
) bool {
	return 0 <= indexOffset &&
		0 <= offset && 0 < compressedSize && 0 < uncompressedSize &&
		uncompressedSize <= MaxPayloadSize &&
		integer(offset)+integer(compressedSize) <= math.MaxInt64 &&
		integer(offset)+integer(compressedSize) <= integer(indexOffset) &&
		(!hasPrev || (0 <= lastEnd && lastEnd <= offset))
}
@*/

// checkDictionarySegmentBounds validates Format v2 shared dictionary bounds without integer overflow.
// @ requires ErrOutOfBounds != nil && isComparable(ErrOutOfBounds)
// @ requires ErrInvalidDictionary != nil && isComparable(ErrInvalidDictionary)
// @ ensures (err == nil) == dictBoundsValid(indexOffset, dictOffset, dictSize)
// @ ensures err == nil ==> integer(lastEnd) == expectedDictEnd(dictOffset, dictSize)
// @ ensures err == nil ==> 0 <= lastEnd && lastEnd <= indexOffset
// @ ensures err != nil ==> lastEnd == 0 && isComparable(err) && (err == ErrOutOfBounds || err == ErrInvalidDictionary)
// @ decreases
func checkDictionarySegmentBounds(indexOffset, dictOffset, dictSize int64) (
	/*@ lastEnd @*/ int64,
	/*@ err @*/ error,
) {
	if indexOffset < 0 {
		return 0, ErrOutOfBounds
	}
	if dictSize < 0 || dictSize > MaxDictionarySize {
		return 0, ErrInvalidDictionary
	}
	if dictSize == 0 {
		return 0, nil
	}
	if dictOffset < 0 {
		return 0, ErrInvalidDictionary
	}
	if dictOffset > indexOffset {
		return 0, ErrOutOfBounds
	}
	if dictSize > math.MaxInt64-dictOffset {
		return 0, ErrOutOfBounds
	}
	if dictSize > indexOffset-dictOffset {
		return 0, ErrOutOfBounds
	}
	return dictOffset + dictSize, nil
}

// checkVariantSegmentBounds validates a Format v2 variant extent for dimensions, index bounds,
// overflow freedom, and non-overlap against the preceding segment ending at lastEnd.
// @ requires ErrOutOfBounds != nil && isComparable(ErrOutOfBounds)
// @ requires ErrPayloadTooLarge != nil && isComparable(ErrPayloadTooLarge)
// @ requires ErrOverlappingVariant != nil && isComparable(ErrOverlappingVariant)
// @ ensures (err == nil) == variantBoundsValid(indexOffset, lastEnd, hasPrev, offset, compressedSize, uncompressedSize)
// @ ensures err == nil ==> integer(nextEnd) == integer(offset) + integer(compressedSize)
// @ ensures err == nil ==> 0 <= nextEnd && nextEnd <= indexOffset && (!hasPrev || lastEnd <= offset)
// @ ensures err != nil ==> nextEnd == 0 && isComparable(err)
// @ ensures err != nil ==> (err == ErrOutOfBounds || err == ErrPayloadTooLarge || err == ErrOverlappingVariant)
// @ decreases
func checkVariantSegmentBounds(
	indexOffset, lastEnd int64,
	hasPrev bool,
	offset, compressedSize, uncompressedSize int64,
) (
	/*@ nextEnd @*/ int64,
	/*@ err @*/ error,
) {
	if indexOffset < 0 {
		return 0, ErrOutOfBounds
	}
	if offset < 0 || compressedSize <= 0 || uncompressedSize <= 0 {
		return 0, ErrOutOfBounds
	}
	if uncompressedSize > MaxPayloadSize {
		return 0, ErrPayloadTooLarge
	}
	if offset > indexOffset {
		return 0, ErrOutOfBounds
	}
	if compressedSize > math.MaxInt64-offset {
		return 0, ErrOutOfBounds
	}
	if compressedSize > indexOffset-offset {
		return 0, ErrOutOfBounds
	}
	if hasPrev && lastEnd < 0 {
		return 0, ErrOutOfBounds
	}
	if hasPrev && offset < lastEnd {
		return 0, ErrOverlappingVariant
	}
	return offset + compressedSize, nil
}

// checkTrailerAndIndexBounds validates Format v2 EOF trailer offset and index bounds without signed overflow.
// @ requires ErrBinaryTooSmall != nil && isComparable(ErrBinaryTooSmall)
// @ requires ErrInvalidIndexOffset != nil && isComparable(ErrInvalidIndexOffset)
// @ requires ErrInvalidIndexSize != nil && isComparable(ErrInvalidIndexSize)
// @ requires trailerSize > 0 && maxIndexSize > 0
// @ ensures (err == nil) == trailerAndIndexBoundsValid(totalSize, indexOffset, indexSize, trailerSize, maxIndexSize)
// @ ensures err == nil ==> integer(trailerOffset) == integer(totalSize) - integer(trailerSize)
// @ ensures err == nil ==> integer(trailerOffset) + integer(trailerSize) == integer(totalSize)
// @ ensures err == nil ==> 0 <= indexOffset && indexOffset < trailerOffset && 0 < indexSize && indexSize <= maxIndexSize
// @ ensures err != nil ==> trailerOffset == 0 && isComparable(err)
// @ ensures err != nil ==> (err == ErrBinaryTooSmall || err == ErrInvalidIndexOffset || err == ErrInvalidIndexSize)
// @ decreases
func checkTrailerAndIndexBounds(totalSize, indexOffset, indexSize, trailerSize, maxIndexSize int64) (
	/*@ trailerOffset @*/ int64,
	/*@ err @*/ error,
) {
	if totalSize < trailerSize {
		return 0, ErrBinaryTooSmall
	}
	trailerOffset := totalSize - trailerSize
	if indexOffset < 0 || indexOffset >= trailerOffset {
		return 0, ErrInvalidIndexOffset
	}
	if indexSize <= 0 || indexSize > maxIndexSize || indexSize != trailerOffset-indexOffset {
		return 0, ErrInvalidIndexSize
	}
	return trailerOffset, nil
}
