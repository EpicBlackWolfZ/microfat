package compress

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"runtime"
	"sort"
	"sync"

	"github.com/klauspost/compress/zstd"
)

const (
	// V3ContainerMagic identifies Format v3 lab containers on disk (exact 8 bytes).
	V3ContainerMagic = "\x00\xFA\x7FM3H\x00\x01"
	// V3IndexMagic identifies Format v3 serialized metadata tables.
	V3IndexMagic = "\x00\xFAM3"
	// V3TrailerMagic identifies the fixed 56-byte Format v3 trailer (exact 8 bytes).
	V3TrailerMagic = "\x00\xFA\x7FM3T\x00\x01"

	v3HeaderLen         = 8
	v3TrailerSize       = 56
	v3TrailerIdxOff     = 0
	v3TrailerSizeOff    = 8
	v3TrailerHashOff    = 16
	v3TrailerMagicOff   = 48
	v3MaxIndexSize      = 16 * 1024 * 1024
	v3MaxVariantBytes   = 512 * 1024 * 1024
	v3MinChunkFloor     = 16 * 1024
	v3DefaultChunkSize  = 128 * 1024
	v3MaskShiftExtra    = 1
	v3DefaultDictSize   = 112 * 1024
	dictHeaderReserve   = 4096
	minDictHistoryBytes = 64
	minRawDictHistBytes = 8
	dictHistDivisor     = 4
	maxDictSampleBytes  = 128 * 1024
	cdcSizeDivisor      = 2
	cdcSizeMult         = 2
	minDictSamples      = 2
	preallocHalfDiv     = 2
	arm64AlignMask      = uint64(arm64InsnLen - 1)
	arm64AlignRound     = uint64(arm64InsnLen - 1)

	v3FlagFiltered  byte = 1 << 0
	v3FlagHasDict   byte = 1 << 1
	extFlagFiltered byte = 1 << 0

	u16Bytes  = 2
	u32Bytes  = 4
	u64Bytes  = 8
	shaLen    = 32
	gearSlots = 256

	splitMixGamma uint64 = 0x9E3779B97F4A7C15
	splitMixMult1 uint64 = 0xBF58476D1CE4E5B9
	splitMixMult2 uint64 = 0x94D049BB133111EB
	splitMixS1           = 30
	splitMixS2           = 27
	splitMixS3           = 31
)

var (
	// ErrInvalidV3Container indicates malformed Format v3 headers, trailers, bounds, or digests.
	ErrInvalidV3Container = errors.New("invalid format v3 container")
	// ErrVariantNotFound indicates a requested level does not exist in the v3 archive.
	ErrVariantNotFound = errors.New("variant not found in format v3 container")
)

// ChunkMode selects how ELF variant bytes are partitioned into independent compressed blocks.
type ChunkMode string

const (
	// ChunkFixed splits variant bytes into fixed-size blocks (plus trailing tail).
	ChunkFixed ChunkMode = "fixed"
	// ChunkSectionCDC splits variant bytes at ELF section boundaries and Gear FastCDC anchors.
	ChunkSectionCDC ChunkMode = "section-cdc"
)

// V3PackOptions configures Format v3 prototype packing and deduplication.
type V3PackOptions struct {
	TargetArch     string
	Variants       map[string][]byte
	ChunkMode      ChunkMode
	AvgChunkSize   int
	EnableBCJRIP   bool
	EnableDict     bool
	DictProfitable bool
	DictSize       int
	ZstdLevel      zstd.EncoderLevel
	ParallelEncode int
}

// V3BlockMeta describes one unique content-addressed Zstd frame in the container.
type V3BlockMeta struct {
	Offset         uint64
	CompressedSize uint32
	RawSize        uint32
	SHA256         [shaLen]byte
}

// V3Extent maps a unique block into a variant's reconstructed image and optional filter base offset.
type V3Extent struct {
	BlockIdx      uint32
	VariantOffset uint64
	Length        uint32
	Filtered      bool
	FilterBasePos uint32
}

// V3VariantMeta describes how to reconstruct, unfilter, and verify one variant.
type V3VariantMeta struct {
	Level            string
	UncompressedSize uint64
	Filtered         bool
	OrigSHA256       [shaLen]byte
	ExecRanges       []SectionRange
	Extents          []V3Extent
}

// V3Archive holds parsed Format v3 metadata and global block index.
type V3Archive struct {
	TargetArch   string
	Machine      uint16
	IndexOffset  uint64
	HasDict      bool
	DictOffset   uint64
	DictSize     uint32
	DictSHA256   [shaLen]byte
	Blocks       []V3BlockMeta
	Variants     []V3VariantMeta
	variantByLvl map[string]int
}

// V3PackStats reports deduplication and size breakdowns for benchmarking.
type V3PackStats struct {
	LogicalRawBytes    int64
	UniqueRawBytes     int64
	DeduplicatedBytes  int64
	UniqueBlockCount   int
	LogicalExtentCount int
	CompressedPayload  int64
	DictBytes          int64
	IndexBytes         int64
	TotalArchiveBytes  int64
}

type pendingBlock struct {
	key        [shaLen]byte
	raw        []byte
	compressed []byte
}

var gearTable = buildGearTable()

func buildGearTable() [gearSlots]uint64 {
	var table [gearSlots]uint64
	for i := range gearSlots {
		z := uint64(i+1) * splitMixGamma
		z = (z ^ (z >> splitMixS1)) * splitMixMult1
		z = (z ^ (z >> splitMixS2)) * splitMixMult2
		table[i] = z ^ (z >> splitMixS3)
	}
	return table
}

func fastCDCMasks(avgSize int) (minSize, maxSize int, maskS, maskL uint64) {
	if avgSize < v3MinChunkFloor {
		avgSize = v3DefaultChunkSize
	}
	pow2 := bits.Len(uint(avgSize)) - 1 // #nosec G115 -- avgSize bounded positive
	if pow2 < 1 {
		pow2 = 1
	}
	minSize = avgSize / cdcSizeDivisor
	maxSize = avgSize * cdcSizeMult
	// Shift right by (64 - bits) cleanly selects normalized Gear masks on MSBs or LSBs.
	maskS = (uint64(1) << (pow2 + v3MaskShiftExtra)) - 1
	maskL = (uint64(1) << (pow2 - v3MaskShiftExtra)) - 1
	return minSize, maxSize, maskS, maskL
}

