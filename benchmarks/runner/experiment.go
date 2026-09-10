package runner

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/compare"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/env"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/load"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/report"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
)

type ExperimentResult struct {
	Experiment *schema.ExperimentV2
	Bundle     string
}

func RunExperiment(ctx context.Context, cfg ExperimentConfig, opts RunOptions) (*ExperimentResult, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := opts.resolve(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, maxRunDuration+buildTimeout)
	defer cancel()
	fortio := &load.Fortio{Path: opts.Fortio, Env: cleanEnvironment()}
	if err := fortio.CheckVersion(ctx); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(opts.OutputDir, defaultDirPerm); err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(opts.OutputDir, ".run-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	_, _ = fmt.Fprintln(opts.LogWriter, "Building identical native and fat benchmark payloads...")
	built, err := buildArtifacts(ctx, cfg, opts, root)
	if err != nil {
		return nil, err
	}
	if err := identifyTools(opts, built); err != nil {
		return nil, err
	}
	environment, err := env.Detect()
	if err != nil {
		return nil, err
	}
	configData, err := schema.CanonicalV2(cfg)
	if err != nil {
		return nil, err
	}
	configDigest, err := schema.ConfigDigest(configData)
	if err != nil {
		return nil, err
	}
	schedule, err := compare.Schedule(built.Configurations, cfg.Blocks, cfg.Seed)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	exp := &schema.ExperimentV2{SchemaVersion: schema.VersionV2, ID: cfg.Name + "-" + now.Format("20060102T150405.000000000Z"),
		CreatedAt: now.Format(time.RFC3339Nano), SourceSHA: built.SourceSHA, Dirty: built.Dirty,
		ConfigSHA256: configDigest, Config: configData, Environment: *environment, Artifacts: built.Artifacts,
		Configurations: built.Configurations, Schedule: schedule, Seed: cfg.Seed, Runner: env.Runner(),
		Warnings: []string{"filesystem page-cache state is uncontrolled"}}
	if cfg.RunnerIdentity == "" {
		exp.Warnings = append(exp.Warnings, "release hardware qualification has not been established")
	}
	exp.HostTelemetry = system.ReadHost("/sys")
	exp.Warnings = append(exp.Warnings, hostQualifications(exp.HostTelemetry, cfg)...)
	partial := filepath.Join(opts.OutputDir, ".partial-"+exp.ID)
	if err := os.Mkdir(partial, defaultDirPerm); err != nil {
		return nil, err
	}
	if err := persistCheckpoint(partial, exp, nil); err != nil {
		return nil, err
	}
	files := make(map[string][]byte)
	if err := executeSchedule(ctx, cfg, opts, root, partial, built, exp, files); err != nil {
		return nil, err
	}
	exp.Complete = len(exp.Trials) == len(schedule)
	for _, trial := range exp.Trials {
		exp.Warnings = append(exp.Warnings, trial.Warnings...)
	}
	slices.Sort(exp.Warnings)
	exp.Warnings = slices.Compact(exp.Warnings)
	exp.ReleaseEligible = releaseEligible(cfg, exp)
	comparisons, err := compare.All(exp, built.Configurations[0].ID)
	if opts.BaseRepository != "" {
		comparisons, err = compare.Revisions(exp)
	}
	if err != nil {
		return nil, err
	}
	exp.Comparisons = comparisons
	destination := filepath.Join(opts.OutputDir, now.Format("2006-01-02")+"-"+built.SourceSHA, exp.ID)
	if err := report.WriteBundle(exp, destination, files); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(partial); err != nil {
		_, _ = fmt.Fprintf(opts.LogWriter, "Published bundle; redundant checkpoint retained: %s\n", err)
	}
	result := &ExperimentResult{Experiment: exp, Bundle: destination}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	for _, trial := range exp.Trials {
		if trial.Outcome != schema.OutcomeOK {
			return result, errors.New("experiment contains unsuccessful trials; see evidence")
		}
	}
	return result, nil
}

func releaseEligible(cfg ExperimentConfig, exp *schema.ExperimentV2) bool {
	const releaseMinimumBlocks = 20
	if exp.Runner != nil && exp.Runner.Provider == schema.HostedProvider {
		return false
	}
	if cfg.RunnerIdentity == "" || cfg.DisableObserver || cfg.Blocks < releaseMinimumBlocks || !exp.Complete || exp.Dirty ||
		len(exp.Trials) == 0 || len(cfg.Target.Affinity) == 0 || len(cfg.Generator.Affinity) == 0 ||
		cfg.Target.CgroupRoot == "" || cfg.Generator.CgroupRoot == "" {
		return false
	}
	for _, warning := range exp.Warnings {
		if strings.HasPrefix(warning, "noisy-environment:") {
			return false
		}
	}
	for _, trial := range exp.Trials {
		if trial.Outcome != schema.OutcomeOK || len(trial.Controls) == 0 || trial.SampleCount == 0 || trial.Load == nil ||
			trial.Load.Successful != trial.Load.Requests || len(trial.Warnings) != 0 {
			return false
		}
		for _, control := range trial.Controls {
			if control.State != "applied" {
				return false
			}
		}
	}
	return true
}

func encodeExtra(files map[string][]byte, path string, value any) error {
	// Keep raw telemetry deterministic without the repeated indentation cost of full cgroup counters.
	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return err
	}
	files[path] = data
	return nil
}

