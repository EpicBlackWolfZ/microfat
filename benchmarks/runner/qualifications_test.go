package runner

import (
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/compare"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/internal/testfixture"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixturePerformanceGovernor = "performance"

func TestHardwareQualifications(t *testing.T) {
	t.Parallel()
	cfg := DefaultExperimentConfig()
	cfg.Target.Affinity, cfg.Generator.Affinity = []int{1}, []int{3}
	assert.Len(t, hostQualifications(nil, cfg), 2)
	host := map[string]string{
		"/sys/cpu1/cpufreq/scaling_governor": fixturePerformanceGovernor, "/sys/cpu1/topology/thread_siblings_list": "1-2",
		"/sys/cpu3/cpufreq/scaling_governor": fixturePerformanceGovernor, "/sys/cpu3/topology/thread_siblings_list": "3-4",
		"/sys/cpu2/cpufreq/scaling_governor": fixturePerformanceGovernor, "/sys/cpu2/topology/thread_siblings_list": "1-2",
	}
	assert.Empty(t, hostQualifications(host, cfg))
	cfg.Generator.Affinity = []int{2}
	assert.Len(t, hostQualifications(host, cfg), 1)
	host["/sys/cpu1/cpufreq/scaling_governor"] = "powersave"
	assert.Len(t, hostQualifications(host, cfg), 2)
	for _, value := range []string{"", "bad", "2-bad", "4-2", "3,4", "-1", "2,bad", "2,4-2"} {
		assert.False(t, cpuInList(2, value))
	}
	assert.True(t, cpuInList(2, "1, 2"))
}

func TestSelectedCPUQualificationCompleteness(t *testing.T) {
	t.Parallel()
	cfg := DefaultExperimentConfig()
	cfg.RunnerIdentity, cfg.Blocks = "fixture-dedicated-host", 20
	cfg.Target.Affinity, cfg.Generator.Affinity = []int{1, 2}, []int{3}
	cfg.Target.CgroupRoot, cfg.Generator.CgroupRoot = "/fixture/target", "/fixture/generator"
	require.NoError(t, cfg.Validate())
	const (
		governorWarning = "performance governor"
		siblingWarning  = "physical-core separation"
	)
	for _, test := range []struct {
		name, warning string
		alter         func(map[string]string)
	}{
		{"complete", "", func(map[string]string) {}},
		{"missing_target_topology", siblingWarning, func(host map[string]string) {
			delete(host, "/sys/cpu2/topology/thread_siblings_list")
		}},
		{"missing_generator_topology", siblingWarning, func(host map[string]string) {
			delete(host, "/sys/cpu3/topology/thread_siblings_list")
		}},
		{"generator_reports_shared_core", siblingWarning, func(host map[string]string) {
			host["/sys/cpu3/topology/thread_siblings_list"] = "2,3"
		}},
		{"missing_target_and_generator_reports_shared_core", siblingWarning, func(host map[string]string) {
			delete(host, "/sys/cpu2/topology/thread_siblings_list")
			host["/sys/cpu3/topology/thread_siblings_list"] = "2,3"
		}},
		{"missing_target_governor", governorWarning, func(host map[string]string) {
			delete(host, "/sys/cpu2/cpufreq/scaling_governor")
		}},
		{"missing_generator_governor", governorWarning, func(host map[string]string) {
			delete(host, "/sys/cpu3/cpufreq/scaling_governor")
		}},
		{"unrelated_governor_only", governorWarning, func(host map[string]string) {
			for _, cpu := range []string{"1", "2", "3"} {
				delete(host, "/sys/cpu"+cpu+"/cpufreq/scaling_governor")
			}
		}},
		{"variable_generator_governor", governorWarning, func(host map[string]string) {
			host["/sys/cpu3/cpufreq/scaling_governor"] = "powersave"
		}},
		{"topology_missing_self", siblingWarning, func(host map[string]string) {
			host["/sys/cpu2/topology/thread_siblings_list"] = "6"
		}},
		{"invalid_topology", siblingWarning, func(host map[string]string) {
			host["/sys/cpu2/topology/thread_siblings_list"] = "2,bad"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			host := map[string]string{"/sys/cpu0/cpufreq/scaling_governor": fixturePerformanceGovernor}
			for _, cpu := range []int{1, 2, 3} {
				prefix := "/sys/cpu" + strconv.Itoa(cpu)
				host[prefix+"/cpufreq/scaling_governor"] = fixturePerformanceGovernor
				host[prefix+"/topology/thread_siblings_list"] = strconv.Itoa(cpu) + "," + strconv.Itoa(cpu+4)
			}
			test.alter(host)
			exp := qualifiedHostFixture(t, cfg)
			exp.HostTelemetry = host
			exp.Warnings = hostQualifications(host, cfg)
			exp.ReleaseEligible = releaseEligible(cfg, exp)
			require.NoError(t, schema.ValidateV2(exp))
			if test.warning == "" {
				require.Empty(t, exp.Warnings)
				require.True(t, exp.ReleaseEligible)
				return
			}
			require.Contains(t, strings.Join(exp.Warnings, "\n"), test.warning)
			require.False(t, exp.ReleaseEligible)
		})
	}
}

func qualifiedHostFixture(t *testing.T, cfg ExperimentConfig) *schema.ExperimentV2 {
	t.Helper()
	exp := testfixture.Experiment()
	var err error
	exp.Config, err = schema.CanonicalV2(cfg)
	require.NoError(t, err)
	exp.ConfigSHA256, err = schema.ConfigDigest(exp.Config)
	require.NoError(t, err)
	exp.Schedule, err = compare.Schedule(exp.Configurations, cfg.Blocks, cfg.Seed)
	require.NoError(t, err)
	template := exp.Trials[0]
	exp.Trials = nil
	for _, scheduled := range exp.Schedule {
		trial := template
		trial.ScheduledTrial = scheduled
		trial.Controls = []schema.Control{
			{Name: "target.affinity", Requested: "1,2", Effective: "1,2", State: fixtureControlApplied},
			{Name: "generator.affinity", Requested: "3", Effective: "3", State: fixtureControlApplied},
			{Name: "target.cpu.max", Requested: "max 100000", Effective: "max 100000", State: fixtureControlApplied},
			{Name: "generator.cpu.max", Requested: "max 100000", Effective: "max 100000", State: fixtureControlApplied},
			{Name: fixtureTargetMemoryMax, Requested: "max", Effective: "max", State: fixtureControlApplied},
			{Name: "generator.memory.max", Requested: "max", Effective: "max", State: fixtureControlApplied},
		}
		trial.SampleCount = 1
		load := testfixture.HTTP()
		load.RawPath = "trials/" + scheduled.ID + "/fortio.json"
		trial.Load = &load
		exp.Trials = append(exp.Trials, trial)
	}
	return exp
}
