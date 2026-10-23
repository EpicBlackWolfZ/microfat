//go:build linux

// This opt-in experiment is deliberately outside ordinary unit-test discovery.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	baselineSHA             = "c28bbe73fb4a43ddab85443865ba52c6f3ef4e01"
	goVersion               = "go1.27.3"
	archARM64               = "arm64"
	protocolPath            = "docs/verified-cache-handoff-confirmation.md"
	pairs                   = 160
	resamples               = 2000
	seed                    = 392
	launchTimeout           = 15 * time.Second
	maxRSSKiB               = 512 * 1024
	preparationTimeout      = 10 * time.Minute
	privateDirMode          = 0o700
	privateFileMode         = 0o600
	armBaseline             = "baseline"
	armCandidate            = "candidate"
	pathCold                = "cold-cache"
	pathWarm                = "warm-cache"
	pathMemfd               = "memfd"
	armCount                = 2
	controlLower            = 0.95
	controlUpper            = 1.05
	cpuUpper                = 1.10
	rssRelativeAllowance    = 0.10
	rssAbsoluteAllowanceKiB = 2048
	bootstrapTailDivisor    = 40
	progressInterval        = 180
	usefulFamilyMinimum     = 2
)

type family struct {
	Name, Module, Version, Sum, Package string
	Args                                []string
}

var corpus = []family{
	{
		"shfmt", "mvdan.cc/sh/v3", "v3.14.1", "h1:bXkhQWNHCs0KZEChF8hYS6FC+T2N9mUZLbQv9blditI=",
		"mvdan.cc/sh/v3/cmd/shfmt", []string{"--version"},
	},
	{
		"yamlfmt", "github.com/google/yamlfmt", "v0.21.0", "h1:9FKApQkDpMKgBjwLFytBHUCgqnQgxaQnci0uiESfbzs=",
		"github.com/google/yamlfmt/cmd/yamlfmt", []string{"-version"},
	},
	{
		"gitleaks", "github.com/zricethezav/gitleaks/v8", "v8.30.0", "h1:5heLlxRQkHfXgTJgdQsJhi/evX1oj6i+xBanDu2XUM8=",
		"github.com/zricethezav/gitleaks/v8", []string{"version"},
	},
}

type identity struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type prepared struct {
	Family         family `json:"family"`
	Native         string `json:"native"`
	SelectedSHA256 string `json:"selected_sha256"`
	Stdout, Stderr string
	Bundles        map[string]string `json:"bundles"`
}

type scheduled struct {
	Sequence, Round   int
	Family, Path, Arm string
}

type observation struct {
	scheduled
	Started                                         string
	WallNS, CPUNS, MaxRSSKiB                        int64
	Stdout, Stderr, Error, CacheError, CleanupError string
	Success                                         bool
}

type interval struct {
	Median, Lower95, Upper95 float64
}

type decision struct {
	Family, Path                        string
	PlannedPairs, CompletePairs         int
	WallRatio, CPURatio, RSSIncreaseKiB interval
	BaselineRSSMedianKiB                float64
	UsefulCold, WallPass, ResourcePass  bool
}

