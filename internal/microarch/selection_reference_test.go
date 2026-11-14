package microarch

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This reference deliberately does not consult Rank, Normalize, ARM64Requirements,
// hostSupports or the production requirement graph. Each row describes a complete
// capability set, so an incorrect graph edge cannot change the expected answer.
type referenceTier struct {
	level    string
	features string
}

const (
	referenceAMD64V2     = "lahf_lm cx16 popcnt sse3 ssse3 sse4.1 sse4.2"
	referenceAMD64V3     = referenceAMD64V2 + " avx avx2 bmi1 bmi2 fma osxsave f16c lzcnt movbe"
	referenceARM85       = "fp asimd atomics crc32 fphp asimdhp jscvt fcma lrcpc dcpop asimddp dit"
	referenceARM86       = referenceARM85 + " i8mm bf16"
	referenceTrials      = 256
	referenceSeed        = 153
	referenceArchX86     = "x86_64"
	referenceArchX86Dash = "x86-64"
	referenceArchAArch64 = "aarch64"
)

var referenceAMD64Tiers = []referenceTier{
	{"v1", ""},
	{"v2", referenceAMD64V2},
	{"v3", referenceAMD64V3},
	{"v4", referenceAMD64V3 + " avx512f avx512bw avx512cd avx512dq avx512vl"},
}

var referenceARM64Tiers = []referenceTier{
	{ARM64v8_0, "fp asimd"},
	{"v8.1", "fp asimd atomics crc32"},
	{"v8.2", "fp asimd atomics crc32 fphp asimdhp"},
	{"v8.3", "fp asimd atomics crc32 fphp asimdhp jscvt fcma lrcpc"},
	{"v8.4", "fp asimd atomics crc32 fphp asimdhp jscvt fcma lrcpc dcpop asimddp"},
	{"v8.5", referenceARM85},
	{"v8.6", referenceARM86},
	{"v8.7", referenceARM86 + " wfxt"},
	{"v8.8", referenceARM86 + " wfxt mops nmi hbc"},
	{"v8.9", referenceARM86 + " wfxt mops nmi hbc gcs the"},
	{"v9.0", referenceARM85 + " sve"},
	{"v9.1", referenceARM86 + " sve"},
	{"v9.2", referenceARM86 + " sve sve2"},
	{"v9.3", referenceARM86 + " sve sve2 sme"},
	{"v9.4", referenceARM86 + " sve sve2 sme"},
	{"v9.5", referenceARM86 + " sve sve2 sme sme2"},
}

// Field names are explicit to check detection composition independently of the
// production feature-to-string conversion. Unrelated crypto/unknown flags must
// not satisfy a missing mandatory capability.
var referenceFeatureFields = map[string]string{
	"lahf_lm": "HasLAHFSAHF", "cx16": "HasCX16", "popcnt": "HasPOPCNT", "sse3": "HasSSE3",
	"ssse3": "HasSSSE3", "sse4.1": "HasSSE41", "sse4.2": "HasSSE42", "avx": "HasAVX",
	"avx2": "HasAVX2", "bmi1": "HasBMI1", "bmi2": "HasBMI2", "fma": "HasFMA", "osxsave": "HasOSXSAVE",
	"f16c": "HasF16C", "lzcnt": "HasLZCNT", "movbe": "HasMOVBE", "avx512f": "HasAVX512F",
	"avx512bw": "HasAVX512BW", "avx512cd": "HasAVX512CD", "avx512dq": "HasAVX512DQ", "avx512vl": "HasAVX512VL",
	"fp": "HasFP", "asimd": "HasASIMD", "atomics": "HasATOMICS", "crc32": "HasCRC32", "fphp": "HasFPHP",
	"asimdhp": "HasASIMDHP", "jscvt": "HasJSCVT", "fcma": "HasFCMA", "lrcpc": "HasLRCPC", "dcpop": "HasDCPOP",
	"asimddp": "HasASIMDDP", "dit": "HasDIT", "i8mm": "HasI8MM", "bf16": "HasBF16", "wfxt": "HasWFxT",
	"mops": "HasMOPS", "nmi": "HasNMI", "hbc": "HasHBC", "gcs": "HasGCS", "the": "HasTHE", "sve": "HasSVE",
	"sve2": "HasSVE2", "sme": "HasSME", "sme2": "HasSME2", "aes": "HasAES", "sha2": "HasSHA2",
}

