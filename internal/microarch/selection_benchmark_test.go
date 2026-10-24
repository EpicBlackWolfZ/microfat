package microarch

import (
	"fmt"
	"math/rand"
	"runtime"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

const benchmarkUnknownLevel = "v999"

// The experiment builds exact requirement sets once, but reconstructs the host
// feature mask, available mask and compatible input on every selector call. In
// particular, ARM64 v9.0 does not imply the numerically lower v8.8 or v8.9 bits.
// This is deliberately test-only until the recorded adoption gates are met.
type benchmarkLevelRequirement struct {
	bit      uint64
	required uint64
}

type benchmarkRequirementMasks struct {
	features map[string]uint64
	levels   []benchmarkLevelRequirement
	bits     map[string]uint64
}

var benchmarkExactMasks = buildBenchmarkRequirementMasks()

func buildBenchmarkRequirementMasks() benchmarkRequirementMasks {
	masks := benchmarkRequirementMasks{
		features: make(map[string]uint64),
		levels:   make([]benchmarkLevelRequirement, 0, len(arm64LevelRequirements)),
		bits:     make(map[string]uint64, len(arm64LevelRequirements)),
	}
	for _, requirement := range arm64LevelRequirements {
		for _, feature := range requirement.RequiredFeatures {
			if _, exists := masks.features[feature]; !exists {
				masks.features[feature] = uint64(1) << len(masks.features)
			}
		}
	}
	for index, requirement := range arm64LevelRequirements {
		features := make(map[string]bool)
		collectARM64Requirements(requirement.Level, features)
		var required uint64
		for feature := range features {
			required |= masks.features[feature]
		}
		bit := uint64(1) << index
		masks.levels = append(masks.levels, benchmarkLevelRequirement{
			bit: bit, required: required,
		})
		masks.bits[requirement.Level] = bit
	}
	return masks
}

func benchmarkSelectVariantExactMask(arch string, host Info, available []string, policy Policy) (PolicyResult, error) {
	if canonicalArch(arch) != canonicalArch(host.Arch) {
		return PolicyResult{}, fmt.Errorf("%w: host %s cannot execute %s", ErrNoMatchingVariant, host.Arch, arch)
	}
	if Rank(arch, host.Level) < 0 {
		return PolicyResult{}, fmt.Errorf("%w: unknown host level %q", ErrNoMatchingVariant, host.Level)
	}

	hostMask := benchmarkHostCompatibilityMask(host)
	var availableMask uint64
	for _, level := range available {
		availableMask |= benchmarkLevelBit(host.Arch, level)
	}
	permittedMask := hostMask & availableMask
	compatible := make([]string, 0, len(available))
	for _, level := range available {
		if permittedMask&benchmarkLevelBit(host.Arch, level) != 0 {
			compatible = append(compatible, level)
		}
	}
	if policy.ForceLevel != "" && hostMask&benchmarkLevelBit(host.Arch, policy.ForceLevel) == 0 {
		return PolicyResult{}, ErrIncompatibleForcedVariant
	}
	if len(available) > 0 && len(compatible) == 0 {
		return PolicyResult{}, ErrNoMatchingVariant
	}
	return selectCompatibleVariant(arch, host.Level, compatible, policy)
}

func benchmarkHostCompatibilityMask(host Info) uint64 {
	var compatible uint64
	if canonicalArch(host.Arch) == ArchARM64 {
		var features uint64
		for _, feature := range host.Features {
			features |= benchmarkExactMasks.features[feature]
		}
		for _, requirement := range benchmarkExactMasks.levels {
			if features&requirement.required == requirement.required {
				compatible |= requirement.bit
			}
		}
		return compatible
	}
	// AMD64's existing contract is a linear hierarchy; unrelated architectures
	// have only their v1 baseline. ARM64 always uses the exact sets above.
	return (uint64(1)<<Rank(host.Arch, host.Level) - 1) << rankBaseline
}

func benchmarkLevelBit(arch, level string) uint64 {
	if canonicalArch(arch) == ArchARM64 {
		return benchmarkExactMasks.bits[Normalize(level)]
	}
	rank := Rank(arch, level)
	if rank < 0 {
		return 0
	}
	return uint64(1) << rank
}

type benchmarkSelectionFixture struct {
	name      string
	arch      string
	host      Info
	available []string
	policy    Policy
	want      string
	wantErr   error
}

func benchmarkSelectionFixtures() []benchmarkSelectionFixture {
	amd64Host := Info{Arch: ArchAMD64, Level: AMD64v4}
	amd64Dense := []string{AMD64v1, AMD64v2, AMD64v3, AMD64v4}
	arm64Dense := []string{
		ARM64v8_0, ARM64v8_1, ARM64v8_2, ARM64v8_3, ARM64v8_4, ARM64v8_5, ARM64v8_6, ARM64v8_7,
		ARM64v8_8, ARM64v8_9, ARM64v9_0, ARM64v9_1, ARM64v9_2, ARM64v9_3, ARM64v9_4, ARM64v9_5,
	}
	arm64v90 := benchmarkARMHost(ARM64v9_0)
	arm64Branch := []string{ARM64v8_0, ARM64v8_9, ARM64v9_0}
	return []benchmarkSelectionFixture{
		{name: "amd64_sparse", arch: ArchAMD64, host: amd64Host,
			available: []string{AMD64v1, AMD64v3}, want: AMD64v3},
		{name: "amd64_dense", arch: ArchAMD64, host: amd64Host, available: amd64Dense, want: AMD64v4},
		{name: "amd64_cap", arch: ArchAMD64, host: amd64Host, available: amd64Dense,
			policy: Policy{MaxLevel: AMD64v2}, want: AMD64v2},
		{name: "amd64_disable", arch: ArchAMD64, host: amd64Host, available: amd64Dense,
			policy: Policy{DisabledVariants: []string{AMD64v4, AMD64v3}}, want: AMD64v2},
		{name: "amd64_force", arch: ArchAMD64, host: amd64Host, available: amd64Dense,
			policy: Policy{ForceLevel: AMD64v2, MaxLevel: AMD64v1, DisabledVariants: []string{AMD64v2}}, want: AMD64v2},
		{name: "amd64_normalization", arch: "X86_64", host: Info{Arch: ArchAMD64, Level: " AMD64-V4 "},
			available: []string{" LINUX_AMD64_V1 ", "AMD64-V3", "x86_64_v4", "unknown"}, want: "x86_64_v4"},
		{name: "amd64_unknown", arch: ArchAMD64, host: amd64Host, available: []string{benchmarkUnknownLevel},
			wantErr: ErrNoMatchingVariant},
		{name: "amd64_unavailable", arch: ArchAMD64, host: Info{Arch: ArchAMD64, Level: AMD64v1},
			available: []string{AMD64v4}, wantErr: ErrNoMatchingVariant},
		{name: "arm64_sparse_v8_0", arch: ArchARM64, host: benchmarkARMHost(ARM64v8_0),
			available: []string{ARM64v8_0, ARM64v8_2}, want: ARM64v8_0},
		{name: "arm64_sparse_v8_2", arch: ArchARM64, host: benchmarkARMHost(ARM64v8_2),
			available: []string{ARM64v8_0, ARM64v8_2, ARM64v9_0}, want: ARM64v8_2},
		{name: "arm64_sparse_v9_0_no_v8_9", arch: ArchARM64, host: arm64v90, available: arm64Branch, want: ARM64v9_0},
		{name: "arm64_dense_all16", arch: ArchARM64, host: benchmarkARMHost(ARM64v9_5, ARM64v8_9),
			available: arm64Dense, want: ARM64v9_5},
		{name: "arm64_cap", arch: ArchARM64, host: arm64v90, available: arm64Branch,
			policy: Policy{MaxLevel: ARM64v8_9}, want: ARM64v8_0},
		{name: "arm64_disable", arch: ArchARM64, host: arm64v90, available: arm64Branch,
			policy: Policy{DisabledVariants: []string{ARM64v9_0}}, want: ARM64v8_0},
		{name: "arm64_force", arch: ArchARM64, host: arm64v90,
			available: []string{ARM64v8_0, ARM64v8_2, ARM64v9_0}, policy: Policy{ForceLevel: ARM64v8_2}, want: ARM64v8_2},
		{name: "arm64_force_unavailable", arch: ArchARM64, host: arm64v90, available: arm64Branch,
			policy: Policy{ForceLevel: ARM64v8_2}, wantErr: ErrVariantNotEmbedded},
		{name: "arm64_force_incompatible", arch: ArchARM64, host: arm64v90, available: arm64Branch,
			policy: Policy{ForceLevel: ARM64v8_9}, wantErr: ErrIncompatibleForcedVariant},
		{name: "arm64_unknown", arch: ArchARM64, host: arm64v90,
			available: []string{benchmarkUnknownLevel}, wantErr: ErrNoMatchingVariant},
		{name: "arm64_unavailable", arch: ArchARM64, host: arm64v90, available: []string{ARM64v8_9}, wantErr: ErrNoMatchingVariant},
	}
}

func benchmarkARMHost(level string, extraLevels ...string) Info {
	features := make(map[string]bool)
	collectARM64Requirements(level, features)
	for _, extra := range extraLevels {
		collectARM64Requirements(extra, features)
	}
	host := Info{Arch: ArchARM64, Level: level, Features: make([]string, 0, len(features))}
	for feature := range features {
		host.Features = append(host.Features, feature)
	}
	sort.Strings(host.Features)
	return host
}

func BenchmarkSelectVariantForHost(b *testing.B) {
	for _, fixture := range benchmarkSelectionFixtures() {
		b.Run(fixture.name, func(b *testing.B) {
			for _, implementation := range []struct {
				name          string
				selectVariant func(string, Info, []string, Policy) (PolicyResult, error)
			}{
				{"current", SelectVariantForHost},
				{"exact_mask", benchmarkSelectVariantExactMask},
			} {
				b.Run(implementation.name, func(b *testing.B) {
					benchmarkSelector(b, fixture, implementation.selectVariant)
				})
			}
		})
	}
}

func TestSelectionExactMaskEdges(t *testing.T) {
	t.Parallel()
	for _, fixture := range []benchmarkSelectionFixture{
		{name: "duplicate_original_strings", arch: ArchARM64, host: benchmarkARMHost(ARM64v8_2),
			available: []string{" ARM64-V8.2 ", ARM64v8_2, ARM64v8_0}},
		{name: "unknown_duplicate_features", arch: ArchARM64,
			host:      Info{Arch: "aarch64", Level: ARM64v8_0, Features: []string{"fp", "fp", "asimd", "unknown", "FP"}},
			available: []string{ARM64v8_0, benchmarkUnknownLevel, "ARM64-V8.1"}},
		{name: "empty_available", arch: ArchARM64, host: benchmarkARMHost(ARM64v8_0)},
		{name: "force_empty_available", arch: ArchARM64, host: benchmarkARMHost(ARM64v8_0),
			policy: Policy{ForceLevel: ARM64v8_0}},
		{name: "unknown_force", arch: ArchARM64, host: benchmarkARMHost(ARM64v8_0),
			available: []string{ARM64v8_0}, policy: Policy{ForceLevel: benchmarkUnknownLevel}},
		{name: "unknown_host", arch: ArchARM64, host: Info{Arch: ArchARM64, Level: benchmarkUnknownLevel},
			available: []string{ARM64v8_0}},
		{name: "mismatched_arch", arch: ArchAMD64, host: benchmarkARMHost(ARM64v8_0),
			available: []string{AMD64v1}},
		{name: "unknown_arch_baseline", arch: "riscv64", host: Info{Arch: "riscv64", Level: AMD64v1},
			available: []string{AMD64v1, AMD64v2}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			assertBenchmarkSelectorEquivalent(t, fixture.arch, fixture.host, fixture.available, fixture.policy)
		})
	}
}

