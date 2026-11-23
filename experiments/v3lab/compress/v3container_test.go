package compress

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	testChunkSize64K    = 64 * 1024
	testDictChunkSize   = 32 * 1024
	testDecodeWorkers   = 4
	elfHeaderBytes      = 64
	shdrEntryBytes      = 64
	shstrtabBytes       = 20
	synthArm64TextOff   = testDictChunkSize
	synthArm64CodeBytes = 64 * 1024

	elfOffType      = 16
	elfOffMachine   = 18
	elfOffVersion   = 20
	elfOffShoff     = 40
	elfOffEhsize    = 52
	elfOffShentsize = 58
	elfOffShnum     = 60
	elfOffShstrndx  = 62
	shOffType       = 4
	shOffFlags      = 8
	shOffOffset     = 24
	shOffSize       = 32
	shOffAddralign  = 48
	shCount3        = 3
	expectedExtents = 4
	shstrIdx2       = 2
	shstrTextName   = 1
	shstrTabName    = 7
	blPatternStride = 16
	arm64InsnNOP    = uint32(0xD503201F)
	arm64ConstImm   = uint32(0x1234)
)

func mutateVariantTextSection(t *testing.T, baseELF []byte, xorByte byte) []byte {
	t.Helper()
	out := make([]byte, len(baseELF))
	copy(out, baseELF)
	ranges, _, err := ExecutableRangesELF(out)
	require.NoError(t, err)
	require.NotEmpty(t, ranges)
	// Modify a tiny call immediate inside .text so .rodata and most .text still share identical blocks.
	mid := (ranges[0].Start + ranges[0].End) / 2
	out[mid] ^= xorByte
	return out
}

func TestV3PackDecodeMemfdRoundTripAndDedup(t *testing.T) {
	t.Parallel()

	exePath, err := os.Executable()
	require.NoError(t, err)
	v1Raw, err := os.ReadFile(exePath) // #nosec G304 -- reading current test executable
	require.NoError(t, err)

	v2Raw := mutateVariantTextSection(t, v1Raw, 0x01)
	v3Raw := mutateVariantTextSection(t, v1Raw, 0x02)

	for _, mode := range []ChunkMode{ChunkFixed, ChunkSectionCDC} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			_, stats, packErr := PackV3(&buf, V3PackOptions{
				TargetArch:     "amd64",
				ChunkMode:      mode,
				AvgChunkSize:   testChunkSize64K,
				EnableBCJRIP:   true,
				ParallelEncode: testDecodeWorkers,
				Variants: map[string][]byte{
					"v1": v1Raw,
					"v2": v2Raw,
					"v3": v3Raw,
				},
			})
			require.NoError(t, packErr)
			require.Greater(t, stats.DeduplicatedBytes, int64(0), "expected substantial cross-variant block dedup")

			rawArchive := buf.Bytes()
			reader := bytes.NewReader(rawArchive)
			arch, parseErr := ReadV3Archive(reader, int64(len(rawArchive)))
			require.NoError(t, parseErr)
			require.Equal(t, uint16(elf.EM_X86_64), arch.Machine)

			for lvl, wantBytes := range map[string][]byte{"v1": v1Raw, "v2": v2Raw, "v3": v3Raw} {
				gotBuf, decErr := DecodeVariantBytes(arch, reader, lvl, testDecodeWorkers)
				require.NoError(t, decErr)
				require.Equal(t, wantBytes, gotBuf)

				fd, memErr := DecodeVariantToSealedMemfd(arch, reader, lvl, testDecodeWorkers, true)
				require.NoError(t, memErr)
				memBytes := make([]byte, len(wantBytes))
				require.NoError(t, preadAll(fd, memBytes, 0))
				require.Equal(t, wantBytes, memBytes)
				_ = unix.Close(fd)
			}
		})
	}
}

func TestV3RejectsTamperedContainer(t *testing.T) {
	t.Parallel()

	exePath, err := os.Executable()
	require.NoError(t, err)
	raw, err := os.ReadFile(exePath) // #nosec G304 -- test executable
	require.NoError(t, err)

	var buf bytes.Buffer
	_, _, err = PackV3(&buf, V3PackOptions{
		TargetArch:   "amd64",
		ChunkMode:    ChunkFixed,
		AvgChunkSize: testChunkSize64K,
		EnableBCJRIP: false,
		Variants:     map[string][]byte{"v1": raw},
	})
	require.NoError(t, err)

	corrupted := append([]byte(nil), buf.Bytes()...)
	corrupted[v3HeaderLen+1] ^= 0xFF
	r := bytes.NewReader(corrupted)
	arch, err := ReadV3Archive(r, int64(len(corrupted)))
	require.NoError(t, err)

	_, err = DecodeVariantBytes(arch, r, "v1", 2)
	require.Error(t, err)

	corrupted[len(corrupted)-v3TrailerSize-1] ^= 0xFF
	_, err = ReadV3Archive(bytes.NewReader(corrupted), int64(len(corrupted)))
	require.Error(t, err)
}

