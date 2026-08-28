// Package microarch provides runtime CPU microarchitecture level detection,
// feature probing, and variant resolution compatible with Go's GOAMD64 and GOARM64
// architecture levels.
package microarch

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/sys/cpu"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

// Standard error definitions for microarch operations.
var (
	ErrNoMatchingVariant         = errors.New("no compatible microarchitecture variant found for host CPU")
	ErrEmptyVariants             = errors.New("available variants list is empty")
	ErrIncompatibleForcedVariant = errors.New("forced variant is incompatible with host CPU hardware")
	ErrVariantNotEmbedded        = errors.New("requested variant is not embedded in the fat binary")
)

// Standard policy preset names.
const (
	PolicyDefault     = "default"
	PolicySafeAVX512  = "safe_avx512"
	PolicyNoDownclock = "no_downclock"
)

// Policy defines CPU variant selection and dispatch rules.
type Policy struct {
	Name                      string   `json:"name,omitempty"`
	ForceLevel                string   `json:"force_level,omitempty"`
	MaxLevel                  string   `json:"max_level,omitempty"`
	DisabledVariants          []string `json:"disabled_variants,omitempty"`
	AVX512DownclockProtection bool     `json:"avx512_downclock_protection,omitempty"`
}

// PolicyResult contains the variant selection outcome along with telemetry metadata.
type PolicyResult struct {
	SelectedVariant string `json:"selected_variant"`
	PolicyApplied   string `json:"policy_applied,omitempty"`
	OverrideReason  string `json:"override_reason,omitempty"`
}

// Supported CPU architecture identifiers.
const (
	ArchAMD64 = "amd64"
	ArchARM64 = "arm64"
)

// AMD64 microarchitecture levels.
const (
	AMD64v1 = "v1"
	AMD64v2 = "v2"
	AMD64v3 = "v3"
	AMD64v4 = "v4"
)

// ARM64 microarchitecture levels.
const (
	ARM64v8_0 = "v8.0"
	ARM64v8_1 = "v8.1"
	ARM64v8_2 = "v8.2"
	ARM64v8_3 = "v8.3"
	ARM64v8_4 = "v8.4"
	ARM64v8_5 = "v8.5"
	ARM64v8_6 = "v8.6"
	ARM64v8_7 = "v8.7"
	ARM64v9_0 = "v9.0"
	ARM64v9_1 = "v9.1"
	ARM64v9_2 = "v9.2"
	ARM64v9_3 = "v9.3"
	ARM64v9_4 = "v9.4"
	ARM64v9_5 = "v9.5"
)

// Internal ranks for level comparisons.
const (
	rankUnknown  = -1
	rankBaseline = 1

	rankAMD64v1 = 1
	rankAMD64v2 = 2
	rankAMD64v3 = 3
	rankAMD64v4 = 4

	rankARM64v8_0 = 80
	rankARM64v8_1 = 81
	rankARM64v8_2 = 82
	rankARM64v8_3 = 83
	rankARM64v8_4 = 84
	rankARM64v8_5 = 85
	rankARM64v8_6 = 86
	rankARM64v8_7 = 87
	rankARM64v9_0 = 90
	rankARM64v9_1 = 91
	rankARM64v9_2 = 92
	rankARM64v9_3 = 93
	rankARM64v9_4 = 94
	rankARM64v9_5 = 95

	maxX86Features   = 20
	maxARM64Features = 24

	cpuInfoSplitParts = 2

	cpuidBasicLeafInfo     = 0x0
	cpuidBasicLeafFeatures = 0x1
	cpuidExtLeafInfo       = 0x80000000
	cpuidExtLeafFeatures   = 0x80000001

	cpuidLeaf1ECXMOVBEBit  = 22
	cpuidLeaf1ECXF16CBit   = 29
	cpuidLeafExt1ECXABMBit = 5
)

var readCPUInfoFunc = readLinuxCPUInfoARM64

// Info describes the host's operating system, architecture, highest microarchitecture
// level, and detected CPU instruction feature flags.
type Info struct {
	OS       string   `json:"os"`
	Arch     string   `json:"arch"`
	Level    string   `json:"level"`
	Features []string `json:"features"`
}

