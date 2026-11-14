package p99investigation

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/load"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/report"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	investigationHarness = "0123456789abcdef0123456789abcdef01234567"
	fixtureWorktree      = "worktree"
	fixtureDirty         = "dirty"
	fixtureFat           = "fat"
	fixtureBaseFat       = "base-fat"
	fixtureAbsent        = "absent"
	fixtureFortio        = "fortio"
)

func investigationControls() (system.Options, system.Options) {
	target := system.Options{Affinity: []int{0}, CgroupRoot: "/sys/fs/cgroup/investigation-fixture", Version: "v2",
		CPUQuotaUS: 100000, CPUPeriodUS: 100000, MemoryBytes: 2147483648}
	generator := target
	generator.Affinity = []int{3}
	return target, generator
}

func investigationPlan(t *testing.T) Plan {
	t.Helper()
	target, generator := investigationControls()
	plan, err := NewPlan("memory32", 0, investigationHarness, target, generator)
	require.NoError(t, err)
	return plan
}

func TestHistoricalPlan(t *testing.T) {
	t.Parallel()
	var schedules [][]schema.ScheduledTrial
	for _, scenario := range []string{"memory32", "mixed4"} {
		for cohort := range 2 {
			target, generator := investigationControls()
			plan, err := NewPlan(scenario, cohort, investigationHarness, target, generator)
			require.NoError(t, err)
			assert.Equal(t, BaseSHA, plan.BaseSHA)
			assert.Equal(t, HeadSHA, plan.HeadSHA)
			assert.Equal(t, HistoricalArchiveSHA256, plan.HistoricalArchiveSHA256)
			assert.Equal(t, uint64(1), plan.Config.Seed, "workload and bootstrap inputs remain historical")
			require.NotNil(t, plan.Config.ScheduleSeed)
			assert.Equal(t, uint64(23001+cohort), *plan.Config.ScheduleSeed)
			assert.Equal(t, 20, plan.Config.Blocks)
			assert.Equal(t, 30000, plan.Config.DurationMS)
			assert.Equal(t, 10000, plan.Config.WarmupMS)
			assert.Equal(t, 250, plan.Config.SampleMS)
			assert.Equal(t, float64(1000), plan.Config.QPS)
			assert.Equal(t, 16, plan.Config.Concurrency)
			assert.Equal(t, 4096, plan.Config.PayloadBytes)
			assert.Equal(t, "matched", plan.Config.Tuning)
			assert.True(t, plan.Config.ExecDiagnostics)
			assert.False(t, plan.Config.DisableObserver)
			workload, iterations, isa := "mixed", 4, "v4"
			if scenario == "memory32" {
				workload, iterations, isa = "memory", 32, "v3"
			}
			assert.Equal(t, workload, plan.Config.Workload)
			assert.Equal(t, iterations, plan.Config.Iterations)
			assert.Equal(t, isa, plan.Config.SpecializedLevel)
			require.Len(t, plan.Schedule, 200)
			byBlock := make(map[int]map[string]bool)
			for _, trial := range plan.Schedule {
				if byBlock[trial.Block] == nil {
					byBlock[trial.Block] = make(map[string]bool)
				}
				assert.False(t, byBlock[trial.Block][trial.ConfigurationID], "one independent observation per arm/block")
				byBlock[trial.Block][trial.ConfigurationID] = true
			}
			for _, configurations := range byBlock {
				assert.Len(t, configurations, 10)
			}
			schedules = append(schedules, plan.Schedule)
		}
	}
	assert.NotEqual(t, schedules[0], schedules[1], "independent cohorts counterbalance different orders")
	assert.Equal(t, schedules[0], schedules[2], "scenario differences do not silently change assigned cohort order")
}

