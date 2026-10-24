//go:build linux

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/runtimeinfo"
	"github.com/stretchr/testify/require"
)

const (
	telemetryColdCache  = "cold-cache"
	telemetryWarmCache  = "warm-cache"
	telemetryJSON       = "json"
	telemetryDebug      = "debug"
	telemetryFormatFlag = "--format-version"
)

type telemetryDiagnosticMode struct {
	name, debug, log, output string
}

func telemetryDiagnosticModes() []telemetryDiagnosticMode {
	return []telemetryDiagnosticMode{
		{name: "unset"},
		{name: "zero", debug: "0"},
		{name: "false", debug: "false"},
		{name: "mixed-case-false", debug: "FaLsE"},
		{name: telemetryJSON, log: "JsOn", output: telemetryJSON},
		{name: telemetryDebug, debug: "true", output: telemetryDebug},
	}
}

// This is a behavioral compatibility matrix, not startup performance evidence.
// Cold extraction and warm descriptor verification must keep the same contract
// with clocks disabled and with either supported diagnostic output enabled.
func TestTelemetryTimingCompatibility(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	profiles := []cacheFirstProfile{
		{name: launcherFullProfile, path: stubPath},
		{name: launcherMinimalProfile, path: buildMinimalLauncherStub(t, dir)},
	}
	codecs := []checksumCasingCodec{
		{name: creationEpochNone, algorithm: creationEpochNone},
		{name: creationEpochLZ4, algorithm: "lz4:fast"},
		{name: mountCodecZstd, algorithm: creationEpochZstd},
		{name: "zstd-dictionary", algorithm: creationEpochZstd, dictionary: true},
	}
	levels := creationEpochLevels()
	require.NotEmpty(t, levels)
	for _, profile := range profiles {
		for _, codec := range codecs {
			for _, version := range []int{1, 2} {
				name := fmt.Sprintf("%s/%s/format%d", profile.name, codec.name, version)
				t.Run(name, func(t *testing.T) {
					fat := filepath.Join(t.TempDir(), "telemetry.fat")
					args := []string{inputPackCommand, inputStubFlag, profile.path, mountArchFlag, currentHostArch,
						creationEpochCompressionFlag, codec.algorithm, telemetryFormatFlag, strconv.Itoa(version), "-o", fat}
					for _, level := range levels {
						args = append(args, "-v", level+"="+goldenVariantBins[level])
					}
					if codec.dictionary {
						args = append(args, "--dict")
					}
					creationEpochCommand(t, nil, args...)
					require.Equal(t, codec.dictionary, inspectCreationEpoch(t, fat).DictionarySize > 0)
					for _, diagnostic := range telemetryDiagnosticModes() {
						for _, path := range []string{execModeMemfd, telemetryColdCache, telemetryWarmCache} {
							t.Run(diagnostic.name+"/"+path, func(t *testing.T) {
								env, mode := telemetryProcessEnvironment(t, fat, levels[0], path, diagnostic)
								stdout, stderr, err := runTelemetryProcess(t, fat, env)
								require.NoError(t, err, stderr)
								require.Equal(t, "golden:variant="+levels[0]+"\ngolden:env=telemetry-preserved\n", stdout)
								requireTelemetryDiagnostics(t, diagnostic, stderr, levels[0], mode, path == telemetryWarmCache)
							})
						}
					}
				})
			}
		}
	}
}

