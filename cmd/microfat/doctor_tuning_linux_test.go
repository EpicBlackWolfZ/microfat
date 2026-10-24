//go:build linux

package main

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/runtimeinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorTuningRejectsInputsBeforeProbes(t *testing.T) {
	originalDetect, originalRead := microarchDetectFunc, readCgroupLimitsFunc
	t.Cleanup(func() { microarchDetectFunc, readCgroupLimitsFunc = originalDetect, originalRead })
	microarchDetectFunc = func() microarch.Info {
		t.Fatal("CPU probe ran before input validation")
		return microarch.Info{}
	}
	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		t.Fatal("cgroup probe ran before input validation")
		return cgroup.Limits{}, nil
	}
	t.Setenv(format.EnvGCProfile, "default")
	t.Setenv(format.EnvLiveHeapEstimate, "0")
	for _, args := range [][]string{
		{"--gc-profile=unknown"}, {"--live-heap-estimate=-1MiB"}, {"--live-heap-estimate=9223372036854775808"},
		{"--live-heap-estimate="}, {"--live-heap-estimate=NaN"},
	} {
		t.Run(args[0], func(t *testing.T) {
			cmd := newDoctorCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(append([]string{"--json"}, args...))
			require.Error(t, cmd.Execute())
			assert.Empty(t, out.String())
		})
	}
	t.Run("invalid inherited profile", func(t *testing.T) {
		t.Setenv(format.EnvGCProfile, "unknown")
		cmd := newDoctorCmd()
		cmd.SetErr(io.Discard)
		require.ErrorContains(t, cmd.Execute(), format.EnvGCProfile)
	})
}

func TestDoctorTuningCommandDoesNotApplySettings(t *testing.T) {
	originalRead := readCgroupLimitsFunc
	t.Cleanup(func() { readCgroupLimitsFunc = originalRead })
	reads := 0
	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		reads++
		return cgroup.Limits{CgroupVersion: cgroup.VersionV2, MemoryLimitBytes: bytesInGiB, CPUs: 2}, nil
	}
	t.Setenv(format.EnvGCProfile, "invalid-but-overridden")
	t.Setenv(format.EnvLiveHeapEstimate, "invalid-but-overridden")
	t.Setenv("GOMEMLIMIT", "")
	t.Setenv("GOMAXPROCS", "3")
	t.Setenv("GOGC", "off")
	t.Setenv(format.EnvAutotune, "1")
	t.Setenv(format.EnvDryRun, "0")
	t.Setenv(format.EnvCPUPolicy, "static")
	t.Setenv(format.EnvMemRatio, "0.75")
	cacheDir := filepath.Join(t.TempDir(), "existing-cache")
	require.NoError(t, os.Mkdir(cacheDir, 0o755))
	beforeInfo, err := os.Stat(cacheDir)
	require.NoError(t, err)
	beforeRuntime, beforeEnv := runtimeinfo.Read().Observed, os.Environ()

	cmd := newDoctorCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json", "--cache-dir", cacheDir, "--gc-profile=adaptive", "--live-heap-estimate=150MiB"})
	require.NoError(t, cmd.Execute())
	var report DoctorReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	require.NotNil(t, report.TuningPreview)
	assert.False(t, report.TuningPreview.Applied)
	assert.Equal(t, "adaptive", report.TuningPreview.Tuning.GCProfile)
	assert.Equal(t, "--gc-profile", report.TuningPreview.Inputs.GCProfile.Source)
	assert.Equal(t, 1, reads, "observations and preview must share one cgroup read")
	assert.Equal(t, beforeRuntime, runtimeinfo.Read().Observed)
	assert.Equal(t, beforeEnv, os.Environ())
	afterInfo, err := os.Stat(cacheDir)
	require.NoError(t, err)
	assert.Equal(t, beforeInfo.Mode(), afterInfo.Mode())
	entries, err := os.ReadDir(cacheDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
	for _, key := range []string{"GOMEMLIMIT", "GOMAXPROCS", "GOGC"} {
		value := report.TuningPreview.EffectiveEnvironment[key]
		assert.True(t, value.Present)
		assert.Equal(t, "inherited", value.Source)
		assert.Equal(t, os.Getenv(key), value.Value)
	}
	var text bytes.Buffer
	require.NoError(t, printDoctorReport(&text, &report))
	assert.Contains(t, text.String(), "Candidate GOGC:")
	assert.Contains(t, text.String(), "Supplied Heap:")
	assert.Contains(t, text.String(), "not measured")
}

func TestDoctorTuningCgroupReadFailure(t *testing.T) {
	originalRead := readCgroupLimitsFunc
	t.Cleanup(func() { readCgroupLimitsFunc = originalRead })
	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{CgroupVersion: cgroup.VersionV2, MemoryLimitBytes: bytesInGiB}, errors.New("unreadable cgroup")
	}
	report, preview := probeDoctorCgroup(doctorTuningInputs{})
	assert.False(t, report.Detected)
	assert.Equal(t, "unavailable", report.Status)
	assert.Equal(t, "unreadable cgroup", report.Error)
	assert.False(t, preview.Tuning.Available)
	assert.Empty(t, preview.Tuning.GOMEMLIMIT)
}
