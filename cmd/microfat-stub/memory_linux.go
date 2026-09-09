//go:build linux

package main

import (
	"fmt"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

func checkExtractionBudget(entry *format.VariantEntry, idx *format.Index) error {
	if entry.UncompressedSize <= 0 || entry.UncompressedSize > format.MaxPayloadSize {
		return format.ErrPayloadTooLarge
	}
	dictionary := int64(0)
	if idx != nil {
		dictionary = idx.DictionarySize
	}
	if dictionary < 0 || dictionary > format.MaxDictionarySize {
		return format.ErrInvalidDictionary
	}
	limits, err := readCgroupLimitsFunc()
	if err != nil || limits.CgroupVersion == cgroup.VersionUnknown {
		return nil
	}
	limit := limits.EffectiveMemoryLimitBytes
	if limit <= 0 {
		limit = cgroup.CalculateEffectiveMemoryLimit(limits.MemoryLimitBytes, limits.MemoryHighBytes)
	}
	if limit <= 0 || limit >= cgroup.UnlimitedCgroupV1MemoryThreshold {
		return nil
	}
	// Deliberately conservative decoder allowances tied to the configured codec limits.
	// They are estimates and cannot account for unrelated processes or concurrent page pressure.
	const copyBuffer = int64(32 * 1024)
	const lz4Buffers = int64(12 * 1024 * 1024)
	decoder := copyBuffer
	switch entry.Compression {
	case codec.AlgorithmZstd:
		decoder += int64(codec.DefaultDecoderMaxMemory)
	case codec.AlgorithmLZ4:
		decoder += lz4Buffers
	}
	estimate, err := cgroup.ExtractionMemory(entry.UncompressedSize, dictionary, decoder, cgroup.DefaultMinHeadroomBytes)
	if err != nil {
		return err
	}
	if estimate > limit {
		return fmt.Errorf("%w: estimated extraction %d bytes exceeds cgroup ceiling %d", cgroup.ErrMemoryBudget, estimate, limit)
	}
	return nil
}
