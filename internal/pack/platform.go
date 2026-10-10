package pack

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
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
)

type goBuildInfoAccounting struct {
	stringBytes int
	records     int
}

// validateELFPlatform examines declared platform evidence after executable load
// bounds have been validated. SysV ELF is common for Linux binaries and does
// not identify an operating system; explicit foreign OSABI values do.
func validateELFPlatform(data []byte, f *elf.File, targetOS string) error {
	if f.OSABI != elf.ELFOSABI_NONE && f.OSABI != elf.ELFOSABI_LINUX {
		return fmt.Errorf("ELF OSABI %s conflicts with target operating system %s", f.OSABI, targetOS)
	}
	loads := make([]loadSegment, 0, len(f.Progs))
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD {
			loads = append(loads, loadSegment{off: p.Off, vaddr: p.Vaddr, filesz: p.Filesz, memsz: p.Memsz})
		}
	}
	accounting := &goBuildInfoAccounting{}
	for _, p := range f.Progs {
		if p.Type != elf.PT_LOAD || p.Flags&elf.PF_W == 0 || p.Filesz < goBuildInfoHeaderSize {
			continue
		}
		found, err := checkGoBuildInfoSegment(data, loads, p, targetOS, accounting)
		if err != nil || found {
			return err
		}
	}
	return nil
}

// Search file-backed writable segments without consulting section names. This
// also works when section headers have been stripped and avoids debug/elf's
// eager expansion and copying of auxiliary section-name tables.
func checkGoBuildInfoSegment(
	data []byte, loads []loadSegment, p *elf.Prog, targetOS string, accounting *goBuildInfoAccounting,
) (bool, error) {
	segment := data[p.Off : p.Off+p.Filesz]
	start := (goBuildInfoAlignment - p.Vaddr%goBuildInfoAlignment) % goBuildInfoAlignment
	for start < uint64(len(segment)) {
		index := bytes.Index(segment[start:], []byte(goBuildInfoMagic))
		if index < 0 {
			break
		}
		offset := start + uint64(index)
		start = offset + uint64(len(goBuildInfoMagic))
		if (p.Vaddr+offset)%goBuildInfoAlignment != 0 || uint64(len(segment))-offset < goBuildInfoHeaderSize {
			continue
		}
		module, err := readGoBuildInfoModule(data, loads, segment[offset:])
		if err != nil {
			return false, err
		}
		found, err := checkGoBuildInfoTarget(module, targetOS, accounting)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

func readGoBuildInfoModule(data []byte, loads []loadSegment, blob []byte) ([]byte, error) {
	var version, module []byte
	var err error
	if blob[goBuildInfoFlagsOffset]&goBuildInfoInlineFlag != 0 {
		version, blob, err = readGoBuildInfoInlineString(blob[goBuildInfoHeaderSize:])
		if err == nil && len(version) != 0 {
			module, _, err = readGoBuildInfoInlineString(blob)
		}
	} else {
		pointerSize := blob[goBuildInfoPointerSize]
		if pointerSize != goBuildInfoSmallPtr && pointerSize != goBuildInfoLargePtr {
			return nil, nil
		}
		order := binary.ByteOrder(binary.LittleEndian)
		if blob[goBuildInfoFlagsOffset]&goBuildInfoEndianFlag != 0 {
			order = binary.BigEndian
		}
		version, err = readGoBuildInfoPointerString(data, loads, blob[goBuildInfoPointerOff:], pointerSize, order)
		if err == nil && len(version) != 0 {
			module, err = readGoBuildInfoPointerString(data, loads, blob[goBuildInfoPointerOff+pointerSize:], pointerSize, order)
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

func readGoBuildInfoInlineString(blob []byte) ([]byte, []byte, error) {
	length, prefix := binary.Uvarint(blob)
	if prefix <= 0 {
		return nil, nil, nil
	}
	if length > uint64(len(blob))-uint64(prefix) {
		return nil, nil, nil
	}
	if length > MaxABIStringBytesPerInput {
		return nil, nil, fmt.Errorf("%w: Go buildinfo string size %d exceeds budget limit %d",
			ErrABIResourceLimit, length, MaxABIStringBytesPerInput)
	}
	end := uint64(prefix) + length
	return blob[uint64(prefix):end], blob[end:], nil
}

func readGoBuildInfoPointerString(
	data []byte, loads []loadSegment, rawPointer []byte, pointerSize uint8, order binary.ByteOrder,
) ([]byte, error) {
	headerAddr := readGoBuildInfoPointer(rawPointer, pointerSize, order)
	fileSize := uint64(len(data))
	headerSize := uint64(pointerSize) * goBuildInfoStringWord
	headerOff, err := translateVaddr(loads, fileSize, headerAddr, headerSize)
	if err != nil {
		return nil, nil
	}
	header := data[headerOff : headerOff+headerSize]
	address := readGoBuildInfoPointer(header, pointerSize, order)
	length := readGoBuildInfoPointer(header[pointerSize:], pointerSize, order)
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
	return data[offset : offset+length], nil
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
