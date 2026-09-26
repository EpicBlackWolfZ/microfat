package pack

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testLdLinux    = "/lib64/ld-linux-x86-64.so.2"
	testMuslLoader = "/lib/ld-musl-x86_64.so.1"
	testLibc       = "libc.so.6"
	testLibm       = "libm.so.6"
	testGlibc225   = "GLIBC_2.2.5"
	testGlibc214   = "GLIBC_2.14"
)

type syntheticVerneed struct {
	file     string
	versions []syntheticVernaux
}

type syntheticVernaux struct {
	name  string
	flags uint16
}

type syntheticELFOpts struct {
	class                     byte
	data                      byte
	eType                     uint16
	machine                   uint16
	entry                     uint64
	interp                    string
	duplicateInterp           bool
	interpRaw                 []byte
	dependencies              []string
	verneeds                  []syntheticVerneed
	loadVaddr                 uint64
	noDynamicNull             bool
	dynPtrNonFileBacked       bool
	verneedCycle              bool
	vernauxCycle              bool
	verneedZeroProgress       bool
	vernauxZeroProgress       bool
	invalidStringOffset       bool
	unsupportedVerneedVersion bool
	largePhnum                uint16
	largeShnum                uint16
	phentsize                 uint16
	shentsize                 uint16
	shoff                     uint64
	extendedShnum             uint64
	truncated                 bool
}

func buildSyntheticELF(tb testing.TB, opts syntheticELFOpts) []byte {
	tb.Helper()
	buf := new(bytes.Buffer)
	class := byte(elf.ELFCLASS64)
	if opts.class != 0 {
		class = opts.class
	}
	dataEnc := byte(elf.ELFDATA2LSB)
	if opts.data != 0 {
		dataEnc = opts.data
	}
	order := binary.ByteOrder(binary.LittleEndian)
	if dataEnc == byte(elf.ELFDATA2MSB) {
		order = binary.BigEndian
	}

	// 1. ELF Header (64 bytes)
	ident := make([]byte, 16)
	ident[0], ident[1], ident[2], ident[3] = 0x7f, 'E', 'L', 'F'
	ident[4] = class
	ident[5] = dataEnc
	ident[6] = 1 // EV_CURRENT
	buf.Write(ident)

	eType := uint16(elf.ET_EXEC)
	if opts.eType != 0 {
		eType = opts.eType
	}
	_ = binary.Write(buf, order, eType)

	machine := uint16(elf.EM_X86_64)
	if opts.machine != 0 {
		machine = opts.machine
	}
	_ = binary.Write(buf, order, machine)
	_ = binary.Write(buf, order, uint32(1)) // e_version

	entry := uint64(0x400040)
	if opts.entry != 0 {
		entry = opts.entry
	}
	_ = binary.Write(buf, order, entry) // e_entry

	ePhoff := uint64(64)
	_ = binary.Write(buf, order, ePhoff) // e_phoff

	eShoff := uint64(0)
	if opts.shoff != 0 {
		eShoff = opts.shoff
	}
	_ = binary.Write(buf, order, eShoff) // e_shoff

	_ = binary.Write(buf, order, uint32(0)) // e_flags

	ehsize := uint16(64)
	_ = binary.Write(buf, order, ehsize) // e_ehsize

	phentsize := uint16(56)
	if opts.phentsize != 0 {
		phentsize = opts.phentsize
	}
	_ = binary.Write(buf, order, phentsize) // e_phentsize

	phnum := uint16(1) // at least 1 PT_LOAD
	if opts.interp != "" || len(opts.interpRaw) > 0 {
		phnum++
	}
	if opts.duplicateInterp {
		phnum++
	}
	hasDyn := len(opts.dependencies) > 0 || len(opts.verneeds) > 0 || opts.noDynamicNull
	if hasDyn {
		phnum++
	}
	if opts.largePhnum != 0 {
		phnum = opts.largePhnum
	}
	_ = binary.Write(buf, order, phnum) // e_phnum

	shentsize := uint16(64)
	if opts.shentsize != 0 {
		shentsize = opts.shentsize
	}
	_ = binary.Write(buf, order, shentsize) // e_shentsize

	shnum := uint16(0)
	if opts.largeShnum != 0 {
		shnum = opts.largeShnum
	}
	_ = binary.Write(buf, order, shnum)     // e_shnum
	_ = binary.Write(buf, order, uint16(0)) // e_shstrndx

	require.Equal(tb, 64, buf.Len(), "ELF header must be exactly 64 bytes")

	// Program Headers area
	phOffset := uint64(buf.Len())
	phBytesLen := int(phnum) * int(phentsize)
	phPlaceholder := make([]byte, phBytesLen)
	buf.Write(phPlaceholder)

	// Now construct payloads and tables
	loadVaddr := uint64(0x400000)
	if opts.loadVaddr != 0 {
		loadVaddr = opts.loadVaddr
	}
	loadOffset := uint64(0)

	// String table construction
	strtabBuf := new(bytes.Buffer)
	strtabBuf.WriteByte(0) // offset 0 is empty string

	getStringOffset := func(s string) uint32 {
		off := uint32(strtabBuf.Len())
		strtabBuf.WriteString(s)
		strtabBuf.WriteByte(0)
		return off
	}

	depOffsets := make([]uint32, len(opts.dependencies))
	for i, d := range opts.dependencies {
		depOffsets[i] = getStringOffset(d)
	}

	type encodedVerneed struct {
		fileOff uint32
		auxes   []syntheticVernaux
	}
	var encVerneeds []encodedVerneed
	for _, vn := range opts.verneeds {
		encVerneeds = append(encVerneeds, encodedVerneed{
			fileOff: getStringOffset(vn.file),
			auxes:   vn.versions,
		})
	}

	// Body area after program headers
	bodyStartOffset := uint64(buf.Len())

	// Write PT_INTERP string if requested
	interpOffset := uint64(0)
	interpFilesz := uint64(0)
	if len(opts.interpRaw) > 0 {
		interpOffset = uint64(buf.Len())
		buf.Write(opts.interpRaw)
		interpFilesz = uint64(len(opts.interpRaw))
	} else if opts.interp != "" {
		interpOffset = uint64(buf.Len())
		buf.WriteString(opts.interp)
		buf.WriteByte(0)
		interpFilesz = uint64(len(opts.interp) + 1)
	}

	// Align to 16 bytes for dynamic structures
	for buf.Len()%16 != 0 {
		buf.WriteByte(0)
	}

	verneedOffset := uint64(0)
	verneedCount := len(encVerneeds)
	if verneedCount > 0 {
		verneedOffset = uint64(buf.Len())
		for vi, vn := range encVerneeds {
			vStart := buf.Len()
			vnVersion := uint16(1)
			if opts.unsupportedVerneedVersion {
				vnVersion = 99
			}
			cnt := uint16(len(vn.auxes))
			if opts.vernauxCycle {
				cnt = 3
			}
			_ = binary.Write(buf, order, vnVersion)  // vn_version
			_ = binary.Write(buf, order, cnt)        // vn_cnt
			_ = binary.Write(buf, order, vn.fileOff) // vn_file
			_ = binary.Write(buf, order, uint32(16)) // vn_aux (first aux starts immediately after 16-byte Verneed)

			vnNext := uint32(0)
			auxBytes := len(vn.auxes) * 16
			if vi < verneedCount-1 {
				vnNext = uint32(16 + auxBytes)
				if opts.verneedZeroProgress {
					vnNext = 0
				}
			}
			if opts.verneedCycle {
				if vi == 0 {
					vnNext = uint32(16 + auxBytes)
				} else if vi == 1 {
					back := int32(-(16 + len(encVerneeds[0].auxes)*16))
					vnNext = uint32(back)
				}
			}
			_ = binary.Write(buf, order, vnNext) // vn_next

			// Write Vernaux entries
			for ai, aux := range vn.auxes {
				nameOff := getStringOffset(aux.name)
				if opts.invalidStringOffset {
					nameOff = 0xffffff
				}
				_ = binary.Write(buf, order, uint32(0)) // vna_hash
				_ = binary.Write(buf, order, aux.flags) // vna_flags
				_ = binary.Write(buf, order, uint16(0)) // vna_other
				_ = binary.Write(buf, order, nameOff)   // vna_name

				vnaNext := uint32(0)
				if ai < len(vn.auxes)-1 {
					vnaNext = 16
					if opts.vernauxZeroProgress {
						vnaNext = 0
					}
				}
				if opts.vernauxCycle {
					if ai == 0 {
						vnaNext = 16
					} else if ai == 1 {
						vnaNext = 0xfffffff0 // -16 in 32-bit two's complement
					}
				}
				_ = binary.Write(buf, order, vnaNext) // vna_next
			}
			_ = vStart
		}
	}

	// Align to 16 bytes
	for buf.Len()%16 != 0 {
		buf.WriteByte(0)
	}

	strtabOffset := uint64(buf.Len())
	strtabBytes := strtabBuf.Bytes()
	buf.Write(strtabBytes)

	// Align to 16 bytes for PT_DYNAMIC
	for buf.Len()%16 != 0 {
		buf.WriteByte(0)
	}

	dynOffset := uint64(buf.Len())
	dynFilesz := uint64(0)

	if hasDyn {
		dynStart := buf.Len()
		// DT_STRTAB
		strtabVaddr := loadVaddr + strtabOffset
		if opts.dynPtrNonFileBacked {
			strtabVaddr = loadVaddr + 0x900000 // unmapped
		}
		_ = binary.Write(buf, order, int64(elf.DT_STRTAB))
		_ = binary.Write(buf, order, strtabVaddr)

		// DT_STRSZ
		_ = binary.Write(buf, order, int64(elf.DT_STRSZ))
		_ = binary.Write(buf, order, uint64(len(strtabBytes)))

		// DT_NEEDED entries
		for _, off := range depOffsets {
			_ = binary.Write(buf, order, int64(elf.DT_NEEDED))
			_ = binary.Write(buf, order, uint64(off))
		}

		// DT_VERNEED
		if verneedCount > 0 {
			verneedVaddr := loadVaddr + verneedOffset
			_ = binary.Write(buf, order, int64(elf.DT_VERNEED))
			_ = binary.Write(buf, order, verneedVaddr)

			vNum := uint64(verneedCount)
			if opts.verneedCycle {
				vNum = 3
			}
			_ = binary.Write(buf, order, int64(elf.DT_VERNEEDNUM))
			_ = binary.Write(buf, order, vNum)
		}

		if !opts.noDynamicNull {
			_ = binary.Write(buf, order, int64(elf.DT_NULL))
			_ = binary.Write(buf, order, uint64(0))
		}
		dynFilesz = uint64(buf.Len() - dynStart)
	}

	// Pad file to at least 4096 bytes so load segment covers everything comfortably
	for buf.Len() < 4096 {
		buf.WriteByte(0x90) // nop padding
	}

	fileSize := uint64(buf.Len())
	loadFilesz := fileSize
	loadMemsz := loadFilesz + 0x1000

	// Now write program headers back into the placeholder area
	phBuf := new(bytes.Buffer)

	// Program Header 0: PT_LOAD
	_ = binary.Write(phBuf, order, uint32(elf.PT_LOAD))
	_ = binary.Write(phBuf, order, uint32(elf.PF_R|elf.PF_X))
	_ = binary.Write(phBuf, order, loadOffset)     // p_offset
	_ = binary.Write(phBuf, order, loadVaddr)      // p_vaddr
	_ = binary.Write(phBuf, order, loadVaddr)      // p_paddr
	_ = binary.Write(phBuf, order, loadFilesz)     // p_filesz
	_ = binary.Write(phBuf, order, loadMemsz)      // p_memsz
	_ = binary.Write(phBuf, order, uint64(0x1000)) // p_align

	if opts.interp != "" || len(opts.interpRaw) > 0 {
		_ = binary.Write(phBuf, order, uint32(elf.PT_INTERP))
		_ = binary.Write(phBuf, order, uint32(elf.PF_R))
		_ = binary.Write(phBuf, order, interpOffset)
		_ = binary.Write(phBuf, order, loadVaddr+interpOffset)
		_ = binary.Write(phBuf, order, loadVaddr+interpOffset)
		_ = binary.Write(phBuf, order, interpFilesz)
		_ = binary.Write(phBuf, order, interpFilesz)
		_ = binary.Write(phBuf, order, uint64(1))
	}

	if opts.duplicateInterp {
		_ = binary.Write(phBuf, order, uint32(elf.PT_INTERP))
		_ = binary.Write(phBuf, order, uint32(elf.PF_R))
		_ = binary.Write(phBuf, order, interpOffset)
		_ = binary.Write(phBuf, order, loadVaddr+interpOffset)
		_ = binary.Write(phBuf, order, loadVaddr+interpOffset)
		_ = binary.Write(phBuf, order, interpFilesz)
		_ = binary.Write(phBuf, order, interpFilesz)
		_ = binary.Write(phBuf, order, uint64(1))
	}

	if hasDyn {
		_ = binary.Write(phBuf, order, uint32(elf.PT_DYNAMIC))
		_ = binary.Write(phBuf, order, uint32(elf.PF_R|elf.PF_W))
		_ = binary.Write(phBuf, order, dynOffset)
		_ = binary.Write(phBuf, order, loadVaddr+dynOffset)
		_ = binary.Write(phBuf, order, loadVaddr+dynOffset)
		_ = binary.Write(phBuf, order, dynFilesz)
		_ = binary.Write(phBuf, order, dynFilesz)
		_ = binary.Write(phBuf, order, uint64(8))
	}

	// Overwrite placeholder program headers in buf
	resBytes := buf.Bytes()
	copy(resBytes[phOffset:], phBuf.Bytes())

	if opts.extendedShnum > 0 && eShoff > 0 && int(eShoff+64) <= len(resBytes) {
		// Set section 0 sh_size to extendedShnum
		order.PutUint64(resBytes[eShoff+32:eShoff+40], opts.extendedShnum)
	}

	if opts.truncated {
		resBytes = resBytes[:len(resBytes)/2]
	}

	_ = bodyStartOffset
	return resBytes
}

