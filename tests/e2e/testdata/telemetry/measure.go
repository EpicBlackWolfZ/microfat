//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

func measure(output string, item artifacts, study studyConfig) ([]observation, error) {
	if err := os.Mkdir(filepath.Join(output, "attempts"), privateDirectory); err != nil {
		return nil, err
	}
	// #nosec G304 -- new raw evidence in the operator-selected private output directory.
	f, err := os.OpenFile(filepath.Join(output, "observations.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, privateFile)
	if err != nil {
		return nil, err
	}
	encoder := json.NewEncoder(f)
	var observations []observation
	plan := scheduleFor(study)
	for _, step := range plan {
		obs := attempt(output, step, item)
		observations = append(observations, obs)
		if err := encoder.Encode(obs); err != nil {
			return observations, errors.Join(err, f.Close())
		}
		if err := f.Sync(); err != nil {
			return observations, errors.Join(err, f.Close())
		}
		const progressInterval = 160
		if (step.Sequence+1)%progressInterval == 0 {
			fmt.Printf("recorded %d/%d planned attempts\n", step.Sequence+1, len(plan))
		}
	}
	return observations, f.Close()
}

func attempt(output string, step scheduled, item artifacts) observation {
	obs := observation{scheduled: step, Started: time.Now().UTC().Format(time.RFC3339Nano)}
	cacheDir := filepath.Join(output, "attempts", fmt.Sprintf("%04d", step.Sequence))
	if err := os.Mkdir(cacheDir, privateDirectory); err != nil {
		obs.Error = err.Error()
		return obs
	}
	if step.Path == warm {
		data, err := os.ReadFile(item.Native)
		if err == nil {
			// #nosec G703 -- fixture checksum filename in a newly created private attempt directory.
			err = os.WriteFile(filepath.Join(cacheDir, item.SelectedSHA256), data, privateDirectory)
		}
		if err != nil {
			obs.Error, obs.CleanupError = err.Error(), errorText(os.RemoveAll(cacheDir))
			return obs
		}
	}
	path := item.Bundles[step.Profile+"/"+step.Arm]
	if step.Profile == native {
		path = item.Native
	}
	mode := "cache"
	if step.Path == memfd {
		mode = memfd
	}
	env := []string{"MICROFAT_CACHE_DIR=" + cacheDir, "MICROFAT_FORCE_LEVEL=" + item.Level, "MICROFAT_EXEC_MODE=" + mode}
	if step.Logging == "json" {
		env = append(env, "MICROFAT_LOG=json")
	}
	if step.Logging == "debug" {
		env = append(env, "MICROFAT_DEBUG=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
	cmd := exec.CommandContext(ctx, path, "--startup-only") // #nosec G204 -- frozen experiment-owned executable.
	cmd.Env = cleanEnv(env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	obs.WallNS = time.Since(start).Nanoseconds()
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	cancel()
	obs.Error, obs.Stdout, obs.Stderr = errorText(err), stdout.String(), stderr.String()
	if cmd.ProcessState != nil {
		obs.CPUNS = (cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()).Nanoseconds()
		if usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			obs.MaxRSSKiB = usage.Maxrss
		}
	}
	validationErr := validateOutput(step, item, obs.Stdout, obs.Stderr)
	if validationErr != nil {
		obs.Error = errorText(errors.Join(err, validationErr))
	}
	obs.CacheError = errorText(checkCache(cacheDir, item.SelectedSHA256, step.Path == memfd || step.Path == native))
	obs.CleanupError = errorText(os.RemoveAll(cacheDir))
	if obs.MaxRSSKiB <= 0 || obs.MaxRSSKiB > maxRSSKiB || obs.CPUNS <= 0 || obs.WallNS <= 0 {
		obs.Error = errorText(errors.Join(errors.New("missing resource observation or RSS exceeds 512 MiB"),
			validationErr, err))
	}
	obs.Success = obs.Error == "" && obs.CacheError == "" && obs.CleanupError == ""
	return obs
}

func validateOutput(step scheduled, item artifacts, stdout, stderr string) error {
	if stdout != ready {
		return fmt.Errorf("stdout differs from unique fixture marker: %q", stdout)
	}
	if step.Logging == off {
		if stderr != "" {
			return errors.New("disabled telemetry wrote stderr")
		}
		return nil
	}
	wantMode := "cache"
	if step.Path == memfd {
		wantMode = memfd
	}
	if step.Logging == "json" {
		line := strings.TrimSuffix(stderr, "\n")
		if !strings.HasPrefix(line, "[microfat] ") || strings.Contains(line, "\n") {
			return errors.New("JSON logging must emit exactly one dispatch line")
		}
		var event format.DispatchTelemetry
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "[microfat] ")), &event); err != nil {
			return err
		}
		if event.Event != format.EventDispatch || event.SelectedVariant != item.Level ||
			event.SelectedSHA256 != item.SelectedSHA256 || event.ExecMode != wantMode || event.TimestampUnixNano <= 0 {
			return errors.New("JSON dispatch fields differ from fixed fixture/path")
		}
		return nil
	}
	if step.Logging != "debug" {
		return errors.New("unknown logging mode")
	}
	if strings.Count(stderr, "[microfat:debug] host_arch=") != 1 ||
		!strings.Contains(stderr, " selected_variant="+item.Level+" ") ||
		!strings.Contains(stderr, " exec_mode="+wantMode+" ") ||
		!strings.Contains(stderr, " decompress_us=") || !strings.Contains(stderr, " total_us=") {
		return errors.New("debug dispatch fields differ from fixed fixture/path")
	}
	for _, line := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
		if !strings.HasPrefix(line, "[microfat:debug] ") {
			return errors.New("unexpected non-debug stderr")
		}
	}
	return nil
}

func checkCache(dir, wantHash string, empty bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if empty {
		if len(entries) != 0 {
			return errors.New("memfd/native populated cache")
		}
		return nil
	}
	if len(entries) != 1 || entries[0].Name() != wantHash {
		return errors.New("cache must contain only selected SHA-256")
	}
	path := filepath.Join(dir, wantHash)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != privateDirectory {
		return errors.New("cache is not regular mode 0700")
	}
	id, err := identify(path)
	if err != nil {
		return err
	}
	if id.SHA256 != wantHash {
		return errors.New("cache bytes differ from selected payload")
	}
	return nil
}
