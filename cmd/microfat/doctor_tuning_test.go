package main

import (
	"math"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/runtimeinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	doctorTuningMiB         = int64(1024 * 1024)
	doctorTuningGiB         = 1024 * doctorTuningMiB
	doctorTuningDefault     = string(cgroup.GCProfileDefault)
	doctorTuningHeapFlag    = "--live-heap-estimate"
	doctorTuningProfileFlag = "--gc-profile"
	doctorTuningMemoryEnv   = "GOMEMLIMIT"
	doctorTuningCPUEnv      = "GOMAXPROCS"
	doctorTuningGCEnv       = "GOGC"
	doctorTuningInherited   = "inherited"
	doctorTuningGCOff       = "off"
	doctorTuningInvalid     = "invalid"
)

func doctorTuningLookup(values map[string]string) doctorLookupEnv {
	return func(key string) (string, bool) {
		value, present := values[key]
		return value, present
	}
}

func TestResolveDoctorTuningInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		env                 map[string]string
		profile, heap       string
		profileSet, heapSet bool
		wantProfile         string
		wantProfileRaw      string
		wantProfileSource   string
		wantHeap            int64
		wantHeapRaw         string
		wantHeapSource      string
		wantHeapStatus      runtimeinfo.Status
	}{
		{
			name: "defaults preserve missing heap", wantProfile: doctorTuningDefault, wantProfileSource: doctorTuningDefault,
			wantHeapSource: format.EnvLiveHeapEstimate, wantHeapStatus: runtimeinfo.StatusMissing,
		},
		{
			name: "environment aliases and units", env: map[string]string{
				format.EnvGCProfile: "LATENCY-CRITICAL", format.EnvLiveHeapEstimate: "1.5MiB",
			},
			wantProfile: "latency_critical", wantProfileRaw: "LATENCY-CRITICAL", wantProfileSource: format.EnvGCProfile,
			wantHeap: doctorTuningMiB + doctorTuningMiB/2, wantHeapRaw: "1.5MiB",
			wantHeapSource: format.EnvLiveHeapEstimate, wantHeapStatus: runtimeinfo.StatusPresent,
		},
		{
			name: "flags override invalid environment", env: map[string]string{
				format.EnvGCProfile: doctorTuningInvalid, format.EnvLiveHeapEstimate: "-1B",
			},
			profile: "memory", heap: "2GiB", profileSet: true, heapSet: true,
			wantProfile: "memory_constrained", wantProfileRaw: "memory", wantProfileSource: doctorTuningProfileFlag,
			wantHeap: 2 * doctorTuningGiB, wantHeapRaw: "2GiB", wantHeapSource: doctorTuningHeapFlag,
			wantHeapStatus: runtimeinfo.StatusPresent,
		},
		{
			name: "explicit empty profile selects default", profileSet: true,
			env:         map[string]string{format.EnvGCProfile: "batch"},
			wantProfile: doctorTuningDefault, wantProfileSource: doctorTuningProfileFlag,
			wantHeapSource: format.EnvLiveHeapEstimate, wantHeapStatus: runtimeinfo.StatusMissing,
		},
		{
			name: "present empty environment profile", env: map[string]string{format.EnvGCProfile: ""},
			wantProfile: doctorTuningDefault, wantProfileSource: format.EnvGCProfile,
			wantHeapSource: format.EnvLiveHeapEstimate, wantHeapStatus: runtimeinfo.StatusMissing,
		},
		{
			name: "explicit zero heap is present", profile: "adaptive", profileSet: true, heap: "0B", heapSet: true,
			wantProfile: "adaptive", wantProfileRaw: "adaptive", wantProfileSource: doctorTuningProfileFlag,
			wantHeapRaw: "0B", wantHeapSource: doctorTuningHeapFlag, wantHeapStatus: runtimeinfo.StatusPresent,
		},
		{
			name: "maximum int64 heap remains exact", env: map[string]string{format.EnvLiveHeapEstimate: "9223372036854775807B"},
			wantProfile: doctorTuningDefault, wantProfileSource: doctorTuningDefault, wantHeap: math.MaxInt64,
			wantHeapRaw: "9223372036854775807B", wantHeapSource: format.EnvLiveHeapEstimate,
			wantHeapStatus: runtimeinfo.StatusPresent,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inputs, err := resolveDoctorTuningInputs(tc.profile, tc.heap, tc.profileSet, tc.heapSet, doctorTuningLookup(tc.env))
			require.NoError(t, err)
			assert.Equal(t, runtimeinfo.Field[string]{
				Value: tc.wantProfile, Raw: tc.wantProfileRaw, Source: tc.wantProfileSource, Status: runtimeinfo.StatusPresent,
			}, inputs.GCProfile)
			assert.Equal(t, runtimeinfo.Field[int64]{
				Value: tc.wantHeap, Raw: tc.wantHeapRaw, Source: tc.wantHeapSource, Status: tc.wantHeapStatus,
			}, inputs.LiveHeapEstimateBytes)
		})
	}
}

func TestResolveDoctorTuningInputsRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, profile, heap string
		profileSet, heapSet bool
		env                 map[string]string
		wantSource          string
	}{
		{name: "unknown flag profile", profile: "future", profileSet: true, wantSource: doctorTuningProfileFlag},
		{name: "unknown environment profile", env: map[string]string{format.EnvGCProfile: "future"}, wantSource: format.EnvGCProfile},
		{name: "malformed heap", heap: "not-bytes", heapSet: true, wantSource: doctorTuningHeapFlag},
		{name: "negative heap", heap: "-1B", heapSet: true, wantSource: doctorTuningHeapFlag},
		{name: "overflowing heap", heap: "9223372036854775808B", heapSet: true, wantSource: doctorTuningHeapFlag},
		{name: "overflowing unit", heap: "9223372036854775807GiB", heapSet: true, wantSource: doctorTuningHeapFlag},
		{name: "explicit empty heap", heapSet: true, wantSource: doctorTuningHeapFlag},
		{
			name: "malformed environment heap", env: map[string]string{format.EnvLiveHeapEstimate: "bogus"},
			wantSource: format.EnvLiveHeapEstimate,
		},
		{
			name: "empty environment heap", env: map[string]string{format.EnvLiveHeapEstimate: ""},
			wantSource: format.EnvLiveHeapEstimate,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveDoctorTuningInputs(tc.profile, tc.heap, tc.profileSet, tc.heapSet, doctorTuningLookup(tc.env))
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.wantSource+":")
		})
	}
}

func TestDoctorTuningPreviewSharedProfiles(t *testing.T) {
	t.Parallel()
	limits := cgroup.Limits{CgroupVersion: cgroup.VersionV2, MemoryLimitBytes: doctorTuningGiB, CPUQuota: 2.75}
	tests := []struct {
		profile  cgroup.GCProfile
		wantGOGC string
	}{
		{cgroup.GCProfileDefault, ""},
		{cgroup.GCProfileLatencyCritical, "75"},
		{cgroup.GCProfileMemoryConstrained, "40"},
		{cgroup.GCProfileBatchETL, doctorTuningGCOff},
		{cgroup.GCProfileAdaptive, "100"},
	}
	for _, tc := range tests {
		t.Run(string(tc.profile), func(t *testing.T) {
			t.Parallel()
			inputs, err := resolveDoctorTuningInputs(string(tc.profile), "128MiB", true, true, doctorTuningLookup(nil))
			require.NoError(t, err)
			preview := buildDoctorTuningPreview(limits, true, inputs, doctorTuningLookup(nil))
			plan := cgroup.ResolveTuningPlanWithProfile(limits, "", cgroup.DefaultMemoryRatio,
				cgroup.DefaultMinHeadroomBytes, tc.profile, 128*doctorTuningMiB)
			assert.Equal(t, format.RuntimeTuningInfo{
				Available: true, GOMEMLIMIT: plan.GOMEMLIMITStr, GOMEMLIMITBytes: plan.GOMEMLIMITBytes,
				GOMAXPROCS: plan.GOMAXPROCS, GOMAXPROCSValue: plan.GOMAXPROCSStr, GOGC: plan.GOGCStr,
				GCProfile: string(plan.GCProfile), AppliedRatio: plan.AppliedRatio, ConstrainingLimit: plan.ConstrainingLimit,
				GOGCSkippedReason: plan.GOGCSkippedReason,
			}, preview.Tuning)
			assert.Equal(t, tc.wantGOGC, preview.Tuning.GOGC)
			assert.False(t, preview.Applied)
			assert.Equal(t, inputs, preview.Inputs)
			assert.Equal(t, "enabled", preview.Autotune)
			assert.Equal(t, cgroup.CPUPolicyStatic, preview.CPUPolicy)
			assert.Equal(t, "2", preview.EffectiveEnvironment[doctorTuningCPUEnv].Value)
			assert.Contains(t, preview.Notice, "not measured")
			assert.Contains(t, preview.Notice, "not an RSS cap")
			assert.Contains(t, preview.Notice, "retained executable memory is excluded")
		})
	}
}

func TestDoctorTuningPreviewMemoryBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		limits         cgroup.Limits
		unavailable    bool
		wantMemory     string
		wantConstraint string
	}{
		{name: "finite hard limit", limits: cgroup.Limits{MemoryLimitBytes: doctorTuningGiB},
			wantMemory: "966367641B", wantConstraint: cgroup.LimitConstraintMax},
		{name: "lower high boundary", limits: cgroup.Limits{MemoryLimitBytes: doctorTuningGiB, MemoryHighBytes: 512 * doctorTuningMiB},
			wantMemory: "469762048B", wantConstraint: cgroup.LimitConstraintHigh},
		{name: "finite zero hard limit", limits: cgroup.Limits{MemoryLimitFinite: true},
			wantMemory: "0B", wantConstraint: cgroup.LimitConstraintMax},
		{name: "finite zero high boundary", limits: cgroup.Limits{MemoryLimitBytes: doctorTuningGiB, MemoryHighFinite: true},
			wantMemory: "0B", wantConstraint: cgroup.LimitConstraintHigh},
		{name: "exhausted tiny budget", limits: cgroup.Limits{MemoryLimitBytes: 1}},
		{name: "unlimited", limits: cgroup.Limits{CgroupVersion: cgroup.VersionV2}},
		{name: "unavailable ignores stale limits", limits: cgroup.Limits{MemoryLimitBytes: doctorTuningGiB, CPUs: 4}, unavailable: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			preview := buildDoctorTuningPreview(tc.limits, !tc.unavailable, doctorTuningInputs{}, doctorTuningLookup(nil))
			assert.Equal(t, !tc.unavailable, preview.Tuning.Available)
			assert.Equal(t, tc.wantMemory, preview.Tuning.GOMEMLIMIT)
			assert.Equal(t, tc.wantConstraint, preview.Tuning.ConstrainingLimit)
			assert.Equal(t, tc.wantMemory != "", preview.EffectiveEnvironment[doctorTuningMemoryEnv].Present)
			assert.Equal(t, tc.wantMemory, preview.EffectiveEnvironment[doctorTuningMemoryEnv].Value)
			if !tc.unavailable {
				plan := cgroup.ResolveTuningPlanWithProfile(tc.limits, "", cgroup.DefaultMemoryRatio,
					cgroup.DefaultMinHeadroomBytes, cgroup.GCProfileDefault, 0)
				assert.Equal(t, plan.GOMEMLIMITStr, preview.Tuning.GOMEMLIMIT)
				assert.Equal(t, plan.GOMEMLIMITBytes, preview.Tuning.GOMEMLIMITBytes)
				assert.Equal(t, plan.GOMAXPROCSStr, preview.Tuning.GOMAXPROCSValue)
				assert.Equal(t, plan.GOGCStr, preview.Tuning.GOGC)
			}
			assert.False(t, preview.Applied)
			assert.Equal(t, doctorTuningDefault, preview.Inputs.GCProfile.Source)
			assert.Equal(t, runtimeinfo.StatusMissing, preview.Inputs.LiveHeapEstimateBytes.Status)
			if tc.unavailable {
				for _, key := range []string{doctorTuningMemoryEnv, doctorTuningCPUEnv, doctorTuningGCEnv} {
					assert.False(t, preview.EffectiveEnvironment[key].Present)
					assert.Contains(t, preview.Reasons[key], "discovery unavailable")
				}
			}
		})
	}
}

func TestDoctorTuningPreviewInheritedValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", doctorTuningInvalid, " 80 ", doctorTuningGCOff} {
		for _, available := range []bool{false, true} {
			t.Run(value+map[bool]string{false: "/unavailable", true: "/available"}[available], func(t *testing.T) {
				t.Parallel()
				env := map[string]string{doctorTuningMemoryEnv: value, doctorTuningCPUEnv: value, doctorTuningGCEnv: value}
				preview := buildDoctorTuningPreview(cgroup.Limits{MemoryLimitBytes: doctorTuningGiB, CPUs: 2},
					available, doctorTuningInputs{}, doctorTuningLookup(env))
				for key := range env {
					assert.Equal(t, format.RuntimeEnvironmentValue{Present: true, Value: value, Source: doctorTuningInherited},
						preview.EffectiveEnvironment[key])
					assert.Contains(t, preview.Reasons[key], "preserved verbatim")
					assert.Contains(t, preview.Reasons[key], "not an observed runtime setting")
				}
			})
		}
	}
}

func TestDoctorTuningPreviewBatchInheritedMemory(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, memory string
		wantGOGC     string
	}{
		{"finite runtime limit", "128MiB", doctorTuningGCOff},
		{"empty runtime limit", "", ""},
		{"invalid runtime limit", doctorTuningInvalid, ""},
		{"disabled runtime limit", doctorTuningGCOff, ""},
		{"zero runtime limit", "0B", ""},
		{"negative runtime limit", "-1B", ""},
		{"unlimited runtime sentinel", "9223372036854775807B", ""},
		{"overflowing runtime limit", "9223372036854775808B", ""},
	}
	for _, tc := range tests {
		for _, explicitGC := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/automatic GOGC", true: "/explicit GOGC"}[explicitGC], func(t *testing.T) {
				t.Parallel()
				env := map[string]string{format.EnvGCProfile: "batch", doctorTuningMemoryEnv: tc.memory}
				if explicitGC {
					env[doctorTuningGCEnv] = doctorTuningGCOff
				}
				inputs, err := resolveDoctorTuningInputs("", "", false, false, doctorTuningLookup(env))
				require.NoError(t, err)
				preview := buildDoctorTuningPreview(cgroup.Limits{MemoryLimitBytes: doctorTuningGiB},
					true, inputs, doctorTuningLookup(env))
				assert.Equal(t, tc.wantGOGC, preview.Tuning.GOGC)
				plan := cgroup.ResolveTuningPlanWithProfile(cgroup.Limits{MemoryLimitBytes: doctorTuningGiB},
					"", cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes, cgroup.GCProfileBatchETL, 0)
				plan.ResolveBatchGOGC(cgroup.RuntimeMemoryLimit(tc.memory))
				assert.Equal(t, plan.GOGCStr, preview.Tuning.GOGC)
				assert.Equal(t, plan.GOGCSkippedReason, preview.Tuning.GOGCSkippedReason)
				assert.Equal(t, format.RuntimeEnvironmentValue{Present: true, Value: tc.memory, Source: doctorTuningInherited},
					preview.EffectiveEnvironment[doctorTuningMemoryEnv])
				if explicitGC {
					assert.Equal(t, format.RuntimeEnvironmentValue{Present: true, Value: doctorTuningGCOff, Source: doctorTuningInherited},
						preview.EffectiveEnvironment[doctorTuningGCEnv])
				} else {
					assert.Equal(t, tc.wantGOGC != "", preview.EffectiveEnvironment[doctorTuningGCEnv].Present)
					assert.Equal(t, tc.wantGOGC, preview.EffectiveEnvironment[doctorTuningGCEnv].Value)
				}
				assert.Equal(t, tc.wantGOGC == "", preview.Tuning.GOGCSkippedReason != "")
			})
		}
	}
}

