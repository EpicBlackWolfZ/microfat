package pack

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testLibA = "libA.so"
	testLibB = "libB.so"
	testLibC = "libc.so"
)

// R3 Regression: Known-field comparison independent of first variant.
func TestCompareVariantABIs_R3_Permutations(t *testing.T) {
	vUnknown := &VariantABIReport{
		Level:          "v1",
		Linkage:        LinkageDynamic,
		HasInterpreter: true,
		Interpreter:    testLdLinux,
		Dependencies:   []string{testLibc},
		Completeness:   MetadataUnsupported,
	}
	vKnown1 := &VariantABIReport{
		Level:          "v2",
		Linkage:        LinkageDynamic,
		HasInterpreter: true,
		Interpreter:    testLdLinux,
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
		Interpreter:    testLdLinux,
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

// Helper to build a minimal 64-bit LE ELF with PT_DYNAMIC and given dynamic entries
func buildTestELFWithDynTags(dynEntries [][2]uint64, strtabContent []byte) []byte {
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

// R4 Regression: Version-declaration structural validation.
func TestScanDynamicMetadata_R4_PairedTags(t *testing.T) {
	strtab := []byte("\x00libc.so.6\x00GLIBC_2.17\x00")
	const mappedAddr = 0xdeadbeef

	t.Run("DT_VERNEEDNUM_without_DT_VERNEED_must_fail", func(t *testing.T) {
		elfData := buildTestELFWithDynTags([][2]uint64{
			{uint64(elf.DT_STRTAB), mappedAddr},
			{uint64(elf.DT_STRSZ), uint64(len(strtab))},
			{uint64(elf.DT_VERNEEDNUM), 1},
		}, strtab)

		_, err := InspectELFABI(elfData)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata), "must be ErrABIMetadata, got: %v", err)
	})

	t.Run("DT_VERNEED_without_DT_VERNEEDNUM_must_fail", func(t *testing.T) {
		elfData := buildTestELFWithDynTags([][2]uint64{
			{uint64(elf.DT_STRTAB), mappedAddr},
			{uint64(elf.DT_STRSZ), uint64(len(strtab))},
			{uint64(elf.DT_VERNEED), mappedAddr},
		}, strtab)

		_, err := InspectELFABI(elfData)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata), "must be ErrABIMetadata, got: %v", err)
	})

	t.Run("Lone_zero_count_DT_VERNEEDNUM_must_fail", func(t *testing.T) {
		elfData := buildTestELFWithDynTags([][2]uint64{
			{uint64(elf.DT_STRTAB), mappedAddr},
			{uint64(elf.DT_STRSZ), uint64(len(strtab))},
			{uint64(elf.DT_VERNEEDNUM), 0},
		}, strtab)

		_, err := InspectELFABI(elfData)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata), "must be ErrABIMetadata, got: %v", err)
	})
}

func TestAccounting_CoverageAndBoundaries(t *testing.T) {
	t.Parallel()

	// NewArtifactMetadataAccounting with 0 defaults to MaxArtifactMetadataBytes
	a0 := NewArtifactMetadataAccounting(0)
	require.NotNil(t, a0)
	assert.Equal(t, uint64(MaxArtifactMetadataBytes), a0.maxBytes)

	// nil receiver checks
	var aNil *ArtifactMetadataAccounting
	require.NoError(t, aNil.check(100))
	aNil.commit(100) // must not panic

	// overflow/limit checks
	a := NewArtifactMetadataAccounting(100)
	require.NoError(t, a.check(50))
	a.commit(50)
	require.ErrorIs(t, a.check(60), ErrABIResourceLimit)

	// NewReportAccounting with 0 defaults to MaxArtifactReportBytes
	r0 := NewReportAccounting(0)
	require.NotNil(t, r0)
	assert.Equal(t, uint64(MaxArtifactReportBytes), r0.maxBytes)

	// nil receiver check
	var rNil *ReportAccounting
	require.NoError(t, rNil.reserve(100))

	// report reserve limit checks
	ra := NewReportAccounting(100)
	require.NoError(t, ra.reserve(60))
	require.ErrorIs(t, ra.reserve(50), ErrABIResourceLimit)

	// inputAccounting shared limit check
	shared := NewArtifactMetadataAccounting(10)
	acc := &inputAccounting{maxMetadataBytes: 100, shared: shared}
	require.ErrorIs(t, acc.chargeMetadata(20), ErrABIResourceLimit)

	// inputAccounting string limit check
	accStr := &inputAccounting{maxStringBytes: 10}
	require.ErrorIs(t, accStr.chargeString(20), ErrABIResourceLimit)

	// inputAccounting version record limit check
	accVer := &inputAccounting{maxVersionRecords: 1}
	require.NoError(t, accVer.chargeVersionRecord())
	require.ErrorIs(t, accVer.chargeVersionRecord(), ErrABIResourceLimit)
}

func TestDynamicTags_ConflictingDuplicates(t *testing.T) {
	t.Parallel()

	t.Run("conflicting duplicate DT_STRTAB", func(t *testing.T) {
		t.Parallel()
		data := buildTestELFWithDynTags([][2]uint64{
			{uint64(elf.DT_STRTAB), 0x400100},
			{uint64(elf.DT_STRTAB), 0x400200},
		}, nil)
		_, err := InspectELFABI(data)
		require.ErrorIs(t, err, ErrABIMetadata)
		assert.Contains(t, err.Error(), "conflicting duplicate DT_STRTAB")
	})

	t.Run("conflicting duplicate DT_STRSZ", func(t *testing.T) {
		t.Parallel()
		data := buildTestELFWithDynTags([][2]uint64{
			{uint64(elf.DT_STRSZ), 100},
			{uint64(elf.DT_STRSZ), 200},
		}, nil)
		_, err := InspectELFABI(data)
		require.ErrorIs(t, err, ErrABIMetadata)
		assert.Contains(t, err.Error(), "conflicting duplicate DT_STRSZ")
	})

	t.Run("conflicting duplicate DT_VERNEED", func(t *testing.T) {
		t.Parallel()
		data := buildTestELFWithDynTags([][2]uint64{
			{uint64(elf.DT_VERNEED), 0x400100},
			{uint64(elf.DT_VERNEED), 0x400200},
		}, nil)
		_, err := InspectELFABI(data)
		require.ErrorIs(t, err, ErrABIMetadata)
		assert.Contains(t, err.Error(), "conflicting duplicate DT_VERNEED")
	})

	t.Run("conflicting duplicate DT_VERNEEDNUM", func(t *testing.T) {
		t.Parallel()
		data := buildTestELFWithDynTags([][2]uint64{
			{uint64(elf.DT_VERNEEDNUM), 1},
			{uint64(elf.DT_VERNEEDNUM), 2},
		}, nil)
		_, err := InspectELFABI(data)
		require.ErrorIs(t, err, ErrABIMetadata)
		assert.Contains(t, err.Error(), "conflicting duplicate DT_VERNEEDNUM")
	})
}

func TestResolveDynamicStrtab_NoNeededOrVerneed(t *testing.T) {
	t.Parallel()
	tags := &dynamicTags{}
	acc := &inputAccounting{}
	strtab, err := resolveDynamicStrtab(nil, nil, 0, tags, acc)
	require.NoError(t, err)
	assert.Nil(t, strtab)
}

