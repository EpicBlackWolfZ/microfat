//go:build linux

// This prospective experiment is opt-in and outside ordinary unit-test discovery.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	baselineSHA        = "6eb862b914e8b3dfdf820dcbd1bfa079e4275dd6"
	goVersion          = "go1.27.3"
	protocolPath       = "docs/disabled-telemetry.md"
	pairs              = 80
	privateDirectory   = 0o700
	privateFile        = 0o600
	launchTimeout      = 10 * time.Second
	preparationTimeout = 10 * time.Minute
	maxRSSKiB          = 512 * 1024
	baseline           = "baseline"
	candidateArm       = "candidate"
	native             = "native"
	cold               = "cold-cache"
	warm               = "warm-cache"
	memfd              = "memfd"
	off                = "off"
	ready              = "MICROFAT_TELEMETRY_STARTUP_READY_v1\n"
)

type cell struct{ Profile, Path, Logging string }
type scheduled struct {
	cell
	Sequence, Round int
	Arm             string
}
type identity struct {
	Path, SHA256 string
	Bytes        int64
}
type artifacts struct {
	Native, SelectedSHA256, Level string
	Bundles                       map[string]string
}
type observation struct {
	scheduled
	Started                                         string
	WallNS, CPUNS, MaxRSSKiB                        int64
	Stdout, Stderr, Error, CacheError, CleanupError string
	Success                                         bool
}

func main() {
	base := flag.String("base", "", "clean checkout of the fixed baseline")
	candidate := flag.String("candidate", "", "clean checkout of the exact frozen candidate")
	output := flag.String("output", "", "new evidence directory, never overwritten")
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
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") || runtime.Version() != goVersion {
		return errors.New("requires native Linux amd64/arm64 and Go 1.27.3")
	}
	for _, path := range []*string{&base, &candidate, &output} {
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return err
		}
		*path = absolute
	}
	if err := os.Mkdir(output, privateDirectory); err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, os.WriteFile(filepath.Join(output, "failure.txt"), []byte(retErr.Error()+"\n"), privateFile))
		}
	}()
	// Freeze all intended attempts before source/toolchain/fixture preparation.
	if err := saveJSON(filepath.Join(output, "schedule.json"), schedule()); err != nil {
		return err
	}
	if err := freezeSources(base, candidate, output); err != nil {
		return err
	}
	if err := verifyToolchain(output); err != nil {
		return err
	}
	if err := recordHost(output, "before"); err != nil {
		return err
	}
	items, err := prepare(base, candidate, output)
	if err != nil {
		return err
	}
	if err := recheckSources(base, candidate, output, "before-timing"); err != nil {
		return err
	}
	observations, err := measure(output, items)
	if err != nil {
		return err
	}
	if err := recheckSources(base, candidate, output, "after-timing"); err != nil {
		return err
	}
	if err := recordHost(output, "after"); err != nil {
		return err
	}
	decisions, correct, resource, equivalent := evaluate(observations)
	if err := saveJSON(filepath.Join(output, "decisions.json"), map[string]any{
		"architecture": runtime.GOARCH, "planned_attempts": len(schedule()), "recorded_attempts": len(observations),
		"correctness_pass": correct, "resource_pass": resource, "all_cells_equivalent": equivalent,
		"decisions": decisions, "scope": "one fixed complete-process startup fixture; no p99 or fleet claim",
	}); err != nil {
		return err
	}
	if !correct || !resource {
		return errors.New("correctness/resource qualification failed; retain every attempt")
	}
	if !equivalent {
		fmt.Println("Timing equivalence is unresolved or outside the declared band; see decisions.json")
	}
	return nil
}

func cells() []cell {
	result := []cell{{native, native, off}}
	for _, profile := range []string{"full", "minimal"} {
		for _, path := range []string{memfd, cold, warm} {
			for _, logging := range []string{off, "json", "debug"} {
				result = append(result, cell{profile, path, logging})
			}
		}
	}
	return result
}

func schedule() []scheduled {
	var result []scheduled
	for round := range pairs {
		visits := cells()
		arms := []string{baseline, candidateArm}
		if round%2 != 0 {
			for i, j := 0, len(visits)-1; i < j; i, j = i+1, j-1 {
				visits[i], visits[j] = visits[j], visits[i]
			}
			arms[0], arms[1] = arms[1], arms[0]
		}
		for _, visit := range visits {
			for _, arm := range arms {
				result = append(result, scheduled{visit, len(result), round, arm})
			}
		}
	}
	return result
}

func saveJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), privateFile)
}

func errorText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
