package microarch

import (
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

func TestEvaluateARM64(t *testing.T) {
	t.Parallel()
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
			name: "v8.5 (dit + dcpop)",
			features: ARM64Features{
				HasATOMICS: true, HasCRC32: true, HasFPHP: true, HasASIMDHP: true,
				HasJSCVT: true, HasFCMA: true, HasLRCPC: true,
				HasDCPOP: true, HasASIMDDP: true, HasDIT: true,
			},
			expected: ARM64v8_5,
		},
		{
			name: "v8.6 (i8mm or bf16)",
			features: ARM64Features{
				HasATOMICS: true, HasCRC32: true, HasI8MM: true,
			},
			expected: ARM64v8_6,
		},
		{
			name: "v8.6 (bf16)",
			features: ARM64Features{
				HasATOMICS: true, HasCRC32: true, HasBF16: true,
			},
			expected: ARM64v8_6,
		},
		{
			name:     "v9.0 (sve)",
			features: ARM64Features{HasSVE: true},
			expected: ARM64v9_0,
		},
		{
			name:     "v9.1 (sve2)",
			features: ARM64Features{HasSVE2: true},
			expected: ARM64v9_1,
		},
		{
			name:     "v9.2 (sve2 + i8mm)",
			features: ARM64Features{HasSVE2: true, HasI8MM: true},
			expected: ARM64v9_2,
		},
		{
			name:     "v9.3 (sme)",
			features: ARM64Features{HasSME: true},
			expected: ARM64v9_3,
		},
		{
			name:     "v9.5 (sme2)",
			features: ARM64Features{HasSME2: true},
			expected: ARM64v9_5,
		},
		{
			name:     "v9.5 (sme + sve2)",
			features: ARM64Features{HasSME: true, HasSVE2: true},
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
		HasASIMDRDM: true,
	}
	armList := extractARM64FeatureList(allARM64)
	const expectedARM64Count = 28
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
	if armInfo.Arch != ArchARM64 || armInfo.Level != "v8.0" || len(armInfo.Features) == 0 {
		t.Errorf("unexpected arm64 detect info: %+v", armInfo)
	}

	// Detect unsupported/fallback arch
	otherInfo := detectForArch("linux", "mips64", X86Features{}, ARM64Features{})
	if otherInfo.Level != "v1" {
		t.Errorf("expected level v1 for fallback arch, got %s", otherInfo.Level)
	}
}

func TestParseLinuxCPUInfoARM64AllTokens(t *testing.T) {
	t.Parallel()
	// Comprehensive test string containing all supported ARM64 tokens and aliases
	allTokens := `
Features : fp asimd neon atomics lse crc32 fphp asimdhp jscvt fcma lrcpc dcpop
Features : asimddp dotprod dit sve sve2 i8mm svei8mm bf16 svebf16 sme sme2
Features : aes pmull sha1 sha2 sha256 sha3 sha512 sm3 sm4 asimdfhm asimdrdm
`
	feat := parseLinuxCPUInfoARM64(strings.NewReader(allTokens))
	if !feat.HasFP || !feat.HasASIMD || !feat.HasATOMICS || !feat.HasCRC32 ||
		!feat.HasFPHP || !feat.HasASIMDHP || !feat.HasJSCVT || !feat.HasFCMA ||
		!feat.HasLRCPC || !feat.HasDCPOP || !feat.HasASIMDDP || !feat.HasDIT ||
		!feat.HasSVE || !feat.HasSVE2 || !feat.HasI8MM || !feat.HasBF16 ||
		!feat.HasSME || !feat.HasSME2 || !feat.HasAES || !feat.HasPMULL ||
		!feat.HasSHA1 || !feat.HasSHA2 || !feat.HasSHA3 || !feat.HasSHA512 ||
		!feat.HasSM3 || !feat.HasSM4 || !feat.HasASIMDFHM || !feat.HasASIMDRDM {
		t.Errorf("expected all ARM64 feature flags to be true, got %+v", feat)
	}
}

