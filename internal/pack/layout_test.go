package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
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

type failingWriteSeeker struct {
	remaining int
	err       error
}

func (w *failingWriteSeeker) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, w.err
	}
	if len(p) <= w.remaining {
		w.remaining -= len(p)
		return len(p), nil
	}
	n := w.remaining
	w.remaining = 0
	return n, w.err
}

func (w *failingWriteSeeker) Seek(int64, int) (int64, error) {
	return 0, nil
}

func TestWriteDictionaryAndVariants_LayoutAndWriteErrors(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "dict-layout-*.bin")
	require.NoError(t, err)
	defer func() { _ = tmpFile.Close() }()

	_, _, err = writeDictionaryAndVariants(tmpFile, t.TempDir(), nil, &Options{}, math.MaxInt64, &format.Index{}, []byte("dict"), "sha")
	require.ErrorIs(t, err, ErrInvalidSegmentLayout)

	closedFile, err := os.CreateTemp(t.TempDir(), "dict-closed-*.bin")
	require.NoError(t, err)
	require.NoError(t, closedFile.Close())

	const validOffset int64 = 1024
	_, _, err = writeDictionaryAndVariants(closedFile, t.TempDir(), nil, &Options{}, validOffset, &format.Index{}, []byte("dict"), "sha")
	require.ErrorContains(t, err, "writing dictionary payload")
}

func TestWriteVariantPayload_InvalidSegmentExtent(t *testing.T) {
	t.Parallel()

	const samplePayloadSize = 64
	dir := t.TempDir()
	payloadPath := filepath.Join(dir, "variant.bin")
	require.NoError(t, os.WriteFile(payloadPath, bytes.Repeat([]byte{'A'}, samplePayloadSize), 0o600))

	tmpFile, err := os.CreateTemp(dir, "extent-*.bin")
	require.NoError(t, err)
	defer func() { _ = tmpFile.Close() }()

	const mismatchedOffset int64 = 10000
	_, _, err = writeVariantPayload(tmpFile, "v1", payloadPath, &Options{Compression: codec.AlgorithmNone}, mismatchedOffset, nil)
	require.ErrorIs(t, err, ErrInvalidSegmentLayout)
}

func TestCopyTrimmedSegment_Errors(t *testing.T) {
	t.Parallel()

	src := bytes.NewReader([]byte("segment-bytes"))
	writeErr := errors.New("write failed")

	_, _, err := copyTrimmedSegment(&failingWriteSeeker{err: writeErr}, src, 0, int64(src.Len()), math.MaxInt64, "dictionary")
	require.ErrorIs(t, err, ErrInvalidSegmentLayout)

	const validOffset int64 = 1024
	_, _, err = copyTrimmedSegment(&failingWriteSeeker{err: writeErr}, src, 0, int64(src.Len()), validOffset, "dictionary")
	require.ErrorContains(t, err, "copying dictionary frame")
}

func TestTrimBinary_DictionaryCopyWriteError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binPath := filepath.Join(dir, "fat.bin")
	f, err := os.Create(binPath)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	stub := []byte("STUB-HDR")
	dict := []byte("DICT-PAYLOAD")
	variant := []byte("VARIANT-PAYLOAD")

	_, err = f.Write(stub)
	require.NoError(t, err)
	dictOff := int64(len(stub))
	_, err = f.Write(dict)
	require.NoError(t, err)
	varOff := dictOff + int64(len(dict))
	_, err = f.Write(variant)
	require.NoError(t, err)

	dictSum := sha256.Sum256(dict)
	varSum := sha256.Sum256(variant)
	idx := &format.Index{
		Version:          format.FormatVersion2,
		AppName:          "app",
		TargetOS:         testOSLinux,
		TargetArch:       testArchAMD64,
		DictionaryOffset: dictOff,
		DictionarySize:   int64(len(dict)),
		DictionarySHA256: hex.EncodeToString(dictSum[:]),
		DictionaryID:     codec.DefaultDictionaryID,
		Variants: []format.VariantEntry{{
			Level:            "amd64_v1",
			Offset:           varOff,
			CompressedSize:   int64(len(variant)),
			UncompressedSize: int64(len(variant)),
			SHA256:           hex.EncodeToString(varSum[:]),
			Compression:      codec.AlgorithmNone,
		}},
	}
	indexOffset := varOff + int64(len(variant))
	_, err = format.WriteIndexAndTrailer(f, idx, indexOffset)
	require.NoError(t, err)

	info, err := f.Stat()
	require.NoError(t, err)

	out := &failingWriteSeeker{remaining: len(stub), err: errors.New("disk full")}
	_, err = TrimBinary(f, info.Size(), "amd64_v1", out)
	require.ErrorContains(t, err, "copying dictionary frame")
}