func TestPreflightELFHeaders_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("File smaller than 64 bytes", func(t *testing.T) {
		t.Parallel()
		data := []byte{0x7f, 'E', 'L', 'F', 2, 1, 1}
		err := PreflightELFHeaders(data)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Invalid ELF magic", func(t *testing.T) {
		t.Parallel()
		data := make([]byte, 64)
		copy(data, []byte("NOT_AN_ELF_MAGIC"))
		err := PreflightELFHeaders(data)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidELF))
	})

	t.Run("32-bit ELF class", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{class: 1})
		err := PreflightELFHeaders(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidELF))
	})

	t.Run("Invalid data encoding", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{data: 99})
		err := PreflightELFHeaders(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidELF))
	})

	t.Run("Program headers count exceeds budget", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{largePhnum: 5000})
		err := PreflightELFHeaders(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
	})

	t.Run("Invalid program header entry size (< 56)", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{phentsize: 32})
		err := PreflightELFHeaders(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Program headers extend beyond file", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{largePhnum: 200})
		// truncate file right after headers
		raw = raw[:128]
		err := PreflightELFHeaders(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Section header entry size invalid (< 64)", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{shoff: 64, shentsize: 32, largeShnum: 2})
		err := PreflightELFHeaders(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Section headers count exceeds budget", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{shoff: 64, shentsize: 64, extendedShnum: 70000})
		err := PreflightELFHeaders(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
	})

	t.Run("Valid sectionless ELF", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{})
		err := PreflightELFHeaders(raw)
		require.NoError(t, err)
	})
}

