package microarch

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestNonAdjacentVariantCandidateSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		arch       string
		hostLevel  string
		candidates []string
		policy     Policy
		wantLevel  string
		wantOK     bool
	}{
		{
			name:       "Host v4 with only v1 and v3 available",
			arch:       ArchAMD64,
			hostLevel:  AMD64v4,
			candidates: []string{AMD64v1, AMD64v3},
			policy:     Policy{},
			wantLevel:  AMD64v3,
			wantOK:     true,
		},
		{
			name:       "Host v3 with only v1 and v4 available",
			arch:       ArchAMD64,
			hostLevel:  AMD64v3,
			candidates: []string{AMD64v1, AMD64v4},
			policy:     Policy{},
			wantLevel:  AMD64v1,
			wantOK:     true,
		},
		{
			name:       "Host v2 with only v3 and v4 available (no match)",
			arch:       ArchAMD64,
			hostLevel:  AMD64v2,
			candidates: []string{AMD64v3, AMD64v4},
			policy:     Policy{},
			wantLevel:  "",
			wantOK:     false,
		},
		{
			name:       "Host ARM64 v9.2 with only v8.0 and v8.4 available",
			arch:       ArchARM64,
			hostLevel:  ARM64v9_2,
			candidates: []string{ARM64v8_0, ARM64v8_4},
			policy:     Policy{},
			wantLevel:  ARM64v8_4,
			wantOK:     true,
		},
		{
			name:       "Host ARM64 v8.2 with only v9.0 available (no match)",
			arch:       ArchARM64,
			hostLevel:  ARM64v8_2,
			candidates: []string{ARM64v9_0},
			policy:     Policy{},
			wantLevel:  "",
			wantOK:     false,
		},
		{
			name:       "Forced compatible variant selects specified variant",
			arch:       ArchAMD64,
			hostLevel:  AMD64v3,
			candidates: []string{AMD64v1, AMD64v2, AMD64v3},
			policy:     Policy{ForceLevel: AMD64v2},
			wantLevel:  AMD64v2,
			wantOK:     true,
		},
		{
			name:       "Forced incompatible variant exceeds host capability (rejected)",
			arch:       ArchAMD64,
			hostLevel:  AMD64v2,
			candidates: []string{AMD64v1, AMD64v3},
			policy:     Policy{ForceLevel: AMD64v3},
			wantLevel:  "",
			wantOK:     false,
		},
		{
			name:       "MaxLevel clamps candidate selection",
			arch:       ArchAMD64,
			hostLevel:  AMD64v4,
			candidates: []string{AMD64v1, AMD64v2, AMD64v3, AMD64v4},
			policy:     Policy{MaxLevel: AMD64v2},
			wantLevel:  AMD64v2,
			wantOK:     true,
		},
		{
			name:       "Disabled variant skipped",
			arch:       ArchAMD64,
			hostLevel:  AMD64v3,
			candidates: []string{AMD64v1, AMD64v2, AMD64v3},
			policy:     Policy{DisabledVariants: []string{AMD64v3}},
			wantLevel:  AMD64v2,
			wantOK:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			polRes, err := SelectVariantWithPolicy(tt.arch, tt.hostLevel, tt.candidates, tt.policy)
			gotOK := (err == nil)
			if gotOK != tt.wantOK {
				t.Fatalf("SelectVariantWithPolicy ok = %v (err=%v), want %v", gotOK, err, tt.wantOK)
			}
			if polRes.SelectedVariant != tt.wantLevel {
				t.Fatalf("SelectVariantWithPolicy level = %q, want %q", polRes.SelectedVariant, tt.wantLevel)
			}
		})
	}
}