func telemetryProcessEnvironment(
	t *testing.T, fat, level, path string, diagnostic telemetryDiagnosticMode,
) ([]string, string) {
	t.Helper()
	mode := execModeCache
	if path == execModeMemfd {
		mode = execModeMemfd
	}
	env := stubInfoProcessEnvironment(filepath.Join(t.TempDir(), "cache"), "MICROFAT_FORCE_LEVEL="+level,
		"MICROFAT_EXEC_MODE="+mode, "TEST_CUSTOM_KEY=telemetry-preserved", "GOMEMLIMIT=384MiB", "GOMAXPROCS=2", "GOGC=75")
	if path == telemetryWarmCache {
		_, stderr, err := runTelemetryProcess(t, fat, env)
		require.NoError(t, err, stderr)
		require.Empty(t, stderr, "cache preparation must also execute with diagnostics disabled")
	}
	if diagnostic.debug != "" {
		env = append(env, "MICROFAT_DEBUG="+diagnostic.debug)
	}
	if diagnostic.log != "" {
		env = append(env, "MICROFAT_LOG="+diagnostic.log)
	}
	return env, mode
}

func runTelemetryProcess(t *testing.T, path string, env []string, args ...string) (string, string, error) {
	t.Helper()
	const timeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	err := runFixtureCommand(func() *exec.Cmd {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = env
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		return cmd
	})
	require.NoError(t, ctx.Err(), "launcher blocked: %s", stderr.String())
	return stdout.String(), stderr.String(), err
}