// Keep an initial schedule and completed trials outside disposable build directories.
// A killed harness leaves this explicitly incomplete journal available for diagnosis.
func persistCheckpoint(root string, exp *schema.ExperimentV2, files map[string][]byte) error {
	for name, data := range files {
		if !schema.SafeReference(name) || !strings.HasPrefix(name, "trials/") {
			return errors.New("invalid checkpoint path")
		}
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), defaultDirPerm); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, evidenceFilePerm); err != nil {
			return err
		}
	}
	data, err := json.Marshal(exp, json.Deterministic(true))
	if err != nil {
		return err
	}
	temporary := filepath.Join(root, "raw.json.tmp")
	if err := os.WriteFile(temporary, data, evidenceFilePerm); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(root, "raw.json"))
}

func identifyTools(opts RunOptions, built *builtArtifacts) error {
	for id, path := range map[string]string{"fortio": opts.Fortio, "harness": opts.Helper} {
		artifact, err := identify(path, id, built.SourceSHA)
		if err != nil {
			return err
		}
		if id == "fortio" {
			artifact.SourceSHA = load.SourceCommit
		} else {
			artifact.SourceSHA = artifact.Settings["vcs.revision"]
			if artifact.SourceSHA == "" {
				artifact.SourceSHA = "unknown"
			}
			built.Dirty = built.Dirty || artifact.Settings["vcs.modified"] != "false"
		}
		built.Artifacts = append(built.Artifacts, artifact)
	}
	slices.SortFunc(built.Artifacts, func(a, b schema.Artifact) int { return strings.Compare(a.ID, b.ID) })
	return nil
}

func executeSchedule(ctx context.Context, cfg ExperimentConfig, opts RunOptions, root, partial string, built *builtArtifacts,
	exp *schema.ExperimentV2, files map[string][]byte) error {
	schedule := exp.Schedule
	var retainedBytes int
	for _, scheduled := range schedule {
		if ctx.Err() != nil {
			break
		}
		_, _ = fmt.Fprintf(opts.LogWriter, "Trial %d/%d: block %d, %s\n", scheduled.Position+1, len(schedule), scheduled.Block,
			scheduled.ConfigurationID)
		trial, raw := runProcessTrial(ctx, cfg, opts, root, built, scheduled)
		exp.Trials = append(exp.Trials, trial)
		if err := persistCheckpoint(partial, exp, raw); err != nil {
			return fmt.Errorf("retaining interrupted evidence in %s: %w", partial, err)
		}
		if err := retainRaw(files, raw, &retainedBytes); err != nil {
			return fmt.Errorf("evidence budget reached; incomplete journal retained in %s: %w", partial, err)
		}
		if trial.Outcome != schema.OutcomeOK {
			_, _ = fmt.Fprintf(opts.LogWriter, "Trial %s: %s\n", trial.ID, trial.Reason)
		}
	}
	return nil
}

// A release shard retains 200 trials with roughly 160 full cgroup samples each,
// plus up to two seconds of millisecond exec-diagnostic samples per trial.
// Keep both within a finite bound; reports have a separate bundle limit.
const maxRetainedBytes = 1024 * 1024 * 1024

func retainRaw(files, raw map[string][]byte, retainedBytes *int) error {
	additional := 0
	for _, data := range raw {
		if len(data) > schema.MaxEvidenceBytes || len(data) > maxRetainedBytes-*retainedBytes-additional {
			return errors.New("raw evidence exceeds memory budget; shard the suite or increase the sampling interval")
		}
		additional += len(data)
	}
	maps.Copy(files, raw)
	*retainedBytes += additional
	return nil
}
