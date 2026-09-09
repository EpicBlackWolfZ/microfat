package microarch

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestARM64DetectionSelectionComposition(t *testing.T) {
	t.Parallel()
	f := ARM64Features{HasFP: true, HasASIMD: true, HasATOMICS: true, HasCRC32: true, HasFPHP: true, HasASIMDHP: true,
		HasJSCVT: true, HasFCMA: true, HasLRCPC: true, HasDCPOP: true, HasASIMDDP: true, HasDIT: true, HasSVE: true, HasSVE2: true}
	host := detectForArch("linux", ArchARM64, X86Features{}, f)
	require.Equal(t, ARM64v9_0, host.Level)
	levels := []string{ARM64v8_0, ARM64v8_9, ARM64v9_0}
	for _, tc := range []struct {
		name   string
		policy Policy
		want   string
		fails  bool
	}{
		{"default", Policy{}, ARM64v9_0, false},
		{"cap excludes v9", Policy{MaxLevel: ARM64v8_9}, ARM64v8_0, false},
		{"disable v9", Policy{DisabledVariants: []string{ARM64v9_0}}, ARM64v8_0, false},
		{"force unsafe", Policy{ForceLevel: ARM64v8_9}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := SelectVariantForHost(ArchARM64, host, levels, tc.policy)
			if tc.fails {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, result.SelectedVariant)
			}
		})
	}
	_, err := SelectVariantForHost(ArchARM64, host, []string{ARM64v8_9}, Policy{})
	require.ErrorIs(t, err, ErrNoMatchingVariant)
	_, err = SelectVariantWithPolicy(ArchARM64, ARM64v9_0, []string{ARM64v8_9}, Policy{})
	require.ErrorIs(t, err, ErrNoMatchingVariant)
}

func TestARM64CompatibilitySubsetOracle(t *testing.T) {
	t.Parallel()
	// Independent fixed-point oracle computes satisfiable nodes, without production recursion.
	const seed = 172
	random := rand.New(rand.NewSource(seed))
	const trials = 500
	for range trials {
		host := Info{Arch: ArchARM64, Level: ARM64v9_5}
		features := map[string]bool{}
		for _, req := range ARM64Requirements() {
			for _, feature := range req.RequiredFeatures {
				if random.Intn(2) == 1 {
					features[feature] = true
				}
			}
		}
		for feature := range features {
			host.Features = append(host.Features, feature)
		}
		satisfied := map[string]bool{}
		for range ARM64Requirements() {
			for _, req := range ARM64Requirements() {
				ok := true
				for _, p := range req.Prereqs {
					ok = ok && satisfied[p]
				}
				for _, f := range req.RequiredFeatures {
					ok = ok && features[f]
				}
				if ok {
					satisfied[req.Level] = true
				}
			}
		}
		for _, req := range ARM64Requirements() {
			require.Equal(t, satisfied[req.Level], hostSupports(host, req.Level), req.Level)
		}
	}
}
