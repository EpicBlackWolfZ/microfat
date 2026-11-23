// Package compress hosts isolated Format v3 compression and filter prototypes.
package compress

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

const (
	x86OpCall         byte = 0xE8
	x86OpJmp          byte = 0xE9
	x86OpEsc0F        byte = 0x0F
	x86JccMask        byte = 0xF0
	x86JccTag         byte = 0x80
	x86RexW           byte = 0x48
	x86RexWR          byte = 0x4C
	x86OpLea          byte = 0x8D
	x86OpMovLoad      byte = 0x8B
	x86OpMovStore     byte = 0x89
	x86ModRMNoIndex   byte = 0xC7
	x86ModRMRIPRelTag byte = 0x05

	x86Rel8InsnLen   = 5
	x86JccInsnLen    = 6
	x86RipInsnLen    = 7
	x86ImmOffset1    = 1
	x86ImmOffset2    = 2
	x86ImmOffset3    = 3
	x86ModRMOffset   = 2
	imm32ByteWidth   = 4
	arm64InsnLen     = 4
	arm64PageShift   = 12
	arm64WordShift   = 2
	arm64LoShift     = 29
	arm64HiShift     = 5
	arm64ImmHiBits   = 19
	arm64Imm26Mask   = uint32(0x03FFFFFF)
	arm64Op26Mask    = uint32(0xFC000000)
	arm64OpBL        = uint32(0x94000000)
	arm64OpB         = uint32(0x14000000)
	arm64ADRPCheck   = uint32(0x9F000000)
	arm64ADRPPattern = uint32(0x90000000)
	arm64ADRPKeep    = uint32(0x9F00001F)
	arm64Imm21Mask   = uint32(0x001FFFFF)
	arm64ImmLoMask   = uint32(0x3)
	arm64ImmHiMask   = uint32(0x7FFFF)
	adrpLoBitCount   = 2
)

// ErrInvalidELFRange indicates an executable section range lies outside the ELF buffer.
var ErrInvalidELFRange = errors.New("invalid ELF executable section range")

// SectionRange identifies a half-open byte interval [Start, End) inside an ELF image.
type SectionRange struct {
	Start uint64
	End   uint64
}

func shiftImm32LE(dst []byte, pos uint32, encode bool) {
	v := binary.LittleEndian.Uint32(dst)
	if encode {
		v += pos
	} else {
		v -= pos
	}
	binary.LittleEndian.PutUint32(dst, v)
}

func isX86Rel8Opcode(b byte) bool {
	return b == x86OpCall || b == x86OpJmp
}

func isX86RexPrefix(b byte) bool {
	return b == x86RexW || b == x86RexWR
}

func isX86RipOpCode(b byte) bool {
	return b == x86OpLea || b == x86OpMovLoad || b == x86OpMovStore
}

// TransformX86_64 applies the reversible x86-64 BCJ+RIP filter matching Lean `Microfat.ElfFilter.x86Transform`.
func TransformX86_64(data []byte, basePos uint32, encode bool) []byte {
	if len(data) == 0 {
		if data == nil {
			return nil
		}
		return []byte{}
	}
	out := make([]byte, len(data))
	copy(out, data)
	TransformX86_64InPlace(out, basePos, encode)
	return out
}

func x86StepImm(buf []byte, i int) (step, immOff int) {
	n := len(buf)
	b0 := buf[i]
	if isX86Rel8Opcode(b0) && i+x86Rel8InsnLen <= n {
		return x86Rel8InsnLen, x86ImmOffset1
	}
	if b0 == x86OpEsc0F && i+x86JccInsnLen <= n && (buf[i+x86ImmOffset1]&x86JccMask) == x86JccTag {
		return x86JccInsnLen, x86ImmOffset2
	}
	if isX86RexPrefix(b0) && i+x86RipInsnLen <= n &&
		isX86RipOpCode(buf[i+x86ImmOffset1]) &&
		(buf[i+x86ModRMOffset]&x86ModRMNoIndex) == x86ModRMRIPRelTag {
		return x86RipInsnLen, x86ImmOffset3
	}
	return 1, 0
}