func main() {
	base := flag.String("base", "", "checkout of the fixed baseline")
	candidate := flag.String(armCandidate, "", "clean checkout of the frozen candidate")
	output := flag.String("output", "", "new evidence directory (never overwritten)")
	flag.Parse()
	if err := run(*base, *candidate, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(base, candidate, output string) (retErr error) {
	if base == "" || candidate == "" || output == "" {
		return errors.New("-base, -candidate and -output are required")
	}
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != archARM64) || runtime.Version() != goVersion {
		return fmt.Errorf("requires native Linux amd64/arm64 and Go 1.27.3; got %s/%s %s", runtime.GOOS, runtime.GOARCH, runtime.Version())
	}
	if err := absolutePaths(&base, &candidate, &output); err != nil {
		return err
	}
	if err := os.Mkdir(output, privateDirMode); err != nil {
		return fmt.Errorf("create new output directory: %w", err)
	}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, os.WriteFile(filepath.Join(output, "failure.txt"), []byte(retErr.Error()+"\n"), privateFileMode))
		}
	}()
	for _, dir := range []string{"bin", "corpus", "bundles", "attempts"} {
		if err := os.Mkdir(filepath.Join(output, dir), privateDirMode); err != nil {
			return err
		}
	}
	if err := saveJSON(filepath.Join(output, "schedule.json"), schedule()); err != nil {
		return err
	}
	if err := freezeSources(base, candidate, output); err != nil {
		return err
	}
	if err := verifyToolchain(output); err != nil {
		return err
	}
	if err := host(output, "before"); err != nil {
		return err
	}
	preparedCorpus, err := prepare(base, candidate, output)
	if err != nil {
		return err
	}
	if err := saveJSON(filepath.Join(output, "prepared.json"), preparedCorpus); err != nil {
		return err
	}
	observations, err := measure(output, preparedCorpus)
	if err != nil {
		return err
	}
	if err := host(output, "after"); err != nil {
		return err
	}
	decisions, pass := evaluate(observations)
	if err := saveJSON(filepath.Join(output, "decisions.json"), map[string]any{
		"architecture": runtime.GOARCH, "pass": pass, "planned_attempts": len(schedule()),
		"recorded_attempts": len(observations), "decisions": decisions,
		"scope": "complete version-command launches; no population p99 or service-readiness claim",
	}); err != nil {
		return err
	}
	if !pass {
		return errors.New("native confirmation failed or is inconclusive; preserve every raw observation and control")
	}
	return nil
}

func absolutePaths(paths ...*string) error {
	for _, path := range paths {
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return err
		}
		*path = absolute
	}
	return nil
}

func cleanEnv(extra ...string) []string {
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "MICROFAT_") || strings.HasPrefix(key, "GO") || key == "CGO_ENABLED" || key == "SOURCE_DATE_EPOCH" {
			continue
		}
		env = append(env, item)
	}
	fixed := []string{
		"CGO_ENABLED=0", "GOWORK=off", "GOTOOLCHAIN=local", "GOSUMDB=sum.golang.org", "GOENV=off",
		"GOOS=linux", "GOARCH=" + runtime.GOARCH,
		"GOPROXY=https://proxy.golang.org", "SOURCE_DATE_EPOCH=0",
	}
	return append(env, append(fixed, extra...)...)
}

func verifyToolchain(output string) error {
	version, versionErr := command("", cleanEnv(), "go", "version")
	environment, envErr := command("", cleanEnv(), "go", "env", "-json",
		"GOVERSION", "GOOS", "GOARCH", "GOROOT", "GOENV", "GOTOOLCHAIN", "GOAMD64", "GOARM64", "CGO_ENABLED", "GOPROXY", "GOSUMDB")
	machine, machineErr := command("", cleanEnv(), "uname", "-m")
	if err := saveJSON(filepath.Join(output, "toolchain.json"), map[string]any{
		"go_version": string(version), "go_env_json": string(environment), "uname_machine": string(machine),
		"go_version_error": errorText(versionErr), "go_env_error": errorText(envErr), "uname_error": errorText(machineErr),
	}); err != nil {
		return err
	}
	if err := errors.Join(versionErr, envErr, machineErr); err != nil {
		return err
	}
	var settings map[string]string
	if err := json.Unmarshal(environment, &settings); err != nil {
		return err
	}
	return checkToolchain(string(version), settings, strings.TrimSpace(string(machine)))
}

