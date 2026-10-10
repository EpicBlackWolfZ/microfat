package pack

import (
	"bytes"
	"compress/zlib"
	"context"
	"debug/elf"
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

const (
	elfBudgetHeaderSize        = 64
	elfBudgetProgramSize       = 56
	elfBudgetSectionSize       = 64
	elfBudgetSectionOffset     = 256
	elfBudgetProgramCount      = 2
	elfBudgetBaseAddress       = 0x400000
	elfBudgetDecodedNames      = 64 * 1024 * 1024
	elfBudgetLongNameBytes     = 1024 * 1024
	elfBudgetRepeatedSections  = 128
	elfBudgetCompressionChunk  = 64 * 1024
	elfBudgetSmallNames        = 4 * 1024
	elfBudgetInvalidAlignment  = 3
	elfBudgetAllocationBase    = 16 * 1024 * 1024
	elfBudgetInputAllocFactor  = 8
	elfBudgetAllocationTimeout = 30 * time.Second
	elfBudgetFixtureMode       = 0o600
	elfBudgetHelperPathEnv     = "MICROFAT_TEST_ELF_ALLOCATION_PATH"
)

type elfBudgetFixtureOptions struct {
	order        binary.ByteOrder
	compression  elf.CompressionType
	nameBytes    uint64
	sectionCount uint64
	extended     bool
	longName     bool
}

// The measured validation runs in a separate process: unrelated parallel tests
// cannot contribute to its TotalAlloc delta. Fixture compression happens in the
// parent and writes bounded chunks rather than allocating the expanded table.
func TestValidateELFBinaryAuxiliarySectionsBoundedAllocation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		opts elfBudgetFixtureOptions
	}{
		{"zlib section names", elfBudgetFixtureOptions{compression: elf.COMPRESS_ZLIB}},
		{"zstd section names", elfBudgetFixtureOptions{compression: elf.COMPRESS_ZSTD}},
		{"big endian extended zlib names", elfBudgetFixtureOptions{
			order: binary.BigEndian, compression: elf.COMPRESS_ZLIB, extended: true,
		}},
		{"extended zstd names", elfBudgetFixtureOptions{compression: elf.COMPRESS_ZSTD, extended: true}},
		{"repeated long uncompressed names", elfBudgetFixtureOptions{
			nameBytes: elfBudgetLongNameBytes, sectionCount: elfBudgetRepeatedSections, longName: true,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := elfBudgetFixture(t, tc.opts)
			input := filepath.Join(t.TempDir(), "input.elf")
			require.NoError(t, os.WriteFile(input, data, elfBudgetFixtureMode))
			testExecutable, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), elfBudgetAllocationTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, testExecutable, "-test.run=^TestELFValidationAllocationHelper$", "-test.v")
			cmd.Env = append(os.Environ(), elfBudgetHelperPathEnv+"="+input)
			output, err := cmd.CombinedOutput()
			t.Logf("isolated allocation measurement:\n%s", output)
			require.NoError(t, err, "isolated ELF validation: %s", output)
		})
	}
}

func TestELFValidationAllocationHelper(t *testing.T) {
	input := os.Getenv(elfBudgetHelperPathEnv)
	if input == "" {
		t.Skip("allocation measurement runs only in the isolated helper process")
	}
	stat, err := os.Stat(input)
	require.NoError(t, err)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err = ValidateELFBinary(input, testOSLinux, testArchAMD64)
	runtime.ReadMemStats(&after)
	require.NoError(t, err)
	allocated := after.TotalAlloc - before.TotalAlloc
	// io.ReadAll grows its input buffer; allow that cost without allowing section
	// expansion or per-section copies of the same large name.
	budget := uint64(elfBudgetAllocationBase) + uint64(stat.Size())*elfBudgetInputAllocFactor
	t.Logf("input=%d bytes allocated=%d bytes budget=%d bytes", stat.Size(), allocated, budget)
	require.LessOrEqual(t, allocated, budget, "validation allocated %d bytes for a %d-byte ELF", allocated, stat.Size())
}

