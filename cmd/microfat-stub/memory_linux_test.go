//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractionBudgetPolicies(t *testing.T) {
	old := readCgroupLimitsFunc
	t.Cleanup(func() { readCgroupLimitsFunc = old })
	const large = int64(2 * 1024 * 1024 * 1024)
	for _, tc := range []struct {
		name      string
		limit     int64
		version   int
		algorithm string
		wantErr   bool
	}{
		{"absent", 0, cgroup.VersionUnknown, codec.AlgorithmZstd, false},
		{"unlimited", 0, cgroup.VersionV2, codec.AlgorithmZstd, false},
		{"tiny", 1, cgroup.VersionV2, codec.AlgorithmNone, true},
		{"zstd", large, cgroup.VersionV2, codec.AlgorithmZstd, false},
		{"lz4", large, cgroup.VersionV1, codec.AlgorithmLZ4, false},
		{"raw", large, cgroup.VersionV2, codec.AlgorithmNone, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readCgroupLimitsFunc = func() (cgroup.Limits, error) {
				return cgroup.Limits{CgroupVersion: tc.version, MemoryLimitBytes: tc.limit}, nil
			}
			err := checkExtractionBudget(&format.VariantEntry{UncompressedSize: 1, Compression: tc.algorithm}, nil)
			if tc.wantErr {
				require.ErrorIs(t, err, cgroup.ErrMemoryBudget)
			} else {
				require.NoError(t, err)
			}
		})
	}
	entry := &format.VariantEntry{UncompressedSize: format.MaxPayloadSize + 1}
	require.ErrorIs(t, checkExtractionBudget(entry, nil), format.ErrPayloadTooLarge)
	entry.UncompressedSize = 1
	require.ErrorIs(t, checkExtractionBudget(entry, &format.Index{DictionarySize: format.MaxDictionarySize + 1}), format.ErrInvalidDictionary)
	readCgroupLimitsFunc = func() (cgroup.Limits, error) { return cgroup.Limits{}, errors.New("unavailable") }
	require.NoError(t, checkExtractionBudget(entry, nil))
}