func TestHistoricalPlanRejectsProtocolChanges(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*Options, *string, *system.Options, *system.Options){
		"scenario":            func(o *Options, _ *string, _, _ *system.Options) { o.Scenario = "cpu" },
		"negative cohort":     func(o *Options, _ *string, _, _ *system.Options) { o.Cohort = -1 },
		"extra cohort":        func(o *Options, _ *string, _, _ *system.Options) { o.Cohort = 2 },
		"short source":        func(_ *Options, sha *string, _, _ *system.Options) { *sha = "1234" },
		"nonhex source":       func(_ *Options, sha *string, _, _ *system.Options) { *sha = strings.Repeat("z", 40) },
		"target affinity":     func(_ *Options, _ *string, a, _ *system.Options) { a.Affinity = []int{1} },
		"generator affinity":  func(_ *Options, _ *string, _, b *system.Options) { b.Affinity = []int{0} },
		"missing delegation":  func(_ *Options, _ *string, a, _ *system.Options) { a.CgroupRoot = "" },
		"relative delegation": func(_ *Options, _ *string, a, _ *system.Options) { a.CgroupRoot = "relative" },
		"old cgroup":          func(_ *Options, _ *string, a, _ *system.Options) { a.Version = "v1" },
		"quota":               func(_ *Options, _ *string, a, _ *system.Options) { a.CPUQuotaUS = 200000 },
		"period":              func(_ *Options, _ *string, a, _ *system.Options) { a.CPUPeriodUS = 200000 },
		"memory":              func(_ *Options, _ *string, a, _ *system.Options) { a.MemoryBytes++ },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			opts, sha := Options{Scenario: "memory32"}, investigationHarness
			target, generator := investigationControls()
			change(&opts, &sha, &target, &generator)
			_, err := NewPlan(opts.Scenario, opts.Cohort, sha, target, generator)
			require.Error(t, err)
		})
	}
}

type investigationExecutor struct {
	calls   []process.Spec
	sources map[string]string
	dirty   map[string]bool
	fail    func(process.Spec) error
	stdout  []byte
}

func (e *investigationExecutor) execute(_ context.Context, spec process.Spec) ([]byte, error) {
	e.calls = append(e.calls, spec)
	if e.fail != nil {
		if err := e.fail(spec); err != nil {
			return e.stdout, err
		}
	}
	if spec.Path == "bin/microfat" {
		return e.stdout, nil
	}
	if spec.Path != "git" || len(spec.Args) == 0 {
		return nil, fmt.Errorf("unexpected command %s %v", spec.Path, spec.Args)
	}
	switch spec.Args[0] {
	case "rev-parse":
		return []byte(e.sources[spec.Dir] + "\n"), nil
	case "status":
		if e.dirty[spec.Dir] {
			return []byte("?? untracked\n"), nil
		}
		return nil, nil
	case fixtureWorktree:
		e.sources[spec.Args[3]] = spec.Args[4]
		return nil, os.MkdirAll(spec.Args[3], directoryMode)
	default:
		return nil, fmt.Errorf("unexpected git operation %v", spec.Args)
	}
}

func preparationFixture(t *testing.T) (Options, *investigationExecutor) {
	t.Helper()
	target, generator := investigationControls()
	controls := filepath.Join(t.TempDir(), "controls.json")
	require.NoError(t, writeJSON(controls, map[string]system.Options{"target": target, "generator": generator}))
	return Options{Scenario: "memory32", Controls: controls, Output: filepath.Join(t.TempDir(), "cohort")},
		&investigationExecutor{sources: map[string]string{"": investigationHarness}, dirty: make(map[string]bool),
			stdout: []byte("retained command output\n")}
}

func TestPrepareFreezesExclusivePlanAndHistoricalSources(t *testing.T) {
	t.Parallel()
	opts, executor := preparationFixture(t)
	require.NoError(t, Prepare(context.Background(), opts, executor.execute))
	plan, err := readPlan(opts.Output)
	require.NoError(t, err)
	assert.Equal(t, investigationPlan(t), plan)
	assert.Equal(t, BaseSHA, executor.sources[filepath.Join(opts.Output, "sources", "base")])
	assert.Equal(t, HeadSHA, executor.sources[filepath.Join(opts.Output, "sources", "head")])
	before, err := os.ReadFile(filepath.Join(opts.Output, planName))
	require.NoError(t, err)
	worktrees := func() int {
		return len(slices.DeleteFunc(slices.Clone(executor.calls), func(spec process.Spec) bool {
			return len(spec.Args) == 0 || spec.Args[0] != fixtureWorktree
		}))
	}
	assert.Equal(t, 2, worktrees())
	require.ErrorIs(t, Prepare(context.Background(), opts, executor.execute), os.ErrExist)
	after, err := os.ReadFile(filepath.Join(opts.Output, planName))
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Equal(t, 2, worktrees(), "reprepare must not recollect source trees")
	for _, name := range []string{planName, "config.json"} {
		info, err := os.Stat(filepath.Join(opts.Output, name))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestPrepareRetainsFailuresAndRefusesDirtySource(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"controls missing", "controls malformed", "controls unknown", "source",
		fixtureDirty, fixtureWorktree, "output"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			opts, executor := preparationFixture(t)
			switch stage {
			case "controls missing":
				opts.Controls += ".missing"
			case "controls malformed":
				require.NoError(t, os.WriteFile(opts.Controls, []byte("{"), fileMode))
			case "controls unknown":
				require.NoError(t, os.WriteFile(opts.Controls, []byte(`{"unknown":true}`), fileMode))
			case "source":
				executor.fail = func(process.Spec) error { return errors.New("git unavailable") }
			case fixtureDirty:
				executor.dirty[""] = true
			case fixtureWorktree:
				executor.fail = func(spec process.Spec) error {
					if spec.Args[0] == fixtureWorktree {
						return errors.New("worktree creation failed")
					}
					return nil
				}
			case "output":
				require.NoError(t, os.WriteFile(opts.Output, []byte("existing file"), fileMode))
			}
			require.Error(t, Prepare(context.Background(), opts, executor.execute))
			if stage == fixtureWorktree {
				_, err := readPlan(opts.Output)
				require.NoError(t, err, "frozen failed preparation must remain reviewable")
			}
		})
	}
}