func checkToolchain(version string, settings map[string]string, machine string) error {
	wantMachine := "x86_64"
	if runtime.GOARCH == archARM64 {
		wantMachine = "aarch64"
	}
	wantVersion := "go version " + goVersion + " linux/" + runtime.GOARCH
	if strings.TrimSpace(version) != wantVersion || settings["GOVERSION"] != goVersion || settings["GOOS"] != "linux" ||
		settings["GOARCH"] != runtime.GOARCH || settings["GOENV"] != "off" || settings["GOTOOLCHAIN"] != "local" ||
		machine != wantMachine {
		return fmt.Errorf("external compiler or machine differs from frozen native Go 1.27.3 requirement: %s, %v, %s",
			version, settings, machine)
	}
	return nil
}

func command(dir string, env []string, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), preparationTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- commands and arguments come from this frozen harness.
	cmd.Dir, cmd.Env = dir, env
	data, err := cmd.CombinedOutput()
	if err != nil {
		return data, fmt.Errorf("%s %v: %w: %s", name, args, err, data)
	}
	return data, nil
}

func freezeSources(base, candidate, output string) error {
	metadata := map[string]any{armBaseline: baselineSHA, "corpus": corpus, "pairs": pairs, "bootstrap_resamples": resamples, "seed": seed}
	for arm, dir := range map[string]string{armBaseline: base, armCandidate: candidate} {
		sha, err := command(dir, cleanEnv(), "git", "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		if arm == armBaseline && strings.TrimSpace(string(sha)) != baselineSHA {
			return errors.New("baseline does not match frozen SHA")
		}
		status, err := command(dir, cleanEnv(), "git", "status", "--porcelain")
		if err != nil {
			return err
		}
		if len(status) != 0 {
			return fmt.Errorf("%s source is dirty: %s", arm, status)
		}
		metadata[arm+"_commit"] = strings.TrimSpace(string(sha))
	}
	var files []identity
	for _, rel := range []string{
		protocolPath, "tests/e2e/testdata/fd_handoff/main.go", "tests/e2e/testdata/fd_handoff/main_test.go",
		".github/workflows/ci.yml", "Taskfile.yml", ".config/tool-versions.env", "go.mod", "go.sum",
	} {
		path := filepath.Join(candidate, rel)
		id, err := identify(path)
		if err != nil {
			return err
		}
		files = append(files, id)
	}
	data, err := os.ReadFile(filepath.Join(candidate, protocolPath)) // #nosec G304 -- explicit local candidate root.
	if err != nil {
		return err
	}
	// #nosec G703 -- output is an explicitly selected, newly created private experiment directory.
	if err := os.WriteFile(filepath.Join(output, "protocol.md"), data, privateFileMode); err != nil {
		return err
	}
	metadata["frozen_files"] = files
	return saveJSON(filepath.Join(output, "freeze.json"), metadata)
}

func host(output, stage string) error {
	data := map[string]any{
		"time_utc": time.Now().UTC().Format(time.RFC3339Nano), "go": runtime.Version(),
		"arch": runtime.GOARCH, "cpus": runtime.NumCPU(),
	}
	for _, path := range []string{
		"/proc/cpuinfo", "/proc/meminfo", "/proc/self/cgroup", "/proc/self/status", "/sys/fs/cgroup/cpu.max",
		"/sys/fs/cgroup/cpu.stat", "/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory.events",
		"/proc/pressure/cpu", "/proc/pressure/memory",
	} {
		value, err := os.ReadFile(path) // #nosec G304 -- only the fixed host-observation paths above.
		if err != nil {
			data[path] = map[string]string{"unavailable": err.Error()}
		} else {
			data[path] = string(value)
		}
	}
	uname, err := command("", cleanEnv(), "uname", "-a")
	if err != nil {
		return err
	}
	data["uname"] = string(uname)
	return saveJSON(filepath.Join(output, "host-"+stage+".json"), data)
}

