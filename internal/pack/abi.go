package pack

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
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
	mismatchDiffBulletOverhead     = 6
	depPresentationOverhead        = 4
	versionPresentationOverhead    = 24
)

// ABILimits specifies configurable resource bounds for ABI metadata inspection and comparison.
type ABILimits struct {
	MaxProgramHeaders         uint16
	MaxSectionHeaders         uint32
	MaxInterpreterSize        uint64
	MaxDynamicEntries         uint64
	MaxABIStringBytesPerInput uint64
	MaxVersionRecordsPerInput uint64
	MaxMetadataBytesPerInput  uint64
	MaxArtifactMetadataBytes  uint64
	MaxArtifactReportBytes    uint64
}

var defaultABILimits = ABILimits{
	MaxProgramHeaders:         MaxProgramHeaders,
	MaxSectionHeaders:         MaxSectionHeaders,
	MaxInterpreterSize:        MaxInterpreterSize,
	MaxDynamicEntries:         MaxDynamicEntries,
	MaxABIStringBytesPerInput: MaxABIStringBytesPerInput,
	MaxVersionRecordsPerInput: MaxVersionRecordsPerInput,
	MaxMetadataBytesPerInput:  MaxMetadataBytesPerInput,
	MaxArtifactMetadataBytes:  MaxArtifactMetadataBytes,
	MaxArtifactReportBytes:    MaxArtifactReportBytes,
}

// ArtifactMetadataAccounting tracks aggregate metadata read/scanned across all variant inputs in an artifact.
type ArtifactMetadataAccounting struct {
	maxBytes  uint64
	usedBytes uint64
}

// NewArtifactMetadataAccounting constructs an aggregate metadata accounting tracker.
func NewArtifactMetadataAccounting(maxBytes uint64) *ArtifactMetadataAccounting {
	if maxBytes == 0 {
		maxBytes = MaxArtifactMetadataBytes
	}
	return &ArtifactMetadataAccounting{maxBytes: maxBytes}
}

func (a *ArtifactMetadataAccounting) check(n uint64) error {
	if a == nil {
		return nil
	}
	if n > a.maxBytes || a.usedBytes > a.maxBytes-n {
		return fmt.Errorf("%w: aggregate artifact metadata bytes read (%d + %d) exceeds budget limit %d",
			ErrABIResourceLimit, a.usedBytes, n, a.maxBytes)
	}
	return nil
}

func (a *ArtifactMetadataAccounting) commit(n uint64) {
	if a != nil {
		a.usedBytes += n
	}
}

func (a *ArtifactMetadataAccounting) remaining() uint64 {
	if a == nil {
		return math.MaxUint64
	}
	limit := a.maxBytes
	if limit == 0 {
		limit = MaxArtifactMetadataBytes
	}
	if a.usedBytes >= limit {
		return 0
	}
	return limit - a.usedBytes
}

func (a *ArtifactMetadataAccounting) refund(n uint64) {
	if a == nil {
		return
	}
	if n > a.usedBytes {
		a.usedBytes = 0
	} else {
		a.usedBytes -= n
	}
}

// ReportAccounting tracks aggregate retained and generated report data across all variants in an artifact.
type ReportAccounting struct {
	maxBytes  uint64
	usedBytes uint64
}

// NewReportAccounting constructs an aggregate report accounting tracker.
func NewReportAccounting(maxBytes uint64) *ReportAccounting {
	if maxBytes == 0 {
		maxBytes = MaxArtifactReportBytes
	}
	return &ReportAccounting{maxBytes: maxBytes}
}

func (ra *ReportAccounting) reserve(n uint64) error {
	if ra == nil {
		return nil
	}
	if n > ra.maxBytes || ra.usedBytes > ra.maxBytes-n {
		return fmt.Errorf("%w: report data (%d + %d) exceeds budget limit %d bytes",
			ErrABIResourceLimit, ra.usedBytes, n, ra.maxBytes)
	}
	ra.usedBytes += n
	return nil
}

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
	shared              *ArtifactMetadataAccounting
	maxMetadataBytes    uint64
	maxStringBytes      uint64
	maxStringScanBytes  uint64
	maxVersionRecords   uint64
	metadataBytesRead   uint64
	stringScanBytesRead uint64
	retainedStringBytes uint64
	versionRecords      uint64
}

func (acc *inputAccounting) checkMetadata(n uint64) error {
	limit := acc.maxMetadataBytes
	if limit == 0 {
		limit = MaxMetadataBytesPerInput
	}
	if n > limit || acc.metadataBytesRead > limit-n {
		return fmt.Errorf("%w: metadata bytes read (%d + %d) exceeds per-input budget %d",
			ErrABIResourceLimit, acc.metadataBytesRead, n, limit)
	}
	if acc.shared != nil {
		if err := acc.shared.check(n); err != nil {
			return err
		}
	}
	return nil
}

func (acc *inputAccounting) chargeMetadata(n uint64) error {
	if err := acc.checkMetadata(n); err != nil {
		return err
	}
	acc.metadataBytesRead += n
	if acc.shared != nil {
		acc.shared.commit(n)
	}
	return nil
}

func (acc *inputAccounting) remainingMetadataBudget() uint64 {
	limit := acc.maxMetadataBytes
	if limit == 0 {
		limit = MaxMetadataBytesPerInput
	}
	if acc.metadataBytesRead >= limit {
		return 0
	}
	return limit - acc.metadataBytesRead
}

func (acc *inputAccounting) checkStringScan(n uint64) error {
	limit := acc.maxStringScanBytes
	if limit == 0 {
		if acc.maxStringBytes != 0 {
			limit = acc.maxStringBytes
		} else {
			limit = MaxABIStringBytesPerInput
		}
	}
	if n > limit || acc.stringScanBytesRead > limit-n {
		return fmt.Errorf("%w: string scan bytes (%d + %d) exceeds per-input budget %d",
			ErrABIResourceLimit, acc.stringScanBytesRead, n, limit)
	}
	return nil
}

func (acc *inputAccounting) checkAndChargeStringScan(n uint64) error {
	if err := acc.checkStringScan(n); err != nil {
		return err
	}
	if err := acc.checkMetadata(n); err != nil {
		return err
	}
	acc.stringScanBytesRead += n
	acc.metadataBytesRead += n
	if acc.shared != nil {
		acc.shared.commit(n)
	}
	return nil
}