func TestParseDynamicMetadata_VerneedNumZeroAndMissingStrtab(t *testing.T) {
	t.Parallel()

	// verneedNum == 0 should initialize empty VersionRequirements and succeed
	strtab := []byte("\x00libc.so.6\x00GLIBC_2.17\x00")
	dataZero := buildTestELFWithDynTags([][2]uint64{
		{uint64(elf.DT_STRTAB), 0xdeadbeef},
		{uint64(elf.DT_STRSZ), uint64(len(strtab))},
		{uint64(elf.DT_VERNEED), 0xdeadbeef},
		{uint64(elf.DT_VERNEEDNUM), 0},
	}, strtab)
	rep, err := InspectELFABI(dataZero)
	require.NoError(t, err)
	assert.Empty(t, rep.VersionRequirements)

	// verneedNum > 0 but missing STRTAB or STRSZ should return ErrABIMetadata
	dataMissingStrtab := buildTestELFWithDynTags([][2]uint64{
		{uint64(elf.DT_VERNEED), 0xdeadbeef},
		{uint64(elf.DT_VERNEEDNUM), 1},
	}, nil)
	_, err = InspectELFABI(dataMissingStrtab)
	require.ErrorIs(t, err, ErrABIMetadata)
	assert.Contains(t, err.Error(), "without DT_STRTAB or DT_STRSZ")
}

func TestFormatBoundedDepList_Branches(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "none", formatBoundedDepList(nil, 5))
	assert.Equal(t, "[a, b, c ... (+2 more)]", formatBoundedDepList([]string{"a", "b", "c", "d", "e"}, 3))
}

func TestCompareVariantABIs_AccountingAndInterpreterBranches(t *testing.T) {
	t.Parallel()

	t.Run("compareDependencySets differing deps reserve error", func(t *testing.T) {
		t.Parallel()
		ra := NewReportAccounting(5)
		base := &VariantABIReport{Level: "v1", Dependencies: []string{"liba.so"}}
		curr := &VariantABIReport{Level: "v2", Dependencies: []string{"libb.so"}}
		_, _, err := compareDependencySets(ra, base, curr)
		require.ErrorIs(t, err, ErrABIResourceLimit)
	})

	t.Run("compareDependencySets differing order reserve error", func(t *testing.T) {
		t.Parallel()
		ra := NewReportAccounting(5)
		base := &VariantABIReport{Level: "v1", Dependencies: []string{"liba.so", "libb.so"}}
		curr := &VariantABIReport{Level: "v2", Dependencies: []string{"libb.so", "liba.so"}}
		_, _, err := compareDependencySets(ra, base, curr)
		require.ErrorIs(t, err, ErrABIResourceLimit)
	})

	t.Run("compareVariantInterpreters differing presence", func(t *testing.T) {
		t.Parallel()
		v1 := &VariantABIReport{Level: "v1", Linkage: LinkageDynamic, HasInterpreter: true, Interpreter: "/lib64/ld.so"}
		v2 := &VariantABIReport{Level: "v2", Linkage: LinkageDynamic, HasInterpreter: false, Interpreter: ""}
		ra := NewReportAccounting(1000)
		diffs, err := compareVariantInterpreters([]*VariantABIReport{v1, v2}, ra)
		require.NoError(t, err)
		require.Len(t, diffs, 1)
		assert.Contains(t, diffs[0], "differing interpreter configuration")
	})

	t.Run("compareVariantInterpreters reserve error on presence mismatch", func(t *testing.T) {
		t.Parallel()
		v1 := &VariantABIReport{Level: "v1", Linkage: LinkageDynamic, HasInterpreter: true, Interpreter: "/lib64/ld.so"}
		v2 := &VariantABIReport{Level: "v2", Linkage: LinkageDynamic, HasInterpreter: false, Interpreter: ""}
		ra := NewReportAccounting(5)
		_, err := compareVariantInterpreters([]*VariantABIReport{v1, v2}, ra)
		require.ErrorIs(t, err, ErrABIResourceLimit)
	})

	t.Run("compareVariantInterpreters reserve error on pathname mismatch", func(t *testing.T) {
		t.Parallel()
		v1 := &VariantABIReport{Level: "v1", Linkage: LinkageDynamic, HasInterpreter: true, Interpreter: "/lib64/ld1.so"}
		v2 := &VariantABIReport{Level: "v2", Linkage: LinkageDynamic, HasInterpreter: true, Interpreter: "/lib64/ld2.so"}
		ra := NewReportAccounting(5)
		_, err := compareVariantInterpreters([]*VariantABIReport{v1, v2}, ra)
		require.ErrorIs(t, err, ErrABIResourceLimit)
	})

	t.Run("compareVariantVersions reserve error", func(t *testing.T) {
		t.Parallel()
		v1 := &VariantABIReport{
			Level:               "v1",
			Linkage:             LinkageDynamic,
			Completeness:        MetadataComplete,
			VersionRequirements: []VersionRequirement{{Library: testLibc, Version: "GLIBC_2.17"}},
		}
		v2 := &VariantABIReport{
			Level:               "v2",
			Linkage:             LinkageDynamic,
			Completeness:        MetadataComplete,
			VersionRequirements: []VersionRequirement{{Library: testLibc, Version: "GLIBC_2.34"}},
		}
		ra := NewReportAccounting(5)
		_, err := compareVariantVersions([]*VariantABIReport{v1, v2}, ra)
		require.ErrorIs(t, err, ErrABIResourceLimit)
	})

	t.Run("CompareVariantABIsWithOptions budget failure on disclaimer", func(t *testing.T) {
		t.Parallel()
		v1 := &VariantABIReport{Level: "v1", Linkage: LinkageStatic}
		limits := ABILimits{MaxArtifactReportBytes: 10}
		_, err := CompareVariantABIsWithOptions([]*VariantABIReport{v1}, false, limits)
		require.ErrorIs(t, err, ErrABIResourceLimit)
	})

	t.Run("CompareVariantABIsWithOptions budget failure on overridden diffs", func(t *testing.T) {
		t.Parallel()
		vStatic := &VariantABIReport{Level: "v1", Linkage: LinkageStatic}
		vDynamic := &VariantABIReport{Level: "v2", Linkage: LinkageDynamic}
		// Budget enough for disclaimer + linkage diff, but not overridden warning message
		limits := ABILimits{MaxArtifactReportBytes: 250}
		_, err := CompareVariantABIsWithOptions([]*VariantABIReport{vStatic, vDynamic}, true, limits)
		require.ErrorIs(t, err, ErrABIResourceLimit)
	})

	t.Run("handleSingleReport budget failures", func(t *testing.T) {
		t.Parallel()
		vUnk := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageDynamic,
			Completeness: MetadataUnsupported,
			Warnings:     []string{"sample warning"},
		}
		// Fails reserving warning
		ra := NewReportAccounting(10)
		_, err := handleSingleReport(vUnk, ra, &ArtifactABIReport{})
		require.ErrorIs(t, err, ErrABIResourceLimit)
	})
}