func prepare(base, candidate, output string) ([]prepared, error) {
	baselineStub, candidateStub := filepath.Join(output, "bin", "baseline-stub"), filepath.Join(output, "bin", "candidate-stub")
	packer := filepath.Join(output, "bin", "baseline-packer")
	for _, item := range []struct{ dir, target, pkg string }{
		{base, baselineStub, "./cmd/microfat-stub"}, {candidate, candidateStub, "./cmd/microfat-stub"},
		{base, packer, "./cmd/microfat"},
	} {
		args := []string{"build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w -buildid=", "-o", item.target, item.pkg}
		if _, err := command(item.dir, cleanEnv("GOAMD64=v1", "GOARM64=v8.0"), "go", args...); err != nil {
			return nil, err
		}
	}
	var preparedCorpus []prepared
	var identities []identity
	for _, path := range []string{baselineStub, candidateStub, packer} {
		id, err := identify(path)
		if err != nil {
			return nil, err
		}
		identities = append(identities, id)
	}
	stubSizeDelta := identities[1].Bytes - identities[0].Bytes
	if err := saveJSON(filepath.Join(output, "stub-identities.json"), map[string]any{
		armBaseline: identities[0], armCandidate: identities[1], "stub_size_delta_bytes": stubSizeDelta,
	}); err != nil {
		return nil, err
	}
	levels := []string{"v1", "v2"}
	if runtime.GOARCH == archARM64 {
		levels = []string{"v8.0", "v8.1"}
	}
	for _, fam := range corpus {
		moduleData, downloadErr := command(candidate, cleanEnv(), "go", "mod", "download", "-json", fam.Module+"@"+fam.Version)
		writeErr := os.WriteFile(filepath.Join(output, "corpus", fam.Name+"-module.json"), moduleData, privateFileMode)
		if err := errors.Join(downloadErr, writeErr); err != nil {
			return nil, err
		}
		var module struct{ Sum string }
		if err := json.Unmarshal(moduleData, &module); err != nil {
			return nil, err
		}
		if module.Sum != fam.Sum {
			return nil, fmt.Errorf("%s module sum differs from frozen protocol", fam.Name)
		}
		item := prepared{Family: fam, Bundles: map[string]string{}}
		var variants []string
		for n, level := range levels {
			binDir := filepath.Join(output, "corpus", fam.Name+"-"+level)
			if err := os.Mkdir(binDir, privateDirMode); err != nil {
				return nil, err
			}
			levelEnv := "GOAMD64=" + level
			if runtime.GOARCH == archARM64 {
				levelEnv = "GOARM64=" + level
			}
			args := []string{"install", "-trimpath", "-buildvcs=false", "-ldflags=-s -w -buildid=", fam.Package + "@" + fam.Version}
			if _, err := command(candidate, cleanEnv("GOBIN="+binDir, levelEnv), "go", args...); err != nil {
				return nil, err
			}
			native := filepath.Join(binDir, fam.Name)
			id, err := identify(native)
			if err != nil {
				return nil, err
			}
			identities = append(identities, id)
			variants = append(variants, "-v", level+"="+native)
			if n == 0 {
				item.Native, item.SelectedSHA256 = native, id.SHA256
			}
		}
		stdout, stderr, err := directOutput(item.Native, fam.Args)
		if err != nil {
			return nil, err
		}
		item.Stdout, item.Stderr = stdout, stderr
		for _, arm := range []string{armBaseline, armCandidate} {
			bundle := filepath.Join(output, "bundles", fam.Name+"-"+arm)
			stub := baselineStub
			if arm == armCandidate {
				stub = candidateStub
			}
			args := []string{
				"pack", "--name", fam.Name, "--arch", runtime.GOARCH, "--stub", stub,
				"--format-version", "2", "--compression", "zstd:better", "-o", bundle,
			}
			if _, err := command(candidate, cleanEnv(), packer, append(args, variants...)...); err != nil {
				return nil, err
			}
			id, err := identify(bundle)
			if err != nil {
				return nil, err
			}
			identities = append(identities, id)
			item.Bundles[arm] = bundle
		}
		match, err := matchBundles(item.Bundles[armBaseline], item.Bundles[armCandidate])
		if err != nil {
			return nil, err
		}
		match["stub_size_delta_bytes"] = stubSizeDelta
		if err := saveJSON(filepath.Join(output, "bundles", fam.Name+"-matching.json"), match); err != nil {
			return nil, err
		}
		preparedCorpus = append(preparedCorpus, item)
	}
	return preparedCorpus, saveJSON(filepath.Join(output, "identities.json"), identities)
}

