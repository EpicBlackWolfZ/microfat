package microarch

import (
	"errors"
	"testing"
)

const testArchAMD64 = "amd64"

func TestDetectAndCurrentLevel(t *testing.T) {
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
			},
			expected: AMD64v3,
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
			got := EvaluateAMD64(tt.features)
			if got != tt.expected {
				t.Errorf("EvaluateAMD64() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestEvaluateARM64(t *testing.T) {
	tests := []struct {
		name     string
		features ARM64Features
		expected string
	}{
		{
			name:     "baseline v8.0",
			features: ARM64Features{HasFP: true, HasASIMD: true},
			expected: ARM64v8_0,
		},
		{
			name:     "v8.1 (atomics + crc32)",
			features: ARM64Features{HasATOMICS: true, HasCRC32: true},
			expected: ARM64v8_1,
		},
		{
			name:     "v8.2 (fp16)",
			features: ARM64Features{HasATOMICS: true, HasCRC32: true, HasFPHP: true, HasASIMDHP: true},
			expected: ARM64v8_2,
		},
		{
			name: "v8.3 (jscvt, fcma, lrcpc)",
			features: ARM64Features{
				HasATOMICS: true, HasCRC32: true, HasFPHP: true, HasASIMDHP: true,
				HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
			},
			expected: ARM64v8_3,
		},
		{
			name: "v8.4 (dcpop, asimddp)",
			features: ARM64Features{
				HasATOMICS: true, HasCRC32: true, HasFPHP: true, HasASIMDHP: true,
				HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
				HasDCPOP: true, HasASIMDDP: true,
			},
			expected: ARM64v8_4,
		},
		{
			name: "v8.5 (dit)",
			features: ARM64Features{
				HasATOMICS: true, HasCRC32: true, HasFPHP: true, HasASIMDHP: true,
				HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
				HasDCPOP: true, HasASIMDDP: true, HasDIT: true,
			},
			expected: ARM64v8_5,
		},
		{
			name:     "v9.0 (sve)",
			features: ARM64Features{HasSVE: true},
			expected: ARM64v9_0,
		},
		{
			name:     "v9.2 (sve2 + i8mm)",
			features: ARM64Features{HasSVE2: true, HasI8MM: true},
			expected: ARM64v9_2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateARM64(tt.features)
			if got != tt.expected {
				t.Errorf("EvaluateARM64() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestBestMatchingVariantFor(t *testing.T) {
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
			arch:        "arm64",
			hostLevel:   "v8.2",
			available:   []string{"linux_arm64_v8.0", "linux_arm64_v8.2"},
			expected:    "linux_arm64_v8.2",
			expectError: false,
		},
		{
			name:        "arm64 host v8.1 with v8.0, v8.2, v9.0 available",
			arch:        "arm64",
			hostLevel:   "v8.1",
			available:   []string{"v8.0", "v8.2", "v9.0"},
			expected:    "v8.0",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
	prefixes := []string{
		"linux_amd64_v3",
		"darwin_arm64_v8.2",
		"windows_amd64_v2",
		"amd64_v4",
		"arm64_v8.0",
		"x86_64_v1",
		"aarch64_v9.0",
		"",
	}
	for _, p := range prefixes {
		norm := Normalize(p)
		if norm == "" {
			t.Errorf("Normalize(%q) returned empty", p)
		}
	}

	if Rank(testArchAMD64, "v1") != 1 || Rank(testArchAMD64, "v2") != 2 || Rank(testArchAMD64, "v3") != 3 || Rank(testArchAMD64, "v4") != 4 {
		t.Errorf("unexpected amd64 rank")
	}

	armLevels := []string{ARM64v8_0, ARM64v8_1, ARM64v8_2, ARM64v8_3, ARM64v8_4, ARM64v8_5, ARM64v9_0, ARM64v9_2}
	for _, lvl := range armLevels {
		if Rank("arm64", lvl) <= 0 {
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

	if Compare(testArchAMD64, "v3", "v2") <= 0 {
		t.Errorf("expected Compare(v3, v2) > 0")
	}
	if Compare(testArchAMD64, "v1", "v3") >= 0 {
		t.Errorf("expected Compare(v1, v3) < 0")
	}
	if Compare(testArchAMD64, "v2", "v2") != 0 {
		t.Errorf("expected Compare(v2, v2) == 0")
	}
}

func TestExtractFeatureLists(t *testing.T) {
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
		HasAVX512F:  true,
		HasAVX512BW: true,
		HasAVX512CD: true,
		HasAVX512DQ: true,
		HasAVX512VL: true,
	}
	x86List := extractX86FeatureList(allX86)
	const expectedX86Count = 17
	if len(x86List) != expectedX86Count {
		t.Errorf("expected %d x86 features, got %d", expectedX86Count, len(x86List))
	}

	allARM64 := ARM64Features{
		HasFP:      true,
		HasASIMD:   true,
		HasATOMICS: true,
		HasCRC32:   true,
		HasFPHP:    true,
		HasASIMDHP: true,
		HasJSCVT:   true,
		HasFCMA:    true,
		HasLRCPC:   true,
		HasDCPOP:   true,
		HasASIMDDP: true,
		HasDIT:     true,
		HasSVE:     true,
		HasSVE2:    true,
		HasI8MM:    true,
	}
	armList := extractARM64FeatureList(allARM64)
	const expectedARM64Count = 15
	if len(armList) != expectedARM64Count {
		t.Errorf("expected %d arm64 features, got %d", expectedARM64Count, len(armList))
	}

	_ = currentARM64Features()
	_ = currentX86Features()
}

func TestBestMatchingVariantHost(t *testing.T) {
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
	// Detect default host
	hostInfo := Detect()
	if hostInfo.OS == "" || hostInfo.Arch == "" || hostInfo.Level == "" {
		t.Errorf("invalid host info: %+v", hostInfo)
	}

	// Detect ARM64
	armInfo := detectForArch("linux", "arm64", X86Features{}, ARM64Features{HasFP: true, HasASIMD: true, HasATOMICS: true})
	if armInfo.Arch != "arm64" || armInfo.Level != "v8.0" || len(armInfo.Features) == 0 {
		t.Errorf("unexpected arm64 detect info: %+v", armInfo)
	}

	// Detect unsupported/fallback arch
	otherInfo := detectForArch("linux", "mips64", X86Features{}, ARM64Features{})
	if otherInfo.Level != "v1" {
		t.Errorf("expected level v1 for fallback arch, got %s", otherInfo.Level)
	}
}
