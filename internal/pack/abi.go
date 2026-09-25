package pack

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// ABI parser and inspection resource budgets.
const (
	MaxProgramHeaders              = 4096
	MaxSectionHeaders              = 65536
	MaxInterpreterSize             = 4 * 1024 // 4 KiB
	MaxDynamicEntries              = 65536
	MaxABIStringBytesPerInput      = 8 * 1024 * 1024 // 8 MiB
	MaxVersionRecordsPerInput      = 65536
	MaxMetadataBytesPerInput       = 32 * 1024 * 1024  // 32 MiB
	MaxArtifactMetadataBytes       = 256 * 1024 * 1024 // 256 MiB
	MaxArtifactReportBytes         = 64 * 1024 * 1024  // 64 MiB
	minELFHeaderSize               = 64
	minProgramHeaderSize           = 56
	minSectionHeaderSize           = 64
	dynamicEntrySize               = 16
	verneedEntrySize               = 16
	vernauxEntrySize               = 16
	versionReportSeparatorOverhead = 2
)

// Error categories for ABI inspection and policy comparison.
var (
	ErrABIMetadata      = errors.New("invalid or malformed ELF ABI metadata")
	ErrABIResourceLimit = errors.New("ELF ABI resource budget exceeded")
	ErrABIMismatch      = errors.New("declared ABI requirements mismatch between variants")
)

// LinkageForm describes the observed ELF execution linkage form.
type LinkageForm string

const (
	LinkageStatic    LinkageForm = "static"
	LinkageStaticPIE LinkageForm = "static-pie"
	LinkageDynamic   LinkageForm = "dynamic"
	LinkageAmbiguous LinkageForm = "ambiguous"
)

// Completeness represents metadata completeness status.
type Completeness string

const (
	MetadataComplete    Completeness = "complete"
	MetadataAbsent      Completeness = "absent"
	MetadataPartial     Completeness = "partial"
	MetadataUnsupported Completeness = "unsupported"
	MetadataSkipped     Completeness = "skipped"
)

// ComparisonStatus represents aggregate comparison uncertainty status.
type ComparisonStatus string

const (
	ComparisonConsistent   ComparisonStatus = "consistent"
	ComparisonInconsistent ComparisonStatus = "inconsistent"
	ComparisonUnknown      ComparisonStatus = "unknown"
	ComparisonSkipped      ComparisonStatus = "skipped"
)

// VersionRequirement represents a declared GNU symbol version requirement tuple.
type VersionRequirement struct {
	Library string `json:"library"`
	Version string `json:"version"`
	Flags   uint16 `json:"flags,omitempty"`
}

// VariantABIReport contains discovered ABI requirements for a single variant binary.
type VariantABIReport struct {
	Level               string               `json:"level"`
	Path                string               `json:"path"`
	Class               string               `json:"class"`
	Type                string               `json:"type"`
	Machine             string               `json:"machine"`
	Linkage             LinkageForm          `json:"linkage"`
	HasInterpreter      bool                 `json:"has_interpreter"`
	Interpreter         string               `json:"interpreter,omitempty"`
	Dependencies        []string             `json:"dependencies,omitempty"`
	VersionRequirements []VersionRequirement `json:"version_requirements,omitempty"`
	Completeness        Completeness         `json:"completeness"`
	InspectionSource    string               `json:"inspection_source"`
	Warnings            []string             `json:"warnings,omitempty"`
}

// ArtifactABIReport aggregates ABI reports across all packaged variants.
type ArtifactABIReport struct {
	TargetOS             string              `json:"target_os"`
	TargetArch           string              `json:"target_arch"`
	Variants             []*VariantABIReport `json:"variants"`
	Status               ComparisonStatus    `json:"status"`
	Consistent           bool                `json:"consistent"`
	Overridden           bool                `json:"overridden"`
	Differences          []string            `json:"differences,omitempty"`
	Warnings             []string            `json:"warnings,omitempty"`
	DeploymentDisclaimer string              `json:"deployment_disclaimer"`
}

// inputAccounting tracks resource usage during inspection of a single ELF input.
type inputAccounting struct {
	metadataBytesRead   uint64
	retainedStringBytes uint64
	versionRecords      uint64
}

func (acc *inputAccounting) chargeMetadata(n uint64) error {
	if n > MaxMetadataBytesPerInput || acc.metadataBytesRead > MaxMetadataBytesPerInput-n {
		return fmt.Errorf("%w: metadata bytes read (%d + %d) exceeds per-input budget %d",
			ErrABIResourceLimit, acc.metadataBytesRead, n, MaxMetadataBytesPerInput)
	}
	acc.metadataBytesRead += n
	return nil
}

func (acc *inputAccounting) chargeString(n uint64) error {
	if n > MaxABIStringBytesPerInput || acc.retainedStringBytes > MaxABIStringBytesPerInput-n {
		return fmt.Errorf("%w: decoded string bytes (%d + %d) exceeds per-input budget %d",
			ErrABIResourceLimit, acc.retainedStringBytes, n, MaxABIStringBytesPerInput)
	}
	acc.retainedStringBytes += n
	return nil
}

func (acc *inputAccounting) chargeVersionRecord() error {
	if acc.versionRecords >= MaxVersionRecordsPerInput {
		return fmt.Errorf("%w: total version records (%d) exceeds per-input budget %d",
			ErrABIResourceLimit, acc.versionRecords+1, MaxVersionRecordsPerInput)
	}
	acc.versionRecords++
	return nil
}

