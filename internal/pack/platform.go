package pack

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io"
	"runtime/debug"
)

const (
	goBuildInfoMagic       = "\xff Go buildinf:"
	goBuildInfoHeaderSize  = 32
	goBuildInfoAlignment   = 16
	goBuildInfoPointerSize = 14
	goBuildInfoFlagsOffset = 15
	goBuildInfoPointerOff  = 16
	goBuildInfoInlineFlag  = 2
	goBuildInfoEndianFlag  = 1
	goBuildInfoFrameSize   = 16
	goBuildInfoFrameStart  = "\x30\x77\xaf\x0c\x92\x74\x08\x02\x41\xe1\xc1\x07\xe6\xd6\x18\xe6"
	goBuildInfoFrameEnd    = "\xf9\x32\x43\x31\x86\x18\x20\x72\x00\x82\x42\x10\x41\x16\xd8\xf2"
	goBuildInfoTargetOSKey = "GOOS"
	goBuildInfoSmallPtr    = 4
	goBuildInfoLargePtr    = 8
	goBuildInfoStringWord  = 2
	goBuildInfoModuleTabs  = 2
	goBuildInfoScanWindow  = 64 * 1024
)

type goBuildInfoAccounting struct {
	stringBytes int
	records     int
}

// validateELFPlatform examines declared platform evidence after executable load
// bounds have been validated. SysV ELF is common for Linux binaries and does
// not identify an operating system; explicit foreign OSABI values do.
func validateELFPlatform(data []byte, f *elf.File, targetOS string) error {
	return validateELFPlatformReaderAt(bytes.NewReader(data), uint64(len(data)), f, targetOS)
}

func validateELFPlatformReaderAt(r io.ReaderAt, fileSize uint64, f *elf.File, targetOS string) error {
	if f.OSABI != elf.ELFOSABI_NONE && f.OSABI != elf.ELFOSABI_LINUX {
		return fmt.Errorf("ELF OSABI %s conflicts with target operating system %s", f.OSABI, targetOS)
	}
	loads := make([]loadSegment, 0, len(f.Progs))
	var maxWritable uint64
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD {
			loads = append(loads, loadSegment{off: p.Off, vaddr: p.Vaddr, filesz: p.Filesz, memsz: p.Memsz})
			if p.Flags&elf.PF_W != 0 && p.Filesz >= goBuildInfoHeaderSize && p.Filesz > maxWritable {
				maxWritable = p.Filesz
			}
		}
	}
	if maxWritable == 0 {
		return nil
	}
	scanBuf := make([]byte, min(maxWritable, uint64(goBuildInfoScanWindow)))
	accounting := &goBuildInfoAccounting{}
	for _, p := range f.Progs {
		if p.Type != elf.PT_LOAD || p.Flags&elf.PF_W == 0 || p.Filesz < goBuildInfoHeaderSize {
			continue
		}
		found, err := checkGoBuildInfoSegmentReaderAt(r, fileSize, loads, p, targetOS, accounting, scanBuf)
		if err != nil || found {
			return err
		}
	}
	return nil
}

// Search file-backed writable segments in bounded 64 KiB windows without consulting section names.
// This works when section headers have been stripped and avoids reading multi-megabyte .text/.rodata
// or eagerly allocating entire writable segments into heap buffers.
func checkGoBuildInfoSegmentReaderAt(
	r io.ReaderAt,
	fileSize uint64,
	loads []loadSegment,
	p *elf.Prog,
	targetOS string,
	accounting *goBuildInfoAccounting,
	scanBuf []byte,
) (bool, error) {
	segEnd := p.Off + p.Filesz
	var windowStart uint64
	for windowStart < p.Filesz {
		rem := p.Filesz - windowStart
		if rem < goBuildInfoHeaderSize {
			break
		}
		windowLen := min(uint64(len(scanBuf)), rem)
		window := scanBuf[:windowLen]
		if err := readIntoAt(r, window, p.Off+windowStart); err != nil {
			return false, err
		}

		maxRelStart := windowLen - goBuildInfoHeaderSize
		rel := (goBuildInfoAlignment - (p.Vaddr+windowStart)%goBuildInfoAlignment) % goBuildInfoAlignment
		for rel <= maxRelStart {
			idx := bytes.Index(window[rel:], []byte(goBuildInfoMagic))
			if idx < 0 {
				break
			}
			candRel := rel + uint64(idx)
			rel = candRel + uint64(len(goBuildInfoMagic))
			if candRel > maxRelStart {
				break
			}
			offset := windowStart + candRel
			if (p.Vaddr+offset)%goBuildInfoAlignment != 0 {
				continue
			}
			header := window[candRel : candRel+goBuildInfoHeaderSize]
			module, err := readGoBuildInfoModuleReaderAt(r, fileSize, loads, header, p.Off+offset+goBuildInfoHeaderSize, segEnd)
			if err != nil {
				return false, err
			}
			found, err := checkGoBuildInfoTarget(module, targetOS, accounting)
			if err != nil || found {
				return found, err
			}
		}

		if windowStart+windowLen >= p.Filesz {
			break
		}
		windowStart += windowLen - (goBuildInfoHeaderSize - 1)
	}
	return false, nil
}