func TestAVX512DownclockingRiskEdgeCases(t *testing.T) {
	t.Parallel()

	// Skylake-X model (Family 6, Model 85, Stepping 4)
	skylakeXInfo := "processor\t: 0\n" +
		"vendor_id\t: GenuineIntel\n" +
		"cpu family\t: 6\n" +
		"model\t\t: 85\n" +
		"model name\t: Intel(R) Xeon(R) Gold 6140 CPU @ 2.30GHz\n" +
		"stepping\t: 4\n" +
		"flags\t\t: fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush " +
		"sse sse2 ss ht syscall nx lm avx avx2 avx512f avx512dq avx512cd avx512bw avx512vl\n"

	modelInfo := parseLinuxCPUInfoX86(strings.NewReader(skylakeXInfo))
	if modelInfo.Family != 6 || modelInfo.Model != 85 || modelInfo.Vendor != "GenuineIntel" {
		t.Errorf("expected Skylake-X model info, got %+v", modelInfo)
	}

	// AMD EPYC Genoa (Zen 4) - Has AVX-512 without Intel downclocking penalty
	zen4Info := "processor\t: 0\n" +
		"vendor_id\t: AuthenticAMD\n" +
		"cpu family\t: 25\n" +
		"model\t\t: 17\n" +
		"model name\t: AMD EPYC 9654 96-Core Processor\n" +
		"stepping\t: 1\n" +
		"flags\t\t: fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush " +
		"sse sse2 ht syscall nx lm avx avx2 avx512f avx512dq avx512cd sha_ni avx512bw avx512vl\n"

	zen4ModelInfo := parseLinuxCPUInfoX86(strings.NewReader(zen4Info))
	if zen4ModelInfo.Family == 6 && zen4ModelInfo.Model == 85 {
		t.Errorf("expected AMD Zen 4 not to match Skylake-X model 85, got %+v", zen4ModelInfo)
	}

	// Empty / invalid cpuinfo
	emptyModelInfo := parseLinuxCPUInfoX86(strings.NewReader(""))
	if emptyModelInfo.Family != 0 || emptyModelInfo.Model != 0 {
		t.Errorf("expected zero model info for empty input, got %+v", emptyModelInfo)
	}
}

func TestLinuxAuxvARM64ParsingEdgeCases(t *testing.T) {
	t.Parallel()

	// Multi-entry auxv data with various tags, including duplicate AT_HWCAP2 and terminating AT_NULL
	auxvData := make([]byte, 64)
	binary.LittleEndian.PutUint64(auxvData[0:8], 16) // AT_HWCAP
	binary.LittleEndian.PutUint64(auxvData[8:16], 0x1234)
	binary.LittleEndian.PutUint64(auxvData[16:24], auxvAT_HWCAP2) // AT_HWCAP2
	binary.LittleEndian.PutUint64(auxvData[24:32], hwcap2BF16|hwcap2WFXT)
	binary.LittleEndian.PutUint64(auxvData[32:40], 0) // AT_NULL
	binary.LittleEndian.PutUint64(auxvData[40:48], 0)
	binary.LittleEndian.PutUint64(auxvData[48:56], auxvAT_HWCAP2) // After AT_NULL (should be ignored)
	binary.LittleEndian.PutUint64(auxvData[56:64], hwcap2SME|hwcap2SME2)

	bf16, wfxt, sme, sme2 := parseLinuxAuxvARM64(auxvData)
	if !bf16 || !wfxt {
		t.Errorf("expected bf16 and wfxt to be true, got bf16=%v, wfxt=%v", bf16, wfxt)
	}
	if sme || sme2 {
		t.Errorf("expected sme and sme2 to be false due to AT_NULL termination, got sme=%v, sme2=%v", sme, sme2)
	}
}

