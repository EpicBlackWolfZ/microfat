//go:build linux

package main

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
)

const (
	workerTimeout    = 2 * time.Minute
	minimumDiskBytes = 2 * 1024 * mebibyte
	cgroupOOMEvents  = "memory.events/oom"
	cgroupOOMKills   = "memory.events/oom_kill"
)

type attempt struct {
	Cell           cell                          `json:"cell"`
	Block          int                           `json:"block"`
	Arm            string                        `json:"arm"`
	ExitCode       *int                          `json:"exit_code"`
	TimedOut       bool                          `json:"timed_out"`
	Controls       []schema.Control              `json:"controls"`
	Cgroup         map[string]schema.Measurement `json:"cgroup"`
	Worker         workerResult                  `json:"worker"`
	ArtifactSHA256 string                        `json:"artifact_sha256,omitempty"`
	ArtifactBytes  int64                         `json:"artifact_bytes,omitempty"`
	Failure        string                        `json:"failure,omitempty"`
	ControlError   string                        `json:"control_error,omitempty"`
}

func (c campaign) collect(ctx context.Context, trials []pair, groups map[string]map[string]string) ([]attempt, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	var results []attempt
	for _, trial := range trials {
		for _, arm := range trial.Arms {
			n := len(results)
			dir := filepath.Join(c.Output, fmt.Sprintf("attempt-%04d", n))
			if err := os.Mkdir(dir, privateDir); err != nil {
				return results, err
			}
			workspace := filepath.Join(dir, "pack")
			if err := os.Mkdir(workspace, privateDir); err != nil {
				return results, err
			}
			input := workerInput{
				Arch: c.Arch, Family: c.Family, Profile: trial.Cell.PackProfile,
				Stub: filepath.Join(c.Corpus, "stub-full"), Output: filepath.Join(workspace, "artifact"),
				Variants: groups[trial.Cell.BinaryProfile], Serial: arm == serialArm,
			}
			observation := attempt{Cell: trial.Cell, Block: trial.Block, Arm: arm}
			if err := recordAttempt(ctx, dir, c.Root, executable, input, &observation); err != nil {
				return append(results, observation), err
			}
			results = append(results, observation)
		}
	}
	return results, nil
}

func recordAttempt(ctx context.Context, dir, root, executable string, input workerInput, a *attempt) error {
	inputPath := filepath.Join(dir, "input.json")
	if err := saveJSON(inputPath, input); err != nil {
		return err
	}
	// The planned invocation survives a cancellation or torn final observation.
	if err := saveJSON(filepath.Join(dir, "intent.json"), a); err != nil {
		return err
	}
	if err := checkDisk(dir); err != nil {
		a.ControlError = err.Error()
		return errors.Join(err, saveJSON(filepath.Join(dir, "observation.json"), a))
	}
	var runErr error
	stdout, stderr, runErr := a.execute(ctx, root, executable, dir, input)
	if runErr != nil {
		a.Failure = runErr.Error()
	}
	if runErr == nil {
		hash, size, err := identify(input.Output)
		if err != nil {
			a.Failure = err.Error()
		} else {
			a.ArtifactSHA256, a.ArtifactBytes = hash, size
		}
	}
	err := errors.Join(
		os.WriteFile(filepath.Join(dir, "stdout.log"), stdout, privateFile),
		os.WriteFile(filepath.Join(dir, "stderr.log"), stderr, privateFile),
		saveJSON(filepath.Join(dir, "observation.json"), a),
	)
	if err == nil {
		// A killed Pack can leave private snapshots and temporary output behind.
		// Preserve logs and observations before removing this attempt's owned workspace.
		err = os.RemoveAll(filepath.Join(dir, "pack"))
	}
	if a.ControlError != "" {
		return errors.Join(err, errors.New(a.ControlError))
	}
	return err
}

func (a *attempt) execute(ctx context.Context, root, executable, dir string, input workerInput) (stdout, stderr []byte, retErr error) {
	target, err := system.Prepare(system.Options{
		CgroupRoot: root, Version: "v2", CPUPeriodUS: system.DefaultPeriod,
		CPUQuotaUS: int64(a.Cell.CPUs) * system.DefaultPeriod, MemoryBytes: int64(a.Cell.MemoryMiB) * mebibyte,
	})
	if err != nil {
		a.ControlError = err.Error()
		return nil, nil, err
	}
	defer func() {
		if err := target.Close(); err != nil {
			a.ControlError = "cleanup: " + err.Error()
			retErr = errors.Join(retErr, err)
		}
	}()
	a.Controls = target.Controls
	if len(target.Paths) != 1 || len(target.Procs) != 1 {
		a.ControlError = "CPU and memory controls did not take effect"
		return nil, nil, errors.New(a.ControlError)
	}
	if err := disableSwap(target.Paths[0]); err != nil {
		a.ControlError = err.Error()
		return nil, nil, err
	}
	a.Controls = append(a.Controls, schema.Control{Name: "memory.swap.max", Requested: "0", Effective: "0", State: "applied"})
	input.Cgroup = target.Paths[0]
	workerPath := filepath.Join(dir, "worker.json")
	if err := saveJSON(workerPath, input); err != nil {
		a.ControlError = err.Error()
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workerTimeout)
	defer cancel()
	env := []string{"GOMAXPROCS=" + strconv.Itoa(a.Cell.CPUs), "GOGC=100", "GOMEMLIMIT=off", "LC_ALL=C", "TZ=UTC",
		"TMPDIR=" + filepath.Dir(input.Output)}
	child, err := process.Start(ctx, target.Wrap(executable, process.Spec{
		Path: executable, Args: []string{"-worker", workerPath}, Env: env,
	}))
	if err != nil {
		a.ControlError = err.Error()
		return nil, nil, err
	}
	waitErr := child.Wait()
	a.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	a.ExitCode = new(child.State().ExitCode())
	a.Cgroup = target.Read("complete-pack")
	stdout, stderr = child.Stdout(), child.Stderr()
	if !hasTelemetry(*a) {
		a.ControlError = "cgroup v2 memory.peak or memory.events OOM counters unavailable"
	}
	if waitErr != nil && len(stdout) == 0 && (a.TimedOut || a.oom()) {
		return stdout, stderr, waitErr
	}
	if err := json.Unmarshal(stdout, &a.Worker); err != nil {
		if !a.TimedOut && !a.oom() {
			a.ControlError = "worker produced no valid Pack record"
		}
		return stdout, stderr, errors.Join(waitErr, err)
	}
	if a.Worker.Cgroup != input.Cgroup {
		a.ControlError = "worker did not verify its cgroup membership"
	}
	return stdout, stderr, waitErr
}

func hasTelemetry(a attempt) bool {
	return a.Cgroup["memory.peak"].Value != nil && a.Cgroup[cgroupOOMEvents].Value != nil &&
		a.Cgroup[cgroupOOMKills].Value != nil
}
