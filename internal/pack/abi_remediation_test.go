package pack

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R3 Regression: Known-field comparison independent of first variant.
func TestCompareVariantABIs_R3_Permutations(t *testing.T) {
	vUnknown := &VariantABIReport{
		Level:          "v1",
		Linkage:        LinkageDynamic,
		HasInterpreter: true,
		Interpreter:    "/lib64/ld-linux-x86-64.so.2",
		Dependencies:   []string{testLibc},
		Completeness:   MetadataUnsupported,
	}
	vKnown1 := &VariantABIReport{
		Level:          "v2",
		Linkage:        LinkageDynamic,
		HasInterpreter: true,
		Interpreter:    "/lib64/ld-linux-x86-64.so.2",
		Dependencies:   []string{testLibc},
		Completeness:   MetadataComplete,
		VersionRequirements: []VersionRequirement{
			{Library: testLibc, Version: "GLIBC_2.17"},
		},
	}
	vKnown2 := &VariantABIReport{
		Level:          "v3",
		Linkage:        LinkageDynamic,
		HasInterpreter: true,
		Interpreter:    "/lib64/ld-linux-x86-64.so.2",
		Dependencies:   []string{testLibc},
		Completeness:   MetadataComplete,
		VersionRequirements: []VersionRequirement{
			{Library: testLibc, Version: "GLIBC_2.34"},
		},
	}

	reports := []*VariantABIReport{vUnknown, vKnown1, vKnown2}
	perms := [][]*VariantABIReport{
		{reports[0], reports[1], reports[2]},
		{reports[0], reports[2], reports[1]},
		{reports[1], reports[0], reports[2]},
		{reports[1], reports[2], reports[0]},
		{reports[2], reports[0], reports[1]},
		{reports[2], reports[1], reports[0]},
	}

	for i, perm := range perms {
		t.Run(fmt.Sprintf("Permutation_%d", i), func(t *testing.T) {
			// Without override: MUST fail with ErrABIMismatch regardless of permutation!
			artRep, err := CompareVariantABIs(perm, false)
			require.Error(t, err, "must reject known mismatch even if unknown variant is first")
			require.True(t, errors.Is(err, ErrABIMismatch), "error must wrap ErrABIMismatch, got: %v", err)
			require.False(t, artRep.Consistent, "Consistent must be false on mismatch")
			require.Equal(t, ComparisonInconsistent, artRep.Status)
			require.NotEmpty(t, artRep.Differences)

			// With override: MUST permit packaging, with Overridden=true and Status=ComparisonInconsistent
			artRepOvr, errOvr := CompareVariantABIs(perm, true)
			require.NoError(t, errOvr, "with allowMixedABI, comparison must not return an error")
			require.True(t, artRepOvr.Overridden, "Overridden must be true")
			require.False(t, artRepOvr.Consistent, "Consistent must be false when inconsistent")
			require.Equal(t, ComparisonInconsistent, artRepOvr.Status)
			require.NotEmpty(t, artRepOvr.Differences)
		})
	}
}