func TestReadExecutableELFPreservesHeaderAndProgramFacts(t *testing.T) {
	t.Parallel()
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		t.Run(order.String(), func(t *testing.T) {
			t.Parallel()
			data := elfBudgetFixture(t, elfBudgetFixtureOptions{
				order: order, compression: elf.COMPRESS_ZLIB, nameBytes: elfBudgetSmallNames,
			})
			f, err := readExecutableELF(data)
			require.NoError(t, err)
			require.Equal(t, elf.ELFCLASS64, f.Class)
			require.Equal(t, order, f.ByteOrder)
			require.Equal(t, elf.EV_CURRENT, f.Version)
			require.Equal(t, elf.ELFOSABI_LINUX, f.OSABI)
			require.Equal(t, uint8(1), f.ABIVersion)
			require.Equal(t, elf.ET_EXEC, f.Type)
			require.Equal(t, elf.EM_X86_64, f.Machine)
			require.Equal(t, uint64(elfBudgetBaseAddress+elfBudgetHeaderSize+elfBudgetProgramCount*elfBudgetProgramSize), f.Entry)
			require.Len(t, f.Progs, elfBudgetProgramCount)
			require.Equal(t, elf.PT_LOAD, f.Progs[0].Type)
			require.Equal(t, elf.PF_R|elf.PF_X, f.Progs[0].Flags)
			require.Equal(t, uint64(len(data)), f.Progs[0].Filesz)
			require.Equal(t, uint64(len(data)), f.Progs[0].Memsz)
			require.Equal(t, elf.PT_NOTE, f.Progs[1].Type)
			require.Empty(t, f.Sections, "executable validation must not load auxiliary section contents or names")
			require.NoError(t, validateExecutableELF(f, uint64(len(data))))
		})
	}
}

func TestValidateELFBinaryHeaderOnlyParserRejections(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		mutate  func(*elf.Header64, []elf.Prog64)
		wantABI error
	}{
		{"mismatched ELF version", func(h *elf.Header64, _ []elf.Prog64) { h.Version++ }, nil},
		{"signed program offset with no programs", func(h *elf.Header64, _ []elf.Prog64) {
			h.Phnum = 0
			h.Phoff = math.MaxInt64 + 1
		}, nil},
		{"signed load offset", func(_ *elf.Header64, p []elf.Prog64) { p[0].Off = math.MaxInt64 + 1 }, nil},
		{"signed load file size", func(_ *elf.Header64, p []elf.Prog64) { p[0].Filesz = math.MaxInt64 + 1 }, nil},
		{"signed nonload offset", func(_ *elf.Header64, p []elf.Prog64) { p[1].Off = math.MaxInt64 + 1 }, nil},
		{"signed nonload file size", func(_ *elf.Header64, p []elf.Prog64) { p[1].Filesz = math.MaxInt64 + 1 }, nil},
		{"wrong architecture", func(h *elf.Header64, _ []elf.Prog64) { h.Machine = uint16(elf.EM_AARCH64) }, nil},
		{"nonexecutable type", func(h *elf.Header64, _ []elf.Prog64) { h.Type = uint16(elf.ET_REL) }, nil},
		{"no entry point", func(h *elf.Header64, _ []elf.Prog64) { h.Entry = 0 }, nil},
		{"nonexecutable load segment", func(_ *elf.Header64, p []elf.Prog64) { p[0].Flags = uint32(elf.PF_R) }, nil},
		{"invalid load alignment", func(_ *elf.Header64, p []elf.Prog64) { p[0].Align = elfBudgetInvalidAlignment }, nil},
		{"program count budget", func(h *elf.Header64, _ []elf.Prog64) {
			h.Phnum = MaxProgramHeaders + 1
		}, ErrABIResourceLimit},
		{"section table outside input", func(h *elf.Header64, _ []elf.Prog64) {
			h.Shoff = math.MaxUint64
		}, ErrABIMetadata},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := elfBudgetFixture(t, elfBudgetFixtureOptions{nameBytes: elfBudgetSmallNames})
			elfBudgetMutateHeaders(t, data, tc.mutate)
			input := filepath.Join(t.TempDir(), "input.elf")
			require.NoError(t, os.WriteFile(input, data, elfBudgetFixtureMode))
			err := ValidateELFBinary(input, testOSLinux, testArchAMD64)
			require.ErrorIs(t, err, ErrInvalidELF)
			if tc.wantABI != nil {
				require.ErrorIs(t, err, tc.wantABI)
			}
		})
	}
}