func sectionAlignedSpans(raw []byte) []SectionRange {
	total := uint64(len(raw))
	if total == 0 {
		return nil
	}
	ef, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		return []SectionRange{{Start: 0, End: total}}
	}
	defer func() {
		_ = ef.Close()
	}()

	var cuts []uint64
	cuts = append(cuts, 0, total)
	for _, s := range ef.Sections {
		if s.Type == elf.SHT_NOBITS || s.Size == 0 || s.Offset >= total {
			continue
		}
		end := s.Offset + s.Size
		if end > total {
			end = total
		}
		cuts = append(cuts, s.Offset, end)
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i] < cuts[j] })
	var spans []SectionRange
	prev := cuts[0]
	for _, c := range cuts[1:] {
		if c > prev {
			spans = append(spans, SectionRange{Start: prev, End: c})
			prev = c
		}
	}
	return spans
}

func chunkVariantData(raw, img []byte, mode ChunkMode, avgSize int) []SectionRange {
	if avgSize < v3MinChunkFloor {
		avgSize = v3DefaultChunkSize
	}
	if mode == ChunkFixed {
		var out []SectionRange
		n := uint64(len(img))
		step := uint64(avgSize) // #nosec G115 -- avgSize >= v3MinChunkFloor
		for off := uint64(0); off < n; off += step {
			end := off + step
			if end > n {
				end = n
			}
			out = append(out, SectionRange{Start: off, End: end})
		}
		return out
	}

	minSize, maxSize, maskS, maskL := fastCDCMasks(avgSize)
	spans := sectionAlignedSpans(raw)
	var out []SectionRange
	for _, sp := range spans {
		off := int(sp.Start)   // #nosec G115 -- bounded by len(img)
		spanEnd := int(sp.End) // #nosec G115 -- bounded by len(img)
		for off < spanEnd {
			rem := spanEnd - off
			if rem <= minSize {
				out = append(out, SectionRange{
					Start: uint64(off),     // #nosec G115 -- non-negative slice index
					End:   uint64(spanEnd), // #nosec G115 -- non-negative slice index
				})
				off = spanEnd
				continue
			}
			norm := off + avgSize
			if norm > spanEnd {
				norm = spanEnd
			}
			lim := off + maxSize
			if lim > spanEnd {
				lim = spanEnd
			}
			cut := off + minSize
			var fp uint64
			for cut < norm {
				fp = (fp << 1) + gearTable[img[cut]]
				cut++
				if (fp & maskS) == 0 {
					goto emit
				}
			}
			for cut < lim {
				fp = (fp << 1) + gearTable[img[cut]]
				cut++
				if (fp & maskL) == 0 {
					goto emit
				}
			}
		emit:
			out = append(out, SectionRange{
				Start: uint64(off), // #nosec G115 -- non-negative slice index
				End:   uint64(cut), // #nosec G115 -- non-negative slice index
			})
			off = cut
		}
	}
	return out
}

func snapARM64FilterCuts(sectionLen uint64, relCuts []uint64) []uint64 {
	if sectionLen == 0 || len(relCuts) == 0 {
		return nil
	}
	out := make([]uint64, 0, len(relCuts))
	var prev uint64
	for _, c := range relCuts {
		if c == 0 || c >= sectionLen {
			continue
		}
		snapped := (c + arm64AlignRound) &^ arm64AlignMask
		if snapped > prev && snapped < sectionLen {
			out = append(out, snapped)
			prev = snapped
		}
	}
	return out
}

func alignChunksToExecRanges(
	rawChunks []SectionRange,
	raw []byte,
	execRanges []SectionRange,
	machine elf.Machine,
) []SectionRange {
	if len(rawChunks) == 0 || len(execRanges) == 0 {
		return rawChunks
	}
	totalLen := uint64(len(raw))
	cuts := make([]uint64, 0, len(rawChunks)+1+len(execRanges)*preallocHalfDiv)
	cuts = append(cuts, 0, totalLen)
	for _, ch := range rawChunks {
		cuts = append(cuts, ch.Start, ch.End)
	}
	for _, er := range execRanges {
		cuts = append(cuts, er.Start, er.End)
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i] < cuts[j] })

	uniq := make([]uint64, 0, len(cuts))
	for _, c := range cuts {
		if len(uniq) == 0 || c > uniq[len(uniq)-1] {
			uniq = append(uniq, c)
		}
	}

	finalCuts := make([]uint64, 0, len(uniq))
	finalCuts = append(finalCuts, 0)
	appendCut := func(c uint64) {
		if c > finalCuts[len(finalCuts)-1] {
			finalCuts = append(finalCuts, c)
		}
	}

	ci := 0
	for ci < len(uniq) && uniq[ci] == 0 {
		ci++
	}
	for _, er := range execRanges {
		for ci < len(uniq) && uniq[ci] <= er.Start {
			appendCut(uniq[ci])
			ci++
		}
		appendCut(er.Start)
		var relCuts []uint64
		for ci < len(uniq) && uniq[ci] < er.End {
			relCuts = append(relCuts, uniq[ci]-er.Start)
			ci++
		}
		switch machine {
		case elf.EM_X86_64:
			relCuts = SnapX86FilterCuts(raw[er.Start:er.End], relCuts)
		case elf.EM_AARCH64:
			relCuts = snapARM64FilterCuts(er.End-er.Start, relCuts)
		}
		for _, rc := range relCuts {
			appendCut(er.Start + rc)
		}
		appendCut(er.End)
		for ci < len(uniq) && uniq[ci] == er.End {
			ci++
		}
	}
	for ci < len(uniq) {
		appendCut(uniq[ci])
		ci++
	}

	out := make([]SectionRange, 0, len(finalCuts)-1)
	for i := 0; i+1 < len(finalCuts); i++ {
		if finalCuts[i+1] > finalCuts[i] {
			out = append(out, SectionRange{Start: finalCuts[i], End: finalCuts[i+1]})
		}
	}
	return out
}

