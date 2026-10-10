package pack

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"math"
)

// readExecutableELF reads only the headers used for executable validation.
// debug/elf.NewFile eagerly decompresses the section-name table and copies each
// section name, which can amplify a small input beyond the metadata budgets.
// Auxiliary section contents and names are not part of the execution contract.
func readExecutableELF(data []byte) (*elf.File, error) {
	if err := PreflightELFHeaders(data); err != nil {
		return nil, err
	}
	order := binary.ByteOrder(binary.LittleEndian)
	if data[elf.EI_DATA] == byte(elf.ELFDATA2MSB) {
		order = binary.BigEndian
	}
	var header elf.Header64
	if err := binary.Read(bytes.NewReader(data), order, &header); err != nil {
		return nil, err
	}
	if header.Version != uint32(elf.EV_CURRENT) {
		return nil, fmt.Errorf("invalid ELF version %d", header.Version)
	}
	if header.Phoff > math.MaxInt64 {
		return nil, fmt.Errorf("invalid program header offset %d", header.Phoff)
	}
	f := &elf.File{
		FileHeader: elf.FileHeader{
			Class:      elf.Class(header.Ident[elf.EI_CLASS]),
			Data:       elf.Data(header.Ident[elf.EI_DATA]),
			Version:    elf.Version(header.Version),
			OSABI:      elf.OSABI(header.Ident[elf.EI_OSABI]),
			ABIVersion: header.Ident[elf.EI_ABIVERSION],
			ByteOrder:  order,
			Type:       elf.Type(header.Type),
			Machine:    elf.Machine(header.Machine),
			Entry:      header.Entry,
		},
		Progs: make([]*elf.Prog, 0, header.Phnum),
	}
	for i := range header.Phnum {
		offset := header.Phoff + uint64(i)*uint64(header.Phentsize)
		var program elf.Prog64
		if err := binary.Read(bytes.NewReader(data[offset:]), order, &program); err != nil {
			return nil, err
		}
		if program.Off > math.MaxInt64 || program.Filesz > math.MaxInt64 {
			return nil, fmt.Errorf("invalid program offset or file size")
		}
		f.Progs = append(f.Progs, &elf.Prog{ProgHeader: elf.ProgHeader{
			Type:   elf.ProgType(program.Type),
			Flags:  elf.ProgFlag(program.Flags),
			Off:    program.Off,
			Vaddr:  program.Vaddr,
			Paddr:  program.Paddr,
			Filesz: program.Filesz,
			Memsz:  program.Memsz,
			Align:  program.Align,
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