// EscapeMetadata escapes untrusted metadata bytes for safe terminal / log display.
// Control characters (including \r, \n, \t, ESC), DEL, C1 controls (0x80..0x9F),
// and invalid UTF-8 sequences are escaped as \xNN, \n, \r, \t to prevent display injection.
func EscapeMetadata(s string) string {
	const maxDisplayLen = 4096
	var buf strings.Builder
	buf.Grow(len(s))
	truncated := false
	if len(s) > maxDisplayLen {
		s = s[:maxDisplayLen]
		truncated = true
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			fmt.Fprintf(&buf, "\\x%02x", s[i])
			i++
			continue
		}
		if r < 32 || r == 127 || (r >= 0x80 && r <= 0x9f) {
			fmt.Fprintf(&buf, "\\x%02x", r)
		} else {
			buf.WriteRune(r)
		}
		i += size
	}
	if truncated {
		buf.WriteString("...[truncated]")
	}
	return buf.String()
}

// SanitizeName sanitizes untrusted ELF names to prevent terminal control escape sequences or malformed output.
func SanitizeName(name string) string {
	return EscapeMetadata(name)
}

func sanitizeStringSlice(ss []string) []string {
	res := make([]string, len(ss))
	for i, s := range ss {
		res[i] = EscapeMetadata(s)
	}
	return res
}

func validateELFHeaderMagicAndEncoding(data []byte) (binary.ByteOrder, error) {
	if len(data) < minELFHeaderSize {
		return nil, fmt.Errorf("%w: file size %d bytes is smaller than 64-byte ELF header", ErrABIMetadata, len(data))
	}
	header := data[:minELFHeaderSize]
	if header[0] != 0x7f || header[1] != 'E' || header[2] != 'L' || header[3] != 'F' {
		return nil, fmt.Errorf("%w: invalid ELF magic header", ErrInvalidELF)
	}
	if header[4] != byte(elf.ELFCLASS64) {
		return nil, fmt.Errorf("%w: expected 64-bit ELF class (%d), got %d", ErrInvalidELF, elf.ELFCLASS64, header[4])
	}
	var order binary.ByteOrder
	switch header[5] {
	case byte(elf.ELFDATA2LSB):
		order = binary.LittleEndian
	case byte(elf.ELFDATA2MSB):
		order = binary.BigEndian
	default:
		return nil, fmt.Errorf("%w: invalid ELF data encoding %d", ErrInvalidELF, header[5])
	}
	if header[6] != 1 {
		return nil, fmt.Errorf("%w: invalid ELF version %d (expected 1)", ErrInvalidELF, header[6])
	}
	return order, nil
}

func validateProgramHeaderTable(acc *inputAccounting, fileSize, ePhoff uint64, ePhentsize, ePhnum uint16) error {
	if ePhnum > MaxProgramHeaders {
		return fmt.Errorf("%w: program header count %d exceeds budget %d", ErrABIResourceLimit, ePhnum, MaxProgramHeaders)
	}
	if ePhnum == 0 {
		return nil
	}
	if ePhentsize < minProgramHeaderSize {
		return fmt.Errorf("%w: invalid program header entry size %d (< 56)", ErrABIMetadata, ePhentsize)
	}
	if ePhoff > fileSize {
		return fmt.Errorf("%w: program header table offset %d extends beyond file size %d", ErrABIMetadata, ePhoff, fileSize)
	}
	tableSize := uint64(ePhentsize) * uint64(ePhnum)
	if tableSize > fileSize-ePhoff {
		return fmt.Errorf("%w: program header table extends beyond file (offset %d, count %d, size %d, file size %d)",
			ErrABIMetadata, ePhoff, ePhnum, ePhentsize, fileSize)
	}
	if acc != nil {
		if err := acc.chargeMetadata(tableSize); err != nil {
			return err
		}
	}
	return nil
}

func validateSectionHeaderTable(
	acc *inputAccounting,
	data []byte,
	order binary.ByteOrder,
	fileSize, eShoff uint64,
	eShentsize, eShnum uint16,
) error {
	if eShoff == 0 {
		return nil
	}
	if eShentsize < minSectionHeaderSize {
		return fmt.Errorf("%w: invalid section header entry size %d (< 64)", ErrABIMetadata, eShentsize)
	}
	if eShoff > fileSize {
		return fmt.Errorf("%w: section header table offset %d extends beyond file size %d", ErrABIMetadata, eShoff, fileSize)
	}
	if eShnum == 0 {
		if eShoff+uint64(eShentsize) > fileSize {
			return fmt.Errorf("%w: section header 0 extends beyond file size %d", ErrABIMetadata, fileSize)
		}
		realShnum := order.Uint64(data[eShoff+32 : eShoff+40])
		if realShnum > MaxSectionHeaders {
			return fmt.Errorf("%w: extended section header count %d exceeds budget %d",
				ErrABIResourceLimit, realShnum, MaxSectionHeaders)
		}
		if realShnum > 0 {
			extTableSize := realShnum * uint64(eShentsize)
			if extTableSize > fileSize-eShoff {
				return fmt.Errorf("%w: extended section header table extends beyond file", ErrABIMetadata)
			}
			if acc != nil {
				if err := acc.chargeMetadata(extTableSize); err != nil {
					return err
				}
			}
		}
		return nil
	}
	tableSize := uint64(eShnum) * uint64(eShentsize)
	if tableSize > fileSize-eShoff {
		return fmt.Errorf("%w: section header table extends beyond file (offset %d, count %d, size %d, file size %d)",
			ErrABIMetadata, eShoff, eShnum, eShentsize, fileSize)
	}
	if acc != nil {
		if err := acc.chargeMetadata(tableSize); err != nil {
			return err
		}
	}
	return nil
}

