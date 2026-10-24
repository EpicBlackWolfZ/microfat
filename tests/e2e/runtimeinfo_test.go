//go:build linux

package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/runtimeinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	runtimeInfoReporterPkg = "../../runtimeinfo/testdata/reporter"
	runtimeInfoARM64Base   = "GOARM64=v8.0"
	runtimeInfoMemory      = uint64(384 << 20)
	runtimeInfoMaxProcs    = uint64(2)
	runtimeInfoGCPercent   = uint64(75)
	runtimeInfoNewMemory   = uint64(128 << 20)
	runtimeInfoNewMaxProcs = uint64(3)
)

func runtimeInfoProducts(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	reporter := filepath.Join(dir, "runtimeinfo-reporter")
	require.NoError(t, compileBinary(runtimeInfoReporterPkg, reporter, []string{envStatic, envBaselineAMD64, runtimeInfoARM64Base}))
	minimalStub := filepath.Join(dir, "microfat-stub-minimal")
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, minimalStub,
		[]string{envStatic, envBaselineAMD64, runtimeInfoARM64Base}, "-tags=minimal", "-ldflags=-s -w"))
	baseLevel, _ := hostVariantLabels()
	fat := make(map[string]string)
	for profile, stub := range map[string]string{launcherFullProfile: stubPath, launcherMinimalProfile: minimalStub} {
		fat[profile] = filepath.Join(dir, "runtimeinfo-"+profile+".fat")
		require.NoError(t, packBinary(cliPath, stub, "runtimeinfo", fat[profile], map[string]string{baseLevel: reporter}))
	}
	return reporter, fat
}

func runRuntimeInfoReporter(t *testing.T, path string, env []string, args ...string) runtimeinfo.Snapshot {
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
	require.NoError(t, ctx.Err(), "runtimeinfo reporter blocked: %s", stderr.String())
	require.NoError(t, err, "runtimeinfo reporter failed: %s", stderr.String())
	var snapshot runtimeinfo.Snapshot
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &snapshot), "reporter output: %s", stdout.String())
	return snapshot
}

func runtimeInfoEnvironment(t *testing.T, overrides ...string) []string {
	t.Helper()
	defaults := []string{"GOMEMLIMIT=384MiB", "GOMAXPROCS=2", "GOGC=75"}
	return stubInfoProcessEnvironment(filepath.Join(t.TempDir(), "cache"), append(defaults, overrides...)...)
}

func assertRuntimeInfoField[T any](t *testing.T, field runtimeinfo.Field[T], want T) {
	t.Helper()
	assert.Equal(t, runtimeinfo.StatusPresent, field.Status)
	assert.Equal(t, want, field.Value)
	assert.NotEmpty(t, field.Source)
}

func assertRuntimeInfoObserved(t *testing.T, observed runtimeinfo.ObservedRuntime, memory, procs, gc uint64) {
	t.Helper()
	assertRuntimeInfoField(t, observed.GOMEMLIMITBytes, memory)
	assertRuntimeInfoField(t, observed.GOMAXPROCS, procs)
	assertRuntimeInfoField(t, observed.GOGCPercent, gc)
	assert.Equal(t, "runtime/metrics:/gc/gomemlimit:bytes", observed.GOMEMLIMITBytes.Source)
	assert.Equal(t, "runtime/metrics:/sched/gomaxprocs:threads", observed.GOMAXPROCS.Source)
	assert.Equal(t, "runtime/metrics:/gc/gogc:percent", observed.GOGCPercent.Source)
	assert.Empty(t, observed.GOMEMLIMITBytes.Raw)
	assert.Empty(t, observed.GOMAXPROCS.Raw)
	assert.Empty(t, observed.GOGCPercent.Raw)
}

func assertRuntimeInfoExplicitEnvironment(t *testing.T, configured runtimeinfo.ConfiguredEnvironment) {
	t.Helper()
	assertRuntimeInfoField(t, configured.GOMEMLIMIT, "384MiB")
	assertRuntimeInfoField(t, configured.GOMAXPROCS, "2")
	assertRuntimeInfoField(t, configured.GOGC, "75")
	assert.Equal(t, "384MiB", configured.GOMEMLIMIT.Raw)
	assert.Equal(t, "2", configured.GOMAXPROCS.Raw)
	assert.Equal(t, "75", configured.GOGC.Raw)
	assert.Equal(t, "GOMEMLIMIT", configured.GOMEMLIMIT.Source)
	assert.Equal(t, "GOMAXPROCS", configured.GOMAXPROCS.Source)
	assert.Equal(t, "GOGC", configured.GOGC.Source)
}

