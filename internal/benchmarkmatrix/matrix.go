// Package benchmarkmatrix enumerates and preserves hosted benchmark case outcomes.
package benchmarkmatrix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
)

const (
	MixedWorkload         = "mixed"
	StandardIntensity     = "standard"
	HeavyIntensity        = "heavy"
	Compatibility         = "compatibility"
	Release               = "release"
	Nightly               = "nightly"
	standardIterations    = 4
	heavyIterations       = 32
	compatibilityDuration = 200
	nightlyBlocks         = 10
	releaseBlocks         = 20
	nightlyDuration       = 3000
	releaseDuration       = 30000
	nightlyWarmup         = 1000
	releaseWarmup         = 10000
	nightlySample         = 25
	releaseSample         = 250
	caseTimeout           = 6 * time.Hour
	fileMode              = 0o600
	directoryMode         = 0o700
)

type Case map[string]any

type Options struct {
	Tier, Workload, Intensity, Output string
}

func Cases(options Options) ([]Case, error) {
	if !slices.Contains([]string{Compatibility, Nightly, Release}, options.Tier) ||
		!slices.Contains([]string{MixedWorkload, "cpu", "memory"}, options.Workload) ||
		!slices.Contains([]string{"", StandardIntensity, HeavyIntensity}, options.Intensity) {
		return nil, errors.New("invalid matrix tier, workload or intensity")
	}
	if options.Tier == Compatibility {
		if options.Intensity != "" {
			return nil, errors.New("compatibility has no sustained intensity shards")
		}
		return compatibilityCases(), nil
	}
	var cases []Case
	for _, intensity := range []string{StandardIntensity, HeavyIntensity} {
		if options.Intensity != "" && options.Intensity != intensity {
			continue
		}
		iterations := standardIterations
		if intensity == HeavyIntensity {
			iterations = heavyIterations
		}
		blocks, duration, warmup, sample := nightlyBlocks, nightlyDuration, nightlyWarmup, nightlySample
		if options.Tier == Release {
			blocks, duration, warmup, sample = releaseBlocks, releaseDuration, releaseWarmup, releaseSample
		}
		cases = append(cases, Case{"name": options.Tier + "-" + options.Workload + "-" + intensity, "workload": options.Workload,
			"iterations": iterations, "blocks": blocks, "duration_ms": duration, "warmup_ms": warmup, "sample_ms": sample,
			"exec_diagnostics": options.Tier == Release})
	}
	return cases, nil
}

func compatibilityCases() []Case {
	var cases []Case
	for _, version := range []int{1, 2} {
		for _, profile := range []string{"full", "minimal"} {
			for _, codec := range []string{"none", "lz4", "zstd"} {
				dictionaries := []bool{false}
				if codec == "zstd" {
					dictionaries = append(dictionaries, true)
				}
				for index, dictionary := range dictionaries {
					cases = append(cases, Case{"name": fmt.Sprintf("format%d-%s-%s-dict%d", version, profile, codec, index),
						"format": version, "profile": profile, "codec": codec, "dictionary": dictionary, "workload": MixedWorkload,
						"blocks": 1, "duration_ms": compatibilityDuration, "warmup_ms": 0})
				}
			}
		}
	}
	return cases
}

func ParseControls(data []byte) (map[string]any, error) {
	var controls map[string]any
	if err := json.Unmarshal(data, &controls); err != nil {
		return nil, err
	}
	if controls == nil {
		return nil, errors.New("control file must contain a JSON object")
	}
	for key := range controls {
		if !slices.Contains([]string{"target", "generator", "runner_identity"}, key) {
			return nil, errors.New("control file may contain only target, generator and runner_identity settings")
		}
	}
	return controls, nil
}

func RequireControls(controls map[string]any) error {
	for _, role := range []string{"target", "generator"} {
		settings, ok := controls[role].(map[string]any)
		if !ok || len(settings) == 0 {
			return errors.New("hosted release measurement requires explicit target and generator controls")
		}
	}
	return nil
}

type Outcome struct {
	Scenario string `json:"scenario"`
	ExitCode int    `json:"exit_code"`
	Reason   string `json:"reason,omitempty"`
}

type Execute func(context.Context, process.Spec) (int, error)

func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), fileMode) // #nosec G304 -- explicit operator-selected artifact path.
}

func Run(ctx context.Context, options Options, controls map[string]any, execute Execute) error {
	cases, err := Cases(options)
	if err != nil {
		return err
	}
	if options.Tier == Release {
		if err := RequireControls(controls); err != nil {
			return err
		}
	}
	output, err := filepath.Abs(options.Output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(output, directoryMode); err != nil {
		return err
	}
	if err := WriteJSON(filepath.Join(output, "schedule.json"), cases); err != nil {
		return err
	}
	var outcomes []Outcome
	failed := false
	for _, scenario := range cases {
		outcome := runCase(ctx, options.Tier, output, scenario, controls, execute)
		outcomes = append(outcomes, outcome)
		failed = failed || outcome.ExitCode != 0
		if err := WriteJSON(filepath.Join(output, "outcomes.json"), outcomes); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if failed {
		return errors.New("one or more benchmark matrix cases failed; see outcomes.json")
	}
	return nil
}

func runCase(ctx context.Context, tier, output string, scenario Case, controls map[string]any, execute Execute) Outcome {
	name := scenario["name"].(string)
	outcome := Outcome{Scenario: name, ExitCode: 1}
	config := maps.Clone(scenario)
	maps.Copy(config, controls)
	configPath := filepath.Join(output, name+".json")
	if err := WriteJSON(configPath, config); err != nil {
		outcome.Reason = err.Error()
		return outcome
	}
	hosted := "0"
	if tier == Release {
		hosted = "1"
	}
	spec := process.Spec{Path: "bash", Args: []string{"scripts/benchmark-ci.sh"},
		Env: caseEnvironment(configPath, filepath.Join(output, name), hosted)}
	ctx, cancel := context.WithTimeout(ctx, caseTimeout)
	defer cancel()
	status, err := execute(ctx, spec)
	if ctx.Err() != nil {
		outcome.Reason = "matrix case timeout or cancellation"
		return outcome
	}
	if err != nil {
		if status != 0 {
			outcome.ExitCode = status
		}
		outcome.Reason = err.Error()
		return outcome
	}
	outcome.ExitCode = status
	if tier == Release && status == 0 {
		if err := checkQualification(filepath.Join(output, name, "qualification.json")); err != nil {
			outcome.ExitCode, outcome.Reason = 1, err.Error()
		}
	}
	return outcome
}

func caseEnvironment(config, output, hosted string) []string {
	env := slices.DeleteFunc(os.Environ(), func(value string) bool {
		return strings.HasPrefix(value, "BENCHMARK_CONFIG=") || strings.HasPrefix(value, "BENCHMARK_OUTPUT=") ||
			strings.HasPrefix(value, "BENCHMARK_HOSTED_RELEASE=")
	})
	return append(env, "BENCHMARK_CONFIG="+config, "BENCHMARK_OUTPUT="+output, "BENCHMARK_HOSTED_RELEASE="+hosted)
}

func checkQualification(path string) error {
	data, err := os.ReadFile(path) // #nosec G304 -- result path beneath the selected matrix output directory.
	if err != nil {
		return err
	}
	var evidence struct {
		Publishable bool `json:"publishable"`
	}
	if err := json.Unmarshal(data, &evidence); err != nil {
		return err
	}
	if !evidence.Publishable {
		return errors.New("experiment did not meet release qualification")
	}
	return nil
}