// X86Features represents an inspectable set of x86/amd64 CPU feature flags.
type X86Features struct {
	HasCX16     bool
	HasPOPCNT   bool
	HasSSE3     bool
	HasSSSE3    bool
	HasSSE41    bool
	HasSSE42    bool
	HasAVX      bool
	HasAVX2     bool
	HasBMI1     bool
	HasBMI2     bool
	HasFMA      bool
	HasOSXSAVE  bool
	HasF16C     bool
	HasLZCNT    bool
	HasMOVBE    bool
	HasAVX512F  bool
	HasAVX512BW bool
	HasAVX512CD bool
	HasAVX512DQ bool
	HasAVX512VL bool
}

// ARM64Features represents an inspectable set of arm64 CPU feature flags.
type ARM64Features struct {
	HasFP       bool
	HasASIMD    bool
	HasATOMICS  bool
	HasCRC32    bool
	HasFPHP     bool
	HasASIMDHP  bool
	HasJSCVT    bool
	HasFCMA     bool
	HasLRCPC    bool
	HasDCPOP    bool
	HasASIMDDP  bool
	HasDIT      bool
	HasSVE      bool
	HasSVE2     bool
	HasI8MM     bool
	HasBF16     bool
	HasSME      bool
	HasSME2     bool
	HasAES      bool
	HasPMULL    bool
	HasSHA1     bool
	HasSHA2     bool
	HasSHA3     bool
	HasSHA512   bool
	HasSM3      bool
	HasSM4      bool
	HasASIMDFHM bool
	HasASIMDRDM bool
}

// Detect inspects the current host CPU and returns detailed Info.
func Detect() Info {
	return detectForArch(runtime.GOOS, runtime.GOARCH, currentX86Features(), currentARM64Features())
}

func detectForArch(goos, goarch string, x86Feat X86Features, armFeat ARM64Features) Info {
	info := Info{
		OS:   goos,
		Arch: goarch,
	}

	switch goarch {
	case ArchAMD64:
		info.Level = EvaluateAMD64(x86Feat)
		info.Features = extractX86FeatureList(x86Feat)
	case ArchARM64:
		info.Level = EvaluateARM64(armFeat)
		info.Features = extractARM64FeatureList(armFeat)
	default:
		info.Level = "v1"
	}

	return info
}

// CurrentLevel returns the host's highest supported Go microarchitecture level string
// (e.g., "v3" for AMD64 or "v8.2" for ARM64).
func CurrentLevel() string {
	return Detect().Level
}

// ReadPolicyFromEnv reads policy settings from ambient MICROFAT_* environment variables.
func ReadPolicyFromEnv() Policy {
	var p Policy
	p.ForceLevel = strings.TrimSpace(os.Getenv(format.EnvForceLevel))
	p.MaxLevel = strings.TrimSpace(os.Getenv(format.EnvMaxLevel))

	if dis := strings.TrimSpace(os.Getenv(format.EnvDisableVariants)); dis != "" {
		parts := strings.Split(dis, ",")
		p.DisabledVariants = make([]string, 0, len(parts))
		for _, v := range parts {
			v = strings.TrimSpace(v)
			if v != "" {
				p.DisabledVariants = append(p.DisabledVariants, v)
			}
		}
	}

	p.Name = strings.ToLower(strings.TrimSpace(os.Getenv(format.EnvPolicy)))

	avxVal := strings.ToLower(strings.TrimSpace(os.Getenv(format.EnvAVX512DownclockProtection)))
	if avxVal == "1" || avxVal == "true" || avxVal == "yes" || avxVal == "on" ||
		p.Name == PolicySafeAVX512 || p.Name == PolicyNoDownclock {
		p.AVX512DownclockProtection = true
	}

	return p
}

// BestMatchingVariant selects the highest microarchitecture variant from availableLevels
// that the current host CPU can execute. Returns an error if no compatible variant exists.
func BestMatchingVariant(availableLevels []string) (string, error) {
	if len(availableLevels) == 0 {
		return "", ErrEmptyVariants
	}

	info := Detect()
	return BestMatchingVariantFor(info.Arch, info.Level, availableLevels)
}

// BestMatchingVariantFor evaluates availableLevels against a target arch and host level.
func BestMatchingVariantFor(arch, hostLevel string, availableLevels []string) (string, error) {
	res, err := SelectVariantWithPolicy(arch, hostLevel, availableLevels, Policy{})
	if err != nil {
		return "", err
	}
	return res.SelectedVariant, nil
}