func TestMeasureFreezesSourcesConfigAndSingleCollection(t *testing.T) {
	t.Parallel()
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			t.Parallel()
			opts, executor := preparationFixture(t)
			require.NoError(t, Prepare(context.Background(), opts, executor.execute))
			executor.fail = func(spec process.Spec) error {
				if failure && spec.Path == "bin/microfat" {
					return errors.New("bounded collection failed")
				}
				return nil
			}
			err := Measure(context.Background(), opts.Output, executor.execute)
			if failure {
				require.ErrorContains(t, err, "bounded collection failed")
			} else {
				require.NoError(t, err)
			}
			stdout, err := os.ReadFile(filepath.Join(opts.Output, "measurement.stdout"))
			require.NoError(t, err)
			assert.Equal(t, executor.stdout, stdout, "stdout survives a failed or time limited command")
			run := executor.calls[len(executor.calls)-1]
			assert.Equal(t, "bin/microfat", run.Path)
			assert.Equal(t, []string{"benchmark", "run", "--config", filepath.Join(opts.Output, "config.json"),
				"--output-dir", filepath.Join(opts.Output, "evidence"), "--head-repository", filepath.Join(opts.Output, "sources", "head"),
				"--base-repository", filepath.Join(opts.Output, "sources", "base"), "--fortio", ".work/benchmark-tools/fortio"}, run.Args)
			require.ErrorIs(t, Measure(context.Background(), opts.Output, executor.execute), os.ErrExist)
			var measured int
			for _, call := range executor.calls {
				if call.Path == "bin/microfat" {
					measured++
				}
			}
			assert.Equal(t, 1, measured, "failed cohorts cannot be silently recollected")
		})
	}
}

func TestMeasureRefusesTamperingBeforeCollection(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"plan absent", "plan malformed", "plan unknown", "plan changed", "source changed", "source dirty",
		"source command", "status command", "config absent", "config malformed", "config unknown", "config changed", "marker"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			opts, executor := preparationFixture(t)
			require.NoError(t, Prepare(context.Background(), opts, executor.execute))
			plan, err := readPlan(opts.Output)
			require.NoError(t, err)
			path := filepath.Join(opts.Output, planName)
			switch stage {
			case "plan absent":
				require.NoError(t, os.Remove(path))
			case "plan malformed":
				require.NoError(t, os.WriteFile(path, []byte("{"), fileMode))
			case "plan unknown":
				require.NoError(t, os.WriteFile(path, []byte(`{"unknown":true}`), fileMode))
			case "plan changed":
				plan.Config.Iterations++
				require.NoError(t, writeJSON(path, plan))
			case "source changed":
				executor.sources[filepath.Join(opts.Output, "sources", "head")] = strings.Repeat("b", 40)
			case "source dirty":
				executor.dirty[filepath.Join(opts.Output, "sources", "base")] = true
			case "source command", "status command":
				executor.fail = func(spec process.Spec) error {
					if stage == "source command" || spec.Args[0] == "status" {
						return errors.New("source inspection failed")
					}
					return nil
				}
			case "config absent":
				require.NoError(t, os.Remove(filepath.Join(opts.Output, "config.json")))
			case "config malformed":
				require.NoError(t, os.WriteFile(filepath.Join(opts.Output, "config.json"), []byte("{"), fileMode))
			case "config unknown":
				data, err := json.Marshal(plan.Config)
				require.NoError(t, err)
				data = append(data[:len(data)-1], []byte(`,"unknown":true}`)...)
				require.NoError(t, os.WriteFile(filepath.Join(opts.Output, "config.json"), data, fileMode))
			case "config changed":
				plan.Config.Seed++
				require.NoError(t, writeJSON(filepath.Join(opts.Output, "config.json"), plan.Config))
			case "marker":
				require.NoError(t, os.WriteFile(filepath.Join(opts.Output, "measurement-started"), nil, fileMode))
			}
			require.Error(t, Measure(context.Background(), opts.Output, executor.execute))
			for _, spec := range executor.calls {
				assert.NotEqual(t, "bin/microfat", spec.Path)
			}
		})
	}
}