func TestInspectELFABI_LinkageAndInterpreter(t *testing.T) {
	t.Parallel()

	t.Run("Static ET_EXEC", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{eType: uint16(elf.ET_EXEC)})
		rep, err := InspectELFABI(raw)
		require.NoError(t, err)
		assert.Equal(t, LinkageStatic, rep.Linkage)
		assert.False(t, rep.HasInterpreter)
		assert.Equal(t, MetadataAbsent, rep.Completeness)
	})

	t.Run("Static PIE ET_DYN", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{eType: uint16(elf.ET_DYN)})
		rep, err := InspectELFABI(raw)
		require.NoError(t, err)
		assert.Equal(t, LinkageStaticPIE, rep.Linkage)
		assert.False(t, rep.HasInterpreter)
		assert.Equal(t, MetadataAbsent, rep.Completeness)
	})

	t.Run("Dynamic ET_DYN with interpreter and dependencies", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			eType:        uint16(elf.ET_DYN),
			interp:       testLdLinux,
			dependencies: []string{testLibc, testLibm},
			verneeds: []syntheticVerneed{
				{
					file: testLibc,
					versions: []syntheticVernaux{
						{name: testGlibc225, flags: 0},
						{name: testGlibc214, flags: 0},
					},
				},
			},
		})
		rep, err := InspectELFABI(raw)
		require.NoError(t, err)
		assert.Equal(t, LinkageDynamic, rep.Linkage)
		assert.True(t, rep.HasInterpreter)
		assert.Equal(t, testLdLinux, rep.Interpreter)
		assert.Equal(t, []string{testLibc, testLibm}, rep.Dependencies)
		assert.Equal(t, MetadataComplete, rep.Completeness)
		assert.Len(t, rep.VersionRequirements, 2)
	})

	t.Run("Dynamic ET_DYN with musl loader and no version requirements", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			eType:        uint16(elf.ET_DYN),
			interp:       testMuslLoader,
			dependencies: []string{"libc.musl-x86_64.so.1"},
		})
		rep, err := InspectELFABI(raw)
		require.NoError(t, err)
		assert.Equal(t, LinkageDynamic, rep.Linkage)
		assert.Equal(t, testMuslLoader, rep.Interpreter)
		assert.Equal(t, MetadataAbsent, rep.Completeness)
		assert.Empty(t, rep.VersionRequirements)
	})

	t.Run("Duplicate PT_INTERP segments rejected", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			interp:          testLdLinux,
			duplicateInterp: true,
		})
		_, err := InspectELFABI(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Empty PT_INTERP segment rejected", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			interpRaw: []byte{0},
		})
		_, err := InspectELFABI(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Unterminated PT_INTERP rejected", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			interpRaw: []byte("/lib64/ld-linux-no-nul"),
		})
		_, err := InspectELFABI(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("PT_INTERP with literal whitespace preserved exactly", func(t *testing.T) {
		t.Parallel()
		whitespaceInterp := " /lib64/ld-linux.so.2 "
		raw := buildSyntheticELF(t, syntheticELFOpts{
			interp: whitespaceInterp,
		})
		rep, err := InspectELFABI(raw)
		require.NoError(t, err)
		assert.Equal(t, whitespaceInterp, rep.Interpreter)
	})

	t.Run("Ambiguous linkage: dependencies declared without PT_INTERP", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			dependencies: []string{testLibc},
		})
		rep, err := InspectELFABI(raw)
		require.NoError(t, err)
		assert.Equal(t, LinkageAmbiguous, rep.Linkage)
		assert.NotEmpty(t, rep.Warnings)
	})
}

func TestInspectELFABI_MalformedDynamicAndVersions(t *testing.T) {
	t.Parallel()

	t.Run("PT_DYNAMIC missing DT_NULL terminator", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			interp:        testLdLinux,
			noDynamicNull: true,
		})
		_, err := InspectELFABI(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Virtual address in non-file-backed load segment", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			interp:              testLdLinux,
			dependencies:        []string{testLibc},
			dynPtrNonFileBacked: true,
		})
		_, err := InspectELFABI(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Elf64_Verneed chain cycle detected", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			interp: testLdLinux,
			verneeds: []syntheticVerneed{
				{file: testLibc, versions: []syntheticVernaux{{name: testGlibc225}}},
				{file: testLibm, versions: []syntheticVernaux{{name: testGlibc225}}},
			},
			verneedCycle: true,
		})
		_, err := InspectELFABI(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Elf64_Vernaux chain cycle detected", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			interp: testLdLinux,
			verneeds: []syntheticVerneed{
				{file: testLibc, versions: []syntheticVernaux{
					{name: testGlibc225},
					{name: testGlibc214},
				}},
			},
			vernauxCycle: true,
		})
		_, err := InspectELFABI(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Elf64_Verneed zero-progress next offset", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			interp: testLdLinux,
			verneeds: []syntheticVerneed{
				{file: testLibc, versions: []syntheticVernaux{{name: testGlibc225}}},
				{file: testLibm, versions: []syntheticVernaux{{name: testGlibc225}}},
			},
			verneedZeroProgress: true,
		})
		_, err := InspectELFABI(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Elf64_Vernaux zero-progress next offset", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			interp: testLdLinux,
			verneeds: []syntheticVerneed{
				{file: testLibc, versions: []syntheticVernaux{
					{name: testGlibc225},
					{name: testGlibc214},
				}},
			},
			vernauxZeroProgress: true,
		})
		_, err := InspectELFABI(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Invalid string offset in verneed", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			interp: testLdLinux,
			verneeds: []syntheticVerneed{
				{file: testLibc, versions: []syntheticVernaux{{name: testGlibc225}}},
			},
			invalidStringOffset: true,
		})
		_, err := InspectELFABI(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Unsupported Elf64_Verneed version reports MetadataUnsupported", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			interp: testLdLinux,
			verneeds: []syntheticVerneed{
				{file: testLibc, versions: []syntheticVernaux{{name: testGlibc225}}},
			},
			unsupportedVerneedVersion: true,
		})
		rep, err := InspectELFABI(raw)
		require.NoError(t, err)
		assert.Equal(t, MetadataUnsupported, rep.Completeness)
		assert.NotEmpty(t, rep.Warnings)
	})
}

func TestCompareVariantABIs_PolicyMatrix(t *testing.T) {
	t.Parallel()

	t.Run("All static payloads allowed", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{Level: "v1", Linkage: LinkageStatic, Completeness: MetadataAbsent}
		r2 := &VariantABIReport{Level: "v3", Linkage: LinkageStaticPIE, Completeness: MetadataAbsent}

		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, err)
		assert.True(t, res.Consistent)
		assert.False(t, res.Overridden)
		assert.Empty(t, res.Differences)
	})

	t.Run("Dynamic payloads with matching interpreter, deps, and versions allowed", func(t *testing.T) {
		t.Parallel()
		reqs := []VersionRequirement{
			{Library: testLibc, Version: testGlibc214},
			{Library: testLibc, Version: testGlibc225},
		}
		r1 := &VariantABIReport{
			Level:               "v1",
			Linkage:             LinkageDynamic,
			Interpreter:         testLdLinux,
			Dependencies:        []string{testLibc, testLibm},
			VersionRequirements: reqs,
			Completeness:        MetadataComplete,
		}
		r2 := &VariantABIReport{
			Level:               "v3",
			Linkage:             LinkageDynamic,
			Interpreter:         testLdLinux,
			Dependencies:        []string{testLibc, testLibm},
			VersionRequirements: reqs,
			Completeness:        MetadataComplete,
		}

		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, err)
		assert.True(t, res.Consistent)
		assert.False(t, res.Overridden)
	})

	t.Run("Mixed static and dynamic payloads: rejected by default, allowed with flag", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{Level: "v1", Linkage: LinkageStatic, Completeness: MetadataAbsent}
		r2 := &VariantABIReport{
			Level:        "v3",
			Linkage:      LinkageDynamic,
			Interpreter:  testLdLinux,
			Dependencies: []string{testLibc},
			Completeness: MetadataAbsent,
		}

		// Default reject
		_, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMismatch))

		// Allowed with flag
		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, true)
		require.NoError(t, err)
		assert.False(t, res.Consistent)
		assert.True(t, res.Overridden)
		assert.NotEmpty(t, res.Differences)
	})

	t.Run("Differing interpreters: rejected by default, allowed with flag", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageDynamic,
			Interpreter:  testLdLinux,
			Dependencies: []string{testLibc},
		}
		r2 := &VariantABIReport{
			Level:        "v3",
			Linkage:      LinkageDynamic,
			Interpreter:  testMuslLoader,
			Dependencies: []string{testLibc},
		}

		// Default reject
		_, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMismatch))

		// Allowed with flag
		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, true)
		require.NoError(t, err)
		assert.False(t, res.Consistent)
		assert.True(t, res.Overridden)
	})

	t.Run("Differing dependency sets: rejected by default, allowed with flag", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageDynamic,
			Interpreter:  testLdLinux,
			Dependencies: []string{testLibc},
		}
		r2 := &VariantABIReport{
			Level:        "v3",
			Linkage:      LinkageDynamic,
			Interpreter:  testLdLinux,
			Dependencies: []string{testLibc, testLibm},
		}

		// Default reject
		_, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMismatch))

		// Allowed with flag
		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, true)
		require.NoError(t, err)
		assert.False(t, res.Consistent)
		assert.True(t, res.Overridden)
	})

	t.Run("Matching dependency sets with different order: allowed with order qualification warning", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageDynamic,
			Interpreter:  testLdLinux,
			Dependencies: []string{testLibc, testLibm},
		}
		r2 := &VariantABIReport{
			Level:        "v3",
			Linkage:      LinkageDynamic,
			Interpreter:  testLdLinux,
			Dependencies: []string{testLibm, testLibc},
		}

		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, err)
		assert.True(t, res.Consistent)
		assert.NotEmpty(t, res.Warnings)
		assert.Contains(t, res.Warnings[0], "search order differs")
	})

	t.Run("Differing version requirements: rejected by default, allowed with flag", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageDynamic,
			Interpreter:  testLdLinux,
			Dependencies: []string{testLibc},
			VersionRequirements: []VersionRequirement{
				{Library: testLibc, Version: testGlibc225},
			},
		}
		r2 := &VariantABIReport{
			Level:        "v3",
			Linkage:      LinkageDynamic,
			Interpreter:  testLdLinux,
			Dependencies: []string{testLibc},
			VersionRequirements: []VersionRequirement{
				{Library: testLibc, Version: testGlibc214},
			},
		}

		// Default reject
		_, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMismatch))

		// Allowed with flag
		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, true)
		require.NoError(t, err)
		assert.False(t, res.Consistent)
		assert.True(t, res.Overridden)
	})

	t.Run("Opaque version labels not compared as numeric sequence", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageDynamic,
			Interpreter:  testLdLinux,
			Dependencies: []string{"libapp.so.1"},
			VersionRequirements: []VersionRequirement{
				{Library: "libapp.so.1", Version: "LIBAPP_alpha"},
			},
		}
		r2 := &VariantABIReport{
			Level:        "v3",
			Linkage:      LinkageDynamic,
			Interpreter:  testLdLinux,
			Dependencies: []string{"libapp.so.1"},
			VersionRequirements: []VersionRequirement{
				{Library: "libapp.so.1", Version: "LIBAPP_2"},
			},
		}

		_, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMismatch))
	})

	t.Run("Known interpreter mismatch plus unknown version metadata", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageDynamic,
			Interpreter:  testLdLinux,
			Completeness: MetadataComplete,
		}
		r2 := &VariantABIReport{
			Level:        "v3",
			Linkage:      LinkageDynamic,
			Interpreter:  testMuslLoader,
			Completeness: MetadataUnsupported,
		}

		// Known interpreter mismatch rejects even though r2 version metadata is unknown
		_, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMismatch))

		// Flag allows interpreter mismatch, still preserves unsupported warning
		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, true)
		require.NoError(t, err)
		assert.True(t, res.Overridden)
		assert.NotEmpty(t, res.Warnings)
	})
}