// SelectVariantWithPolicy selects the optimal variant from availableLevels considering host CPU capabilities,
// explicit overrides, max level caps, denylisted variants, and CPU downclocking protection rules.
func SelectVariantWithPolicy(arch, hostLevel string, availableLevels []string, policy Policy) (PolicyResult, error) {
	if len(availableLevels) == 0 {
		return PolicyResult{}, ErrEmptyVariants
	}

	hostRank := Rank(arch, hostLevel)

	if policy.ForceLevel != "" {
		return resolveForcedVariant(arch, hostRank, hostLevel, availableLevels, policy.ForceLevel)
	}

	if hostRank < 0 {
		return PolicyResult{}, fmt.Errorf("unknown host level %q for target architecture %s", hostLevel, arch)
	}

	effectiveMaxRank, policyApplied, overrideReason := computeEffectiveMaxRank(arch, hostRank, policy)
	disabledSet := buildDisabledSet(policy.DisabledVariants)
	if len(disabledSet) > 0 && policyApplied == "" {
		policyApplied = "disable_variants"
		overrideReason = fmt.Sprintf("MICROFAT_DISABLE_VARIANTS=%s", strings.Join(policy.DisabledVariants, ","))
	}

	candidates := filterAndRankCandidates(arch, hostRank, effectiveMaxRank, availableLevels, disabledSet)
	if len(candidates) == 0 {
		return PolicyResult{}, fmt.Errorf("%w (host: %s %s, available: %v)", ErrNoMatchingVariant, arch, hostLevel, availableLevels)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].rank > candidates[j].rank
	})

	return PolicyResult{
		SelectedVariant: candidates[0].original,
		PolicyApplied:   policyApplied,
		OverrideReason:  overrideReason,
	}, nil
}

func resolveForcedVariant(arch string, hostRank int, hostLevel string, availableLevels []string, forceLevel string) (PolicyResult, error) {
	normForced := Normalize(forceLevel)
	forcedRank := Rank(arch, normForced)
	if forcedRank < 0 {
		return PolicyResult{}, fmt.Errorf("%w: unknown variant level %q", ErrIncompatibleForcedVariant, forceLevel)
	}
	if hostRank >= 0 && forcedRank > hostRank {
		return PolicyResult{}, fmt.Errorf("%w: forced variant %s (rank %d) exceeds host CPU %s %s (rank %d)",
			ErrIncompatibleForcedVariant, normForced, forcedRank, arch, hostLevel, hostRank)
	}

	for _, lvl := range availableLevels {
		if Normalize(lvl) == normForced {
			return PolicyResult{
				SelectedVariant: lvl,
				PolicyApplied:   "force_level",
				OverrideReason:  fmt.Sprintf("MICROFAT_FORCE_LEVEL=%s", forceLevel),
			}, nil
		}
	}
	return PolicyResult{}, fmt.Errorf("%w: %s (available: %v)", ErrVariantNotEmbedded, forceLevel, availableLevels)
}

func computeEffectiveMaxRank(arch string, hostRank int, policy Policy) (int, string, string) {
	effectiveMaxRank := hostRank
	var policyApplied string
	var overrideReason string

	if policy.MaxLevel != "" {
		maxRank := Rank(arch, Normalize(policy.MaxLevel))
		if maxRank >= 0 && maxRank < effectiveMaxRank {
			effectiveMaxRank = maxRank
			policyApplied = "max_level"
			overrideReason = fmt.Sprintf("MICROFAT_MAX_LEVEL=%s", policy.MaxLevel)
		}
	}

	isSafeAVX512 := policy.AVX512DownclockProtection || policy.Name == PolicySafeAVX512 || policy.Name == PolicyNoDownclock
	if isSafeAVX512 && strings.ToLower(arch) == ArchAMD64 && hostRank >= rankAMD64v4 && isSkylakeXOrCascadeLakeFunc() {
		const v3Rank = rankAMD64v3
		if v3Rank < effectiveMaxRank {
			effectiveMaxRank = v3Rank
			policyApplied = "avx512_downclock_protection"
			overrideReason = "Intel Skylake-X/Cascade Lake AVX-512 downclocking protection applied (capped at v3)"
		}
	}

	return effectiveMaxRank, policyApplied, overrideReason
}

func buildDisabledSet(disabled []string) map[string]struct{} {
	disabledSet := make(map[string]struct{}, len(disabled))
	for _, d := range disabled {
		disabledSet[Normalize(d)] = struct{}{}
	}
	return disabledSet
}

type variantCandidate struct {
	original string
	norm     string
	rank     int
}