func TestValidatePayloadABIs_ErrorBranches(t *testing.T) {
	t.Parallel()

	t.Run("missing variant file returns error", func(t *testing.T) {
		t.Parallel()
		opts := &Options{
			Variants: map[string]string{"v1": filepath.Join(t.TempDir(), "nonexistent.bin")},
		}
		err := validatePayloadABIs(opts, []string{"v1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading snapshotted variant v1")
	})

	t.Run("invalid ELF variant file returns error", func(t *testing.T) {
		t.Parallel()
		corruptFile := filepath.Join(t.TempDir(), "corrupt.elf")
		require.NoError(t, os.WriteFile(corruptFile, []byte("invalid elf header bytes"), 0o600))
		opts := &Options{
			Variants: map[string]string{"v1": corruptFile},
		}
		err := validatePayloadABIs(opts, []string{"v1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "inspecting declared ABI for variant v1")
	})
}

// buildTestELFWithVerneed constructs a minimal valid 64-bit LE ELF binary with PT_DYNAMIC,
// DT_STRTAB, DT_STRSZ, DT_VERNEED, and DT_VERNEEDNUM pointing to the provided verneed and strtab bytes.
func buildTestELFWithVerneed(verneedBytes, strtabBytes []byte) []byte {
	eh := make([]byte, 64)
	copy(eh[:4], []byte{0x7f, 'E', 'L', 'F'})
	eh[4] = 2 // 64-bit
	eh[5] = 1 // LE
	eh[6] = 1 // EV_CURRENT
	binary.LittleEndian.PutUint16(eh[16:18], uint16(elf.ET_DYN))
	binary.LittleEndian.PutUint16(eh[18:20], uint16(elf.EM_X86_64))
	binary.LittleEndian.PutUint32(eh[20:24], 1)
	binary.LittleEndian.PutUint64(eh[32:40], 64) // e_phoff
	binary.LittleEndian.PutUint16(eh[52:54], 64) // e_ehsize
	binary.LittleEndian.PutUint16(eh[54:56], 56) // e_phentsize
	binary.LittleEndian.PutUint16(eh[56:58], 2)  // e_phnum (LOAD, DYNAMIC)
	binary.LittleEndian.PutUint16(eh[58:60], 64) // e_shentsize
	binary.LittleEndian.PutUint16(eh[60:62], 0)  // e_shnum

	phLoad := make([]byte, 56)
	binary.LittleEndian.PutUint32(phLoad[0:4], uint32(elf.PT_LOAD))
	binary.LittleEndian.PutUint32(phLoad[4:8], 7)
	binary.LittleEndian.PutUint64(phLoad[8:16], 0)
	binary.LittleEndian.PutUint64(phLoad[16:24], 0x400000)
	binary.LittleEndian.PutUint64(phLoad[24:32], 0x400000)

	phDyn := make([]byte, 56)
	binary.LittleEndian.PutUint32(phDyn[0:4], uint32(elf.PT_DYNAMIC))
	binary.LittleEndian.PutUint32(phDyn[4:8], 6)
	dynOffset := uint64(64 + 56*2) // 176
	dynVaddr := 0x400000 + dynOffset
	binary.LittleEndian.PutUint64(phDyn[8:16], dynOffset)
	binary.LittleEndian.PutUint64(phDyn[16:24], dynVaddr)
	binary.LittleEndian.PutUint64(phDyn[24:32], dynVaddr)
	const dynEntriesCount = 5 // STRTAB, STRSZ, VERNEED, VERNEEDNUM, NULL
	dynSize := uint64(dynEntriesCount * 16)
	binary.LittleEndian.PutUint64(phDyn[32:40], dynSize)
	binary.LittleEndian.PutUint64(phDyn[40:48], dynSize)

	verneedOffset := dynOffset + dynSize // 256
	strtabOffset := verneedOffset + uint64(len(verneedBytes))
	totalSize := strtabOffset + uint64(len(strtabBytes))

	binary.LittleEndian.PutUint64(phLoad[32:40], totalSize)
	binary.LittleEndian.PutUint64(phLoad[40:48], totalSize)

	buf := new(bytes.Buffer)
	buf.Write(eh)
	buf.Write(phLoad)
	buf.Write(phDyn)

	writeDyn := func(tag, val uint64) {
		var d [16]byte
		binary.LittleEndian.PutUint64(d[0:8], tag)
		binary.LittleEndian.PutUint64(d[8:16], val)
		buf.Write(d[:])
	}
	writeDyn(uint64(elf.DT_STRTAB), 0x400000+strtabOffset)
	writeDyn(uint64(elf.DT_STRSZ), uint64(len(strtabBytes)))
	writeDyn(uint64(elf.DT_VERNEED), 0x400000+verneedOffset)
	writeDyn(uint64(elf.DT_VERNEEDNUM), 1)
	writeDyn(uint64(elf.DT_NULL), 0)

	buf.Write(verneedBytes)
	buf.Write(strtabBytes)

	return buf.Bytes()
}

// TestABI_R3_AuxiliaryAndNameValidation exercises the ABI-1 regression matrix.
func TestABI_R3_AuxiliaryAndNameValidation(t *testing.T) {
	t.Parallel()

	t.Run("positive_count_zero_aux_offset_fails", func(t *testing.T) {
		t.Parallel()
		vn := make([]byte, 16)
		binary.LittleEndian.PutUint16(vn[0:2], 1)  // vn_version = 1
		binary.LittleEndian.PutUint16(vn[2:4], 1)  // vn_cnt = 1
		binary.LittleEndian.PutUint32(vn[4:8], 1)  // vn_file = 1 (libA.so)
		binary.LittleEndian.PutUint32(vn[8:12], 0) // vn_aux = 0 (parent overlap defect)
		binary.LittleEndian.PutUint32(vn[12:16], 0)
		strtab := []byte("\x00libA.so\x00")
		elfData := buildTestELFWithVerneed(vn, strtab)
		_, err := InspectELFABI(elfData)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
		assert.Contains(t, err.Error(), "has invalid vn_aux 0 overlapping parent record")
	})

	t.Run("positive_count_offsets_1_to_15_fail", func(t *testing.T) {
		t.Parallel()
		for aux := uint32(1); aux < 16; aux++ {
			vn := make([]byte, 32)
			binary.LittleEndian.PutUint16(vn[0:2], 1)
			binary.LittleEndian.PutUint16(vn[2:4], 1)
			binary.LittleEndian.PutUint32(vn[4:8], 1)
			binary.LittleEndian.PutUint32(vn[8:12], aux)
			binary.LittleEndian.PutUint32(vn[12:16], 0)
			strtab := []byte("\x00libA.so\x00")
			elfData := buildTestELFWithVerneed(vn, strtab)
			_, err := InspectELFABI(elfData)
			require.Error(t, err, "offset %d must fail", aux)
			assert.True(t, errors.Is(err, ErrABIMetadata))
			assert.Contains(t, err.Error(), "overlapping parent record")
		}
	})

	t.Run("distinct_adjacent_auxiliary_offset_16_succeeds", func(t *testing.T) {
		t.Parallel()
		vn := make([]byte, 32)
		binary.LittleEndian.PutUint16(vn[0:2], 1)
		binary.LittleEndian.PutUint16(vn[2:4], 1)
		binary.LittleEndian.PutUint32(vn[4:8], 1)   // vn_file = 1 (libA.so)
		binary.LittleEndian.PutUint32(vn[8:12], 16) // vn_aux = 16
		binary.LittleEndian.PutUint32(vn[12:16], 0)
		// Vernaux at 16:
		binary.LittleEndian.PutUint32(vn[16:20], 0)
		binary.LittleEndian.PutUint16(vn[20:22], 0)
		binary.LittleEndian.PutUint16(vn[22:24], 0)
		binary.LittleEndian.PutUint32(vn[24:28], 9) // vna_name = 9 (GLIBC_2.17)
		binary.LittleEndian.PutUint32(vn[28:32], 0)
		strtab := []byte("\x00libA.so\x00GLIBC_2.17\x00")
		elfData := buildTestELFWithVerneed(vn, strtab)
		rep, err := InspectELFABI(elfData)
		require.NoError(t, err)
		require.Len(t, rep.VersionRequirements, 1)
		assert.Equal(t, "libA.so", rep.VersionRequirements[0].Library)
		assert.Equal(t, "GLIBC_2.17", rep.VersionRequirements[0].Version)
	})

	t.Run("padded_noncontiguous_auxiliary_offset_32_succeeds", func(t *testing.T) {
		t.Parallel()
		vn := make([]byte, 48) // 16 verneed + 16 padding + 16 vernaux
		binary.LittleEndian.PutUint16(vn[0:2], 1)
		binary.LittleEndian.PutUint16(vn[2:4], 1)
		binary.LittleEndian.PutUint32(vn[4:8], 1)
		binary.LittleEndian.PutUint32(vn[8:12], 32) // vn_aux = 32
		binary.LittleEndian.PutUint32(vn[12:16], 0)
		// Vernaux at 32:
		binary.LittleEndian.PutUint32(vn[32:36], 0)
		binary.LittleEndian.PutUint16(vn[36:38], 0)
		binary.LittleEndian.PutUint16(vn[38:40], 0)
		binary.LittleEndian.PutUint32(vn[40:44], 9)
		binary.LittleEndian.PutUint32(vn[44:48], 0)
		strtab := []byte("\x00libA.so\x00GLIBC_2.17\x00")
		elfData := buildTestELFWithVerneed(vn, strtab)
		rep, err := InspectELFABI(elfData)
		require.NoError(t, err)
		require.Len(t, rep.VersionRequirements, 1)
		assert.Equal(t, "GLIBC_2.17", rep.VersionRequirements[0].Version)
	})

	t.Run("out_of_range_auxiliary_offset_fails", func(t *testing.T) {
		t.Parallel()
		vn := make([]byte, 16)
		binary.LittleEndian.PutUint16(vn[0:2], 1)
		binary.LittleEndian.PutUint16(vn[2:4], 1)
		binary.LittleEndian.PutUint32(vn[4:8], 1)
		binary.LittleEndian.PutUint32(vn[8:12], 999999)
		binary.LittleEndian.PutUint32(vn[12:16], 0)
		strtab := []byte("\x00libA.so\x00")
		elfData := buildTestELFWithVerneed(vn, strtab)
		_, err := InspectELFABI(elfData)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
	})

	t.Run("later_auxiliary_hop_into_parent_fails", func(t *testing.T) {
		t.Parallel()
		vn := make([]byte, 32)
		binary.LittleEndian.PutUint16(vn[0:2], 1)
		binary.LittleEndian.PutUint16(vn[2:4], 2) // vn_cnt = 2
		binary.LittleEndian.PutUint32(vn[4:8], 1)
		binary.LittleEndian.PutUint32(vn[8:12], 16)
		binary.LittleEndian.PutUint32(vn[12:16], 0)
		// Vernaux at 16 has vna_next jumping back into parent: -16 (0xfffffff0)
		binary.LittleEndian.PutUint32(vn[24:28], 9)
		binary.LittleEndian.PutUint32(vn[28:32], 0xfffffff0)
		strtab := []byte("\x00libA.so\x00GLIBC_2.17\x00")
		elfData := buildTestELFWithVerneed(vn, strtab)
		_, err := InspectELFABI(elfData)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
		assert.Contains(t, err.Error(), "overlaps containing Elf64_Verneed parent record")
	})

	t.Run("empty_library_name_fails", func(t *testing.T) {
		t.Parallel()
		vn := make([]byte, 32)
		binary.LittleEndian.PutUint16(vn[0:2], 1)
		binary.LittleEndian.PutUint16(vn[2:4], 1)
		binary.LittleEndian.PutUint32(vn[4:8], 0) // points to \x00 -> empty name!
		binary.LittleEndian.PutUint32(vn[8:12], 16)
		binary.LittleEndian.PutUint32(vn[12:16], 0)
		binary.LittleEndian.PutUint32(vn[24:28], 1)
		binary.LittleEndian.PutUint32(vn[28:32], 0)
		strtab := []byte("\x00GLIBC_2.17\x00")
		elfData := buildTestELFWithVerneed(vn, strtab)
		_, err := InspectELFABI(elfData)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
		assert.Contains(t, err.Error(), "vn_file string at offset 0 is empty")
	})

	t.Run("empty_version_name_fails", func(t *testing.T) {
		t.Parallel()
		vn := make([]byte, 32)
		binary.LittleEndian.PutUint16(vn[0:2], 1)
		binary.LittleEndian.PutUint16(vn[2:4], 1)
		binary.LittleEndian.PutUint32(vn[4:8], 1) // valid libA.so
		binary.LittleEndian.PutUint32(vn[8:12], 16)
		binary.LittleEndian.PutUint32(vn[12:16], 0)
		binary.LittleEndian.PutUint32(vn[24:28], 0) // points to \x00 -> empty version!
		binary.LittleEndian.PutUint32(vn[28:32], 0)
		strtab := []byte("\x00libA.so\x00")
		elfData := buildTestELFWithVerneed(vn, strtab)
		_, err := InspectELFABI(elfData)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMetadata))
		assert.Contains(t, err.Error(), "vna_name string at offset 0 is empty")
	})

	t.Run("legitimate_zero_aux_count_succeeds", func(t *testing.T) {
		t.Parallel()
		vn := make([]byte, 16)
		binary.LittleEndian.PutUint16(vn[0:2], 1)
		binary.LittleEndian.PutUint16(vn[2:4], 0) // vn_cnt = 0
		binary.LittleEndian.PutUint32(vn[4:8], 1)
		binary.LittleEndian.PutUint32(vn[8:12], 0) // vn_aux = 0 allowed when vn_cnt == 0
		binary.LittleEndian.PutUint32(vn[12:16], 0)
		strtab := []byte("\x00libA.so\x00")
		elfData := buildTestELFWithVerneed(vn, strtab)
		rep, err := InspectELFABI(elfData)
		require.NoError(t, err)
		assert.Empty(t, rep.VersionRequirements)
	})

	t.Run("mixed_abi_override_settings_both_reject_malformed_input", func(t *testing.T) {
		t.Parallel()
		vn := make([]byte, 16)
		binary.LittleEndian.PutUint16(vn[0:2], 1)
		binary.LittleEndian.PutUint16(vn[2:4], 1)
		binary.LittleEndian.PutUint32(vn[4:8], 1)
		binary.LittleEndian.PutUint32(vn[8:12], 0) // malformed overlap
		binary.LittleEndian.PutUint32(vn[12:16], 0)
		strtab := []byte("\x00libA.so\x00")
		elfData := buildTestELFWithVerneed(vn, strtab)

		tmpDir := t.TempDir()
		badPath := filepath.Join(tmpDir, "bad.bin")
		require.NoError(t, os.WriteFile(badPath, elfData, 0o755))

		// Pack with AllowMixedABI = false
		optsFalse := &Options{
			Variants:      map[string]string{"v1": badPath},
			AllowMixedABI: false,
		}
		errFalse := validatePayloadABIs(optsFalse, []string{"v1"})
		require.Error(t, errFalse)
		assert.True(t, errors.Is(errFalse, ErrABIMetadata))

		// Pack with AllowMixedABI = true
		optsTrue := &Options{
			Variants:      map[string]string{"v1": badPath},
			AllowMixedABI: true,
		}
		errTrue := validatePayloadABIs(optsTrue, []string{"v1"})
		require.Error(t, errTrue)
		assert.True(t, errors.Is(errTrue, ErrABIMetadata))
		assert.Equal(t, errFalse.Error(), errTrue.Error(), "AllowMixedABI must not bypass malformed metadata validation")
	})

	t.Run("pack_canary_preservation_on_malformed_fixture", func(t *testing.T) {
		t.Parallel()
		vn := make([]byte, 16)
		binary.LittleEndian.PutUint16(vn[0:2], 1)
		binary.LittleEndian.PutUint16(vn[2:4], 1)
		binary.LittleEndian.PutUint32(vn[4:8], 1)
		binary.LittleEndian.PutUint32(vn[8:12], 0) // malformed overlap
		binary.LittleEndian.PutUint32(vn[12:16], 0)
		strtab := []byte("\x00libA.so\x00")
		badElf := buildTestELFWithVerneed(vn, strtab)

		tmpDir := t.TempDir()
		badVariant := filepath.Join(tmpDir, "variant.bin")
		require.NoError(t, os.WriteFile(badVariant, badElf, 0o755))

		canaryPath := filepath.Join(tmpDir, "output.fat")
		canaryContent := []byte("CANARY_DESTINATION_CONTENT_DO_NOT_OVERWRITE")
		require.NoError(t, os.WriteFile(canaryPath, canaryContent, 0o644))
		statBefore, err := os.Stat(canaryPath)
		require.NoError(t, err)

		opts := Options{
			OutputPath: canaryPath,
			Variants:   map[string]string{"v1": badVariant},
			TargetOS:   testOSLinux,
			TargetArch: testArchAMD64,
			StubPath:   filepath.Join(tmpDir, "stub"),
		}
		require.NoError(t, os.WriteFile(opts.StubPath, []byte("stub_bytes"), 0o755))

		_, pErr := Pack(opts)
		require.Error(t, pErr)
		assert.True(t, errors.Is(pErr, ErrABIMetadata))

		statAfter, err := os.Stat(canaryPath)
		require.NoError(t, err)
		assert.True(t, os.SameFile(statBefore, statAfter), "canary inode must be preserved on failure")
		assert.Equal(t, statBefore.Mode(), statAfter.Mode(), "canary file mode must be preserved")
		afterBytes, err := os.ReadFile(canaryPath)
		require.NoError(t, err)
		assert.Equal(t, canaryContent, afterBytes, "canary file content must be unchanged")

		absentDest := filepath.Join(tmpDir, "absent.fat")
		opts.OutputPath = absentDest
		_, pErr2 := Pack(opts)
		require.Error(t, pErr2)
		assert.True(t, errors.Is(pErr2, ErrABIMetadata))
		assert.NoFileExists(t, absentDest, "absent destination file must not be created on validation failure")
	})
}

func TestABI_R3_RecordWorkAndLimits(t *testing.T) {
	t.Parallel()

	t.Run("allowance_16_fails_before_aux_allowance_32_admits_both", func(t *testing.T) {
		t.Parallel()
		// 1 parent (16 bytes) + 1 auxiliary (16 bytes)
		vnData := make([]byte, 32)
		// Parent record
		binary.LittleEndian.PutUint16(vnData[0:2], 1)   // vn_version = 1
		binary.LittleEndian.PutUint16(vnData[2:4], 1)   // vn_cnt = 1
		binary.LittleEndian.PutUint32(vnData[4:8], 1)   // vn_file offset in strtab
		binary.LittleEndian.PutUint32(vnData[8:12], 16) // vn_aux offset = 16
		binary.LittleEndian.PutUint32(vnData[12:16], 0) // vn_next = 0

		// Auxiliary record at offset 16
		binary.LittleEndian.PutUint16(vnData[20:22], 0)  // vna_flags
		binary.LittleEndian.PutUint32(vnData[24:28], 11) // vna_name offset in strtab
		binary.LittleEndian.PutUint32(vnData[28:32], 0)  // vna_next = 0

		strtab := []byte("\x00libc.so.6\x00GLIBC_2.2.5\x00")
		loads := []loadSegment{{off: 0, vaddr: 0x1000, filesz: 1000, memsz: 1000}}

		// Allowance 16: parent record succeeds (16 bytes), string scan fails when local metadata budget is exhausted
		acc16 := &inputAccounting{maxMetadataBytes: 16}
		err16 := parseVerneed(vnData, loads, 1000, binary.LittleEndian, 0x1000, 1, strtab, acc16, &VariantABIReport{})
		require.Error(t, err16)
		assert.True(t, errors.Is(err16, ErrABIResourceLimit))
		assert.Equal(t, uint64(16), acc16.metadataBytesRead, "must have charged exactly 16 bytes for parent before string scan exhaustion")

		// Allowance 26: parent record (16) + vn_file string (10) succeed, auxiliary record (16) fails
		acc26 := &inputAccounting{maxMetadataBytes: 26}
		err26 := parseVerneed(vnData, loads, 1000, binary.LittleEndian, 0x1000, 1, strtab, acc26, &VariantABIReport{})
		require.Error(t, err26)
		assert.True(t, errors.Is(err26, ErrABIResourceLimit))
		assert.Equal(t, uint64(26), acc26.metadataBytesRead)

		// Allowance 54: both parent record (16), vn_file (10), auxiliary record (16), and vna_name (12) succeed
		acc54 := &inputAccounting{maxMetadataBytes: 54}
		err54 := parseVerneed(vnData, loads, 1000, binary.LittleEndian, 0x1000, 1, strtab, acc54, &VariantABIReport{})
		require.NoError(t, err54)
		assert.Equal(t, uint64(54), acc54.metadataBytesRead, "must have charged 54 bytes for parent, auxiliary, and strings")
	})

	t.Run("multiple_parents_and_auxiliaries_shared_and_local_charged", func(t *testing.T) {
		t.Parallel()
		// 2 parents, each with 2 auxiliaries = 6 records * 16 = 96 bytes
		vnData := make([]byte, 96)
		// Parent 1 at offset 0 (aux at offset 16, next at offset 48)
		binary.LittleEndian.PutUint16(vnData[0:2], 1)
		binary.LittleEndian.PutUint16(vnData[2:4], 2)
		binary.LittleEndian.PutUint32(vnData[4:8], 1)
		binary.LittleEndian.PutUint32(vnData[8:12], 16)
		binary.LittleEndian.PutUint32(vnData[12:16], 48)

		// Aux 1.1 at offset 16 (next at offset 16 -> 32)
		binary.LittleEndian.PutUint32(vnData[24:28], 11)
		binary.LittleEndian.PutUint32(vnData[28:32], 16)
		// Aux 1.2 at offset 32 (next = 0)
		binary.LittleEndian.PutUint32(vnData[40:44], 11)
		binary.LittleEndian.PutUint32(vnData[44:48], 0)

		// Parent 2 at offset 48 (aux at offset 16 -> 64, next = 0)
		binary.LittleEndian.PutUint16(vnData[48:50], 1)
		binary.LittleEndian.PutUint16(vnData[50:52], 2)
		binary.LittleEndian.PutUint32(vnData[52:56], 1)
		binary.LittleEndian.PutUint32(vnData[56:60], 16)
		binary.LittleEndian.PutUint32(vnData[60:64], 0)

		// Aux 2.1 at offset 64 (next at offset 16 -> 80)
		binary.LittleEndian.PutUint32(vnData[72:76], 11)
		binary.LittleEndian.PutUint32(vnData[76:80], 16)
		// Aux 2.2 at offset 80 (next = 0)
		binary.LittleEndian.PutUint32(vnData[88:92], 11)
		binary.LittleEndian.PutUint32(vnData[92:96], 0)

		strtab := []byte("\x00libc.so.6\x00GLIBC_2.2.5\x00")
		loads := []loadSegment{{off: 0, vaddr: 0x1000, filesz: 1000, memsz: 1000}}

		shared := NewArtifactMetadataAccounting(1000)
		acc := &inputAccounting{shared: shared}
		err := parseVerneed(vnData, loads, 1000, binary.LittleEndian, 0x1000, 2, strtab, acc, &VariantABIReport{})
		require.NoError(t, err)
		const expectedRecordBytes = 96
		const expectedStringScanBytes = 68 // 2 parent vn_file (20) + 4 aux vna_name (48)
		const expectedTotalMetadataBytes = expectedRecordBytes + expectedStringScanBytes
		const expectedVersionRecords = 6
		assert.Equal(t, uint64(expectedTotalMetadataBytes), acc.metadataBytesRead)
		assert.Equal(t, uint64(expectedTotalMetadataBytes), shared.usedBytes)
		assert.Equal(t, uint64(expectedStringScanBytes), acc.stringScanBytesRead)
		assert.Equal(t, uint64(expectedVersionRecords), acc.versionRecords)
	})

	t.Run("missing_nul_budget_exhaustion_vs_end_of_table", func(t *testing.T) {
		t.Parallel()
		strtab := []byte("unterminated_string_without_nul")

		// Case 1: Budget sufficient to reach end of table without NUL -> ErrABIMetadata
		accFull := &inputAccounting{maxStringScanBytes: 1000}
		_, errFull := scanBoundedCString(strtab, 0, accFull, "test_field", true)
		require.Error(t, errFull)
		assert.True(t, errors.Is(errFull, ErrABIMetadata))
		assert.Contains(t, errFull.Error(), "not NUL-terminated")

		// Case 2: Budget exhausted before reaching end of table -> ErrABIResourceLimit
		accShort := &inputAccounting{maxStringScanBytes: 5}
		_, errShort := scanBoundedCString(strtab, 0, accShort, "test_field", true)
		require.Error(t, errShort)
		assert.True(t, errors.Is(errShort, ErrABIResourceLimit))
		assert.Contains(t, errShort.Error(), "exceeds per-input budget")
	})

	t.Run("repeated_string_offsets_work_charged_and_string_budgeted", func(t *testing.T) {
		t.Parallel()
		strtab := []byte("libc.so.6\x00")
		acc := &inputAccounting{maxMetadataBytes: 1000, maxStringBytes: 1000}
		deps, err := parseNeededDependencies(strtab, uint64(len(strtab)), []uint64{0, 0, 0, 0, 0}, acc)
		require.NoError(t, err)
		assert.Len(t, deps, 5)
		for _, d := range deps {
			assert.Equal(t, "libc.so.6", d)
		}
		expectedRetained := uint64(5 * len("libc.so.6"))
		assert.Equal(t, expectedRetained, acc.retainedStringBytes)
	})

	t.Run("report_reservation_checked_before_construction", func(t *testing.T) {
		t.Parallel()
		const smallBudget = 10
		ra := NewReportAccounting(smallBudget)
		builder := newBoundedReportBuilder(ra)
		err := builder.writeString("a very long string that exceeds small budget")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
		assert.Empty(t, builder.string())
	})

	t.Run("format_mismatch_error_bounded_on_budget_exhaustion", func(t *testing.T) {
		t.Parallel()
		diffs := []string{"diff 1", "diff 2", "diff 3"}
		const smallBudget = 10
		ra := NewReportAccounting(smallBudget)
		err := formatMismatchError(diffs, ra)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMismatch))
		assert.Contains(t, err.Error(), "3 declared differences (detailed output omitted")
	})

	t.Run("presentation_size_preflight_fails_before_pack_canary_preserved", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		canaryPath := filepath.Join(tmpDir, "canary.fat")
		canaryContent := []byte("CANARY_PREFLIGHT_PROTECTION")
		require.NoError(t, os.WriteFile(canaryPath, canaryContent, 0o644))

		// Build two valid ELFs
		elf1 := buildTestELFWithDynTags([][2]uint64{
			{uint64(elf.DT_STRTAB), 0xdeadbeef},
			{uint64(elf.DT_STRSZ), uint64(len("\x00libA.so\x00"))},
			{uint64(elf.DT_NEEDED), 1},
		}, []byte("\x00libA.so\x00"))

		// Pack with extremely small report limit via custom test
		rep1, err := InspectELFABI(elf1)
		require.NoError(t, err)
		rep1.Level = "v1"

		tinyLimits := defaultABILimits
		tinyLimits.MaxArtifactReportBytes = 50 // too small for presentation
		_, cErr := CompareVariantABIsWithOptions([]*VariantABIReport{rep1}, false, tinyLimits)
		require.Error(t, cErr)
		assert.True(t, errors.Is(cErr, ErrABIResourceLimit))

		// Canary remains untouched
		content, err := os.ReadFile(canaryPath)
		require.NoError(t, err)
		assert.Equal(t, canaryContent, content)
	})

	t.Run("large_elf_small_metadata_unrelated_payload_size_does_not_inflate_metadata_work", func(t *testing.T) {
		t.Parallel()
		baseELF := buildTestELFWithDynTags([][2]uint64{
			{uint64(elf.DT_STRTAB), 0xdeadbeef},
			{uint64(elf.DT_STRSZ), uint64(len("\x00" + testLibA + "\x00"))},
			{uint64(elf.DT_NEEDED), 1},
		}, []byte("\x00"+testLibA+"\x00"))

		// Append 2 MiB of padding to simulate large code/assets
		const paddingSize = 2 * 1024 * 1024
		largeELF := make([]byte, len(baseELF)+paddingSize)
		copy(largeELF, baseELF)
		// Update PT_LOAD segment filesz in ELF header to cover total size
		binary.LittleEndian.PutUint64(largeELF[64+32:64+40], uint64(len(largeELF)))
		binary.LittleEndian.PutUint64(largeELF[64+40:64+48], uint64(len(largeELF)))

		acc := &inputAccounting{}
		rep, err := InspectELFABIWithAccounting(largeELF, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{testLibA}, rep.Dependencies)

		// Metadata charged must be small (header + program headers + dynamic tags + strtab), NOT 2 MiB!
		const maxExpectedMetadata = 16 * 1024
		assert.Less(t, acc.metadataBytesRead, uint64(maxExpectedMetadata))
	})
}

