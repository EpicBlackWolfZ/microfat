//go:build linux

// This opt-in prospective experiment is outside ordinary unit-test discovery.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

const (
	goVersion          = "go1.27.3"
	archARM64          = "arm64"
	boundaryFamily     = "boundary"
	balancedProfile    = "balanced"
	fullStub           = "full"
	minimalStub        = "minimal"
	memfdMode          = "memfd"
	nativeSetting      = "native"
	metadataTimeout    = 10 * time.Second
	preparationTimeout = 10 * time.Minute
	bootstrapResamples = 2000
	bootstrapSeed      = 42
	boundaryBytes      = 512 * 1024
	fixedProcs         = 4
	halves             = 2
	lowerTail          = 0.025
	upperTail          = 0.975
	packRounds         = 3
	launchRounds       = 20
	packTimeout        = 120 * time.Second
	launchTimeout      = 10 * time.Second
	packRSSKiB         = 2 * 1024 * 1024
	launchRSSKiB       = 512 * 1024
	privateDir         = 0o700
	privateFile        = 0o600
	protocol           = "docs/codec-policy.md"
)

type group struct{ Family, Profile string }
type setting struct {
	Name, Profile, Compression, Level string
	Dictionary                        bool
}
type step struct {
	group
	Phase, Setting, Stub, Mode string
	Sequence, Round            int
}
type observation struct {
	step
	Started                  string
	WallNS, CPUNS, MaxRSSKiB int64
	SHA256                   string
	Bytes                    int64
	Sizes                    map[string]int64 `json:",omitempty"`
	Variants                 any              `json:",omitempty"`
	Warnings                 []string         `json:",omitempty"`
	Stdout, Stderr, Error    string
	ResourcesObserved        bool
	Success                  bool
}
type workerInput struct {
	Group              group
	Setting            setting
	Stub, Target, Arch string
	Variants           map[string]string
}
type workerResult struct {
	Warnings []string
	Error    string
}

func groups() []group {
	var result []group
	for _, family := range []string{"fortio", "task", "actionlint", "govulncheck"} {
		for _, profile := range []string{"stripped", "unstripped"} {
			result = append(result, group{family, profile})
		}
	}
	for _, profile := range []string{"below", "at", "above"} {
		result = append(result, group{boundaryFamily, profile})
	}
	return result
}

func settings() []setting {
	return []setting{
		{Name: "latency", Profile: "latency"},
		{Name: balancedProfile, Profile: balancedProfile},
		{Name: "size", Profile: "size"},
		{Name: "explicit-none", Profile: balancedProfile, Compression: "none"},
		{Name: "explicit-lz4", Profile: balancedProfile, Compression: "lz4:fastest"},
		{Name: "explicit-zstd-fastest", Profile: balancedProfile, Compression: "zstd:fastest"},
		{Name: "explicit-zstd-best", Profile: balancedProfile, Compression: "zstd:best"},
		{Name: "balanced-dict", Profile: balancedProfile, Dictionary: true},
	}
}

func rotated[T any](input []T, round int) []T {
	result := slices.Clone(input)
	if round%2 != 0 {
		slices.Reverse(result)
	}
	shift := round % len(result)
	return append(result[shift:], result[:shift]...)
}

func schedule() []step {
	var result []step
	for round := range packRounds {
		for _, g := range rotated(groups(), round) {
			for _, s := range rotated(settings(), round) {
				result = append(result, step{g, "pack", s.Name, fullStub, "", len(result), round})
			}
		}
	}
	type arm struct{ Stub, Mode string }
	arms := []arm{{fullStub, memfdMode}, {fullStub, "cold-cache"}, {fullStub, "warm-cache"},
		{minimalStub, memfdMode}, {minimalStub, "cold-cache"}, {minimalStub, "warm-cache"}}
	for round := range launchRounds {
		for _, g := range rotated(groups(), round) {
			for _, a := range rotated(arms, round) {
				result = append(result, step{g, "launch", nativeSetting, a.Stub, a.Mode, len(result), round})
				for _, s := range rotated(settings(), round) {
					result = append(result, step{g, "launch", s.Name, a.Stub, a.Mode, len(result), round})
				}
			}
		}
	}
	return result
}

