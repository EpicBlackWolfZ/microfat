package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	json "encoding/json/v2"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

var producerRevision = "unversioned"

const maximumCacheFiles = 32

func runCampaign(ctx context.Context, in request, requestSHA, directory string, runner processRunner) (report, error) {
	if err := validateRequest(in); err != nil {
		return report{}, err
	}
	if producerRevision != in.SourceRevision {
		return report{}, errors.New("requested source revision differs from producer build")
	}
	if err := checkExecutables(in); err != nil {
		return report{}, err
	}
	if err := os.Mkdir(directory, privateDirMode); err != nil {
		return report{}, err
	}
	start := time.Now()
	result := report{SchemaVersion: schemaVersion, Request: in, RequestSHA256: requestSHA, ProducerRevision: producerRevision,
		GoVersion: runtime.Version(), MeasurementKind: measurementKind, StartedUTC: start.UTC().Format(time.RFC3339Nano)}
	budget, cancel := context.WithTimeout(ctx, time.Duration(in.CampaignBudgetNS))
	defer cancel()
	for block := 0; block < in.SamplesPerArm; block++ {
		for _, app := range in.Apps {
			for _, sc := range in.Scenarios {
				arms := []string{armBaseline, armPrewarm}
				if block%armCount != 0 {
					arms[0], arms[1] = arms[1], arms[0]
				}
				for _, arm := range arms {
					if budget.Err() != nil {
						result.StopReason = "campaign context/budget exhausted"
						return saveCampaign(directory, result, start)
					}
					ep := runEpisode(budget, in, app, sc, arm, block, directory, runner)
					result.Episodes = append(result.Episodes, ep)
					result.WallNS = time.Since(start).Nanoseconds()
					if err := appendEpisode(filepath.Join(directory, "episodes.jsonl"), ep); err != nil {
						return result, err
					}
				}
			}
		}
	}
	if budget.Err() != nil {
		result.StopReason = "campaign context/budget exhausted"
		return saveCampaign(directory, result, start)
	}
	result.Complete = true
	return saveCampaign(directory, result, start)
}

func appendEpisode(path string, ep episode) error {
	data, err := json.Marshal(ep)
	if err != nil {
		return err
	}
	// #nosec G304 -- fixed journal name inside the exclusively created private campaign directory.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, privateFileMode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	return errors.Join(writeErr, file.Close())
}

func saveCampaign(directory string, result report, start time.Time) (report, error) {
	result.WallNS = time.Since(start).Nanoseconds()
	return result, writeJSON(filepath.Join(directory, "report.json"), result)
}

func checkExecutables(in request) error {
	identities := map[string]string{in.Tool: in.ToolSHA256}
	for _, app := range in.Apps {
		if existing, ok := identities[app.Artifact]; ok && existing != app.ArtifactSHA256 {
			return errors.New("conflicting executable digests")
		}
		identities[app.Artifact] = app.ArtifactSHA256
	}
	for path, expected := range identities {
		actual, err := hashFile(path)
		if err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf("executable digest mismatch: %s", path)
		}
	}
	return nil
}

func runEpisode(ctx context.Context, in request, app application, sc scenario, arm string, block int,
	directory string, runner processRunner) (ep episode) {
	id := fmt.Sprintf("episode-%06d", block*len(in.Apps)*len(in.Scenarios)*armCount)
	ep = episode{ID: fmt.Sprintf("%s/%s/%s/%s", id, app.Family, sc.Name, arm), Family: app.Family, Scenario: sc.Name, Arm: arm, Block: block}
	cache, err := os.MkdirTemp(directory, "cache-")
	if err != nil {
		ep.Failed = true
		ep.Error = err.Error()
		return ep
	}
	defer func() {
		if err := os.RemoveAll(cache); err != nil {
			ep.Failed = true
			ep.Error = "cache cleanup: " + err.Error()
		}
	}()
	env := processEnvironment(cache, app.Level, in.GOMAXPROCS)
	ep.Environment = env
	if arm == armPrewarm {
		args := []string{"prewarm", app.Artifact, "--cache-dir", cache, "--level", app.Level, "--json"}
		prepared := invoke(ctx, in.TimeoutNS, runner, in.Tool, args, env)
		ep.Preparation = &prepared.Stats
		ep.InitialDelayNS = prepared.Stats.WallNS
		if err := checkPrewarm(prepared, app.Level, false); err != nil {
			ep.Failed = true
			ep.TimedOut = prepared.Stats.TimedOut
			ep.Error = "prewarm: " + err.Error()
		}
	}
	ep.Launches = runLaunches(ctx, in, app, sc, arm, env, runner)
	recordLaunchCosts(&ep)
	ep.CacheBytes, ep.CacheFiles, err = cacheSize(cache)
	if err != nil {
		ep.Failed = true
		ep.Error = "cache inventory: " + err.Error()
	}
	ep.ByteTime = float64(ep.CacheBytes) * float64(sc.RetentionNS) / float64(time.Second)
	if arm == armPrewarm {
		args := []string{"prewarm", app.Artifact, "--cache-dir", cache, "--level", app.Level, "--verify", "--json"}
		verified := invoke(ctx, in.TimeoutNS, runner, in.Tool, args, env)
		ep.Verification = &verified.Stats
		if err := checkPrewarm(verified, app.Level, true); err != nil {
			ep.Failed = true
			ep.TimedOut = ep.TimedOut || verified.Stats.TimedOut
			ep.Error = "cache verification: " + err.Error()
		}
	}
	return ep
}