func TestDoctorTuningPreviewPolicies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		env           map[string]string
		wantStatus    string
		wantCPU       cgroup.CPUPolicy
		wantCPUValue  string
		wantRatio     float64
		wantEffective bool
	}{
		{name: "custom ratio", env: map[string]string{format.EnvMemRatio: "0.75"}, wantStatus: "enabled",
			wantCPU: cgroup.CPUPolicyStatic, wantCPUValue: "2", wantRatio: 0.75, wantEffective: true},
		{name: "invalid ratio falls back", env: map[string]string{format.EnvMemRatio: "NaN"}, wantStatus: "enabled",
			wantCPU: cgroup.CPUPolicyStatic, wantCPUValue: "2", wantRatio: cgroup.DefaultMemoryRatio, wantEffective: true},
		{name: "native CPU", env: map[string]string{format.EnvCPUPolicy: " NATIVE "}, wantStatus: "enabled",
			wantCPU: cgroup.CPUPolicyNative, wantRatio: cgroup.DefaultMemoryRatio, wantEffective: true},
		{name: "invalid CPU falls back", env: map[string]string{format.EnvCPUPolicy: "future"}, wantStatus: "enabled",
			wantCPU: cgroup.CPUPolicyStatic, wantCPUValue: "2", wantRatio: cgroup.DefaultMemoryRatio, wantEffective: true},
		{name: "autotune disabled zero", env: map[string]string{format.EnvAutotune: "0"}, wantStatus: "disabled",
			wantCPU: cgroup.CPUPolicyStatic, wantCPUValue: "2", wantRatio: cgroup.DefaultMemoryRatio},
		{name: "autotune disabled false wins over dry run", env: map[string]string{
			format.EnvAutotune: " FALSE ", format.EnvDryRun: "true",
		}, wantStatus: "disabled", wantCPU: cgroup.CPUPolicyStatic, wantCPUValue: "2", wantRatio: cgroup.DefaultMemoryRatio},
		{name: "dry run numeric", env: map[string]string{format.EnvDryRun: "1"}, wantStatus: "dry-run",
			wantCPU: cgroup.CPUPolicyStatic, wantCPUValue: "2", wantRatio: cgroup.DefaultMemoryRatio},
		{name: "dry run bool", env: map[string]string{format.EnvDryRun: " TRUE "}, wantStatus: "dry-run",
			wantCPU: cgroup.CPUPolicyStatic, wantCPUValue: "2", wantRatio: cgroup.DefaultMemoryRatio},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			preview := buildDoctorTuningPreview(cgroup.Limits{MemoryLimitBytes: doctorTuningGiB, CPUs: 2},
				true, doctorTuningInputs{}, doctorTuningLookup(tc.env))
			assert.Equal(t, tc.wantStatus, preview.Autotune)
			assert.Equal(t, tc.wantCPU, preview.CPUPolicy)
			assert.Equal(t, tc.wantCPUValue, preview.Tuning.GOMAXPROCSValue)
			assert.Equal(t, tc.wantRatio, preview.Tuning.AppliedRatio)
			assert.Equal(t, tc.wantEffective, preview.EffectiveEnvironment[doctorTuningMemoryEnv].Present)
			assert.False(t, preview.Applied)
			if tc.wantCPU == cgroup.CPUPolicyNative {
				assert.False(t, preview.EffectiveEnvironment[doctorTuningCPUEnv].Present)
				assert.Contains(t, preview.Reasons[doctorTuningCPUEnv], "Native CPU policy")
			}
			if !tc.wantEffective {
				assert.NotEmpty(t, preview.Tuning.GOMEMLIMIT)
				for _, key := range []string{doctorTuningMemoryEnv, doctorTuningCPUEnv, doctorTuningGCEnv} {
					assert.False(t, preview.EffectiveEnvironment[key].Present)
					assert.Contains(t, preview.Reasons[key], "would not be applied")
				}
			}
		})
	}
}

