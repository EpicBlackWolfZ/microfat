package runner

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchedMemoryControlsStartGoAndMatch(t *testing.T) {
	t.Parallel()
	child := buildRuntimeObserver(t)
	for _, tc := range []struct {
		input string
		bytes string
	}{
		{"1GiB", "1073741824"},
		{"1GB", "1073741824"},
		{"1G", "1073741824"},
		{"1.5GiB", "1610612736"},
		{"1gb", "1073741824"},
		{" 1GiB ", "1073741824"},
		{"0", "0"},
		{"0.5B", "1"},
		{"0001024", "1024"},
		{"9223372036854775807", "9223372036854775807"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			cfg := DefaultExperimentConfig()
			cfg.GOMEMLIMIT = tc.input
			require.NoError(t, cfg.Validate())
			environment, err := cfg.matchedEnvironment()
			require.NoError(t, err)
			assert.Equal(t, tc.bytes, environment["GOMEMLIMIT"])
			diagnostic := observeRuntime(t, child, environment)
			assert.Equal(t, tc.bytes, diagnostic["gomemlimit"])
			require.NoError(t, validateIdentity(runtimeConfiguration(environment), diagnostic))
		})
	}
}

func TestMatchedGCControlsStartGoAndMatch(t *testing.T) {
	t.Parallel()
	child := buildRuntimeObserver(t)
	for _, tc := range []struct {
		input     string
		canonical string
		observed  string
	}{
		{"100", "100", "100"},
		{"+100", "100", "100"},
		{"0100", "100", "100"},
		{"0", "0", "0"},
		{"-0", "0", "0"},
		{"2147483647", "2147483647", "2147483647"},
		{"off", "off", "18446744073709551615"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			cfg := DefaultExperimentConfig()
			cfg.GOGC = tc.input
			require.NoError(t, cfg.Validate())
			environment, err := cfg.matchedEnvironment()
			require.NoError(t, err)
			assert.Equal(t, tc.canonical, environment[gogcEnvironmentKey])
			diagnostic := observeRuntime(t, child, environment)
			assert.Equal(t, tc.observed, diagnostic["gogc"])
			require.NoError(t, validateIdentity(runtimeConfiguration(environment), diagnostic))
			// Readback also accepts noncanonical numeric spellings in existing configurations.
			environment[gogcEnvironmentKey] = tc.input
			require.NoError(t, validateIdentity(runtimeConfiguration(environment), diagnostic))
		})
	}
}

func TestMatchedControlsRejectInvalidSettingsBeforeBuild(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"GOMEMLIMIT", gogcEnvironmentKey} {
		invalid := []string{"", "-1", "bad", "9223372036854775808"}
		if field == gogcEnvironmentKey {
			invalid = append(invalid, "2147483648", "1.5", " 100 ", "OFF", "0x64")
		} else {
			invalid = append(invalid, "+1GiB", "1XB", "1e9", "off")
		}
		for _, input := range invalid {
			t.Run(field+"="+input, func(t *testing.T) {
				t.Parallel()
				cfg := DefaultExperimentConfig()
				if field == gogcEnvironmentKey {
					cfg.GOGC = input
				} else {
					cfg.GOMEMLIMIT = input
				}
				require.Error(t, cfg.Validate())
				_, err := buildArtifacts(context.Background(), cfg, RunOptions{Go: missingExperimentPath}, t.TempDir())
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "benchmark builds require")
			})
		}
	}
	if strconv.IntSize > 32 {
		cfg := DefaultExperimentConfig()
		tooLarge := int64(math.MaxInt32) + 1
		cfg.GOMAXPROCS = int(tooLarge)
		require.Error(t, cfg.Validate())
	}
	cfg := DefaultExperimentConfig()
	cfg.GOGC = "2147483648"
	configuration := runtimeConfiguration(map[string]string{"GOMAXPROCS": "2", gogcEnvironmentKey: cfg.GOGC})
	require.ErrorContains(t, validateIdentity(configuration, map[string]string{"level": "v1", "gomaxprocs": "2", "gogc": "100"}),
		"invalid matched GOGC configuration")
}

func TestOnOffOmitsUnusedRuntimeControls(t *testing.T) {
	t.Parallel()
	cfg := DefaultExperimentConfig()
	cfg.Tuning, cfg.GOMAXPROCS, cfg.GOMEMLIMIT, cfg.GOGC = "on-off", 0, "malformed", "malformed"
	require.NoError(t, cfg.Validate())
	environment, err := cfg.matchedEnvironment()
	require.NoError(t, err)
	assert.Nil(t, environment)
}

func buildRuntimeObserver(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "observer.go")
	const payload = `package main
import (
    "fmt"
    "runtime"
    "runtime/debug"
    "runtime/metrics"
)
func main() {
    samples := []metrics.Sample{{Name: "/gc/gogc:percent"}}
    metrics.Read(samples)
    fmt.Printf("%d %d %d\n", debug.SetMemoryLimit(-1), samples[0].Value.Uint64(), runtime.GOMAXPROCS(0))
}
`
	require.NoError(t, os.WriteFile(source, []byte(payload), evidenceFilePerm))
	child := filepath.Join(root, "observer")
	_, stderr, err := process.Run(t.Context(), process.Spec{Path: "go", Args: []string{"build", "-o", child, source},
		Env: cleanEnvironment()})
	require.NoError(t, err, "%s", stderr)
	return child
}

func observeRuntime(t *testing.T, child string, environment map[string]string) map[string]string {
	t.Helper()
	stdout, stderr, err := process.Run(t.Context(), process.Spec{Path: child,
		Env: targetEnvironment(runtimeConfiguration(environment), t.TempDir())})
	require.NoError(t, err, "%s", stderr)
	fields := strings.Fields(string(stdout))
	require.Len(t, fields, 3)
	return map[string]string{"level": "v1", "gomaxprocs": fields[2], "gomemlimit": fields[0], "gogc": fields[1]}
}

func runtimeConfiguration(environment map[string]string) schema.Configuration {
	return schema.Configuration{Level: "v1", Mode: nativeMode, Tuning: tuningMatched, Environment: environment}
}
