package runner

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/load"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

const (
	readyTimeout       = 15 * time.Second
	stopTimeout        = 5 * time.Second
	readyPoll          = 5 * time.Millisecond
	diagnosticTimeout  = 2 * time.Second
	trialGrace         = 30 * time.Second
	maxDiagnosticBytes = 1024 * 1024
)

func runProcessTrial(ctx context.Context, cfg ExperimentConfig, opts RunOptions, root string,
	built *builtArtifacts, scheduled schema.ScheduledTrial) (trial schema.ProcessTrial, files map[string][]byte) {
	start := time.Now()
	parentCtx := ctx
	trial = schema.ProcessTrial{ScheduledTrial: scheduled, StartedAt: start.UTC().Format(time.RFC3339Nano),
		Outcome: schema.OutcomeFailed, Metrics: make(map[string]schema.Measurement)}
	files = make(map[string][]byte)
	defer func() { finalizeTrial(parentCtx, start, &trial, files) }()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.DurationMS+cfg.WarmupMS)*time.Millisecond+trialGrace)
	defer cancel()
	var config schema.Configuration
	for _, item := range built.Configurations {
		if item.ID == scheduled.ConfigurationID {
			config = item
		}
	}
	var artifact schema.Artifact
	for _, item := range built.Artifacts {
		if item.ID == config.ArtifactID {
			artifact = item
		}
	}
	target, err := system.Prepare(cfg.Target)
	if err != nil {
		trial.Reason = err.Error()
		return trial, files
	}
	defer func() {
		if err := target.Close(); err != nil {
			trial.Outcome, trial.Reason = schema.OutcomeFailed, "target cleanup: "+err.Error()
		}
	}()
	generator, err := system.Prepare(cfg.Generator)
	if err != nil {
		trial.Reason = err.Error()
		return trial, files
	}
	defer func() {
		if err := generator.Close(); err != nil {
			trial.Outcome, trial.Reason = schema.OutcomeFailed, "generator cleanup: "+err.Error()
		}
	}()
	for role, controls := range map[string][]schema.Control{"target": target.Controls, "generator": generator.Controls} {
		for _, control := range controls {
			control.Name = role + "." + control.Name
			trial.Controls = append(trial.Controls, control)
		}
	}
	slices.SortFunc(trial.Controls, func(a, b schema.Control) int { return strings.Compare(a.Name, b.Name) })
	trial.Warnings = append(trial.Warnings, system.Uncontrolled(trial.Controls)...)
	cacheDir := filepath.Join(root, trial.ID+"-cache")
	if err := os.Mkdir(cacheDir, defaultDirPerm); err != nil {
		trial.Reason = err.Error()
		return trial, files
	}
	if config.Cache == "warm" {
		if err := prewarm(artifact.Path, config.Level, cacheDir); err != nil {
			trial.Reason = err.Error()
			return trial, files
		}
	}
	args := []string{"-payload", strconv.Itoa(cfg.PayloadBytes), "-iterations", strconv.Itoa(cfg.Iterations),
		"-concurrency", strconv.Itoa(cfg.Concurrency), "-seed", strconv.FormatUint(cfg.Seed, 10)}
	environment := targetEnvironment(config, cacheDir)
	launch := time.Now()
	child, err := process.Start(ctx, target.Wrap(opts.Helper, process.Spec{Path: artifact.Path, Args: args, Env: environment}))
	if err != nil {
		trial.Reason = err.Error()
		return trial, files
	}
	trial.Target = schema.ProcessIdentity{PID: child.PID(), Executable: artifact.Path, Arguments: args}
	sampler, sampleErr := trialSampler(ctx, cfg, opts, root, target, &trial)
	cleanup := trialCleanup{ctx: parentCtx, cfg: cfg, opts: opts, target: target, artifact: artifact,
		args: args, environment: environment, child: child, sampler: sampler, trial: &trial, files: files, config: config}
	defer cleanup.finish()

	if sampleErr != nil {
		trial.Reason = sampleErr.Error()
		return trial, files
	}
	url, err := awaitReady(ctx, child)
	if err != nil {
		trial.Reason = err.Error()
		return trial, files
	}
	trial.Metrics["startup_ns"] = schema.Measured(float64(time.Since(launch).Nanoseconds()), "ns", "startup", "helper-spawn-to-ready")
	trial.Metrics["artifact_bytes"] = schema.Measured(float64(artifact.Bytes), "bytes", "artifact", "file-stat")
	addArtifactMetrics(config, artifact, built, &trial)
	trial.Metrics["extraction_peak_bytes"] = schema.Unavailable("bytes", "extraction", "observer",
		"exec-boundary diagnostic unavailable; startup is not extraction-only")
	diagnostic, err := diagnostics(ctx, url)
	if err != nil {
		trial.Reason = err.Error()
		return trial, files
	}
	trial.Target.Effective = diagnostic
	if err := validateIdentity(config, diagnostic); err != nil {
		trial.Reason = err.Error()
		return trial, files
	}
	if config.Mode != nativeMode {
		for _, payload := range built.Artifacts {
			if payload.ID == "native-"+config.Level && diagnostic["MICROFAT_SELECTED_SHA256"] != payload.SHA256 {
				trial.Reason = "dispatched payload digest differs from native comparison artifact"
				return trial, files
			}
		}
	}
	measureTrial(ctx, cfg, opts, generator, sampler, url, &trial, files)
	return trial, files
}