func TestRuntimeInfoNativeAndDispatchedProcesses(t *testing.T) {
	t.Parallel()
	reporter, fat := runtimeInfoProducts(t)
	payload, err := os.ReadFile(reporter)
	require.NoError(t, err)
	digest := sha256.Sum256(payload)
	baseLevel, _ := hostVariantLabels()
	nativeDefaults := runRuntimeInfoReporter(t, reporter, stubInfoProcessEnvironment(t.TempDir()))
	cases := []struct {
		name, path, mode string
	}{
		{name: execModeNative, path: reporter},
	}
	for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
		for _, mode := range []string{execModeMemfd, execModeCache} {
			cases = append(cases, struct{ name, path, mode string }{
				name: profile + "/" + mode, path: fat[profile], mode: mode,
			})
		}
	}
	for _, tc := range cases {
		for _, tuning := range []struct{ name, key, value string }{
			{name: "AutotuneDisabled", key: "MICROFAT_AUTOTUNE", value: "0"},
			{name: "DryRun", key: "MICROFAT_DRY_RUN", value: "1"},
		} {
			t.Run(tc.name+"/"+tuning.name, func(t *testing.T) {
				t.Parallel()
				env := runtimeInfoEnvironment(t, tuning.key+"="+tuning.value)
				if tc.mode != "" {
					env = append(env, "MICROFAT_EXEC_MODE="+tc.mode)
				}
				snapshot := runRuntimeInfoReporter(t, tc.path, env)
				assertRuntimeInfoExplicitEnvironment(t, snapshot.Configured)
				assertRuntimeInfoObserved(t, snapshot.Observed, runtimeInfoMemory, runtimeInfoMaxProcs, runtimeInfoGCPercent)
				// With no explicit Go overrides, disabled tuning and dry-run must leave
				// actual runtime settings equal to a freshly started native Go process.
				unsetEnv := stubInfoProcessEnvironment(t.TempDir(), tuning.key+"="+tuning.value)
				if tc.mode != "" {
					unsetEnv = append(unsetEnv, "MICROFAT_EXEC_MODE="+tc.mode)
				}
				untuned := runRuntimeInfoReporter(t, tc.path, unsetEnv)
				assert.Equal(t, runtimeinfo.StatusMissing, untuned.Configured.GOMEMLIMIT.Status)
				assert.Equal(t, runtimeinfo.StatusMissing, untuned.Configured.GOMAXPROCS.Status)
				assert.Equal(t, runtimeinfo.StatusMissing, untuned.Configured.GOGC.Status)
				assert.Equal(t, nativeDefaults.Observed, untuned.Observed)
				if tuning.key == "MICROFAT_AUTOTUNE" {
					assertRuntimeInfoField(t, snapshot.Configured.Autotune, tuning.value)
				} else {
					assertRuntimeInfoField(t, snapshot.Configured.DryRun, tuning.value)
				}
				if tc.mode == "" {
					assert.Equal(t, runtimeinfo.StatusMissing, snapshot.Launcher.SelectedVariant.Status)
					assert.Equal(t, runtimeinfo.StatusMissing, snapshot.Launcher.HostArch.Status)
					assert.Equal(t, runtimeinfo.StatusMissing, snapshot.Launcher.ExecMode.Status)
					assert.Equal(t, runtimeinfo.StatusMissing, snapshot.Launcher.SelectedSHA256.Status)
					assert.Equal(t, runtimeinfo.StatusMissing, snapshot.Launcher.SelectedSizeBytes.Status)
					assert.Equal(t, runtimeinfo.StatusMissing, snapshot.Launcher.OriginalExecutable.Status)
					assert.Equal(t, runtimeinfo.StatusMissing, snapshot.Launcher.Cgroup.Version.Status)
					return
				}
				assertRuntimeInfoField(t, snapshot.Launcher.SelectedVariant, baseLevel)
				assertRuntimeInfoField(t, snapshot.Launcher.HostArch, currentHostArch)
				assertRuntimeInfoField(t, snapshot.Launcher.HostLevel, currentHostLevel)
				assertRuntimeInfoField(t, snapshot.Launcher.ExecMode, tc.mode)
				assertRuntimeInfoField(t, snapshot.Launcher.DispatchMode, tc.mode)
				assertRuntimeInfoField(t, snapshot.Launcher.SelectedSHA256, hex.EncodeToString(digest[:]))
				assertRuntimeInfoField(t, snapshot.Launcher.SelectedSizeBytes, int64(len(payload)))
				assertRuntimeInfoField(t, snapshot.Launcher.OriginalExecutable, tc.path)
			})
		}
	}
}

func TestRuntimeInfoConfiguredEnvironmentDiffersFromRunningRuntime(t *testing.T) {
	t.Parallel()
	reporter := filepath.Join(t.TempDir(), "runtimeinfo-reporter")
	require.NoError(t, compileBinary(runtimeInfoReporterPkg, reporter, []string{envStatic}))
	tests := []struct {
		name   string
		args   []string
		memory uint64
		procs  uint64
		gc     uint64
		config [3]string
	}{
		{name: "EnvironmentChangedAfterStartup", args: []string{
			"-set-env", "GOMEMLIMIT=768MiB", "-set-env", "GOMAXPROCS=7", "-set-env", "GOGC=25",
		}, memory: runtimeInfoMemory, procs: runtimeInfoMaxProcs, gc: runtimeInfoGCPercent,
			config: [3]string{"768MiB", "7", "25"}},
		{name: "RuntimeChangedWithoutEnvironment", args: []string{
			"-memory-limit", "134217728", "-maxprocs", "3", "-gc-percent", "-1",
		}, memory: runtimeInfoNewMemory, procs: runtimeInfoNewMaxProcs, gc: math.MaxUint64,
			config: [3]string{"384MiB", "2", "75"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			snapshot := runRuntimeInfoReporter(t, reporter, runtimeInfoEnvironment(t), tc.args...)
			assertRuntimeInfoField(t, snapshot.Configured.GOMEMLIMIT, tc.config[0])
			assertRuntimeInfoField(t, snapshot.Configured.GOMAXPROCS, tc.config[1])
			assertRuntimeInfoField(t, snapshot.Configured.GOGC, tc.config[2])
			assertRuntimeInfoObserved(t, snapshot.Observed, tc.memory, tc.procs, tc.gc)
		})
	}
}