// PreflightELFHeaders validates ELF header tables and arithmetic bounds before general ELF parsing.
func PreflightELFHeaders(data []byte) error {
	acc := &inputAccounting{}
	return preflightELFHeadersWithAccounting(acc, data)
}

func preflightELFHeadersWithAccounting(acc *inputAccounting, data []byte) error {
	if len(data) < minELFHeaderSize {
		return fmt.Errorf("%w: file size %d bytes is smaller than 64-byte ELF header", ErrABIMetadata, len(data))
	}
	if acc != nil {
		if err := acc.chargeMetadata(minELFHeaderSize); err != nil {
			return err
		}
	}
	order, err := validateELFHeaderMagicAndEncoding(data)
	if err != nil {
		return err
	}
	fileSize := uint64(len(data))
	eEhsize := order.Uint16(data[52:54])
	if eEhsize < minELFHeaderSize || int(eEhsize) > len(data) {
		return fmt.Errorf("%w: invalid ELF header size %d", ErrABIMetadata, eEhsize)
	}

	ePhoff := order.Uint64(data[32:40])
	ePhentsize := order.Uint16(data[54:56])
	ePhnum := order.Uint16(data[56:58])
	if err := validateProgramHeaderTable(acc, fileSize, ePhoff, ePhentsize, ePhnum); err != nil {
		return err
	}

	eShoff := order.Uint64(data[40:48])
	eShentsize := order.Uint16(data[58:60])
	eShnum := order.Uint16(data[60:62])
	return validateSectionHeaderTable(acc, data, order, fileSize, eShoff, eShentsize, eShnum)
}

type loadSegment struct {
	off    uint64
	vaddr  uint64
	filesz uint64
	memsz  uint64
}

func translateVaddr(loads []loadSegment, fileSize uint64, vaddr uint64, size uint64) (uint64, error) {
	if size > fileSize {
		return 0, fmt.Errorf("%w: requested size %d exceeds file size %d", ErrABIMetadata, size, fileSize)
	}
	var foundOffset uint64
	matchCount := 0
	for _, seg := range loads {
		if seg.filesz == 0 {
			continue
		}
		if vaddr >= seg.vaddr && vaddr < seg.vaddr+seg.filesz {
			offsetInSeg := vaddr - seg.vaddr
			if size > seg.filesz-offsetInSeg {
				return 0, fmt.Errorf("%w: virtual address range [0x%x..0x%x) extends into zero-fill memory",
					ErrABIMetadata, vaddr, vaddr+size)
			}
			fileOff := seg.off + offsetInSeg
			if fileOff > fileSize || size > fileSize-fileOff {
				return 0, fmt.Errorf("%w: mapped file offset 0x%x+0x%x exceeds file size %d",
					ErrABIMetadata, fileOff, size, fileSize)
			}
			foundOffset = fileOff
			matchCount++
		}
	}
	if matchCount == 0 {
		return 0, fmt.Errorf("%w: virtual address 0x%x is not covered by any file-backed PT_LOAD segment", ErrABIMetadata, vaddr)
	}
	if matchCount > 1 {
		return 0, fmt.Errorf("%w: ambiguous PT_LOAD mapping for virtual address 0x%x (%d matching segments)",
			ErrABIMetadata, vaddr, matchCount)
	}
	return foundOffset, nil
}

// InspectELFABI parses declared ABI metadata from raw ELF binary bytes.
func InspectELFABI(data []byte) (*VariantABIReport, error) {
	acc := &inputAccounting{}
	if err := preflightELFHeadersWithAccounting(acc, data); err != nil {
		return nil, err
	}
	return inspectELFABIWithAccounting(acc, data)
}