func investigationExperiment(t *testing.T, plan Plan) *schema.ExperimentV2 {
	t.Helper()
	config, err := schema.CanonicalV2(plan.Config)
	require.NoError(t, err)
	digest, err := schema.ConfigDigest(config)
	require.NoError(t, err)
	exp := &schema.ExperimentV2{SchemaVersion: schema.VersionV2, ID: "historical-fixture", CreatedAt: "2026-10-25T00:00:00Z",
		SourceSHA: plan.HarnessSHA, Config: config, ConfigSHA256: digest, Schedule: slices.Clone(plan.Schedule), Seed: 1,
		Environment: schema.EnvironmentSnapshot{Host: schema.HostInfo{Arch: "amd64"}}}
	for _, id := range []string{"native-v1", "native-" + plan.Config.SpecializedLevel, "stub", "head-packer", fixtureFat,
		"base-stub", "base-packer", fixtureBaseFat, fixtureHarness, fixtureFortio} {
		source := HeadSHA
		if strings.HasPrefix(id, "base-") {
			source = BaseSHA
		}
		if id == fixtureHarness {
			source = plan.HarnessSHA
		}
		if id == fixtureFortio {
			source = load.SourceCommit()
		}
		exp.Artifacts = append(exp.Artifacts, schema.Artifact{ID: id, SHA256: schema.Digest([]byte(id)), Bytes: 1,
			SourceSHA: source, GoVersion: "go1.27.3", Settings: map[string]string{"vcs.modified": "false"}})
	}
	for _, id := range configurationIDs() {
		cfg := schema.Configuration{ID: id.ID, ArtifactID: fixtureFat, Level: plan.Config.SpecializedLevel,
			Mode: "cache", Cache: "cold", Tuning: "matched", Environment: map[string]string{
				"MICROFAT_AUTOTUNE": "0", "GOMAXPROCS": "2", "GOMEMLIMIT": "1073741824", "GOGC": "100"}}
		if strings.Contains(id.ID, "native-") {
			if strings.HasSuffix(id.ID, "generic") {
				cfg.Level = "v1"
			}
			cfg.ArtifactID, cfg.Mode, cfg.Cache = "native-"+cfg.Level, "native", ""
		} else {
			if strings.HasPrefix(id.ID, "base-") {
				cfg.ArtifactID = fixtureBaseFat
			}
			if strings.HasSuffix(id.ID, "memfd") {
				cfg.Mode, cfg.Cache = "memfd", fixtureAbsent
			}
			if strings.HasSuffix(id.ID, "warm") {
				cfg.Cache = "warm"
			}
		}
		exp.Configurations = append(exp.Configurations, cfg)
	}
	require.NoError(t, schema.ValidateV2(exp))
	return exp
}

