//go:build linux

package main

import (
	"context"
	"crypto/sha256"
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
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

func cleanEnv(extra ...string) []string {
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "MICROFAT_") || strings.HasPrefix(key, "GO") ||
			key == "CGO_ENABLED" || key == "SOURCE_DATE_EPOCH" || key == "XDG_CACHE_HOME" {
			continue
		}
		env = append(env, item)
	}
	return append(append(env, "GOENV=off", "GOTOOLCHAIN=local", "GOWORK=off", "CGO_ENABLED=0",
		"GOOS=linux", "GOARCH="+runtime.GOARCH, "SOURCE_DATE_EPOCH=0"), extra...)
}

func command(dir string, env []string, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), preparationTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- frozen build/preparation commands.
	cmd.Dir, cmd.Env = dir, env
	data, err := cmd.CombinedOutput()
	if err != nil {
		return data, fmt.Errorf("%s %v: %w: %s", name, args, err, data)
	}
	return data, nil
}

func freezeSources(base, candidate, output string) error {
	metadata := map[string]any{"fixed_baseline": baselineSHA, "pairs_per_cell": pairs, "bootstrap_seed": seed,
		"bootstrap_resamples": resamples}
	for _, arm := range []struct{ name, dir string }{{baseline, base}, {candidateArm, candidate}} {
		sha, err := command(arm.dir, cleanEnv(), "git", "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		if arm.name == baseline && strings.TrimSpace(string(sha)) != baselineSHA {
			return errors.New("baseline differs from frozen SHA")
		}
		status, err := command(arm.dir, cleanEnv(), "git", "status", "--porcelain")
		if err != nil {
			return err
		}
		if len(status) != 0 {
			return fmt.Errorf("%s source is dirty: %s", arm.name, status)
		}
		metadata[arm.name+"_commit"] = strings.TrimSpace(string(sha))
	}
	var files []identity
	for _, rel := range []string{protocolPath, "go.mod", "go.sum", ".config/tool-versions.env",
		"tests/e2e/testdata/telemetry/main.go", "tests/e2e/testdata/telemetry/prepare.go",
		"tests/e2e/testdata/telemetry/measure.go", "tests/e2e/testdata/telemetry/statistics.go",
		"tests/e2e/testdata/telemetry/main_test.go", "tests/e2e/testdata/telemetry/fixture/main.go"} {
		id, err := identify(filepath.Join(candidate, rel))
		if err != nil {
			return err
		}
		files = append(files, id)
	}
	metadata["frozen_files"] = files
	protocol, err := os.ReadFile(filepath.Join(candidate, protocolPath)) // #nosec G304 -- explicit local candidate checkout.
	if err != nil {
		return err
	}
	// #nosec G703 -- output is the newly created private evidence directory.
	if err := os.WriteFile(filepath.Join(output, "protocol.md"), protocol, privateFile); err != nil {
		return err
	}
	return saveJSON(filepath.Join(output, "freeze.json"), metadata)
}

func recheckSources(base, candidate, output, stage string) (retErr error) {
	data, err := os.ReadFile(filepath.Join(output, "freeze.json")) // #nosec G304 -- experiment-owned frozen identities.
	if err != nil {
		return err
	}
	var frozen map[string]any
	if err := json.Unmarshal(data, &frozen); err != nil {
		return err
	}
	observed := map[string]any{}
	defer func() {
		observed["error"] = errorText(retErr)
		retErr = errors.Join(retErr, saveJSON(filepath.Join(output, "source-check-"+stage+".json"), observed))
	}()
	for _, arm := range []struct{ name, dir string }{{baseline, base}, {candidateArm, candidate}} {
		sha, shaErr := command(arm.dir, cleanEnv(), "git", "rev-parse", "HEAD")
		status, statusErr := command(arm.dir, cleanEnv(), "git", "status", "--porcelain")
		head := strings.TrimSpace(string(sha))
		observed[arm.name] = map[string]string{"head": head, "status": string(status),
			"head_error": errorText(shaErr), "status_error": errorText(statusErr)}
		retErr = errors.Join(retErr, shaErr, statusErr)
		if head != frozen[arm.name+"_commit"] || len(status) != 0 {
			retErr = errors.Join(retErr, fmt.Errorf("%s source changed or is dirty at %s", arm.name, stage))
		}
	}
	return retErr
}