func TestX86FeaturePermutationsFallback(t *testing.T) {
	t.Parallel()

	baseV3 := X86Features{
		HasCX16:    true,
		HasPOPCNT:  true,
		HasSSE3:    true,
		HasSSSE3:   true,
		HasSSE41:   true,
		HasSSE42:   true,
		HasAVX:     true,
		HasAVX2:    true,
		HasBMI1:    true,
		HasBMI2:    true,
		HasFMA:      true,
		HasOSXSAVE:  true,
		HasF16C:     true,
		HasLZCNT:    true,
		HasMOVBE:    true,
	}

	if got := EvaluateAMD64(baseV3); got != AMD64v3 {
		t.Fatalf("EvaluateAMD64(baseV3) = %q, want %q", got, AMD64v3)
	}

	v3MissingTests := []struct {
		name    string
		mutate  func(f *X86Features)
		wantLvl string
	}{
		{name: "missing F16C", mutate: func(f *X86Features) { f.HasF16C = false }, wantLvl: AMD64v2},
		{name: "missing LZCNT", mutate: func(f *X86Features) { f.HasLZCNT = false }, wantLvl: AMD64v2},
		{name: "missing MOVBE", mutate: func(f *X86Features) { f.HasMOVBE = false }, wantLvl: AMD64v2},
		{name: "missing AVX", mutate: func(f *X86Features) { f.HasAVX = false }, wantLvl: AMD64v2},
		{name: "missing AVX2", mutate: func(f *X86Features) { f.HasAVX2 = false }, wantLvl: AMD64v2},
		{name: "missing BMI1", mutate: func(f *X86Features) { f.HasBMI1 = false }, wantLvl: AMD64v2},
		{name: "missing BMI2", mutate: func(f *X86Features) { f.HasBMI2 = false }, wantLvl: AMD64v2},
		{name: "missing FMA", mutate: func(f *X86Features) { f.HasFMA = false }, wantLvl: AMD64v2},
		{name: "missing OSXSAVE", mutate: func(f *X86Features) { f.HasOSXSAVE = false }, wantLvl: AMD64v2},
		{name: "missing SSE4.2 (drops to v1)", mutate: func(f *X86Features) { f.HasSSE42 = false }, wantLvl: AMD64v1},
		{name: "missing CX16 (drops to v1)", mutate: func(f *X86Features) { f.HasCX16 = false }, wantLvl: AMD64v1},
		{name: "missing POPCNT (drops to v1)", mutate: func(f *X86Features) { f.HasPOPCNT = false }, wantLvl: AMD64v1},
	}

	for _, tc := range v3MissingTests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			feat := baseV3
			tc.mutate(&feat)
			if got := EvaluateAMD64(feat); got != tc.wantLvl {
				t.Errorf("EvaluateAMD64() with %s = %q, want %q", tc.name, got, tc.wantLvl)
			}
		})
	}
}

func TestCurrentX86FeaturesExclusiveCPUID(t *testing.T) {
	origProbe := probeX86ExtraFeaturesFunc
	defer func() {
		probeX86ExtraFeaturesFunc = origProbe
	}()

	// Verify currentX86Features strictly reflects CPUID probe responses without any fallback
	probeX86ExtraFeaturesFunc = func() (bool, bool, bool) {
		return true, false, true
	}
	feat1 := currentX86Features()
	if !feat1.HasF16C || feat1.HasLZCNT || !feat1.HasMOVBE {
		t.Errorf("expected feat1: f16c=true, lzcnt=false, movbe=true; got f16c=%v, lzcnt=%v, movbe=%v",
			feat1.HasF16C, feat1.HasLZCNT, feat1.HasMOVBE)
	}

	probeX86ExtraFeaturesFunc = func() (bool, bool, bool) {
		return false, true, false
	}
	feat2 := currentX86Features()
	if feat2.HasF16C || !feat2.HasLZCNT || feat2.HasMOVBE {
		t.Errorf("expected feat2: f16c=false, lzcnt=true, movbe=false; got f16c=%v, lzcnt=%v, movbe=%v",
			feat2.HasF16C, feat2.HasLZCNT, feat2.HasMOVBE)
	}
}

func TestCPUIDDirectProbing(t *testing.T) {
	t.Parallel()

	// Call cpuid directly on basic and extended leaves
	maxBasic, _, _, _ := cpuid(cpuidBasicLeafInfo, 0)
	if maxBasic > 0 {
		_, _, ecx, _ := cpuid(cpuidBasicLeafFeatures, 0)
		_ = (ecx & (1 << cpuidLeaf1ECXMOVBEBit)) != 0
		_ = (ecx & (1 << cpuidLeaf1ECXF16CBit)) != 0
	}

	maxExt, _, _, _ := cpuid(cpuidExtLeafInfo, 0)
	if maxExt >= cpuidExtLeafFeatures {
		_, _, ecx, _ := cpuid(cpuidExtLeafFeatures, 0)
		_ = (ecx & (1 << cpuidLeafExt1ECXABMBit)) != 0
	}
}


