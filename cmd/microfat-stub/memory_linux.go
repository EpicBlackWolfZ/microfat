//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

var (
	zstdDecoderMemoryFunc = cgroup.ZstdDecoderMemory
	extractionMemoryFunc  = cgroup.ExtractionMemory
)

func checkExtractionBudget(entry *format.VariantEntry, idx *format.Index) error {
	_, err := checkExtractionBudgetForReader(nil, entry, idx)
	return err
}

func checkExtractionBudgetForReader(selfReader io.ReaderAt, entry *format.VariantEntry, idx *format.Index) (uint64, error) {
	if entry.UncompressedSize <= 0 || entry.UncompressedSize > format.MaxPayloadSize {
		return 0, format.ErrPayloadTooLarge
	}
	dictionary := int64(0)
	if idx != nil {
		dictionary = idx.DictionarySize
	}
	if dictionary < 0 || dictionary > format.MaxDictionarySize {
		return 0, format.ErrInvalidDictionary
	}

	var zstdWindow uint64
	if entry.Compression == codec.AlgorithmZstd {
		zstdWindow = codec.DefaultDecoderMaxWindow
		if selfReader != nil && entry.Offset >= 0 && entry.CompressedSize > 0 {
			win, winErr := codec.InspectZstdFrameWindow(selfReader, entry.Offset, entry.CompressedSize)
			if errors.Is(winErr, codec.ErrWindowSizeExceeded) {
				return 0, fmt.Errorf("%w: %w", cgroup.ErrMemoryBudget, winErr)
			}
			if winErr == nil {
				zstdWindow = win
			}
		}
	}

	limits, err := readCgroupLimitsFunc()
	if err != nil || limits.CgroupVersion == cgroup.VersionUnknown {
		return zstdWindow, nil
	}
	limit, finite := limits.EffectiveMemoryLimit()
	if !finite || limit >= cgroup.UnlimitedCgroupV1MemoryThreshold {
		return zstdWindow, nil
	}
	// Decoder allowances combine verified sliding-window bounds (for Zstd) or codec scratch ceilings
	// with I/O copy buffers. They are estimates and cannot account for unrelated page pressure.
	const copyBuffer = int64(32 * 1024)
	const lz4Buffers = int64(12 * 1024 * 1024)
	decoder := copyBuffer
	switch entry.Compression {
	case codec.AlgorithmNone:
		decoder = int64(codec.NoneCopyBufferSize)
	case codec.AlgorithmZstd:
		zstdMem, zErr := zstdDecoderMemoryFunc(
			int64(zstdWindow),
			int64(codec.DefaultDecoderMaxWindow),
			int64(codec.DefaultZstdDecoderOverhead),
			int64(codec.DefaultDecoderMaxMemory),
		)
		if zErr != nil {
			return 0, zErr
		}
		decoder += zstdMem
	case codec.AlgorithmLZ4:
		decoder += lz4Buffers
	}
	estimate, err := extractionMemoryFunc(entry.UncompressedSize, dictionary, decoder, cgroup.DefaultMinHeadroomBytes)
	if err != nil {
		return 0, err
	}
	if estimate > limit {
		return 0, fmt.Errorf("%w: estimated extraction %d bytes exceeds cgroup ceiling %d", cgroup.ErrMemoryBudget, estimate, limit)
	}
	return zstdWindow, nil
}