func targetEnvironment(config schema.Configuration, cacheDir string) []string {
	result := cleanEnvironment()
	for key, value := range config.Environment {
		result = append(result, key+"="+value)
	}
	result = append(result, "MICROFAT_CACHE_DIR="+cacheDir)
	if config.Mode != nativeMode {
		result = append(result, "MICROFAT_DISPATCH_MODE="+config.Mode, "MICROFAT_FORCE_LEVEL="+config.Level)
	}
	slices.Sort(result)
	return result
}

func prewarm(path, level, cache string) error {
	file, err := os.Open(path) // #nosec G304 -- benchmark-built artifact selected for prewarming.
	if err != nil {
		return err
	}
	stat, err := file.Stat()
	if err != nil {
		return errors.Join(err, file.Close())
	}
	_, _, err = pack.PrewarmBinary(file, stat.Size(), []string{level}, cache)
	return errors.Join(err, file.Close())
}

func awaitReady(ctx context.Context, child *process.Child) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	ticker := time.NewTicker(readyPoll)
	defer ticker.Stop()
	for {
		line, _, found := bytes.Cut(child.Stdout(), []byte{'\n'})
		if found {
			var message struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(line, &message); err != nil {
				return "", err
			}
			if !strings.HasPrefix(message.URL, "http://127.0.0.1:") {
				return "", errors.New("invalid readiness URL")
			}
			return message.URL, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-child.Done():
			return "", fmt.Errorf("target exited before readiness: %w: %s", child.Wait(), child.Stderr())
		case <-ticker.C:
		}
	}
}

func diagnostics(ctx context.Context, url string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, diagnosticTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/diagnostics", nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxDiagnosticBytes+1))
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK || len(data) > maxDiagnosticBytes {
		return nil, errors.New("invalid diagnostic response")
	}
	var result map[string]string
	err = json.Unmarshal(data, &result)
	return result, err
}

