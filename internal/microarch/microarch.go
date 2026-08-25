// Package microarch provides runtime CPU microarchitecture level detection,
// feature probing, and variant resolution compatible with Go's GOAMD64 and GOARM64
// architecture levels.
package microarch

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/sys/cpu"
)

// Standard error definitions for microarch operations.
var (
	ErrNoMatchingVariant = errors.New("no compatible microarchitecture variant found for host CPU")
	ErrEmptyVariants     = errors.New("available variants list is empty")
)

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
	ARM64v9_0 = "v9.0"
	ARM64v9_2 = "v9.2"
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
	rankARM64v9_0 = 90
	rankARM64v9_2 = 92
)

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
	HasAVX512F  bool
	HasAVX512BW bool
	HasAVX512CD bool
	HasAVX512DQ bool
	HasAVX512VL bool
}

// ARM64Features represents an inspectable set of arm64 CPU feature flags.
type ARM64Features struct {
	HasFP      bool
	HasASIMD   bool
	HasATOMICS bool
	HasCRC32   bool
	HasFPHP    bool
	HasASIMDHP bool
	HasJSCVT   bool
	HasFCMA    bool
	HasLRCPC   bool
	HasDCPOP   bool
	HasASIMDDP bool
	HasDIT     bool
	HasSVE     bool
	HasSVE2    bool
	HasI8MM    bool
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
	if len(availableLevels) == 0 {
		return "", ErrEmptyVariants
	}

	hostRank := Rank(arch, hostLevel)
	type candidate struct {
		original string
		norm     string
		rank     int
	}

	var candidates []candidate
	for _, lvl := range availableLevels {
		norm := Normalize(lvl)
		r := Rank(arch, norm)
		if r >= 0 && r <= hostRank {
			candidates = append(candidates, candidate{
				original: lvl,
				norm:     norm,
				rank:     r,
			})
		}
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("%w (host: %s %s, available: %v)", ErrNoMatchingVariant, arch, hostLevel, availableLevels)
	}

	// Sort descending by rank
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].rank > candidates[j].rank
	})

	return candidates[0].original, nil
}

// IsSupported checks if the specified microarchitecture level can run on the current host.
func IsSupported(level string) bool {
	info := Detect()
	reqRank := Rank(info.Arch, Normalize(level))
	hostRank := Rank(info.Arch, info.Level)
	return reqRank >= 0 && reqRank <= hostRank
}

// Normalize cleans up variant strings (e.g. "amd64_v3" -> "v3", "V3" -> "v3", "v8.0" -> "v8.0").
func Normalize(level string) string {
	l := strings.ToLower(strings.TrimSpace(level))
	l = strings.TrimPrefix(l, "linux_")
	l = strings.TrimPrefix(l, "darwin_")
	l = strings.TrimPrefix(l, "windows_")
	l = strings.TrimPrefix(l, "amd64_")
	l = strings.TrimPrefix(l, "arm64_")
	l = strings.TrimPrefix(l, "x86_64_")
	l = strings.TrimPrefix(l, "aarch64_")

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
		case ARM64v9_0:
			return rankARM64v9_0
		case ARM64v9_2:
			return rankARM64v9_2
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

	// v3: v2 + AVX, AVX2, BMI1, BMI2, FMA, OSXSAVE
	hasV3 := hasV2 && f.HasAVX && f.HasAVX2 && f.HasBMI1 && f.HasBMI2 && f.HasFMA && f.HasOSXSAVE
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
	if f.HasSVE2 && f.HasI8MM {
		return ARM64v9_2
	}
	if f.HasSVE {
		return ARM64v9_0
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
	return X86Features{
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
		HasAVX512F:  cpu.X86.HasAVX512F,
		HasAVX512BW: cpu.X86.HasAVX512BW,
		HasAVX512CD: cpu.X86.HasAVX512CD,
		HasAVX512DQ: cpu.X86.HasAVX512DQ,
		HasAVX512VL: cpu.X86.HasAVX512VL,
	}
}

func currentARM64Features() ARM64Features {
	return ARM64Features{
		HasFP:      cpu.ARM64.HasFP,
		HasASIMD:   cpu.ARM64.HasASIMD,
		HasATOMICS: cpu.ARM64.HasATOMICS,
		HasCRC32:   cpu.ARM64.HasCRC32,
		HasFPHP:    cpu.ARM64.HasFPHP,
		HasASIMDHP: cpu.ARM64.HasASIMDHP,
		HasJSCVT:   cpu.ARM64.HasJSCVT,
		HasFCMA:    cpu.ARM64.HasFCMA,
		HasLRCPC:   cpu.ARM64.HasLRCPC,
		HasDCPOP:   cpu.ARM64.HasDCPOP,
		HasASIMDDP: cpu.ARM64.HasASIMDDP,
		HasDIT:     cpu.ARM64.HasDIT,
		HasSVE:     cpu.ARM64.HasSVE,
		HasSVE2:    cpu.ARM64.HasSVE2,
		HasI8MM:    cpu.ARM64.HasI8MM,
	}
}

func extractX86FeatureList(f X86Features) []string {
	var list []string
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
	var list []string
	if f.HasFP {
		list = append(list, "fp")
	}
	if f.HasASIMD {
		list = append(list, "asimd")
	}
	if f.HasATOMICS {
		list = append(list, "atomics")
	}
	if f.HasCRC32 {
		list = append(list, "crc32")
	}
	if f.HasFPHP {
		list = append(list, "fphp")
	}
	if f.HasASIMDHP {
		list = append(list, "asimdhp")
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
	if f.HasASIMDDP {
		list = append(list, "asimddp")
	}
	if f.HasDIT {
		list = append(list, "dit")
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
	return list
}