func TestABI_R3_AmbiguousDependencyComparison(t *testing.T) {
	t.Parallel()

	t.Run("two_ambiguous_differing_dependencies", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{Level: "v1", Linkage: LinkageAmbiguous, Dependencies: []string{testLibA}}
		r2 := &VariantABIReport{Level: "v2", Linkage: LinkageAmbiguous, Dependencies: []string{testLibB}}

		// Default: must reject with ErrABIMismatch
		repDef, errDef := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.Error(t, errDef)
		assert.True(t, errors.Is(errDef, ErrABIMismatch))
		assert.False(t, repDef.Consistent)
		assert.Equal(t, ComparisonInconsistent, repDef.Status)

		// Override: admitted with recorded difference and inconsistent status
		repOvr, errOvr := CompareVariantABIs([]*VariantABIReport{r1, r2}, true)
		require.NoError(t, errOvr)
		assert.False(t, repOvr.Consistent)
		assert.Equal(t, ComparisonInconsistent, repOvr.Status)
		assert.True(t, repOvr.Overridden)
		require.NotEmpty(t, repOvr.Warnings)
		assert.Contains(t, repOvr.Warnings[len(repOvr.Warnings)-1], "[OVERRIDDEN] differing declared dependencies")
	})

	t.Run("dynamic_vs_ambiguous_differing_dependencies", func(t *testing.T) {
		t.Parallel()
		rDyn := &VariantABIReport{
			Level: "v1", Linkage: LinkageDynamic, HasInterpreter: true,
			Interpreter: "/lib64/ld-linux-x86-64.so.2", Dependencies: []string{testLibA, testLibC},
		}
		rAmb := &VariantABIReport{
			Level: "v2", Linkage: LinkageAmbiguous, Dependencies: []string{testLibB, testLibC},
		}

		repDef, errDef := CompareVariantABIs([]*VariantABIReport{rDyn, rAmb}, false)
		require.Error(t, errDef)
		assert.True(t, errors.Is(errDef, ErrABIMismatch))
		assert.False(t, repDef.Consistent)

		repOvr, errOvr := CompareVariantABIs([]*VariantABIReport{rDyn, rAmb}, true)
		require.NoError(t, errOvr)
		assert.False(t, repOvr.Consistent)
		assert.True(t, repOvr.Overridden)
	})

	t.Run("same_known_sets_with_ambiguous_linkage_preserves_uncertainty", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{Level: "v1", Linkage: LinkageAmbiguous, Dependencies: []string{testLibC}}
		r2 := &VariantABIReport{Level: "v2", Linkage: LinkageAmbiguous, Dependencies: []string{testLibC}}

		repDef, errDef := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, errDef)
		assert.False(t, repDef.Consistent, "must not upgrade uncertain linkage into proven consistency")
		assert.Equal(t, ComparisonUnknown, repDef.Status)

		repOvr, errOvr := CompareVariantABIs([]*VariantABIReport{r1, r2}, true)
		require.NoError(t, errOvr)
		assert.False(t, repOvr.Consistent)
		assert.Equal(t, ComparisonUnknown, repOvr.Status)
	})

	t.Run("unsupported_version_observation_differing_dependencies", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{
			Level: "v1", Linkage: LinkageDynamic, Completeness: MetadataUnsupported, Dependencies: []string{testLibA},
		}
		r2 := &VariantABIReport{
			Level: "v2", Linkage: LinkageDynamic, Completeness: MetadataComplete, Dependencies: []string{testLibB},
		}

		repDef, errDef := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.Error(t, errDef)
		assert.True(t, errors.Is(errDef, ErrABIMismatch))
		assert.False(t, repDef.Consistent)
	})

	t.Run("one_skipped_two_differing_known_dependencies", func(t *testing.T) {
		t.Parallel()
		rSkip := &VariantABIReport{Level: "v1", Completeness: MetadataSkipped}
		rA := &VariantABIReport{Level: "v2", Linkage: LinkageAmbiguous, Dependencies: []string{testLibA}}
		rB := &VariantABIReport{Level: "v3", Linkage: LinkageAmbiguous, Dependencies: []string{testLibB}}

		repDef, errDef := CompareVariantABIs([]*VariantABIReport{rSkip, rA, rB}, false)
		require.Error(t, errDef)
		assert.True(t, errors.Is(errDef, ErrABIMismatch))
		assert.False(t, repDef.Consistent)
	})

	t.Run("known_empty_static_vs_known_nonempty_dynamic", func(t *testing.T) {
		t.Parallel()
		rStatic := &VariantABIReport{Level: "v1", Linkage: LinkageStatic, Dependencies: nil}
		rDyn := &VariantABIReport{Level: "v2", Linkage: LinkageDynamic, Dependencies: []string{testLibC}}

		repDef, errDef := CompareVariantABIs([]*VariantABIReport{rStatic, rDyn}, false)
		require.Error(t, errDef)
		assert.True(t, errors.Is(errDef, ErrABIMismatch))
		assert.False(t, repDef.Consistent)
	})

	t.Run("same_set_different_search_order", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{Level: "v1", Linkage: LinkageDynamic, Dependencies: []string{testLibA, testLibB}}
		r2 := &VariantABIReport{Level: "v2", Linkage: LinkageDynamic, Dependencies: []string{testLibB, testLibA}}

		rep, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, err)
		assert.True(t, rep.Consistent)
		assert.Equal(t, ComparisonConsistent, rep.Status)
		require.NotEmpty(t, rep.Warnings)
		assert.Contains(t, rep.Warnings[0], "declared dependency search order differs")
	})

	t.Run("all_skipped", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{Level: "v1", Completeness: MetadataSkipped}
		r2 := &VariantABIReport{Level: "v2", Completeness: MetadataSkipped}

		rep, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, err)
		assert.False(t, rep.Consistent)
		assert.Equal(t, ComparisonSkipped, rep.Status)
	})

	t.Run("permutation_invariance", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{Level: "v1", Linkage: LinkageAmbiguous, Dependencies: []string{testLibA}}
		r2 := &VariantABIReport{Level: "v2", Linkage: LinkageAmbiguous, Dependencies: []string{testLibB}}

		// Pair orders
		_, err12 := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.Error(t, err12)
		assert.True(t, errors.Is(err12, ErrABIMismatch))

		_, err21 := CompareVariantABIs([]*VariantABIReport{r2, r1}, false)
		require.Error(t, err21)
		assert.True(t, errors.Is(err21, ErrABIMismatch))

		// 3-variant permutations
		r3 := &VariantABIReport{Level: "v3", Linkage: LinkageAmbiguous, Dependencies: []string{"libC.so"}}
		perms := [][]*VariantABIReport{
			{r1, r2, r3},
			{r1, r3, r2},
			{r2, r1, r3},
			{r2, r3, r1},
			{r3, r1, r2},
			{r3, r2, r1},
		}
		for i, p := range perms {
			rep, pErr := CompareVariantABIs(p, false)
			require.Error(t, pErr, "perm %d must fail", i)
			assert.True(t, errors.Is(pErr, ErrABIMismatch), "perm %d must wrap ErrABIMismatch", i)
			assert.False(t, rep.Consistent, "perm %d must be inconsistent", i)
			assert.Equal(t, ComparisonInconsistent, rep.Status, "perm %d status must be ComparisonInconsistent", i)
		}
	})

	t.Run("parser_to_comparator_linkage_ambiguous_from_fixture_bytes", func(t *testing.T) {
		t.Parallel()
		elfA := buildTestELFWithDynTags([][2]uint64{
			{uint64(elf.DT_STRTAB), 0xdeadbeef},
			{uint64(elf.DT_STRSZ), uint64(len("\x00" + testLibA + "\x00"))},
			{uint64(elf.DT_NEEDED), 1},
		}, []byte("\x00"+testLibA+"\x00"))

		elfB := buildTestELFWithDynTags([][2]uint64{
			{uint64(elf.DT_STRTAB), 0xdeadbeef},
			{uint64(elf.DT_STRSZ), uint64(len("\x00" + testLibB + "\x00"))},
			{uint64(elf.DT_NEEDED), 1},
		}, []byte("\x00"+testLibB+"\x00"))

		repA, errA := InspectELFABI(elfA)
		require.NoError(t, errA)
		assert.Equal(t, LinkageAmbiguous, repA.Linkage)
		assert.Equal(t, []string{testLibA}, repA.Dependencies)

		repB, errB := InspectELFABI(elfB)
		require.NoError(t, errB)
		assert.Equal(t, LinkageAmbiguous, repB.Linkage)
		assert.Equal(t, []string{testLibB}, repB.Dependencies)

		repA.Level = "v1"
		repB.Level = "v2"

		// Compare them
		rep, cErr := CompareVariantABIs([]*VariantABIReport{repA, repB}, false)
		require.Error(t, cErr)
		assert.True(t, errors.Is(cErr, ErrABIMismatch))
		assert.False(t, rep.Consistent)
		assert.Equal(t, ComparisonInconsistent, rep.Status)
	})
}