func referenceArch(arch string) string {
	switch strings.ToLower(arch) {
	case ArchAMD64, referenceArchX86, referenceArchX86Dash:
		return ArchAMD64
	case ArchARM64, referenceArchAArch64:
		return ArchARM64
	default:
		return strings.ToLower(arch)
	}
}

func referenceNormalize(level string) string {
	value := strings.ToLower(strings.TrimSpace(level))
	for _, prefix := range []string{"linux_", "darwin_", "windows_"} {
		value = strings.TrimPrefix(value, prefix)
	}
	for _, architecture := range []string{ArchAMD64, ArchARM64, referenceArchX86, referenceArchX86Dash, referenceArchAArch64} {
		for _, separator := range []string{"_", "-"} {
			value = strings.TrimPrefix(value, architecture+separator)
		}
	}
	if value == "" {
		return "v1"
	}
	if value[0] != 'v' {
		value = "v" + value
	}
	return value
}

func referenceTiers(arch string) []referenceTier {
	switch referenceArch(arch) {
	case ArchAMD64:
		return referenceAMD64Tiers
	case ArchARM64:
		return referenceARM64Tiers
	default:
		return []referenceTier{{"v1", ""}}
	}
}

func referenceRank(arch, level string) int {
	for index, tier := range referenceTiers(arch) {
		if tier.level == referenceNormalize(level) {
			return index
		}
	}
	return -1
}

func referenceRequirements(arch, level string) []string {
	for _, tier := range referenceTiers(arch) {
		if tier.level == referenceNormalize(level) {
			return strings.Fields(tier.features)
		}
	}
	return nil
}

func referenceContainsAll(features, requirements []string) bool {
	for _, feature := range requirements {
		if !slices.Contains(features, feature) {
			return false
		}
	}
	return true
}

func referenceCompatible(host Info, level string) bool {
	if referenceRank(host.Arch, level) < 0 {
		return false
	}
	features := host.Features
	if referenceArch(host.Arch) != ArchARM64 {
		// AMD64 level-only helpers guarantee their tier's complete prerequisites.
		features = referenceRequirements(host.Arch, host.Level)
	}
	return referenceContainsAll(features, referenceRequirements(host.Arch, level))
}

func referenceSelection(
	arch string, host Info, available []string, policy Policy, skylakeX bool,
) (PolicyResult, error) {
	hostRank := referenceRank(arch, host.Level)
	if referenceArch(arch) != referenceArch(host.Arch) || hostRank < 0 {
		return PolicyResult{}, ErrNoMatchingVariant
	}
	if policy.ForceLevel != "" && !referenceCompatible(host, policy.ForceLevel) {
		return PolicyResult{}, ErrIncompatibleForcedVariant
	}
	if len(available) == 0 {
		return PolicyResult{}, ErrEmptyVariants
	}
	compatible := false
	for _, level := range available {
		compatible = compatible || referenceCompatible(host, level)
	}
	if !compatible {
		return PolicyResult{}, ErrNoMatchingVariant
	}
	if policy.ForceLevel != "" {
		return referenceForcedSelection(arch, host, available, policy.ForceLevel)
	}
	ceiling, applied, reason := referencePolicyCeiling(arch, hostRank, policy, skylakeX)
	best := PolicyResult{PolicyApplied: applied, OverrideReason: reason}
	bestRank := -1
	for _, level := range available {
		rank := referenceRank(arch, level)
		if rank <= bestRank || rank > ceiling || !referenceCompatible(host, level) || referenceDisabled(level, policy.DisabledVariants) {
			continue
		}
		best.SelectedVariant, bestRank = level, rank
	}
	if bestRank < 0 {
		return PolicyResult{}, ErrNoMatchingVariant
	}
	return best, nil
}

