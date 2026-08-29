package microarch

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const testArchAMD64 = "amd64"

func TestDetectAndCurrentLevel(t *testing.T) {
	t.Parallel()
	info := Detect()
	if info.OS == "" {
		t.Errorf("expected non-empty OS, got empty")
	}
	if info.Arch == "" {
		t.Errorf("expected non-empty Arch, got empty")
	}
	if info.Level == "" {
		t.Errorf("expected non-empty Level, got empty")
	}

	cur := CurrentLevel()
	if cur != info.Level {
		t.Errorf("CurrentLevel() = %q, expected %q", cur, info.Level)
	}

	if !IsSupported(cur) {
		t.Errorf("expected CurrentLevel %q to be supported on host", cur)
	}
}

func TestEvaluateAMD64(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		features X86Features
		expected string
	}{
		{
			name:     "baseline v1 (no features)",
			features: X86Features{},
			expected: AMD64v1,
		},
		{
			name: "partial v2 missing POPCNT",
			features: X86Features{
				HasCX16:  true,
				HasSSE3:  true,
				HasSSSE3: true,
				HasSSE41: true,
				HasSSE42: true,
			},
			expected: AMD64v1,
		},
		{
			name: "complete v2",
			features: X86Features{
				HasCX16:   true,
				HasPOPCNT: true,
				HasSSE3:   true,
				HasSSSE3:  true,
				HasSSE41:  true,
				HasSSE42:  true,
			},
			expected: AMD64v2,
		},
		{
			name: "v2 with AVX but missing AVX2/FMA",
			features: X86Features{
				HasCX16:   true,
				HasPOPCNT: true,
				HasSSE3:   true,
				HasSSSE3:  true,
				HasSSE41:  true,
				HasSSE42:  true,
				HasAVX:    true,
			},
			expected: AMD64v2,
		},
		{
			name: "complete v3",
			features: X86Features{
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
			},
			expected: AMD64v3,
		},
		{
			name: "v3 missing F16C",
			features: X86Features{
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
				HasLZCNT:    true,
				HasMOVBE:    true,
			},
			expected: AMD64v2,
		},
		{
			name: "v3 missing LZCNT",
			features: X86Features{
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
				HasMOVBE:    true,
			},
			expected: AMD64v2,
		},
		{
			name: "v3 missing MOVBE",
			features: X86Features{
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
			},
			expected: AMD64v2,
		},
		{
			name: "complete v4 (AVX-512)",
			features: X86Features{
				HasCX16:     true,
				HasPOPCNT:   true,
				HasSSE3:     true,
				HasSSSE3:    true,
				HasSSE41:    true,
				HasSSE42:    true,
				HasAVX:      true,
				HasAVX2:     true,
				HasBMI1:     true,
				HasBMI2:     true,
				HasFMA:      true,
				HasOSXSAVE:  true,
				HasF16C:     true,
				HasLZCNT:    true,
				HasMOVBE:    true,
				HasAVX512F:  true,
				HasAVX512BW: true,
				HasAVX512CD: true,
				HasAVX512DQ: true,
				HasAVX512VL: true,
			},
			expected: AMD64v4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := EvaluateAMD64(tt.features)
			if got != tt.expected {
				t.Errorf("EvaluateAMD64() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func arm64BaseV8_0() ARM64Features {
	return ARM64Features{HasFP: true, HasASIMD: true}
}

func arm64BaseV8_1() ARM64Features {
	f := arm64BaseV8_0()
	f.HasATOMICS = true
	f.HasCRC32 = true
	return f
}

func arm64BaseV8_2() ARM64Features {
	f := arm64BaseV8_1()
	f.HasFPHP = true
	f.HasASIMDHP = true
	return f
}

func arm64BaseV8_3() ARM64Features {
	f := arm64BaseV8_2()
	f.HasJSCVT = true
	f.HasFCMA = true
	f.HasLRCPC = true
	return f
}

func arm64BaseV8_4() ARM64Features {
	f := arm64BaseV8_3()
	f.HasDCPOP = true
	f.HasASIMDDP = true
	return f
}

func arm64BaseV8_5() ARM64Features {
	f := arm64BaseV8_4()
	f.HasDIT = true
	return f
}

func arm64BaseV8_6() ARM64Features {
	f := arm64BaseV8_5()
	f.HasI8MM = true
	f.HasBF16 = true
	return f
}

func arm64BaseV8_7() ARM64Features {
	f := arm64BaseV8_6()
	f.HasWFxT = true
	return f
}

func arm64BaseV9_0() ARM64Features {
	f := arm64BaseV8_5()
	f.HasSVE = true
	return f
}

func arm64BaseV9_1() ARM64Features {
	f := arm64BaseV9_0()
	f.HasI8MM = true
	f.HasBF16 = true
	return f
}

func arm64BaseV9_2() ARM64Features {
	f := arm64BaseV9_1()
	f.HasSVE2 = true
	return f
}

func arm64BaseV9_3() ARM64Features {
	f := arm64BaseV9_2()
	f.HasSME = true
	return f
}

func arm64BaseV9_4() ARM64Features {
	return arm64BaseV9_3()
}

func arm64BaseV9_5() ARM64Features {
	f := arm64BaseV9_4()
	f.HasSME2 = true
	return f
}

func TestARM64Requirements(t *testing.T) {
	t.Parallel()
	reqs := ARM64Requirements()
	if len(reqs) != 14 {
		t.Fatalf("expected 14 ARM64 level requirements, got %d", len(reqs))
	}
	for _, req := range reqs {
		if req.Level == "" {
			t.Errorf("expected non-empty level")
		}
		if req.SourceDoc == "" {
			t.Errorf("expected non-empty SourceDoc for level %s", req.Level)
		}
	}
}

func TestEvaluateARM64(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		features ARM64Features
		expected string
	}{
		{
			name:     "baseline v8.0",
			features: arm64BaseV8_0(),
			expected: ARM64v8_0,
		},
		{
			name:     "v8.1 (atomics + crc32)",
			features: arm64BaseV8_1(),
			expected: ARM64v8_1,
		},
		{
			name:     "v8.2 (fp16)",
			features: arm64BaseV8_2(),
			expected: ARM64v8_2,
		},
		{
			name:     "v8.3 (jscvt, fcma, lrcpc)",
			features: arm64BaseV8_3(),
			expected: ARM64v8_3,
		},
		{
			name:     "v8.4 (dcpop, asimddp)",
			features: arm64BaseV8_4(),
			expected: ARM64v8_4,
		},
		{
			name:     "v8.5 (dit + dcpop)",
			features: arm64BaseV8_5(),
			expected: ARM64v8_5,
		},
		{
			name:     "v8.6 (i8mm + bf16)",
			features: arm64BaseV8_6(),
			expected: ARM64v8_6,
		},
		{
			name:     "v8.7 (wfxt)",
			features: arm64BaseV8_7(),
			expected: ARM64v8_7,
		},
		{
			name:     "v9.0 (sve + v8.5)",
			features: arm64BaseV9_0(),
			expected: ARM64v9_0,
		},
		{
			name:     "v9.1 (v9.0 + v8.6)",
			features: arm64BaseV9_1(),
			expected: ARM64v9_1,
		},
		{
			name:     "v9.2 (v9.1 + sve2)",
			features: arm64BaseV9_2(),
			expected: ARM64v9_2,
		},
		{
			name:     "v9.3 / v9.4 (v9.2 + sme)",
			features: arm64BaseV9_3(),
			expected: ARM64v9_4,
		},
		{
			name:     "v9.4 (v9.3)",
			features: arm64BaseV9_4(),
			expected: ARM64v9_4,
		},
		{
			name:     "v9.5 (v9.4 + sme2)",
			features: arm64BaseV9_5(),
			expected: ARM64v9_5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := EvaluateARM64(tt.features)
			if got != tt.expected {
				t.Errorf("EvaluateARM64() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestEvaluateARM64_PrerequisiteFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		features ARM64Features
		expected string
	}{
		{
			name: "v9.0 with missing v8.1 atomics falls back to v8.0",
			features: func() ARM64Features {
				f := arm64BaseV9_0()
				f.HasATOMICS = false
				return f
			}(),
			expected: ARM64v8_0,
		},
		{
			name: "v9.0 with missing v8.5 DIT falls back to v8.4",
			features: func() ARM64Features {
				f := arm64BaseV9_0()
				f.HasDIT = false
				return f
			}(),
			expected: ARM64v8_4,
		},
		{
			name: "v9.1 with missing v8.6 BF16 falls back to v9.0",
			features: func() ARM64Features {
				f := arm64BaseV9_1()
				f.HasBF16 = false
				return f
			}(),
			expected: ARM64v9_0,
		},
		{
			name: "v9.1 with missing v9.0 SVE falls back to v8.6",
			features: func() ARM64Features {
				f := arm64BaseV9_1()
				f.HasSVE = false
				return f
			}(),
			expected: ARM64v8_6,
		},
		{
			name: "v8.7 with missing v8.6 I8MM falls back to v8.5",
			features: func() ARM64Features {
				f := arm64BaseV8_7()
				f.HasI8MM = false
				return f
			}(),
			expected: ARM64v8_5,
		},
		{
			name: "v8.7 with missing WFxT falls back to v8.6",
			features: func() ARM64Features {
				f := arm64BaseV8_7()
				f.HasWFxT = false
				return f
			}(),
			expected: ARM64v8_6,
		},
		{
			name: "v9.5 with missing SME falls back to v9.2",
			features: func() ARM64Features {
				f := arm64BaseV9_5()
				f.HasSME = false
				return f
			}(),
			expected: ARM64v9_2,
		},
		{
			name: "v9.5 with missing SVE2 falls back to v9.1",
			features: func() ARM64Features {
				f := arm64BaseV9_5()
				f.HasSVE2 = false
				return f
			}(),
			expected: ARM64v9_1,
		},
		{
			name: "missing FP and ASIMD returns baseline v8.0",
			features: func() ARM64Features {
				return ARM64Features{HasSVE: true}
			}(),
			expected: ARM64v8_0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := EvaluateARM64(tt.features)
			if got != tt.expected {
				t.Errorf("EvaluateARM64() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestEvaluateARM64Detailed(t *testing.T) {
	t.Parallel()

	// 1. Fully satisfied v9.2
	f := arm64BaseV9_2()
	highest, statuses := EvaluateARM64Detailed(f)
	if highest != ARM64v9_2 {
		t.Errorf("expected highest %s, got %s", ARM64v9_2, highest)
	}
	if len(statuses) != 14 {
		t.Fatalf("expected 14 statuses, got %d", len(statuses))
	}

	// 2. Missing v8.6 BF16
	f2 := arm64BaseV9_2()
	f2.HasBF16 = false
	highest2, statuses2 := EvaluateARM64Detailed(f2)
	if highest2 != ARM64v9_0 {
		t.Errorf("expected highest %s, got %s", ARM64v9_0, highest2)
	}
	for _, st := range statuses2 {
		if st.Level == ARM64v9_1 {
			if st.Satisfied {
				t.Errorf("expected v9.1 to not be satisfied")
			}
			if len(st.MissingPrereqs) == 0 {
				t.Errorf("expected missing prereqs for v9.1")
			}
		}
	}
}

func TestBestMatchingVariantFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		arch        string
		hostLevel   string
		available   []string
		expected    string
		expectError bool
	}{
		{
			name:        "host v4 with v1, v3, v4 available",
			arch:        testArchAMD64,
			hostLevel:   "v4",
			available:   []string{"v1", "v3", "v4"},
			expected:    "v4",
			expectError: false,
		},
		{
			name:        "host v4 with only v1 and v3 available (fallback)",
			arch:        testArchAMD64,
			hostLevel:   "v4",
			available:   []string{"v1", "v3"},
			expected:    "v3",
			expectError: false,
		},
		{
			name:        "host v2 with v1, v3, v4 available (v3/v4 too high)",
			arch:        testArchAMD64,
			hostLevel:   "v2",
			available:   []string{"v1", "v3", "v4"},
			expected:    "v1",
			expectError: false,
		},
		{
			name:        "host v1 with only v3 available (incompatible)",
			arch:        testArchAMD64,
			hostLevel:   "v1",
			available:   []string{"v3", "v4"},
			expected:    "",
			expectError: true,
		},
		{
			name:        "empty available variants",
			arch:        testArchAMD64,
			hostLevel:   "v3",
			available:   []string{},
			expected:    "",
			expectError: true,
		},
		{
			name:        "arm64 host v8.2 with v8.0 and v8.2 available",
			arch:        ArchARM64,
			hostLevel:   ARM64v8_2,
			available:   []string{"linux_arm64_v8.0", "linux_arm64_v8.2"},
			expected:    "linux_arm64_v8.2",
			expectError: false,
		},
		{
			name:        "arm64 host v8.1 with v8.0, v8.2, v9.0 available",
			arch:        ArchARM64,
			hostLevel:   ARM64v8_1,
			available:   []string{ARM64v8_0, ARM64v8_2, ARM64v9_0},
			expected:    ARM64v8_0,
			expectError: false,
		},
		{
			name:        "arm64 host v9.2 with v8.0, v8.2, v9.0, v9.2 available",
			arch:        ArchARM64,
			hostLevel:   ARM64v9_2,
			available:   []string{ARM64v8_0, ARM64v8_2, ARM64v9_0, ARM64v9_2},
			expected:    ARM64v9_2,
			expectError: false,
		},
		{
			name:        "arm64 host v9.5 with v8.0, v8.2, v9.0 available",
			arch:        ArchARM64,
			hostLevel:   ARM64v9_5,
			available:   []string{ARM64v8_0, ARM64v8_2, ARM64v9_0},
			expected:    ARM64v9_0,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := BestMatchingVariantFor(tt.arch, tt.hostLevel, tt.available)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil (result: %q)", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.expected {
				t.Errorf("BestMatchingVariantFor() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestNormalizeAndRank(t *testing.T) {
	t.Parallel()
	prefixes := []string{
		"linux_amd64_v3",
		"darwin_arm64_v8.2",
		"windows_amd64_v2",
		"amd64_v4",
		"arm64_v8.0",
		"x86_64_v1",
		"aarch64_v9.0",
		"arm64-v8.2",
		"aarch64-v9.2",
		"",
	}
	for _, p := range prefixes {
		norm := Normalize(p)
		if norm == "" {
			t.Errorf("Normalize(%q) returned empty", p)
		}
	}

	if Rank(testArchAMD64, "v1") != 1 || Rank(testArchAMD64, "v2") != 2 ||
		Rank(testArchAMD64, "v3") != 3 || Rank(testArchAMD64, "v4") != 4 {
		t.Errorf("unexpected amd64 rank")
	}

	armLevels := []string{
		ARM64v8_0, ARM64v8_1, ARM64v8_2, ARM64v8_3, ARM64v8_4,
		ARM64v8_5, ARM64v8_6, ARM64v8_7, ARM64v9_0, ARM64v9_1,
		ARM64v9_2, ARM64v9_3, ARM64v9_4, ARM64v9_5,
	}
	for _, lvl := range armLevels {
		if Rank(ArchARM64, lvl) <= 0 {
			t.Errorf("expected positive rank for arm64 level %s", lvl)
		}
	}

	if Rank("other", "v1") != 1 {
		t.Errorf("expected Rank(other, v1) == 1")
	}
	if Rank("other", "unknown") != -1 {
		t.Errorf("expected Rank(other, unknown) == -1")
	}
	if Rank(testArchAMD64, "unknown") != -1 {
		t.Errorf("expected -1 for unknown amd64 level")
	}
	if Rank(ArchARM64, "unknown") != -1 {
		t.Errorf("expected -1 for unknown arm64 level")
	}

	if Compare(testArchAMD64, "v3", "v2") <= 0 {
		t.Errorf("expected Compare(v3, v2) > 0")
	}
	if Compare(testArchAMD64, "v1", "v3") >= 0 {
		t.Errorf("expected Compare(v1, v3) < 0")
	}
	if Compare(testArchAMD64, "v2", "v2") != 0 {
		t.Errorf("expected Compare(v2, v2) == 0")
	}

	if Compare(ArchARM64, "v9.2", "v8.2") <= 0 {
		t.Errorf("expected Compare(v9.2, v8.2) > 0")
	}
	if Compare(ArchARM64, "v8.0", "v9.0") >= 0 {
		t.Errorf("expected Compare(v8.0, v9.0) < 0")
	}
}

func TestExtractFeatureLists(t *testing.T) {
	t.Parallel()
	allX86 := X86Features{
		HasCX16:     true,
		HasPOPCNT:   true,
		HasSSE3:     true,
		HasSSSE3:    true,
		HasSSE41:    true,
		HasSSE42:    true,
		HasAVX:      true,
		HasAVX2:     true,
		HasBMI1:     true,
		HasBMI2:     true,
		HasFMA:      true,
		HasOSXSAVE:  true,
		HasF16C:     true,
		HasLZCNT:    true,
		HasMOVBE:    true,
		HasAVX512F:  true,
		HasAVX512BW: true,
		HasAVX512CD: true,
		HasAVX512DQ: true,
		HasAVX512VL: true,
	}
	x86List := extractX86FeatureList(allX86)
	const expectedX86Count = 20
	if len(x86List) != expectedX86Count {
		t.Errorf("expected %d x86 features, got %d", expectedX86Count, len(x86List))
	}

	allARM64 := ARM64Features{
		HasFP:       true,
		HasASIMD:    true,
		HasATOMICS:  true,
		HasCRC32:    true,
		HasFPHP:     true,
		HasASIMDHP:  true,
		HasJSCVT:    true,
		HasFCMA:     true,
		HasLRCPC:    true,
		HasDCPOP:    true,
		HasASIMDDP:  true,
		HasDIT:      true,
		HasSVE:      true,
		HasSVE2:     true,
		HasI8MM:     true,
		HasBF16:     true,
		HasSME:      true,
		HasSME2:     true,
		HasAES:      true,
		HasPMULL:    true,
		HasSHA1:     true,
		HasSHA2:     true,
		HasSHA3:     true,
		HasSHA512:   true,
		HasSM3:      true,
		HasSM4:      true,
		HasASIMDFHM: true,
		HasWFxT:     true,
		HasASIMDRDM: true,
	}
	armList := extractARM64FeatureList(allARM64)
	const expectedARM64Count = 29
	if len(armList) != expectedARM64Count {
		t.Errorf("expected %d arm64 features, got %d", expectedARM64Count, len(armList))
	}

	_ = currentARM64Features()
	_ = currentX86Features()
}

func TestBestMatchingVariantHost(t *testing.T) {
	t.Parallel()
	_, err := BestMatchingVariant([]string{})
	if !errors.Is(err, ErrEmptyVariants) {
		t.Errorf("expected ErrEmptyVariants, got %v", err)
	}

	// Always includes v1, which should succeed on all hosts
	matched, err := BestMatchingVariant([]string{"v1"})
	if err != nil {
		t.Fatalf("expected v1 to succeed on any host: %v", err)
	}
	if matched != "v1" {
		t.Errorf("expected v1, got %q", matched)
	}
}

func TestDetectForArch(t *testing.T) {
	t.Parallel()
	// Detect default host
	hostInfo := Detect()
	if hostInfo.OS == "" || hostInfo.Arch == "" || hostInfo.Level == "" {
		t.Errorf("invalid host info: %+v", hostInfo)
	}

	// Detect ARM64
	armInfo := detectForArch("linux", ArchARM64, X86Features{}, ARM64Features{HasFP: true, HasASIMD: true, HasATOMICS: true})
	if armInfo.Arch != ArchARM64 || armInfo.Level != "v8.0" || len(armInfo.Features) == 0 || len(armInfo.Levels) == 0 {
		t.Errorf("unexpected arm64 detect info: %+v", armInfo)
	}

	// Detect unsupported/fallback arch
	otherInfo := detectForArch("linux", "mips64", X86Features{}, ARM64Features{})
	if otherInfo.Level != "v1" {
		t.Errorf("expected level v1 for fallback arch, got %s", otherInfo.Level)
	}
}

func TestParseLinuxAuxvARM64(t *testing.T) {
	t.Parallel()

	// 1. Construct valid auxv binary data with AT_HWCAP2
	auxvData := make([]byte, 32)
	binary.LittleEndian.PutUint64(auxvData[0:8], auxvAT_HWCAP2)
	binary.LittleEndian.PutUint64(auxvData[8:16], hwcap2BF16|hwcap2WFXT|hwcap2SME|hwcap2SME2)
	binary.LittleEndian.PutUint64(auxvData[16:24], 0)
	binary.LittleEndian.PutUint64(auxvData[24:32], 0)

	bf16, wfxt, sme, sme2 := parseLinuxAuxvARM64(auxvData)
	if !bf16 || !wfxt || !sme || !sme2 {
		t.Errorf("expected all auxv flags to be true, got bf16=%v, wfxt=%v, sme=%v, sme2=%v", bf16, wfxt, sme, sme2)
	}

	// 2. Empty / truncated data
	bf16, wfxt, sme, sme2 = parseLinuxAuxvARM64([]byte{1, 2, 3})
	if bf16 || wfxt || sme || sme2 {
		t.Errorf("expected all false for truncated auxv data")
	}

	// 3. Different tag
	otherTagData := make([]byte, 16)
	binary.LittleEndian.PutUint64(otherTagData[0:8], 16) // AT_HWCAP
	binary.LittleEndian.PutUint64(otherTagData[8:16], 0xFFFFFFFFFFFFFFFF)
	bf16, wfxt, sme, sme2 = parseLinuxAuxvARM64(otherTagData)
	if bf16 || wfxt || sme || sme2 {
		t.Errorf("expected all false for non-HWCAP2 tag")
	}
}

func TestEvaluateARM64_RealWorldHardware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		features ARM64Features
		expected string
	}{
		{
			name: "AWS Graviton 2 (Neoverse N1: ARMv8.2-A)",
			features: func() ARM64Features {
				f := arm64BaseV8_2()
				f.HasAES = true
				f.HasPMULL = true
				f.HasSHA1 = true
				f.HasSHA2 = true
				f.HasCRC32 = true
				f.HasATOMICS = true
				return f
			}(),
			expected: ARM64v8_2,
		},
		{
			name: "AWS Graviton 3 (Neoverse V1: ARMv8.4-A + SVE + BF16 + I8MM + DIT -> v9.1)",
			features: func() ARM64Features {
				f := arm64BaseV8_5()
				f.HasSVE = true
				f.HasBF16 = true
				f.HasI8MM = true
				return f
			}(),
			expected: ARM64v9_1,
		},
		{
			name: "AWS Graviton 4 / NVIDIA Grace (Neoverse V2: ARMv9.0-A + SVE2 + BF16 + I8MM -> v9.2)",
			features: func() ARM64Features {
				f := arm64BaseV8_5()
				f.HasSVE = true
				f.HasSVE2 = true
				f.HasBF16 = true
				f.HasI8MM = true
				return f
			}(),
			expected: ARM64v9_2,
		},
		{
			name: "Apple Silicon M1/M2/M3 (ARMv8.5-A + BF16 + I8MM -> v8.6)",
			features: func() ARM64Features {
				f := arm64BaseV8_5()
				f.HasBF16 = true
				f.HasI8MM = true
				return f
			}(),
			expected: ARM64v8_6,
		},
		{
			name: "Raspberry Pi 4 (Cortex-A72: ARMv8.0-A + CRC32)",
			features: func() ARM64Features {
				f := arm64BaseV8_0()
				f.HasCRC32 = true
				return f
			}(),
			expected: ARM64v8_0,
		},
		{
			name: "Raspberry Pi 5 (Cortex-A76: ARMv8.2-A + DotProd)",
			features: func() ARM64Features {
				f := arm64BaseV8_2()
				f.HasASIMDDP = true
				return f
			}(),
			expected: ARM64v8_2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := EvaluateARM64(tt.features)
			if got != tt.expected {
				t.Errorf("EvaluateARM64() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestCurrentARM64FeaturesAuxv(t *testing.T) {
	origFunc := readLinuxAuxvARM64Func
	defer func() { readLinuxAuxvARM64Func = origFunc }()

	readLinuxAuxvARM64Func = func() (bool, bool, bool, bool) {
		return true, true, true, true
	}
	f := currentARM64Features()
	if !f.HasBF16 || !f.HasWFxT || !f.HasSME || !f.HasSME2 {
		t.Errorf("expected mock auxv flags to be populated, got %+v", f)
	}

	_, _, _, _ = readLinuxAuxvARM64()
}

func TestReadPolicyFromEnv(t *testing.T) {
	t.Setenv(format.EnvForceLevel, "v3")
	t.Setenv(format.EnvMaxLevel, "v4")
	t.Setenv(format.EnvDisableVariants, "v2, v1")
	t.Setenv(format.EnvPolicy, "safe_avx512")
	t.Setenv(format.EnvAVX512DownclockProtection, "1")

	p := ReadPolicyFromEnv()
	if p.ForceLevel != "v3" {
		t.Errorf("expected ForceLevel v3, got %s", p.ForceLevel)
	}
	if p.MaxLevel != "v4" {
		t.Errorf("expected MaxLevel v4, got %s", p.MaxLevel)
	}
	if len(p.DisabledVariants) != 2 || p.DisabledVariants[0] != "v2" || p.DisabledVariants[1] != "v1" {
		t.Errorf("unexpected DisabledVariants: %+v", p.DisabledVariants)
	}
	if p.Name != "safe_avx512" {
		t.Errorf("expected Policy Name safe_avx512, got %s", p.Name)
	}
	if !p.AVX512DownclockProtection {
		t.Errorf("expected AVX512DownclockProtection to be true")
	}
}

func TestSelectVariantWithPolicy_ForceLevel(t *testing.T) {
	t.Parallel()

	// 1. Valid force level within host capabilities
	res, err := SelectVariantWithPolicy(testArchAMD64, "v4", []string{"v1", "v2", "v3", "v4"}, Policy{ForceLevel: "v2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SelectedVariant != "v2" || res.PolicyApplied != "force_level" {
		t.Errorf("unexpected result: %+v", res)
	}

	// 2. Incompatible force level exceeding host capabilities (strict fail-fast)
	_, err = SelectVariantWithPolicy(testArchAMD64, "v2", []string{"v1", "v2", "v3", "v4"}, Policy{ForceLevel: "v4"})
	if !errors.Is(err, ErrIncompatibleForcedVariant) {
		t.Errorf("expected ErrIncompatibleForcedVariant, got %v", err)
	}

	// 3. Unknown forced level
	_, err = SelectVariantWithPolicy(testArchAMD64, "v4", []string{"v1", "v2"}, Policy{ForceLevel: "v99"})
	if !errors.Is(err, ErrIncompatibleForcedVariant) {
		t.Errorf("expected ErrIncompatibleForcedVariant for unknown level, got %v", err)
	}

	// 4. Force level compatible with host but not embedded in binary
	_, err = SelectVariantWithPolicy(testArchAMD64, "v4", []string{"v1", "v2"}, Policy{ForceLevel: "v3"})
	if !errors.Is(err, ErrVariantNotEmbedded) {
		t.Errorf("expected ErrVariantNotEmbedded, got %v", err)
	}
}

func TestSelectVariantWithPolicy_MaxLevelAndDisabled(t *testing.T) {
	t.Parallel()

	// 1. MaxLevel capping
	res, err := SelectVariantWithPolicy(testArchAMD64, "v4", []string{"v1", "v2", "v3", "v4"}, Policy{MaxLevel: "v3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SelectedVariant != "v3" || res.PolicyApplied != "max_level" {
		t.Errorf("unexpected max_level result: %+v", res)
	}

	// 2. DisabledVariants filtering
	res, err = SelectVariantWithPolicy(testArchAMD64, "v4", []string{"v1", "v2", "v3", "v4"}, Policy{DisabledVariants: []string{"v4", "v3"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SelectedVariant != "v2" || res.PolicyApplied != "disable_variants" {
		t.Errorf("unexpected disable_variants result: %+v", res)
	}

	// 3. MaxLevel + DisabledVariants combined
	res, err = SelectVariantWithPolicy(testArchAMD64, "v4", []string{"v1", "v2", "v3", "v4"}, Policy{
		MaxLevel:         "v3",
		DisabledVariants: []string{"v3"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SelectedVariant != "v2" {
		t.Errorf("expected v2, got %+v", res)
	}

	// 4. All variants disabled -> ErrNoMatchingVariant
	_, err = SelectVariantWithPolicy(testArchAMD64, "v3", []string{"v1", "v2", "v3"}, Policy{
		DisabledVariants: []string{"v1", "v2", "v3"},
	})
	if !errors.Is(err, ErrNoMatchingVariant) {
		t.Errorf("expected ErrNoMatchingVariant, got %v", err)
	}

	// 5. Empty available variants
	_, err = SelectVariantWithPolicy(testArchAMD64, "v3", nil, Policy{})
	if !errors.Is(err, ErrEmptyVariants) {
		t.Errorf("expected ErrEmptyVariants, got %v", err)
	}

	// 6. Unknown host level
	_, err = SelectVariantWithPolicy(testArchAMD64, "unknown_lvl", []string{"v1"}, Policy{})
	if err == nil {
		t.Errorf("expected error for unknown host level")
	}
}

func TestSelectVariantWithPolicy_SkylakeXDownclockProtection(t *testing.T) {
	origFunc := isSkylakeXOrCascadeLakeFunc
	defer func() { isSkylakeXOrCascadeLakeFunc = origFunc }()

	// Mock host as Skylake-X (AVX-512 capable v4 host)
	isSkylakeXOrCascadeLakeFunc = func() bool { return true }

	// When downclock protection is active on a v4 host, it caps at v3
	res, err := SelectVariantWithPolicy(testArchAMD64, "v4", []string{"v1", "v2", "v3", "v4"}, Policy{
		AVX512DownclockProtection: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SelectedVariant != "v3" || res.PolicyApplied != "avx512_downclock_protection" {
		t.Errorf("expected capped v3 with downclock protection, got %+v", res)
	}

	// When policy name is "safe_avx512", it activates protection
	res, err = SelectVariantWithPolicy(testArchAMD64, "v4", []string{"v1", "v2", "v3", "v4"}, Policy{
		Name: PolicySafeAVX512,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SelectedVariant != "v3" || res.PolicyApplied != "avx512_downclock_protection" {
		t.Errorf("expected capped v3 with safe_avx512, got %+v", res)
	}

	// When host is NOT Skylake-X, v4 is retained
	isSkylakeXOrCascadeLakeFunc = func() bool { return false }
	res, err = SelectVariantWithPolicy(testArchAMD64, "v4", []string{"v1", "v2", "v3", "v4"}, Policy{
		AVX512DownclockProtection: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SelectedVariant != "v4" {
		t.Errorf("expected v4 on non-Skylake host, got %+v", res)
	}
}

func TestParseLinuxCPUInfoX86(t *testing.T) {
	t.Parallel()

	mockSkylakeX := "processor\t: 0\n" +
		"vendor_id\t: GenuineIntel\n" +
		"cpu family\t: 6\n" +
		"model\t\t: 85\n" +
		"model name\t: Intel(R) Xeon(R) Platinum 8175M CPU @ 2.50GHz\n" +
		"stepping\t: 4\n" +
		"flags\t\t: fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush mmx fxsr sse sse2\n" +
		"flags\t\t: ss ht syscall nx pdpe1gb rdtscp lm constant_tsc rep_good nopl xtopology nonstop_tsc cpuid\n" +
		"flags\t\t: pni pclmulqdq ssse3 fma cx16 pcid sse4_1 sse4_2 x2apic movbe popcnt tsc_deadline_timer aes xsave avx\n" +
		"flags\t\t: f16c rdrand hypervisor lahf_lm abm 3dnowprefetch invpcid_single pti fsgsbase tsc_adjust bmi1 hle\n" +
		"flags\t\t: avx2 smep bmi2 erms invpcid rtm mpx avx512f avx512dq rdseed adx smap clflushopt clwb avx512cd avx512bw\n"
	info := parseLinuxCPUInfoX86(strings.NewReader(mockSkylakeX))
	if info.Vendor != "GenuineIntel" || info.Family != 6 || info.Model != 85 || info.Stepping != 4 {
		t.Errorf("unexpected parsed x86 cpu info: %+v", info)
	}

	// Test default reader implementations
	_ = readLinuxCPUInfoX86()
	_, _, _ = probeX86ExtraFeatures()
	_ = isHostSkylakeXOrCascadeLake()
	_ = IsAVX512DownclockingRisk()
}

func BenchmarkSelectVariantWithPolicy(b *testing.B) {
	amd64Variants := []string{"v1", "v2", "v3", "v4"}
	arm64Variants := []string{"v8.0", "v8.1", "v8.2", "v8.4", "v9.0", "v9.2"}

	b.Run("AMD64_Default", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = SelectVariantWithPolicy(ArchAMD64, AMD64v3, amd64Variants, Policy{})
		}
	})

	b.Run("AMD64_PolicyCaps", func(b *testing.B) {
		b.ReportAllocs()
		p := Policy{
			MaxLevel:         "v2",
			DisabledVariants: []string{"v3"},
		}
		for i := 0; i < b.N; i++ {
			_, _ = SelectVariantWithPolicy(ArchAMD64, AMD64v4, amd64Variants, p)
		}
	})

	b.Run("ARM64_Default", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = SelectVariantWithPolicy(ArchARM64, ARM64v9_2, arm64Variants, Policy{})
		}
	})
}

func BenchmarkNormalize(b *testing.B) {
	inputs := []string{"amd64_v3", "arm64-v8.2", "linux_v1", "V4"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Normalize(inputs[i%len(inputs)])
	}
}

func BenchmarkDetect(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Detect()
	}
}

func TestARM64Evaluation_EdgeCases(t *testing.T) {
	t.Parallel()

	// 1. Unknown level in checkARM64LevelSatisfied
	sat := checkARM64LevelSatisfied("v_unknown", ARM64Features{}, make(map[string]bool), make(map[string]bool))
	if sat {
		t.Errorf("expected unknown level to not be satisfied")
	}

	// 2. Unknown feature in hasARM64NamedFeature
	hasFeat := hasARM64NamedFeature("unknown_feat", ARM64Features{})
	if hasFeat {
		t.Errorf("expected unknown feature to return false")
	}

	// 3. Cycle guard simulation in checkARM64LevelSatisfied
	visiting := map[string]bool{"v8.1": true}
	satCycle := checkARM64LevelSatisfied("v8.1", ARM64Features{}, make(map[string]bool), visiting)
	if satCycle {
		t.Errorf("expected visiting level to return false")
	}
}