func TestSanitizeName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, testLibc, SanitizeName(testLibc))
	assert.Equal(t, testGlibc214, SanitizeName(testGlibc214))
	assert.Equal(t, "lib\\x1b[31m.so", SanitizeName("lib\x1b[31m.so"))
	assert.Equal(t, "lib\\x00name", SanitizeName("lib\x00name"))
	assert.Equal(t, "lib\\x0d\\x0aname", SanitizeName("lib\r\nname"))
}

func TestPack_ABIConsistency_Integration(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "microfat-stub")
	stubRaw := buildSyntheticELF(t, syntheticELFOpts{
		eType:  uint16(elf.ET_EXEC),
		interp: "", // stub is static
	})
	require.NoError(t, os.WriteFile(stubPath, stubRaw, 0o755))

	v1Path := filepath.Join(tempDir, "v1")
	v1Raw := buildSyntheticELF(t, syntheticELFOpts{
		eType:        uint16(elf.ET_DYN),
		interp:       testLdLinux,
		dependencies: []string{testLibc},
		verneeds: []syntheticVerneed{
			{file: testLibc, versions: []syntheticVernaux{{name: testGlibc225}}},
		},
	})
	require.NoError(t, os.WriteFile(v1Path, v1Raw, 0o755))

	// v3 has different interpreter (testMuslLoader)
	v3Path := filepath.Join(tempDir, "v3")
	v3Raw := buildSyntheticELF(t, syntheticELFOpts{
		eType:        uint16(elf.ET_DYN),
		interp:       testMuslLoader,
		dependencies: []string{"libc.musl-x86_64.so.1"},
	})
	require.NoError(t, os.WriteFile(v3Path, v3Raw, 0o755))

	outPath := filepath.Join(tempDir, "output.fat")
	// Pre-create output file to test that failure leaves existing file unchanged
	preexistingContent := []byte("PREEXISTING_OUTPUT_DATA")
	require.NoError(t, os.WriteFile(outPath, preexistingContent, 0o755))

	// 1. Pack with mismatched variants (default: must fail with ErrABIMismatch)
	opts := Options{
		StubPath:   stubPath,
		OutputPath: outPath,
		AppName:    "abi-test-app",
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants:   map[string]string{"v1": v1Path, "v3": v3Path},
	}

	_, err := Pack(opts)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrABIMismatch), "expected ErrABIMismatch, got: %v", err)

	// Invariant: failure must leave existing output completely byte-identical
	afterContent, readErr := os.ReadFile(outPath)
	require.NoError(t, readErr)
	assert.Equal(t, preexistingContent, afterContent, "output file must not be modified on ABI mismatch failure")

	// 2. Pack with --allow-mixed-abi (must succeed and report overridden differences)
	var capturedReport *ArtifactABIReport
	opts.AllowMixedABI = true
	opts.ABIReportCallback = func(report *ArtifactABIReport) {
		capturedReport = report
	}

	idx, err := Pack(opts)
	require.NoError(t, err)
	assert.NotNil(t, idx)
	assert.NotNil(t, capturedReport)
	assert.False(t, capturedReport.Consistent)
	assert.True(t, capturedReport.Overridden)
	assert.NotEmpty(t, capturedReport.Differences)

	// 3. Static stub with consistently dynamic variants must NOT be rejected
	v3MatchingPath := filepath.Join(tempDir, "v3_matching")
	v3MatchingRaw := buildSyntheticELF(t, syntheticELFOpts{
		eType:        uint16(elf.ET_DYN),
		interp:       testLdLinux,
		dependencies: []string{testLibc},
		verneeds: []syntheticVerneed{
			{file: testLibc, versions: []syntheticVernaux{{name: "GLIBC_2.2.5"}}},
		},
	})
	require.NoError(t, os.WriteFile(v3MatchingPath, v3MatchingRaw, 0o755))

	optsConsistent := Options{
		StubPath:   stubPath, // static stub
		OutputPath: outPath,
		AppName:    "abi-test-app",
		TargetOS:   "linux",
		TargetArch: "amd64",
		Variants:   map[string]string{"v1": v1Path, "v3": v3MatchingPath}, // both dynamic glibc
	}

	var consistentReport *ArtifactABIReport
	optsConsistent.ABIReportCallback = func(report *ArtifactABIReport) {
		consistentReport = report
	}

	idxConsistent, err := Pack(optsConsistent)
	require.NoError(t, err)
	assert.NotNil(t, idxConsistent)
	assert.NotNil(t, consistentReport)
	assert.True(t, consistentReport.Consistent)
	assert.False(t, consistentReport.Overridden)
}

func TestAccounting_Limits(t *testing.T) {
	t.Parallel()

	// 1. Metadata bytes limit
	acc := &inputAccounting{}
	require.NoError(t, acc.chargeMetadata(MaxMetadataBytesPerInput/2))
	require.NoError(t, acc.chargeMetadata(MaxMetadataBytesPerInput/2))
	err := acc.chargeMetadata(1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrABIResourceLimit))

	// 2. String bytes limit
	accStr := &inputAccounting{}
	require.NoError(t, accStr.chargeString(MaxABIStringBytesPerInput/2))
	require.NoError(t, accStr.chargeString(MaxABIStringBytesPerInput/2))
	errStr := accStr.chargeString(1)
	require.Error(t, errStr)
	assert.True(t, errors.Is(errStr, ErrABIResourceLimit))

	// 3. Version records limit
	accVer := &inputAccounting{}
	for range MaxVersionRecordsPerInput {
		require.NoError(t, accVer.chargeVersionRecord())
	}
	errVer := accVer.chargeVersionRecord()
	require.Error(t, errVer)
	assert.True(t, errors.Is(errVer, ErrABIResourceLimit))
}

func TestEscapeMetadata_EdgeCases(t *testing.T) {
	t.Parallel()

	// Truncation when exceeding maxDisplayLen (4096)
	longStr := strings.Repeat("A", 5000)
	escaped := EscapeMetadata(longStr)
	assert.True(t, strings.HasSuffix(escaped, "...[truncated]"))
	assert.LessOrEqual(t, len(escaped), 4096+len("...[truncated]"))

	// Invalid UTF-8 bytes
	invalidUTF8 := string([]byte{0xff, 0xfe, 'h', 'i'})
	escapedUTF8 := EscapeMetadata(invalidUTF8)
	assert.Equal(t, "\\xff\\xfehi", escapedUTF8)

	// sanitizeStringSlice
	slice := []string{"foo\nbar", "baz\x00qux"}
	sanitized := sanitizeStringSlice(slice)
	assert.Equal(t, []string{"foo\\x0abar", "baz\\x00qux"}, sanitized)
}