// R4 Regression: Version-declaration structural validation.
func TestScanDynamicMetadata_R4_PairedTags(t *testing.T) {
	// Helper to build a minimal 64-bit LE ELF with PT_DYNAMIC and given dynamic entries
	buildELFWithDynTags := func(dynEntries [][2]uint64, strtabContent []byte) []byte {
		buf := new(bytes.Buffer)
		// ELF header (64 bytes)
		eh := make([]byte, 64)
		copy(eh[:4], []byte{0x7f, 'E', 'L', 'F'})
		eh[4] = 2 // 64-bit
		eh[5] = 1 // 2's complement, little endian
		eh[6] = 1 // EV_CURRENT
		eh[7] = 0 // ELFOSABI_NONE
		binary.LittleEndian.PutUint16(eh[16:18], uint16(elf.ET_DYN))
		binary.LittleEndian.PutUint16(eh[18:20], uint16(elf.EM_X86_64))
		binary.LittleEndian.PutUint32(eh[20:24], 1)  // EV_CURRENT
		binary.LittleEndian.PutUint64(eh[32:40], 64) // e_phoff
		binary.LittleEndian.PutUint16(eh[52:54], 64) // e_ehsize
		binary.LittleEndian.PutUint16(eh[54:56], 56) // e_phentsize
		binary.LittleEndian.PutUint16(eh[56:58], 2)  // e_phnum (LOAD, DYNAMIC)
		binary.LittleEndian.PutUint16(eh[58:60], 64) // e_shentsize
		binary.LittleEndian.PutUint16(eh[60:62], 0)  // e_shnum
		buf.Write(eh)

		// Program headers:
		// 0: PT_LOAD covering whole file
		// 1: PT_DYNAMIC
		phLoad := make([]byte, 56)
		binary.LittleEndian.PutUint32(phLoad[0:4], uint32(elf.PT_LOAD))
		binary.LittleEndian.PutUint32(phLoad[4:8], 7)          // PF_R | PF_W | PF_X
		binary.LittleEndian.PutUint64(phLoad[8:16], 0)         // p_offset
		binary.LittleEndian.PutUint64(phLoad[16:24], 0x400000) // p_vaddr
		binary.LittleEndian.PutUint64(phLoad[24:32], 0x400000) // p_paddr
		// filesz and memsz will be updated at the end

		phDyn := make([]byte, 56)
		binary.LittleEndian.PutUint32(phDyn[0:4], uint32(elf.PT_DYNAMIC))
		binary.LittleEndian.PutUint32(phDyn[4:8], 6) // PF_R | PF_W
		dynOffset := uint64(64 + 56*2)
		dynVaddr := 0x400000 + dynOffset
		binary.LittleEndian.PutUint64(phDyn[8:16], dynOffset) // p_offset
		binary.LittleEndian.PutUint64(phDyn[16:24], dynVaddr) // p_vaddr
		binary.LittleEndian.PutUint64(phDyn[24:32], dynVaddr) // p_paddr
		dynSize := uint64((len(dynEntries) + 1) * 16)         // entries + DT_NULL
		binary.LittleEndian.PutUint64(phDyn[32:40], dynSize)  // p_filesz
		binary.LittleEndian.PutUint64(phDyn[40:48], dynSize)  // p_memsz

		buf.Write(phLoad)
		buf.Write(phDyn)

		// Write dynamic entries
		for _, ent := range dynEntries {
			var d [16]byte
			binary.LittleEndian.PutUint64(d[0:8], ent[0])
			val := ent[1]
			if val == 0xdeadbeef {
				// marker for valid mapped address
				val = dynVaddr + dynSize
			}
			binary.LittleEndian.PutUint64(d[8:16], val)
			buf.Write(d[:])
		}
		// DT_NULL
		var dNull [16]byte
		buf.Write(dNull[:])

		// Write STRTAB if provided
		if len(strtabContent) > 0 {
			buf.Write(strtabContent)
		}

		totalSize := uint64(buf.Len())
		data := buf.Bytes()
		// Update PT_LOAD filesz and memsz
		binary.LittleEndian.PutUint64(data[64+32:64+40], totalSize)
		binary.LittleEndian.PutUint64(data[64+40:64+48], totalSize)
		return data
	}

	strtab := []byte("\x00libc.so.6\x00GLIBC_2.17\x00")
	const mappedAddr = 0xdeadbeef

	t.Run("DT_VERNEEDNUM_without_DT_VERNEED_must_fail", func(t *testing.T) {
		elfData := buildELFWithDynTags([][2]uint64{
			{uint64(elf.DT_STRTAB), mappedAddr},
			{uint64(elf.DT_STRSZ), uint64(len(strtab))},
			{uint64(elf.DT_VERNEEDNUM), 1},
		}, strtab)

		_, err := InspectELFABI(elfData)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata), "must be ErrABIMetadata, got: %v", err)
	})

	t.Run("DT_VERNEED_without_DT_VERNEEDNUM_must_fail", func(t *testing.T) {
		elfData := buildELFWithDynTags([][2]uint64{
			{uint64(elf.DT_STRTAB), mappedAddr},
			{uint64(elf.DT_STRSZ), uint64(len(strtab))},
			{uint64(elf.DT_VERNEED), mappedAddr},
		}, strtab)

		_, err := InspectELFABI(elfData)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata), "must be ErrABIMetadata, got: %v", err)
	})

	t.Run("Lone_zero_count_DT_VERNEEDNUM_must_fail", func(t *testing.T) {
		elfData := buildELFWithDynTags([][2]uint64{
			{uint64(elf.DT_STRTAB), mappedAddr},
			{uint64(elf.DT_STRSZ), uint64(len(strtab))},
			{uint64(elf.DT_VERNEEDNUM), 0},
		}, strtab)

		_, err := InspectELFABI(elfData)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata), "must be ErrABIMetadata, got: %v", err)
	})
}