func filterAndRankCandidates(
	arch string,
	hostRank int,
	effectiveMaxRank int,
	availableLevels []string,
	disabledSet map[string]struct{},
) []variantCandidate {
	candidates := make([]variantCandidate, 0, len(availableLevels))
	for _, lvl := range availableLevels {
		norm := Normalize(lvl)
		r := Rank(arch, norm)
		if r < 0 || r > hostRank || r > effectiveMaxRank {
			continue
		}
		if _, disabled := disabledSet[norm]; disabled {
			continue
		}
		candidates = append(candidates, variantCandidate{
			original: lvl,
			norm:     norm,
			rank:     r,
		})
	}
	return candidates
}

// IsSupported checks if the specified microarchitecture level can run on the current host.
func IsSupported(level string) bool {
	info := Detect()
	reqRank := Rank(info.Arch, Normalize(level))
	hostRank := Rank(info.Arch, info.Level)
	return reqRank >= 0 && reqRank <= hostRank
}

// Normalize cleans up variant strings (e.g. "amd64_v3" -> "v3", "V3" -> "v3", "v8.0" -> "v8.0", "arm64-v8.2" -> "v8.2").
func Normalize(level string) string {
	l := strings.ToLower(strings.TrimSpace(level))
	l = strings.TrimPrefix(l, "linux_")
	l = strings.TrimPrefix(l, "darwin_")
	l = strings.TrimPrefix(l, "windows_")
	l = strings.TrimPrefix(l, "amd64_")
	l = strings.TrimPrefix(l, "arm64_")
	l = strings.TrimPrefix(l, "x86_64_")
	l = strings.TrimPrefix(l, "aarch64_")
	l = strings.TrimPrefix(l, "arm64-")
	l = strings.TrimPrefix(l, "aarch64-")

	if l == "" {
		return "v1"
	}
	if !strings.HasPrefix(l, "v") {
		l = "v" + l
	}
	return l
}

// Rank maps a normalized level string to an integer rank for comparison.
// Returns -1 if the level is unknown.
func Rank(arch, level string) int {
	norm := Normalize(level)
	switch strings.ToLower(arch) {
	case ArchAMD64, "x86_64", "x86-64":
		switch norm {
		case AMD64v1:
			return rankAMD64v1
		case AMD64v2:
			return rankAMD64v2
		case AMD64v3:
			return rankAMD64v3
		case AMD64v4:
			return rankAMD64v4
		default:
			return rankUnknown
		}
	case ArchARM64, "aarch64":
		switch norm {
		case ARM64v8_0:
			return rankARM64v8_0
		case ARM64v8_1:
			return rankARM64v8_1
		case ARM64v8_2:
			return rankARM64v8_2
		case ARM64v8_3:
			return rankARM64v8_3
		case ARM64v8_4:
			return rankARM64v8_4
		case ARM64v8_5:
			return rankARM64v8_5
		case ARM64v8_6:
			return rankARM64v8_6
		case ARM64v8_7:
			return rankARM64v8_7
		case ARM64v9_0:
			return rankARM64v9_0
		case ARM64v9_1:
			return rankARM64v9_1
		case ARM64v9_2:
			return rankARM64v9_2
		case ARM64v9_3:
			return rankARM64v9_3
		case ARM64v9_4:
			return rankARM64v9_4
		case ARM64v9_5:
			return rankARM64v9_5
		default:
			return rankUnknown
		}
	default:
		if norm == "v1" {
			return rankBaseline
		}
		return rankUnknown
	}
}

// Compare compares two microarchitecture levels for the given architecture.
// Returns 1 if a > b, -1 if a < b, 0 if equal.
func Compare(arch, a, b string) int {
	ra := Rank(arch, a)
	rb := Rank(arch, b)
	if ra > rb {
		return 1
	}
	if ra < rb {
		return -1
	}
	return 0
}