func referenceForcedSelection(arch string, host Info, available []string, force string) (PolicyResult, error) {
	if referenceRank(arch, force) > referenceRank(arch, host.Level) {
		return PolicyResult{}, ErrIncompatibleForcedVariant
	}
	for _, level := range available {
		if referenceCompatible(host, level) && referenceNormalize(level) == referenceNormalize(force) {
			return PolicyResult{
				SelectedVariant: level, PolicyApplied: "force_level", OverrideReason: "MICROFAT_FORCE_LEVEL=" + force,
			}, nil
		}
	}
	return PolicyResult{}, ErrVariantNotEmbedded
}

func referencePolicyCeiling(arch string, hostRank int, policy Policy, skylakeX bool) (int, string, string) {
	ceiling, applied, reason := hostRank, "", ""
	if rank := referenceRank(arch, policy.MaxLevel); policy.MaxLevel != "" && rank >= 0 && rank < ceiling {
		ceiling, applied, reason = rank, "max_level", "MICROFAT_MAX_LEVEL="+policy.MaxLevel
	}
	safe := policy.AVX512DownclockProtection || policy.Name == PolicySafeAVX512 || policy.Name == PolicyNoDownclock
	if safe && skylakeX && referenceArch(arch) == ArchAMD64 && ceiling > referenceRank(arch, "v3") {
		ceiling, applied = referenceRank(arch, "v3"), "avx512_downclock_protection"
		reason = "Intel Skylake-X/Cascade Lake AVX-512 downclocking protection applied (capped at v3)"
	}
	if len(policy.DisabledVariants) > 0 && applied == "" {
		applied, reason = "disable_variants", "MICROFAT_DISABLE_VARIANTS="+strings.Join(policy.DisabledVariants, ",")
	}
	return ceiling, applied, reason
}

func referenceDisabled(level string, disabled []string) bool {
	for _, denied := range disabled {
		if referenceNormalize(level) == referenceNormalize(denied) {
			return true
		}
	}
	return false
}

func referenceDetectedHost(t *testing.T, arch string, features []string) Info {
	t.Helper()
	var x86 X86Features
	var arm ARM64Features
	fields := reflect.ValueOf(&x86).Elem()
	if arch == ArchARM64 {
		fields = reflect.ValueOf(&arm).Elem()
	}
	for _, name := range features {
		if field, exists := referenceFeatureFields[name]; exists {
			fields.FieldByName(field).SetBool(true)
		}
	}
	host := detectForArch("linux", arch, x86, arm)
	expected := referenceTiers(arch)[0].level
	for _, tier := range referenceTiers(arch) {
		if referenceContainsAll(features, strings.Fields(tier.features)) {
			expected = tier.level
		}
	}
	require.Equal(t, expected, host.Level, "arch=%s features=%v", arch, features)
	if arch == ArchARM64 {
		for _, status := range host.Levels {
			require.Equal(t, referenceContainsAll(features, referenceRequirements(arch, status.Level)), status.Satisfied, status.Level)
		}
	}
	return host
}

func referenceFeatureSets(arch string, random *rand.Rand) [][]string {
	var all []string
	for _, tier := range referenceTiers(arch) {
		for _, feature := range strings.Fields(tier.features) {
			if !slices.Contains(all, feature) {
				all = append(all, feature)
			}
		}
	}
	sets := [][]string{nil, all}
	for index := range all {
		missing := slices.Clone(all)
		sets = append(sets, slices.Delete(missing, index, index+1))
	}
	for _, tier := range referenceTiers(arch) {
		sets = append(sets, strings.Fields(tier.features))
	}
	const densitySteps = 4
	for trial := range referenceTrials {
		var features []string
		for _, feature := range all {
			if random.IntN(densitySteps) <= trial%densitySteps {
				features = append(features, feature)
			}
		}
		sets = append(sets, features)
	}
	return sets
}