func TestVerifyHistoricalBundleProtocol(t *testing.T) {
	t.Parallel()
	plan := investigationPlan(t)
	require.NoError(t, VerifyPlan(plan, investigationExperiment(t, plan)))
	for name, mutate := range map[string]func(*schema.ExperimentV2){
		"invalid schema": func(e *schema.ExperimentV2) { e.SchemaVersion = "v3" },
		"harness source": func(e *schema.ExperimentV2) { e.SourceSHA = HeadSHA },
		fixtureDirty:     func(e *schema.ExperimentV2) { e.Dirty = true },
		"architecture":   func(e *schema.ExperimentV2) { e.Environment.Host.Arch = "arm64" },
		"order":          func(e *schema.ExperimentV2) { e.Schedule[0], e.Schedule[1] = e.Schedule[1], e.Schedule[0] },
		"config": func(e *schema.ExperimentV2) {
			cfg := plan.Config
			cfg.Seed = 2
			var err error
			e.Config, err = schema.CanonicalV2(cfg)
			require.NoError(t, err)
			e.ConfigSHA256, err = schema.ConfigDigest(e.Config)
			require.NoError(t, err)
		},
		"missing stub": func(e *schema.ExperimentV2) {
			e.Artifacts = slices.DeleteFunc(e.Artifacts, func(a schema.Artifact) bool { return a.ID == "stub" })
		},
		"artifact source":    func(e *schema.ExperimentV2) { e.Artifacts[0].SourceSHA = BaseSHA },
		"toolchain":          func(e *schema.ExperimentV2) { e.Artifacts[0].GoVersion = "go1.27.1" },
		"artifact dirty":     func(e *schema.ExperimentV2) { e.Artifacts[0].Settings["vcs.modified"] = "true" },
		"native payload":     func(e *schema.ExperimentV2) { e.Configurations[0].ArtifactID = fixtureFat },
		"native mode":        func(e *schema.ExperimentV2) { e.Configurations[0].Mode = "memfd" },
		"native ISA":         func(e *schema.ExperimentV2) { e.Configurations[0].Level = "v2" },
		"native tuning":      func(e *schema.ExperimentV2) { e.Configurations[0].Tuning = "on" },
		"native environment": func(e *schema.ExperimentV2) { e.Configurations[0].Environment["GOMAXPROCS"] = "1" },
		"fat artifact":       func(e *schema.ExperimentV2) { e.Configurations[5].ArtifactID = fixtureBaseFat },
		"fat mode":           func(e *schema.ExperimentV2) { e.Configurations[5].Mode = "cache" },
		"fat cache":          func(e *schema.ExperimentV2) { e.Configurations[5].Cache = "warm" },
		"fat ISA":            func(e *schema.ExperimentV2) { e.Configurations[5].Level = "v4" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			exp := investigationExperiment(t, plan)
			mutate(exp)
			require.Error(t, VerifyPlan(plan, exp))
		})
	}
}

func TestSummaryRetainsMissingPartialAndContradictoryEvidence(t *testing.T) {
	t.Parallel()
	for _, state := range []string{fixtureAbsent, "partial", "corrupt", "incomplete", "protocol", "multiple"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			output := filepath.Join(t.TempDir(), "cohort")
			plan := investigationPlan(t)
			if state != fixtureAbsent {
				require.NoError(t, os.MkdirAll(output, directoryMode))
				require.NoError(t, writeJSON(filepath.Join(output, planName), plan))
			}
			root := filepath.Join(output, "evidence")
			switch state {
			case "partial":
				partial := filepath.Join(root, ".partial-cohort")
				require.NoError(t, os.MkdirAll(partial, directoryMode))
				require.NoError(t, os.WriteFile(filepath.Join(partial, "raw.json"), []byte("retained failure"), fileMode))
			case "corrupt":
				require.NoError(t, os.MkdirAll(root, directoryMode))
				require.NoError(t, os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte("bad checksum"), fileMode))
			case fixtureAbsent:
			default:
				exp := investigationExperiment(t, plan)
				if state == "protocol" {
					exp.SourceSHA = HeadSHA
				}
				require.NoError(t, report.WriteBundle(exp, filepath.Join(root, "first"), nil))
				if state == "multiple" {
					require.NoError(t, report.WriteBundle(exp, filepath.Join(root, "second"), nil))
				}
			}
			require.Error(t, Summarize(output), "incomplete collection remains an operational failure after report retention")
			data, err := os.ReadFile(filepath.Join(output, "summary.json"))
			require.NoError(t, err)
			var summary Summary
			require.NoError(t, json.Unmarshal(data, &summary))
			assert.False(t, summary.Complete)
			assert.NotEmpty(t, summary.Reasons)
			assert.Contains(t, summary.Disposition, "Maintainer review required")
			if state == "incomplete" {
				require.NotNil(t, summary.Qualification)
				assert.False(t, summary.Qualification.Publishable)
				require.NotNil(t, summary.Analysis)
				assert.False(t, summary.Analysis.Complete)
			}
			if state == "partial" {
				retained, err := os.ReadFile(filepath.Join(root, ".partial-cohort", "raw.json"))
				require.NoError(t, err)
				assert.Equal(t, "retained failure", string(retained))
			}
			markdown, err := os.ReadFile(filepath.Join(output, "summary.md"))
			require.NoError(t, err)
			assert.Contains(t, string(markdown), "Complete qualified cohort: false")
		})
	}
}