func TestPreflightELFHeaders_TableBounds(t *testing.T) {
	t.Parallel()

	t.Run("Invalid ELF version", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{})
		raw[6] = 2 // invalid version
		err := PreflightELFHeaders(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidELF))
	})

	t.Run("Section header offset beyond file size", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			shoff: 999999,
		})
		err := PreflightELFHeaders(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Extended section headers: section 0 extends beyond file", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			shoff:     uint64(50), // near end
			shentsize: 64,
		})
		// Force e_shnum = 0
		raw[60], raw[61] = 0, 0
		err := PreflightELFHeaders(raw[:70]) // truncate
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Extended section headers: realShnum exceeds budget", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			shoff:         64,
			shentsize:     64,
			extendedShnum: MaxSectionHeaders + 1,
		})
		err := PreflightELFHeaders(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
	})

	t.Run("Extended section headers: realShnum extends beyond file", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			shoff:         64,
			shentsize:     64,
			extendedShnum: 1000,
		})
		err := PreflightELFHeaders(raw[:200]) // truncated
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Extended section headers: valid extended count", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			shoff:         64,
			shentsize:     64,
			extendedShnum: 5,
		})
		err := PreflightELFHeaders(raw)
		require.NoError(t, err)
	})
}

func TestTranslateVaddr_ErrorPaths(t *testing.T) {
	t.Parallel()

	loads := []loadSegment{
		{off: 0x1000, vaddr: 0x401000, filesz: 0x1000, memsz: 0x2000},
	}
	fileSize := uint64(0x3000)

	// 1. Requested size exceeds file size
	_, err := translateVaddr(loads, fileSize, 0x401000, 0x4000)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrABIMetadata))

	// 2. Virtual address range extends into zero-fill memory
	_, err = translateVaddr(loads, fileSize, 0x401800, 0x1000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero-fill memory")

	// 3. Mapped file offset exceeds file size
	_, err = translateVaddr(loads, 0x1500, 0x401000, 0x1000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds file size")

	// 4. Ambiguous PT_LOAD mapping
	ambiguousLoads := []loadSegment{
		{off: 0x1000, vaddr: 0x401000, filesz: 0x1000, memsz: 0x1000},
		{off: 0x1000, vaddr: 0x401000, filesz: 0x1000, memsz: 0x1000},
	}
	_, err = translateVaddr(ambiguousLoads, fileSize, 0x401000, 0x100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous PT_LOAD mapping")
}

func TestAdvanceELFRelativeOffset_ErrorPaths(t *testing.T) {
	t.Parallel()

	// 1. Zero progress
	_, err := advanceELFRelativeOffset(100, 0, 0, 500, 16)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero progress")

	// 2. Negative offset underflow (jumping back 20 from 10)
	_, err = advanceELFRelativeOffset(10, 0xFFFFFFEC, 0, 500, 16)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "negative offset underflow")

	// 3. Out of bounds (< minOff)
	_, err = advanceELFRelativeOffset(50, 0xFFFFFFF0, 100, 500, 16) // jumps back below minOff=100
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of bounds")

	// 4. Out of bounds (> maxOff)
	_, err = advanceELFRelativeOffset(450, 100, 0, 500, 16)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of bounds")

	// 5. Valid forward and backward jumps
	nextFwd, err := advanceELFRelativeOffset(100, 32, 0, 500, 16)
	require.NoError(t, err)
	assert.Equal(t, uint64(132), nextFwd)

	nextBwd, err := advanceELFRelativeOffset(132, 0xFFFFFFE0, 0, 500, 16) // -32
	require.NoError(t, err)
	assert.Equal(t, uint64(100), nextBwd)
}

func TestParseNeededDependencies_ErrorPaths(t *testing.T) {
	t.Parallel()

	strtab := []byte("libc.so.6\x00\x00libm.so.6\x00unterminated")
	strtabSize := uint64(len(strtab))
	acc := &inputAccounting{}

	// 1. Count exceeds budget
	hugeOffsets := make([]uint64, MaxDynamicEntries+1)
	_, err := parseNeededDependencies(strtab, strtabSize, hugeOffsets, acc)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrABIResourceLimit))

	// 2. Empty string
	_, err = parseNeededDependencies(strtab, strtabSize, []uint64{10}, acc) // offset 10 is '\x00'
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is empty")

	// 3. Unterminated string
	_, err = parseNeededDependencies(strtab, strtabSize, []uint64{21}, acc) // 'unterminated' without NUL
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not NUL-terminated")
}

func TestParseDynamicMetadata_ErrorPaths(t *testing.T) {
	t.Parallel()

	// 1. Segment size not multiple of 16
	raw := buildSyntheticELF(t, syntheticELFOpts{
		eType:        uint16(elf.ET_DYN),
		dependencies: []string{testLibc},
	})
	// Tamper dynamic segment size
	acc := &inputAccounting{}
	prog := &loadSegment{off: 0, filesz: 15}
	err := parseDynamicMetadata(raw, prog, nil, uint64(len(raw)), binary.LittleEndian, acc, &VariantABIReport{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a multiple of 16")

	// 2. Dynamic entry count exceeds budget
	progHuge := &loadSegment{off: 0, filesz: (MaxDynamicEntries + 1) * 16}
	err = parseDynamicMetadata(raw, progHuge, nil, uint64(len(raw)), binary.LittleEndian, acc, &VariantABIReport{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrABIResourceLimit))
}

func TestValidateReportBudget_DefensiveValidation(t *testing.T) {
	t.Parallel()

	// 1. nil report
	err := validateReportBudget([]*VariantABIReport{nil})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrABIMetadata))

	// 2. Interpreter path length exceeds budget
	rInterp := &VariantABIReport{
		Interpreter: strings.Repeat("a", MaxInterpreterSize+1),
	}
	err = validateReportBudget([]*VariantABIReport{rInterp})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrABIResourceLimit))

	// 3. Dependency count exceeds budget
	rDeps := &VariantABIReport{
		Dependencies: make([]string, MaxDynamicEntries+1),
	}
	err = validateReportBudget([]*VariantABIReport{rDeps})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrABIResourceLimit))

	// 4. Version requirements count exceeds budget
	rVer := &VariantABIReport{
		VersionRequirements: make([]VersionRequirement, MaxVersionRecordsPerInput+1),
	}
	err = validateReportBudget([]*VariantABIReport{rVer})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrABIResourceLimit))

	// 5. Total decoded string bytes exceeds per-input budget
	rStrings := &VariantABIReport{
		Dependencies: []string{strings.Repeat("a", MaxABIStringBytesPerInput+1)},
	}
	err = validateReportBudget([]*VariantABIReport{rStrings})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrABIResourceLimit))

	// 6. Zero reports allowed
	resZero, err := CompareVariantABIs(nil, false)
	require.NoError(t, err)
	assert.Equal(t, ComparisonConsistent, resZero.Status)

	// 7. Single report with MetadataSkipped
	resSkip, err := CompareVariantABIs([]*VariantABIReport{{Completeness: MetadataSkipped}}, false)
	require.NoError(t, err)
	assert.Equal(t, ComparisonSkipped, resSkip.Status)
	assert.False(t, resSkip.Consistent)

	// 8. Single report with MetadataUnsupported
	resUnsupp, err := CompareVariantABIs([]*VariantABIReport{{Level: "v1", Completeness: MetadataUnsupported}}, false)
	require.NoError(t, err)
	assert.Equal(t, ComparisonUnknown, resUnsupp.Status)
	assert.False(t, resUnsupp.Consistent)
	assert.NotEmpty(t, resUnsupp.Warnings)
}

func Test65MiB_Amplification_ReviewProbeDefect(t *testing.T) {
	t.Parallel()

	// Review defect test: A single 1 MiB string referenced 65 times = 65 MiB decoded string bytes.
	// 1. Single report passed to CompareVariantABIs must be rejected!
	bigDep := strings.Repeat("A", 1024*1024) // 1 MiB
	deps := make([]string, 65)
	for i := range deps {
		deps[i] = bigDep
	}
	giantReport := &VariantABIReport{
		Level:        "v1",
		Dependencies: deps,
		Completeness: MetadataComplete,
	}

	_, err := CompareVariantABIs([]*VariantABIReport{giantReport}, false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrABIResourceLimit))

	// 2. parseNeededDependencies must reject before allocating 65 MiB of strings!
	acc := &inputAccounting{}
	strtab := []byte(bigDep + "\x00")
	strtabSize := uint64(len(strtab))
	neededOffsets := make([]uint64, 65) // all 65 point to offset 0

	_, err = parseNeededDependencies(strtab, strtabSize, neededOffsets, acc)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrABIResourceLimit))
	assert.LessOrEqual(t, acc.retainedStringBytes, uint64(MaxABIStringBytesPerInput))
}

