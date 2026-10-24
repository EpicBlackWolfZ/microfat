// Command codecboundary generates static Linux ELF fixtures around the codec policy boundary.
// These are synthetic syscall-only executables, not Go applications.
package main

import (
	"debug/elf"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const (
	boundaryBytes            = 512 * 1024
	elfHeaderSize            = 64
	programHeaderSize        = 56
	instructionSize          = 4
	codeOffset               = 4096
	loadAddress              = 0x400000
	fileMode                 = 0o755
	marker                   = "microfat-codec-boundary\n"
	paddingSeed       uint64 = 0x6a09e667f3bcc909
	paddingShift             = 12
	paddingShiftLeft         = 25
	paddingShiftFinal        = 27
	paddingMultiplier uint64 = 2685821657736338717
)

var boundaryProfiles = []struct {
	name  string
	bytes int
}{
	{name: "below", bytes: boundaryBytes - 1},
	{name: "at", bytes: boundaryBytes},
	{name: "above", bytes: boundaryBytes + 1},
}

func main() {
	architecture := flag.String("arch", "", "Linux architecture: amd64 or arm64")
	output := flag.String("output", "", "existing output directory")
	flag.Parse()
	if err := generate(*architecture, *output); err != nil {
		log.New(os.Stderr, "", 0).Fatal(err)
	}
}

func generate(architecture, output string) error {
	if output == "" {
		return errors.New("-output is required")
	}
	for _, profile := range boundaryProfiles {
		image, err := buildImage(architecture, profile.bytes)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(output, "boundary-"+profile.name), image, fileMode); err != nil {
			return fmt.Errorf("write %s: %w", profile.name, err)
		}
	}
	return nil
}

func buildImage(architecture string, size int) ([]byte, error) {
	machine, code, err := executableCode(architecture)
	if err != nil {
		return nil, err
	}
	if size < codeOffset+len(code)+len(marker) {
		return nil, errors.New("image is too small for the ELF and marker")
	}
	unsignedSize := uint64(size) // #nosec G115 -- the preceding lower bound guarantees a positive int fitting uint64.
	image := make([]byte, size)
	// Deterministic xorshift64* padding keeps the synthetic payload approximately
	// incompressible and avoids an all-zero tail dominating the codec comparison.
	state := paddingSeed
	for offset := range image {
		state ^= state >> paddingShift
		state ^= state << paddingShiftLeft
		state ^= state >> paddingShiftFinal
		image[offset] = byte((state * paddingMultiplier) >> 56)
	}
	clear(image[:elfHeaderSize+programHeaderSize])
	copy(image, []byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)})
	put := binary.LittleEndian
	put.PutUint16(image[16:], uint16(elf.ET_EXEC))
	put.PutUint16(image[18:], uint16(machine))
	put.PutUint32(image[20:], uint32(elf.EV_CURRENT))
	put.PutUint64(image[24:], loadAddress+codeOffset)
	put.PutUint64(image[32:], elfHeaderSize)
	put.PutUint16(image[52:], elfHeaderSize)
	put.PutUint16(image[54:], programHeaderSize)
	put.PutUint16(image[56:], 1)
	program := image[elfHeaderSize:]
	put.PutUint32(program, uint32(elf.PT_LOAD))
	put.PutUint32(program[4:], uint32(elf.PF_R|elf.PF_X))
	put.PutUint64(program[16:], loadAddress)
	put.PutUint64(program[24:], loadAddress)
	put.PutUint64(program[32:], unsignedSize)
	put.PutUint64(program[40:], unsignedSize)
	put.PutUint64(program[48:], codeOffset)
	copy(image[codeOffset:], code)
	copy(image[codeOffset+len(code):], marker)
	return image, nil
}

func executableCode(architecture string) (elf.Machine, []byte, error) {
	switch architecture {
	case "amd64":
		// write(1, RIP-relative marker, sizeof(marker)); exit(0).
		code := []byte{
			0xb8, 1, 0, 0, 0, // mov eax, SYS_write
			0xbf, 1, 0, 0, 0, // mov edi, stdout
			0x48, 0x8d, 0x35, 0x10, 0, 0, 0, // lea rsi, [rip + 16]
			0xba, byte(len(marker)), 0, 0, 0, // mov edx, marker length
			0x0f, 0x05, // syscall
			0xb8, 60, 0, 0, 0, // mov eax, SYS_exit
			0x31, 0xff, // xor edi, edi
			0x0f, 0x05, // syscall
		}
		return elf.EM_X86_64, code, nil
	case "arm64":
		// write(1, PC-relative marker, sizeof(marker)); exit(0).
		instructions := [...]uint32{
			0xd2800020,                          // mov x0, #1 (stdout)
			0x100000e1,                          // adr x1, .+28 (marker)
			0xd2800002 | uint32(len(marker))<<5, // mov x2, marker length
			0xd2800808,                          // mov x8, #64 (SYS_write)
			0xd4000001,                          // svc #0
			0xd2800000,                          // mov x0, #0
			0xd2800ba8,                          // mov x8, #93 (SYS_exit)
			0xd4000001,                          // svc #0
		}
		code := make([]byte, len(instructions)*instructionSize)
		for index, instruction := range instructions {
			binary.LittleEndian.PutUint32(code[index*instructionSize:], instruction)
		}
		return elf.EM_AARCH64, code, nil
	default:
		return 0, nil, fmt.Errorf("unsupported architecture %q", architecture)
	}
}