func TestV3DictSeedingProfitableAndTamper(t *testing.T) {
	t.Parallel()

	exePath, err := os.Executable()
	require.NoError(t, err)
	v1Raw, err := os.ReadFile(exePath) // #nosec G304 -- reading current test executable
	require.NoError(t, err)
	v2Raw := mutateVariantTextSection(t, v1Raw, 0x01)

	var buf bytes.Buffer
	arch, stats, err := PackV3(&buf, V3PackOptions{
		TargetArch:     "amd64",
		ChunkMode:      ChunkSectionCDC,
		AvgChunkSize:   testDictChunkSize,
		EnableBCJRIP:   true,
		EnableDict:     true,
		DictProfitable: false,
		DictSize:       v3DefaultDictSize,
		ParallelEncode: testDecodeWorkers,
		Variants:       map[string][]byte{"v1": v1Raw, "v2": v2Raw},
	})
	require.NoError(t, err)
	require.True(t, arch.HasDict, "stratified history seeding should produce a valid v3 block dictionary")
	require.Greater(t, stats.DictBytes, int64(0))

	rawArchive := buf.Bytes()
	reader := bytes.NewReader(rawArchive)
	parsed, err := ReadV3Archive(reader, int64(len(rawArchive)))
	require.NoError(t, err)
	require.True(t, parsed.HasDict)

	for lvl, want := range map[string][]byte{"v1": v1Raw, "v2": v2Raw} {
		got, decErr := DecodeVariantBytes(parsed, reader, lvl, testDecodeWorkers)
		require.NoError(t, decErr)
		require.Equal(t, want, got)

		fd, memErr := DecodeVariantToSealedMemfd(parsed, reader, lvl, testDecodeWorkers, true)
		require.NoError(t, memErr)
		memBytes := make([]byte, len(want))
		require.NoError(t, preadAll(fd, memBytes, 0))
		require.Equal(t, want, memBytes)
		_ = unix.Close(fd)
	}

	// Tamper the dictionary payload directly and ensure DecodeVariantBytes rejects it before decoding.
	tampered := append([]byte(nil), rawArchive...)
	tampered[parsed.DictOffset] ^= 0xFF
	_, err = DecodeVariantBytes(parsed, bytes.NewReader(tampered), "v1", 2)
	require.Error(t, err)

	// Verify DictProfitable option runs and decodes accurately.
	var profBuf bytes.Buffer
	profArch, _, profErr := PackV3(&profBuf, V3PackOptions{
		TargetArch:     "amd64",
		ChunkMode:      ChunkSectionCDC,
		AvgChunkSize:   testChunkSize64K,
		EnableBCJRIP:   true,
		EnableDict:     true,
		DictProfitable: true,
		DictSize:       v3DefaultDictSize,
		ParallelEncode: testDecodeWorkers,
		Variants:       map[string][]byte{"v1": v1Raw, "v2": v2Raw},
	})
	require.NoError(t, profErr)
	profReader := bytes.NewReader(profBuf.Bytes())
	gotProf, err := DecodeVariantBytes(profArch, profReader, "v1", testDecodeWorkers)
	require.NoError(t, err)
	require.Equal(t, v1Raw, gotProf)
}