func requireTelemetryDiagnostics(
	t *testing.T, diagnostic telemetryDiagnosticMode, stderr, level, mode string, warm bool,
) {
	t.Helper()
	if diagnostic.output == "" {
		require.Empty(t, stderr, "disabled diagnostics emitted output")
		return
	}
	if diagnostic.output == telemetryDebug {
		require.Contains(t, stderr, "[microfat:debug] host_arch="+currentHostArch)
		require.Contains(t, stderr, "selected_variant="+level+" exec_mode="+mode)
		require.Regexp(t, `decompress_us=\d+ total_us=[1-9]\d*`, stderr)
		if warm {
			require.Contains(t, stderr, "decompress_us=0 ")
		}
		return
	}
	line := strings.TrimSpace(stderr)
	require.True(t, strings.HasPrefix(line, "[microfat] "), stderr)
	var record struct {
		Event             string `json:"event"`
		Timestamp         int64  `json:"timestamp_unix_nano"`
		HostArch          string `json:"host_arch"`
		SelectedVariant   string `json:"selected_variant"`
		SelectedSHA256    string `json:"selected_sha256"`
		SelectedSize      int64  `json:"selected_size_bytes"`
		ExecMode          string `json:"exec_mode"`
		DecompressionTime *int64 `json:"decompression_duration_us"`
		TotalTime         int64  `json:"total_launcher_us"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "[microfat] ")), &record), stderr)
	require.Equal(t, "dispatch", record.Event)
	require.Positive(t, record.Timestamp)
	require.Equal(t, currentHostArch, record.HostArch)
	require.Equal(t, level, record.SelectedVariant)
	require.Regexp(t, `^[a-f0-9]{64}$`, record.SelectedSHA256)
	require.Positive(t, record.SelectedSize)
	require.Equal(t, mode, record.ExecMode)
	require.Positive(t, record.TotalTime)
	if warm {
		require.Nil(t, record.DecompressionTime, "warm execution must retain the omitted decompression field")
	} else {
		require.NotNil(t, record.DecompressionTime)
		require.Positive(t, *record.DecompressionTime)
		require.GreaterOrEqual(t, record.TotalTime, *record.DecompressionTime)
	}
}

func TestTelemetryEnvironmentHints(t *testing.T) {
	t.Parallel()
	_, products := runtimeInfoProducts(t)
	baseLevel, _ := hostVariantLabels()
	for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
		for _, diagnostic := range telemetryDiagnosticModes() {
			for _, path := range []string{execModeMemfd, telemetryColdCache, telemetryWarmCache} {
				t.Run(profile+"/"+diagnostic.name+"/"+path, func(t *testing.T) {
					fat := products[profile]
					env, mode := telemetryProcessEnvironment(t, fat, baseLevel, path, diagnostic)
					stdout, stderr, err := runTelemetryProcess(t, fat, env)
					require.NoError(t, err, stderr)
					var snapshot runtimeinfo.Snapshot
					require.NoError(t, json.Unmarshal([]byte(stdout), &snapshot), stdout)
					assertRuntimeInfoExplicitEnvironment(t, snapshot.Configured)
					assertRuntimeInfoObserved(t, snapshot.Observed, runtimeInfoMemory, runtimeInfoMaxProcs, runtimeInfoGCPercent)
					assertRuntimeInfoField(t, snapshot.Launcher.SelectedVariant, baseLevel)
					assertRuntimeInfoField(t, snapshot.Launcher.HostArch, currentHostArch)
					assertRuntimeInfoField(t, snapshot.Launcher.HostLevel, currentHostLevel)
					assertRuntimeInfoField(t, snapshot.Launcher.ExecMode, mode)
					assertRuntimeInfoField(t, snapshot.Launcher.DispatchMode, mode)
					assertRuntimeInfoField(t, snapshot.Launcher.OriginalExecutable, fat)
					requireTelemetryDiagnostics(t, diagnostic, stderr, baseLevel, mode, path == telemetryWarmCache)
				})
			}
		}
	}
}

func TestTelemetryNonemptyDiagnosticValues(t *testing.T) {
	t.Parallel()
	level := creationEpochLevels()[0]
	cases := []telemetryDiagnosticMode{
		{name: "unrecognized-debug", debug: "enabled", output: telemetryDebug},
		{name: "whitespace-debug", debug: " false ", output: telemetryDebug},
		{name: "zero-log", log: "0", output: telemetryDebug},
		{name: "false-log", log: "false", output: telemetryDebug},
		{name: "json-with-disabled-debug", debug: "0", log: "json", output: telemetryJSON},
	}
	for _, diagnostic := range cases {
		t.Run(diagnostic.name, func(t *testing.T) {
			env, mode := telemetryProcessEnvironment(t, goldenFatBin, level, execModeMemfd, diagnostic)
			stdout, stderr, err := runTelemetryProcess(t, goldenFatBin, env)
			require.NoError(t, err, stderr)
			require.Contains(t, stdout, "golden:variant="+level)
			requireTelemetryDiagnostics(t, diagnostic, stderr, level, mode, false)
		})
	}
}

func TestTelemetryAutoWarmCache(t *testing.T) {
	t.Parallel()
	level := creationEpochLevels()[0]
	for _, profile := range cacheFirstProfiles(t) {
		for _, diagnostic := range telemetryDiagnosticModes() {
			t.Run(profile.name+"/"+diagnostic.name, func(t *testing.T) {
				env, _ := telemetryProcessEnvironment(t, profile.path, level, telemetryWarmCache, diagnostic)
				for i, setting := range env {
					if strings.HasPrefix(setting, "MICROFAT_EXEC_MODE=") {
						env[i] = "MICROFAT_EXEC_MODE=auto"
					}
				}
				stdout, stderr, err := runTelemetryProcess(t, profile.path, env, "--echo-env", "MICROFAT_EXEC_MODE")
				require.NoError(t, err, stderr)
				require.Equal(t, execModeCache, stdout)
				requireTelemetryDiagnostics(t, diagnostic, stderr, level, execModeCache, true)
			})
		}
	}
}

func TestTelemetryDisabledStillReportsErrors(t *testing.T) {
	t.Parallel()
	for _, diagnostic := range telemetryDiagnosticModes() {
		if diagnostic.output != "" {
			continue
		}
		t.Run(diagnostic.name, func(t *testing.T) {
			env, _ := telemetryProcessEnvironment(t, goldenFatBin, "invalid-level", execModeMemfd, diagnostic)
			stdout, stderr, err := runTelemetryProcess(t, goldenFatBin, env)
			require.Error(t, err)
			require.Empty(t, stdout)
			require.Contains(t, stderr, "[microfat] error:")
			require.NotRegexp(t, `decompress_us=|total_us=|"event":"dispatch"`, stderr)
		})
	}
}
