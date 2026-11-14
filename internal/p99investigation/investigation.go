// Package p99investigation freezes and replays the historical #230 service-request experiment.
package p99investigation

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/compare"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/runner"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
)

const (
	BaseSHA                 = "3d8496b6f01f97a2fae1b27ff04215f9b8fad257"
	HeadSHA                 = "9607f3a0bb5954f37e8775351d76cee3398574a8"
	HistoricalArchiveSHA256 = "20a0d4d61a27754ec3b4f410d37b76a447b38ae4b6916412e802814d1cfd41a6"
	blocks                  = 20
	cohorts                 = 2
	scheduleSeed            = uint64(23001)
	loadDurationMS          = 30000
	warmupDurationMS        = 10000
	sampleIntervalMS        = 250
	controlMemory           = int64(2 * 1024 * 1024 * 1024)
	directoryMode           = 0o700
	fileMode                = 0o600
	planName                = "plan.json"
)

type Options struct {
	Scenario string
	Cohort   int
	Controls string
	Output   string
}

type Plan struct {
	Version                 string                  `json:"version"`
	Scenario                string                  `json:"scenario"`
	Cohort                  int                     `json:"cohort"`
	HarnessSHA              string                  `json:"harness_sha"`
	BaseSHA                 string                  `json:"base_sha"`
	HeadSHA                 string                  `json:"head_sha"`
	HistoricalArchiveSHA256 string                  `json:"historical_archive_sha256"`
	Config                  runner.ExperimentConfig `json:"config"`
	Schedule                []schema.ScheduledTrial `json:"schedule"`
}

type Execute func(context.Context, process.Spec) ([]byte, error)

func configurationIDs() []schema.Configuration {
	var configs []schema.Configuration
	for _, mode := range []string{"native-generic", "native-specialized", "fat-memfd", "fat-cache-cold", "fat-cache-warm"} {
		for _, revision := range []string{"base-", "head-"} {
			configs = append(configs, schema.Configuration{ID: revision + mode})
		}
	}
	return configs
}

func NewPlan(scenario string, cohort int, harnessSHA string, target, generator system.Options) (Plan, error) {
	plan := Plan{Version: "v1", Scenario: scenario, Cohort: cohort, HarnessSHA: harnessSHA,
		BaseSHA: BaseSHA, HeadSHA: HeadSHA, HistoricalArchiveSHA256: HistoricalArchiveSHA256}
	if scenario != "memory32" && scenario != "mixed4" || cohort < 0 || cohort >= cohorts || !validSHA(harnessSHA) {
		return plan, errors.New("expected memory32|mixed4, cohort 0|1 and a full harness commit SHA")
	}
	for i, control := range []system.Options{target, generator} {
		cpu := 0
		if i == 1 {
			cpu = 3
		}
		if control.Version != "v2" || control.CgroupRoot == "" || !reflect.DeepEqual(control.Affinity, []int{cpu}) ||
			control.CPUQuotaUS != system.DefaultPeriod || control.CPUPeriodUS != system.DefaultPeriod || control.MemoryBytes != controlMemory {
			return plan, errors.New("historical controls require target CPU0, generator CPU3, v2, one CPU and 2GiB per role")
		}
	}
	cfg := runner.DefaultExperimentConfig()
	cfg.Name, cfg.Blocks = fmt.Sprintf("historical-p99-%s-%d", scenario, cohort), blocks
	cfg.DurationMS, cfg.WarmupMS, cfg.SampleMS = loadDurationMS, warmupDurationMS, sampleIntervalMS
	cfg.Target, cfg.Generator, cfg.ExecDiagnostics = target, generator, true
	seed := scheduleSeed + uint64(cohort)
	cfg.ScheduleSeed = &seed
	cfg.SpecializedLevel = "v4"
	if scenario == "memory32" {
		cfg.Workload, cfg.Iterations, cfg.SpecializedLevel = "memory", 32, "v3"
	}
	if err := cfg.Validate(); err != nil {
		return plan, err
	}
	plan.Config = cfg
	var err error
	plan.Schedule, err = compare.Schedule(configurationIDs(), blocks, seed)
	return plan, err
}

func validSHA(value string) bool {
	const shaLength = 40
	if len(value) != shaLength {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' && char < 'a' || char > 'f' {
			return false
		}
	}
	return true
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), fileMode) // #nosec G304 -- explicit investigation output path.
}

