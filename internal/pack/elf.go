package pack

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

const (
	elfEhdrVersionOff = 20
	elfEhdrVersionEnd = 24
	elfEhdrEntryOff   = 24
	elfEhdrEntryEnd   = 32
	phFlagsFieldEnd   = 8
	phPaddrFieldOff   = 24
	phAlignFieldOff   = 48
	phAlignFieldEnd   = 56
)

// readExecutableELF reads only the headers used for executable validation.
// debug/elf.NewFile eagerly decompresses the section-name table and copies each
// section name, which can amplify a small input beyond the metadata budgets.
// Auxiliary section contents and names are not part of the execution contract.
func readExecutableELF(data []byte) (*elf.File, error) {
	return readExecutableELFReaderAt(bytes.NewReader(data), uint64(len(data)))
}

func readExecutableELFReaderAt(r io.ReaderAt, fileSize uint64) (*elf.File, error) {
	if err := PreflightELFHeadersReaderAt(r, fileSize); err != nil {
		return nil, err
	}
	headerBytes, err := readExactAt(r, 0, minELFHeaderSize)
	if err != nil {
		return nil, err
	}
	order := binary.ByteOrder(binary.LittleEndian)
	if headerBytes[elf.EI_DATA] == byte(elf.ELFDATA2MSB) {
		order = binary.BigEndian
	}
	version := order.Uint32(headerBytes[elfEhdrVersionOff:elfEhdrVersionEnd])
	if version != uint32(elf.EV_CURRENT) {
		return nil, fmt.Errorf("invalid ELF version %d", version)
	}
	phoff := order.Uint64(headerBytes[elfEhdrPhoffOff:elfEhdrPhoffEnd])
	if phoff > math.MaxInt64 {
		return nil, fmt.Errorf("invalid program header offset %d", phoff)
	}
	phnum := order.Uint16(headerBytes[elfEhdrPhnumOff:elfEhdrPhnumEnd])
	phentsize := order.Uint16(headerBytes[elfEhdrPhentsizeOff:elfEhdrPhentsizeEnd])
	f := &elf.File{
		FileHeader: elf.FileHeader{
			Class:      elf.Class(headerBytes[elf.EI_CLASS]),
			Data:       elf.Data(headerBytes[elf.EI_DATA]),
			Version:    elf.Version(version),
			OSABI:      elf.OSABI(headerBytes[elf.EI_OSABI]),
			ABIVersion: headerBytes[elf.EI_ABIVERSION],
			ByteOrder:  order,
			Type:       elf.Type(order.Uint16(headerBytes[elfEhdrTypeOff:elfEhdrTypeEnd])),
			Machine:    elf.Machine(order.Uint16(headerBytes[elfEhdrMachineOff:elfEhdrMachineEnd])),
			Entry:      order.Uint64(headerBytes[elfEhdrEntryOff:elfEhdrEntryEnd]),
		},
		Progs: make([]*elf.Prog, 0, phnum),
	}
	if phnum == 0 {
		return f, nil
	}
	phTableBytes, err := readExactAt(r, phoff, uint64(phnum)*uint64(phentsize))
	if err != nil {
		return nil, err
	}
	for i := range phnum {
		ent := phTableBytes[uint64(i)*uint64(phentsize):]
		pOff := order.Uint64(ent[phOffsetFieldOff:phVaddrFieldOff])
		pFilesz := order.Uint64(ent[phFileszFieldOff:phMemszFieldOff])
		if pOff > math.MaxInt64 || pFilesz > math.MaxInt64 {
			return nil, fmt.Errorf("invalid program offset or file size")
		}
		f.Progs = append(f.Progs, &elf.Prog{ProgHeader: elf.ProgHeader{
			Type:   elf.ProgType(order.Uint32(ent[:phTypeFieldEnd])),
			Flags:  elf.ProgFlag(order.Uint32(ent[phTypeFieldEnd:phFlagsFieldEnd])),
			Off:    pOff,
			Vaddr:  order.Uint64(ent[phVaddrFieldOff:phPaddrFieldOff]),
			Paddr:  order.Uint64(ent[phPaddrFieldOff:phFileszFieldOff]),
			Filesz: pFilesz,
			Memsz:  order.Uint64(ent[phMemszFieldOff:phAlignFieldOff]),
			Align:  order.Uint64(ent[phAlignFieldOff:phAlignFieldEnd]),
		}})
	}
	return f, nil
}

// Validate executable structure, including PIE/static PIE (ET_DYN), without
// claiming cross-variant interpreter or libc compatibility.
func validateExecutableELF(f *elf.File, size uint64) error {
	if f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return fmt.Errorf("expected executable ELF type ET_EXEC or ET_DYN, got %s", f.Type)
	}
	if f.Entry == 0 {
		return fmt.Errorf("executable ELF has no entry point")
	}
	entryLoaded := false
	for _, p := range f.Progs {
		if p.Type != elf.PT_LOAD {
			continue
		}
		if p.Filesz > p.Memsz || p.Off > size || p.Filesz > size-p.Off || p.Memsz > math.MaxUint64-p.Vaddr {
			return fmt.Errorf("invalid loadable segment bounds")
		}
		if p.Align > 1 && (p.Align&(p.Align-1) != 0 || p.Vaddr%p.Align != p.Off%p.Align) {
			return fmt.Errorf("invalid loadable segment alignment")
		}
		if p.Flags&elf.PF_X != 0 && f.Entry >= p.Vaddr && f.Entry-p.Vaddr < p.Filesz {
			entryLoaded = true
		}
	}
	if !entryLoaded {
		return fmt.Errorf("entry point is not in a file-backed executable loadable segment")
	}
	return nil
}