func TestABI_R3_CoverageAndBudgetBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("bounded_dep_list_truncation", func(t *testing.T) {
		t.Parallel()
		ra := NewReportAccounting(1024)
		b := newBoundedReportBuilder(ra)
		deps := []string{"lib1.so", "lib2.so", "lib3.so", "lib4.so", "lib5.so", "lib6.so", "lib7.so", "lib8.so", "lib9.so", "lib10.so"}
		err := b.writeBoundedDepList(deps, 4)
		require.NoError(t, err)
		s := b.string()
		assert.Contains(t, s, "... (+6 more)")
	})

	t.Run("input_accounting_string_scan_bounds", func(t *testing.T) {
		t.Parallel()
		acc := &inputAccounting{maxStringScanBytes: 10}
		acc.stringScanBytesRead = 10
		assert.Equal(t, uint64(0), acc.remainingStringScanBudget())
		assert.Error(t, acc.chargeStringScan(1))

		acc.refundStringScan(20) // n > acc.stringScanBytesRead
		assert.Equal(t, uint64(0), acc.stringScanBytesRead)
	})

	t.Run("scan_bounded_cstring_empty_scan_budget", func(t *testing.T) {
		t.Parallel()
		acc := &inputAccounting{maxStringScanBytes: 1}
		acc.stringScanBytesRead = 1
		_, err := scanBoundedCString([]byte("hello\x00"), 0, acc, "test", false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
	})

	t.Run("report_builder_gradual_budget_exhaustion", func(t *testing.T) {
		t.Parallel()
		rBase := &VariantABIReport{
			Level: "v1", Linkage: LinkageDynamic, HasInterpreter: true, Interpreter: "/lib64/ld-linux.so.2",
			Dependencies:        []string{"libA.so", "libB.so"},
			VersionRequirements: []VersionRequirement{{Library: "libA.so", Version: "V1"}},
		}
		rCurr := &VariantABIReport{
			Level: "v2", Linkage: LinkageDynamic, HasInterpreter: true, Interpreter: "/lib64/ld-linux2.so.2",
			Dependencies:        []string{"libA.so", "libC.so"},
			VersionRequirements: []VersionRequirement{{Library: "libA.so", Version: "V2"}},
		}
		rIncomp := &VariantABIReport{
			Level: "v3", Completeness: MetadataPartial,
		}

		for budget := uint64(0); budget <= 200; budget += 3 {
			ra := NewReportAccounting(budget)
			_, _ = formatDependencySetDiff(ra, rBase, rCurr, []string{"libC.so"}, []string{"libB.so"})

			ra2 := NewReportAccounting(budget)
			_, _ = formatDependencyOrderWarning(ra2, rBase, rCurr)

			ra3 := NewReportAccounting(budget)
			_, _ = formatInterpreterConfigDiff(ra3, rBase, &VariantABIReport{Level: "v4"})

			ra4 := NewReportAccounting(budget)
			_, _ = formatInterpreterPathDiff(ra4, rBase, rCurr)

			ra5 := NewReportAccounting(budget)
			_, _ = formatIncompleteMetadataWarning(ra5, rIncomp)

			ra7 := NewReportAccounting(budget)
			_, _ = compareVariantVersions([]*VariantABIReport{rBase, rCurr}, ra7)

			ra8 := NewReportAccounting(budget)
			_, _ = compareVariantInterpreters([]*VariantABIReport{rBase, rCurr}, ra8)

			ra9 := NewReportAccounting(budget)
			_, _ = compareVariantLinkages([]*VariantABIReport{{Level: "v1", Linkage: LinkageStatic}, {Level: "v2", Linkage: LinkageDynamic}}, ra9)

			ra10 := NewReportAccounting(budget)
			_ = applyOverriddenDifferences(&ArtifactABIReport{}, []string{"diff1", "diff2"}, ra10)
		}
	})

	t.Run("verneed_relative_offset_zero", func(t *testing.T) {
		t.Parallel()
		_, err := advanceELFRelativeOffset(100, 0, 50, 200, 16)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "zero progress")
	})

	t.Run("verneed_advance_offset_contradictory_termination", func(t *testing.T) {
		t.Parallel()
		_, err := advanceVerneedOffset(0, 1, 100, 16, 50, 200)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "contradictory termination of Elf64_Verneed chain")
	})

	t.Run("measure_presentation_bytes", func(t *testing.T) {
		t.Parallel()
		zeroBytes, err := MeasureABIReportPresentation(nil)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), zeroBytes)

		manyDeps := make([]string, 12)
		for i := range manyDeps {
			manyDeps[i] = fmt.Sprintf("lib%d.so", i)
		}
		manyVers := make([]VersionRequirement, 12)
		for i := range manyVers {
			manyVers[i] = VersionRequirement{Library: "libc.so", Version: fmt.Sprintf("V%d", i)}
		}
		rep := &ArtifactABIReport{
			Variants: []*VariantABIReport{
				nil,
				{
					Level:               "v1",
					Dependencies:        manyDeps,
					VersionRequirements: manyVers,
				},
			},
			Warnings:    []string{"warn1"},
			Differences: []string{"diff1"},
		}
		est, err := MeasureABIReportPresentation(rep)
		require.NoError(t, err)
		assert.Greater(t, est, uint64(0))

		var buf bytes.Buffer
		require.NoError(t, RenderABIReport(&buf, rep))
		assert.Equal(t, est, uint64(buf.Len()))
	})

	t.Run("compare_variant_abis_presentation_budget_exhaustion", func(t *testing.T) {
		t.Parallel()
		limitsSmall := defaultABILimits
		limitsSmall.MaxArtifactReportBytes = 100
		_, errEmpty := CompareVariantABIsWithOptions(nil, false, limitsSmall)
		require.Error(t, errEmpty)

		_, errSingle := CompareVariantABIsWithOptions([]*VariantABIReport{{Level: "v1", Completeness: MetadataComplete}}, false, limitsSmall)
		require.Error(t, errSingle)
	})

	t.Run("handle_single_report_warning_reservation_failure", func(t *testing.T) {
		t.Parallel()
		ra := NewReportAccounting(5)
		rep := &VariantABIReport{Level: "v1", Warnings: []string{"this is a long warning"}}
		_, err := handleSingleReport(rep, ra, &ArtifactABIReport{})
		require.Error(t, err)
	})

	t.Run("evaluate_all_reports_warning_reservation_failure", func(t *testing.T) {
		t.Parallel()
		ra := NewReportAccounting(5)
		reports := []*VariantABIReport{
			{Level: "v1", Warnings: []string{"warning too long for budget"}},
			{Level: "v2"},
		}
		_, _, _, err := evaluateAllReportsCompleteness(reports, ra)
		require.Error(t, err)
	})

	t.Run("collect_variant_differences_budget_exhaustion", func(t *testing.T) {
		t.Parallel()
		r1 := &VariantABIReport{Level: "v1", Linkage: LinkageDynamic, HasInterpreter: true, Interpreter: "/lib64/ld1.so"}
		r2 := &VariantABIReport{Level: "v2", Linkage: LinkageDynamic, HasInterpreter: true, Interpreter: "/lib64/ld2.so"}
		ra := NewReportAccounting(2)
		_, _, err := collectVariantDifferences([]*VariantABIReport{r1, r2}, ra)
		require.Error(t, err)
	})

	t.Run("compare_variant_abis_overridden_differences_budget_exhaustion", func(t *testing.T) {
		t.Parallel()
		limits := defaultABILimits
		limits.MaxArtifactReportBytes = 320
		r1 := &VariantABIReport{Level: "v1", Linkage: LinkageStatic}
		r2 := &VariantABIReport{Level: "v2", Linkage: LinkageDynamic}
		_, err := CompareVariantABIsWithOptions([]*VariantABIReport{r1, r2}, true, limits)
		require.Error(t, err)
	})
}
