//go:build linux && !minimal

package main

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	infoMemoryKey      = "GOMEMLIMIT"
	infoCPUKey         = "GOMAXPROCS"
	infoInvalidSetting = "invalid"
	infoOffSetting     = "off"
	infoTestCeiling    = int64(1 << 30)
	infoTestHigh       = infoTestCeiling / 2
	infoTestImage      = int64(100 << 20)
	infoTestCPUs       = 4
	infoFractionalCPU  = 2.999
	infoFloorCPU       = 2
)

func clearInfoEnvironment(t *testing.T) {
	t.Helper()
	keys := []string{
		format.EnvExecMode, format.EnvDispatchMode, format.EnvMemRatio, format.EnvGCProfile,
		format.EnvLiveHeapEstimate, format.EnvCPUPolicy, format.EnvAutotune, format.EnvDryRun,
		format.EnvDebug, infoMemoryKey, infoCPUKey, "GOGC",
	}
	for _, key := range keys {
		oldValue, present := os.LookupEnv(key)
		require.NoError(t, os.Unsetenv(key))
		t.Cleanup(func() {
			if present {
				require.NoError(t, os.Setenv(key, oldValue))
			} else {
				require.NoError(t, os.Unsetenv(key))
			}
		})
	}
}

func captureInfoOutput(t *testing.T, entry *format.VariantEntry, jsonOutput bool) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "info-output")
	file, err := os.Create(path)
	require.NoError(t, err)
	oldStdout := os.Stdout
	os.Stdout = file
	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = file.Close()
	})
	require.NoError(t, printInfo(&format.Index{Variants: []format.VariantEntry{*entry}},
		microarch.Info{}, entry, microarch.PolicyResult{}, 0, jsonOutput))
	os.Stdout = oldStdout
	require.NoError(t, file.Close())
	output, err := os.ReadFile(path)
	require.NoError(t, err)
	return output
}

func infoEnvironmentValues(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, present := strings.Cut(entry, "=")
		if present {
			values[key] = value
		}
	}
	return values
}