func inspectELFABIWithAccounting(acc *inputAccounting, data []byte) (*VariantABIReport, error) {
	var order binary.ByteOrder
	if data[5] == byte(elf.ELFDATA2LSB) {
		order = binary.LittleEndian
	} else {
		order = binary.BigEndian
	}

	fileSize := uint64(len(data))
	eType := elf.Type(order.Uint16(data[16:18]))
	eMachine := elf.Machine(order.Uint16(data[18:20]))
	ePhoff := order.Uint64(data[32:40])
	ePhentsize := uint64(order.Uint16(data[54:56]))
	ePhnum := uint64(order.Uint16(data[56:58]))

	report := &VariantABIReport{
		Class:            "ELFCLASS64",
		Type:             eType.String(),
		Machine:          eMachine.String(),
		InspectionSource: "program_headers",
		Completeness:     MetadataAbsent,
	}

	var loads []loadSegment
	var interpProg *loadSegment
	var dynamicProg *loadSegment
	interpCount := 0

	for i := range ePhnum {
		phOff := ePhoff + i*ePhentsize
		pType := order.Uint32(data[phOff : phOff+4])
		pOffset := order.Uint64(data[phOff+8 : phOff+16])
		pVaddr := order.Uint64(data[phOff+16 : phOff+24])
		pFilesz := order.Uint64(data[phOff+32 : phOff+40])
		pMemsz := order.Uint64(data[phOff+40 : phOff+48])

		switch elf.ProgType(pType) {
		case elf.PT_LOAD:
			if pFilesz > pMemsz || pOffset > fileSize || pFilesz > fileSize-pOffset || pMemsz > math.MaxUint64-pVaddr {
				return nil, fmt.Errorf("%w: invalid PT_LOAD segment bounds (offset %d, filesz %d, memsz %d, vaddr 0x%x)",
					ErrABIMetadata, pOffset, pFilesz, pMemsz, pVaddr)
			}
			loads = append(loads, loadSegment{off: pOffset, vaddr: pVaddr, filesz: pFilesz, memsz: pMemsz})

		case elf.PT_INTERP:
			interpCount++
			if interpCount > 1 {
				return nil, fmt.Errorf("%w: multiple PT_INTERP segments in ELF program headers", ErrABIMetadata)
			}
			if pFilesz > MaxInterpreterSize {
				return nil, fmt.Errorf("%w: PT_INTERP size %d exceeds budget limit %d",
					ErrABIResourceLimit, pFilesz, MaxInterpreterSize)
			}
			if pFilesz == 0 {
				return nil, fmt.Errorf("%w: empty PT_INTERP segment", ErrABIMetadata)
			}
			if pOffset > fileSize || pFilesz > fileSize-pOffset {
				return nil, fmt.Errorf("%w: PT_INTERP segment extends beyond file", ErrABIMetadata)
			}
			interpProg = &loadSegment{off: pOffset, vaddr: pVaddr, filesz: pFilesz, memsz: pMemsz}

		case elf.PT_DYNAMIC:
			if pOffset > fileSize || pFilesz > fileSize-pOffset {
				return nil, fmt.Errorf("%w: PT_DYNAMIC segment extends beyond file", ErrABIMetadata)
			}
			dynamicProg = &loadSegment{off: pOffset, vaddr: pVaddr, filesz: pFilesz, memsz: pMemsz}
		}
	}

	if interpProg != nil {
		rawInterp := data[interpProg.off : interpProg.off+interpProg.filesz]
		nulIdx := bytes.IndexByte(rawInterp, 0)
		if nulIdx < 0 {
			return nil, fmt.Errorf("%w: PT_INTERP string is not NUL-terminated", ErrABIMetadata)
		}
		if nulIdx == 0 {
			return nil, fmt.Errorf("%w: PT_INTERP string is empty", ErrABIMetadata)
		}
		if err := acc.chargeString(uint64(nulIdx)); err != nil {
			return nil, err
		}
		report.HasInterpreter = true
		report.Interpreter = string(rawInterp[:nulIdx])
	}

	if dynamicProg != nil {
		report.InspectionSource = "program_headers/dynamic_metadata"
		if err := parseDynamicMetadata(data, dynamicProg, loads, fileSize, order, acc, report); err != nil {
			return nil, err
		}
	}

	classifyLinkage(report, eType)
	report.VersionRequirements = normalizeVersionRequirements(report.VersionRequirements)

	return report, nil
}

type dynamicTags struct {
	neededOffsets []uint64
	strtabVaddr   uint64
	strtabSize    uint64
	verneedVaddr  uint64
	verneedNum    uint64
	hasStrtab     bool
	hasStrsz      bool
	hasVerneed    bool
	hasVerneedNum bool
}

func scanDynamicEntries(data []byte, dynamicProg *loadSegment, numDyn uint64, order binary.ByteOrder) (*dynamicTags, error) {
	tags := &dynamicTags{}
	hasNullTerminator := false

	for i := range numDyn {
		entryOff := dynamicProg.off + i*dynamicEntrySize
		dTag := order.Uint64(data[entryOff : entryOff+8])
		dVal := order.Uint64(data[entryOff+8 : entryOff+16])

		switch dTag {
		case uint64(elf.DT_NULL):
			hasNullTerminator = true
		case uint64(elf.DT_NEEDED):
			tags.neededOffsets = append(tags.neededOffsets, dVal)
		case uint64(elf.DT_STRTAB):
			tags.strtabVaddr = dVal
			tags.hasStrtab = true
		case uint64(elf.DT_STRSZ):
			tags.strtabSize = dVal
			tags.hasStrsz = true
		case uint64(elf.DT_VERNEED):
			tags.verneedVaddr = dVal
			tags.hasVerneed = true
		case uint64(elf.DT_VERNEEDNUM):
			tags.verneedNum = dVal
			tags.hasVerneedNum = true
		}
		if hasNullTerminator {
			break
		}
	}

	if numDyn > 0 && !hasNullTerminator {
		return nil, fmt.Errorf("%w: PT_DYNAMIC table is missing DT_NULL terminator", ErrABIMetadata)
	}
	return tags, nil
}

func parseNeededDependencies(
	strtab []byte,
	strtabSize uint64,
	neededOffsets []uint64,
	acc *inputAccounting,
) ([]string, error) {
	if uint64(len(neededOffsets)) > MaxDynamicEntries {
		return nil, fmt.Errorf("%w: needed dependencies count %d exceeds budget %d",
			ErrABIResourceLimit, len(neededOffsets), MaxDynamicEntries)
	}
	deps := make([]string, 0, len(neededOffsets))
	for _, off := range neededOffsets {
		if off >= strtabSize {
			return nil, fmt.Errorf("%w: DT_NEEDED offset %d out of bounds for STRTAB size %d", ErrABIMetadata, off, strtabSize)
		}
		nulIdx := bytes.IndexByte(strtab[off:], 0)
		if nulIdx < 0 {
			return nil, fmt.Errorf("%w: DT_NEEDED string at offset %d is not NUL-terminated", ErrABIMetadata, off)
		}
		strLen := uint64(nulIdx)
		if strLen == 0 {
			return nil, fmt.Errorf("%w: DT_NEEDED string at offset %d is empty", ErrABIMetadata, off)
		}
		if err := acc.chargeString(strLen); err != nil {
			return nil, err
		}
		depName := string(strtab[off : off+strLen])
		deps = append(deps, depName)
	}
	return deps, nil
}