func benchmarkSelector(
	b *testing.B,
	fixture benchmarkSelectionFixture,
	selectVariant func(string, Info, []string, Policy) (PolicyResult, error),
) {
	b.Helper()
	want, wantErr := SelectVariantForHost(fixture.arch, fixture.host, fixture.available, fixture.policy)
	if fixture.wantErr != nil {
		require.ErrorIs(b, wantErr, fixture.wantErr)
	} else {
		require.NoError(b, wantErr)
		require.Equal(b, fixture.want, want.SelectedVariant)
	}
	var got PolicyResult
	var gotErr error
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got, gotErr = selectVariant(fixture.arch, fixture.host, fixture.available, fixture.policy)
	}
	b.StopTimer()
	require.Equal(b, want, got)
	if wantErr != nil {
		require.EqualError(b, gotErr, wantErr.Error())
	} else {
		require.NoError(b, gotErr)
	}
	runtime.KeepAlive(got)
	runtime.KeepAlive(gotErr)
}

func TestSelectionExactMaskDifferential(t *testing.T) {
	t.Parallel()
	const maskWidth = 64
	require.LessOrEqual(t, len(benchmarkExactMasks.features), maskWidth)
	require.LessOrEqual(t, len(benchmarkExactMasks.levels), maskWidth)
	for _, fixture := range benchmarkSelectionFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			assertBenchmarkSelectorEquivalent(t, fixture.arch, fixture.host, fixture.available, fixture.policy)
		})
	}
}