func recordLaunchCosts(ep *episode) {
	for _, launch := range ep.Launches {
		ep.MaxReadyNS = max(ep.MaxReadyNS, launch.ScheduledToReadyNS)
		ep.ForegroundCPUNS += launch.Process.CPUNS
		ep.ForegroundSpanNS = max(ep.ForegroundSpanNS, launch.ReadyNS)
		if !launch.Failed {
			ready := ep.InitialDelayNS + launch.ReadyNS
			if ep.DeploymentFirstReadyNS == nil || ready < *ep.DeploymentFirstReadyNS {
				ep.DeploymentFirstReadyNS = &ready
			}
		}
		ep.Failed = ep.Failed || launch.Failed
		ep.TimedOut = ep.TimedOut || launch.Process.TimedOut
	}
	ep.DeploymentSpanNS = ep.InitialDelayNS + ep.ForegroundSpanNS
	ep.TotalCPUNS = ep.ForegroundCPUNS
	if ep.Preparation != nil {
		ep.TotalCPUNS += ep.Preparation.CPUNS
	}
}

func checkPrewarm(result processResult, level string, verify bool) error {
	if result.Stats.ExitCode != 0 || result.Stats.Error != "" {
		return errors.New("command failed")
	}
	var telemetry format.PrewarmTelemetry
	if err := json.Unmarshal(result.Stdout, &telemetry); err != nil {
		return err
	}
	if len(telemetry.Results) != 1 || telemetry.Results[0].Level != level {
		return errors.New("prewarm did not select exactly the frozen variant")
	}
	if verify && !telemetry.Results[0].Valid {
		return errors.New("selected cache descriptor failed verification")
	}
	return nil
}

func runLaunches(ctx context.Context, in request, app application, sc scenario, arm string, env []string, runner processRunner) []launch {
	result := make([]launch, len(sc.OffsetsNS))
	origin := time.Now()
	var workers sync.WaitGroup
	for index, offset := range sc.OffsetsNS {
		workers.Go(func() {
			item := launch{Index: index, ScheduledNS: offset}
			if err := waitUntil(ctx, origin.Add(time.Duration(offset))); err != nil {
				item.Failed = true
				item.Process.Error = err.Error()
				item.Process.TimedOut = errors.Is(err, context.DeadlineExceeded)
				item.StartedNS = time.Since(origin).Nanoseconds()
				item.ReadyNS = item.StartedNS
			} else {
				item.StartedNS = time.Since(origin).Nanoseconds()
				measured := invoke(ctx, in.TimeoutNS, runner, app.Artifact, app.Args, env)
				item.Process = measured.Stats
				item.ReadyNS = time.Since(origin).Nanoseconds()
				item.Failed = measured.Stats.ExitCode != 0 || measured.Stats.Error != "" || measured.Stats.StdoutSHA256 != app.StdoutSHA256
				if err := readDispatch(&item, measured.Stderr, app, arm); err != nil {
					item.Failed = true
					item.DispatchError = err.Error()
				}
			}
			item.ScheduledToReadyNS = max(int64(0), item.ReadyNS-offset)
			result[index] = item
		})
	}
	workers.Wait()
	return result
}

func readDispatch(item *launch, stderr []byte, app application, arm string) error {
	for _, line := range strings.Split(string(stderr), "\n") {
		data, found := strings.CutPrefix(line, "[microfat] ")
		if !found {
			continue
		}
		var value format.DispatchTelemetry
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			return err
		}
		if value.Event != format.EventDispatch || item.Dispatch != nil {
			return errors.New("expected one successful dispatch diagnostic")
		}
		item.Dispatch = &value
	}
	actual := item.Dispatch
	if actual == nil {
		return errors.New("missing dispatch diagnostic")
	}
	if actual.HostArch != runtime.GOARCH || actual.SelectedVariant != app.Level || actual.SelectedSHA256 != app.PayloadSHA256 ||
		actual.SelectedSizeBytes != app.PayloadBytes {
		return errors.New("dispatch selected an unexpected payload")
	}
	if actual.ExecMode != format.ExecModeMemfd && actual.ExecMode != format.ExecModeCache {
		return errors.New("unexpected actual dispatch mode")
	}
	if arm == armPrewarm && actual.ExecMode != format.ExecModeCache {
		return errors.New("prewarmed launch did not use cache")
	}
	return nil
}

func waitUntil(ctx context.Context, deadline time.Time) error {
	timer := time.NewTimer(max(time.Duration(0), time.Until(deadline)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func cacheSize(directory string) (int64, int, error) {
	var size int64
	count := 0
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("nonregular cache entry")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		count++
		size += info.Size()
		if count > maximumCacheFiles || size > maximumExecutable {
			return errors.New("cache inventory exceeds bound")
		}
		return nil
	})
	return size, count, err
}
