//go:build linux

package main

import (
	"errors"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
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