func (acc *inputAccounting) chargeStringScan(n uint64) error {
	return acc.checkAndChargeStringScan(n)
}

func (acc *inputAccounting) remainingStringScanBudget() uint64 {
	limit := acc.maxStringScanBytes
	if limit == 0 {
		if acc.maxStringBytes != 0 {
			limit = acc.maxStringBytes
		} else {
			limit = MaxABIStringBytesPerInput
		}
	}
	if acc.stringScanBytesRead >= limit {
		return 0
	}
	return limit - acc.stringScanBytesRead
}

func (acc *inputAccounting) refundAllStringScan(n uint64) {
	if n > acc.stringScanBytesRead {
		acc.stringScanBytesRead = 0
	} else {
		acc.stringScanBytesRead -= n
	}
	if n > acc.metadataBytesRead {
		acc.metadataBytesRead = 0
	} else {
		acc.metadataBytesRead -= n
	}
	if acc.shared != nil {
		acc.shared.refund(n)
	}
}

func (acc *inputAccounting) refundStringScan(n uint64) {
	acc.refundAllStringScan(n)
}

func (acc *inputAccounting) chargeString(n uint64) error {
	limit := acc.maxStringBytes
	if limit == 0 {
		limit = MaxABIStringBytesPerInput
	}
	if n > limit || acc.retainedStringBytes > limit-n {
		return fmt.Errorf("%w: decoded string bytes (%d + %d) exceeds per-input budget %d",
			ErrABIResourceLimit, acc.retainedStringBytes, n, limit)
	}
	acc.retainedStringBytes += n
	return nil
}

func (acc *inputAccounting) chargeVersionRecord() error {
	limit := acc.maxVersionRecords
	if limit == 0 {
		limit = MaxVersionRecordsPerInput
	}
	if acc.versionRecords >= limit {
		return fmt.Errorf("%w: total version records (%d) exceeds per-input budget %d",
			ErrABIResourceLimit, acc.versionRecords+1, limit)
	}
	acc.versionRecords++
	return nil
}

// EscapedMetadataLen returns the exact escaped length of s when rendered with EscapeMetadata,
// and whether the string will be truncated at maxDisplayLen (4096).
func EscapedMetadataLen(s string) (int, bool) {
	const maxDisplayLen = 4096
	const truncMarkerLen = 14 // len("...[truncated]")
	truncated := false
	if len(s) > maxDisplayLen {
		s = s[:maxDisplayLen]
		truncated = true
	}
	outputLen := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			outputLen += 4
			i++
			continue
		}
		if r < 32 || r == 127 || (r >= 0x80 && r <= 0x9f) {
			outputLen += 4
		} else {
			outputLen += size
		}
		i += size
	}
	if truncated {
		outputLen += truncMarkerLen
	}
	return outputLen, truncated
}

func writeEscapedIntoBuilder(buf *strings.Builder, s string) {
	const maxDisplayLen = 4096
	truncated := false
	if len(s) > maxDisplayLen {
		s = s[:maxDisplayLen]
		truncated = true
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			fmt.Fprintf(buf, "\\x%02x", s[i])
			i++
			continue
		}
		if r < 32 || r == 127 || (r >= 0x80 && r <= 0x9f) {
			fmt.Fprintf(buf, "\\x%02x", r)
		} else {
			buf.WriteRune(r)
		}
		i += size
	}
	if truncated {
		buf.WriteString("...[truncated]")
	}
}

// EscapeMetadata escapes untrusted metadata bytes for safe terminal / log display.
// Control characters (including \r, \n, \t, ESC), DEL, C1 controls (0x80..0x9F),
// and invalid UTF-8 sequences are escaped as \xNN, \n, \r, \t to prevent display injection.
func EscapeMetadata(s string) string {
	needed, _ := EscapedMetadataLen(s)
	var buf strings.Builder
	buf.Grow(needed)
	writeEscapedIntoBuilder(&buf, s)
	return buf.String()
}

type boundedReportBuilder struct {
	ra *ReportAccounting
	sb strings.Builder
}

func newBoundedReportBuilder(ra *ReportAccounting) *boundedReportBuilder {
	return &boundedReportBuilder{ra: ra}
}

func (b *boundedReportBuilder) writeString(s string) error {
	if err := b.ra.reserve(uint64(len(s))); err != nil {
		return err
	}
	b.sb.WriteString(s)
	return nil
}

func (b *boundedReportBuilder) writeEscaped(s string) error {
	needed, _ := EscapedMetadataLen(s)
	if needed < 0 {
		return fmt.Errorf("%w: invalid escaped metadata length %d", ErrABIMetadata, needed)
	}
	if err := b.ra.reserve(uint64(needed)); err != nil {
		return err
	}
	writeEscapedIntoBuilder(&b.sb, s)
	return nil
}

func (b *boundedReportBuilder) writeQuotedEscaped(s string) error {
	needed, _ := EscapedMetadataLen(s)
	if needed < 0 {
		return fmt.Errorf("%w: invalid escaped metadata length %d", ErrABIMetadata, needed)
	}
	const quotesOverhead = 2
	total := uint64(needed) + quotesOverhead
	if err := b.ra.reserve(total); err != nil {
		return err
	}
	b.sb.WriteByte('"')
	writeEscapedIntoBuilder(&b.sb, s)
	b.sb.WriteByte('"')
	return nil
}

func (b *boundedReportBuilder) writeInt(n int) error {
	s := strconv.Itoa(n)
	return b.writeString(s)
}

func (b *boundedReportBuilder) writeBoundedDepList(deps []string, maxItems int) error {
	if len(deps) == 0 {
		return b.writeString("none")
	}
	if err := b.writeString("["); err != nil {
		return err
	}
	n := min(len(deps), maxItems)
	for i := 0; i < n; i++ {
		if i > 0 {
			if err := b.writeString(", "); err != nil {
				return err
			}
		}
		if err := b.writeEscaped(deps[i]); err != nil {
			return err
		}
	}
	if len(deps) > maxItems {
		if err := b.writeString(" ... (+"); err != nil {
			return err
		}
		if err := b.writeInt(len(deps) - maxItems); err != nil {
			return err
		}
		if err := b.writeString(" more)"); err != nil {
			return err
		}
	}
	return b.writeString("]")
}