func execRangeForChunk(ch SectionRange, execRanges []SectionRange, idx *int) (SectionRange, bool) {
	for *idx < len(execRanges) && execRanges[*idx].End <= ch.Start {
		*idx++
	}
	if *idx < len(execRanges) {
		er := execRanges[*idx]
		if ch.Start >= er.Start && ch.End <= er.End {
			return er, true
		}
	}
	return SectionRange{}, false
}

func prepareVariantFilterProbe(raw []byte, enableFilter bool) ([]byte, []SectionRange, elf.Machine, error) {
	if !enableFilter {
		return raw, nil, elf.EM_NONE, nil
	}
	ranges, machine, err := ExecutableRangesELF(raw)
	if err != nil {
		return nil, nil, elf.EM_NONE, err
	}
	if len(ranges) == 0 {
		return raw, nil, machine, nil
	}
	probeImg := make([]byte, len(raw))
	copy(probeImg, raw)
	if err := ApplyELFFilter(probeImg, ranges, machine, true); err != nil {
		return nil, nil, machine, err
	}
	return probeImg, ranges, machine, nil
}

func buildStratifiedDictHistory(blocks []*pendingBlock, maxHist int) []byte {
	var hist bytes.Buffer
	hist.Grow(maxHist)
	taken := make([]int, len(blocks))
	perBlock := max(maxHist/len(blocks), minDictHistoryBytes)

	for i, b := range blocks {
		if hist.Len() >= maxHist {
			break
		}
		want := min(perBlock, len(b.raw), maxHist-hist.Len())
		if want > 0 {
			hist.Write(b.raw[:want])
			taken[i] = want
		}
	}
	for i, b := range blocks {
		if hist.Len() >= maxHist {
			break
		}
		if taken[i] < len(b.raw) {
			want := min(len(b.raw)-taken[i], maxHist-hist.Len())
			hist.Write(b.raw[taken[i] : taken[i]+want])
		}
	}
	return hist.Bytes()
}

func buildDictSamples(blocks []*pendingBlock) [][]byte {
	samples := make([][]byte, 0, len(blocks))
	for _, b := range blocks {
		s := b.raw
		for len(s) > maxDictSampleBytes {
			samples = append(samples, s[:maxDictSampleBytes])
			s = s[maxDictSampleBytes:]
		}
		if len(s) > 0 {
			samples = append(samples, s)
		}
	}
	return samples
}

func trainBlockDict(blocks []*pendingBlock, dictSize int) []byte {
	if dictSize <= 0 {
		dictSize = v3DefaultDictSize
	}
	if len(blocks) < minDictSamples || dictSize <= dictHeaderReserve {
		return nil
	}
	totalRaw := 0
	for _, b := range blocks {
		totalRaw += len(b.raw)
	}
	if totalRaw < minRawDictHistBytes {
		return nil
	}
	histCap := dictSize - dictHeaderReserve
	maxHist := min(histCap, max(totalRaw/dictHistDivisor, minRawDictHistBytes))
	if maxHist < minRawDictHistBytes {
		return nil
	}

	samples := buildDictSamples(blocks)
	if len(samples) < minDictSamples {
		return nil
	}

	for attempt := 0; attempt < preallocHalfDiv && maxHist >= minRawDictHistBytes; attempt++ {
		hist := buildStratifiedDictHistory(blocks, maxHist)
		if len(hist) < minRawDictHistBytes {
			return nil
		}
		dict, err := zstd.BuildDict(zstd.BuildDictOptions{
			ID:       1,
			History:  hist,
			Contents: samples,
			Level:    zstd.SpeedDefault,
		})
		if err != nil || len(dict) == 0 {
			return nil
		}
		if len(dict) <= dictSize {
			return dict
		}
		maxHist /= preallocHalfDiv
	}
	return nil
}

func compressBlocksMeasured(
	blocks []*pendingBlock,
	level zstd.EncoderLevel,
	dict []byte,
	workers int,
) ([][]byte, int64, error) {
	compressed := make([][]byte, len(blocks))
	if len(blocks) == 0 {
		return compressed, 0, nil
	}
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > len(blocks) {
		workers = len(blocks)
	}
	if level == 0 {
		level = zstd.SpeedBetterCompression
	}

	var (
		nextIdx uint32
		mu      sync.Mutex
		wg      sync.WaitGroup
		firstEr error
	)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			opts := []zstd.EOption{
				zstd.WithEncoderLevel(level),
				zstd.WithEncoderConcurrency(1),
			}
			if len(dict) > 0 {
				opts = append(opts, zstd.WithEncoderDict(dict))
			}
			enc, err := zstd.NewWriter(nil, opts...)
			if err != nil {
				mu.Lock()
				if firstEr == nil {
					firstEr = err
				}
				mu.Unlock()
				return
			}
			defer func() {
				_ = enc.Close()
			}()

			for {
				mu.Lock()
				idx := int(nextIdx)
				if idx < len(blocks) {
					nextIdx++
				}
				mu.Unlock()
				if idx >= len(blocks) {
					return
				}
				raw := blocks[idx].raw
				compressed[idx] = enc.EncodeAll(raw, make([]byte, 0, len(raw)/preallocHalfDiv))
			}
		}()
	}
	wg.Wait()
	if firstEr != nil {
		return nil, 0, firstEr
	}
	var totalBytes int64
	for _, c := range compressed {
		totalBytes += int64(len(c))
	}
	return compressed, totalBytes, nil
}

type v3PackBuildState struct {
	blocks       []*pendingBlock
	blockIdxByID map[[shaLen]byte]uint32
	stats        *V3PackStats
	machine      elf.Machine
}