func parseDynamicMetadata(
	data []byte,
	dynamicProg *loadSegment,
	loads []loadSegment,
	fileSize uint64,
	order binary.ByteOrder,
	acc *inputAccounting,
	report *VariantABIReport,
) error {
	if dynamicProg.filesz%dynamicEntrySize != 0 {
		return fmt.Errorf("%w: PT_DYNAMIC segment size %d is not a multiple of 16", ErrABIMetadata, dynamicProg.filesz)
	}
	numDyn := dynamicProg.filesz / dynamicEntrySize
	if numDyn > MaxDynamicEntries {
		return fmt.Errorf("%w: dynamic entry count %d exceeds budget limit %d",
			ErrABIResourceLimit, numDyn, MaxDynamicEntries)
	}
	if err := acc.chargeMetadata(dynamicProg.filesz); err != nil {
		return err
	}

	tags, err := scanDynamicEntries(data, dynamicProg, numDyn, order)
	if err != nil {
		return err
	}

	var strtab []byte
	if len(tags.neededOffsets) > 0 || tags.hasVerneed {
		if !tags.hasStrtab || !tags.hasStrsz {
			return fmt.Errorf("%w: dynamic dependencies or version requirements declared without DT_STRTAB or DT_STRSZ", ErrABIMetadata)
		}
		if tags.strtabSize > MaxABIStringBytesPerInput {
			return fmt.Errorf("%w: dynamic STRTAB size %d exceeds budget limit %d",
				ErrABIResourceLimit, tags.strtabSize, MaxABIStringBytesPerInput)
		}
		if err := acc.chargeMetadata(tags.strtabSize); err != nil {
			return err
		}
		strtabOff, tErr := translateVaddr(loads, fileSize, tags.strtabVaddr, tags.strtabSize)
		if tErr != nil {
			return fmt.Errorf("%w: translating DT_STRTAB address: %w", ErrABIMetadata, tErr)
		}
		strtab = data[strtabOff : strtabOff+tags.strtabSize]
	}

	deps, err := parseNeededDependencies(strtab, tags.strtabSize, tags.neededOffsets, acc)
	if err != nil {
		return err
	}
	report.Dependencies = deps

	if tags.hasVerneed && tags.hasVerneedNum && tags.verneedNum > 0 {
		return parseVerneed(data, loads, fileSize, order, tags.verneedVaddr, tags.verneedNum, strtab, acc, report)
	}
	return nil
}

func advanceELFRelativeOffset(currOff uint64, relOffset uint32, minOff, maxOff, entrySize uint64) (uint64, error) {
	if relOffset == 0 {
		return 0, fmt.Errorf("%w: zero progress in ELF structure chain", ErrABIMetadata)
	}
	var newOff uint64
	const signBit = 0x80000000
	if relOffset&signBit == 0 {
		newOff = currOff + uint64(relOffset)
	} else {
		neg := (uint64(1) << 32) - uint64(relOffset)
		if currOff < neg {
			return 0, fmt.Errorf("%w: negative offset underflow in ELF structure chain", ErrABIMetadata)
		}
		newOff = currOff - neg
	}
	if newOff < minOff || newOff+entrySize > maxOff {
		return 0, fmt.Errorf("%w: relative offset 0x%x out of bounds [0x%x..0x%x)", ErrABIMetadata, relOffset, minOff, maxOff)
	}
	return newOff, nil
}

func parseVernauxChain(
	data []byte,
	order binary.ByteOrder,
	verneedOff, availableBytes, currVernauxOff uint64,
	vnCnt uint16,
	libName string,
	strtab []byte,
	strtabSize uint64,
	acc *inputAccounting,
	report *VariantABIReport,
) error {
	visitedVernaux := make(map[uint64]bool)

	for auxIdx := range vnCnt {
		if err := acc.chargeVersionRecord(); err != nil {
			return err
		}
		if currVernauxOff < verneedOff || currVernauxOff+vernauxEntrySize > verneedOff+availableBytes {
			return fmt.Errorf("%w: Elf64_Vernaux entry %d offset out of bounds", ErrABIMetadata, auxIdx)
		}
		if visitedVernaux[currVernauxOff] {
			return fmt.Errorf("%w: cycle detected in Elf64_Vernaux chain at offset 0x%x", ErrABIMetadata, currVernauxOff)
		}
		visitedVernaux[currVernauxOff] = true

		vnaFlags := order.Uint16(data[currVernauxOff+4 : currVernauxOff+6])
		vnaName := order.Uint32(data[currVernauxOff+8 : currVernauxOff+12])
		vnaNext := order.Uint32(data[currVernauxOff+12 : currVernauxOff+16])

		if uint64(vnaName) >= strtabSize {
			return fmt.Errorf("%w: vna_name string offset %d out of bounds", ErrABIMetadata, vnaName)
		}
		nameNul := bytes.IndexByte(strtab[vnaName:], 0)
		if nameNul < 0 {
			return fmt.Errorf("%w: vna_name string at offset %d is not NUL-terminated", ErrABIMetadata, vnaName)
		}
		verLen := uint64(nameNul)
		if err := acc.chargeString(verLen); err != nil {
			return err
		}
		verName := string(strtab[vnaName : uint64(vnaName)+verLen])

		report.VersionRequirements = append(report.VersionRequirements, VersionRequirement{
			Library: libName,
			Version: verName,
			Flags:   vnaFlags,
		})

		if auxIdx < vnCnt-1 {
			if vnaNext == 0 {
				return fmt.Errorf("%w: premature termination of Elf64_Vernaux chain (expected %d entries, stopped at %d)",
					ErrABIMetadata, vnCnt, auxIdx+1)
			}
			nextOff, err := advanceELFRelativeOffset(currVernauxOff, vnaNext, verneedOff, verneedOff+availableBytes, vernauxEntrySize)
			if err != nil {
				return fmt.Errorf("%w: Elf64_Vernaux entry %d offset: %w", ErrABIMetadata, auxIdx+1, err)
			}
			currVernauxOff = nextOff
		}
	}
	return nil
}

