//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"golang.org/x/sys/unix"
)

type artifactKey struct {
	group
	Setting, Stub string
}
type artifact struct{ Path, SHA256 string }

func execute(path string, args, env []string, timeout time.Duration) observation {
	obs := observation{Started: time.Now().UTC().Format(time.RFC3339Nano)}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := ownedCommand(ctx, path, args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := runOwned(cmd)
	obs.WallNS = time.Since(start).Nanoseconds()
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	obs.Stdout, obs.Stderr = stdout.String(), stderr.String()
	if cmd.ProcessState != nil {
		obs.CPUNS = (cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()).Nanoseconds()
		if usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			// Linux can retain the pre-exec shared-mm high-water mark; this does not isolate codec memory.
			obs.MaxRSSKiB, obs.ResourcesObserved = usage.Maxrss, true
		}
	}
	ceiling := int64(launchRSSKiB)
	if timeout == packTimeout {
		ceiling = packRSSKiB
	}
	// Linux may report zero RSS for tiny native ELF controls; the wait4 record is still present.
	if !obs.ResourcesObserved || obs.MaxRSSKiB < 0 || obs.MaxRSSKiB > ceiling || obs.CPUNS < 0 || obs.WallNS <= 0 {
		err = errors.Join(err, errors.New("resource observation missing or exceeds prospective RSS ceiling"))
	}
	obs.Error, obs.Success = errorText(err), err == nil
	return obs
}