func (s *v3PackBuildState) ingestVariant(
	lvl string,
	raw []byte,
	mode ChunkMode,
	avgSize int,
	enableFilter bool,
) (V3VariantMeta, error) {
	if len(raw) == 0 || uint64(len(raw)) > v3MaxVariantBytes {
		return V3VariantMeta{}, fmt.Errorf("%w: variant %s length %d invalid", ErrInvalidV3Container, lvl, len(raw))
	}
	probeImg, execRanges, vMachine, err := prepareVariantFilterProbe(raw, enableFilter)
	if err != nil {
		return V3VariantMeta{}, fmt.Errorf("prepare variant %s: %w", lvl, err)
	}
	if s.machine == elf.EM_NONE && vMachine != elf.EM_NONE {
		s.machine = vMachine
	} else if s.machine != elf.EM_NONE && vMachine != elf.EM_NONE && s.machine != vMachine {
		return V3VariantMeta{}, fmt.Errorf("%w: variant %s machine %v mismatches %v",
			ErrInvalidV3Container, lvl, vMachine, s.machine)
	}

	origSum := sha256.Sum256(raw)
	chunks := alignChunksToExecRanges(chunkVariantData(raw, probeImg, mode, avgSize), raw, execRanges, vMachine)
	extents := make([]V3Extent, 0, len(chunks))
	erIdx := 0
	for _, ch := range chunks {
		chunkLen := ch.End - ch.Start
		s.stats.LogicalRawBytes += int64(chunkLen) // #nosec G115 -- chunkLen bounded by variant size
		s.stats.LogicalExtentCount++

		chunkData := make([]byte, chunkLen)
		copy(chunkData, raw[ch.Start:ch.End])

		var (
			filtered      bool
			filterBasePos uint32
		)
		if er, inside := execRangeForChunk(ch, execRanges, &erIdx); inside {
			filterBasePos = uint32(ch.Start - er.Start) // #nosec G115 -- mod-2^32 section offset matching Lean spec
			if err := ApplyExtentFilterInPlace(chunkData, vMachine, filterBasePos, true); err != nil {
				return V3VariantMeta{}, fmt.Errorf("filter variant %s extent [%d,%d): %w", lvl, ch.Start, ch.End, err)
			}
			filtered = true
		}

		sum := sha256.Sum256(chunkData)
		blkIdx, exists := s.blockIdxByID[sum]
		if !exists {
			blkIdx = uint32(len(s.blocks)) // #nosec G115 -- bounded by max chunks
			s.blockIdxByID[sum] = blkIdx
			s.blocks = append(s.blocks, &pendingBlock{
				key: sum,
				raw: chunkData,
			})
			s.stats.UniqueRawBytes += int64(chunkLen) // #nosec G115 -- bounded by variant size
		} else {
			s.stats.DeduplicatedBytes += int64(chunkLen) // #nosec G115 -- bounded by variant size
		}
		extents = append(extents, V3Extent{
			BlockIdx:      blkIdx,
			VariantOffset: ch.Start,
			Length:        uint32(chunkLen), // #nosec G115 -- chunkLen <= max chunk
			Filtered:      filtered,
			FilterBasePos: filterBasePos,
		})
	}

	return V3VariantMeta{
		Level:            lvl,
		UncompressedSize: uint64(len(raw)),
		Filtered:         enableFilter && len(execRanges) > 0,
		OrigSHA256:       origSum,
		ExecRanges:       execRanges,
		Extents:          extents,
	}, nil
}

func compressBlocksWithOptionalDict(blocks []*pendingBlock, opts V3PackOptions) ([]byte, error) {
	var dict []byte
	if opts.EnableDict {
		dict = trainBlockDict(blocks, opts.DictSize)
	}
	compressedSlices, compBytes, err := compressBlocksMeasured(blocks, opts.ZstdLevel, dict, opts.ParallelEncode)
	if err != nil {
		return nil, fmt.Errorf("compress blocks: %w", err)
	}
	if len(dict) > 0 && opts.DictProfitable {
		noDictSlices, noDictBytes, noDictErr := compressBlocksMeasured(blocks, opts.ZstdLevel, nil, opts.ParallelEncode)
		if noDictErr != nil {
			return nil, fmt.Errorf("compress blocks (no-dict comparison): %w", noDictErr)
		}
		if int64(len(dict))+compBytes >= noDictBytes {
			dict = nil
			compressedSlices = noDictSlices
		}
	}
	for i, c := range compressedSlices {
		blocks[i].compressed = c
	}
	return dict, nil
}