func TestDoctorTuningPreviewAdaptiveMissingAndZeroHeap(t *testing.T) {
	t.Parallel()
	for _, heap := range []string{"", "0B"} {
		t.Run(map[bool]string{false: "missing", true: "explicit zero"}[heap != ""], func(t *testing.T) {
			t.Parallel()
			inputs, err := resolveDoctorTuningInputs("adaptive", heap, true, heap != "", doctorTuningLookup(nil))
			require.NoError(t, err)
			preview := buildDoctorTuningPreview(cgroup.Limits{MemoryLimitBytes: doctorTuningGiB},
				true, inputs, doctorTuningLookup(nil))
			assert.Empty(t, preview.Tuning.GOGC)
			assert.False(t, preview.EffectiveEnvironment[doctorTuningGCEnv].Present)
			assert.Contains(t, preview.Reasons[doctorTuningGCEnv], "positive supplied heap estimate")
			assert.Equal(t, heap != "", preview.Inputs.LiveHeapEstimateBytes.Status == runtimeinfo.StatusPresent)
		})
	}
}

func TestPrintDoctorTuningPreview(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		env         map[string]string
		heap        string
		unavailable bool
		wantText    []string
		absentText  []string
	}{
		{
			name: "missing heap and default profile",
			wantText: []string{"Supplied Heap:  missing (not measured)", "GC Profile:     default (source: default)",
				`Candidate GOMEMLIMIT: "469762048B"`, "hard/throttle boundary: high", "Candidate GOGC: unavailable"},
		},
		{
			name: "adaptive supplied estimate", env: map[string]string{format.EnvGCProfile: "adaptive"}, heap: "128MiB",
			wantText: []string{"GC Profile:     adaptive", "Supplied Heap:  134217728 B (128.00 MiB)", "not measured", `Candidate GOGC: "100"`},
		},
		{
			name: "unavailable cgroups", unavailable: true,
			wantText:   []string{"Candidate GOMEMLIMIT: unavailable", "GOMEMLIMIT: unset (runtime value unknown)", "discovery unavailable"},
			absentText: []string{"Memory Ratio:"},
		},
		{
			name: "disabled still shows candidate", env: map[string]string{format.EnvAutotune: "0"},
			wantText: []string{`Candidate GOMEMLIMIT: "469762048B"`, "GOMEMLIMIT: unset (runtime value unknown)", "would not be applied"},
		},
		{
			name: "native CPU and inherited empty value", env: map[string]string{format.EnvCPUPolicy: "native", doctorTuningGCEnv: ""},
			wantText: []string{"CPU policy: native", "Candidate GOMAXPROCS: unavailable", "Native CPU policy", `GOGC: "" (source: inherited)`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inputs, err := resolveDoctorTuningInputs("", tc.heap, false, tc.heap != "", doctorTuningLookup(tc.env))
			require.NoError(t, err)
			preview := buildDoctorTuningPreview(cgroup.Limits{
				MemoryLimitBytes: doctorTuningGiB, MemoryHighBytes: 512 * doctorTuningMiB, CPUQuota: 2.75,
			}, !tc.unavailable, inputs, doctorTuningLookup(tc.env))
			var output strings.Builder
			printDoctorTuningPreview(&output, preview)
			assert.Contains(t, output.String(), "Runtime Tuning Preview (no settings applied)")
			for _, want := range tc.wantText {
				assert.Contains(t, output.String(), want)
			}
			for _, absent := range tc.absentText {
				assert.NotContains(t, output.String(), absent)
			}
		})
	}
	t.Run("nil preview emits nothing", func(t *testing.T) {
		t.Parallel()
		var output strings.Builder
		printDoctorTuningPreview(&output, nil)
		assert.Empty(t, output.String())
	})
}