// EvaluateAMD64 computes the highest AMD64 level supported by the given feature set.
func EvaluateAMD64(f X86Features) string {
	// v2: CMPXCHG16B, POPCNT, SSE3, SSSE3, SSE4.1, SSE4.2
	hasV2 := f.HasCX16 && f.HasPOPCNT && f.HasSSE3 && f.HasSSSE3 && f.HasSSE41 && f.HasSSE42
	if !hasV2 {
		return AMD64v1
	}

	// v3: v2 + AVX, AVX2, BMI1, BMI2, FMA, OSXSAVE, F16C, LZCNT, MOVBE
	hasV3 := hasV2 && f.HasAVX && f.HasAVX2 && f.HasBMI1 && f.HasBMI2 && f.HasFMA && f.HasOSXSAVE &&
		f.HasF16C && f.HasLZCNT && f.HasMOVBE
	if !hasV3 {
		return AMD64v2
	}

	// v4: v3 + AVX512F, AVX512BW, AVX512CD, AVX512DQ, AVX512VL
	hasV4 := hasV3 && f.HasAVX512F && f.HasAVX512BW && f.HasAVX512CD && f.HasAVX512DQ && f.HasAVX512VL
	if !hasV4 {
		return AMD64v3
	}

	return AMD64v4
}

// EvaluateARM64 computes the highest ARM64 level supported by the given feature set.
func EvaluateARM64(f ARM64Features) string {
	if f.HasSME2 || (f.HasSME && f.HasSVE2) {
		return ARM64v9_5
	}
	if f.HasSME {
		return ARM64v9_3
	}
	if f.HasSVE2 && f.HasI8MM {
		return ARM64v9_2
	}
	if f.HasSVE2 {
		return ARM64v9_1
	}
	if f.HasSVE {
		return ARM64v9_0
	}
	if f.HasI8MM || f.HasBF16 {
		return ARM64v8_6
	}
	if f.HasDIT && f.HasDCPOP {
		return ARM64v8_5
	}
	if f.HasDCPOP && f.HasASIMDDP {
		return ARM64v8_4
	}
	if f.HasJSCVT && f.HasFCMA && f.HasLRCPC {
		return ARM64v8_3
	}
	if f.HasFPHP && f.HasASIMDHP {
		return ARM64v8_2
	}
	if f.HasATOMICS && f.HasCRC32 {
		return ARM64v8_1
	}
	return ARM64v8_0
}

func currentX86Features() X86Features {
	hasF16C, hasLZCNT, hasMOVBE := probeX86ExtraFeaturesFunc()

	feat := X86Features{
		HasCX16:     cpu.X86.HasCX16,
		HasPOPCNT:   cpu.X86.HasPOPCNT,
		HasSSE3:     cpu.X86.HasSSE3,
		HasSSSE3:    cpu.X86.HasSSSE3,
		HasSSE41:    cpu.X86.HasSSE41,
		HasSSE42:    cpu.X86.HasSSE42,
		HasAVX:      cpu.X86.HasAVX,
		HasAVX2:     cpu.X86.HasAVX2,
		HasBMI1:     cpu.X86.HasBMI1,
		HasBMI2:     cpu.X86.HasBMI2,
		HasFMA:      cpu.X86.HasFMA,
		HasOSXSAVE:  cpu.X86.HasOSXSAVE,
		HasF16C:     hasF16C,
		HasLZCNT:    hasLZCNT,
		HasMOVBE:    hasMOVBE,
		HasAVX512F:  cpu.X86.HasAVX512F,
		HasAVX512BW: cpu.X86.HasAVX512BW,
		HasAVX512CD: cpu.X86.HasAVX512CD,
		HasAVX512DQ: cpu.X86.HasAVX512DQ,
		HasAVX512VL: cpu.X86.HasAVX512VL,
	}

	// Fallback to /proc/cpuinfo on Linux if CPUID returned false for extra features
	if runtime.GOOS == "linux" && (!feat.HasF16C || !feat.HasLZCNT || !feat.HasMOVBE) && readCPUInfoX86FlagsFunc != nil {
		f16c, lzcnt, movbe := readCPUInfoX86FlagsFunc()
		if f16c {
			feat.HasF16C = true
		}
		if lzcnt {
			feat.HasLZCNT = true
		}
		if movbe {
			feat.HasMOVBE = true
		}
	}

	return feat
}

