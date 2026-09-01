package format_test

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/testutil"
)

const (
	propFormatIterations  = 30
	propMaxVariantCount   = 4
	propBaseOffset        = 1024
	propOffsetSpacing     = 2048
	propCompSizeDefault   = 512
	propUncompSizeDefault = 1024
	propDummyTimestamp    = 1724540000
	testSHA256SampleProp  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func TestFormat_PropertyInvariants(t *testing.T) {
	t.Parallel()

	levels := []string{"v1", "v2", "v3", "v4"}
	codecs := []string{"zstd", "lz4", "none"}

	testutil.RunPropertyTest(t, "IndexSerializationRoundtrip", propFormatIterations, 0, func(subT *testing.T, iter int, rng *rand.Rand) {
		variantCount := rng.IntN(propMaxVariantCount) + 1
		var variants []format.VariantEntry
		currentOffset := int64(propBaseOffset)

		for v := range variantCount {
			comp := codecs[rng.IntN(len(codecs))]
			lvl := levels[v]
			cSize := int64(propCompSizeDefault + rng.IntN(512))
			uSize := int64(propUncompSizeDefault + rng.IntN(1024))
			dummyHash := testSHA256SampleProp

			variants = append(variants, format.VariantEntry{
				Level:            lvl,
				Offset:           currentOffset,
				CompressedSize:   cSize,
				UncompressedSize: uSize,
				SHA256:           dummyHash,
				Compression:      comp,
			})
			currentOffset += cSize + propOffsetSpacing
		}

		for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
			idx := &format.Index{
				Version:     version,
				AppName:     "prop_app",
				TargetOS:    "linux",
				TargetArch:  "amd64",
				CreatedUnix: propDummyTimestamp + int64(iter),
				Variants:    variants,
			}

			var buf bytes.Buffer
			buf.Write(make([]byte, currentOffset))

			writtenBytes, err := format.WriteIndexAndTrailer(&buf, idx, currentOffset)
			if err != nil {
				subT.Fatalf("WriteIndexAndTrailer failed for version %d: %v", version, err)
			}

			fullBytes := buf.Bytes()
			totalSize := int64(len(fullBytes))

			if !format.IsFatBinary(bytes.NewReader(fullBytes), totalSize) {
				subT.Fatalf("IsFatBinary returned false for version %d", version)
			}

			readIdx, err := format.ReadTrailerAndIndex(bytes.NewReader(fullBytes), totalSize)
			if err != nil {
				subT.Fatalf("ReadTrailerAndIndex failed for version %d: %v", version, err)
			}

			if readIdx.AppName != idx.AppName || readIdx.TargetArch != idx.TargetArch || len(readIdx.Variants) != len(idx.Variants) {
				subT.Fatalf("deserialized index mismatch for version %d", version)
			}

			if writtenBytes < format.TrailerSize {
				subT.Fatalf("written bytes %d smaller than trailer size", writtenBytes)
			}

			// Invariant: Mutating trailer magic must fail
			corruptedBytes := make([]byte, len(fullBytes))
			copy(corruptedBytes, fullBytes)
			corruptedBytes[len(corruptedBytes)-1] ^= 0xFF

			if format.IsFatBinary(bytes.NewReader(corruptedBytes), totalSize) {
				subT.Fatalf("expected IsFatBinary false for corrupted magic")
			}
			_, err = format.ReadTrailerAndIndex(bytes.NewReader(corruptedBytes), totalSize)
			if !errors.Is(err, format.ErrInvalidMagic) {
				subT.Fatalf("expected ErrInvalidMagic for corrupted magic, got %v", err)
			}
		}
	})
}
