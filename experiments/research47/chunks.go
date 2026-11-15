package main

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

const (
	kib                   = 1024
	mib                   = kib * kib
	fixedSmall            = 512
	fixedLarge            = 4 * kib
	cdcMinimum            = kib
	cdcAverage            = 4 * kib
	cdcMaximum            = 16 * kib
	maxChunks             = 512 * kib
	maxVariants           = 3
	methodSections        = "sections"
	methodSmall           = "fixed512"
	methodLarge           = "fixed4096"
	methodCDC             = "cdc1024-4096-16384"
	methodBase            = "one-base-cdc"
	elfSectionOffsetField = 40
	elfSectionEntryField  = 58
	elfSectionCountField  = 60
	sectionTypeOffset     = 4
	sectionFileOffset     = 24
	sectionSizeOffset     = 32
)

type extent struct{ start, end int }

func splitChunks(data []byte, method string) ([]extent, error) {
	switch method {
	case methodSections:
		return sectionChunks(data)
	case methodSmall:
		return fixedChunks(len(data), fixedSmall), nil
	case methodLarge:
		return fixedChunks(len(data), fixedLarge), nil
	case methodCDC:
		return contentChunks(data), nil
	default:
		return nil, fmt.Errorf("unknown chunk method %q", method)
	}
}

func fixedChunks(size, block int) []extent {
	var result []extent
	for start := 0; start < size; start += block {
		result = append(result, extent{start, min(start+block, size)})
	}
	return result
}

// The fixed SplitMix seed defines this experiment's deterministic Gear table.
// This is a bounded content-defined partition, not an optimal matching oracle.
func gearTable() [256]uint64 {
	const (
		seed      = uint64(47)
		increment = uint64(0x9e3779b97f4a7c15)
		mixFirst  = uint64(0xbf58476d1ce4e5b9)
		mixSecond = uint64(0x94d049bb133111eb)
		shiftA    = 30
		shiftB    = 27
		shiftC    = 31
	)
	var table [256]uint64
	x := seed
	for i := range table {
		x += increment
		z := (x ^ (x >> shiftA)) * mixFirst
		z = (z ^ (z >> shiftB)) * mixSecond
		table[i] = z ^ (z >> shiftC)
	}
	return table
}

func contentChunks(data []byte) []extent {
	table := gearTable()
	var result []extent
	for start := 0; start < len(data); {
		end, hash := start, uint64(0)
		for end < min(start+cdcMaximum, len(data)) {
			hash = (hash << 1) + table[data[end]]
			end++
			if end-start >= cdcMinimum && hash&(cdcAverage-1) == 0 {
				break
			}
		}
		result = append(result, extent{start, end})
		start = end
	}
	return result
}

func sectionChunks(data []byte) ([]extent, error) {
	if err := pack.PreflightELFHeaders(data); err != nil {
		return nil, fmt.Errorf("reading ELF sections: %w", err)
	}
	if data[elf.EI_DATA] != byte(elf.ELFDATA2LSB) {
		return nil, errors.New("only little-endian ELF sections supported")
	}
	order := binary.LittleEndian
	offset := order.Uint64(data[elfSectionOffsetField:])
	entrySize := uint64(order.Uint16(data[elfSectionEntryField:]))
	count := uint64(order.Uint16(data[elfSectionCountField:]))
	if offset == 0 {
		return []extent{{0, len(data)}}, nil
	}
	if count == 0 {
		count = order.Uint64(data[offset+sectionSizeOffset:])
	}
	var sections []extent
	for i := uint64(0); i < count; i++ {
		// Preflight bounds both ordinary and extended section tables. Reading
		// offsets directly avoids decompressing or copying section-name tables.
		header := data[offset+i*entrySize:]
		kind := elf.SectionType(order.Uint32(header[sectionTypeOffset:]))
		start, size := order.Uint64(header[sectionFileOffset:]), order.Uint64(header[sectionSizeOffset:])
		if kind == elf.SHT_NOBITS || kind == elf.SHT_NULL || size == 0 {
			continue
		}
		if start > uint64(len(data)) || size > uint64(len(data))-start {
			return nil, errors.New("ELF section exceeds input")
		}
		// Both values are bounded by the input length before conversion.
		sections = append(sections, extent{int(start), int(start + size)}) // #nosec G115 -- bounded above
	}
	sort.Slice(sections, func(i, j int) bool { return sections[i].start < sections[j].start })
	var result []extent
	position := 0
	for _, section := range sections {
		if section.start < position {
			return nil, errors.New("overlapping file-backed ELF sections")
		}
		if section.start > position {
			result = append(result, extent{position, section.start})
		}
		result = append(result, section)
		position = section.end
	}
	if position < len(data) {
		result = append(result, extent{position, len(data)})
	}
	return result, nil
}

type chunkRecord struct {
	data       []byte
	prevalence uint8
	poolOffset int
}

type inventory struct {
	records []*chunkRecord
	paths   [][]*chunkRecord
	bytes   int64
	shared  int64
	core    int64
	unique  int64
}

func buildInventory(inputs [][]byte, method string) (inventory, error) {
	return buildInventoryLimit(inputs, method, maxChunks)
}

func buildInventoryLimit(inputs [][]byte, method string, limit int) (inventory, error) {
	if len(inputs) == 0 || len(inputs) > maxVariants {
		return inventory{}, errors.New("invalid inventory variant count")
	}
	inv := inventory{paths: make([][]*chunkRecord, len(inputs))}
	buckets := make(map[[sha256.Size]byte][]*chunkRecord)
	count := 0
	for variant, input := range inputs {
		chunks, err := splitChunks(input, method)
		if err != nil {
			return inventory{}, err
		}
		count += len(chunks)
		if count > limit {
			return inventory{}, errors.New("chunk count exceeds bound")
		}
		for _, chunk := range chunks {
			data := input[chunk.start:chunk.end]
			digest := sha256.Sum256(data)
			var match *chunkRecord
			for _, candidate := range buckets[digest] {
				if bytes.Equal(candidate.data, data) {
					match = candidate
					break
				}
			}
			if match == nil {
				match = &chunkRecord{data: data, poolOffset: -1}
				buckets[digest] = append(buckets[digest], match)
				inv.records = append(inv.records, match)
				inv.unique += int64(len(data))
			}
			match.prevalence |= 1 << variant
			inv.paths[variant] = append(inv.paths[variant], match)
			inv.bytes += int64(len(data))
		}
	}
	for _, path := range inv.paths {
		for _, record := range path {
			if bitsSet(record.prevalence) > 1 {
				inv.shared += int64(len(record.data))
			}
			if bitsSet(record.prevalence) == len(inputs) {
				inv.core += int64(len(record.data))
			}
		}
	}
	return inv, nil
}

func bitsSet(value uint8) int {
	count := 0
	for value != 0 {
		value &= value - 1
		count++
	}
	return count
}