func TestSectionlessELF_RetainsDynamicRequirements(t *testing.T) {
	t.Parallel()

	raw := buildSyntheticELF(t, syntheticELFOpts{
		eType:        uint16(elf.ET_DYN),
		interp:       testLdLinux,
		dependencies: []string{testLibc, testLibm},
		verneeds: []syntheticVerneed{
			{file: testLibc, versions: []syntheticVernaux{{name: testGlibc225}}},
		},
	})

	// Zero out section header table offset and count (make it sectionless)
	raw[40], raw[41], raw[42], raw[43] = 0, 0, 0, 0
	raw[44], raw[45], raw[46], raw[47] = 0, 0, 0, 0
	raw[60], raw[61] = 0, 0

	rep, err := InspectELFABI(raw)
	require.NoError(t, err)
	assert.Equal(t, LinkageDynamic, rep.Linkage)
	assert.True(t, rep.HasInterpreter)
	assert.Equal(t, testLdLinux, rep.Interpreter)
	assert.Equal(t, []string{testLibc, testLibm}, rep.Dependencies)
	assert.Equal(t, MetadataComplete, rep.Completeness)
	assert.Len(t, rep.VersionRequirements, 1)
	assert.Equal(t, testGlibc225, rep.VersionRequirements[0].Version)
}

func TestCompareVariantABIs_FullDecisionMatrix(t *testing.T) {
	t.Parallel()

	// Row 1: Known equal interpreter, dependencies and version tuples -> Allow (Consistent)
	t.Run("Known equal -> consistent", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{
			Level: "v1", Linkage: LinkageDynamic, Interpreter: testLdLinux,
			Dependencies: []string{testLibc}, Completeness: MetadataComplete,
		}
		r2 := &VariantABIReport{
			Level: "v2", Linkage: LinkageDynamic, Interpreter: testLdLinux,
			Dependencies: []string{testLibc}, Completeness: MetadataComplete,
		}
		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, err)
		assert.True(t, res.Consistent)
		assert.Equal(t, ComparisonConsistent, res.Status)
	})

	// Row 2: Supported inspection proves no version declarations (absent) -> Consistent
	t.Run("Absent versions -> consistent", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{
			Level: "v1", Linkage: LinkageDynamic, Interpreter: testLdLinux,
			Dependencies: []string{testLibc}, Completeness: MetadataAbsent,
		}
		r2 := &VariantABIReport{
			Level: "v2", Linkage: LinkageDynamic, Interpreter: testLdLinux,
			Dependencies: []string{testLibc}, Completeness: MetadataAbsent,
		}
		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, err)
		assert.True(t, res.Consistent)
		assert.Equal(t, ComparisonConsistent, res.Status)
	})

	// Row 3: Unknown/partial/unsupported versions, other known fields equal -> Allow with limitation (Unknown status)
	t.Run("Unknown versions, other fields equal -> status unknown, allowed", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{
			Level: "v1", Linkage: LinkageDynamic, Interpreter: testLdLinux,
			Dependencies: []string{testLibc}, Completeness: MetadataComplete,
		}
		r2 := &VariantABIReport{
			Level: "v2", Linkage: LinkageDynamic, Interpreter: testLdLinux,
			Dependencies: []string{testLibc}, Completeness: MetadataUnsupported,
		}
		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, err)
		assert.False(t, res.Consistent)
		assert.Equal(t, ComparisonUnknown, res.Status)
		assert.NotEmpty(t, res.Warnings)
	})

	// Row 4: Unknown versions plus known interpreter mismatch -> Reject by default, override preserves inconsistent status
	t.Run("Unknown versions plus known interpreter mismatch", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{
			Level: "v1", Linkage: LinkageDynamic, Interpreter: testLdLinux,
			Dependencies: []string{testLibc}, Completeness: MetadataComplete,
		}
		r2 := &VariantABIReport{
			Level: "v2", Linkage: LinkageDynamic, Interpreter: testMuslLoader,
			Dependencies: []string{testLibc}, Completeness: MetadataUnsupported,
		}
		_, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMismatch))

		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, true)
		require.NoError(t, err)
		assert.False(t, res.Consistent)
		assert.True(t, res.Overridden)
		assert.Equal(t, ComparisonInconsistent, res.Status)
	})

	// Row 5: Explicit ELF-validation skip -> Status ComparisonSkipped
	t.Run("Skipped validation -> ComparisonSkipped", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{Level: "v1", Completeness: MetadataSkipped}
		r2 := &VariantABIReport{Level: "v2", Completeness: MetadataSkipped}
		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, err)
		assert.Equal(t, ComparisonSkipped, res.Status)
		assert.False(t, res.Consistent)
	})
}

func TestInspectELFABI_ExhaustiveErrorBranches(t *testing.T) {
	t.Parallel()

	t.Run("Normal section header table error paths and valid path", func(t *testing.T) {
		t.Parallel()
		// Valid section headers
		raw := buildSyntheticELF(t, syntheticELFOpts{
			shoff:      64,
			shentsize:  64,
			largeShnum: 2,
		})
		err := PreflightELFHeaders(raw)
		require.NoError(t, err)

		// Section headers count exceeds budget
		rawBig := buildSyntheticELF(t, syntheticELFOpts{
			shoff:      64,
			shentsize:  64,
			largeShnum: 65535,
		})
		// Write 65537 into e_shnum at offset 60
		// Wait, e_shnum is uint16, so max is 65535, but MaxSectionHeaders = 65536.
		_ = rawBig

		// Section header table extends beyond file
		rawTrunc := raw[:120]
		err = PreflightELFHeaders(rawTrunc)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("PT_INTERP error paths", func(t *testing.T) {
		t.Parallel()
		// 1. PT_INTERP size > MaxInterpreterSize (4096)
		rawInterpHuge := buildSyntheticELF(t, syntheticELFOpts{
			interpRaw: make([]byte, MaxInterpreterSize+10),
		})
		_, err := InspectELFABI(rawInterpHuge)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// 2. PT_INTERP size == 0
		rawInterpZero := buildSyntheticELF(t, syntheticELFOpts{
			interp: "a",
		})
		// Find PT_INTERP (tag 3) and set p_filesz to 0
		for i := 64; i < 64+4*56; i += 56 {
			pType := binary.LittleEndian.Uint32(rawInterpZero[i : i+4])
			if pType == uint32(elf.PT_INTERP) {
				binary.LittleEndian.PutUint64(rawInterpZero[i+32:i+40], 0) // p_filesz = 0
				break
			}
		}
		_, err = InspectELFABI(rawInterpZero)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// 3. PT_INTERP p_offset extends beyond file
		rawInterpBeyond := buildSyntheticELF(t, syntheticELFOpts{interp: testLdLinux})
		for i := 64; i < 64+4*56; i += 56 {
			pType := binary.LittleEndian.Uint32(rawInterpBeyond[i : i+4])
			if pType == uint32(elf.PT_INTERP) {
				binary.LittleEndian.PutUint64(rawInterpBeyond[i+8:i+16], 999999) // p_offset
				break
			}
		}
		_, err = InspectELFABI(rawInterpBeyond)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// 4. PT_DYNAMIC p_offset extends beyond file
		rawDynBeyond := buildSyntheticELF(t, syntheticELFOpts{dependencies: []string{testLibc}})
		for i := 64; i < 64+4*56; i += 56 {
			pType := binary.LittleEndian.Uint32(rawDynBeyond[i : i+4])
			if pType == uint32(elf.PT_DYNAMIC) {
				binary.LittleEndian.PutUint64(rawDynBeyond[i+8:i+16], 999999) // p_offset
				break
			}
		}
		_, err = InspectELFABI(rawDynBeyond)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Version requirements normalization and sorting", func(t *testing.T) {
		t.Parallel()
		reqs := []VersionRequirement{
			{Library: testLibc, Version: "GLIBC_2.14", Flags: 1},
			{Library: testLibc, Version: "GLIBC_2.2.5", Flags: 0},
			{Library: testLibc, Version: "GLIBC_2.14", Flags: 0},
			{Library: "libm.so.6", Version: "GLIBC_2.2.5", Flags: 0},
		}
		norm := normalizeVersionRequirements(reqs)
		assert.Len(t, norm, 4)
		assert.Equal(t, testLibc, norm[0].Library)
		assert.Equal(t, "GLIBC_2.14", norm[0].Version)
		assert.Equal(t, uint16(0), norm[0].Flags)
		assert.Equal(t, "GLIBC_2.14", norm[1].Version)
		assert.Equal(t, uint16(1), norm[1].Flags)
	})

	t.Run("MaxArtifactReportBytes exceeded in comparison", func(t *testing.T) {
		t.Parallel()
		// Multiple variants whose aggregate report size exceeds MaxArtifactReportBytes (64 MiB)
		bigStr := strings.Repeat("B", 1024*1024) // 1 MiB
		var reports []*VariantABIReport
		for i := range 65 {
			reports = append(reports, &VariantABIReport{
				Level:        fmt.Sprintf("v%d", i),
				Dependencies: []string{bigStr},
				Completeness: MetadataComplete,
			})
		}
		_, err := CompareVariantABIs(reports, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
	})

	t.Run("Skipped status in multi-variant comparison", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{Level: "v1", Completeness: MetadataSkipped}
		r2 := &VariantABIReport{Level: "v2", Completeness: MetadataComplete}
		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, err)
		assert.Equal(t, ComparisonSkipped, res.Status)
		assert.False(t, res.Consistent)

		r3 := &VariantABIReport{Level: "v1", Completeness: MetadataComplete}
		r4 := &VariantABIReport{Level: "v2", Completeness: MetadataSkipped}
		res2, err := CompareVariantABIs([]*VariantABIReport{r3, r4}, false)
		require.NoError(t, err)
		assert.Equal(t, ComparisonSkipped, res2.Status)
		assert.False(t, res2.Consistent)
	})

	t.Run("Dynamic metadata: STRTAB size exceeds budget", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{
			dependencies: []string{testLibc},
		})
		// In dynamic segment, find DT_STRSZ (tag 10) and set to MaxABIStringBytesPerInput + 10
		for i := 0; i <= len(raw)-16; i += 16 {
			dTag := binary.LittleEndian.Uint64(raw[i : i+8])
			if dTag == uint64(elf.DT_STRSZ) {
				binary.LittleEndian.PutUint64(raw[i+8:i+16], MaxABIStringBytesPerInput+10)
				break
			}
		}
		_, err := InspectELFABI(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
	})
}