func validateIdentity(config schema.Configuration, diagnostic map[string]string) error {
	if diagnostic["level"] != config.Level {
		return errors.New("target ISA does not match scheduled payload")
	}
	if config.Mode != nativeMode && (diagnostic["MICROFAT_EXEC_MODE"] != config.Mode ||
		diagnostic["MICROFAT_SELECTED_VARIANT"] != config.Level) {
		return errors.New("actual dispatch differs from requested mode or ISA")
	}
	if config.Tuning == tuningMatched && diagnostic["gomaxprocs"] != config.Environment["GOMAXPROCS"] {
		return errors.New("effective runtime tuning differs from matched configuration")
	}
	if config.Tuning == tuningMatched {
		if memory := config.Environment["GOMEMLIMIT"]; memory != "" {
			value, err := cgroup.ParseByteSize(memory)
			if err != nil || diagnostic["gomemlimit"] != strconv.FormatInt(value, 10) {
				return errors.New("effective GOMEMLIMIT differs from matched configuration")
			}
		}
		gc := config.Environment["GOGC"]
		if gc == "off" {
			gc = "18446744073709551615" // runtime/metrics represents disabled GOGC as MaxUint64.
		}
		if gc != "" && diagnostic["gogc"] != gc {
			return errors.New("effective GOGC differs from matched configuration")
		}
	}
	return nil
}

type sampler struct {
	child     *process.Child
	phaseFile string
	phaseErr  error
}

func startSampler(ctx context.Context, pid int, target *system.Sandbox, intervalMS int, helper, phaseFile string) (*sampler, error) {
	if err := os.WriteFile(phaseFile, []byte("startup"), evidenceFilePerm); err != nil {
		return nil, err
	}
	data, err := schema.CanonicalV2(system.ObserverConfig{PID: pid, PhaseFile: phaseFile, IntervalMS: intervalMS, CgroupPaths: target.Paths})
	if err != nil {
		return nil, err
	}
	child, err := process.Start(ctx, process.Spec{Path: helper, Args: []string{"benchmark", "observe", string(data)}, Env: cleanEnvironment()})
	if err != nil {
		return nil, err
	}
	return &sampler{child: child, phaseFile: phaseFile}, nil
}

func (s *sampler) setPhase(phase string) {
	if s == nil {
		return
	}
	temporary := s.phaseFile + ".tmp"
	if err := os.WriteFile(temporary, []byte(phase), evidenceFilePerm); err != nil {
		s.phaseErr = errors.Join(s.phaseErr, err)
		return
	}
	s.phaseErr = errors.Join(s.phaseErr, os.Rename(temporary, s.phaseFile))
}

func (s *sampler) stop() ([]schema.ResourceSample, error) {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	err := errors.Join(s.phaseErr, s.child.Stop(ctx))
	var samples []schema.ResourceSample
	for _, line := range bytes.Split(bytes.TrimSpace(s.child.Stdout()), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var sample schema.ResourceSample
		if decodeErr := json.Unmarshal(line, &sample); decodeErr != nil {
			return samples, errors.Join(err, decodeErr)
		}
		samples = append(samples, sample)
	}
	if len(samples) == 0 {
		err = errors.Join(err, errors.New("observer returned no samples"))
	}
	return samples, err
}

func addPhaseMetrics(metrics map[string]schema.Measurement, samples []schema.ResourceSample) {
	first := make(map[string]schema.Measurement)
	for _, sample := range samples {
		for name, value := range sample.Metrics {
			if value.Value == nil {
				continue
			}
			key := sample.Phase + "_sampled_peak_" + name
			if value.Unit != "bytes" || strings.HasPrefix(name, "cpu.stat/") {
				key = sample.Phase + "_sampled_delta_" + name
				before, ok := first[key]
				if !ok {
					first[key] = value
					metrics[key] = schema.Unavailable(value.Unit, value.Phase, value.Source, "only one phase sample")
					continue
				}
				metrics[key] = system.Delta(before, value)
				continue
			}
			current := metrics[key]
			if current.Value == nil || *current.Value < *value.Value {
				value.Kind = "derived"
				metrics[key] = value
			}
		}
	}
}