func directOutput(path string, args []string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...) // #nosec G204 -- pinned, authenticated corpus binary and frozen version arguments.
	cmd.Env = cleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func matchBundles(base, candidate string) (map[string]any, error) {
	indexes := make([]*format.Index, 0, armCount)
	var payloads [][]identity
	for _, path := range []string{base, candidate} {
		f, err := os.Open(path) // #nosec G304 -- compiled experiment bundle in private output directory.
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil {
			return nil, errors.Join(err, f.Close())
		}
		index, err := format.ReadTrailerAndIndex(f, info.Size())
		if err != nil {
			return nil, errors.Join(err, f.Close())
		}
		if index.DictionarySize != 0 {
			return nil, errors.Join(errors.New("unexpected dictionary"), f.Close())
		}
		var blobs []identity
		for n, variant := range index.Variants {
			h := sha256.New()
			_, err := io.Copy(h, io.NewSectionReader(f, variant.Offset, variant.CompressedSize))
			if err != nil {
				return nil, errors.Join(err, f.Close())
			}
			blobs = append(blobs, identity{Path: variant.Level, SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: variant.CompressedSize})
			index.Variants[n].Offset = 0
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		indexes, payloads = append(indexes, index), append(payloads, blobs)
	}
	if !reflect.DeepEqual(indexes[0], indexes[1]) || !reflect.DeepEqual(payloads[0], payloads[1]) {
		return nil, errors.New("arms have different normalized indexes or compressed payloads")
	}
	baseID, err := identify(base)
	if err != nil {
		return nil, err
	}
	candidateID, err := identify(candidate)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"normalized_index": indexes[0], "compressed_payloads": payloads[0], armBaseline: baseID, armCandidate: candidateID,
		"complete_size_delta_bytes": candidateID.Bytes - baseID.Bytes,
	}, nil
}

func schedule() []scheduled {
	var result []scheduled
	for round := range pairs {
		var cells []scheduled
		for _, fam := range corpus {
			for _, path := range []string{pathCold, pathWarm, pathMemfd} {
				cells = append(cells, scheduled{Round: round, Family: fam.Name, Path: path})
			}
		}
		if round%2 != 0 {
			for i, j := 0, len(cells)-1; i < j; i, j = i+1, j-1 {
				cells[i], cells[j] = cells[j], cells[i]
			}
		}
		arms := []string{armBaseline, armCandidate}
		if round%2 != 0 {
			arms[0], arms[1] = arms[1], arms[0]
		}
		for _, cell := range cells {
			for _, arm := range arms {
				cell.Sequence, cell.Arm = len(result), arm
				result = append(result, cell)
			}
		}
	}
	return result
}