func TestInspectELFABI_AdditionalExhaustiveBranches(t *testing.T) {
	t.Parallel()

	t.Run("validateELFHeaderMagicAndEncoding truncated header", func(t *testing.T) {
		t.Parallel()
		_, err := validateELFHeaderMagicAndEncoding([]byte{0x7f, 'E'})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("Big endian ELF header", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{data: byte(elf.ELFDATA2MSB)})
		rep, err := InspectELFABI(raw)
		require.NoError(t, err)
		assert.Equal(t, "ELFCLASS64", rep.Class)
		assert.Equal(t, LinkageStatic, rep.Linkage)
	})

	t.Run("validateProgramHeaderTable branches", func(t *testing.T) {
		t.Parallel()
		// ePhnum == 0
		err := validateProgramHeaderTable(nil, 1000, 64, 56, 0)
		require.NoError(t, err)

		// ePhoff > fileSize
		err = validateProgramHeaderTable(nil, 100, 200, 56, 1)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// acc.chargeMetadata limit
		acc := &inputAccounting{metadataBytesRead: MaxMetadataBytesPerInput}
		err = validateProgramHeaderTable(acc, 1000, 64, 56, 1)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
	})

	t.Run("validateSectionHeaderTable branches", func(t *testing.T) {
		t.Parallel()
		// eShoff + eShentsize > fileSize (eShnum == 0)
		raw := make([]byte, 100)
		err := validateSectionHeaderTable(nil, raw, binary.LittleEndian, 100, 80, 64, 0)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// Standard section header table chargeMetadata error
		acc := &inputAccounting{metadataBytesRead: MaxMetadataBytesPerInput}
		err = validateSectionHeaderTable(acc, raw, binary.LittleEndian, 10000, 64, 64, 2)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// Extended section header table chargeMetadata error
		extData := make([]byte, 2000)
		binary.LittleEndian.PutUint64(extData[64+32:64+40], 2) // realShnum = 2
		err = validateSectionHeaderTable(acc, extData, binary.LittleEndian, 2000, 64, 64, 0)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
	})

	t.Run("preflightELFHeadersWithAccounting branches", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{})

		// acc.chargeMetadata(minELFHeaderSize) limit
		acc := &inputAccounting{metadataBytesRead: MaxMetadataBytesPerInput}
		err := preflightELFHeadersWithAccounting(acc, raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// eEhsize < minELFHeaderSize
		rawShortEhsize := append([]byte(nil), raw...)
		binary.LittleEndian.PutUint16(rawShortEhsize[52:54], 32)
		err = PreflightELFHeaders(rawShortEhsize)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// eEhsize > len(data)
		rawLongEhsize := append([]byte(nil), raw...)
		binary.LittleEndian.PutUint16(rawLongEhsize[52:54], uint16(len(raw)+10))
		err = PreflightELFHeaders(rawLongEhsize)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("translateVaddr zero filesz segment ignored", func(t *testing.T) {
		t.Parallel()
		loads := []loadSegment{
			{off: 0, vaddr: 0x1000, filesz: 0, memsz: 0x1000},
			{off: 100, vaddr: 0x2000, filesz: 100, memsz: 100},
		}
		off, err := translateVaddr(loads, 1000, 0x2010, 10)
		require.NoError(t, err)
		assert.Equal(t, uint64(116), off)
	})

	t.Run("PT_LOAD filesz > memsz rejected", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{})
		binary.LittleEndian.PutUint64(raw[64+32:64+40], 500) // filesz
		binary.LittleEndian.PutUint64(raw[64+40:64+48], 100) // memsz
		_, err := InspectELFABI(raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("PT_INTERP string budget exhausted", func(t *testing.T) {
		t.Parallel()
		raw := buildSyntheticELF(t, syntheticELFOpts{interp: testLdLinux})
		acc := &inputAccounting{retainedStringBytes: MaxABIStringBytesPerInput}
		_, err := inspectELFABIWithAccounting(acc, raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
	})

	t.Run("parseNeededDependencies out of bounds offset", func(t *testing.T) {
		t.Parallel()
		_, err := parseNeededDependencies([]byte("libc.so.6\x00"), 10, []uint64{20}, &inputAccounting{})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("parseDynamicMetadata error paths", func(t *testing.T) {
		t.Parallel()
		// Dynamic metadata chargeMetadata error
		raw := buildSyntheticELF(t, syntheticELFOpts{dependencies: []string{testLibc}})
		acc := &inputAccounting{metadataBytesRead: MaxMetadataBytesPerInput}
		_, err := inspectELFABIWithAccounting(acc, raw)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// Missing DT_STRTAB when DT_NEEDED present
		rawNoStrtab := buildSyntheticELF(t, syntheticELFOpts{dependencies: []string{testLibc}})
		ePhoff := binary.LittleEndian.Uint64(rawNoStrtab[32:40])
		ePhnum := int(binary.LittleEndian.Uint16(rawNoStrtab[56:58]))
		for i := 0; i < ePhnum; i++ {
			phOff := int(ePhoff) + i*56
			pType := binary.LittleEndian.Uint32(rawNoStrtab[phOff : phOff+4])
			if pType == uint32(elf.PT_DYNAMIC) {
				dynOff := int(binary.LittleEndian.Uint64(rawNoStrtab[phOff+8 : phOff+16]))
				dynSz := int(binary.LittleEndian.Uint64(rawNoStrtab[phOff+32 : phOff+40]))
				for d := dynOff; d < dynOff+dynSz; d += 16 {
					tag := binary.LittleEndian.Uint64(rawNoStrtab[d : d+8])
					if tag == uint64(elf.DT_STRTAB) {
						binary.LittleEndian.PutUint64(rawNoStrtab[d:d+8], 0xdeadbeef)
						break
					}
				}
				break
			}
		}
		_, err = InspectELFABI(rawNoStrtab)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// DT_STRSZ chargeMetadata error
		rawStrHuge := buildSyntheticELF(t, syntheticELFOpts{dependencies: []string{testLibc}})
		ePhoffHuge := binary.LittleEndian.Uint64(rawStrHuge[32:40])
		ePhnumHuge := int(binary.LittleEndian.Uint16(rawStrHuge[56:58]))
		for i := 0; i < ePhnumHuge; i++ {
			phOff := int(ePhoffHuge) + i*56
			pType := binary.LittleEndian.Uint32(rawStrHuge[phOff : phOff+4])
			if pType == uint32(elf.PT_DYNAMIC) {
				dynOff := int(binary.LittleEndian.Uint64(rawStrHuge[phOff+8 : phOff+16]))
				dynSz := int(binary.LittleEndian.Uint64(rawStrHuge[phOff+32 : phOff+40]))
				for d := dynOff; d < dynOff+dynSz; d += 16 {
					tag := binary.LittleEndian.Uint64(rawStrHuge[d : d+8])
					if tag == uint64(elf.DT_STRSZ) {
						binary.LittleEndian.PutUint64(rawStrHuge[d+8:d+16], 1000)
						break
					}
				}
				break
			}
		}
		acc2 := &inputAccounting{metadataBytesRead: MaxMetadataBytesPerInput - 200}
		_, err = inspectELFABIWithAccounting(acc2, rawStrHuge)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// parseNeededDependencies error inside parseDynamicMetadata
		rawBadDep := buildSyntheticELF(t, syntheticELFOpts{dependencies: []string{testLibc}})
		ePhoffBad := binary.LittleEndian.Uint64(rawBadDep[32:40])
		ePhnumBad := int(binary.LittleEndian.Uint16(rawBadDep[56:58]))
		for i := 0; i < ePhnumBad; i++ {
			phOff := int(ePhoffBad) + i*56
			pType := binary.LittleEndian.Uint32(rawBadDep[phOff : phOff+4])
			if pType == uint32(elf.PT_DYNAMIC) {
				dynOff := int(binary.LittleEndian.Uint64(rawBadDep[phOff+8 : phOff+16]))
				dynSz := int(binary.LittleEndian.Uint64(rawBadDep[phOff+32 : phOff+40]))
				for d := dynOff; d < dynOff+dynSz; d += 16 {
					tag := binary.LittleEndian.Uint64(rawBadDep[d : d+8])
					if tag == uint64(elf.DT_NEEDED) {
						binary.LittleEndian.PutUint64(rawBadDep[d+8:d+16], 99999) // bad offset
						break
					}
				}
				break
			}
		}
		_, err = InspectELFABI(rawBadDep)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("parseVernauxChain error paths", func(t *testing.T) {
		t.Parallel()
		strtab := []byte(testLibc + "\x00GLIBC_2.2.5\x00")
		data := make([]byte, 512)
		binary.LittleEndian.PutUint16(data[100+4:100+6], 0)        // vna_flags
		binary.LittleEndian.PutUint32(data[100+8:100+12], 10)      // vna_name points to "GLIBC_2.2.5"
		binary.LittleEndian.PutUint32(data[100+12:100+16], 999999) // vna_next invalid

		// Version records limit
		accRec := &inputAccounting{versionRecords: MaxVersionRecordsPerInput}
		err := parseVernauxChain(
			data, binary.LittleEndian, 100, 100, 100, 1, 0, testLibc, strtab, uint64(len(strtab)), accRec, &VariantABIReport{},
		)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// currVernauxOff out of bounds
		err = parseVernauxChain(
			data, binary.LittleEndian, 100, 50, 200, 1, 0, testLibc, strtab, uint64(len(strtab)), &inputAccounting{}, &VariantABIReport{},
		)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// vna_name not NUL-terminated
		strtabNoNul := []byte(testLibc + "\x00GLIBC_NO_NUL")
		err = parseVernauxChain(
			data, binary.LittleEndian, 100, 100, 100, 1, 0, testLibc, strtabNoNul, uint64(len(strtabNoNul)), &inputAccounting{}, &VariantABIReport{},
		)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// String budget exceeded
		accStr := &inputAccounting{retainedStringBytes: MaxABIStringBytesPerInput}
		err = parseVernauxChain(
			data, binary.LittleEndian, 100, 100, 100, 1, 0, testLibc, strtab, uint64(len(strtab)), accStr, &VariantABIReport{},
		)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// advanceELFRelativeOffset error when vn_cnt > 1
		err = parseVernauxChain(
			data, binary.LittleEndian, 100, 100, 100, 2, 0, testLibc, strtab, uint64(len(strtab)), &inputAccounting{}, &VariantABIReport{},
		)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("parseVerneed error paths", func(t *testing.T) {
		t.Parallel()
		// DT_VERNEEDNUM exceeds budget
		err := parseVerneed(
			nil, nil, 1000, binary.LittleEndian, 0x1000, MaxVersionRecordsPerInput+1, nil, &inputAccounting{}, &VariantABIReport{},
		)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// Metadata budget exceeded
		accMeta := &inputAccounting{metadataBytesRead: MaxMetadataBytesPerInput}
		err = parseVerneed(nil, nil, 1000, binary.LittleEndian, 0x1000, 1, nil, accMeta, &VariantABIReport{})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// Address translation error
		err = parseVerneed(nil, nil, 1000, binary.LittleEndian, 0x999999, 1, nil, &inputAccounting{}, &VariantABIReport{})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// Version records limit
		loads := []loadSegment{{off: 0, vaddr: 0x1000, filesz: 1000, memsz: 1000}}
		data := make([]byte, 1000)
		binary.LittleEndian.PutUint16(data[0:2], 1) // vn_version = 1
		strtab := []byte("libc.so.6\x00")
		accRec := &inputAccounting{versionRecords: MaxVersionRecordsPerInput}
		err = parseVerneed(data, loads, 1000, binary.LittleEndian, 0x1000, 1, strtab, accRec, &VariantABIReport{})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// currVerneedOff out of bounds
		loadsShort := []loadSegment{{off: 0, vaddr: 0x1000, filesz: 8, memsz: 8}}
		err = parseVerneed(data, loadsShort, 1000, binary.LittleEndian, 0x1000, 1, strtab, &inputAccounting{}, &VariantABIReport{})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// vn_file string offset out of bounds
		binary.LittleEndian.PutUint32(data[4:8], 9999) // vn_file
		err = parseVerneed(data, loads, 1000, binary.LittleEndian, 0x1000, 1, strtab, &inputAccounting{}, &VariantABIReport{})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// vn_file not NUL-terminated
		binary.LittleEndian.PutUint32(data[4:8], 0)
		strtabNoNul := []byte("libc_no_nul")
		err = parseVerneed(data, loads, 1000, binary.LittleEndian, 0x1000, 1, strtabNoNul, &inputAccounting{}, &VariantABIReport{})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// String budget exceeded for vn_file
		accStr := &inputAccounting{retainedStringBytes: MaxABIStringBytesPerInput}
		err = parseVerneed(data, loads, 1000, binary.LittleEndian, 0x1000, 1, strtab, accStr, &VariantABIReport{})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// Invalid vn_aux offset (< 16 when vn_cnt > 0)
		binary.LittleEndian.PutUint16(data[2:4], 1)  // vn_cnt = 1
		binary.LittleEndian.PutUint32(data[8:12], 4) // vn_aux = 4 (< 16)
		err = parseVerneed(data, loads, 1000, binary.LittleEndian, 0x1000, 1, strtab, &inputAccounting{}, &VariantABIReport{})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))

		// advanceELFRelativeOffset error when verneedNum > 1
		binary.LittleEndian.PutUint16(data[2:4], 0)        // vn_cnt = 0 (legitimate zero count)
		binary.LittleEndian.PutUint32(data[8:12], 0)       // vn_aux = 0
		binary.LittleEndian.PutUint32(data[12:16], 999999) // vn_next invalid
		err = parseVerneed(data, loads, 1000, binary.LittleEndian, 0x1000, 2, strtab, &inputAccounting{}, &VariantABIReport{})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})
}

func TestCompareVariantABIs_AdditionalBranches(t *testing.T) {
	t.Parallel()

	t.Run("validateReportBudget limits", func(t *testing.T) {
		t.Parallel()
		// 1. Interpreter length > MaxInterpreterSize
		repInterp := &VariantABIReport{Level: "v1", Interpreter: strings.Repeat("A", MaxInterpreterSize+1)}
		_, err := CompareVariantABIs([]*VariantABIReport{repInterp}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// 2. Dependencies count > MaxDynamicEntries
		repDeps := &VariantABIReport{Level: "v1", Dependencies: make([]string, MaxDynamicEntries+1)}
		_, err = CompareVariantABIs([]*VariantABIReport{repDeps}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// 3. Version requirements count > MaxVersionRecordsPerInput
		repVers := &VariantABIReport{Level: "v1", VersionRequirements: make([]VersionRequirement, MaxVersionRecordsPerInput+1)}
		_, err = CompareVariantABIs([]*VariantABIReport{repVers}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))

		// 4. Decoded string bytes > MaxABIStringBytesPerInput
		repStrs := &VariantABIReport{
			Level:        "v1",
			Dependencies: []string{strings.Repeat("D", MaxABIStringBytesPerInput+1)},
		}
		_, err = CompareVariantABIs([]*VariantABIReport{repStrs}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
	})

	t.Run("Single report with warnings preserved", func(t *testing.T) {
		t.Parallel()
		rep := &VariantABIReport{Level: "v1", Completeness: MetadataComplete, Warnings: []string{"single report warning"}}
		res, err := CompareVariantABIs([]*VariantABIReport{rep}, false)
		require.NoError(t, err)
		assert.Contains(t, res.Warnings, "single report warning")
	})

	t.Run("Multi-variant baseline with MetadataPartial and warnings", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageDynamic,
			Completeness: MetadataPartial,
			Warnings:     []string{"baseline warning"},
		}
		r2 := &VariantABIReport{
			Level:        "v2",
			Linkage:      LinkageDynamic,
			Completeness: MetadataComplete,
			Warnings:     []string{"current warning"},
		}
		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, err)
		assert.Equal(t, ComparisonUnknown, res.Status)
		assert.Contains(t, res.Warnings, "baseline warning")
		assert.Contains(t, res.Warnings, "current warning")
	})
}