func TestNoneExtractionBudgetIncludesCopyBuffer(t *testing.T) {
	old := readCgroupLimitsFunc
	t.Cleanup(func() { readCgroupLimitsFunc = old })

	const payloadSize = int64(1024 * 1024)
	const scratchSize = int64(128 * 1024)
	const required = payloadSize + scratchSize + cgroup.DefaultMinHeadroomBytes
	entry := &format.VariantEntry{UncompressedSize: payloadSize, Compression: codec.AlgorithmNone}
	for _, tc := range []struct {
		name    string
		limit   int64
		wantErr bool
	}{
		{"below_copy_buffer_requirement", required - 1, true},
		{"exact_copy_buffer_requirement", required, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readCgroupLimitsFunc = func() (cgroup.Limits, error) {
				return cgroup.Limits{
					CgroupVersion:             cgroup.VersionV2,
					MemoryLimitBytes:          tc.limit,
					EffectiveMemoryLimitBytes: tc.limit,
					MemoryLimitFinite:         true,
				}, nil
			}
			err := checkExtractionBudget(entry, nil)
			if tc.wantErr {
				require.ErrorIs(t, err, cgroup.ErrMemoryBudget)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestWindowAccurateZstdExtractionBudget(t *testing.T) {
	old := readCgroupLimitsFunc
	t.Cleanup(func() { readCgroupLimitsFunc = old })

	const (
		container128MiB = int64(128 * 1024 * 1024)
		container256MiB = int64(256 * 1024 * 1024)
		container2GiB   = int64(2 * 1024 * 1024 * 1024)
		win4KiB         = 4 * 1024
		win64MiB        = uint64(64 * 1024 * 1024)
		win256MiB       = uint64(256 * 1024 * 1024)
		win256KiB       = 256 * 1024
		raw64KiB        = 64 * 1024
		frame1Size      = 16 * 1024
		frame2Size      = 512 * 1024
		filePerms       = 0o600
	)

	setFiniteLimit := func(limit int64) {
		readCgroupLimitsFunc = func() (cgroup.Limits, error) {
			return cgroup.Limits{
				CgroupVersion:             cgroup.VersionV2,
				MemoryLimitBytes:          limit,
				EffectiveMemoryLimitBytes: limit,
				MemoryLimitFinite:         true,
			}, nil
		}
	}

	raw := bytes.Repeat([]byte("microfat-window-budget-"), raw64KiB/23+1)[:raw64KiB]
	var compressed bytes.Buffer
	enc, err := zstd.NewWriter(&compressed, zstd.WithWindowSize(win4KiB))
	require.NoError(t, err)
	_, err = enc.Write(raw)
	require.NoError(t, err)
	require.NoError(t, enc.Close())

	t.Run("small_window_zstd_succeeds_in_128MiB_container", func(t *testing.T) {
		setFiniteLimit(container128MiB)
		entry := &format.VariantEntry{
			Level:            "v1",
			Offset:           0,
			CompressedSize:   int64(compressed.Len()),
			UncompressedSize: int64(len(raw)),
			Compression:      codec.AlgorithmZstd,
		}
		win, budgetErr := checkExtractionBudgetForReader(bytes.NewReader(compressed.Bytes()), entry, nil)
		require.NoError(t, budgetErr)
		assert.Equal(t, uint64(win4KiB), win)
	})

	t.Run("crafted_256MiB_window_rejected_with_and_without_cgroup", func(t *testing.T) {
		crafted, appendErr := (&zstd.Header{WindowSize: win256MiB}).AppendTo(nil)
		require.NoError(t, appendErr)
		crafted = append(crafted, 0x01, 0x00, 0x00)
		entry := &format.VariantEntry{
			Level:            "v1",
			Offset:           0,
			CompressedSize:   int64(len(crafted)),
			UncompressedSize: 1,
			Compression:      codec.AlgorithmZstd,
		}

		setFiniteLimit(container2GiB)
		_, budgetErr := checkExtractionBudgetForReader(bytes.NewReader(crafted), entry, nil)
		require.ErrorIs(t, budgetErr, cgroup.ErrMemoryBudget)
		require.ErrorIs(t, budgetErr, codec.ErrWindowSizeExceeded)

		readCgroupLimitsFunc = func() (cgroup.Limits, error) {
			return cgroup.Limits{CgroupVersion: cgroup.VersionUnknown}, nil
		}
		_, budgetErr = checkExtractionBudgetForReader(bytes.NewReader(crafted), entry, nil)
		require.ErrorIs(t, budgetErr, cgroup.ErrMemoryBudget)
		require.ErrorIs(t, budgetErr, codec.ErrWindowSizeExceeded)
	})

	t.Run("valid_64MiB_window_rejected_in_128MiB_and_allowed_in_256MiB", func(t *testing.T) {
		header64, appendErr := (&zstd.Header{WindowSize: win64MiB}).AppendTo(nil)
		require.NoError(t, appendErr)
		header64 = append(header64, 0x01, 0x00, 0x00)
		entry := &format.VariantEntry{
			Level:            "v1",
			Offset:           0,
			CompressedSize:   int64(len(header64)),
			UncompressedSize: raw64KiB,
			Compression:      codec.AlgorithmZstd,
		}

		setFiniteLimit(container128MiB)
		_, budgetErr := checkExtractionBudgetForReader(bytes.NewReader(header64), entry, nil)
		require.ErrorIs(t, budgetErr, cgroup.ErrMemoryBudget)

		setFiniteLimit(container256MiB)
		win, budgetErr := checkExtractionBudgetForReader(bytes.NewReader(header64), entry, nil)
		require.NoError(t, budgetErr)
		assert.Equal(t, win64MiB, win)
	})

	t.Run("corrupt_header_falls_back_to_conservative_budget", func(t *testing.T) {
		corrupt := []byte("corrupt-zstd-magic")
		entry := &format.VariantEntry{
			Level:            "v1",
			Offset:           0,
			CompressedSize:   int64(len(corrupt)),
			UncompressedSize: raw64KiB,
			Compression:      codec.AlgorithmZstd,
		}

		setFiniteLimit(container2GiB)
		win, budgetErr := checkExtractionBudgetForReader(bytes.NewReader(corrupt), entry, nil)
		require.NoError(t, budgetErr)
		assert.Equal(t, codec.DefaultDecoderMaxWindow, win)

		setFiniteLimit(container128MiB)
		_, budgetErr = checkExtractionBudgetForReader(bytes.NewReader(corrupt), entry, nil)
		require.ErrorIs(t, budgetErr, cgroup.ErrMemoryBudget)
		assert.NotErrorIs(t, budgetErr, codec.ErrWindowSizeExceeded)
	})

	t.Run("multi_frame_escalation_blocked_by_inspected_window_cap", func(t *testing.T) {
		var frame1, frame2 bytes.Buffer
		raw1 := bytes.Repeat([]byte("f1-"), frame1Size/3+1)[:frame1Size]
		raw2 := bytes.Repeat([]byte("f2-"), frame2Size/3+1)[:frame2Size]

		e1, e1Err := zstd.NewWriter(&frame1, zstd.WithWindowSize(win4KiB))
		require.NoError(t, e1Err)
		_, e1Err = e1.Write(raw1)
		require.NoError(t, e1Err)
		require.NoError(t, e1.Close())

		e2, e2Err := zstd.NewWriter(&frame2, zstd.WithWindowSize(win256KiB))
		require.NoError(t, e2Err)
		_, e2Err = e2.Write(raw2)
		require.NoError(t, e2Err)
		require.NoError(t, e2.Close())

		combined := append(bytes.Clone(frame1.Bytes()), frame2.Bytes()...)
		combinedRaw := append(bytes.Clone(raw1), raw2...)
		sum := sha256.Sum256(combinedRaw)

		path := filepath.Join(t.TempDir(), "multi.zst")
		require.NoError(t, os.WriteFile(path, combined, filePerms))
		f, openErr := os.Open(path)
		require.NoError(t, openErr)
		t.Cleanup(func() { _ = f.Close() })

		entry := &format.VariantEntry{
			Level:            "v1",
			Offset:           0,
			CompressedSize:   int64(len(combined)),
			UncompressedSize: int64(len(combinedRaw)),
			SHA256:           hex.EncodeToString(sum[:]),
			Compression:      codec.AlgorithmZstd,
		}

		setFiniteLimit(container128MiB)
		win, budgetErr := checkExtractionBudgetForReader(f, entry, nil)
		require.NoError(t, budgetErr)
		assert.Equal(t, uint64(win4KiB), win)

		var out bytes.Buffer
		extErr := extractVariantToWriterWithWindow(f, entry, nil, &out, win)
		require.ErrorIs(t, extErr, codec.ErrDecompressionFailed)
		require.ErrorIs(t, extErr, codec.ErrWindowSizeExceeded)
		assert.False(t, isPayloadCorruptionOrDecompressionError(nil))
		assert.True(t, isPayloadCorruptionOrDecompressionError(codec.ErrWindowSizeExceeded))
	})

	t.Run("propagates_zstd_decoder_and_extraction_budget_errors", func(t *testing.T) {
		oldZstd := zstdDecoderMemoryFunc
		oldExt := extractionMemoryFunc
		t.Cleanup(func() {
			zstdDecoderMemoryFunc = oldZstd
			extractionMemoryFunc = oldExt
		})

		entry := &format.VariantEntry{
			Level:            "v1",
			Offset:           0,
			CompressedSize:   int64(compressed.Len()),
			UncompressedSize: int64(len(raw)),
			Compression:      codec.AlgorithmZstd,
		}
		setFiniteLimit(container2GiB)

		zstdDecoderMemoryFunc = func(_, _, _, _ int64) (int64, error) {
			return 0, cgroup.ErrMemoryBudget
		}
		_, budgetErr := checkExtractionBudgetForReader(bytes.NewReader(compressed.Bytes()), entry, nil)
		require.ErrorIs(t, budgetErr, cgroup.ErrMemoryBudget)

		zstdDecoderMemoryFunc = oldZstd
		extractionMemoryFunc = func(_, _, _, _ int64) (int64, error) {
			return 0, cgroup.ErrMemoryBudget
		}
		_, budgetErr = checkExtractionBudgetForReader(bytes.NewReader(compressed.Bytes()), entry, nil)
		require.ErrorIs(t, budgetErr, cgroup.ErrMemoryBudget)
	})
}
