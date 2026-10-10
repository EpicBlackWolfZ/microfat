package runtimeinit

import (
	"bytes"
	json "encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	cpuPolicyTestMemoryLimit = int64(1024 * 1024 * 1024)
	cpuPolicyTestCPUs        = 2
	cpuPolicyTestGOGC        = 75
	cpuPolicyTestManualCPUs  = 7
)

func TestWithCPUPolicy(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		policy CPUPolicy
		want   CPUPolicy
	}{
		{name: "Static", policy: CPUPolicyStatic, want: CPUPolicyStatic},
		{name: "Native", policy: CPUPolicyNative, want: CPUPolicyNative},
		{name: "NormalizedNative", policy: " NATIVE ", want: CPUPolicyNative},
		{name: "EmptyDefaultsToStatic", want: CPUPolicyStatic},
		{name: "InvalidDefaultsToStatic", policy: "other", want: CPUPolicyStatic},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultConfig()
			assert.Equal(t, CPUPolicyStatic, cfg.cpuPolicy)
			WithCPUPolicy(tc.policy)(cfg)
			assert.Equal(t, tc.want, cfg.cpuPolicy)
		})
	}
}

func TestAutoTune_CPUPolicy(t *testing.T) {
	useZeroRetainedExecutableMemory(t)

	testCases := []struct {
		name          string
		policy        CPUPolicy
		envPolicy     string
		maxProcsEnv   string
		dryRun        bool
		wantPolicy    CPUPolicy
		wantCPUPlan   bool
		wantCPUSetter bool
	}{
		{name: "DefaultStatic", wantPolicy: CPUPolicyStatic, wantCPUPlan: true, wantCPUSetter: true},
		{name: "NativeOption", policy: CPUPolicyNative, wantPolicy: CPUPolicyNative},
		{name: "NativeEnvironment", envPolicy: string(CPUPolicyNative), wantPolicy: CPUPolicyNative},
		{
			name: "NativeEnvironmentOverridesStatic", policy: CPUPolicyStatic,
			envPolicy: string(CPUPolicyNative), wantPolicy: CPUPolicyNative,
		},
		{
			name: "StaticEnvironmentOverridesNative", policy: CPUPolicyNative, envPolicy: "static",
			wantPolicy: CPUPolicyStatic, wantCPUPlan: true, wantCPUSetter: true,
		},
		{name: "NormalizedEnvironment", envPolicy: " NaTiVe \t", wantPolicy: CPUPolicyNative},
		{name: "InvalidEnvironmentPreservesNative", policy: CPUPolicyNative, envPolicy: "other", wantPolicy: CPUPolicyNative},
		{
			name: "InvalidEnvironmentPreservesStaticDefault", envPolicy: "other",
			wantPolicy: CPUPolicyStatic, wantCPUPlan: true, wantCPUSetter: true,
		},
		{
			name: "StaticRespectsExplicitGOMAXPROCS", maxProcsEnv: "7",
			wantPolicy: CPUPolicyStatic,
		},
		{
			name: "NativeRespectsExplicitGOMAXPROCS", policy: CPUPolicyNative, maxProcsEnv: "7",
			wantPolicy: CPUPolicyNative,
		},
		{
			name: "NativeDryRun", policy: CPUPolicyNative, dryRun: true,
			wantPolicy: CPUPolicyNative,
		},
		{
			name: "DefaultStaticDryRun", dryRun: true,
			wantPolicy: CPUPolicyStatic, wantCPUPlan: true,
		},
		{
			name: "NativeExplicitGOMAXPROCSDryRun", envPolicy: string(CPUPolicyNative), maxProcsEnv: "7", dryRun: true,
			wantPolicy: CPUPolicyNative,
		},
	}

	limits := cpuPolicyTestLimits()
	wantMemory, ok := cgroup.CalculateGOMEMLIMIT(
		cpuPolicyTestMemoryLimit, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes,
	)
	require.True(t, ok)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{format.EnvCPUPolicy: tc.envPolicy, "GOMAXPROCS": tc.maxProcsEnv}
			withIsolatedEnv(t, env, &limits, nil, func(memLimit *int64, maxProcs, gogc *int, _ *bytes.Buffer) {
				opts := []Option{WithProfile(ProfileLatencyCritical), WithDryRun(tc.dryRun)}
				if tc.policy != "" {
					opts = append(opts, WithCPUPolicy(tc.policy))
				}
				res := AutoTune(opts...)
				assert.Equal(t, tc.wantPolicy, res.CPUPolicy)
				assert.Equal(t, tc.wantCPUSetter, res.MaxProcsApplied)
				if tc.wantCPUPlan {
					assert.Equal(t, cpuPolicyTestCPUs, res.GOMAXPROCS)
				} else {
					assert.Zero(t, res.GOMAXPROCS)
				}
				if tc.wantCPUSetter {
					assert.Equal(t, cpuPolicyTestCPUs, *maxProcs)
				} else {
					assert.Equal(t, -1, *maxProcs, "CPU setter must not be invoked")
				}
				assert.Equal(t, wantMemory, res.GOMEMLIMIT, "memory planning is independent of CPU policy")
				assert.Equal(t, cpuPolicyTestGOGC, res.GOGC, "GC planning is independent of CPU policy")
				assert.Equal(t, !tc.dryRun, res.MemLimitApplied)
				assert.Equal(t, !tc.dryRun, res.GOGCApplied)
				if tc.dryRun {
					assert.Equal(t, int64(-1), *memLimit)
					assert.Equal(t, -999, *gogc)
				} else {
					assert.Equal(t, wantMemory, *memLimit)
					assert.Equal(t, cpuPolicyTestGOGC, *gogc)
				}
			})
		})
	}
}