func measureTrial(ctx context.Context, cfg ExperimentConfig, opts RunOptions, generator *system.Sandbox,
	sampler *sampler, url string, trial *schema.ProcessTrial, files map[string][]byte) {
	driver := &load.Fortio{Path: opts.Fortio, Env: cleanEnvironment(),
		Wrap: func(spec process.Spec) process.Spec { return generator.Wrap(opts.Helper, spec) }}
	options := load.Options{Duration: time.Duration(cfg.DurationMS) * time.Millisecond, QPS: cfg.QPS,
		Concurrency: cfg.Concurrency, Resolution: cfg.Resolution, RawPath: "trials/" + trial.ID + "/fortio.json"}
	if cfg.WarmupMS > 0 {
		sampler.setPhase("warmup")
		warmOptions := options
		warmOptions.Duration, warmOptions.RawPath = time.Duration(cfg.WarmupMS)*time.Millisecond, "trials/"+trial.ID+"/warmup.json"
		if err := driver.Configure(warmOptions); err != nil {
			trial.Reason = err.Error()
			return
		}
		warm, err := driver.Run(ctx, url+"/"+cfg.Workload)
		if warm != nil {
			files[warmOptions.RawPath] = warm.Raw
		}
		if err != nil {
			trial.Reason = "warmup: " + err.Error()
			return
		}
	}
	sampler.setPhase("steady_state")
	if err := driver.Configure(options); err != nil {
		trial.Reason = err.Error()
		return
	}
	result, err := driver.Run(ctx, url+"/"+cfg.Workload)
	if result != nil {
		files[options.RawPath], files["trials/"+trial.ID+"/fortio.stderr"] = result.Raw, result.Stderr
		trial.Generator = result.Process
		for name, metric := range result.Resources {
			trial.Metrics["generator_"+name] = metric
		}
	}
	if err != nil {
		trial.Reason = err.Error()
		return
	}
	trial.Load = &result.HTTP
	trial.Warnings = append(trial.Warnings, result.HTTP.Warnings...)
	trial.Metrics["throughput_qps"] = schema.Measured(result.HTTP.ActualQPS, "requests/s", "steady_state", "fortio")
	addDensityMetrics(cfg, trial)
	trial.Metrics["request_errors"] = schema.Measured(float64(result.HTTP.Requests-result.HTTP.Successful), "count", "steady_state", "fortio")
	for name, value := range result.HTTP.PercentilesSeconds {
		trial.Metrics["latency_"+name] = schema.Measured(value, "s", "steady_state", "fortio-histogram")
	}
	if result.HTTP.Successful != result.HTTP.Requests {
		trial.Reason = "HTTP or transport errors"
		return
	}
	trial.Outcome = schema.OutcomeOK
	return
}

type trialCleanup struct {
	ctx               context.Context
	cfg               ExperimentConfig
	config            schema.Configuration
	opts              RunOptions
	target            *system.Sandbox
	artifact          schema.Artifact
	args, environment []string
	child             *process.Child
	sampler           *sampler
	trial             *schema.ProcessTrial
	files             map[string][]byte
}

func (c trialCleanup) finish() {
	child, sampler, trial, files := c.child, c.sampler, c.trial, c.files
	stopCtx, stopCancel := context.WithTimeout(context.Background(), stopTimeout)
	defer stopCancel()
	stopErr := child.Stop(stopCtx)
	if stopErr != nil && trial.Outcome == schema.OutcomeOK {
		trial.Outcome, trial.Reason = schema.OutcomeFailed, stopErr.Error()
	}
	if sampler != nil {
		var err error
		trial.Samples, err = sampler.stop()
		if err != nil {
			trial.Warnings = append(trial.Warnings, "incomplete telemetry: "+err.Error())
		}
	}
	addPhaseMetrics(trial.Metrics, trial.Samples)
	maps.Copy(trial.Metrics, system.Usage(child.State()))
	if c.cfg.ExecDiagnostics && c.ctx.Err() == nil {
		c.diagnose()
	}
	files["trials/"+trial.ID+"/target.stderr"] = child.Stderr()
	if err := encodeExtra(files, "trials/"+trial.ID+"/telemetry.json", trial.Samples); err != nil {
		trial.Outcome, trial.Reason = schema.OutcomeFailed, err.Error()
	}
	trial.TelemetryPath = "trials/" + trial.ID + "/telemetry.json"
	trial.SampleCount, trial.SampleIntervalMS = len(trial.Samples), c.cfg.SampleMS
	trial.Samples = nil // Retained once in the checksummed telemetry file, not duplicated in raw/outcome JSON.
}

