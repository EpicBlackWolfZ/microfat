//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
)

type identity struct {
	Path, SHA256 string
	Bytes        int64
}
type variant struct {
	Family, Profile, Level, Path, SHA256 string
	Bytes                                int64
}
type corpus struct {
	Schema    int              `json:"schema_version"`
	Revision  string           `json:"source_revision"`
	Go        string           `json:"go_version"`
	Arch      string           `json:"architecture"`
	Sources   []map[string]any `json:"sources"`
	Variants  []variant        `json:"variants"`
	Synthetic []struct {
		Family, Kind, Generator, Marker string
		GeneratorSHA256                 string   `json:"generator_sha256"`
		ReadinessArgs                   []string `json:"readiness_args"`
		GoRuntime                       bool     `json:"go_runtime"`
		BoundaryBytes                   int64    `json:"boundary_bytes"`
		Profiles                        map[string]int64
	} `json:"synthetic_sources"`
}
type prepared struct {
	Study    studyConfig
	Corpus   corpus
	Stubs    map[string]string
	Variants map[group]map[string]variant
	Controls map[group]observation
}

// No inherited microfat, Go, cache, profiling or reproducibility controls can alter a cell.
func cleanEnv(extra ...string) []string {
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "MICROFAT_") || strings.HasPrefix(key, "GO") || key == "CGO_ENABLED" ||
			key == "SOURCE_DATE_EPOCH" || key == "XDG_CACHE_HOME" || key == "LD_PRELOAD" || key == "LD_LIBRARY_PATH" {
			continue
		}
		env = append(env, item)
	}
	return append(append(env, "GOENV=off", "GOTOOLCHAIN=local", "GOWORK=off", "CGO_ENABLED=0", "GOOS=linux",
		"GOARCH="+runtime.GOARCH, "GOMAXPROCS=4", "SOURCE_DATE_EPOCH=0"), extra...)
}