func parseVerneed(
	data []byte,
	loads []loadSegment,
	fileSize uint64,
	order binary.ByteOrder,
	verneedVaddr uint64,
	verneedNum uint64,
	strtab []byte,
	acc *inputAccounting,
	report *VariantABIReport,
) error {
	if verneedNum > MaxVersionRecordsPerInput {
		return fmt.Errorf("%w: DT_VERNEEDNUM %d exceeds budget limit %d",
			ErrABIResourceLimit, verneedNum, MaxVersionRecordsPerInput)
	}
	verneedBytes := verneedNum * uint64(verneedEntrySize)
	if err := acc.chargeMetadata(verneedBytes); err != nil {
		return err
	}

	verneedOff, err := translateVaddr(loads, fileSize, verneedVaddr, verneedEntrySize)
	if err != nil {
		return fmt.Errorf("%w: translating DT_VERNEED address: %w", ErrABIMetadata, err)
	}

	var availableBytes uint64
	for _, seg := range loads {
		if verneedVaddr >= seg.vaddr && verneedVaddr < seg.vaddr+seg.filesz {
			availableBytes = seg.filesz - (verneedVaddr - seg.vaddr)
			break
		}
	}

	strtabSize := uint64(len(strtab))
	visitedVerneed := make(map[uint64]bool)
	currVerneedOff := verneedOff

	for recordIdx := range verneedNum {
		if err := acc.chargeVersionRecord(); err != nil {
			return err
		}
		if currVerneedOff < verneedOff || currVerneedOff+verneedEntrySize > verneedOff+availableBytes {
			return fmt.Errorf("%w: Elf64_Verneed entry %d offset out of bounds", ErrABIMetadata, recordIdx)
		}
		if visitedVerneed[currVerneedOff] {
			return fmt.Errorf("%w: cycle detected in Elf64_Verneed chain at offset 0x%x", ErrABIMetadata, currVerneedOff)
		}
		visitedVerneed[currVerneedOff] = true

		vnVersion := order.Uint16(data[currVerneedOff : currVerneedOff+2])
		if vnVersion != 1 {
			report.Completeness = MetadataUnsupported
			report.Warnings = append(report.Warnings, fmt.Sprintf("unsupported Elf64_Verneed version %d", vnVersion))
			break
		}
		vnCnt := order.Uint16(data[currVerneedOff+2 : currVerneedOff+4])
		vnFile := order.Uint32(data[currVerneedOff+4 : currVerneedOff+8])
		vnAux := order.Uint32(data[currVerneedOff+8 : currVerneedOff+12])
		vnNext := order.Uint32(data[currVerneedOff+12 : currVerneedOff+16])

		if uint64(vnFile) >= strtabSize {
			return fmt.Errorf("%w: vn_file string offset %d out of bounds", ErrABIMetadata, vnFile)
		}
		fileNul := bytes.IndexByte(strtab[vnFile:], 0)
		if fileNul < 0 {
			return fmt.Errorf("%w: vn_file string at offset %d is not NUL-terminated", ErrABIMetadata, vnFile)
		}
		libLen := uint64(fileNul)
		if err := acc.chargeString(libLen); err != nil {
			return err
		}
		libName := string(strtab[vnFile : uint64(vnFile)+libLen])

		if vnCnt > 0 {
			if uint64(vnAux) < vernauxEntrySize && vnAux != 0 {
				return fmt.Errorf("%w: invalid vn_aux offset %d", ErrABIMetadata, vnAux)
			}
			currVernauxOff := currVerneedOff + uint64(vnAux)
			if err := parseVernauxChain(
				data, order, verneedOff, availableBytes, currVernauxOff,
				vnCnt, libName, strtab, strtabSize, acc, report,
			); err != nil {
				return err
			}
		}

		if recordIdx < verneedNum-1 {
			if vnNext == 0 {
				return fmt.Errorf("%w: premature termination of Elf64_Verneed chain (expected %d records, stopped at %d)",
					ErrABIMetadata, verneedNum, recordIdx+1)
			}
			nextOff, aErr := advanceELFRelativeOffset(currVerneedOff, vnNext, verneedOff, verneedOff+availableBytes, verneedEntrySize)
			if aErr != nil {
				return fmt.Errorf("%w: Elf64_Verneed entry %d offset: %w", ErrABIMetadata, recordIdx+1, aErr)
			}
			currVerneedOff = nextOff
		}
	}

	if report.Completeness != MetadataUnsupported {
		report.Completeness = MetadataComplete
	}
	return nil
}

func classifyLinkage(report *VariantABIReport, eType elf.Type) {
	if report.HasInterpreter {
		report.Linkage = LinkageDynamic
		return
	}

	if len(report.Dependencies) == 0 {
		switch eType {
		case elf.ET_DYN:
			report.Linkage = LinkageStaticPIE
		default:
			report.Linkage = LinkageStatic
		}
		if report.Completeness != MetadataUnsupported && report.Completeness != MetadataPartial {
			report.Completeness = MetadataAbsent
		}
	} else {
		report.Linkage = LinkageAmbiguous
		report.Warnings = append(report.Warnings, "payload declares dynamic dependencies but has no PT_INTERP")
	}
}