func TestParseLinuxCPUInfoARM64(t *testing.T) {
	t.Parallel()
	// Mock AWS Graviton 3 cpuinfo snippet
	mockGraviton3 := `
processor	: 0
BogoMIPS	: 2100.00
Features	: fp asimd evtstrm aes pmull sha1 sha2 crc32 atomics fphp asimdhp cpuid
Features	: asimdrdm jscvt fcma lrcpc dcpop asimddp sha3 sha512 sve sve2 i8mm bf16 dit
CPU implementer	: 0x41
CPU architecture: 8
CPU variant	: 0x1
CPU part	: 0xd40
CPU revision	: 1
`
	feat := parseLinuxCPUInfoARM64(strings.NewReader(mockGraviton3))
	if !feat.HasFP || !feat.HasASIMD || !feat.HasATOMICS || !feat.HasCRC32 || !feat.HasSVE || !feat.HasSVE2 || !feat.HasI8MM || !feat.HasBF16 {
		t.Errorf("parseLinuxCPUInfoARM64 failed on Graviton3 fixture: %+v", feat)
	}

	level := EvaluateARM64(feat)
	if level != ARM64v9_2 {
		t.Errorf("expected Graviton 3 to evaluate to %s, got %s", ARM64v9_2, level)
	}

	// Mock Raspberry Pi 4 (Cortex-A72) cpuinfo
	mockRpi4 := `
processor	: 0
Features	: fp asimd evtstrm crc32 cpuid
`
	rpiFeat := parseLinuxCPUInfoARM64(strings.NewReader(mockRpi4))
	if !rpiFeat.HasFP || !rpiFeat.HasASIMD || !rpiFeat.HasCRC32 {
		t.Errorf("parseLinuxCPUInfoARM64 failed on RPi4 fixture: %+v", rpiFeat)
	}
	rpiLevel := EvaluateARM64(rpiFeat)
	if rpiLevel != ARM64v8_0 {
		t.Errorf("expected RPi4 to evaluate to %s, got %s", ARM64v8_0, rpiLevel)
	}

	// Empty and malformed reader
	emptyFeat := parseLinuxCPUInfoARM64(strings.NewReader("random text without features line\n"))
	if emptyFeat.HasFP || emptyFeat.HasASIMD {
		t.Errorf("expected empty features from invalid cpuinfo, got %+v", emptyFeat)
	}

	// Line with flags prefix (e.g. QEMU or x86 emulation)
	flagsFeat := parseLinuxCPUInfoARM64(strings.NewReader("flags : fp neon lse sve\n"))
	if !flagsFeat.HasFP || !flagsFeat.HasASIMD || !flagsFeat.HasATOMICS || !flagsFeat.HasSVE {
		t.Errorf("flags prefix parsing failed: %+v", flagsFeat)
	}
}

func TestCurrentARM64FeaturesFallback(t *testing.T) {
	origFunc := readCPUInfoFunc
	defer func() { readCPUInfoFunc = origFunc }()

	// Test fallback returning true features
	readCPUInfoFunc = func() ARM64Features {
		return ARM64Features{HasFP: true, HasASIMD: true, HasATOMICS: true}
	}
	f := currentARM64Features()
	if !f.HasFP && !f.HasASIMD {
		t.Errorf("expected fallback to provide features, got %+v", f)
	}

	// Call default implementation directly
	_ = readLinuxCPUInfoARM64()
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

	hasF16C, hasLZCNT, hasMOVBE := parseLinuxCPUInfoX86Flags(strings.NewReader(mockSkylakeX))
	if !hasF16C || !hasLZCNT || !hasMOVBE {
		t.Errorf("expected true for f16c, lzcnt, movbe on mock fixture; got f16c=%v, lzcnt=%v, movbe=%v", hasF16C, hasLZCNT, hasMOVBE)
	}

	// Empty and malformed flags reader
	emptyF16C, emptyLZCNT, emptyMOVBE := parseLinuxCPUInfoX86Flags(strings.NewReader("random text without flags\n"))
	if emptyF16C || emptyLZCNT || emptyMOVBE {
		t.Errorf("expected false flags for invalid cpuinfo, got f16c=%v, lzcnt=%v, movbe=%v", emptyF16C, emptyLZCNT, emptyMOVBE)
	}

	// Test default reader implementations
	_ = readLinuxCPUInfoX86()
	_, _, _ = readLinuxCPUInfoX86Flags()
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