func TestSelectionExactMaskRandomizedDifferential(t *testing.T) {
	t.Parallel()
	const seed = 153
	const trials = 500
	random := rand.New(rand.NewSource(seed))
	levels := []string{ARM64v8_0, ARM64v8_2, ARM64v8_9, ARM64v9_0, ARM64v9_5, "ARM64-V8.5", benchmarkUnknownLevel, ""}
	for range trials {
		host := Info{Arch: ArchARM64, Level: ARM64v9_5}
		for _, feature := range benchmarkARMHost(ARM64v8_9).Features {
			if random.Intn(2) == 1 {
				host.Features = append(host.Features, feature)
			}
		}
		for _, feature := range []string{"sve", "sve2", "sme", "sme2", "unrecognized"} {
			if random.Intn(2) == 1 {
				host.Features = append(host.Features, feature)
			}
		}
		available := make([]string, random.Intn(len(levels)+1))
		for index := range available {
			available[index] = levels[random.Intn(len(levels))]
		}
		var policy Policy
		if random.Intn(2) == 1 {
			policy.MaxLevel = levels[random.Intn(len(levels))]
		}
		if random.Intn(2) == 1 {
			policy.DisabledVariants = []string{levels[random.Intn(len(levels))]}
		}
		if random.Intn(2) == 1 {
			policy.ForceLevel = levels[random.Intn(len(levels))]
		}
		assertBenchmarkSelectorEquivalent(t, ArchARM64, host, available, policy)
	}
}

func assertBenchmarkSelectorEquivalent(t *testing.T, arch string, host Info, available []string, policy Policy) {
	t.Helper()
	want, wantErr := SelectVariantForHost(arch, host, available, policy)
	got, gotErr := benchmarkSelectVariantExactMask(arch, host, available, policy)
	require.Equal(t, want, got)
	if wantErr != nil {
		require.EqualError(t, gotErr, wantErr.Error())
	} else {
		require.NoError(t, gotErr)
	}
}