func normalizeVersionRequirements(reqs []VersionRequirement) []VersionRequirement {
	if len(reqs) <= 1 {
		return reqs
	}
	seen := make(map[VersionRequirement]bool, len(reqs))
	result := make([]VersionRequirement, 0, len(reqs))
	for _, r := range reqs {
		if !seen[r] {
			seen[r] = true
			result = append(result, r)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Library != result[j].Library {
			return result[i].Library < result[j].Library
		}
		if result[i].Version != result[j].Version {
			return result[i].Version < result[j].Version
		}
		return result[i].Flags < result[j].Flags
	})
	return result
}

func versionRequirementsEqual(a, b []VersionRequirement) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validateReportBudget(reports []*VariantABIReport) error {
	totalReportBytes := 0
	for _, r := range reports {
		if r == nil {
			return fmt.Errorf("%w: nil variant ABI report provided", ErrABIMetadata)
		}
		if len(r.Interpreter) > MaxInterpreterSize {
			return fmt.Errorf("%w: variant %s interpreter path length %d exceeds budget %d",
				ErrABIResourceLimit, r.Level, len(r.Interpreter), MaxInterpreterSize)
		}
		if len(r.Dependencies) > MaxDynamicEntries {
			return fmt.Errorf("%w: variant %s dependency count %d exceeds budget %d",
				ErrABIResourceLimit, r.Level, len(r.Dependencies), MaxDynamicEntries)
		}
		if len(r.VersionRequirements) > MaxVersionRecordsPerInput {
			return fmt.Errorf("%w: variant %s version requirement count %d exceeds budget %d",
				ErrABIResourceLimit, r.Level, len(r.VersionRequirements), MaxVersionRecordsPerInput)
		}
		var inputStringBytes uint64
		inputStringBytes += uint64(len(r.Interpreter))
		for _, d := range r.Dependencies {
			inputStringBytes += uint64(len(d))
		}
		for _, v := range r.VersionRequirements {
			inputStringBytes += uint64(len(v.Library)) + uint64(len(v.Version))
		}
		if inputStringBytes > MaxABIStringBytesPerInput {
			return fmt.Errorf("%w: variant %s decoded string bytes %d exceeds per-input budget %d",
				ErrABIResourceLimit, r.Level, inputStringBytes, MaxABIStringBytesPerInput)
		}

		reportBytes := len(r.Level) + len(r.Path) + len(r.Class) + len(r.Type) +
			len(r.Machine) + len(r.Interpreter) + len(r.Linkage) + len(r.Completeness) + len(r.InspectionSource)
		for _, d := range r.Dependencies {
			reportBytes += len(d)
		}
		for _, v := range r.VersionRequirements {
			reportBytes += len(v.Library) + len(v.Version) + versionReportSeparatorOverhead
		}
		for _, w := range r.Warnings {
			reportBytes += len(w)
		}
		totalReportBytes += reportBytes
	}
	if totalReportBytes > MaxArtifactReportBytes {
		return fmt.Errorf("%w: aggregate retained decoded ABI report data (%d bytes) exceeds budget limit %d bytes",
			ErrABIResourceLimit, totalReportBytes, MaxArtifactReportBytes)
	}
	return nil
}

func compareDependencySets(baseline, current *VariantABIReport) (string, string) {
	baseDeps := baseline.Dependencies
	currDeps := current.Dependencies

	baseSet := make(map[string]bool, len(baseDeps))
	for _, d := range baseDeps {
		baseSet[d] = true
	}
	currSet := make(map[string]bool, len(currDeps))
	for _, d := range currDeps {
		currSet[d] = true
	}

	setMismatch := false
	if len(baseSet) != len(currSet) {
		setMismatch = true
	} else {
		for d := range baseSet {
			if !currSet[d] {
				setMismatch = true
				break
			}
		}
	}

	if setMismatch {
		escapedBase := make([]string, len(baseDeps))
		for i, d := range baseDeps {
			escapedBase[i] = EscapeMetadata(d)
		}
		escapedCurr := make([]string, len(currDeps))
		for i, d := range currDeps {
			escapedCurr[i] = EscapeMetadata(d)
		}
		return fmt.Sprintf("differing declared dependencies: variant %s requires %v, variant %s requires %v",
			EscapeMetadata(baseline.Level), escapedBase, EscapeMetadata(current.Level), escapedCurr), ""
	}

	if len(baseDeps) == len(currDeps) {
		for idx := range baseDeps {
			if baseDeps[idx] != currDeps[idx] {
				return "", fmt.Sprintf(
					"declared dependency search order differs between variant %s and %s (may affect dynamic symbol resolution order)",
					EscapeMetadata(baseline.Level), EscapeMetadata(current.Level))
			}
		}
	}
	return "", ""
}

func compareDynamicVariants(baseline, current *VariantABIReport) ([]string, []string) {
	var diffs []string
	var warns []string

	if baseline.Interpreter != current.Interpreter {
		diffs = append(diffs, fmt.Sprintf("differing interpreter pathnames: variant %s requires %q, variant %s requires %q",
			EscapeMetadata(baseline.Level), EscapeMetadata(baseline.Interpreter),
			EscapeMetadata(current.Level), EscapeMetadata(current.Interpreter)))
	}

	depDiff, depWarn := compareDependencySets(baseline, current)
	if depDiff != "" {
		diffs = append(diffs, depDiff)
	}
	if depWarn != "" {
		warns = append(warns, depWarn)
	}

	baselineUnknown := baseline.Completeness == MetadataPartial || baseline.Completeness == MetadataUnsupported
	currentUnknown := current.Completeness == MetadataPartial || current.Completeness == MetadataUnsupported

	if !baselineUnknown && !currentUnknown {
		if !versionRequirementsEqual(baseline.VersionRequirements, current.VersionRequirements) {
			diffs = append(diffs, fmt.Sprintf("differing symbol version requirements: variant %s (%d requirements) vs variant %s (%d requirements)",
				EscapeMetadata(baseline.Level), len(baseline.VersionRequirements),
				EscapeMetadata(current.Level), len(current.VersionRequirements)))
		}
	}

	return diffs, warns
}