func verifyToolchain(output string) (retErr error) {
	version, versionErr := command("", cleanEnv(), "go", "version")
	env, envErr := command("", cleanEnv(), "go", "env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOROOT", "GOENV",
		"GOTOOLCHAIN", "GOAMD64", "GOARM64", "CGO_ENABLED")
	machine, machineErr := command("", cleanEnv(), "uname", "-m")
	metadata := map[string]any{
		"version": string(version), "environment": string(env), "machine": string(machine),
		"errors": []string{errorText(versionErr), errorText(envErr), errorText(machineErr)},
	}
	defer func() {
		metadata["validation_error"] = errorText(retErr)
		retErr = errors.Join(retErr, saveJSON(filepath.Join(output, "toolchain.json"), metadata))
	}()
	if err := errors.Join(versionErr, envErr, machineErr); err != nil {
		return err
	}
	var settings map[string]string
	if err := json.Unmarshal(env, &settings); err != nil {
		return err
	}
	wantMachine := "x86_64"
	if runtime.GOARCH == "arm64" {
		wantMachine = "aarch64"
	}
	_, hasGOENV := settings["GOENV"]
	if strings.TrimSpace(string(version)) != "go version "+goVersion+" linux/"+runtime.GOARCH ||
		settings["GOVERSION"] != goVersion || settings["GOOS"] != "linux" || settings["GOARCH"] != runtime.GOARCH ||
		!hasGOENV || settings["GOENV"] != "" || settings["GOTOOLCHAIN"] != "local" || strings.TrimSpace(string(machine)) != wantMachine {
		return errors.New("external toolchain/machine differs from native Go 1.27.3 requirement")
	}
	pathGo, err := exec.LookPath("go")
	if err != nil {
		return err
	}
	resolvedGo, err := filepath.EvalSymlinks(pathGo)
	if err != nil {
		return err
	}
	harness, err := os.Executable()
	if err != nil {
		return err
	}
	metadata["selected_path_go"] = pathGo
	identities := map[string]identity{}
	metadata["executable_identities"] = identities
	for label, path := range map[string]string{
		"resolved_path_go": resolvedGo, "goroot_go": filepath.Join(settings["GOROOT"], "bin", "go"),
		"compile": filepath.Join(settings["GOROOT"], "pkg", "tool", "linux_"+runtime.GOARCH, "compile"),
		"link":    filepath.Join(settings["GOROOT"], "pkg", "tool", "linux_"+runtime.GOARCH, "link"), "harness": harness,
	} {
		id, err := identify(path)
		if err != nil {
			return fmt.Errorf("identify %s: %w", label, err)
		}
		identities[label] = id
	}
	return nil
}

func recordHost(output, stage string) error {
	data := map[string]any{"time_utc": time.Now().UTC().Format(time.RFC3339Nano), "arch": runtime.GOARCH,
		"cpus": runtime.NumCPU(), "go": runtime.Version()}
	for _, path := range []string{"/proc/cpuinfo", "/proc/meminfo", "/proc/self/cgroup", "/proc/self/status",
		"/proc/pressure/cpu", "/proc/pressure/memory", "/sys/fs/cgroup/cpu.max", "/sys/fs/cgroup/cpu.stat",
		"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory.events"} {
		value, err := os.ReadFile(path) // #nosec G304 -- only the fixed host-observation paths listed above.
		if err != nil {
			data[path] = map[string]string{"unavailable": err.Error()}
		} else {
			data[path] = string(value)
		}
	}
	membership, membershipErr := os.ReadFile("/proc/self/cgroup")
	data["unified_cgroup"] = observeCgroups(string(membership), membershipErr)
	limits, limitsErr := cgroup.ReadLimits()
	data["launcher_resolved_limits"], data["launcher_limits_error"] = limits, errorText(limitsErr)
	uname, err := command("", cleanEnv(), "uname", "-a")
	if err != nil {
		return err
	}
	data["uname"] = string(uname)
	return saveJSON(filepath.Join(output, "host-"+stage+".json"), data)
}

func currentCgroupPath(membership string) (string, error) {
	for line := range strings.SplitSeq(membership, "\n") {
		path, found := strings.CutPrefix(line, "0::")
		if !found {
			continue
		}
		if !strings.HasPrefix(path, "/") {
			return "", errors.New("unified cgroup path is not absolute")
		}
		for component := range strings.SplitSeq(path, "/") {
			if component == ".." {
				return "", errors.New("unified cgroup path contains parent traversal")
			}
		}
		return filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(filepath.Clean(path), "/")), nil
	}
	return "", errors.New("unified cgroup membership unavailable; v1 ancestors are unknown")
}

func observeCgroups(membership string, membershipErr error) map[string]any {
	result := map[string]any{"membership": membership}
	current, err := currentCgroupPath(membership)
	if err := errors.Join(membershipErr, err); err != nil {
		result["unknown"] = err.Error()
		return result
	}
	result["current"] = current
	var ancestors []map[string]any
	const root = "/sys/fs/cgroup"
	for path := current; ; path = filepath.Dir(path) {
		files := map[string]any{"path": path}
		resolved, err := filepath.EvalSymlinks(path)
		relative, relativeErr := filepath.Rel(root, resolved)
		if err != nil || relativeErr != nil || relative == ".." || strings.HasPrefix(relative, "../") {
			files["unknown"] = "cgroup unavailable or resolved outside /sys/fs/cgroup"
		} else {
			for _, name := range []string{"cpu.max", "cpu.stat", "memory.max", "memory.high", "memory.current", "memory.events"} {
				// #nosec G304 -- fixed filenames below a resolved and bounded /sys/fs/cgroup ancestor.
				value, readErr := os.ReadFile(filepath.Join(resolved, name))
				if readErr != nil {
					files[name] = map[string]string{"unknown": readErr.Error()}
				} else {
					files[name] = string(value)
				}
			}
		}
		ancestors = append(ancestors, files)
		if path == root {
			break
		}
	}
	result["current_and_ancestors"] = ancestors
	return result
}