func currentARM64Features() ARM64Features {
	feat := ARM64Features{
		HasFP:       cpu.ARM64.HasFP,
		HasASIMD:    cpu.ARM64.HasASIMD,
		HasATOMICS:  cpu.ARM64.HasATOMICS,
		HasCRC32:    cpu.ARM64.HasCRC32,
		HasFPHP:     cpu.ARM64.HasFPHP,
		HasASIMDHP:  cpu.ARM64.HasASIMDHP,
		HasJSCVT:    cpu.ARM64.HasJSCVT,
		HasFCMA:     cpu.ARM64.HasFCMA,
		HasLRCPC:    cpu.ARM64.HasLRCPC,
		HasDCPOP:    cpu.ARM64.HasDCPOP,
		HasASIMDDP:  cpu.ARM64.HasASIMDDP,
		HasDIT:      cpu.ARM64.HasDIT,
		HasSVE:      cpu.ARM64.HasSVE,
		HasSVE2:     cpu.ARM64.HasSVE2,
		HasI8MM:     cpu.ARM64.HasI8MM,
		HasAES:      cpu.ARM64.HasAES,
		HasPMULL:    cpu.ARM64.HasPMULL,
		HasSHA1:     cpu.ARM64.HasSHA1,
		HasSHA2:     cpu.ARM64.HasSHA2,
		HasSHA3:     cpu.ARM64.HasSHA3,
		HasSHA512:   cpu.ARM64.HasSHA512,
		HasSM3:      cpu.ARM64.HasSM3,
		HasSM4:      cpu.ARM64.HasSM4,
		HasASIMDFHM: cpu.ARM64.HasASIMDFHM,
		HasASIMDRDM: cpu.ARM64.HasASIMDRDM,
	}

	// Fallback to /proc/cpuinfo on Linux if auxv returned zero features
	if runtime.GOOS == "linux" && !feat.HasFP && !feat.HasASIMD && readCPUInfoFunc != nil {
		fallback := readCPUInfoFunc()
		if fallback.HasFP || fallback.HasASIMD {
			return fallback
		}
	}

	return feat
}

type x86CPUModelInfo struct {
	Vendor   string
	Family   int
	Model    int
	Stepping int
}

var (
	readCPUInfoX86Func          = readLinuxCPUInfoX86
	readCPUInfoX86FlagsFunc     = readLinuxCPUInfoX86Flags
	probeX86ExtraFeaturesFunc   = probeX86ExtraFeatures
	isSkylakeXOrCascadeLakeFunc = isHostSkylakeXOrCascadeLake
)

func probeX86ExtraFeatures() (hasF16C, hasLZCNT, hasMOVBE bool) {
	if runtime.GOARCH != ArchAMD64 {
		return false, false, false
	}

	maxBasic, _, _, _ := cpuid(cpuidBasicLeafInfo, 0)
	if maxBasic >= cpuidBasicLeafFeatures {
		_, _, ecx, _ := cpuid(cpuidBasicLeafFeatures, 0)
		hasMOVBE = (ecx & (1 << cpuidLeaf1ECXMOVBEBit)) != 0
		hasF16C = (ecx & (1 << cpuidLeaf1ECXF16CBit)) != 0
	}

	maxExt, _, _, _ := cpuid(cpuidExtLeafInfo, 0)
	if maxExt >= cpuidExtLeafFeatures {
		_, _, ecx, _ := cpuid(cpuidExtLeafFeatures, 0)
		hasLZCNT = (ecx & (1 << cpuidLeafExt1ECXABMBit)) != 0
	}

	return hasF16C, hasLZCNT, hasMOVBE
}

func readLinuxCPUInfoX86Flags() (hasF16C, hasLZCNT, hasMOVBE bool) {
	// #nosec G304 -- reading linux system cpuinfo
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return false, false, false
	}
	defer func() { _ = f.Close() }()
	return parseLinuxCPUInfoX86Flags(f)
}

func parseLinuxCPUInfoX86Flags(r io.Reader) (hasF16C, hasLZCNT, hasMOVBE bool) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "flags") && !strings.HasPrefix(line, "Features") {
			continue
		}
		parts := strings.SplitN(line, ":", cpuInfoSplitParts)
		if len(parts) < cpuInfoSplitParts {
			continue
		}
		tokens := strings.Fields(parts[1])
		for _, token := range tokens {
			switch strings.ToLower(token) {
			case "f16c":
				hasF16C = true
			case "abm", "lzcnt":
				hasLZCNT = true
			case "movbe":
				hasMOVBE = true
			}
		}
	}
	return hasF16C, hasLZCNT, hasMOVBE
}

// IsAVX512DownclockingRisk reports whether the host CPU is an AMD64 processor subject to
// AVX-512 frequency downclocking (such as Intel Skylake-X or Cascade Lake Xeon).
func IsAVX512DownclockingRisk() bool {
	return isSkylakeXOrCascadeLakeFunc()
}