func TestValidateELFBinaryIgnoresUnusedAuxiliaryContents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*elf.Section64, []byte)
	}{
		{"truncated compression header", func(s *elf.Section64, _ []byte) { s.Size = 1 }},
		{"unsupported compression type", func(s *elf.Section64, data []byte) {
			binary.LittleEndian.PutUint32(data[s.Off:], math.MaxUint32)
		}},
		{"section name outside table", func(s *elf.Section64, _ []byte) { s.Name = math.MaxUint32 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := elfBudgetFixture(t, elfBudgetFixtureOptions{
				compression: elf.COMPRESS_ZLIB, nameBytes: elfBudgetSmallNames,
			})
			const nameSectionOffset = elfBudgetSectionOffset + elfBudgetSectionSize
			var section elf.Section64
			require.NoError(t, binary.Read(bytes.NewReader(data[nameSectionOffset:]), binary.LittleEndian, &section))
			tc.mutate(&section, data)
			var patched bytes.Buffer
			require.NoError(t, binary.Write(&patched, binary.LittleEndian, &section))
			copy(data[nameSectionOffset:], patched.Bytes())
			require.NoError(t, PreflightELFHeaders(data), "header tables and their budgets remain valid")
			input := filepath.Join(t.TempDir(), "input.elf")
			require.NoError(t, os.WriteFile(input, data, elfBudgetFixtureMode))
			require.NoError(t, ValidateELFBinary(input, testOSLinux, testArchAMD64))
		})
	}
}

func elfBudgetMutateHeaders(t *testing.T, data []byte, mutate func(*elf.Header64, []elf.Prog64)) {
	t.Helper()
	var header elf.Header64
	require.NoError(t, binary.Read(bytes.NewReader(data), binary.LittleEndian, &header))
	programs := make([]elf.Prog64, elfBudgetProgramCount)
	require.NoError(t, binary.Read(bytes.NewReader(data[elfBudgetHeaderSize:]), binary.LittleEndian, &programs))
	mutate(&header, programs)
	var patched bytes.Buffer
	require.NoError(t, binary.Write(&patched, binary.LittleEndian, &header))
	require.NoError(t, binary.Write(&patched, binary.LittleEndian, programs))
	copy(data, patched.Bytes())
}