func readPlan(output string) (Plan, error) {
	var plan Plan
	data, err := os.ReadFile(filepath.Join(output, planName)) // #nosec G304 -- explicit investigation output path.
	if err != nil {
		return plan, err
	}
	if err := json.Unmarshal(data, &plan, json.RejectUnknownMembers(true)); err != nil {
		return plan, err
	}
	expected, err := NewPlan(plan.Scenario, plan.Cohort, plan.HarnessSHA, plan.Config.Target, plan.Config.Generator)
	if err != nil {
		return plan, err
	}
	if !reflect.DeepEqual(plan, expected) {
		return plan, errors.New("investigation plan differs from the fixed protocol")
	}
	return plan, nil
}

func Prepare(ctx context.Context, options Options, execute Execute) error {
	data, err := os.ReadFile(options.Controls) // #nosec G304 -- explicit controls input.
	if err != nil {
		return err
	}
	var controls struct {
		Target    system.Options `json:"target"`
		Generator system.Options `json:"generator"`
	}
	if err := json.Unmarshal(data, &controls, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	sha, err := execute(ctx, process.Spec{Path: "git", Args: []string{"rev-parse", "HEAD"}})
	if err != nil {
		return err
	}
	plan, err := NewPlan(options.Scenario, options.Cohort, strings.TrimSpace(string(sha)), controls.Target, controls.Generator)
	if err != nil {
		return err
	}
	if err := cleanSource(ctx, "", plan.HarnessSHA, execute); err != nil {
		return err
	}
	if err := os.MkdirAll(options.Output, directoryMode); err != nil {
		return err
	}
	// Exclusive creation prevents accidental overwrite/recollection of a frozen cohort.
	file, err := os.OpenFile(filepath.Join(options.Output, planName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	data, err = json.Marshal(plan, json.Deterministic(true))
	if err != nil {
		return errors.Join(err, file.Close())
	}
	_, err = file.Write(append(data, '\n'))
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	for role, revision := range map[string]string{"base": BaseSHA, "head": HeadSHA} {
		path := filepath.Join(options.Output, "sources", role)
		if _, err := execute(ctx, process.Spec{Path: "git", Args: []string{"worktree", "add", "--detach", path, revision}}); err != nil {
			return err
		}
	}
	return writeJSON(filepath.Join(options.Output, "config.json"), plan.Config)
}

func cleanSource(ctx context.Context, dir, sha string, execute Execute) error {
	actual, err := execute(ctx, process.Spec{Path: "git", Dir: dir, Args: []string{"rev-parse", "HEAD"}})
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(actual)) != sha {
		return errors.New("source commit differs from frozen plan")
	}
	dirty, err := execute(ctx, process.Spec{Path: "git", Dir: dir, Args: []string{"status", "--porcelain"}})
	if err != nil {
		return err
	}
	if len(dirty) != 0 {
		return errors.New("investigation requires clean source checkouts")
	}
	return nil
}

func Measure(ctx context.Context, output string, execute Execute) error {
	plan, err := readPlan(output)
	if err != nil {
		return err
	}
	for role, sha := range map[string]string{"": plan.HarnessSHA, "base": BaseSHA, "head": HeadSHA} {
		dir := ""
		if role != "" {
			dir = filepath.Join(output, "sources", role)
		}
		if err := cleanSource(ctx, dir, sha, execute); err != nil {
			return err
		}
	}
	config, err := os.ReadFile(filepath.Join(output, "config.json")) // #nosec G304 -- frozen investigation output path.
	if err != nil {
		return err
	}
	var cfg runner.ExperimentConfig
	if err := json.Unmarshal(config, &cfg, json.RejectUnknownMembers(true)); err != nil || !reflect.DeepEqual(cfg, plan.Config) {
		return errors.New("measurement configuration differs from frozen plan")
	}
	// #nosec G304 -- exclusive marker under the explicit investigation output directory.
	marker, err := os.OpenFile(filepath.Join(output, "measurement-started"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	if err := marker.Close(); err != nil {
		return err
	}
	args := []string{"benchmark", "run", "--config", filepath.Join(output, "config.json"),
		"--output-dir", filepath.Join(output, "evidence"), "--head-repository", filepath.Join(output, "sources", "head"),
		"--base-repository", filepath.Join(output, "sources", "base"), "--fortio", ".work/benchmark-tools/fortio"}
	stdout, runErr := execute(ctx, process.Spec{Path: "bin/microfat", Args: args})
	return errors.Join(runErr, os.WriteFile(filepath.Join(output, "measurement.stdout"), stdout, fileMode))
}