// PackV3 builds a deduplicated, optionally chunk-local filtered Format v3 archive on w and returns
// both parsed metadata and detailed compression metrics.
func PackV3(w io.Writer, opts V3PackOptions) (*V3Archive, *V3PackStats, error) {
	if len(opts.Variants) == 0 {
		return nil, nil, fmt.Errorf("%w: no variants supplied", ErrInvalidV3Container)
	}
	mode := opts.ChunkMode
	if mode == "" {
		mode = ChunkSectionCDC
	}
	avgSize := opts.AvgChunkSize
	if avgSize <= 0 {
		avgSize = v3DefaultChunkSize
	}

	levels := make([]string, 0, len(opts.Variants))
	for lvl := range opts.Variants {
		levels = append(levels, lvl)
	}
	sort.Strings(levels)

	state := &v3PackBuildState{
		blockIdxByID: make(map[[shaLen]byte]uint32),
		stats:        &V3PackStats{},
	}
	variantMetas := make([]V3VariantMeta, 0, len(levels))
	for _, lvl := range levels {
		meta, err := state.ingestVariant(lvl, opts.Variants[lvl], mode, avgSize, opts.EnableBCJRIP)
		if err != nil {
			return nil, nil, err
		}
		variantMetas = append(variantMetas, meta)
	}
	state.stats.UniqueBlockCount = len(state.blocks)

	dict, err := compressBlocksWithOptionalDict(state.blocks, opts)
	if err != nil {
		return nil, nil, err
	}
	blocks := state.blocks
	stats := state.stats
	machine := state.machine

	if _, err := w.Write([]byte(V3ContainerMagic)); err != nil {
		return nil, nil, fmt.Errorf("write v3 magic: %w", err)
	}
	curOff := uint64(v3HeaderLen)

	arch := &V3Archive{
		TargetArch:   opts.TargetArch,
		Machine:      uint16(machine), // #nosec G115 -- elf.Machine fits uint16
		HasDict:      len(dict) > 0,
		Blocks:       make([]V3BlockMeta, len(blocks)),
		Variants:     variantMetas,
		variantByLvl: make(map[string]int, len(variantMetas)),
	}
	for i, v := range arch.Variants {
		arch.variantByLvl[v.Level] = i
	}

	if len(dict) > 0 {
		if _, err := w.Write(dict); err != nil {
			return nil, nil, fmt.Errorf("write v3 dict: %w", err)
		}
		arch.DictOffset = curOff
		arch.DictSize = uint32(len(dict)) // #nosec G115 -- bounded by v3DefaultDictSize
		arch.DictSHA256 = sha256.Sum256(dict)
		curOff += uint64(len(dict))
		stats.DictBytes = int64(len(dict))
	}

	for i, blk := range blocks {
		if _, err := w.Write(blk.compressed); err != nil {
			return nil, nil, fmt.Errorf("write block %d: %w", i, err)
		}
		compLen := uint32(len(blk.compressed)) // #nosec G115 -- bounded by chunk max
		rawLen := uint32(len(blk.raw))         // #nosec G115 -- bounded by chunk max
		arch.Blocks[i] = V3BlockMeta{
			Offset:         curOff,
			CompressedSize: compLen,
			RawSize:        rawLen,
			SHA256:         blk.key,
		}
		curOff += uint64(compLen)
		stats.CompressedPayload += int64(compLen)
	}

	arch.IndexOffset = curOff
	idxBytes, err := MarshalV3Index(arch)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal v3 index: %w", err)
	}
	if _, err := w.Write(idxBytes); err != nil {
		return nil, nil, fmt.Errorf("write v3 index: %w", err)
	}
	stats.IndexBytes = int64(len(idxBytes))

	var trailer [v3TrailerSize]byte
	idxSize := uint64(len(idxBytes))
	idxSum := sha256.Sum256(idxBytes)
	binary.LittleEndian.PutUint64(trailer[v3TrailerIdxOff:v3TrailerSizeOff], arch.IndexOffset)
	binary.LittleEndian.PutUint64(trailer[v3TrailerSizeOff:v3TrailerHashOff], idxSize)
	copy(trailer[v3TrailerHashOff:v3TrailerMagicOff], idxSum[:])
	copy(trailer[v3TrailerMagicOff:v3TrailerSize], V3TrailerMagic)
	if _, err := w.Write(trailer[:]); err != nil {
		return nil, nil, fmt.Errorf("write v3 trailer: %w", err)
	}

	total := curOff + idxSize + v3TrailerSize
	stats.TotalArchiveBytes = int64(total) // #nosec G115 -- bounded total file size
	return arch, stats, nil
}

// MarshalV3Index deterministically encodes Format v3 metadata in compact binary format.
func MarshalV3Index(arch *V3Archive) ([]byte, error) {
	if arch == nil || len(arch.Variants) == 0 || len(arch.Blocks) == 0 {
		return nil, fmt.Errorf("%w: empty v3 index", ErrInvalidV3Container)
	}
	var buf bytes.Buffer
	buf.WriteString(V3IndexMagic)
	if len(arch.TargetArch) > int(^uint8(0)) {
		return nil, fmt.Errorf("%w: target arch too long", ErrInvalidV3Container)
	}
	buf.WriteByte(byte(len(arch.TargetArch))) // #nosec G115 -- checked <= MaxUint8 above
	buf.WriteString(arch.TargetArch)

	var tmp2 [u16Bytes]byte
	var tmp4 [u32Bytes]byte
	var tmp8 [u64Bytes]byte
	binary.LittleEndian.PutUint16(tmp2[:], arch.Machine)
	buf.Write(tmp2[:])

	var flags byte
	if arch.HasDict {
		flags |= v3FlagHasDict
	}
	buf.WriteByte(flags)
	if arch.HasDict {
		binary.LittleEndian.PutUint64(tmp8[:], arch.DictOffset)
		buf.Write(tmp8[:])
		binary.LittleEndian.PutUint32(tmp4[:], arch.DictSize)
		buf.Write(tmp4[:])
		buf.Write(arch.DictSHA256[:])
	}

	binary.LittleEndian.PutUint32(tmp4[:], uint32(len(arch.Blocks))) // #nosec G115 -- bounded by max variants/chunks
	buf.Write(tmp4[:])
	for _, b := range arch.Blocks {
		binary.LittleEndian.PutUint64(tmp8[:], b.Offset)
		buf.Write(tmp8[:])
		binary.LittleEndian.PutUint32(tmp4[:], b.CompressedSize)
		buf.Write(tmp4[:])
		binary.LittleEndian.PutUint32(tmp4[:], b.RawSize)
		buf.Write(tmp4[:])
		buf.Write(b.SHA256[:])
	}

	binary.LittleEndian.PutUint32(tmp4[:], uint32(len(arch.Variants))) // #nosec G115 -- bounded variants count
	buf.Write(tmp4[:])
	for _, v := range arch.Variants {
		if len(v.Level) > int(^uint8(0)) {
			return nil, fmt.Errorf("%w: variant level too long", ErrInvalidV3Container)
		}
		buf.WriteByte(byte(len(v.Level))) // #nosec G115 -- checked <= MaxUint8 above
		buf.WriteString(v.Level)

		var vFlags byte
		if v.Filtered {
			vFlags |= v3FlagFiltered
		}
		buf.WriteByte(vFlags)
		binary.LittleEndian.PutUint64(tmp8[:], v.UncompressedSize)
		buf.Write(tmp8[:])
		buf.Write(v.OrigSHA256[:])

		binary.LittleEndian.PutUint32(tmp4[:], uint32(len(v.ExecRanges))) // #nosec G115 -- bounded sections
		buf.Write(tmp4[:])
		for _, er := range v.ExecRanges {
			binary.LittleEndian.PutUint64(tmp8[:], er.Start)
			buf.Write(tmp8[:])
			binary.LittleEndian.PutUint64(tmp8[:], er.End)
			buf.Write(tmp8[:])
		}

		binary.LittleEndian.PutUint32(tmp4[:], uint32(len(v.Extents))) // #nosec G115 -- bounded extents
		buf.Write(tmp4[:])
		for _, ex := range v.Extents {
			binary.LittleEndian.PutUint32(tmp4[:], ex.BlockIdx)
			buf.Write(tmp4[:])
			binary.LittleEndian.PutUint64(tmp8[:], ex.VariantOffset)
			buf.Write(tmp8[:])
			binary.LittleEndian.PutUint32(tmp4[:], ex.Length)
			buf.Write(tmp4[:])
			var extFlags byte
			if ex.Filtered {
				extFlags |= extFlagFiltered
			}
			buf.WriteByte(extFlags)
			if ex.Filtered {
				binary.LittleEndian.PutUint32(tmp4[:], ex.FilterBasePos)
				buf.Write(tmp4[:])
			}
		}
	}

	if buf.Len() > v3MaxIndexSize {
		return nil, fmt.Errorf("%w: serialized index size %d exceeds %d", ErrInvalidV3Container, buf.Len(), v3MaxIndexSize)
	}
	return buf.Bytes(), nil
}