func measure(output string, items []prepared) ([]observation, error) {
	// #nosec G304 -- new raw-evidence file in the explicitly selected private experiment directory.
	f, err := os.OpenFile(filepath.Join(output, "observations.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, privateFileMode)
	if err != nil {
		return nil, err
	}
	encoder := json.NewEncoder(f)
	var observations []observation
	for _, step := range schedule() {
		var item prepared
		for _, candidate := range items {
			if candidate.Family.Name == step.Family {
				item = candidate
			}
		}
		obs := attempt(output, step, item)
		observations = append(observations, obs)
		if err := encoder.Encode(obs); err != nil {
			return observations, errors.Join(err, f.Close())
		}
		if err := f.Sync(); err != nil {
			return observations, errors.Join(err, f.Close())
		}
		if step.Sequence%progressInterval == 0 {
			fmt.Printf("recorded %d/%d complete planned attempts\n", step.Sequence+1, len(schedule()))
		}
	}
	return observations, f.Close()
}

func attempt(output string, step scheduled, item prepared) observation {
	obs := observation{scheduled: step, Started: time.Now().UTC().Format(time.RFC3339Nano)}
	cacheDir := filepath.Join(output, "attempts", fmt.Sprintf("%04d", step.Sequence))
	if err := os.Mkdir(cacheDir, privateDirMode); err != nil {
		obs.Error = err.Error()
		return obs
	}
	mode := "cache"
	if step.Path == pathMemfd {
		mode = pathMemfd
	}
	if step.Path == pathWarm {
		data, err := os.ReadFile(item.Native)
		if err == nil {
			// #nosec G306 G703 -- executable authenticated payload under its frozen checksum, in a new private cache.
			err = os.WriteFile(filepath.Join(cacheDir, item.SelectedSHA256), data, privateDirMode)
		}
		if err != nil {
			obs.Error = err.Error()
			obs.CleanupError = errorText(os.RemoveAll(cacheDir))
			return obs
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
	// #nosec G204 -- locally compiled bundle from frozen sources, with fixed version-only arguments.
	cmd := exec.CommandContext(ctx, item.Bundles[step.Arm], item.Family.Args...)
	level := "v1"
	if runtime.GOARCH == archARM64 {
		level = "v8.0"
	}
	cmd.Env = cleanEnv("MICROFAT_EXEC_MODE="+mode, "MICROFAT_CACHE_DIR="+cacheDir, "MICROFAT_FORCE_LEVEL="+level)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	obs.WallNS = time.Since(start).Nanoseconds()
	obs.Error = errorText(err)
	if ctx.Err() != nil {
		obs.Error = errors.Join(err, ctx.Err()).Error()
	}
	cancel()
	obs.Stdout, obs.Stderr = stdout.String(), stderr.String()
	if cmd.ProcessState != nil {
		obs.CPUNS = (cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()).Nanoseconds()
		if usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			obs.MaxRSSKiB = usage.Maxrss
		}
	}
	if obs.Stdout != item.Stdout || obs.Stderr != item.Stderr {
		obs.Error = strings.TrimSpace(obs.Error + " output differs from direct native invocation")
	}
	obs.CacheError = errorText(checkCache(cacheDir, item.SelectedSHA256, step.Path == pathMemfd))
	obs.CleanupError = errorText(os.RemoveAll(cacheDir))
	if obs.MaxRSSKiB <= 0 || obs.MaxRSSKiB > maxRSSKiB || obs.CPUNS <= 0 || obs.WallNS <= 0 {
		obs.Error = strings.TrimSpace(obs.Error + " missing resource observation or RSS exceeds 512 MiB")
	}
	obs.Success = obs.Error == "" && obs.CacheError == "" && obs.CleanupError == ""
	return obs
}

func checkCache(dir, wantHash string, empty bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if empty {
		if len(entries) != 0 {
			return errors.New("explicit memfd populated cache")
		}
		return nil
	}
	if len(entries) != 1 || entries[0].Name() != wantHash {
		return fmt.Errorf("cache has %d entries, expected exactly selected SHA-256", len(entries))
	}
	path := filepath.Join(dir, wantHash)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != privateDirMode {
		return errors.New("cache is not regular mode 0700")
	}
	id, err := identify(path)
	if err != nil {
		return err
	}
	if id.SHA256 != wantHash {
		return errors.New("cache bytes differ from selected native payload")
	}
	return nil
}

func evaluate(observations []observation) ([]decision, bool) {
	plan := schedule()
	pass := len(observations) == len(plan)
	for i, obs := range observations {
		if !validObservation(obs) || i >= len(plan) || obs.scheduled != plan[i] {
			pass = false
		}
	}
	var decisions []decision
	useful := 0
	for _, fam := range corpus {
		for _, path := range []string{pathCold, pathWarm, pathMemfd} {
			arms := map[int]map[string]observation{}
			for _, obs := range observations {
				if obs.Family == fam.Name && obs.Path == path {
					if arms[obs.Round] == nil {
						arms[obs.Round] = map[string]observation{}
					}
					arms[obs.Round][obs.Arm] = obs
				}
			}
			var wall, cpu, rss, baseRSS []float64
			for round := range pairs {
				a, b := arms[round][armBaseline], arms[round][armCandidate]
				if validObservation(a) && validObservation(b) {
					wall = append(wall, float64(b.WallNS)/float64(a.WallNS))
					cpu = append(cpu, float64(b.CPUNS)/float64(a.CPUNS))
					rss = append(rss, float64(b.MaxRSSKiB-a.MaxRSSKiB))
					baseRSS = append(baseRSS, float64(a.MaxRSSKiB))
				}
			}
			d := decision{Family: fam.Name, Path: path, PlannedPairs: pairs, CompletePairs: len(wall)}
			if len(wall) > 0 {
				d.WallRatio, d.CPURatio, d.RSSIncreaseKiB = bootstrap(wall), bootstrap(cpu), bootstrap(rss)
				d.BaselineRSSMedianKiB = median(baseRSS)
				d.WallPass = d.WallRatio.Lower95 >= controlLower && d.WallRatio.Upper95 <= controlUpper
				if path == pathCold {
					d.UsefulCold = d.WallRatio.Median <= controlLower && d.WallRatio.Upper95 < 1
					d.WallPass = d.WallRatio.Upper95 <= controlUpper
					if d.UsefulCold {
						useful++
					}
				}
				rssAllowance := rssRelativeAllowance*d.BaselineRSSMedianKiB + rssAbsoluteAllowanceKiB
				d.ResourcePass = d.CPURatio.Upper95 <= cpuUpper && d.RSSIncreaseKiB.Upper95 <= rssAllowance
			}
			if d.CompletePairs != pairs || !d.WallPass || !d.ResourcePass {
				pass = false
			}
			decisions = append(decisions, d)
		}
	}
	return decisions, pass && useful >= usefulFamilyMinimum
}

func validObservation(obs observation) bool {
	return obs.Success && obs.Error == "" && obs.CacheError == "" && obs.CleanupError == "" &&
		obs.WallNS > 0 && obs.CPUNS > 0 && obs.MaxRSSKiB > 0 && obs.MaxRSSKiB <= maxRSSKiB
}

func bootstrap(values []float64) interval {
	if len(values) == 0 {
		return interval{}
	}
	rng := rand.New(rand.NewPCG(seed, seed)) // #nosec G404 -- reproducible statistical resampling; no security purpose.
	samples := make([]float64, resamples)
	sample := make([]float64, len(values))
	for n := range samples {
		for i := range sample {
			sample[i] = values[rng.IntN(len(values))]
		}
		samples[n] = median(sample)
	}
	sort.Float64s(samples)
	tail := resamples / bootstrapTailDivisor
	return interval{Median: median(values), Lower95: samples[tail], Upper95: samples[resamples-tail-1]}
}

func median(values []float64) float64 {
	v := append([]float64(nil), values...)
	sort.Float64s(v)
	if len(v)%2 == 1 {
		return v[len(v)/2]
	}
	return (v[len(v)/armCount-1] + v[len(v)/armCount]) / armCount
}

func identify(path string) (identity, error) {
	f, err := os.Open(path) // #nosec G304 -- experiment-owned artifact or fixed source file selected by this harness.
	if err != nil {
		return identity{}, err
	}
	h := sha256.New()
	size, err := io.Copy(h, f)
	err = errors.Join(err, f.Close())
	if err != nil {
		return identity{}, err
	}
	return identity{Path: path, SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: size}, nil
}

func saveJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), privateFileMode)
}

func errorText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