func TestAutoTune_CPUPolicyNativeRepeatedInitialization(t *testing.T) {
	useZeroRetainedExecutableMemory(t)
	limits := cpuPolicyTestLimits()
	withIsolatedEnv(t, nil, &limits, nil, func(_ *int64, maxProcs, _ *int, _ *bytes.Buffer) {
		initial := AutoTune()
		require.True(t, initial.MaxProcsApplied)
		require.Equal(t, cpuPolicyTestCPUs, *maxProcs)

		// Simulate a later explicit application choice. Native initialization must
		// leave that choice intact rather than restoring the runtime's default.
		*maxProcs = cpuPolicyTestManualCPUs
		for range 2 {
			res := AutoTune(WithCPUPolicy(CPUPolicyNative))
			assert.Equal(t, CPUPolicyNative, res.CPUPolicy)
			assert.False(t, res.MaxProcsApplied)
			assert.Zero(t, res.GOMAXPROCS)
			assert.Equal(t, cpuPolicyTestManualCPUs, *maxProcs)
			assert.True(t, res.MemLimitApplied)
		}
	})
}

func TestAutoTune_CPUPolicyNativeDiagnostics(t *testing.T) {
	useZeroRetainedExecutableMemory(t)
	limits := cpuPolicyTestLimits()

	t.Run("JSON", func(t *testing.T) {
		env := map[string]string{format.EnvCPUPolicy: string(CPUPolicyNative), format.EnvLog: "json"}
		withIsolatedEnv(t, env, &limits, nil, func(_ *int64, _ *int, _ *int, stderr *bytes.Buffer) {
			res := AutoTune()
			var telem Telemetry
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(stderr.String(), "[microfat] ")), &telem))
			assert.Equal(t, res.CPUPolicy, telem.CPUPolicy)
			assert.Equal(t, CPUPolicyNative, telem.CPUPolicy)
			assert.Empty(t, telem.GOMAXPROCS)
			assert.False(t, telem.MaxProcsApplied)
			assert.NotEmpty(t, telem.GOMEMLIMIT)
		})
	})

	t.Run("Logger", func(t *testing.T) {
		withIsolatedEnv(t, nil, &limits, nil, func(_ *int64, _ *int, _ *int, _ *bytes.Buffer) {
			var logged string
			logger := func(format string, args ...any) { logged = fmt.Sprintf(format, args...) }
			AutoTune(WithCPUPolicy(CPUPolicyNative), WithLogger(logger))
			assert.Contains(t, logged, "cpu_policy=native")
		})
	})

	t.Run("Debug", func(t *testing.T) {
		env := map[string]string{format.EnvCPUPolicy: string(CPUPolicyNative), format.EnvDebug: "1"}
		withIsolatedEnv(t, env, &limits, nil, func(_ *int64, _ *int, _ *int, stderr *bytes.Buffer) {
			AutoTune()
			assert.Contains(t, stderr.String(), "cpu_policy=native")
		})
	})

	t.Run("Disabled", func(t *testing.T) {
		env := map[string]string{format.EnvCPUPolicy: string(CPUPolicyNative), format.EnvAutotune: "0"}
		withIsolatedEnv(t, env, &limits, nil, func(_ *int64, _ *int, _ *int, _ *bytes.Buffer) {
			assert.Equal(t, CPUPolicyNative, AutoTune().CPUPolicy)
		})
	})

	t.Run("NoCgroup", func(t *testing.T) {
		unknown := cgroup.Limits{CgroupVersion: cgroup.VersionUnknown}
		withIsolatedEnv(t, nil, &unknown, nil, func(_ *int64, _ *int, _ *int, _ *bytes.Buffer) {
			assert.Equal(t, CPUPolicyNative, AutoTune(WithCPUPolicy(CPUPolicyNative)).CPUPolicy)
		})
	})
}

func cpuPolicyTestLimits() cgroup.Limits {
	return cgroup.Limits{
		CgroupVersion:    cgroup.VersionV2,
		MemoryLimitBytes: cpuPolicyTestMemoryLimit,
		CPUQuota:         cpuPolicyTestCPUs,
		CPUs:             cpuPolicyTestCPUs,
	}
}

func useZeroRetainedExecutableMemory(t *testing.T) {
	t.Helper()
	original := retainedExecutableMemoryFunc
	retainedExecutableMemoryFunc = func() (int64, error) { return 0, nil }
	t.Cleanup(func() { retainedExecutableMemoryFunc = original })
}