func measure(output string, plan []step, items prepared) ([]observation, error) {
	// #nosec G304 -- newly created private operator-selected evidence directory.
	file, err := os.OpenFile(filepath.Join(output, "observations.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, privateFile)
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- same fresh private evidence directory; never overwrite previous observations.
	prep, err := os.OpenFile(filepath.Join(output, "preparation.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, privateFile)
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	artifacts := map[artifactKey]artifact{}
	var observations []observation
	var prepErr error
	preparedMinimals := false
	for _, visit := range plan {
		if visit.Phase == "launch" && !preparedMinimals {
			preparedMinimals = true
			for _, g := range groups() {
				for _, s := range settings() {
					input, pathErr := packInput(output, items, step{group: g, Setting: s.Name, Stub: minimalStub, Phase: "preparation"})
					obs := observation{step: step{group: g, Setting: s.Name, Stub: minimalStub, Phase: "preparation"}, Error: errorText(pathErr)}
					if pathErr == nil {
						obs = packAttempt(input, items)
					}
					obs.step = step{group: g, Setting: s.Name, Stub: minimalStub, Phase: "preparation"}
					if obs.Success {
						artifacts[artifactKey{g, s.Name, minimalStub}] = artifact{input.Target, obs.SHA256}
					} else {
						prepErr = errors.Join(prepErr, fmt.Errorf("minimal preparation %v/%s: %s", g, s.Name, obs.Error))
					}
					if err := appendRow(prep, obs); err != nil {
						return observations, errors.Join(err, file.Close(), prep.Close())
					}
				}
			}
		}
		var obs observation
		if visit.Phase == "pack" {
			input, inputErr := packInput(output, items, visit)
			obs = observation{Error: errorText(inputErr)}
			if inputErr == nil {
				obs = packAttempt(input, items)
			}
			if obs.Success {
				key := artifactKey{visit.group, visit.Setting, fullStub}
				if previous, exists := artifacts[key]; exists {
					if previous.SHA256 != obs.SHA256 {
						obs.Success, obs.Error = false, "repeated complete artifact SHA-256 differs"
					}
				} else {
					artifacts[key] = artifact{input.Target, obs.SHA256}
				}
			}
		} else {
			obs = launchAttempt(output, visit, items, artifacts)
		}
		obs.step = visit
		observations = append(observations, obs)
		if err := appendRow(file, obs); err != nil {
			return observations, errors.Join(err, file.Close(), prep.Close())
		}
		if (visit.Sequence+1)%200 == 0 {
			fmt.Printf("recorded %d/%d attempts\n", visit.Sequence+1, len(plan))
		}
	}
	return observations, errors.Join(prepErr, file.Close(), prep.Close())
}

func appendRow(file *os.File, value any) error {
	if err := json.NewEncoder(file).Encode(value); err != nil {
		return err
	}
	return file.Sync()
}

func packInput(output string, items prepared, visit step) (workerInput, error) {
	input := workerInput{Group: visit.group, Stub: items.Stubs[visit.Stub], Arch: items.Corpus.Arch, Variants: map[string]string{}}
	for _, s := range settings() {
		if s.Name == visit.Setting {
			input.Setting = s
		}
	}
	if input.Setting.Name == "" {
		return input, errors.New("unknown setting")
	}
	for level, v := range items.Variants[visit.group] {
		input.Variants[level] = v.Path
	}
	input.Target = filepath.Join(output, "attempts", fmt.Sprintf("%s-%s-%s-%s-%s-%02d", visit.Phase, visit.Family,
		visit.Profile, visit.Setting, visit.Stub, visit.Round))
	return input, nil
}

func packAttempt(input workerInput, items prepared) observation {
	inputPath := input.Target + ".input.json"
	if err := saveJSON(inputPath, input); err != nil {
		return observation{Error: err.Error()}
	}
	harness, err := os.Executable()
	if err != nil {
		return observation{Error: err.Error()}
	}
	obs := execute(harness, []string{"-worker", inputPath}, items.Study.env(), packTimeout)
	var report workerResult
	reportErr := json.Unmarshal([]byte(obs.Stdout), &report)
	obs.Warnings = report.Warnings
	err = errors.Join(reportErr)
	if report.Error != "" {
		err = errors.Join(err, errors.New(report.Error))
	}
	if obs.Success && err == nil {
		id, sizes, variants, verifyErr := verifyArtifact(input.Target, input.Stub, items.Variants[input.Group])
		err = verifyErr
		obs.SHA256, obs.Bytes, obs.Sizes, obs.Variants = id.SHA256, id.Bytes, sizes, variants
	}
	if err != nil {
		obs.Error = errorText(errors.Join(errors.New(obs.Error), err))
		obs.Success = false
	}
	return obs
}

func verifyArtifact(path, stub string, wanted map[string]variant) (identity, map[string]int64, any, error) {
	id, err := identify(path)
	if err != nil {
		return id, nil, nil, err
	}
	file, err := os.Open(path) // #nosec G304 -- newly packed experiment-owned artifact.
	if err != nil {
		return id, nil, nil, err
	}
	idx, results, verifyErr := pack.VerifyBinary(file, id.Bytes)
	var trailer [format.TrailerSize]byte
	_, readErr := file.ReadAt(trailer[:], id.Bytes-format.TrailerSize)
	err = errors.Join(verifyErr, readErr, file.Close())
	if err != nil {
		return id, nil, nil, err
	}
	if len(results) != len(wanted) || idx.Version != format.FormatVersion2 || idx.CreatedUnix != 0 {
		return id, nil, nil, errors.New("format/epoch/variant count differs")
	}
	for _, r := range results {
		v, exists := wanted[r.Level]
		if !exists || !r.Valid || r.ActualSHA256 != v.SHA256 || r.UncompressedSize != v.Bytes {
			return id, nil, nil, fmt.Errorf("complete verification differs for %s: %s", r.Level, r.ErrorString)
		}
	}
	stubID, err := identify(stub)
	if err != nil {
		return id, nil, nil, err
	}
	// #nosec G115 -- ReadTrailerAndIndex inside VerifyBinary already bounded the index size to the actual int64 file size.
	indexBytes := int64(binary.LittleEndian.Uint64(trailer[8:16]))
	sizes := map[string]int64{"complete": id.Bytes, "stub": stubID.Bytes, "dictionary": idx.DictionarySize,
		"frames": 0, "index": indexBytes, "trailer": format.TrailerSize}
	for _, v := range idx.Variants {
		sizes["frames"] += v.CompressedSize
	}
	if sizes["stub"]+sizes["dictionary"]+sizes["frames"]+sizes["index"]+sizes["trailer"] != id.Bytes {
		return id, sizes, idx.Variants, errors.New("complete component sizes do not sum to file size")
	}
	return id, sizes, idx.Variants, nil
}

func baselineLevel() string {
	if runtime.GOARCH == archARM64 {
		return "v8.0"
	}
	return "v1"
}

func launchAttempt(output string, visit step, items prepared, artifacts map[artifactKey]artifact) observation {
	selected := items.Variants[visit.group][baselineLevel()]
	path := selected.Path
	wantHash := selected.SHA256
	if visit.Setting != nativeSetting {
		item, exists := artifacts[artifactKey{visit.group, visit.Setting, visit.Stub}]
		if !exists {
			return observation{Error: "no successfully packed and verified artifact for planned launch"}
		}
		path = item.Path
		wantHash = item.SHA256
	}
	id, identityErr := identify(path)
	if identityErr != nil || id.SHA256 != wantHash {
		return observation{Error: errorText(errors.Join(identityErr, errors.New("launch artifact identity changed")))}
	}
	cache := filepath.Join(output, "attempts", fmt.Sprintf("cache-%05d", visit.Sequence))
	if err := os.Mkdir(cache, privateDir); err != nil {
		return observation{Error: err.Error()}
	}
	if visit.Mode == "warm-cache" && visit.Setting != nativeSetting {
		data, err := os.ReadFile(selected.Path)
		if err == nil {
			// #nosec G703 -- selected SHA-256 was checked against frozen bytes; cache is new and private.
			err = os.WriteFile(filepath.Join(cache, selected.SHA256), data, privateDir)
		}
		if err != nil {
			return observation{Error: errorText(errors.Join(err, os.RemoveAll(cache)))}
		}
	}
	mode := "cache"
	if visit.Mode == memfdMode {
		mode = memfdMode
	}
	env := items.Study.env("MICROFAT_CACHE_DIR="+cache, "MICROFAT_FORCE_LEVEL="+baselineLevel(), "MICROFAT_EXEC_MODE="+mode,
		"MICROFAT_AUTOTUNE=0")
	obs := execute(path, versionArgs(visit.Family, output), env, launchTimeout)
	control := items.Controls[visit.group]
	var validationErr error
	if obs.Stdout != control.Stdout || obs.Stderr != control.Stderr {
		validationErr = errors.New("readiness output differs from unchanged native control")
	}
	empty := visit.Mode == memfdMode || visit.Setting == nativeSetting
	validationErr = errors.Join(validationErr, checkCache(cache, selected, empty), os.RemoveAll(cache))
	if validationErr != nil {
		obs.Success, obs.Error = false, errorText(errors.Join(errors.New(obs.Error), validationErr))
	}
	return obs
}

func checkCache(dir string, selected variant, empty bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if empty {
		if len(entries) != 0 {
			return errors.New("memfd/native populated private cache")
		}
		return nil
	}
	if len(entries) != 1 || entries[0].Name() != selected.SHA256 {
		return errors.New("cache does not contain only selected payload hash")
	}
	path := filepath.Join(dir, selected.SHA256)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != privateDir {
		return errors.New("cache file is not regular mode0700")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(stat.Uid) != int64(os.Geteuid()) {
		return errors.New("cache file owner differs")
	}
	id, err := identify(path)
	if err != nil {
		return err
	}
	if id.SHA256 != selected.SHA256 || id.Bytes != selected.Bytes {
		return errors.New("cache payload identity differs")
	}
	return nil
}

func ownedCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	// #nosec G204 -- fixed local build/version commands or frozen experiment-owned executables.
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return cmd
}

// Observe the leader without reaping it so its PID continues to reserve our
// process group until descendants have been signaled. Signaling after Cmd.Wait
// would risk targeting a reused group. Descendants that create a new session or
// process group are outside this ownership boundary.
func runOwned(cmd *exec.Cmd) error {
	// Cmd.Wait reaps the leader before draining its context watcher. Stop and
	// drain our raw group-signal callback first, while the leader is reserved.
	var cancelMu sync.Mutex
	cancelActive := true
	cancel := cmd.Cancel
	if cancel != nil {
		cmd.Cancel = func() error {
			cancelMu.Lock()
			defer cancelMu.Unlock()
			if !cancelActive {
				return os.ErrProcessDone
			}
			return cancel()
		}
	}
	disableCancel := func() {
		cancelMu.Lock()
		defer cancelMu.Unlock()
		cancelActive = false
	}
	if err := cmd.Start(); err != nil {
		disableCancel()
		return err
	}
	var observeErr error
	var info unix.Siginfo
	for {
		observeErr = unix.Waitid(unix.P_PID, cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(observeErr, unix.EINTR) {
			break
		}
	}
	disableCancel()
	if observeErr != nil {
		// Do not signal a group whose reserved-leader state could not be proved.
		killErr := cmd.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
		return errors.Join(fmt.Errorf("observe owned leader exit without reaping: %w", observeErr), killErr, cmd.Wait())
	}
	cleanupErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(cleanupErr, syscall.ESRCH) {
		cleanupErr = nil
	}
	return errors.Join(cleanupErr, cmd.Wait())
}
