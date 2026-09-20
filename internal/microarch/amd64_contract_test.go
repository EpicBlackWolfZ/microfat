package microarch

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAMD64GoStartupContract(t *testing.T) {
	t.Parallel()
	// Cross-checked with Go 1.27.1 src/runtime/asm_amd64.s V2/V3/V4
	// FEATURES_CX, EXT_FEATURES_CX and EXT_FEATURES_BX. x/sys/cpu also
	// gates AVX/AVX2/AVX512 on the runtime's XGETBV OS-support masks.
	groups := []struct {
		fallback string
		fields   []string
	}{
		{AMD64v1, []string{"HasLAHFSAHF", "HasCX16", "HasPOPCNT", "HasSSE3", "HasSSSE3", "HasSSE41", "HasSSE42"}},
		{AMD64v2, []string{"HasAVX", "HasAVX2", "HasBMI1", "HasBMI2", "HasFMA", "HasOSXSAVE", "HasF16C", "HasLZCNT", "HasMOVBE"}},
		{AMD64v3, []string{"HasAVX512F", "HasAVX512BW", "HasAVX512CD", "HasAVX512DQ", "HasAVX512VL"}},
	}
	var complete X86Features
	fields := reflect.ValueOf(&complete).Elem()
	for i := range fields.NumField() {
		fields.Field(i).SetBool(true)
	}
	require.Equal(t, AMD64v4, EvaluateAMD64(complete))
	for _, group := range groups {
		for _, missing := range group.fields {
			t.Run(missing, func(t *testing.T) {
				t.Parallel()
				features := complete
				reflect.ValueOf(&features).Elem().FieldByName(missing).SetBool(false)
				host := Info{Arch: ArchAMD64, Level: EvaluateAMD64(features), Features: extractX86FeatureList(features)}
				require.Equal(t, group.fallback, host.Level)
				levels := []string{AMD64v1, AMD64v2, AMD64v3, AMD64v4}
				selected, err := SelectVariantForHost(ArchAMD64, host, levels, Policy{})
				require.NoError(t, err)
				require.Equal(t, group.fallback, selected.SelectedVariant)
				for _, forced := range levels {
					_, err = SelectVariantForHost(ArchAMD64, host, levels, Policy{ForceLevel: forced})
					if Compare(ArchAMD64, forced, group.fallback) > 0 {
						require.ErrorIs(t, err, ErrIncompatibleForcedVariant)
					} else {
						require.NoError(t, err)
					}
				}
			})
		}
	}
}

func TestLAHFSAHFExtendedCPUIDRange(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		maxBasic uint32
		maxExt   uint32
		extECX   uint32
		want     bool
	}{
		{"no leaves", 0, 0, 0, false},
		{"short extended range", 1, cpuidExtLeafInfo, 1, false},
		{"bit masked", 1, cpuidExtLeafFeatures, 1 << cpuidLeafExt1ECXABMBit, false},
		{"bit present", 1, cpuidExtLeafFeatures, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			query := func(leaf, subleaf uint32) (uint32, uint32, uint32, uint32) {
				require.Zero(t, subleaf)
				switch leaf {
				case cpuidBasicLeafInfo:
					return tc.maxBasic, 0, 0, 0
				case cpuidExtLeafInfo:
					return tc.maxExt, 0, 0, 0
				case cpuidBasicLeafFeatures:
					require.GreaterOrEqual(t, tc.maxBasic, uint32(cpuidBasicLeafFeatures))
				case cpuidExtLeafFeatures:
					require.GreaterOrEqual(t, tc.maxExt, uint32(cpuidExtLeafFeatures))
					return 0, 0, tc.extECX, 0
				default:
					t.Fatalf("unexpected CPUID leaf %x", leaf)
				}
				return 0, 0, 0, 0
			}
			_, _, _, lahf := probeX86ExtraFeaturesWithCPUID(query)
			require.Equal(t, tc.want, lahf)
			features := extractX86FeatureList(X86Features{HasLAHFSAHF: lahf})
			if tc.want {
				require.Contains(t, features, "lahf_lm")
			} else {
				require.NotContains(t, features, "lahf_lm")
			}
		})
	}
}