func readGoBuildInfoModuleReaderAt(
	r io.ReaderAt,
	fileSize uint64,
	loads []loadSegment,
	header []byte,
	inlinePos uint64,
	segEnd uint64,
) ([]byte, error) {
	var version, module []byte
	var err error
	if header[goBuildInfoFlagsOffset]&goBuildInfoInlineFlag != 0 {
		var nextPos uint64
		version, nextPos, err = readGoBuildInfoInlineStringReaderAt(r, inlinePos, segEnd)
		if err == nil && len(version) != 0 {
			module, _, err = readGoBuildInfoInlineStringReaderAt(r, nextPos, segEnd)
		}
	} else {
		pointerSize := header[goBuildInfoPointerSize]
		if pointerSize != goBuildInfoSmallPtr && pointerSize != goBuildInfoLargePtr {
			return nil, nil
		}
		order := binary.ByteOrder(binary.LittleEndian)
		if header[goBuildInfoFlagsOffset]&goBuildInfoEndianFlag != 0 {
			order = binary.BigEndian
		}
		version, err = readGoBuildInfoPointerStringReaderAt(r, fileSize, loads, header[goBuildInfoPointerOff:], pointerSize, order)
		if err == nil && len(version) != 0 {
			module, err = readGoBuildInfoPointerStringReaderAt(
				r, fileSize, loads, header[goBuildInfoPointerOff+pointerSize:], pointerSize, order,
			)
		}
	}
	if err != nil {
		return nil, err
	}
	if len(version) == 0 {
		return nil, nil
	}
	if len(module) > MaxABIStringBytesPerInput-len(version) {
		return nil, fmt.Errorf("%w: Go buildinfo strings exceed budget limit %d", ErrABIResourceLimit, MaxABIStringBytesPerInput)
	}
	return module, nil
}

func readGoBuildInfoInlineStringReaderAt(r io.ReaderAt, pos, segEnd uint64) ([]byte, uint64, error) {
	if pos >= segEnd {
		return nil, 0, nil
	}
	var varintBuf [binary.MaxVarintLen64]byte
	avail := min(uint64(len(varintBuf)), segEnd-pos)
	if err := readIntoAt(r, varintBuf[:avail], pos); err != nil {
		return nil, 0, err
	}
	length, prefix := binary.Uvarint(varintBuf[:avail])
	if prefix <= 0 {
		return nil, 0, nil
	}
	remAfterPrefix := (segEnd - pos) - uint64(prefix)
	if length > remAfterPrefix {
		return nil, 0, nil
	}
	if length > MaxABIStringBytesPerInput {
		return nil, 0, fmt.Errorf("%w: Go buildinfo string size %d exceeds budget limit %d",
			ErrABIResourceLimit, length, MaxABIStringBytesPerInput)
	}
	strOff := pos + uint64(prefix)
	strBytes, err := readExactAt(r, strOff, length)
	if err != nil {
		return nil, 0, err
	}
	return strBytes, strOff + length, nil
}