func referenceAvailable(arch string, random *rand.Rand) []string {
	var available []string
	for _, tier := range referenceTiers(arch) {
		if random.IntN(2) == 0 {
			available = append(available, referenceAlias(arch, tier.level, random))
		}
	}
	available = append(available, "unknown_level", "v42")
	random.Shuffle(len(available), func(i, j int) { available[i], available[j] = available[j], available[i] })
	return available
}

func referenceAlias(arch, level string, random *rand.Rand) string {
	aliases := []string{level, strings.ToUpper(level), strings.TrimPrefix(level, "v"), arch + "_" + level, "linux_" + arch + "_" + level}
	return aliases[random.IntN(len(aliases))]
}

func referenceCompareSelection(t *testing.T, arch string, host Info, available []string, policy Policy, skylakeX bool) {
	t.Helper()
	want, wantErr := referenceSelection(arch, host, available, policy, skylakeX)
	got, gotErr := SelectVariantForHost(arch, host, available, policy)
	if wantErr != nil {
		require.ErrorIs(t, gotErr, wantErr, "host=%+v available=%v policy=%+v", host, available, policy)
		require.Empty(t, got.SelectedVariant)
		return
	}
	require.NoError(t, gotErr, "host=%+v available=%v policy=%+v", host, available, policy)
	require.Equal(t, want, got, "host=%+v available=%v policy=%+v", host, available, policy)
}

func TestRequirementsReferenceDifferential(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{ArchAMD64, ArchARM64} {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()
			random := rand.New(rand.NewPCG(referenceSeed, referenceSeed))
			for index, features := range referenceFeatureSets(arch, random) {
				t.Run(fmt.Sprintf("features_%d", index), func(t *testing.T) {
					host := referenceDetectedHost(t, arch, features)
					host.Features = append(host.Features, "unknown_feature", "future_isa")
					available := referenceAvailable(arch, random)
					tiers := referenceTiers(arch)
					capLevel := referenceAlias(arch, tiers[random.IntN(len(tiers))].level, random)
					forceLevel := referenceAlias(arch, tiers[random.IntN(len(tiers))].level, random)
					for _, policy := range []Policy{
						{}, {MaxLevel: capLevel}, {MaxLevel: "unknown_level"}, {ForceLevel: forceLevel}, {ForceLevel: "unknown_level"},
						{DisabledVariants: []string{capLevel, forceLevel}},
						{MaxLevel: capLevel, DisabledVariants: []string{forceLevel}},
						{ForceLevel: forceLevel, MaxLevel: capLevel, DisabledVariants: []string{forceLevel}},
					} {
						referenceCompareSelection(t, arch, host, available, policy, false)
					}
				})
			}
		})
	}
}

func TestRequirementsReferenceFailureSemantics(t *testing.T) {
	t.Parallel()
	host := Info{Arch: ArchAMD64, Level: "v3"}
	for _, test := range []struct {
		name      string
		arch      string
		host      Info
		available []string
		policy    Policy
		err       error
	}{
		{"empty", ArchAMD64, host, nil, Policy{}, ErrEmptyVariants},
		{"empty compatible force", ArchAMD64, host, nil, Policy{ForceLevel: "v2"}, ErrEmptyVariants},
		{"empty incompatible force", ArchAMD64, host, nil, Policy{ForceLevel: "v4"}, ErrIncompatibleForcedVariant},
		{"unknown host", ArchAMD64, Info{Arch: ArchAMD64, Level: "v99"}, []string{"v1"}, Policy{}, ErrNoMatchingVariant},
		{"wrong architecture", ArchARM64, host, []string{ARM64v8_0}, Policy{}, ErrNoMatchingVariant},
		{"unknown available", ArchAMD64, host, []string{"v99"}, Policy{}, ErrNoMatchingVariant},
		{"compatible force missing", ArchAMD64, host, []string{"v1"}, Policy{ForceLevel: "v2"}, ErrVariantNotEmbedded},
		{"no compatible available before force", ArchAMD64, host, []string{"v4"}, Policy{ForceLevel: "v2"}, ErrNoMatchingVariant},
		{"all disabled", ArchAMD64, host, []string{"v1"}, Policy{DisabledVariants: []string{"AMD64_V1"}}, ErrNoMatchingVariant},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := SelectVariantForHost(test.arch, test.host, test.available, test.policy)
			require.ErrorIs(t, err, test.err)
			referenceCompareSelection(t, test.arch, test.host, test.available, test.policy, false)
		})
	}
}