func command(study studyConfig, dir, name string, args ...string) ([]byte, error) {
	timeout := metadataTimeout
	if name == "go" && len(args) > 0 && args[0] == "build" {
		timeout = preparationTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := ownedCommand(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, study.env()
	data, err := cmd.CombinedOutput()
	if err != nil {
		return data, fmt.Errorf("%s %v: %w: %s", name, args, err, data)
	}
	return data, nil
}

func repositoryRoot(study studyConfig) (string, error) {
	data, err := command(study, "", "git", "rev-parse", "--show-toplevel")
	return strings.TrimSpace(string(data)), err
}

func identify(path string) (identity, error) {
	file, err := os.Open(path) // #nosec G304 -- explicit local corpus, executable or frozen evidence identity.
	if err != nil {
		return identity{}, err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(hash, file)
	err = errors.Join(copyErr, file.Close())
	return identity{path, hex.EncodeToString(hash.Sum(nil)), n}, err
}

func sourceState(root, output, stage string, study studyConfig) error {
	head, headErr := command(study, root, "git", "rev-parse", "HEAD")
	status, statusErr := command(study, root, "git", "status", "--porcelain", "--untracked-files=all")
	state := map[string]string{"head": strings.TrimSpace(string(head)), "status": string(status)}
	err := errors.Join(headErr, statusErr)
	if len(status) != 0 {
		err = errors.Join(err, errors.New("source checkout is dirty"))
	}
	if stage != "before" {
		var frozen map[string]string
		if readErr := readJSON(filepath.Join(output, "source-before.json"), &frozen); readErr != nil {
			err = errors.Join(err, readErr)
		} else if state["head"] != frozen["head"] {
			err = errors.Join(err, errors.New("source HEAD changed"))
		}
	}
	state["error"] = errorText(err)
	return errors.Join(err, saveJSON(filepath.Join(output, "source-"+stage+".json"), state))
}

func freeze(root, corpusDir, output string, study studyConfig) error {
	var c corpus
	if err := readJSON(filepath.Join(corpusDir, "manifest.json"), &c); err != nil {
		return err
	}
	var state map[string]string
	if err := readJSON(filepath.Join(output, "source-before.json"), &state); err != nil {
		return err
	}
	if err := validateManifest(c, state["head"]); err != nil {
		return err
	}
	files := []string{filepath.Join(root, study.Protocol), filepath.Join(root, "go.mod"), filepath.Join(root, "go.sum"),
		filepath.Join(output, "study.json"),
		filepath.Join(root, "scripts/codec-policy-corpus.sh"), filepath.Join(root, "experiments/codecpolicy/sources.lock.json"),
		filepath.Join(root, "scripts/codec-policy-collect.sh"),
		filepath.Join(root, "tests/e2e/testdata/codecboundary/main.go"),
		filepath.Join(root, ".config/tool-versions.env"), filepath.Join(corpusDir, "manifest.json"),
		filepath.Join(corpusDir, "sources.lock.json"), filepath.Join(corpusDir, "stub-full"),
		filepath.Join(corpusDir, "stub-minimal"), filepath.Join(corpusDir, "microfat")}
	harnessFiles, err := filepath.Glob(filepath.Join(root, "tests/e2e/testdata/codecpolicy/*.go"))
	if err != nil {
		return err
	}
	files = append(files, harnessFiles...)
	files = append(files, study.frozenFiles()...)
	goPath, err := exec.LookPath("go")
	if err != nil {
		return err
	}
	goPath, err = filepath.EvalSymlinks(goPath)
	if err != nil {
		return err
	}
	harness, err := os.Executable()
	if err != nil {
		return err
	}
	harnessSettings, err := collectorSettings(harness)
	if err != nil {
		return err
	}
	files = append(files, goPath, harness)
	for _, v := range c.Variants {
		files = append(files, v.Path)
	}
	var identities []identity
	for _, path := range files {
		id, err := identify(path)
		if err != nil {
			return err
		}
		identities = append(identities, id)
	}
	version, versionErr := command(study, root, "go", "version")
	settings, settingsErr := command(study, root, "go", "env", "-json",
		"GOVERSION", "GOHOSTOS", "GOHOSTARCH", "GOROOT", "GOENV", "GOTOOLCHAIN")
	machine, machineErr := command(study, root, "uname", "-m")
	var goEnv map[string]string
	if err := errors.Join(versionErr, settingsErr, machineErr); err != nil {
		return err
	}
	if err := jsonDecode(settings, &goEnv); err != nil {
		return err
	}
	wantMachine := "x86_64"
	if runtime.GOARCH == archARM64 {
		wantMachine = "aarch64"
	}
	if strings.TrimSpace(string(version)) != "go version go1.27.3 linux/"+runtime.GOARCH ||
		goEnv["GOVERSION"] != goVersion || goEnv["GOHOSTOS"] != "linux" || goEnv["GOHOSTARCH"] != runtime.GOARCH ||
		strings.TrimSpace(string(machine)) != wantMachine {
		return errors.New("external toolchain or native machine differs")
	}
	for _, path := range []string{filepath.Join(goEnv["GOROOT"], "pkg/tool/linux_"+runtime.GOARCH, "compile"),
		filepath.Join(goEnv["GOROOT"], "pkg/tool/linux_"+runtime.GOARCH, "link")} {
		id, err := identify(path)
		if err != nil {
			return err
		}
		identities = append(identities, id)
	}
	data, err := os.ReadFile(filepath.Join(root, study.Protocol)) // #nosec G304 -- fixed protocol in the explicit source root.
	if err != nil {
		return err
	}
	// #nosec G703 -- copy into the new operator-selected private evidence directory.
	if err := os.WriteFile(filepath.Join(output, "protocol.md"), data, privateFile); err != nil {
		return err
	}
	return saveJSON(filepath.Join(output, "freeze.json"), map[string]any{"identities": identities, "go_version": string(version),
		"collector_build_settings": harnessSettings,
		"study":                    study,
		"go_env":                   goEnv, "bootstrap_resamples": bootstrapResamples, "bootstrap_seed": bootstrapSeed, "gomaxprocs": fixedProcs,
		"resource_scope": "observed wait4 child-process MaxRSS ceilings; no imposed memory limit"})
}

func prepare(dir, output, root string, study studyConfig) (prepared, error) {
	items, err := loadCorpus(dir, root)
	if err != nil {
		return items, err
	}
	items.Study = study
	wanted := groups()
	// Rebuild all current tools outside timing; reproducible byte equality binds manifest provenance to source.
	for _, stub := range []string{fullStub, minimalStub, "microfat"} {
		name, pkg := "stub-"+stub, "./cmd/microfat-stub"
		if stub == "microfat" {
			name, pkg = "microfat", "./cmd/microfat"
		}
		target := filepath.Join(output, "rebuilt-"+name)
		args := []string{"build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags=-s -w -buildid="}
		if stub == minimalStub {
			args = append(args, "-tags=minimal")
		}
		args = append(args, "-o", target, pkg)
		if _, err := command(study, root, "go", args...); err != nil {
			return items, err
		}
		original, err := identify(filepath.Join(dir, name))
		if err != nil {
			return items, err
		}
		rebuilt, err := identify(target)
		if err != nil {
			return items, err
		}
		if original.SHA256 != rebuilt.SHA256 {
			return items, fmt.Errorf("%s differs from reproducible current-source build", name)
		}
		items.Stubs[stub] = original.Path
	}
	if err := os.Mkdir(filepath.Join(output, "attempts"), privateDir); err != nil {
		return items, err
	}
	if err := prepareVersionDB(output); err != nil {
		return items, err
	}
	for _, g := range wanted {
		v := items.Variants[g][baselineLevel()]
		obs := execute(v.Path, versionArgs(g.Family, output), study.env("MICROFAT_AUTOTUNE=0"), launchTimeout)
		if !obs.Success {
			return items, fmt.Errorf("native readiness %v: %s", g, obs.Error)
		}
		if g.Family == boundaryFamily && obs.Stdout != "microfat-codec-boundary\n" {
			return items, errors.New("boundary marker differs")
		}
		if obs.Stdout == "" && obs.Stderr == "" {
			return items, errors.New("empty native readiness control")
		}
		items.Controls[g] = obs
	}
	return items, saveJSON(filepath.Join(output, "native-controls.json"), controlRecords(items))
}

func controlRecords(items prepared) []observation {
	var result []observation
	for _, g := range groups() {
		o := items.Controls[g]
		o.group = g
		result = append(result, o)
	}
	return result
}
func slicesContains[T comparable](values []T, target T) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func readinessArgs(family string) []string {
	switch family {
	case "fortio":
		return []string{"version"}
	case "task":
		return []string{"--version"}
	case "actionlint", "govulncheck":
		return []string{"-version"}
	case boundaryFamily:
		return nil
	}
	panic("unlisted readiness family")
}

func versionArgs(family, output string) []string {
	if family == "govulncheck" {
		return []string{"-db", "file://" + filepath.Join(output, "version-db"), "-version"}
	}
	return readinessArgs(family)
}

func prepareVersionDB(output string) error {
	dir := filepath.Join(output, "version-db", "index")
	if err := os.MkdirAll(dir, privateDir); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "modules.json"), []byte("[]\n"), privateFile); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "db.json"), []byte("{\"modified\":\"1970-01-01T00:00:00Z\"}\n"), privateFile); err != nil {
		return err
	}
	var ids []identity
	for _, name := range []string{"modules.json", "db.json"} {
		id, err := identify(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	return saveJSON(filepath.Join(output, "version-commands.json"), map[string]any{"database": ids,
		"fortio": versionArgs("fortio", output), "task": versionArgs("task", output),
		"actionlint": versionArgs("actionlint", output), "govulncheck": versionArgs("govulncheck", output), boundaryFamily: []string{}})
}

func recordHost(output, stage string, study studyConfig) error {
	data := map[string]any{"utc": time.Now().UTC().Format(time.RFC3339Nano), "go": runtime.Version(),
		"arch": runtime.GOARCH, "cpus": runtime.NumCPU()}
	for _, path := range []string{"/proc/cpuinfo", "/proc/meminfo", "/proc/self/cgroup", "/proc/self/status",
		"/proc/pressure/cpu", "/proc/pressure/memory"} {
		value, err := os.ReadFile(path) // #nosec G304 -- fixed read-only /proc host observation allowlist above.
		data[path] = map[string]string{"value": string(value), "error": errorText(err)}
	}
	membership, err := os.ReadFile("/proc/self/cgroup")
	data["cgroup_membership_error"] = errorText(err)
	var ancestors []map[string]any
	for _, line := range strings.Split(string(membership), "\n") {
		rel, ok := strings.CutPrefix(line, "0::")
		if !ok || !strings.HasPrefix(rel, "/") || strings.Contains(rel, "..") {
			continue
		}
		for dir := filepath.Join("/sys/fs/cgroup", rel); ; dir = filepath.Dir(dir) {
			entry := map[string]any{"path": dir}
			for _, name := range []string{"cpu.max", "cpu.stat", "memory.max", "memory.high", "memory.current", "memory.events"} {
				// #nosec G304 G703 -- fixed cgroup filenames below /sys/fs/cgroup, excluding parent traversal above.
				value, readErr := os.ReadFile(filepath.Join(dir, name))
				entry[name] = map[string]string{"value": string(value), "error": errorText(readErr)}
			}
			ancestors = append(ancestors, entry)
			if dir == "/sys/fs/cgroup" {
				break
			}
		}
	}
	data["unified_cgroup_ancestors"] = ancestors
	limits, limitsErr := cgroup.ReadLimits()
	data["launcher_resolved_limits"], data["launcher_limits_error"] = limits, errorText(limitsErr)
	uname, unameErr := command(study, "", "uname", "-a")
	data["uname"], data["uname_error"] = string(uname), errorText(unameErr)
	return saveJSON(filepath.Join(output, "host-"+stage+".json"), data)
}

func jsonDecode(data []byte, value any) error {
	return json.NewDecoder(bytes.NewReader(data)).Decode(value)
}

func recheckInputs(output, stage string) error {
	var frozen struct {
		Identities []identity
		Study      studyConfig
	}
	if err := readJSON(filepath.Join(output, "freeze.json"), &frozen); err != nil {
		return err
	}
	var commands struct{ Database []identity }
	if _, err := os.Stat(filepath.Join(output, "version-commands.json")); err == nil {
		if err := readJSON(filepath.Join(output, "version-commands.json"), &commands); err != nil {
			return err
		}
		frozen.Identities = append(frozen.Identities, commands.Database...)
	}
	var failures []string
	if err := frozen.Study.validateConfig(); err != nil {
		failures = append(failures, err.Error())
	}
	for _, expected := range frozen.Identities {
		actual, err := identify(expected.Path)
		if err != nil || actual != expected {
			failures = append(failures, expected.Path+": "+errorText(err)+" identity differs")
		}
	}
	result := map[string]any{"checked": len(frozen.Identities), "failures": failures}
	err := saveJSON(filepath.Join(output, "input-check-"+stage+".json"), result)
	if len(failures) != 0 {
		err = errors.Join(err, errors.New(strings.Join(failures, "\n")))
	}
	return err
}

func collectorSettings(harness string) (map[string]string, error) {
	harnessBuild, err := buildinfo.ReadFile(harness)
	if err != nil {
		return nil, err
	}
	harnessSettings := map[string]string{}
	for _, item := range harnessBuild.Settings {
		harnessSettings[item.Key] = item.Value
	}
	levelKey, level := "GOAMD64", "v1"
	if runtime.GOARCH == archARM64 {
		levelKey, level = "GOARM64", "v8.0"
	}
	if harnessBuild.GoVersion != goVersion || harnessSettings["CGO_ENABLED"] != "0" || harnessSettings["-trimpath"] != "true" ||
		harnessSettings["GOOS"] != "linux" || harnessSettings["GOARCH"] != runtime.GOARCH || harnessSettings[levelKey] != level {
		return nil, errors.New("collector build settings differ from frozen native baseline recipe")
	}
	for _, key := range []string{"-race", "-msan", "-asan", "-cover", "GOEXPERIMENT"} {
		if value := harnessSettings[key]; value != "" && value != "false" {
			return nil, fmt.Errorf("instrumented/experimental collector: %s=%s", key, value)
		}
	}
	return harnessSettings, nil
}

func loadCorpus(dir, root string) (prepared, error) {
	items := prepared{Stubs: map[string]string{}, Variants: map[group]map[string]variant{}, Controls: map[group]observation{}}
	if err := readJSON(filepath.Join(dir, "manifest.json"), &items.Corpus); err != nil {
		return items, err
	}
	if err := validateLocks(items.Corpus, dir, root); err != nil {
		return items, err
	}
	if err := validateSynthetic(items.Corpus, root); err != nil {
		return items, err
	}
	levels := []string{"v1", "v2", "v3"}
	if runtime.GOARCH == archARM64 {
		levels = []string{"v8.0", "v8.1", "v8.2"}
	}
	wanted := groups()
	for _, v := range items.Corpus.Variants {
		g := group{v.Family, v.Profile}
		if !slicesContains(wanted, g) || !slicesContains(levels, v.Level) {
			return items, errors.New("unexpected corpus group/level")
		}
		if items.Variants[g] == nil {
			items.Variants[g] = map[string]variant{}
		}
		if _, found := items.Variants[g][v.Level]; found {
			return items, errors.New("duplicate corpus variant")
		}
		id, err := identify(v.Path)
		if err != nil {
			return items, err
		}
		if id.SHA256 != v.SHA256 || id.Bytes != v.Bytes {
			return items, errors.New("corpus identity mismatch")
		}
		if g.Family == boundaryFamily && id.Bytes != boundarySizes()[g.Profile] {
			return items, errors.New("boundary size differs")
		}
		if g.Family != boundaryFamily {
			info, err := buildinfo.ReadFile(v.Path)
			if err != nil || info.GoVersion != goVersion {
				return items, errors.New("corpus payload Go build info differs")
			}
		}
		items.Variants[g][v.Level] = v
	}
	for _, g := range wanted {
		if len(items.Variants[g]) != len(levels) {
			return items, fmt.Errorf("incomplete corpus group %v", g)
		}
	}
	return items, nil
}

func validateLocks(c corpus, dir, root string) error {
	var lock struct {
		Sources []map[string]any `json:"sources"`
	}
	if err := readJSON(filepath.Join(dir, "sources.lock.json"), &lock); err != nil {
		return err
	}
	if !reflect.DeepEqual(lock.Sources, c.Sources) {
		return errors.New("corpus sources differ from copied lock")
	}
	var sourceLock struct {
		Sources []map[string]any `json:"sources"`
	}
	if err := readJSON(filepath.Join(root, "experiments/codecpolicy/sources.lock.json"), &sourceLock); err != nil {
		return err
	}
	if !reflect.DeepEqual(lock.Sources, sourceLock.Sources) {
		return errors.New("copied corpus lock differs from current frozen source lock")
	}
	return nil
}

func validateSynthetic(c corpus, root string) error {
	if len(c.Synthetic) != 1 {
		return errors.New("requires exactly one synthetic boundary descriptor")
	}
	synthetic := c.Synthetic[0]
	if synthetic.Family != boundaryFamily || synthetic.Kind != "synthetic-static-linux-elf" ||
		synthetic.Marker != "microfat-codec-boundary\n" || synthetic.GoRuntime || len(synthetic.ReadinessArgs) != 0 ||
		synthetic.BoundaryBytes != boundaryBytes || synthetic.Generator != "tests/e2e/testdata/codecboundary/main.go" ||
		!reflect.DeepEqual(synthetic.Profiles, map[string]int64{"below": boundaryBytes - 1, "at": boundaryBytes, "above": boundaryBytes + 1}) {
		return errors.New("synthetic boundary descriptor differs from prospective definition")
	}
	generator, err := identify(filepath.Join(root, synthetic.Generator))
	if err != nil {
		return err
	}
	if generator.SHA256 != synthetic.GeneratorSHA256 {
		return errors.New("boundary generator identity differs")
	}
	return nil
}

func boundarySizes() map[string]int64 {
	return map[string]int64{"below": boundaryBytes - 1, "at": boundaryBytes, "above": boundaryBytes + 1}
}

func validateManifest(c corpus, head string) error {
	if c.Schema != 2 || c.Go != goVersion || c.Arch != runtime.GOARCH || c.Revision != head {
		return errors.New("corpus schema/toolchain/architecture/revision differs from frozen source")
	}
	return nil
}