func buildSyntheticARM64ELF(t *testing.T, tweakTailWord uint32) []byte {
	t.Helper()
	// Place .text at synthArm64TextOff (32 KiB) so ChunkFixed (32 KiB) splits .text evenly into two
	// chunks at FilterBasePos=0 and FilterBasePos=32768 that filter to the same deduplicated block.
	shstr := []byte("\x00.text\x00.shstrtab\x00")
	require.LessOrEqual(t, len(shstr), shstrtabBytes)
	textOff := uint64(synthArm64TextOff)
	shstrOff := textOff + synthArm64CodeBytes
	shoff := shstrOff + shstrtabBytes
	total := int(shoff + shCount3*shdrEntryBytes)
	img := make([]byte, total)

	copy(img[0:4], []byte{0x7F, 'E', 'L', 'F'})
	img[4] = byte(elf.ELFCLASS64)
	img[5] = byte(elf.ELFDATA2LSB)
	img[6] = byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(img[elfOffType:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(img[elfOffMachine:], uint16(elf.EM_AARCH64))
	binary.LittleEndian.PutUint32(img[elfOffVersion:], uint32(elf.EV_CURRENT))
	binary.LittleEndian.PutUint64(img[elfOffShoff:], shoff)
	binary.LittleEndian.PutUint16(img[elfOffEhsize:], elfHeaderBytes)
	binary.LittleEndian.PutUint16(img[elfOffShentsize:], shdrEntryBytes)
	binary.LittleEndian.PutUint16(img[elfOffShnum:], shCount3)
	binary.LittleEndian.PutUint16(img[elfOffShstrndx:], shstrIdx2)

	for off := 0; off < synthArm64CodeBytes; off += arm64InsnLen {
		var insn uint32
		if off%blPatternStride == 0 {
			pcWords := uint32(off/arm64InsnLen) & arm64Imm26Mask // #nosec G115 -- bounded section offset
			insn = arm64OpBL | ((arm64ConstImm - pcWords) & arm64Imm26Mask)
		} else {
			insn = arm64InsnNOP
		}
		if off == synthArm64CodeBytes-arm64InsnLen {
			insn ^= tweakTailWord
		}
		binary.LittleEndian.PutUint32(img[textOff+uint64(off):], insn)
	}
	copy(img[shstrOff:], shstr)

	s1 := int(shoff + shdrEntryBytes)
	binary.LittleEndian.PutUint32(img[s1:], shstrTextName)
	binary.LittleEndian.PutUint32(img[s1+shOffType:], uint32(elf.SHT_PROGBITS))
	binary.LittleEndian.PutUint64(img[s1+shOffFlags:], uint64(elf.SHF_ALLOC|elf.SHF_EXECINSTR))
	binary.LittleEndian.PutUint64(img[s1+shOffOffset:], textOff)
	binary.LittleEndian.PutUint64(img[s1+shOffSize:], synthArm64CodeBytes)
	binary.LittleEndian.PutUint64(img[s1+shOffAddralign:], arm64InsnLen)

	s2 := int(shoff + shstrIdx2*shdrEntryBytes)
	binary.LittleEndian.PutUint32(img[s2:], shstrTabName)
	binary.LittleEndian.PutUint32(img[s2+shOffType:], uint32(elf.SHT_STRTAB))
	binary.LittleEndian.PutUint64(img[s2+shOffOffset:], shstrOff)
	binary.LittleEndian.PutUint64(img[s2+shOffSize:], uint64(len(shstr)))
	binary.LittleEndian.PutUint64(img[s2+shOffAddralign:], 1)
	return img
}

func TestV3ARM64AlignedAndMultiOffsetDedup(t *testing.T) {
	t.Parallel()

	v80 := buildSyntheticARM64ELF(t, 0)
	v82 := buildSyntheticARM64ELF(t, 1)

	var buf bytes.Buffer
	arch, stats, err := PackV3(&buf, V3PackOptions{
		TargetArch:     "arm64",
		ChunkMode:      ChunkFixed,
		AvgChunkSize:   testDictChunkSize,
		EnableBCJRIP:   true,
		ParallelEncode: testDecodeWorkers,
		Variants:       map[string][]byte{"v8.0": v80, "v8.2": v82},
	})
	require.NoError(t, err)
	require.Equal(t, uint16(elf.EM_AARCH64), arch.Machine)
	require.Greater(t, stats.DeduplicatedBytes, int64(0))

	// Confirm v8.0 has two distinct FilterBasePos (0 and 32768) sharing the exact same filtered BlockIdx.
	parsed, err := ReadV3Archive(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	extents := parsed.Variants[0].Extents
	require.Len(t, extents, expectedExtents)
	require.True(t, extents[1].Filtered)
	require.True(t, extents[2].Filtered)
	require.Equal(t, uint32(0), extents[1].FilterBasePos)
	require.Equal(t, uint32(testDictChunkSize), extents[2].FilterBasePos)
	require.Equal(t, extents[1].BlockIdx, extents[2].BlockIdx)

	for lvl, want := range map[string][]byte{"v8.0": v80, "v8.2": v82} {
		got, decErr := DecodeVariantBytes(parsed, bytes.NewReader(buf.Bytes()), lvl, testDecodeWorkers)
		require.NoError(t, decErr)
		require.Equal(t, want, got)

		fd, memErr := DecodeVariantToSealedMemfd(parsed, bytes.NewReader(buf.Bytes()), lvl, testDecodeWorkers, true)
		require.NoError(t, memErr)
		memBytes := make([]byte, len(want))
		require.NoError(t, preadAll(fd, memBytes, 0))
		require.Equal(t, want, memBytes)
		_ = unix.Close(fd)
	}
}
