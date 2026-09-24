package pack

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testLdLinux  = "/lib64/ld-linux-x86-64.so.2"
	testLibc     = "libc.so.6"
	testLibm     = "libm.so.6"
	testGlibc225 = "GLIBC_2.2.5"
	testGlibc214 = "GLIBC_2.14"
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
	order := binary.LittleEndian

	class := byte(elf.ELFCLASS64)
	if opts.class != 0 {
		class = opts.class
	}
	dataEnc := byte(elf.ELFDATA2LSB)
	if opts.data != 0 {
		dataEnc = opts.data
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
			interp:       "/lib/ld-musl-x86_64.so.1",
			dependencies: []string{"libc.musl-x86_64.so.1"},
		})
		rep, err := InspectELFABI(raw)
		require.NoError(t, err)
		assert.Equal(t, LinkageDynamic, rep.Linkage)
		assert.Equal(t, "/lib/ld-musl-x86_64.so.1", rep.Interpreter)
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
			Interpreter:  "/lib/ld-musl-x86_64.so.1",
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
			Interpreter:  "/lib/ld-musl-x86_64.so.1",
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
		interp:       "/lib64/ld-linux-x86-64.so.2",
		dependencies: []string{"libc.so.6"},
		verneeds: []syntheticVerneed{
			{file: "libc.so.6", versions: []syntheticVernaux{{name: "GLIBC_2.2.5"}}},
		},
	})
	require.NoError(t, os.WriteFile(v1Path, v1Raw, 0o755))

	// v3 has different interpreter (/lib/ld-musl-x86_64.so.1)
	v3Path := filepath.Join(tempDir, "v3")
	v3Raw := buildSyntheticELF(t, syntheticELFOpts{
		eType:        uint16(elf.ET_DYN),
		interp:       "/lib/ld-musl-x86_64.so.1",
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
		TargetOS:   "linux",
		TargetArch: "amd64",
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
		interp:       "/lib64/ld-linux-x86-64.so.2",
		dependencies: []string{"libc.so.6"},
		verneeds: []syntheticVerneed{
			{file: "libc.so.6", versions: []syntheticVernaux{{name: "GLIBC_2.2.5"}}},
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
