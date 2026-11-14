package main

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceDateEpoch(t *testing.T) {
	t.Run("absent retains wall clock", func(t *testing.T) {
		t.Setenv(sourceDateEpochEnv, "")
		require.NoError(t, os.Unsetenv(sourceDateEpochEnv))
		epoch, err := sourceDateEpoch()
		require.NoError(t, err)
		assert.Nil(t, epoch)
	})

	cases := []struct {
		name, raw string
		want      int64
	}{
		{"zero", "0", 0},
		{"negative zero", "-0", 0},
		{"negative", "-1", -1},
		{"explicit positive", "+1", 1},
		{"leading zeroes", "0001", 1},
		{"minimum", "-9223372036854775808", math.MinInt64},
		{"maximum", "9223372036854775807", math.MaxInt64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(sourceDateEpochEnv, tc.raw)
			epoch, err := sourceDateEpoch()
			require.NoError(t, err)
			require.NotNil(t, epoch)
			assert.Equal(t, tc.want, *epoch)
			assert.Equal(t, tc.raw, os.Getenv(sourceDateEpochEnv), "preserve exported value for build children")
		})
	}
}

func TestInvalidSourceDateEpochFailsBeforePackagingSideEffects(t *testing.T) {
	invalidValues := []string{
		"", " ", "1.5", "1e3", "1_000", "0x10", "+", "-", " 1", "1\n",
		"１２", "١٢", "−1", "9223372036854775808", "-9223372036854775809",
	}
	routes := []struct {
		name string
		new  func() *cobra.Command
		args []string
	}{
		{"direct", newPackCmd, []string{flagStub, "missing-stub", "-v", "v1=missing-payload"}},
		{"manifest", newPackCmd, []string{flagManifest, "missing-manifest.yaml"}},
		{"pgo", newPgoPackCmd, []string{flagManifest, "missing-manifest.yaml"}},
	}
	for _, route := range routes {
		for _, raw := range invalidValues {
			t.Run(route.name+"/"+strconv.QuoteToASCII(raw), func(t *testing.T) {
				t.Setenv(sourceDateEpochEnv, raw)
				dir := t.TempDir()
				output := filepath.Join(dir, "existing-output")
				unrelated := filepath.Join(dir, "unrelated")
				original := []byte("retain original output")
				require.NoError(t, os.WriteFile(output, original, 0o755))
				require.NoError(t, os.WriteFile(unrelated, original, 0o644))
				for _, destination := range []string{output, filepath.Join(dir, "missing", "out")} {
					cmd := route.new()
					cmd.SilenceUsage = true
					cmd.SilenceErrors = true
					var out, errOut bytes.Buffer
					cmd.SetOut(&out)
					cmd.SetErr(&errOut)
					cmd.SetArgs(append(append([]string{}, route.args...), flagOutput, destination))
					err := cmd.Execute()
					require.ErrorContains(t, err, "invalid SOURCE_DATE_EPOCH")
					assert.Contains(t, err.Error(), "signed ASCII base-10 integer Unix seconds")
					assert.Empty(t, out.String())
				}
				for _, path := range []string{output, unrelated} {
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					assert.Equal(t, original, data)
				}
				assert.NoDirExists(t, filepath.Join(dir, "missing"))
				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				assert.Len(t, entries, 2, "no temporary build or output paths may be created")
				assert.Equal(t, raw, os.Getenv(sourceDateEpochEnv))
			})
		}
	}
}

func TestCreationDate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		epoch int64
		want  string
	}{
		{"epoch", 0, "1970-01-01T00:00:00Z"},
		{"pre-epoch", -1, "1969-12-31T23:59:59Z"},
		{"first RFC3339 second", minCreationDateUnix, "0000-01-01T00:00:00Z"},
		{"last RFC3339 second", maxCreationDateUnix, "9999-12-31T23:59:59Z"},
		{"before RFC3339", minCreationDateUnix - 1, creationDateUnavailable},
		{"after RFC3339", maxCreationDateUnix + 1, creationDateUnavailable},
		{"minimum integer", math.MinInt64, creationDateUnavailable},
		{"maximum integer", math.MaxInt64, creationDateUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, creationDate(tc.epoch))
		})
	}
}
