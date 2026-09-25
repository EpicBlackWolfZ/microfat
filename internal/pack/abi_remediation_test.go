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
			VersionRequirements: []VersionRequirement{{Library: "libc.so.6", Version: "GLIBC_2.17"}},
		}
		v2 := &VariantABIReport{
			Level:               "v2",
			Linkage:             LinkageDynamic,
			Completeness:        MetadataComplete,
			VersionRequirements: []VersionRequirement{{Library: "libc.so.6", Version: "GLIBC_2.34"}},
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