func isHostSkylakeXOrCascadeLake() bool {
	if runtime.GOARCH != ArchAMD64 {
		return false
	}
	info := readCPUInfoX86Func()
	// Intel Family 6, Model 85 (0x55) is Skylake-X / Cascade Lake / Cooper Lake Xeon
	const (
		intelFamily6    = 6
		skylakeXModel85 = 85
	)
	return (info.Vendor == "GenuineIntel" || info.Vendor == "") && info.Family == intelFamily6 && info.Model == skylakeXModel85
}

func readLinuxCPUInfoX86() x86CPUModelInfo {
	// #nosec G304 -- reading linux system cpuinfo
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return x86CPUModelInfo{}
	}
	defer func() { _ = f.Close() }()
	return parseLinuxCPUInfoX86(f)
}

func parseLinuxCPUInfoX86(r io.Reader) x86CPUModelInfo {
	var info x86CPUModelInfo
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		parts := strings.SplitN(line, ":", cpuInfoSplitParts)
		if len(parts) < cpuInfoSplitParts {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])
		switch key {
		case "vendor_id":
			if info.Vendor == "" {
				info.Vendor = val
			}
		case "cpu family":
			if info.Family == 0 {
				var fam int
				if _, err := fmt.Sscanf(val, "%d", &fam); err == nil {
					info.Family = fam
				}
			}
		case "model":
			if info.Model == 0 {
				var mod int
				if _, err := fmt.Sscanf(val, "%d", &mod); err == nil {
					info.Model = mod
				}
			}
		case "stepping":
			if info.Stepping == 0 {
				var step int
				if _, err := fmt.Sscanf(val, "%d", &step); err == nil {
					info.Stepping = step
				}
			}
		}
		if info.Vendor != "" && info.Family != 0 && info.Model != 0 && info.Stepping != 0 {
			break
		}
	}
	return info
}

func readLinuxCPUInfoARM64() ARM64Features {
	// #nosec G304 -- reading linux system cpuinfo
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ARM64Features{}
	}
	defer func() { _ = f.Close() }()
	return parseLinuxCPUInfoARM64(f)
}

func parseLinuxCPUInfoARM64(r io.Reader) ARM64Features {
	var feat ARM64Features
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "Features") && !strings.HasPrefix(line, "flags") {
			continue
		}
		parts := strings.SplitN(line, ":", cpuInfoSplitParts)
		if len(parts) < cpuInfoSplitParts {
			continue
		}
		tokens := strings.Fields(parts[1])
		for _, token := range tokens {
			applyCPUInfoToken(&feat, strings.ToLower(token))
		}
	}
	return feat
}

func applyCPUInfoToken(feat *ARM64Features, token string) {
	if applySIMDAndVectorTokens(feat, token) {
		return
	}
	if applyCryptoTokens(feat, token) {
		return
	}
	applyCoreAndMemoryTokens(feat, token)
}

func applySIMDAndVectorTokens(feat *ARM64Features, token string) bool {
	switch token {
	case "fp":
		feat.HasFP = true
	case "asimd", "neon":
		feat.HasASIMD = true
	case "fphp":
		feat.HasFPHP = true
	case "asimdhp":
		feat.HasASIMDHP = true
	case "asimddp", "dotprod":
		feat.HasASIMDDP = true
	case "sve":
		feat.HasSVE = true
	case "sve2":
		feat.HasSVE2 = true
	case "i8mm", "svei8mm":
		feat.HasI8MM = true
	case "bf16", "svebf16":
		feat.HasBF16 = true
	case "sme":
		feat.HasSME = true
	case "sme2":
		feat.HasSME2 = true
	case "asimdfhm":
		feat.HasASIMDFHM = true
	case "asimdrdm":
		feat.HasASIMDRDM = true
	default:
		return false
	}
	return true
}

func applyCryptoTokens(feat *ARM64Features, token string) bool {
	switch token {
	case "aes":
		feat.HasAES = true
	case "pmull":
		feat.HasPMULL = true
	case "sha1":
		feat.HasSHA1 = true
	case "sha2", "sha256":
		feat.HasSHA2 = true
	case "sha3":
		feat.HasSHA3 = true
	case "sha512":
		feat.HasSHA512 = true
	case "sm3":
		feat.HasSM3 = true
	case "sm4":
		feat.HasSM4 = true
	default:
		return false
	}
	return true
}

func applyCoreAndMemoryTokens(feat *ARM64Features, token string) {
	switch token {
	case "atomics", "lse":
		feat.HasATOMICS = true
	case "crc32":
		feat.HasCRC32 = true
	case "jscvt":
		feat.HasJSCVT = true
	case "fcma":
		feat.HasFCMA = true
	case "lrcpc":
		feat.HasLRCPC = true
	case "dcpop":
		feat.HasDCPOP = true
	case "dit":
		feat.HasDIT = true
	}
}