func (b *boundedReportBuilder) string() string {
	return b.sb.String()
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

const stringScanChunkSize = 64

func scanBoundedCString(
	strtab []byte,
	off uint64,
	acc *inputAccounting,
	fieldName string,
	requireNonEmpty bool,
) (string, error) {
	strtabSize := uint64(len(strtab))
	if off >= strtabSize {
		return "", fmt.Errorf("%w: %s string offset %d out of bounds for STRTAB size %d", ErrABIMetadata, fieldName, off, strtabSize)
	}

	curr := off
	var scanned uint64
	nulFound := false
	var strLen uint64

	for curr < strtabSize {
		availInTable := strtabSize - curr
		wantScan := min(uint64(stringScanChunkSize), availInTable)
		remScan := acc.remainingStringScanBudget()
		remLocal := acc.remainingMetadataBudget()
		var remShared uint64 = math.MaxUint64
		if acc.shared != nil {
			remShared = acc.shared.remaining()
		}
		toCharge := min(wantScan, remScan, remLocal, remShared)
		if toCharge == 0 {
			return "", acc.checkAndChargeStringScan(1)
		}
		if err := acc.checkAndChargeStringScan(toCharge); err != nil {
			return "", err
		}

		idx := bytes.IndexByte(strtab[curr:curr+toCharge], 0)
		if idx >= 0 {
			nulFound = true
			bytesScanned := uint64(idx + 1)
			if toCharge > bytesScanned {
				acc.refundAllStringScan(toCharge - bytesScanned)
			}
			strLen = scanned + uint64(idx)
			break
		}

		scanned += toCharge
		curr += toCharge
		if toCharge < wantScan {
			return "", acc.checkAndChargeStringScan(1)
		}
	}

	if !nulFound {
		return "", fmt.Errorf("%w: %s string at offset %d is not NUL-terminated", ErrABIMetadata, fieldName, off)
	}
	if requireNonEmpty && strLen == 0 {
		return "", fmt.Errorf("%w: %s string at offset %d is empty", ErrABIMetadata, fieldName, off)
	}
	if err := acc.chargeString(strLen); err != nil {
		return "", err
	}
	return string(strtab[off : off+strLen]), nil
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
	return InspectELFABIWithAccounting(data, nil)
}

// InspectELFABIWithAccounting parses declared ABI metadata from raw ELF binary bytes, charging against a shared artifact account.
func InspectELFABIWithAccounting(data []byte, shared *ArtifactMetadataAccounting) (*VariantABIReport, error) {
	acc := &inputAccounting{shared: shared}
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
		if err := acc.chargeMetadata(interpProg.filesz); err != nil {
			return nil, err
		}
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
			if tags.hasStrtab && tags.strtabVaddr != dVal {
				return nil, fmt.Errorf("%w: conflicting duplicate DT_STRTAB entries (0x%x vs 0x%x)", ErrABIMetadata, tags.strtabVaddr, dVal)
			}
			tags.strtabVaddr = dVal
			tags.hasStrtab = true
		case uint64(elf.DT_STRSZ):
			if tags.hasStrsz && tags.strtabSize != dVal {
				return nil, fmt.Errorf("%w: conflicting duplicate DT_STRSZ entries (%d vs %d)", ErrABIMetadata, tags.strtabSize, dVal)
			}
			tags.strtabSize = dVal
			tags.hasStrsz = true
		case uint64(elf.DT_VERNEED):
			if tags.hasVerneed && tags.verneedVaddr != dVal {
				return nil, fmt.Errorf("%w: conflicting duplicate DT_VERNEED entries (0x%x vs 0x%x)", ErrABIMetadata, tags.verneedVaddr, dVal)
			}
			tags.verneedVaddr = dVal
			tags.hasVerneed = true
		case uint64(elf.DT_VERNEEDNUM):
			if tags.hasVerneedNum && tags.verneedNum != dVal {
				return nil, fmt.Errorf("%w: conflicting duplicate DT_VERNEEDNUM entries (%d vs %d)", ErrABIMetadata, tags.verneedNum, dVal)
			}
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
	if uint64(len(strtab)) > strtabSize {
		strtab = strtab[:strtabSize]
	}
	if uint64(len(neededOffsets)) > MaxDynamicEntries {
		return nil, fmt.Errorf("%w: needed dependencies count %d exceeds budget %d",
			ErrABIResourceLimit, len(neededOffsets), MaxDynamicEntries)
	}
	deps := make([]string, 0, len(neededOffsets))
	for _, off := range neededOffsets {
		depName, err := scanBoundedCString(strtab, off, acc, "DT_NEEDED", true)
		if err != nil {
			return nil, err
		}
		deps = append(deps, depName)
	}
	return deps, nil
}

func validateVersionTagPairing(tags *dynamicTags) error {
	switch {
	case !tags.hasVerneed && !tags.hasVerneedNum:
		return nil
	case tags.hasVerneed && !tags.hasVerneedNum:
		return fmt.Errorf("%w: DT_VERNEED tag present without DT_VERNEEDNUM", ErrABIMetadata)
	case !tags.hasVerneed && tags.hasVerneedNum:
		return fmt.Errorf("%w: DT_VERNEEDNUM tag present without DT_VERNEED", ErrABIMetadata)
	default:
		return nil
	}
}

func resolveDynamicStrtab(
	data []byte,
	loads []loadSegment,
	fileSize uint64,
	tags *dynamicTags,
	acc *inputAccounting,
) ([]byte, error) {
	needsStrtab := len(tags.neededOffsets) > 0 || (tags.hasVerneed && tags.verneedNum > 0)
	if !needsStrtab {
		return nil, nil
	}
	if !tags.hasStrtab || !tags.hasStrsz {
		return nil, fmt.Errorf("%w: dynamic dependencies or version requirements declared without DT_STRTAB or DT_STRSZ", ErrABIMetadata)
	}
	if tags.strtabSize > MaxABIStringBytesPerInput {
		return nil, fmt.Errorf("%w: dynamic STRTAB size %d exceeds budget limit %d",
			ErrABIResourceLimit, tags.strtabSize, MaxABIStringBytesPerInput)
	}
	if err := acc.chargeMetadata(tags.strtabSize); err != nil {
		return nil, err
	}
	strtabOff, tErr := translateVaddr(loads, fileSize, tags.strtabVaddr, tags.strtabSize)
	if tErr != nil {
		return nil, fmt.Errorf("%w: translating DT_STRTAB address: %w", ErrABIMetadata, tErr)
	}
	return data[strtabOff : strtabOff+tags.strtabSize], nil
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

	if err := validateVersionTagPairing(tags); err != nil {
		return err
	}

	strtab, err := resolveDynamicStrtab(data, loads, fileSize, tags, acc)
	if err != nil {
		return err
	}

	deps, err := parseNeededDependencies(strtab, tags.strtabSize, tags.neededOffsets, acc)
	if err != nil {
		return err
	}
	report.Dependencies = deps

	if tags.hasVerneed && tags.hasVerneedNum {
		if tags.verneedNum == 0 {
			report.VersionRequirements = []VersionRequirement{}
			return nil
		}
		if !tags.hasStrtab || !tags.hasStrsz {
			return fmt.Errorf("%w: dynamic version requirements declared without DT_STRTAB or DT_STRSZ", ErrABIMetadata)
		}
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
	parentVerneedOff uint64,
	libName string,
	strtab []byte,
	strtabSize uint64,
	acc *inputAccounting,
	report *VariantABIReport,
) error {
	if uint64(len(strtab)) > strtabSize {
		strtab = strtab[:strtabSize]
	}
	visitedVernaux := make(map[uint64]bool)

	for auxIdx := range vnCnt {
		if err := acc.chargeVersionRecord(); err != nil {
			return err
		}
		if err := acc.chargeMetadata(uint64(vernauxEntrySize)); err != nil {
			return err
		}
		maxVernaux := (verneedOff + availableBytes) - uint64(vernauxEntrySize)
		if availableBytes < uint64(vernauxEntrySize) || currVernauxOff < verneedOff || currVernauxOff > maxVernaux {
			return fmt.Errorf("%w: Elf64_Vernaux entry %d offset out of bounds", ErrABIMetadata, auxIdx)
		}
		if currVernauxOff < parentVerneedOff+uint64(verneedEntrySize) && currVernauxOff+uint64(vernauxEntrySize) > parentVerneedOff {
			return fmt.Errorf("%w: Elf64_Vernaux entry %d overlaps containing Elf64_Verneed parent record", ErrABIMetadata, auxIdx)
		}
		if visitedVernaux[currVernauxOff] {
			return fmt.Errorf("%w: cycle detected in Elf64_Vernaux chain at offset 0x%x", ErrABIMetadata, currVernauxOff)
		}
		visitedVernaux[currVernauxOff] = true

		vnaFlags := order.Uint16(data[currVernauxOff+4 : currVernauxOff+6])
		vnaName := order.Uint32(data[currVernauxOff+8 : currVernauxOff+12])
		vnaNext := order.Uint32(data[currVernauxOff+12 : currVernauxOff+16])

		verName, err := scanBoundedCString(strtab, uint64(vnaName), acc, "vna_name", true)
		if err != nil {
			return err
		}

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
		} else if vnaNext != 0 {
			return fmt.Errorf("%w: contradictory termination of Elf64_Vernaux chain: vna_next != 0 on last entry", ErrABIMetadata)
		}
	}
	return nil
}

func advanceVerneedOffset(
	recordIdx, verneedNum uint64,
	currVerneedOff uint64,
	vnNext uint32,
	verneedOff, availableBytes uint64,
) (uint64, error) {
	if recordIdx < verneedNum-1 {
		if vnNext == 0 {
			return 0, fmt.Errorf("%w: premature termination of Elf64_Verneed chain (expected %d records, stopped at %d)",
				ErrABIMetadata, verneedNum, recordIdx+1)
		}
		nextOff, aErr := advanceELFRelativeOffset(currVerneedOff, vnNext, verneedOff, verneedOff+availableBytes, verneedEntrySize)
		if aErr != nil {
			return 0, fmt.Errorf("%w: Elf64_Verneed entry %d offset: %w", ErrABIMetadata, recordIdx+1, aErr)
		}
		return nextOff, nil
	}
	if vnNext != 0 {
		return 0, fmt.Errorf("%w: contradictory termination of Elf64_Verneed chain: vn_next != 0 on last record", ErrABIMetadata)
	}
	return currVerneedOff, nil
}

func validateVerneedAuxOffset(
	vnCnt uint16,
	vnAux uint32,
	recordIdx uint64,
	currVerneedOff, verneedOff, availableBytes uint64,
) error {
	if vnCnt == 0 {
		return nil
	}
	if uint64(vnAux) < uint64(verneedEntrySize) {
		return fmt.Errorf("%w: Elf64_Verneed entry %d has invalid vn_aux %d overlapping parent record",
			ErrABIMetadata, recordIdx, vnAux)
	}
	remBytes := (verneedOff + availableBytes) - currVerneedOff
	if remBytes < uint64(vernauxEntrySize) || uint64(vnAux) > remBytes-uint64(vernauxEntrySize) {
		return fmt.Errorf("%w: Elf64_Verneed entry %d auxiliary offset %d out of bounds",
			ErrABIMetadata, recordIdx, vnAux)
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
	if verneedNum > 0 {
		if err := acc.checkMetadata(uint64(verneedEntrySize)); err != nil {
			return err
		}
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

	visitedVerneed := make(map[uint64]bool)
	currVerneedOff := verneedOff

	for recordIdx := range verneedNum {
		if err := acc.chargeVersionRecord(); err != nil {
			return err
		}
		if err := acc.chargeMetadata(uint64(verneedEntrySize)); err != nil {
			return err
		}
		maxVerneed := (verneedOff + availableBytes) - uint64(verneedEntrySize)
		if availableBytes < uint64(verneedEntrySize) || currVerneedOff < verneedOff || currVerneedOff > maxVerneed {
			return fmt.Errorf("%w: Elf64_Verneed entry %d offset out of bounds", ErrABIMetadata, recordIdx)
		}
		if visitedVerneed[currVerneedOff] {
			return fmt.Errorf("%w: cycle detected in Elf64_Verneed chain at offset 0x%x", ErrABIMetadata, currVerneedOff)
		}
		visitedVerneed[currVerneedOff] = true

		vnVersion := order.Uint16(data[currVerneedOff : currVerneedOff+2])
		if vnVersion != 1 {
			report.Completeness = MetadataUnsupported
			vnWarn := fmt.Sprintf("unsupported Elf64_Verneed version %d", vnVersion)
			if wErr := acc.chargeString(uint64(len(vnWarn))); wErr != nil {
				return wErr
			}
			report.Warnings = append(report.Warnings, vnWarn)
			break
		}
		vnCnt := order.Uint16(data[currVerneedOff+2 : currVerneedOff+4])
		vnFile := order.Uint32(data[currVerneedOff+4 : currVerneedOff+8])
		vnAux := order.Uint32(data[currVerneedOff+8 : currVerneedOff+12])
		vnNext := order.Uint32(data[currVerneedOff+12 : currVerneedOff+16])

		// Validate auxiliary offset structurally before decoding names when vnCnt > 0
		if err := validateVerneedAuxOffset(vnCnt, vnAux, recordIdx, currVerneedOff, verneedOff, availableBytes); err != nil {
			return err
		}

		libName, err := scanBoundedCString(strtab, uint64(vnFile), acc, "vn_file", true)
		if err != nil {
			return err
		}

		if vnCnt > 0 {
			currVernauxOff := currVerneedOff + uint64(vnAux)
			if err := parseVernauxChain(
				data, order, verneedOff, availableBytes, currVernauxOff,
				vnCnt, currVerneedOff, libName, strtab, uint64(len(strtab)), acc, report,
			); err != nil {
				return err
			}
		}

		nextOff, err := advanceVerneedOffset(recordIdx, verneedNum, currVerneedOff, vnNext, verneedOff, availableBytes)
		if err != nil {
			return err
		}
		currVerneedOff = nextOff
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
	ra := NewReportAccounting(MaxArtifactReportBytes)
	return validateReportBudgetWithLimits(reports, ra, defaultABILimits)
}

func validateReportBudgetWithLimits(reports []*VariantABIReport, ra *ReportAccounting, limits ABILimits) error {
	for _, r := range reports {
		if r == nil {
			return fmt.Errorf("%w: nil variant ABI report provided", ErrABIMetadata)
		}
		if uint64(len(r.Interpreter)) > limits.MaxInterpreterSize {
			return fmt.Errorf("%w: variant %s interpreter path length %d exceeds budget %d",
				ErrABIResourceLimit, r.Level, len(r.Interpreter), limits.MaxInterpreterSize)
		}
		if uint64(len(r.Dependencies)) > limits.MaxDynamicEntries {
			return fmt.Errorf("%w: variant %s dependency count %d exceeds budget %d",
				ErrABIResourceLimit, r.Level, len(r.Dependencies), limits.MaxDynamicEntries)
		}
		if uint64(len(r.VersionRequirements)) > limits.MaxVersionRecordsPerInput {
			return fmt.Errorf("%w: variant %s version requirement count %d exceeds budget %d",
				ErrABIResourceLimit, r.Level, len(r.VersionRequirements), limits.MaxVersionRecordsPerInput)
		}
		var inputStringBytes uint64
		inputStringBytes += uint64(len(r.Interpreter))
		for _, d := range r.Dependencies {
			inputStringBytes += uint64(len(d))
		}
		for _, v := range r.VersionRequirements {
			inputStringBytes += uint64(len(v.Library)) + uint64(len(v.Version))
		}
		if inputStringBytes > limits.MaxABIStringBytesPerInput {
			return fmt.Errorf("%w: variant %s decoded string bytes %d exceeds per-input budget %d",
				ErrABIResourceLimit, r.Level, inputStringBytes, limits.MaxABIStringBytesPerInput)
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
		if err := ra.reserve(uint64(reportBytes)); err != nil {
			return err
		}
	}
	return nil
}

func formatBoundedDepList(deps []string, maxItems int) string {
	if len(deps) == 0 {
		return "none"
	}
	n := len(deps)
	if n > maxItems {
		n = maxItems
	}
	escaped := make([]string, n)
	for i := 0; i < n; i++ {
		escaped[i] = EscapeMetadata(deps[i])
	}
	res := strings.Join(escaped, ", ")
	if len(deps) > maxItems {
		res += fmt.Sprintf(" ... (+%d more)", len(deps)-maxItems)
	}
	return "[" + res + "]"
}

func formatDependencySetDiff(
	ra *ReportAccounting,
	baseline, current *VariantABIReport,
	missingInCurr, missingInBase []string,
) (string, error) {
	const maxDisplayItems = 8
	b := newBoundedReportBuilder(ra)
	if err := b.writeString("differing declared dependencies between variant "); err != nil {
		return "", err
	}
	if err := b.writeEscaped(baseline.Level); err != nil {
		return "", err
	}
	if err := b.writeString(" and "); err != nil {
		return "", err
	}
	if err := b.writeEscaped(current.Level); err != nil {
		return "", err
	}
	if err := b.writeString(": missing in "); err != nil {
		return "", err
	}
	if err := b.writeEscaped(current.Level); err != nil {
		return "", err
	}
	if err := b.writeString(": "); err != nil {
		return "", err
	}
	if err := b.writeBoundedDepList(missingInCurr, maxDisplayItems); err != nil {
		return "", err
	}
	if err := b.writeString("; missing in "); err != nil {
		return "", err
	}
	if err := b.writeEscaped(baseline.Level); err != nil {
		return "", err
	}
	if err := b.writeString(": "); err != nil {
		return "", err
	}
	if err := b.writeBoundedDepList(missingInBase, maxDisplayItems); err != nil {
		return "", err
	}
	return b.string(), nil
}

func formatDependencyOrderWarning(ra *ReportAccounting, baseline, current *VariantABIReport) (string, error) {
	b := newBoundedReportBuilder(ra)
	if err := b.writeString("declared dependency search order differs between variant "); err != nil {
		return "", err
	}
	if err := b.writeEscaped(baseline.Level); err != nil {
		return "", err
	}
	if err := b.writeString(" and "); err != nil {
		return "", err
	}
	if err := b.writeEscaped(current.Level); err != nil {
		return "", err
	}
	if err := b.writeString(" (may affect dynamic symbol resolution order)"); err != nil {
		return "", err
	}
	return b.string(), nil
}

func compareDependencySets(ra *ReportAccounting, baseline, current *VariantABIReport) (string, string, error) {
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

	var missingInCurr []string
	for _, d := range baseDeps {
		if !currSet[d] {
			missingInCurr = append(missingInCurr, d)
		}
	}
	var missingInBase []string
	for _, d := range currDeps {
		if !baseSet[d] {
			missingInBase = append(missingInBase, d)
		}
	}

	if len(missingInCurr) > 0 || len(missingInBase) > 0 {
		diff, err := formatDependencySetDiff(ra, baseline, current, missingInCurr, missingInBase)
		if err != nil {
			return "", "", err
		}
		return diff, "", nil
	}

	if len(baseDeps) == len(currDeps) {
		for idx := range baseDeps {
			if baseDeps[idx] != currDeps[idx] {
				warn, err := formatDependencyOrderWarning(ra, baseline, current)
				if err != nil {
					return "", "", err
				}
				return "", warn, nil
			}
		}
	}
	return "", "", nil
}

func evaluateVariantUncertainty(rep *VariantABIReport) (isSkipped, isUnknown bool) {
	if rep == nil {
		return false, false
	}
	switch rep.Completeness {
	case MetadataSkipped:
		return true, false
	case MetadataPartial, MetadataUnsupported:
		return false, true
	}
	if rep.Linkage == LinkageAmbiguous {
		return false, true
	}
	return false, false
}

func formatAmbiguousLinkageWarning(ra *ReportAccounting, rep *VariantABIReport) (string, error) {
	b := newBoundedReportBuilder(ra)
	if err := b.writeString("variant "); err != nil {
		return "", err
	}
	if err := b.writeEscaped(rep.Level); err != nil {
		return "", err
	}
	if err := b.writeString(
		" has ambiguous linkage (dynamic dependencies declared without PT_INTERP); " +
			"complete compatibility cannot be verified",
	); err != nil {
		return "", err
	}
	return b.string(), nil
}

func formatIncompleteMetadataWarning(ra *ReportAccounting, rep *VariantABIReport) (string, error) {
	b := newBoundedReportBuilder(ra)
	if err := b.writeString("variant "); err != nil {
		return "", err
	}
	if err := b.writeEscaped(rep.Level); err != nil {
		return "", err
	}
	if err := b.writeString(" has partial or unsupported version metadata ("); err != nil {
		return "", err
	}
	if err := b.writeString(string(rep.Completeness)); err != nil {
		return "", err
	}
	if err := b.writeString("); complete symbol version compatibility cannot be verified"); err != nil {
		return "", err
	}
	return b.string(), nil
}

func isVersionKnown(r *VariantABIReport) bool {
	return r.Completeness != MetadataPartial &&
		r.Completeness != MetadataUnsupported &&
		r.Completeness != MetadataSkipped
}

// CompareVariantABIs evaluates declared ABI consistency across variant reports using the uniform ABI policy.
func CompareVariantABIs(reports []*VariantABIReport, allowMixedABI bool) (*ArtifactABIReport, error) {
	return CompareVariantABIsWithOptions(reports, allowMixedABI, defaultABILimits)
}

func handleSingleReport(r *VariantABIReport, ra *ReportAccounting, artifactReport *ArtifactABIReport) (*ArtifactABIReport, error) {
	isSkipped, isUnknown := evaluateVariantUncertainty(r)
	switch {
	case isSkipped:
		artifactReport.Status = ComparisonSkipped
		artifactReport.Consistent = false
	case isUnknown:
		artifactReport.Status = ComparisonUnknown
		artifactReport.Consistent = false
		if r.Completeness == MetadataPartial || r.Completeness == MetadataUnsupported {
			warn, err := formatIncompleteMetadataWarning(ra, r)
			if err != nil {
				return nil, err
			}
			artifactReport.Warnings = append(artifactReport.Warnings, warn)
		}
		if r.Linkage == LinkageAmbiguous {
			hasLinkageWarn := false
			for _, w := range r.Warnings {
				if strings.Contains(w, "PT_INTERP") || strings.Contains(w, "ambiguous") {
					hasLinkageWarn = true
					break
				}
			}
			if !hasLinkageWarn {
				warn, err := formatAmbiguousLinkageWarning(ra, r)
				if err != nil {
					return nil, err
				}
				artifactReport.Warnings = append(artifactReport.Warnings, warn)
			}
		}
	default:
		artifactReport.Status = ComparisonConsistent
		artifactReport.Consistent = true
	}
	for _, w := range r.Warnings {
		if err := ra.reserve(uint64(len(w))); err != nil {
			return nil, err
		}
		artifactReport.Warnings = append(artifactReport.Warnings, w)
	}
	return artifactReport, nil
}

func compareVariantLinkages(reports []*VariantABIReport, ra *ReportAccounting) ([]string, error) {
	var firstStatic *VariantABIReport
	var firstDynamic *VariantABIReport
	for _, r := range reports {
		if r.Linkage == LinkageStatic || r.Linkage == LinkageStaticPIE {
			if firstStatic == nil {
				firstStatic = r
			}
		} else if r.Linkage == LinkageDynamic {
			if firstDynamic == nil {
				firstDynamic = r
			}
		}
	}
	if firstStatic != nil && firstDynamic != nil {
		b := newBoundedReportBuilder(ra)
		if err := b.writeString("mixed static and dynamic payloads: variant "); err != nil {
			return nil, err
		}
		if err := b.writeEscaped(firstStatic.Level); err != nil {
			return nil, err
		}
		if err := b.writeString(" is "); err != nil {
			return nil, err
		}
		if err := b.writeString(string(firstStatic.Linkage)); err != nil {
			return nil, err
		}
		if err := b.writeString(", variant "); err != nil {
			return nil, err
		}
		if err := b.writeEscaped(firstDynamic.Level); err != nil {
			return nil, err
		}
		if err := b.writeString(" is "); err != nil {
			return nil, err
		}
		if err := b.writeString(string(firstDynamic.Linkage)); err != nil {
			return nil, err
		}
		return []string{b.string()}, nil
	}
	return nil, nil
}

func formatInterpreterConfigDiff(ra *ReportAccounting, interpRef, r *VariantABIReport) (string, error) {
	b := newBoundedReportBuilder(ra)
	if err := b.writeString("differing interpreter configuration: variant "); err != nil {
		return "", err
	}
	if err := b.writeEscaped(interpRef.Level); err != nil {
		return "", err
	}
	if err := b.writeString(" has interpreter ("); err != nil {
		return "", err
	}
	if err := b.writeQuotedEscaped(interpRef.Interpreter); err != nil {
		return "", err
	}
	if err := b.writeString("), variant "); err != nil {
		return "", err
	}
	if err := b.writeEscaped(r.Level); err != nil {
		return "", err
	}
	if err := b.writeString(" does not"); err != nil {
		return "", err
	}
	return b.string(), nil
}

func formatInterpreterPathDiff(ra *ReportAccounting, interpRef, r *VariantABIReport) (string, error) {
	b := newBoundedReportBuilder(ra)
	if err := b.writeString("differing interpreter pathnames: variant "); err != nil {
		return "", err
	}
	if err := b.writeEscaped(interpRef.Level); err != nil {
		return "", err
	}
	if err := b.writeString(" requires "); err != nil {
		return "", err
	}
	if err := b.writeQuotedEscaped(interpRef.Interpreter); err != nil {
		return "", err
	}
	if err := b.writeString(", variant "); err != nil {
		return "", err
	}
	if err := b.writeEscaped(r.Level); err != nil {
		return "", err
	}
	if err := b.writeString(" requires "); err != nil {
		return "", err
	}
	if err := b.writeQuotedEscaped(r.Interpreter); err != nil {
		return "", err
	}
	return b.string(), nil
}

func compareVariantInterpreters(reports []*VariantABIReport, ra *ReportAccounting) ([]string, error) {
	var differences []string
	var interpRef *VariantABIReport
	for _, r := range reports {
		if r.Linkage == LinkageDynamic || (r.Linkage == "" && (r.HasInterpreter || r.Interpreter != "")) {
			if interpRef == nil {
				interpRef = r
				continue
			}
			hasInterp1 := interpRef.HasInterpreter || interpRef.Interpreter != ""
			hasInterp2 := r.HasInterpreter || r.Interpreter != ""
			if hasInterp1 != hasInterp2 {
				diff, err := formatInterpreterConfigDiff(ra, interpRef, r)
				if err != nil {
					return nil, err
				}
				differences = append(differences, diff)
			} else if hasInterp1 && hasInterp2 && interpRef.Interpreter != r.Interpreter {
				diff, err := formatInterpreterPathDiff(ra, interpRef, r)
				if err != nil {
					return nil, err
				}
				differences = append(differences, diff)
			}
		}
	}
	return differences, nil
}

func isDependencyKnown(r *VariantABIReport) bool {
	if r == nil || r.Completeness == MetadataSkipped {
		return false
	}
	if r.Linkage == LinkageDynamic || r.Linkage == LinkageAmbiguous ||
		r.Linkage == LinkageStatic || r.Linkage == LinkageStaticPIE {
		return true
	}
	return len(r.Dependencies) > 0 || r.InspectionSource != ""
}

func compareVariantDependencies(reports []*VariantABIReport, ra *ReportAccounting) ([]string, []string, error) {
	var differences []string
	var warnings []string
	var depRef *VariantABIReport
	for _, r := range reports {
		if isDependencyKnown(r) {
			if depRef == nil {
				depRef = r
				continue
			}
			depDiff, depWarn, err := compareDependencySets(ra, depRef, r)
			if err != nil {
				return nil, nil, err
			}
			if depDiff != "" {
				differences = append(differences, depDiff)
			}
			if depWarn != "" {
				warnings = append(warnings, depWarn)
			}
		}
	}
	return differences, warnings, nil
}

func compareVariantVersions(reports []*VariantABIReport, ra *ReportAccounting) ([]string, error) {
	var differences []string
	var verRef *VariantABIReport
	var normVerRef []VersionRequirement
	for _, r := range reports {
		if isVersionKnown(r) {
			if verRef == nil {
				verRef = r
				normVerRef = normalizeVersionRequirements(r.VersionRequirements)
				continue
			}
			normCurr := normalizeVersionRequirements(r.VersionRequirements)
			if !versionRequirementsEqual(normVerRef, normCurr) {
				b := newBoundedReportBuilder(ra)
				if err := b.writeString("differing symbol version requirements: variant "); err != nil {
					return nil, err
				}
				if err := b.writeEscaped(verRef.Level); err != nil {
					return nil, err
				}
				if err := b.writeString(" ("); err != nil {
					return nil, err
				}
				if err := b.writeInt(len(verRef.VersionRequirements)); err != nil {
					return nil, err
				}
				if err := b.writeString(" requirements) vs variant "); err != nil {
					return nil, err
				}
				if err := b.writeEscaped(r.Level); err != nil {
					return nil, err
				}
				if err := b.writeString(" ("); err != nil {
					return nil, err
				}
				if err := b.writeInt(len(r.VersionRequirements)); err != nil {
					return nil, err
				}
				if err := b.writeString(" requirements)"); err != nil {
					return nil, err
				}
				differences = append(differences, b.string())
			}
		}
	}
	return differences, nil
}

type abiMismatchError struct {
	msg string
}

func (e *abiMismatchError) Error() string {
	return e.msg
}

func (e *abiMismatchError) Unwrap() error {
	return ErrABIMismatch
}

func formatMismatchError(differences []string, ra *ReportAccounting) error {
	var needed uint64 = uint64(len(ErrABIMismatch.Error()))
	for _, d := range differences {
		needed += uint64(len(d) + mismatchDiffBulletOverhead)
	}
	if err := ra.reserve(needed); err != nil {
		return fmt.Errorf("%w: %d declared differences (detailed output omitted: %w)",
			ErrABIMismatch, len(differences), err)
	}
	var sb strings.Builder
	if needed <= math.MaxInt {
		sb.Grow(int(needed))
	}
	sb.WriteString(ErrABIMismatch.Error())
	for _, d := range differences {
		sb.WriteString("\n  • ")
		sb.WriteString(d)
	}
	return &abiMismatchError{msg: sb.String()}
}

func evaluateAllReportsCompleteness(
	reports []*VariantABIReport,
	ra *ReportAccounting,
) (hasSkipped, hasUnknown bool, warnings []string, err error) {
	for _, r := range reports {
		isSkipped, isUnknownVar := evaluateVariantUncertainty(r)
		if isSkipped {
			hasSkipped = true
		}
		if isUnknownVar {
			hasUnknown = true
			if r.Completeness == MetadataPartial || r.Completeness == MetadataUnsupported {
				warn, wErr := formatIncompleteMetadataWarning(ra, r)
				if wErr != nil {
					return false, false, nil, wErr
				}
				warnings = append(warnings, warn)
			}
			if r.Linkage == LinkageAmbiguous {
				hasLinkageWarn := false
				for _, w := range r.Warnings {
					if strings.Contains(w, "PT_INTERP") || strings.Contains(w, "ambiguous") {
						hasLinkageWarn = true
						break
					}
				}
				if !hasLinkageWarn {
					warn, wErr := formatAmbiguousLinkageWarning(ra, r)
					if wErr != nil {
						return false, false, nil, wErr
					}
					warnings = append(warnings, warn)
				}
			}
		}
		for _, w := range r.Warnings {
			if rErr := ra.reserve(uint64(len(w))); rErr != nil {
				return false, false, nil, rErr
			}
			warnings = append(warnings, w)
		}
	}
	return hasSkipped, hasUnknown, warnings, nil
}

func collectVariantDifferences(
	reports []*VariantABIReport,
	ra *ReportAccounting,
) (differences, warnings []string, err error) {
	diffs, err := compareVariantLinkages(reports, ra)
	if err != nil {
		return nil, nil, err
	}
	differences = append(differences, diffs...)

	interpDiffs, err := compareVariantInterpreters(reports, ra)
	if err != nil {
		return nil, nil, err
	}
	differences = append(differences, interpDiffs...)

	depDiffs, depWarns, err := compareVariantDependencies(reports, ra)
	if err != nil {
		return nil, nil, err
	}
	differences = append(differences, depDiffs...)
	warnings = append(warnings, depWarns...)

	verDiffs, err := compareVariantVersions(reports, ra)
	if err != nil {
		return nil, nil, err
	}
	differences = append(differences, verDiffs...)

	return differences, warnings, nil
}

func applyOverriddenDifferences(
	artifactReport *ArtifactABIReport,
	differences []string,
	ra *ReportAccounting,
) error {
	artifactReport.Overridden = true
	for _, d := range differences {
		b := newBoundedReportBuilder(ra)
		if err := b.writeString("[OVERRIDDEN] "); err != nil {
			return err
		}
		if err := b.writeString(d); err != nil {
			return err
		}
		artifactReport.Warnings = append(artifactReport.Warnings, b.string())
	}
	return nil
}

// CompareVariantABIsWithOptions evaluates declared ABI consistency across variant reports with custom limits.
func CompareVariantABIsWithOptions(reports []*VariantABIReport, allowMixedABI bool, limits ABILimits) (*ArtifactABIReport, error) {
	ra := NewReportAccounting(limits.MaxArtifactReportBytes)
	if err := validateReportBudgetWithLimits(reports, ra, limits); err != nil {
		return nil, err
	}

	const deploymentDisclaimer = "Matching declared ABI requirements does not guarantee deployment host compatibility; " +
		"the target system must provide the required dynamic linker and libraries."

	artifactReport := &ArtifactABIReport{
		Variants:             reports,
		Consistent:           true,
		Status:               ComparisonConsistent,
		DeploymentDisclaimer: deploymentDisclaimer,
	}
	if err := ra.reserve(uint64(len(deploymentDisclaimer))); err != nil {
		return nil, err
	}

	if len(reports) == 0 {
		presBytes, err := MeasureABIReportPresentation(artifactReport)
		if err != nil {
			return nil, err
		}
		if err := ra.reserve(presBytes); err != nil {
			return nil, err
		}
		return artifactReport, nil
	}
	if len(reports) == 1 {
		rep, err := handleSingleReport(reports[0], ra, artifactReport)
		if err != nil {
			return nil, err
		}
		presBytes, err := MeasureABIReportPresentation(rep)
		if err != nil {
			return nil, err
		}
		if err := ra.reserve(presBytes); err != nil {
			return nil, err
		}
		return rep, nil
	}

	hasSkipped, hasUnknown, warnings, err := evaluateAllReportsCompleteness(reports, ra)
	if err != nil {
		return nil, err
	}

	differences, depWarns, err := collectVariantDifferences(reports, ra)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, depWarns...)

	artifactReport.Differences = differences
	artifactReport.Warnings = warnings

	// Aggregate outcome table (Section 5.3)
	switch {
	case len(differences) > 0:
		artifactReport.Consistent = false
		artifactReport.Status = ComparisonInconsistent
		if !allowMixedABI {
			return artifactReport, formatMismatchError(differences, ra)
		}
		if err := applyOverriddenDifferences(artifactReport, differences, ra); err != nil {
			return nil, err
		}
	case hasSkipped:
		artifactReport.Status = ComparisonSkipped
		artifactReport.Consistent = false
	case hasUnknown:
		artifactReport.Status = ComparisonUnknown
		artifactReport.Consistent = false
	default:
		artifactReport.Status = ComparisonConsistent
		artifactReport.Consistent = true
	}

	presBytes, err := MeasureABIReportPresentation(artifactReport)
	if err != nil {
		return nil, err
	}
	if err := ra.reserve(presBytes); err != nil {
		return nil, err
	}

	return artifactReport, nil
}