// ReadV3Archive parses and cryptographically validates the Format v3 trailer, index, and bounds.
func ReadV3Archive(r io.ReaderAt, totalSize int64) (*V3Archive, error) {
	if totalSize < v3HeaderLen+v3TrailerSize {
		return nil, fmt.Errorf("%w: file too small (%d)", ErrInvalidV3Container, totalSize)
	}
	var hdr [v3HeaderLen]byte
	if _, err := r.ReadAt(hdr[:], 0); err != nil {
		return nil, fmt.Errorf("read v3 header: %w", err)
	}
	if string(hdr[:]) != V3ContainerMagic {
		return nil, fmt.Errorf("%w: invalid v3 container header magic", ErrInvalidV3Container)
	}

	var trailer [v3TrailerSize]byte
	trailerOff := totalSize - v3TrailerSize
	if _, err := r.ReadAt(trailer[:], trailerOff); err != nil {
		return nil, fmt.Errorf("read v3 trailer: %w", err)
	}
	if string(trailer[v3TrailerMagicOff:v3TrailerSize]) != V3TrailerMagic {
		return nil, fmt.Errorf("%w: invalid v3 trailer magic", ErrInvalidV3Container)
	}

	indexOff := binary.LittleEndian.Uint64(trailer[v3TrailerIdxOff:v3TrailerSizeOff])
	indexSize := binary.LittleEndian.Uint64(trailer[v3TrailerSizeOff:v3TrailerHashOff])
	tOffU := uint64(trailerOff) // #nosec G115 -- trailerOff >= v3HeaderLen
	if indexOff < v3HeaderLen || indexOff >= tOffU || indexSize == 0 || indexSize > v3MaxIndexSize || indexSize != tOffU-indexOff {
		return nil, fmt.Errorf("%w: invalid index offset %d size %d", ErrInvalidV3Container, indexOff, indexSize)
	}

	idxBytes := make([]byte, indexSize)
	if _, err := r.ReadAt(idxBytes, int64(indexOff)); err != nil { // #nosec G115 -- indexOff < trailerOff < MaxInt64
		return nil, fmt.Errorf("read v3 index: %w", err)
	}
	actual := sha256.Sum256(idxBytes)
	if !bytes.Equal(actual[:], trailer[v3TrailerHashOff:v3TrailerMagicOff]) {
		return nil, fmt.Errorf("%w: index SHA-256 mismatch", ErrInvalidV3Container)
	}
	return UnmarshalV3Index(idxBytes, indexOff)
}

func readBytesChecked(data []byte, off *int, n int) ([]byte, error) {
	if n < 0 || *off+n > len(data) {
		return nil, fmt.Errorf("%w: truncated index", ErrInvalidV3Container)
	}
	s := data[*off : *off+n]
	*off += n
	return s, nil
}