// TransformX86_64InPlace transforms an x86-64 code buffer in place using BCJ+RIP address normalization.
func TransformX86_64InPlace(buf []byte, basePos uint32, encode bool) {
	n := len(buf)
	for i := 0; i < n; {
		step, immOff := x86StepImm(buf, i)
		if immOff > 0 {
			pos := basePos + uint32(i+step) // #nosec G115 -- deliberate mod-2^32 PC arithmetic matching Lean spec
			shiftImm32LE(buf[i+immOff:i+step], pos, encode)
		}
		i += step
	}
}

// SnapX86FilterCuts advances interior cut offsets within section onto exact x86-64 BCJ+RIP instruction-step boundaries.
func SnapX86FilterCuts(section []byte, relCuts []uint64) []uint64 {
	n := uint64(len(section))
	if n == 0 || len(relCuts) == 0 {
		return nil
	}
	sorted := make([]uint64, 0, len(relCuts))
	for _, c := range relCuts {
		if c > 0 && c < n {
			sorted = append(sorted, c)
		}
	}
	if len(sorted) == 0 {
		return nil
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	out := make([]uint64, 0, len(sorted))
	bufLen := len(section)
	k := 0
	for i := 0; i < bufLen && k < len(sorted); {
		step, _ := x86StepImm(section, i)
		start := uint64(i)      // #nosec G115 -- non-negative slice index
		end := uint64(i + step) // #nosec G115 -- non-negative slice index
		for k < len(sorted) && sorted[k] <= start {
			k++
		}
		emitted := false
		for k < len(sorted) && sorted[k] <= end {
			if !emitted && end < n {
				out = append(out, end)
				emitted = true
			}
			k++
		}
		i += step
	}
	return out
}

// FilterX86_64 encodes x86-64 PC-relative instructions into section-relative constants.
func FilterX86_64(data []byte, basePos uint32) []byte {
	return TransformX86_64(data, basePos, true)
}

// UnfilterX86_64 decodes BCJ+RIP-filtered x86-64 bytes back to their exact original encoding.
func UnfilterX86_64(data []byte, basePos uint32) []byte {
	return TransformX86_64(data, basePos, false)
}

func arm64StepWord(encode bool, pos, insn uint32) uint32 {
	top6 := insn & arm64Op26Mask
	if top6 == arm64OpBL || top6 == arm64OpB {
		imm26 := insn & arm64Imm26Mask
		pcWords := pos >> arm64WordShift
		var nextImm uint32
		if encode {
			nextImm = (imm26 + pcWords) & arm64Imm26Mask
		} else {
			nextImm = (imm26 - pcWords) & arm64Imm26Mask
		}
		return top6 | nextImm
	}
	if (insn & arm64ADRPCheck) == arm64ADRPPattern {
		immlo := (insn >> arm64LoShift) & arm64ImmLoMask
		immhi := (insn >> arm64HiShift) & arm64ImmHiMask
		imm21 := (immhi << adrpLoBitCount) | immlo
		pcPages := pos >> arm64PageShift
		var nextImm21 uint32
		if encode {
			nextImm21 = (imm21 + pcPages) & arm64Imm21Mask
		} else {
			nextImm21 = (imm21 - pcPages) & arm64Imm21Mask
		}
		nextLo := nextImm21 & arm64ImmLoMask
		nextHi := (nextImm21 >> adrpLoBitCount) & arm64ImmHiMask
		return (insn & arm64ADRPKeep) | (nextLo << arm64LoShift) | (nextHi << arm64HiShift)
	}
	return insn
}

// TransformARM64 applies the reversible ARM64 (BL, B, ADRP) filter matching Lean `Microfat.ElfFilter.arm64Transform`.
func TransformARM64(data []byte, basePos uint32, encode bool) []byte {
	if len(data) == 0 {
		if data == nil {
			return nil
		}
		return []byte{}
	}
	out := make([]byte, len(data))
	copy(out, data)
	TransformARM64InPlace(out, basePos, encode)
	return out
}

// TransformARM64InPlace transforms an aligned ARM64 code buffer in place.
func TransformARM64InPlace(buf []byte, basePos uint32, encode bool) {
	limit := len(buf) - (len(buf) % arm64InsnLen)
	for i := 0; i < limit; i += arm64InsnLen {
		pos := basePos + uint32(i) // #nosec G115 -- deliberate mod-2^32 PC arithmetic matching Lean spec
		insn := binary.LittleEndian.Uint32(buf[i : i+arm64InsnLen])
		next := arm64StepWord(encode, pos, insn)
		binary.LittleEndian.PutUint32(buf[i:i+arm64InsnLen], next)
	}
}

// FilterARM64 encodes ARM64 BL/B/ADRP immediates into page/word-normalized constants.
func FilterARM64(data []byte, basePos uint32) []byte {
	return TransformARM64(data, basePos, true)
}

// UnfilterARM64 decodes ARM64 BL/B/ADRP-filtered bytes back to their exact original encoding.
func UnfilterARM64(data []byte, basePos uint32) []byte {
	return TransformARM64(data, basePos, false)
}

// ExecutableRangesELF parses file-backed SHF_EXECINSTR section intervals from an ELF image.
func ExecutableRangesELF(elfBytes []byte) ([]SectionRange, elf.Machine, error) {
	ef, err := elf.NewFile(bytes.NewReader(elfBytes))
	if err != nil {
		return nil, elf.EM_NONE, fmt.Errorf("parse ELF executable ranges: %w", err)
	}
	defer func() {
		_ = ef.Close()
	}()

	totalLen := uint64(len(elfBytes))
	var ranges []SectionRange
	for _, s := range ef.Sections {
		if s.Type == elf.SHT_NOBITS || (s.Flags&elf.SHF_EXECINSTR) == 0 || s.Size == 0 {
			continue
		}
		if s.Offset > totalLen || s.Size > totalLen-s.Offset {
			return nil, ef.Machine, fmt.Errorf("%w: section %s [%d,+%d) outside %d",
				ErrInvalidELFRange, s.Name, s.Offset, s.Size, totalLen)
		}
		ranges = append(ranges, SectionRange{Start: s.Offset, End: s.Offset + s.Size})
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Start == ranges[j].Start {
			return ranges[i].End < ranges[j].End
		}
		return ranges[i].Start < ranges[j].Start
	})
	merged := make([]SectionRange, 0, len(ranges))
	for _, r := range ranges {
		if len(merged) == 0 {
			merged = append(merged, r)
			continue
		}
		last := &merged[len(merged)-1]
		if r.Start < last.End {
			return nil, ef.Machine, fmt.Errorf("%w: overlapping executable ranges [%d,%d) and [%d,%d)",
				ErrInvalidELFRange, last.Start, last.End, r.Start, r.End)
		}
		if r.Start == last.End {
			last.End = r.End
			continue
		}
		merged = append(merged, r)
	}
	return merged, ef.Machine, nil
}

// ApplyExtentFilterInPlace transforms one contiguous code extent at section-relative basePos in place.
func ApplyExtentFilterInPlace(buf []byte, machine elf.Machine, basePos uint32, encode bool) error {
	switch machine {
	case elf.EM_X86_64:
		TransformX86_64InPlace(buf, basePos, encode)
		return nil
	case elf.EM_AARCH64:
		TransformARM64InPlace(buf, basePos, encode)
		return nil
	default:
		return fmt.Errorf("%w: unsupported ELF machine %v", ErrInvalidELFRange, machine)
	}
}

// ApplyELFFilter transforms executable section ranges in place according to target machine ISA.
func ApplyELFFilter(buf []byte, ranges []SectionRange, machine elf.Machine, encode bool) error {
	totalLen := uint64(len(buf))
	for _, r := range ranges {
		if r.End <= r.Start || r.End > totalLen {
			return fmt.Errorf("%w: range [%d,%d) exceeds buffer %d", ErrInvalidELFRange, r.Start, r.End, totalLen)
		}
		if err := ApplyExtentFilterInPlace(buf[r.Start:r.End], machine, 0, encode); err != nil {
			return err
		}
	}
	return nil
}