func TestRequirementsReferenceLAHFNegative(t *testing.T) {
	t.Parallel()
	features := referenceRequirements(ArchAMD64, AMD64v4)
	features = slices.DeleteFunc(features, func(feature string) bool { return feature == "lahf_lm" })
	host := referenceDetectedHost(t, ArchAMD64, features)
	require.Equal(t, AMD64v1, host.Level)
	available := []string{"linux_amd64_v4", "AMD64_V2", "x86-64_v3", AMD64v1}
	referenceCompareSelection(t, ArchAMD64, host, available, Policy{}, false)
	for _, force := range available[:len(available)-1] {
		_, err := SelectVariantForHost(ArchAMD64, host, available, Policy{ForceLevel: force})
		require.ErrorIs(t, err, ErrIncompatibleForcedVariant)
	}
}

func TestRequirementsReferenceAvailableSubsetsAndOrder(t *testing.T) {
	t.Parallel()
	const subsetCount = 1 << 4
	for _, tier := range referenceAMD64Tiers {
		host := Info{Arch: ArchAMD64, Level: tier.level}
		for subset := range subsetCount {
			var available []string
			for index, candidate := range referenceAMD64Tiers {
				if subset&(1<<index) != 0 {
					available = append(available, candidate.level)
				}
			}
			for _, policy := range []Policy{{}, {MaxLevel: AMD64v2}, {DisabledVariants: []string{AMD64v3}}} {
				referenceCompareSelection(t, ArchAMD64, host, available, policy, false)
				forward, forwardErr := SelectVariantForHost(ArchAMD64, host, available, policy)
				reversed := slices.Clone(available)
				slices.Reverse(reversed)
				referenceCompareSelection(t, ArchAMD64, host, reversed, policy, false)
				backward, backwardErr := SelectVariantForHost(ArchAMD64, host, reversed, policy)
				require.Equal(t, forward, backward)
				require.Equal(t, forwardErr == nil, backwardErr == nil)
			}
		}
	}
	// Aliases of the same tier retain the original embedded identifier. Selection
	// chooses the first such identifier, including under an aliased force policy.
	for _, available := range [][]string{{"V3", "amd64_v3"}, {"amd64_v3", "V3"}} {
		host := Info{Arch: ArchAMD64, Level: AMD64v3}
		for _, policy := range []Policy{{}, {ForceLevel: "x86_64-3"}} {
			referenceCompareSelection(t, ArchAMD64, host, available, policy, false)
			result, err := SelectVariantForHost(ArchAMD64, host, available, policy)
			require.NoError(t, err)
			require.Equal(t, available[0], result.SelectedVariant)
		}
	}
}