func (c trialCleanup) diagnose() {
	parentCtx, opts, target, artifact := c.ctx, c.opts, c.target, c.artifact
	args, environment, trial, files := c.args, slices.Clone(c.environment), c.trial, c.files
	// Repeat the declared materialization state independently of the primary trial.
	cache, err := os.MkdirTemp(filepath.Dir(artifact.Path), "diagnostic-cache-")
	if err != nil {
		trial.Warnings = append(trial.Warnings, "exec diagnostic cache: "+err.Error())
		return
	}
	for i, value := range environment {
		if strings.HasPrefix(value, "MICROFAT_CACHE_DIR=") {
			environment[i] = "MICROFAT_CACHE_DIR=" + cache
		}
	}
	if c.config.Cache == "warm" {
		if err := prewarm(artifact.Path, c.config.Level, cache); err != nil {
			trial.Warnings = append(trial.Warnings, "exec diagnostic prewarm: "+err.Error())
			return
		}
	}
	diagnostic := system.TraceExec(parentCtx, target.Wrap(opts.Helper,
		process.Spec{Path: artifact.Path, Args: args, Env: environment}))
	if diagnostic.Status != "observed" {
		trial.Warnings = append(trial.Warnings, "exec diagnostic: "+diagnostic.Reason)
	}
	if err := encodeExtra(files, "trials/"+trial.ID+"/exec-diagnostic.json", diagnostic); err != nil {
		trial.Warnings = append(trial.Warnings, "exec diagnostic serialization: "+err.Error())
	}
}

func trialSampler(ctx context.Context, cfg ExperimentConfig, opts RunOptions, root string, target *system.Sandbox,
	trial *schema.ProcessTrial) (*sampler, error) {
	if cfg.DisableObserver {
		trial.Warnings = append(trial.Warnings, "observer disabled for overhead calibration")
		return nil, nil
	}
	s, err := startSampler(ctx, trial.Target.PID, target, cfg.SampleMS, opts.Helper, filepath.Join(root, trial.ID+"-phase"))
	if s != nil {
		trial.Observer = schema.ProcessIdentity{PID: s.child.PID(), Executable: opts.Helper}
	}
	return s, err
}

func finalizeTrial(parentCtx context.Context, start time.Time, trial *schema.ProcessTrial, files map[string][]byte) {
	trial.DurationNS = time.Since(start).Nanoseconds()
	if parentCtx.Err() != nil {
		trial.Outcome, trial.Reason = schema.OutcomeCancelled, parentCtx.Err().Error()
	}
	if trial.Outcome != schema.OutcomeOK && trial.Reason == "" {
		trial.Reason = "trial failed before completion"
	}
	success := 0.0
	if trial.Outcome == schema.OutcomeOK {
		success = 1
	}
	trial.Metrics["trial_success"] = schema.Measured(success, "fraction", "trial", "harness-outcome")
	if err := encodeExtra(files, "trials/"+trial.ID+"/outcome.json", trial); err != nil {
		trial.Outcome, trial.Reason = schema.OutcomeFailed, err.Error()
	}
}

func addArtifactMetrics(config schema.Configuration, artifact schema.Artifact, built *builtArtifacts, trial *schema.ProcessTrial) {
	if config.Mode != nativeMode {
		var payloadBytes int64
		for _, payload := range built.Artifacts {
			if strings.HasPrefix(payload.ID, "native-") {
				payloadBytes += payload.Bytes
			}
		}
		ratio := schema.Measured(float64(payloadBytes)/float64(artifact.Bytes), "ratio", "artifact", "sum-native-bytes/fat-file-bytes")
		ratio.Kind = "derived"
		trial.Metrics["packaging_ratio"] = ratio
	}
}