func TestInfoTuningMatchesDispatch(t *testing.T) {
	finite := cgroup.Limits{
		CgroupVersion: cgroup.VersionV2, MemoryLimitBytes: infoTestCeiling,
		MemoryHighBytes: infoTestHigh, EffectiveMemoryLimitBytes: infoTestHigh,
		CPUQuota: infoTestCPUs, CPUs: infoTestCPUs,
	}
	fractionalCPU := finite
	fractionalCPU.CPUQuota, fractionalCPU.CPUs = infoFractionalCPU, infoFloorCPU
	for _, tc := range []struct {
		name    string
		env     map[string]string
		limits  cgroup.Limits
		readErr error
		payload int64
	}{
		{name: "effective high and custom ratio", env: map[string]string{format.EnvMemRatio: "0.5"}, limits: finite},
		{name: "precise ratio", env: map[string]string{format.EnvMemRatio: "0.5555"}, limits: finite},
		{name: "latency profile", env: map[string]string{format.EnvGCProfile: "latency_critical"}, limits: finite},
		{name: "memory profile", env: map[string]string{format.EnvGCProfile: "memory_constrained"}, limits: finite},
		{name: "adaptive profile", env: map[string]string{
			format.EnvGCProfile: "adaptive", format.EnvLiveHeapEstimate: "150MiB",
		}, limits: finite},
		{name: "batch profile", env: map[string]string{format.EnvGCProfile: string(cgroup.GCProfileBatchETL)}, limits: finite},
		{name: "batch inherited finite", env: map[string]string{
			format.EnvGCProfile: string(cgroup.GCProfileBatchETL), infoMemoryKey: "32MiB",
		}, limits: finite},
		{name: "batch inherited off", env: map[string]string{
			format.EnvGCProfile: string(cgroup.GCProfileBatchETL), infoMemoryKey: infoOffSetting,
		}, limits: finite},
		{name: "batch inherited empty", env: map[string]string{
			format.EnvGCProfile: string(cgroup.GCProfileBatchETL), infoMemoryKey: "",
		}, limits: finite},
		{name: "batch inherited invalid", env: map[string]string{
			format.EnvGCProfile: string(cgroup.GCProfileBatchETL), infoMemoryKey: infoInvalidSetting,
		}, limits: finite},
		{name: "explicit runtime overrides", env: map[string]string{
			infoMemoryKey: "384MiB", infoCPUKey: "3", "GOGC": "75",
		}, limits: finite},
		{name: "empty runtime overrides", env: map[string]string{
			infoMemoryKey: "", infoCPUKey: "", "GOGC": "",
		}, limits: finite},
		{name: "autotune off", env: map[string]string{format.EnvAutotune: " false "}, limits: finite},
		{name: "dry run", env: map[string]string{format.EnvDryRun: " TRUE "}, limits: finite},
		{name: "off takes priority", env: map[string]string{
			format.EnvAutotune: "0", format.EnvDryRun: "1",
		}, limits: finite},
		{name: "native CPU policy", env: map[string]string{format.EnvCPUPolicy: "native"}, limits: finite},
		{name: "fractional CPU quota", limits: fractionalCPU},
		{name: "invalid configuration", env: map[string]string{
			format.EnvMemRatio: infoInvalidSetting, format.EnvGCProfile: infoInvalidSetting, format.EnvCPUPolicy: infoInvalidSetting,
		}, limits: finite},
		{name: "known cgroup unresolved observations", limits: cgroup.Limits{CgroupVersion: cgroup.VersionV2}},
		{name: "unknown cgroup", env: map[string]string{infoMemoryKey: "32MiB"}},
		{name: "unavailable cgroup", readErr: errors.New("fixture observation failure"),
			env: map[string]string{infoMemoryKey: "32MiB", infoCPUKey: ""}},
		{name: "failed collection with numeric fields", limits: finite, readErr: errors.New("partial observation failure")},
		{name: "retained exceeds ceiling", limits: finite, payload: infoTestCeiling},
		{name: "page rounded retention", limits: finite, payload: infoTestImage + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearInfoEnvironment(t)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			oldRead := readCgroupLimitsFunc
			t.Cleanup(func() { readCgroupLimitsFunc = oldRead })
			readCount := 0
			readCgroupLimitsFunc = func() (cgroup.Limits, error) {
				readCount++
				return tc.limits, tc.readErr
			}
			payload := tc.payload
			if payload == 0 {
				payload = infoTestImage
			}
			entry := &format.VariantEntry{Level: "v1", UncompressedSize: payload}
			var report format.BinaryInfo
			require.NoError(t, json.Unmarshal(captureInfoOutput(t, entry, true), &report))
			require.Equal(t, 1, readCount, "all candidates must use one observation snapshot")
			require.Equal(t, format.ExecModeAuto, report.ExecMode)
			require.NotNil(t, report.Runtime)
			require.Len(t, report.Runtime.Candidates, 2)
			assert.Empty(t, report.Cgroup.GOMEMLIMIT, "auto has no single selected memory plan")
			text := string(captureInfoOutput(t, entry, false))
			if tc.readErr != nil {
				assert.Equal(t, "unavailable", report.Cgroup.Status)
				assert.Equal(t, tc.readErr.Error(), report.Cgroup.Error)
				assert.Equal(t, "unknown", report.Cgroup.MemoryLimitStatus)
				assert.Contains(t, text, "Collection Error: "+tc.readErr.Error())
			} else if tc.limits.CgroupVersion == cgroup.VersionUnknown {
				assert.Equal(t, "unknown", report.Cgroup.Status)
			}
			for _, candidate := range report.Runtime.Candidates {
				env, limits := buildAutoTunedEnviron("", os.Environ(), entry, candidate.ExecMode,
					microarch.Info{}, microarch.PolicyResult{})
				values := infoEnvironmentValues(env)
				assert.Equal(t, limits != nil, candidate.Tuning.Available)
				assert.Equal(t, values[format.EnvCgroupGOMEMLIMIT], candidate.Tuning.GOMEMLIMIT)
				assert.Equal(t, values[format.EnvCgroupGOMAXPROCS], candidate.Tuning.GOMAXPROCSValue)
				assert.Equal(t, values[format.EnvCgroupGOGC], candidate.Tuning.GOGC)
				for _, key := range runtimeEnvironmentKeys {
					value, present := values[key]
					observation := candidate.EffectiveEnvironment[key]
					assert.Equal(t, present, observation.Present, "%s/%s", candidate.ExecMode, key)
					assert.Equal(t, value, observation.Value, "%s/%s", candidate.ExecMode, key)
					if present {
						assert.Contains(t, text, fmt.Sprintf("Effective %s: %q (%s)", key, value, observation.Source))
					} else {
						assert.Contains(t, text, "Effective "+key+": unset (Go runtime default)")
					}
				}
			}
			if tc.name == "effective high and custom ratio" {
				const cacheBudget, memfdBudget = int64(268435456), int64(216006656)
				assert.Equal(t, cacheBudget, report.Runtime.Candidates[0].Tuning.GOMEMLIMITBytes)
				assert.Equal(t, memfdBudget, report.Runtime.Candidates[1].Tuning.GOMEMLIMITBytes)
				assert.Contains(t, text, strconv.FormatInt(cacheBudget, 10)+"B")
				assert.Contains(t, text, strconv.FormatInt(memfdBudget, 10)+"B")
			}
			if tc.name == "precise ratio" {
				assert.Contains(t, text, "Memory Ratio: 0.5555")
			}
			if tc.name == "fractional CPU quota" {
				assert.Contains(t, text, "CPU CFS Quota: 2.999 cores")
				assert.Equal(t, infoFloorCPU, report.Runtime.Candidates[0].Tuning.GOMAXPROCS)
			}
			if tc.name == "known cgroup unresolved observations" {
				assert.Equal(t, "collected", report.Cgroup.Status)
				assert.Equal(t, "unknown", report.Cgroup.MemoryLimitStatus)
				assert.Equal(t, "unknown", report.Cgroup.CPUQuotaStatus)
				assert.Contains(t, text, "Memory Ceiling: unknown (unavailable or unlimited)")
				assert.Contains(t, text, "CPU CFS Quota: unknown (unavailable or unlimited)")
			}
		})
	}
}