func evaluateVariantCompleteness(rep *VariantABIReport) (isSkipped, isUnknown bool, warning string) {
	switch rep.Completeness {
	case MetadataSkipped:
		return true, false, ""
	case MetadataPartial, MetadataUnsupported:
		return false, true, fmt.Sprintf(
			"variant %s has partial or unsupported version metadata (%s); "+
				"complete symbol version compatibility cannot be verified",
			EscapeMetadata(rep.Level), rep.Completeness,
		)
	default:
		return false, false, ""
	}
}

func compareReportWithBaseline(baseline, current *VariantABIReport) (diffs, warns []string, isSkipped, isUnknown bool) {
	isSkipped, isUnknown, warn := evaluateVariantCompleteness(current)
	if warn != "" {
		warns = append(warns, warn)
	}
	if len(current.Warnings) > 0 {
		warns = append(warns, current.Warnings...)
	}

	isBaselineStatic := baseline.Linkage == LinkageStatic || baseline.Linkage == LinkageStaticPIE
	isCurrentStatic := current.Linkage == LinkageStatic || current.Linkage == LinkageStaticPIE

	if isBaselineStatic != isCurrentStatic {
		diff := fmt.Sprintf("mixed static and dynamic payloads: variant %s is %s, variant %s is %s",
			EscapeMetadata(baseline.Level), baseline.Linkage, EscapeMetadata(current.Level), current.Linkage)
		diffs = append(diffs, diff)
	}

	if !isBaselineStatic && !isCurrentStatic {
		dDiffs, dWarns := compareDynamicVariants(baseline, current)
		diffs = append(diffs, dDiffs...)
		warns = append(warns, dWarns...)
	}
	return diffs, warns, isSkipped, isUnknown
}

// CompareVariantABIs evaluates declared ABI consistency across variant reports using the uniform ABI policy.
func CompareVariantABIs(reports []*VariantABIReport, allowMixedABI bool) (*ArtifactABIReport, error) {
	if err := validateReportBudget(reports); err != nil {
		return nil, err
	}

	artifactReport := &ArtifactABIReport{
		Variants:   reports,
		Consistent: true,
		Status:     ComparisonConsistent,
		DeploymentDisclaimer: "Matching declared ABI requirements does not guarantee deployment host compatibility; " +
			"the target system must provide the required dynamic linker and libraries.",
	}
	if len(reports) == 0 {
		return artifactReport, nil
	}
	if len(reports) == 1 {
		r := reports[0]
		isSkipped, isUnknown, warn := evaluateVariantCompleteness(r)
		switch {
		case isSkipped:
			artifactReport.Status = ComparisonSkipped
			artifactReport.Consistent = false
		case isUnknown:
			artifactReport.Status = ComparisonUnknown
			artifactReport.Consistent = true
			artifactReport.Warnings = append(artifactReport.Warnings, warn)
		default:
			artifactReport.Status = ComparisonConsistent
			artifactReport.Consistent = true
		}
		if len(r.Warnings) > 0 {
			artifactReport.Warnings = append(artifactReport.Warnings, r.Warnings...)
		}
		return artifactReport, nil
	}

	baseline := reports[0]
	var differences []string
	var warnings []string
	hasUnknown := false

	baseSkipped, baseUnknown, baseWarn := evaluateVariantCompleteness(baseline)
	if baseSkipped {
		artifactReport.Status = ComparisonSkipped
		artifactReport.Consistent = false
	} else if baseUnknown {
		hasUnknown = true
		warnings = append(warnings, baseWarn)
	}
	if len(baseline.Warnings) > 0 {
		warnings = append(warnings, baseline.Warnings...)
	}

	for i := 1; i < len(reports); i++ {
		cDiffs, cWarns, currSkipped, currUnknown := compareReportWithBaseline(baseline, reports[i])
		if currSkipped {
			artifactReport.Status = ComparisonSkipped
			artifactReport.Consistent = false
		}
		if currUnknown {
			hasUnknown = true
		}
		differences = append(differences, cDiffs...)
		warnings = append(warnings, cWarns...)
	}

	artifactReport.Differences = differences
	artifactReport.Warnings = warnings

	if len(differences) > 0 {
		artifactReport.Consistent = false
		artifactReport.Status = ComparisonInconsistent
		if !allowMixedABI {
			return artifactReport, fmt.Errorf("%w:\n  • %s", ErrABIMismatch, strings.Join(differences, "\n  • "))
		}
		artifactReport.Overridden = true
		for _, d := range differences {
			artifactReport.Warnings = append(artifactReport.Warnings, fmt.Sprintf("[OVERRIDDEN] %s", d))
		}
		if hasUnknown {
			artifactReport.Status = ComparisonUnknown
		}
	} else if artifactReport.Status != ComparisonSkipped {
		if hasUnknown {
			artifactReport.Status = ComparisonUnknown
		} else {
			artifactReport.Status = ComparisonConsistent
		}
		artifactReport.Consistent = true
	}

	return artifactReport, nil
}