func readByteChecked(data []byte, off *int) (byte, error) {
	b, err := readBytesChecked(data, off, 1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func readU16Checked(data []byte, off *int) (uint16, error) {
	b, err := readBytesChecked(data, off, u16Bytes)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b), nil
}

func readU32Checked(data []byte, off *int) (uint32, error) {
	b, err := readBytesChecked(data, off, u32Bytes)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func readU64Checked(data []byte, off *int) (uint64, error) {
	b, err := readBytesChecked(data, off, u64Bytes)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

func parseV3Dict(data []byte, off *int, arch *V3Archive, minDataOff, indexOff uint64) (uint64, error) {
	if !arch.HasDict {
		return minDataOff, nil
	}
	var err error
	if arch.DictOffset, err = readU64Checked(data, off); err != nil {
		return 0, err
	}
	if arch.DictSize, err = readU32Checked(data, off); err != nil {
		return 0, err
	}
	dHash, err := readBytesChecked(data, off, shaLen)
	if err != nil {
		return 0, err
	}
	copy(arch.DictSHA256[:], dHash)
	if arch.DictSize == 0 || arch.DictOffset < minDataOff || arch.DictOffset > indexOff || uint64(arch.DictSize) > indexOff-arch.DictOffset {
		return 0, fmt.Errorf("%w: dict bounds invalid", ErrInvalidV3Container)
	}
	return arch.DictOffset + uint64(arch.DictSize), nil
}

func parseV3Blocks(data []byte, off *int, arch *V3Archive, minDataOff, indexOff uint64) error {
	blockCount, err := readU32Checked(data, off)
	if err != nil {
		return err
	}
	if blockCount == 0 {
		return fmt.Errorf("%w: zero blocks", ErrInvalidV3Container)
	}
	arch.Blocks = make([]V3BlockMeta, blockCount)
	lastEnd := minDataOff
	for i := range int(blockCount) {
		var blk V3BlockMeta
		if blk.Offset, err = readU64Checked(data, off); err != nil {
			return err
		}
		if blk.CompressedSize, err = readU32Checked(data, off); err != nil {
			return err
		}
		if blk.RawSize, err = readU32Checked(data, off); err != nil {
			return err
		}
		bHash, hErr := readBytesChecked(data, off, shaLen)
		if hErr != nil {
			return hErr
		}
		copy(blk.SHA256[:], bHash)
		if blk.CompressedSize == 0 || blk.RawSize == 0 || blk.Offset < lastEnd ||
			blk.Offset > indexOff || uint64(blk.CompressedSize) > indexOff-blk.Offset {
			return fmt.Errorf("%w: invalid block %d bounds", ErrInvalidV3Container, i)
		}
		lastEnd = blk.Offset + uint64(blk.CompressedSize)
		arch.Blocks[i] = blk
	}
	return nil
}

// UnmarshalV3Index parses a canonical Format v3 metadata table and rejects trailing bytes or out-of-bounds blocks.
func UnmarshalV3Index(data []byte, indexOff uint64) (*V3Archive, error) {
	off := 0
	magic, err := readBytesChecked(data, &off, len(V3IndexMagic))
	if err != nil {
		return nil, err
	}
	if string(magic) != V3IndexMagic {
		return nil, fmt.Errorf("%w: bad index magic", ErrInvalidV3Container)
	}

	archLen, err := readByteChecked(data, &off)
	if err != nil {
		return nil, err
	}
	archBytes, err := readBytesChecked(data, &off, int(archLen))
	if err != nil {
		return nil, err
	}
	machine, err := readU16Checked(data, &off)
	if err != nil {
		return nil, err
	}
	flags, err := readByteChecked(data, &off)
	if err != nil {
		return nil, err
	}

	arch := &V3Archive{
		TargetArch:   string(archBytes),
		Machine:      machine,
		IndexOffset:  indexOff,
		HasDict:      (flags & v3FlagHasDict) != 0,
		variantByLvl: make(map[string]int),
	}

	minDataOff, err := parseV3Dict(data, &off, arch, uint64(v3HeaderLen), indexOff)
	if err != nil {
		return nil, err
	}
	if err := parseV3Blocks(data, &off, arch, minDataOff, indexOff); err != nil {
		return nil, err
	}
	if err := parseV3Variants(data, &off, arch, uint32(len(arch.Blocks))); err != nil { // #nosec G115 -- len matches parsed uint32
		return nil, err
	}
	if off != len(data) {
		return nil, fmt.Errorf("%w: trailing bytes (%d)", ErrInvalidV3Container, len(data)-off)
	}
	return arch, nil
}

func parseV3ExecRanges(data []byte, off *int, uSize uint64) ([]SectionRange, error) {
	rangeCount, err := readU32Checked(data, off)
	if err != nil {
		return nil, err
	}
	ranges := make([]SectionRange, rangeCount)
	for ri := range int(rangeCount) {
		rs, rErr := readU64Checked(data, off)
		if rErr != nil {
			return nil, rErr
		}
		re, rErr := readU64Checked(data, off)
		if rErr != nil {
			return nil, rErr
		}
		if re <= rs || re > uSize {
			return nil, fmt.Errorf("%w: invalid exec range", ErrInvalidV3Container)
		}
		ranges[ri] = SectionRange{Start: rs, End: re}
	}
	return ranges, nil
}

func parseV3Extents(data []byte, off *int, arch *V3Archive, blockCount uint32, uSize uint64, level string) ([]V3Extent, error) {
	extCount, err := readU32Checked(data, off)
	if err != nil {
		return nil, err
	}
	machine := elf.Machine(arch.Machine)
	extents := make([]V3Extent, extCount)
	var expectedOff uint64
	for ei := range int(extCount) {
		bIdx, eErr := readU32Checked(data, off)
		if eErr != nil {
			return nil, eErr
		}
		vOff, eErr := readU64Checked(data, off)
		if eErr != nil {
			return nil, eErr
		}
		eLen, eErr := readU32Checked(data, off)
		if eErr != nil {
			return nil, eErr
		}
		extFlags, eErr := readByteChecked(data, off)
		if eErr != nil {
			return nil, eErr
		}
		if (extFlags & ^extFlagFiltered) != 0 {
			return nil, fmt.Errorf("%w: unsupported extent flags 0x%02x in %s", ErrInvalidV3Container, extFlags, level)
		}
		filtered := (extFlags & extFlagFiltered) != 0
		var filterBasePos uint32
		if filtered {
			if machine != elf.EM_X86_64 && machine != elf.EM_AARCH64 {
				return nil, fmt.Errorf("%w: filtered extent in %s with unsupported machine %v", ErrInvalidV3Container, level, machine)
			}
			filterBasePos, eErr = readU32Checked(data, off)
			if eErr != nil {
				return nil, eErr
			}
		}
		if bIdx >= blockCount || vOff != expectedOff || uint64(eLen) != uint64(arch.Blocks[bIdx].RawSize) || uint64(eLen) > uSize-vOff {
			return nil, fmt.Errorf("%w: invalid variant %s extent %d", ErrInvalidV3Container, level, ei)
		}
		expectedOff += uint64(eLen)
		extents[ei] = V3Extent{
			BlockIdx:      bIdx,
			VariantOffset: vOff,
			Length:        eLen,
			Filtered:      filtered,
			FilterBasePos: filterBasePos,
		}
	}
	if uSize == 0 || uSize > v3MaxVariantBytes || expectedOff != uSize {
		return nil, fmt.Errorf("%w: variant %s extent coverage mismatch", ErrInvalidV3Container, level)
	}
	return extents, nil
}

func parseV3Variants(data []byte, off *int, arch *V3Archive, blockCount uint32) error {
	varCount, err := readU32Checked(data, off)
	if err != nil {
		return err
	}
	if varCount == 0 {
		return fmt.Errorf("%w: zero variants", ErrInvalidV3Container)
	}
	arch.Variants = make([]V3VariantMeta, varCount)
	for vi := range int(varCount) {
		lvlLen, err := readByteChecked(data, off)
		if err != nil {
			return err
		}
		lvlBytes, err := readBytesChecked(data, off, int(lvlLen))
		if err != nil {
			return err
		}
		vFlags, err := readByteChecked(data, off)
		if err != nil {
			return err
		}
		uSize, err := readU64Checked(data, off)
		if err != nil {
			return err
		}
		origSumBytes, err := readBytesChecked(data, off, shaLen)
		if err != nil {
			return err
		}
		ranges, err := parseV3ExecRanges(data, off, uSize)
		if err != nil {
			return err
		}
		lvl := string(lvlBytes)
		extents, err := parseV3Extents(data, off, arch, blockCount, uSize, lvl)
		if err != nil {
			return err
		}
		var origSum [shaLen]byte
		copy(origSum[:], origSumBytes)
		arch.Variants[vi] = V3VariantMeta{
			Level:            lvl,
			UncompressedSize: uSize,
			Filtered:         (vFlags & v3FlagFiltered) != 0,
			OrigSHA256:       origSum,
			ExecRanges:       ranges,
			Extents:          extents,
		}
		arch.variantByLvl[lvl] = vi
	}
	return nil
}

type uniqueBlockWork struct {
	blockIdx uint32
	extents  []V3Extent
}

func readV3Dict(arch *V3Archive, r io.ReaderAt) ([]byte, error) {
	if !arch.HasDict {
		return nil, nil
	}
	dictBytes := make([]byte, arch.DictSize)
	if _, err := r.ReadAt(dictBytes, int64(arch.DictOffset)); err != nil { // #nosec G115 -- validated < indexOff
		return nil, fmt.Errorf("read v3 dict: %w", err)
	}
	if sha256.Sum256(dictBytes) != arch.DictSHA256 {
		return nil, fmt.Errorf("%w: dict SHA-256 mismatch", ErrInvalidV3Container)
	}
	return dictBytes, nil
}

func emitDecodedJobExtents(
	job uniqueBlockWork,
	rawBuf []byte,
	unfilterBuf *[]byte,
	machine elf.Machine,
	emit func(offset uint64, data []byte) error,
) error {
	singleUse := len(job.extents) == 1
	for _, ex := range job.extents {
		payload := rawBuf
		if ex.Filtered {
			if !singleUse {
				if cap(*unfilterBuf) < len(rawBuf) {
					*unfilterBuf = make([]byte, len(rawBuf))
				} else {
					*unfilterBuf = (*unfilterBuf)[:len(rawBuf)]
				}
				copy(*unfilterBuf, rawBuf)
				payload = *unfilterBuf
			}
			if uErr := ApplyExtentFilterInPlace(payload, machine, ex.FilterBasePos, false); uErr != nil {
				return uErr
			}
		}
		if err := emit(ex.VariantOffset, payload); err != nil {
			return err
		}
	}
	return nil
}

// decodeBlocksParallel decompresses every needed unique block in parallel and invokes emit(offset, plain).
func decodeBlocksParallel(
	arch *V3Archive,
	r io.ReaderAt,
	v *V3VariantMeta,
	workers int,
	emit func(offset uint64, data []byte) error,
) error {
	dictBytes, err := readV3Dict(arch, r)
	if err != nil {
		return err
	}

	workIdxByBlock := make(map[uint32]int, len(v.Extents))
	var jobs []uniqueBlockWork
	for _, ex := range v.Extents {
		pos, ok := workIdxByBlock[ex.BlockIdx]
		if !ok {
			pos = len(jobs)
			workIdxByBlock[ex.BlockIdx] = pos
			jobs = append(jobs, uniqueBlockWork{blockIdx: ex.BlockIdx})
		}
		jobs[pos].extents = append(jobs[pos].extents, ex)
	}

	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}

	var (
		nextJob int
		mu      sync.Mutex
		wg      sync.WaitGroup
		firstEr error
	)
	setErr := func(err error) {
		mu.Lock()
		if firstEr == nil {
			firstEr = err
		}
		mu.Unlock()
	}

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			opts := []zstd.DOption{zstd.WithDecoderConcurrency(1)}
			if len(dictBytes) > 0 {
				opts = append(opts, zstd.WithDecoderDicts(dictBytes))
			}
			dec, err := zstd.NewReader(nil, opts...)
			if err != nil {
				setErr(err)
				return
			}
			defer dec.Close()

			var compBuf []byte
			var rawBuf []byte
			var unfilterBuf []byte
			machine := elf.Machine(arch.Machine)
			for {
				mu.Lock()
				jIdx := nextJob
				if firstEr != nil {
					mu.Unlock()
					return
				}
				if jIdx < len(jobs) {
					nextJob++
				}
				mu.Unlock()
				if jIdx >= len(jobs) {
					return
				}

				job := jobs[jIdx]
				blk := arch.Blocks[job.blockIdx]
				if cap(compBuf) < int(blk.CompressedSize) {
					compBuf = make([]byte, blk.CompressedSize)
				} else {
					compBuf = compBuf[:blk.CompressedSize]
				}
				if _, err := r.ReadAt(compBuf, int64(blk.Offset)); err != nil { // #nosec G115 -- validated < indexOff
					setErr(err)
					return
				}
				rawBuf, err = dec.DecodeAll(compBuf, rawBuf[:0])
				if err != nil {
					setErr(err)
					return
				}
				if uint32(len(rawBuf)) != blk.RawSize || sha256.Sum256(rawBuf) != blk.SHA256 { // #nosec G115 -- verified equality
					setErr(fmt.Errorf("%w: block %d digest or length mismatch", ErrInvalidV3Container, job.blockIdx))
					return
				}
				if err := emitDecodedJobExtents(job, rawBuf, &unfilterBuf, machine, emit); err != nil {
					setErr(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	return firstEr
}

// DecodeVariantBytes reconstructs, unfilters per-extent, and SHA-256 verifies a variant into a caller buffer.
func DecodeVariantBytes(arch *V3Archive, r io.ReaderAt, level string, workers int) ([]byte, error) {
	if arch == nil {
		return nil, ErrInvalidV3Container
	}
	idx, ok := arch.variantByLvl[level]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrVariantNotFound, level)
	}
	v := &arch.Variants[idx]
	out := make([]byte, v.UncompressedSize)

	err := decodeBlocksParallel(arch, r, v, workers, func(offset uint64, data []byte) error {
		copy(out[offset:offset+uint64(len(data))], data)
		return nil
	})
	if err != nil {
		return nil, err
	}

	if sha256.Sum256(out) != v.OrigSHA256 {
		return nil, fmt.Errorf("%w: reconstructed SHA-256 mismatch for %s", ErrInvalidV3Container, level)
	}
	return out, nil
}