func elfBudgetFixture(t *testing.T, opts elfBudgetFixtureOptions) []byte {
	t.Helper()
	if opts.order == nil {
		opts.order = binary.LittleEndian
	}
	if opts.nameBytes == 0 {
		opts.nameBytes = elfBudgetDecodedNames
	}
	if opts.sectionCount == 0 {
		opts.sectionCount = elfBudgetProgramCount
	}
	nameIndex := uint64(1)
	if opts.extended {
		nameIndex = uint64(elf.SHN_LORESERVE)
		opts.sectionCount = nameIndex + 1
	}
	nameData := elfBudgetNameData(t, opts)
	nameOffset := uint64(elfBudgetSectionOffset) + opts.sectionCount*elfBudgetSectionSize
	data := make([]byte, int(nameOffset)+len(nameData))
	copy(data[nameOffset:], nameData)
	header := elf.Header64{
		Type: uint16(elf.ET_EXEC), Machine: uint16(elf.EM_X86_64), Version: uint32(elf.EV_CURRENT),
		Entry: elfBudgetBaseAddress + elfBudgetHeaderSize + elfBudgetProgramCount*elfBudgetProgramSize,
		Phoff: elfBudgetHeaderSize, Shoff: elfBudgetSectionOffset, Ehsize: elfBudgetHeaderSize,
		Phentsize: elfBudgetProgramSize, Phnum: elfBudgetProgramCount, Shentsize: elfBudgetSectionSize,
		Shnum: uint16(opts.sectionCount), Shstrndx: uint16(nameIndex),
	}
	copy(header.Ident[:], elf.ELFMAG)
	header.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	header.Ident[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	if opts.order == binary.BigEndian {
		header.Ident[elf.EI_DATA] = byte(elf.ELFDATA2MSB)
	}
	header.Ident[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	header.Ident[elf.EI_OSABI] = byte(elf.ELFOSABI_LINUX)
	header.Ident[elf.EI_ABIVERSION] = 1
	if opts.extended {
		header.Shnum = 0
		header.Shstrndx = uint16(elf.SHN_XINDEX)
	}
	programs := []elf.Prog64{
		{Type: uint32(elf.PT_LOAD), Flags: uint32(elf.PF_R | elf.PF_X), Vaddr: elfBudgetBaseAddress,
			Filesz: uint64(len(data)), Memsz: uint64(len(data))},
		{Type: uint32(elf.PT_NOTE)},
	}
	var prefix bytes.Buffer
	require.NoError(t, binary.Write(&prefix, opts.order, &header))
	require.NoError(t, binary.Write(&prefix, opts.order, programs))
	copy(data, prefix.Bytes())
	elfBudgetWriteSections(t, data, opts, nameIndex, nameOffset, uint64(len(nameData)))
	return data
}

func elfBudgetWriteSections(t *testing.T, data []byte, opts elfBudgetFixtureOptions, nameIndex, nameOffset, nameSize uint64) {
	t.Helper()
	for i := range opts.sectionCount {
		section := elf.Section64{}
		if opts.longName && i != 0 {
			section.Name = 1
		}
		if i == 0 && opts.extended {
			section.Size = opts.sectionCount
			section.Link = uint32(nameIndex)
		}
		if i == nameIndex {
			section.Type = uint32(elf.SHT_STRTAB)
			section.Off = nameOffset
			section.Size = nameSize
			section.Addralign = 1
			if opts.compression != 0 {
				section.Flags = uint64(elf.SHF_COMPRESSED)
			}
		}
		var encoded bytes.Buffer
		require.NoError(t, binary.Write(&encoded, opts.order, &section))
		offset := uint64(elfBudgetSectionOffset) + i*elfBudgetSectionSize
		copy(data[offset:], encoded.Bytes())
	}
}

func elfBudgetNameData(t *testing.T, opts elfBudgetFixtureOptions) []byte {
	t.Helper()
	if opts.compression == 0 {
		data := make([]byte, opts.nameBytes)
		if opts.longName {
			for i := 1; i < len(data)-1; i++ {
				data[i] = 'n'
			}
		}
		return data
	}
	var data bytes.Buffer
	header := elf.Chdr64{Type: uint32(opts.compression), Size: opts.nameBytes, Addralign: 1}
	require.NoError(t, binary.Write(&data, opts.order, &header))
	var writer io.WriteCloser
	if opts.compression == elf.COMPRESS_ZLIB {
		writer = zlib.NewWriter(&data)
	} else {
		encoder, err := zstd.NewWriter(&data, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedFastest))
		require.NoError(t, err)
		writer = encoder
	}
	chunk := make([]byte, elfBudgetCompressionChunk)
	for remaining := opts.nameBytes; remaining > 0; {
		n := min(remaining, uint64(len(chunk)))
		written, err := writer.Write(chunk[:n])
		require.NoError(t, err)
		require.Equal(t, int(n), written)
		remaining -= n
	}
	require.NoError(t, writer.Close())
	return data.Bytes()
}
