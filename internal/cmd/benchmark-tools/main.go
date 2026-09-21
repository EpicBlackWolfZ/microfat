// Command benchmark-tools validates inputs used by the hosted benchmark scripts.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
)

const (
	fortioModule    = "fortio.org/fortio"
	goToolchain     = "go1.27.1"
	minimumCPUs     = 2
	benchmarkMemory = 2 * 1024 * 1024 * 1024
	baselineCommand = "baseline"
	controlsCommand = "controls"
	versionCommand  = "fortio-version"
	verifyCommand   = "verify-fortio"
	kernelCommand   = "kernel-result"
)

var stableVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

type fortioPin struct {
	Module       string `json:"module"`
	Version      string `json:"version"`
	ModuleSum    string `json:"module_sum"`
	GoModSum     string `json:"go_mod_sum"`
	SourceCommit string `json:"source_commit"`
	Toolchain    string `json:"toolchain"`
}

func readPin(data []byte) (fortioPin, error) {
	var lock struct {
		Fortio fortioPin `json:"fortio"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return fortioPin{}, fmt.Errorf("decode tool lock: %w", err)
	}
	pin := lock.Fortio
	if pin.Module != fortioModule || pin.Toolchain != goToolchain || !stableVersion.MatchString(pin.Version) ||
		pin.ModuleSum == "" || pin.GoModSum == "" || pin.SourceCommit == "" {
		return fortioPin{}, errors.New("incomplete or unsupported Fortio lock")
	}
	return pin, nil
}

func fortioVersion(lock []byte, version string) (string, error) {
	pin, err := readPin(lock)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(version)
	if len(fields) != 4 || fields[0] != "go" || fields[1] != "version" || fields[2] != pin.Toolchain {
		return "", fmt.Errorf("Go 1.27.1 required; got %q", version)
	}
	return pin.Version, nil
}

func verifyFortio(lock, downloaded []byte) error {
	pin, err := readPin(lock)
	if err != nil {
		return err
	}
	var module struct {
		Path, Version, Sum, GoModSum, Error string
		Origin                              struct{ Hash string }
	}
	if err := json.Unmarshal(downloaded, &module); err != nil {
		return fmt.Errorf("decode downloaded module: %w", err)
	}
	if module.Error != "" || module.Path != pin.Module || module.Version != pin.Version ||
		module.Sum != pin.ModuleSum || module.GoModSum != pin.GoModSum || module.Origin.Hash != pin.SourceCommit {
		return errors.New("Fortio module identity, checksums or source commit differ from lock")
	}
	return nil
}

func previousRelease(git func(...string) ([]byte, error)) (string, error) {
	head, err := git("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	tags, err := git("tag", "--merged", "HEAD", "--sort=-version:refname")
	if err != nil {
		return "", err
	}
	for tag := range strings.SplitSeq(strings.TrimSpace(string(tags)), "\n") {
		if !stableVersion.MatchString(tag) {
			continue
		}
		commit, resolveErr := git("rev-parse", tag+"^{commit}")
		if resolveErr != nil {
			return "", resolveErr
		}
		if strings.TrimSpace(string(commit)) != strings.TrimSpace(string(head)) {
			return strings.TrimSpace(string(commit)), nil
		}
	}
	return "", errors.New("no previous stable release ancestor; explicit baseline support is required")
}

func controls(root string, cpus []int) ([]byte, error) {
	if root == "" || strings.ContainsAny(root, "\r\n") {
		return nil, errors.New("invalid benchmark cgroup root")
	}
	cpus = slices.Clone(cpus)
	slices.Sort(cpus)
	cpus = slices.Compact(cpus)
	if len(cpus) < minimumCPUs || cpus[0] < 0 || cpus[len(cpus)-1] >= system.MaxCPU {
		return nil, errors.New("at least two distinct allowed vCPUs required")
	}
	options := func(cpu int) system.Options {
		return system.Options{Affinity: []int{cpu}, CgroupRoot: root, Version: "v2",
			CPUQuotaUS: system.DefaultPeriod, CPUPeriodUS: system.DefaultPeriod, MemoryBytes: benchmarkMemory}
	}
	return json.MarshalIndent(struct {
		Target    system.Options `json:"target"`
		Generator system.Options `json:"generator"`
	}{Target: options(cpus[0]), Generator: options(cpus[len(cpus)-1])}, "", "  ")
}

func kernelResult(data []byte) error {
	text := string(data)
	const resultPrefix = "MICROFAT_KERNEL_RESULT="
	if strings.Contains(text, "--- SKIP:") || strings.Contains(text, "--- FAIL:") || strings.Count(text, resultPrefix) != 1 {
		return errors.New("required real-kernel tests failed, skipped, or did not complete")
	}
	for line := range strings.SplitSeq(text, "\n") {
		if strings.TrimSuffix(line, "\r") == resultPrefix+"0" {
			return nil
		}
	}
	return errors.New("missing exact successful kernel result")
}

func run(args []string, output io.Writer, git func(...string) ([]byte, error), affinity func([]int) ([]int, error)) error {
	if len(args) == 1 && args[0] == baselineCommand {
		value, err := previousRelease(git)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, value)
		return err
	}
	if len(args) == 2 && args[0] == controlsCommand {
		cpus, err := affinity(nil)
		if err != nil {
			return err
		}
		data, err := controls(args[1], cpus)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, string(data))
		return err
	}
	return fileCommand(args, output)
}

func fileCommand(args []string, output io.Writer) error {
	if (len(args) != 2 || args[0] != kernelCommand) &&
		(len(args) != 3 || (args[0] != versionCommand && args[0] != verifyCommand)) {
		return errors.New("usage: benchmark-tools baseline|controls ROOT|kernel-result LOG|" +
			"fortio-version LOCK GO_VERSION|verify-fortio LOCK MODULE")
	}
	data, err := os.ReadFile(args[1]) // #nosec G304 G703 -- operator-selected input; no confined root is promised.
	if err != nil {
		return err
	}
	switch args[0] {
	case kernelCommand:
		return kernelResult(data)
	case versionCommand:
		version, versionErr := fortioVersion(data, args[2])
		if versionErr != nil {
			return versionErr
		}
		_, err = fmt.Fprintln(output, version)
		return err
	default:
		module, readErr := os.ReadFile(args[2]) // #nosec G304 G703 -- operator-selected module metadata input.
		if readErr != nil {
			return readErr
		}
		return verifyFortio(data, module)
	}
}

func gitOutput(args ...string) ([]byte, error) {
	return exec.Command("git", args...).Output() // #nosec G204 -- all arguments are constructed by previousRelease.
}

func main() {
	if err := run(os.Args[1:], os.Stdout, gitOutput, system.SupportedAffinity); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