func readGoBuildInfoPointerStringReaderAt(
	r io.ReaderAt, fileSize uint64, loads []loadSegment, rawPointer []byte, pointerSize uint8, order binary.ByteOrder,
) ([]byte, error) {
	headerAddr := readGoBuildInfoPointer(rawPointer, pointerSize, order)
	headerSize := uint64(pointerSize) * goBuildInfoStringWord
	headerOff, err := translateVaddr(loads, fileSize, headerAddr, headerSize)
	if err != nil {
		return nil, nil
	}
	var header [goBuildInfoLargePtr * goBuildInfoStringWord]byte
	if err := readIntoAt(r, header[:headerSize], headerOff); err != nil {
		return nil, err
	}
	address := readGoBuildInfoPointer(header[:headerSize], pointerSize, order)
	length := readGoBuildInfoPointer(header[pointerSize:headerSize], pointerSize, order)
	if length > MaxABIStringBytesPerInput {
		return nil, fmt.Errorf("%w: Go buildinfo string size %d exceeds budget limit %d",
			ErrABIResourceLimit, length, MaxABIStringBytesPerInput)
	}
	if length == 0 {
		return nil, nil
	}
	offset, err := translateVaddr(loads, fileSize, address, length)
	if err != nil {
		return nil, nil
	}
	return readExactAt(r, offset, length)
}

func readGoBuildInfoPointer(data []byte, pointerSize uint8, order binary.ByteOrder) uint64 {
	if pointerSize == goBuildInfoSmallPtr {
		return uint64(order.Uint32(data))
	}
	return order.Uint64(data)
}

func checkGoBuildInfoTarget(module []byte, targetOS string, accounting *goBuildInfoAccounting) (bool, error) {
	if len(module) <= goBuildInfoFrameSize*goBuildInfoStringWord ||
		!bytes.HasPrefix(module, []byte(goBuildInfoFrameStart)) || !bytes.HasSuffix(module, []byte(goBuildInfoFrameEnd)) {
		return false, nil
	}
	module = module[goBuildInfoFrameSize : len(module)-goBuildInfoFrameSize]
	if module[len(module)-1] != '\n' {
		return false, nil
	}
	// Bound cumulative parser input and record counts, including repeated legacy
	// pointers to the same data. Preflight columns before ParseBuildInfo can
	// allocate strings.Split results for malformed module lines with many tabs.
	if err := accounting.charge(module); err != nil {
		return false, err
	}
	if !validGoBuildInfoModuleColumns(module) {
		return false, nil
	}
	info, err := debug.ParseBuildInfo(string(module))
	if err != nil {
		return false, nil
	}
	for _, setting := range info.Settings {
		if setting.Key == goBuildInfoTargetOSKey && setting.Value != "" && setting.Value != targetOS {
			return true, fmt.Errorf("Go buildinfo GOOS %q conflicts with target operating system %s", setting.Value, targetOS)
		}
	}
	return true, nil
}

func (accounting *goBuildInfoAccounting) charge(module []byte) error {
	stringBytes := len(module)
	if stringBytes > MaxABIStringBytesPerInput-accounting.stringBytes {
		return fmt.Errorf("%w: cumulative Go buildinfo strings exceed budget limit %d",
			ErrABIResourceLimit, MaxABIStringBytesPerInput)
	}
	records := bytes.Count(module, []byte{'\n'})
	if records > MaxVersionRecordsPerInput-accounting.records {
		return fmt.Errorf("%w: cumulative Go buildinfo records exceed budget limit %d",
			ErrABIResourceLimit, MaxVersionRecordsPerInput)
	}
	accounting.stringBytes += stringBytes
	accounting.records += records
	return nil
}

func validGoBuildInfoModuleColumns(module []byte) bool {
	for len(module) > 0 {
		line, rest, _ := bytes.Cut(module, []byte{'\n'})
		module = rest
		switch {
		case bytes.HasPrefix(line, []byte("mod\t")), bytes.HasPrefix(line, []byte("dep\t")):
			columns := bytes.Count(line[len("mod\t"):], []byte{'\t'})
			if columns != 1 && columns != goBuildInfoModuleTabs {
				return false
			}
		case bytes.HasPrefix(line, []byte("=>\t")):
			if bytes.Count(line[len("=>\t"):], []byte{'\t'}) != goBuildInfoModuleTabs {
				return false
			}
		}
	}
	return true
}