func TestRequirementsReferenceARM64Branches(t *testing.T) {
	t.Parallel()
	for _, level := range []string{"v9.0", "v9.1", "v9.2", "v9.5"} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			host := referenceDetectedHost(t, ArchARM64, referenceRequirements(ArchARM64, level))
			for _, unavailable := range []string{"v8.7", "v8.8", "v8.9"} {
				referenceCompareSelection(t, ArchARM64, host, []string{ARM64v8_0, unavailable}, Policy{}, false)
				_, err := SelectVariantForHost(ArchARM64, host, []string{ARM64v8_0, unavailable}, Policy{ForceLevel: unavailable})
				require.ErrorIs(t, err, ErrIncompatibleForcedVariant)
			}
			// The level-only helper guarantees only the requirements of its level.
			for _, tier := range referenceARM64Tiers {
				want, wantErr := referenceSelection(ArchARM64, host, []string{tier.level}, Policy{}, false)
				got, gotErr := SelectVariantWithPolicy(ArchARM64, level, []string{tier.level}, Policy{})
				if wantErr != nil {
					require.ErrorIs(t, gotErr, wantErr)
				} else {
					require.NoError(t, gotErr)
					require.Equal(t, want, got)
				}
			}
		})
	}
	host := referenceDetectedHost(t, ArchARM64, referenceRequirements(ArchARM64, ARM64v8_9))
	for _, tier := range referenceARM64Tiers {
		if strings.HasPrefix(tier.level, "v9.") {
			_, err := SelectVariantForHost(ArchARM64, host, []string{ARM64v8_0, tier.level}, Policy{ForceLevel: tier.level})
			require.ErrorIs(t, err, ErrIncompatibleForcedVariant, tier.level)
		}
	}
	// AES/SHA and unknown features cannot stand in for baseline FP/ASIMD.
	for _, feature := range []string{"fp", "asimd"} {
		missingBaseline := Info{Arch: ArchARM64, Level: ARM64v8_0, Features: []string{feature, "aes", "sha2", "unknown_feature"}}
		_, err := SelectVariantForHost(ArchARM64, missingBaseline, []string{ARM64v8_0}, Policy{})
		require.ErrorIs(t, err, ErrNoMatchingVariant)
	}
}

func TestRequirementsReferenceArchitecturePolicyAliases(t *testing.T) {
	original := isSkylakeXOrCascadeLakeFunc
	t.Cleanup(func() { isSkylakeXOrCascadeLakeFunc = original })
	isSkylakeXOrCascadeLakeFunc = func() bool { return true }
	for _, arch := range []string{ArchAMD64, "AMD64", referenceArchX86, referenceArchX86Dash} {
		t.Run(arch, func(t *testing.T) {
			for _, hostArch := range []string{ArchAMD64, "AMD64", referenceArchX86, referenceArchX86Dash} {
				host := Info{Arch: hostArch, Level: "v4"}
				available := []string{"v1", "AMD64_V3", "linux_amd64_v4"}
				for _, policy := range []Policy{
					{}, {Name: PolicySafeAVX512}, {Name: PolicyNoDownclock}, {AVX512DownclockProtection: true},
					{MaxLevel: "v2", Name: PolicySafeAVX512},
					{ForceLevel: "v4", MaxLevel: "v1", DisabledVariants: []string{"v4"}, Name: PolicySafeAVX512},
				} {
					referenceCompareSelection(t, arch, host, available, policy, true)
				}
			}
		})
	}
}

func TestRequirementsReferenceARM64ArchitectureAliases(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{ArchARM64, "ARM64", referenceArchAArch64, "AARCH64"} {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()
			for _, hostArch := range []string{ArchARM64, "ARM64", referenceArchAArch64, "AARCH64"} {
				host := Info{Arch: hostArch, Level: "V9.2", Features: referenceRequirements(ArchARM64, "v9.2")}
				available := []string{"linux_arm64_v8.5", "AARCH64-V9.2", "arm64_v8.9"}
				for _, policy := range []Policy{
					{}, {MaxLevel: "aarch64_8.9"}, {ForceLevel: "AARCH64_9.2"},
					{DisabledVariants: []string{"arm64-9.2"}}, {ForceLevel: "aarch64_8.9"},
				} {
					referenceCompareSelection(t, arch, host, available, policy, false)
					want, wantErr := referenceSelection(arch, host, available, policy, false)
					got, gotErr := SelectVariantWithPolicy(arch, host.Level, available, policy)
					if wantErr != nil {
						require.ErrorIs(t, gotErr, wantErr)
					} else {
						require.NoError(t, gotErr)
						require.Equal(t, want, got)
					}
				}
			}
		})
	}
}
