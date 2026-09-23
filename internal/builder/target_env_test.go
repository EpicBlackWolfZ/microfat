package builder

import (
	"slices"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssembleTargetEnv(t *testing.T) {
	t.Parallel()

	t.Run("strips_ambient_target_variables_and_deduplicates", func(t *testing.T) {
		t.Parallel()

		ambient := []string{
			"GOOS=windows",
			"GOARCH=386",
			"GOAMD64=v4",
			"GOARM64=v9.5",
			"CGO_ENABLED=1",
			"CGO_ENABLED=0", // last-value deduplication
			"CUSTOM_VAR=hello",
			"MALFORMED_ENTRY", // no '=' should be ignored
			"=EMPTY_KEY",      // empty key should be ignored
		}

		m := &Manifest{
			TargetOS:   testOSLinux,
			TargetArch: microarch.ArchAMD64,
			Env: map[string]string{
				"ROOT_VAR":   "root_val",
				"CUSTOM_VAR": "root_override",
				EnvGOOS:      testOSLinux,
				EnvGOARCH:    testArchAMD64,
				EnvGOAMD64:   "v1",
			},
			Variants: []VariantConfig{
				{Level: "v1"},
			},
		}

		v := VariantConfig{
			Level: "v1",
			Env: map[string]string{
				"VARIANT_VAR": "var_val",
				"CUSTOM_VAR":  "variant_override",
				EnvGOOS:       testOSLinux,
				EnvGOARCH:     testArchAMD64,
				EnvGOAMD64:    "v1",
			},
		}

		env := assembleTargetEnv(ambient, m, v)

		// Check deterministic sorting
		require.True(t, slices.IsSorted(env), "output environment must be sorted: %v", env)

		envMap := make(map[string]string, len(env))
		for _, e := range env {
			k, val, ok := strings.Cut(e, "=")
			require.True(t, ok)
			envMap[k] = val
		}

		// Authoritative target variables
		assert.Equal(t, testOSLinux, envMap[EnvGOOS])
		assert.Equal(t, testArchAMD64, envMap[EnvGOARCH])
		assert.Equal(t, "v1", envMap[EnvGOAMD64])

		// Inactive tier variable must be absent
		_, hasARM64 := envMap[EnvGOARM64]
		assert.False(t, hasARM64, "GOARM64 must be absent for amd64 target")

		// Precedence: variant overrides root overrides ambient
		assert.Equal(t, "variant_override", envMap["CUSTOM_VAR"])
		assert.Equal(t, "0", envMap["CGO_ENABLED"])
		assert.Equal(t, "root_val", envMap["ROOT_VAR"])
		assert.Equal(t, "var_val", envMap["VARIANT_VAR"])
	})

	t.Run("arm64_target_sets_GOARM64_and_omits_GOAMD64", func(t *testing.T) {
		t.Parallel()

		ambient := []string{
			"GOAMD64=v3",
			EnvGOARM64 + "=v8.0",
		}

		m := &Manifest{
			TargetOS:   testOSLinux,
			TargetArch: microarch.ArchARM64,
			Variants: []VariantConfig{
				{Level: "v8.2"},
			},
		}

		v := VariantConfig{
			Level: "v8.2",
		}

		env := assembleTargetEnv(ambient, m, v)

		envMap := make(map[string]string, len(env))
		for _, e := range env {
			k, val, ok := strings.Cut(e, "=")
			require.True(t, ok)
			envMap[k] = val
		}

		assert.Equal(t, "linux", envMap["GOOS"])
		assert.Equal(t, "arm64", envMap["GOARCH"])
		assert.Equal(t, "v8.2", envMap["GOARM64"])

		_, hasAMD64 := envMap["GOAMD64"]
		assert.False(t, hasAMD64, "GOAMD64 must be absent for arm64 target")
	})
}