func main() {
	corpus := flag.String("corpus", "", "prepared schema-2 native corpus directory")
	output := flag.String("output", "", "new evidence directory, never overwritten")
	worker := flag.String("worker", "", "internal: immutable pack input JSON")
	flag.Parse()
	var err error
	if *worker != "" {
		err = runWorker(*worker)
	} else {
		err = run(*corpus, *output)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runWorker(path string) error {
	var input workerInput
	if err := readJSON(path, &input); err != nil {
		return err
	}
	result := workerResult{}
	options := pack.DefaultOptions()
	epoch := int64(0)
	options.StubPath, options.OutputPath, options.TargetArch = input.Stub, input.Target, input.Arch
	options.AppName, options.Variants, options.CreatedUnix = input.Group.Family, input.Variants, &epoch
	options.Profile, options.Compression, options.CompressionLevel = input.Setting.Profile, input.Setting.Compression, input.Setting.Level
	options.EnableDict = input.Setting.Dictionary
	options.WarnFunc = func(format string, args ...any) {
		result.Warnings = append(result.Warnings, fmt.Sprintf(format, args...))
	}
	_, err := pack.Pack(options)
	result.Error = errorText(err)
	return errors.Join(err, json.NewEncoder(os.Stdout).Encode(result))
}

func run(corpusDir, output string) (retErr error) {
	if corpusDir == "" || output == "" {
		return errors.New("-corpus and -output are required")
	}
	if runtime.Version() != goVersion || (runtime.GOARCH != "amd64" && runtime.GOARCH != archARM64) {
		return errors.New("requires native Linux amd64/arm64 and Go 1.27.3")
	}
	corpusDir, err := filepath.Abs(corpusDir)
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := os.Mkdir(output, privateDir); err != nil {
		return err
	}
	defer func() { retErr = persistOutcome(output, retErr) }()
	// The whole intent is persisted before reading, validating or preparing corpus inputs.
	plan := schedule()
	if err := saveJSON(filepath.Join(output, "schedule.json"), plan); err != nil {
		return err
	}
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	finalChecksDone := false
	defer func() {
		if !finalChecksDone {
			retErr = errors.Join(retErr, sourceState(root, output, "after"), recordHost(output, "after"))
		}
	}()
	if err := sourceState(root, output, "before"); err != nil {
		return err
	}
	if err := recordHost(output, "before"); err != nil {
		return err
	}
	if err := freeze(root, corpusDir, output); err != nil {
		return err
	}
	defer func() {
		if !finalChecksDone {
			retErr = errors.Join(retErr, recheckInputs(output, "after"))
		}
	}()
	items, err := prepare(corpusDir, output, root)
	if err != nil {
		return err
	}
	if err := sourceState(root, output, "before-timing"); err != nil {
		return err
	}
	if err := recheckInputs(output, "before-timing"); err != nil {
		return err
	}
	observations, measureErr := measure(output, plan, items)
	measureErr = errors.Join(measureErr, recheckInputs(output, "after"), sourceState(root, output, "after"), recordHost(output, "after"))
	finalChecksDone = true
	summary, valid := summarize(plan, observations)
	summary["cohort_pass"], summary["qualification_pass"] = valid && measureErr == nil, valid && measureErr == nil
	summary["collection_error"] = errorText(measureErr)
	if err := saveJSON(filepath.Join(output, "summary.json"), summary); err != nil {
		return errors.Join(measureErr, err)
	}
	if !valid {
		measureErr = errors.Join(measureErr, errors.New("incomplete, failed or resource-invalid cohort; retain all observations"))
	}
	return measureErr
}

func saveJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), privateFile)
}
func readJSON(path string, value any) error {
	data, err := os.ReadFile(path) // #nosec G304 -- explicit local corpus, worker or experiment-owned evidence JSON.
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
func errorText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func persistOutcome(output string, retErr error) error {
	var summary map[string]any
	if err := readJSON(filepath.Join(output, "summary.json"), &summary); err == nil {
		summary["qualification_pass"] = retErr == nil
		if retErr != nil {
			summary["cohort_pass"] = false
		}
		summary["final_error"] = errorText(retErr)
		retErr = errors.Join(retErr, saveJSON(filepath.Join(output, "summary.json"), summary))
	}
	if retErr != nil {
		retErr = errors.Join(retErr, os.WriteFile(filepath.Join(output, "failure.txt"), []byte(retErr.Error()+"\n"), privateFile))
	}
	return retErr
}