func TestInfoExplicitMemoryBudget(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want string
	}{
		{mode: format.ExecModeCache, want: "268435456B"},
		{mode: format.ExecModeMemfd, want: "216006656B"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			clearInfoEnvironment(t)
			t.Setenv(format.EnvExecMode, tc.mode)
			t.Setenv(format.EnvMemRatio, "0.5")
			oldRead := readCgroupLimitsFunc
			t.Cleanup(func() { readCgroupLimitsFunc = oldRead })
			readCgroupLimitsFunc = func() (cgroup.Limits, error) {
				return cgroup.Limits{CgroupVersion: cgroup.VersionV2, MemoryLimitBytes: infoTestCeiling,
					MemoryHighBytes: infoTestHigh, EffectiveMemoryLimitBytes: infoTestHigh}, nil
			}
			var report format.BinaryInfo
			entry := &format.VariantEntry{Level: "v1", UncompressedSize: infoTestImage}
			require.NoError(t, json.Unmarshal(captureInfoOutput(t, entry, true), &report))
			assert.Equal(t, tc.want, report.Cgroup.GOMEMLIMIT)
			assert.Contains(t, string(captureInfoOutput(t, entry, false)), tc.want)
		})
	}
}

func TestRuntimeCandidateDuplicateAndEmptyOverrides(t *testing.T) {
	clearInfoEnvironment(t)
	limits := cgroup.Limits{CgroupVersion: cgroup.VersionV2, MemoryLimitBytes: infoTestCeiling, CPUs: infoTestCPUs}
	env := []string{"GOMEMLIMIT=64MiB", "GOMEMLIMIT=", "GOMAXPROCS=3", "GOMAXPROCS=", "GOGC=75", "GOGC="}
	candidate := buildRuntimeCandidate(env, &format.VariantEntry{UncompressedSize: infoTestImage},
		limits, format.ExecModeCache, autotuneEnabled, true)
	for _, key := range runtimeEnvironmentKeys {
		assert.Equal(t, format.RuntimeEnvironmentValue{Present: true, Source: environmentInherited}, candidate.EffectiveEnvironment[key])
	}
}