func extractX86FeatureList(f X86Features) []string {
	list := make([]string, 0, maxX86Features)
	if f.HasCX16 {
		list = append(list, "cx16")
	}
	if f.HasPOPCNT {
		list = append(list, "popcnt")
	}
	if f.HasSSE3 {
		list = append(list, "sse3")
	}
	if f.HasSSSE3 {
		list = append(list, "ssse3")
	}
	if f.HasSSE41 {
		list = append(list, "sse4.1")
	}
	if f.HasSSE42 {
		list = append(list, "sse4.2")
	}
	if f.HasAVX {
		list = append(list, "avx")
	}
	if f.HasAVX2 {
		list = append(list, "avx2")
	}
	if f.HasBMI1 {
		list = append(list, "bmi1")
	}
	if f.HasBMI2 {
		list = append(list, "bmi2")
	}
	if f.HasFMA {
		list = append(list, "fma")
	}
	if f.HasOSXSAVE {
		list = append(list, "osxsave")
	}
	if f.HasF16C {
		list = append(list, "f16c")
	}
	if f.HasLZCNT {
		list = append(list, "lzcnt")
	}
	if f.HasMOVBE {
		list = append(list, "movbe")
	}
	if f.HasAVX512F {
		list = append(list, "avx512f")
	}
	if f.HasAVX512BW {
		list = append(list, "avx512bw")
	}
	if f.HasAVX512CD {
		list = append(list, "avx512cd")
	}
	if f.HasAVX512DQ {
		list = append(list, "avx512dq")
	}
	if f.HasAVX512VL {
		list = append(list, "avx512vl")
	}
	return list
}

func extractARM64FeatureList(f ARM64Features) []string {
	list := make([]string, 0, maxARM64Features)
	list = appendSIMDFeatureList(list, f)
	list = appendCryptoFeatureList(list, f)
	list = appendCoreFeatureList(list, f)
	return list
}

func appendSIMDFeatureList(list []string, f ARM64Features) []string {
	if f.HasFP {
		list = append(list, "fp")
	}
	if f.HasASIMD {
		list = append(list, "asimd")
	}
	if f.HasFPHP {
		list = append(list, "fphp")
	}
	if f.HasASIMDHP {
		list = append(list, "asimdhp")
	}
	if f.HasASIMDDP {
		list = append(list, "asimddp")
	}
	if f.HasSVE {
		list = append(list, "sve")
	}
	if f.HasSVE2 {
		list = append(list, "sve2")
	}
	if f.HasI8MM {
		list = append(list, "i8mm")
	}
	if f.HasBF16 {
		list = append(list, "bf16")
	}
	if f.HasSME {
		list = append(list, "sme")
	}
	if f.HasSME2 {
		list = append(list, "sme2")
	}
	if f.HasASIMDFHM {
		list = append(list, "asimdfhm")
	}
	if f.HasASIMDRDM {
		list = append(list, "asimdrdm")
	}
	return list
}

func appendCryptoFeatureList(list []string, f ARM64Features) []string {
	if f.HasAES {
		list = append(list, "aes")
	}
	if f.HasPMULL {
		list = append(list, "pmull")
	}
	if f.HasSHA1 {
		list = append(list, "sha1")
	}
	if f.HasSHA2 {
		list = append(list, "sha2")
	}
	if f.HasSHA3 {
		list = append(list, "sha3")
	}
	if f.HasSHA512 {
		list = append(list, "sha512")
	}
	if f.HasSM3 {
		list = append(list, "sm3")
	}
	if f.HasSM4 {
		list = append(list, "sm4")
	}
	return list
}

func appendCoreFeatureList(list []string, f ARM64Features) []string {
	if f.HasATOMICS {
		list = append(list, "atomics")
	}
	if f.HasCRC32 {
		list = append(list, "crc32")
	}
	if f.HasJSCVT {
		list = append(list, "jscvt")
	}
	if f.HasFCMA {
		list = append(list, "fcma")
	}
	if f.HasLRCPC {
		list = append(list, "lrcpc")
	}
	if f.HasDCPOP {
		list = append(list, "dcpop")
	}
	if f.HasDIT {
		list = append(list, "dit")
	}
	return list
}