func prepare(base, candidate, output string) (artifacts, error) {
	item := artifacts{Native: filepath.Join(output, "native"), Level: "v1", Bundles: map[string]string{}}
	levelEnv := "GOAMD64=v1"
	if runtime.GOARCH == "arm64" {
		item.Level, levelEnv = "v8.0", "GOARM64=v8.0"
	}
	build := func(dir, target, pkg, profile string) error {
		args := []string{"build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w -buildid="}
		if profile == "minimal" {
			args = append(args, "-tags=minimal")
		}
		_, err := command(dir, cleanEnv(levelEnv), "go", append(args, "-o", target, pkg)...)
		return err
	}
	if err := build(candidate, item.Native, "./tests/e2e/testdata/telemetry/fixture", ""); err != nil {
		return item, err
	}
	nativeID, err := identify(item.Native)
	if err != nil {
		return item, err
	}
	item.SelectedSHA256 = nativeID.SHA256
	packer := filepath.Join(output, "baseline-packer")
	if err := build(base, packer, "./cmd/microfat", ""); err != nil {
		return item, err
	}
	packerID, err := identify(packer)
	if err != nil {
		return item, err
	}
	identities := []identity{nativeID, packerID}
	for _, profile := range []string{"full", "minimal"} {
		var stubIDs []identity
		for _, arm := range []struct{ name, dir string }{{baseline, base}, {candidateArm, candidate}} {
			stub := filepath.Join(output, profile+"-"+arm.name+"-stub")
			if err := build(arm.dir, stub, "./cmd/microfat-stub", profile); err != nil {
				return item, err
			}
			stubID, err := identify(stub)
			if err != nil {
				return item, err
			}
			stubIDs = append(stubIDs, stubID)
			bundle := filepath.Join(output, profile+"-"+arm.name+"-bundle")
			if _, err := command(candidate, cleanEnv(), packer, "pack", "--name", "telemetry-startup", "--arch", runtime.GOARCH,
				"--stub", stub, "--format-version", "2", "--compression", "zstd:better", "-o", bundle,
				"-v", item.Level+"="+item.Native); err != nil {
				return item, err
			}
			bundleID, err := identify(bundle)
			if err != nil {
				return item, err
			}
			identities = append(identities, stubID, bundleID)
			item.Bundles[profile+"/"+arm.name] = bundle
		}
		matching, err := matchBundles(item.Bundles[profile+"/"+baseline], item.Bundles[profile+"/"+candidateArm])
		if err != nil {
			return item, err
		}
		matching["stubs"], matching["stub_size_delta_bytes"] = stubIDs, stubIDs[1].Bytes-stubIDs[0].Bytes
		if err := saveJSON(filepath.Join(output, profile+"-matching.json"), matching); err != nil {
			return item, err
		}
	}
	return item, saveJSON(filepath.Join(output, "identities.json"), identities)
}

func matchBundles(base, candidate string) (map[string]any, error) {
	var indexes []*format.Index
	var payloads [][]identity
	for _, path := range []string{base, candidate} {
		f, err := os.Open(path) // #nosec G304 -- experiment-owned compiled bundle.
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
			blobs = append(blobs, identity{variant.Level, hex.EncodeToString(h.Sum(nil)), variant.CompressedSize})
			index.Variants[n].Offset = 0
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		indexes, payloads = append(indexes, index), append(payloads, blobs)
	}
	if !reflect.DeepEqual(indexes[0], indexes[1]) || !reflect.DeepEqual(payloads[0], payloads[1]) {
		return nil, errors.New("arms differ in normalized metadata or compressed payload")
	}
	baseID, err := identify(base)
	if err != nil {
		return nil, err
	}
	candidateID, err := identify(candidate)
	if err != nil {
		return nil, err
	}
	return map[string]any{"normalized_index": indexes[0], "compressed_payloads": payloads[0],
		baseline: baseID, candidateArm: candidateID, "complete_size_delta_bytes": candidateID.Bytes - baseID.Bytes}, nil
}

func identify(path string) (identity, error) {
	f, err := os.Open(path) // #nosec G304 -- explicit source, executable or experiment-owned artifact identity.
	if err != nil {
		return identity{}, err
	}
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err := errors.Join(err, f.Close()); err != nil {
		return identity{}, err
	}
	return identity{path, hex.EncodeToString(h.Sum(nil)), size}, nil
}
