package benchmarkmatrix

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/stretchr/testify/require"
)

func TestCases(t *testing.T) {
	t.Parallel()
	cases, err := Cases(Options{Tier: Compatibility, Workload: MixedWorkload})
	require.NoError(t, err)
	require.Len(t, cases, 16)
	names := map[string]bool{}
	for _, scenario := range cases {
		name := scenario["name"].(string)
		require.False(t, names[name])
		names[name] = true
		require.Contains(t, []int{1, 2}, scenario["format"])
		if scenario["dictionary"].(bool) {
			require.Equal(t, "zstd", scenario["codec"])
		}
	}
	for _, tier := range []string{Nightly, Release} {
		for _, workload := range []string{MixedWorkload, "cpu", "memory"} {
			all, err := Cases(Options{Tier: tier, Workload: workload})
			require.NoError(t, err)
			require.Len(t, all, 2)
			require.Equal(t, 4, all[0]["iterations"])
			require.Equal(t, 32, all[1]["iterations"])
			for _, intensity := range []string{StandardIntensity, HeavyIntensity} {
				shard, err := Cases(Options{Tier: tier, Workload: workload, Intensity: intensity})
				require.NoError(t, err)
				require.Len(t, shard, 1)
				require.Equal(t, tier+"-"+workload+"-"+intensity, shard[0]["name"])
				require.Equal(t, workload, shard[0]["workload"])
			}
			if tier == Release {
				require.Equal(t, 20, all[0]["blocks"])
				require.Equal(t, 30000, all[0]["duration_ms"])
				require.Equal(t, 10000, all[0]["warmup_ms"])
				require.Equal(t, 250, all[0]["sample_ms"])
				require.Equal(t, true, all[0]["exec_diagnostics"])
			}
		}
	}
	for _, options := range []Options{{Tier: "invalid", Workload: MixedWorkload}, {Tier: Release, Workload: "bad"},
		{Tier: Nightly, Workload: MixedWorkload, Intensity: "bad"},
		{Tier: Compatibility, Workload: MixedWorkload, Intensity: StandardIntensity}} {
		_, err := Cases(options)
		require.Error(t, err)
	}
}

func validControls(t *testing.T) map[string]any {
	t.Helper()
	controls, err := ParseControls([]byte(`{"target":{"affinity":[0]},"generator":{"affinity":[1]},"runner_identity":"test-only"}`))
	require.NoError(t, err)
	return controls
}

func TestControls(t *testing.T) {
	t.Parallel()
	require.NoError(t, RequireControls(validControls(t)))
	for _, invalid := range []string{"null", "[]", "{", `{"output":"injected"}`} {
		_, err := ParseControls([]byte(invalid))
		require.Error(t, err)
	}
	for _, invalid := range []map[string]any{nil, {"target": map[string]any{}},
		{"target": map[string]any{"affinity": []int{0}}}, {"target": "bad", "generator": true}} {
		require.Error(t, RequireControls(invalid))
	}
}

func environmentValue(spec process.Spec, key string) string {
	for _, entry := range spec.Env {
		if value, found := strings.CutPrefix(entry, key+"="); found {
			return value
		}
	}
	return ""
}

func loadJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, target))
}

func TestRunPreservesAllOutcomes(t *testing.T) {
	t.Parallel()
	output := t.TempDir()
	options := Options{Tier: Nightly, Workload: "cpu", Output: output}
	calls := 0
	err := Run(context.Background(), options, validControls(t), func(ctx context.Context, spec process.Spec) (int, error) {
		calls++
		require.Equal(t, "bash", spec.Path)
		require.Equal(t, []string{"scripts/benchmark-ci.sh"}, spec.Args)
		require.Equal(t, "0", environmentValue(spec, "BENCHMARK_HOSTED_RELEASE"))
		var config map[string]any
		loadJSON(t, environmentValue(spec, "BENCHMARK_CONFIG"), &config)
		require.Equal(t, "test-only", config["runner_identity"])
		require.True(t, filepath.IsAbs(environmentValue(spec, "BENCHMARK_OUTPUT")))
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.InDelta(t, caseTimeout.Seconds(), time.Until(deadline).Seconds(), 1)
		if calls == 1 {
			return 7, nil
		}
		return 0, nil
	})
	require.Error(t, err)
	require.Equal(t, 2, calls)
	var outcomes []Outcome
	loadJSON(t, filepath.Join(output, "outcomes.json"), &outcomes)
	require.Equal(t, []Outcome{{Scenario: "nightly-cpu-standard", ExitCode: 7}, {Scenario: "nightly-cpu-heavy", ExitCode: 0}}, outcomes)
	var schedule []map[string]any
	loadJSON(t, filepath.Join(output, "schedule.json"), &schedule)
	require.NotContains(t, schedule[0], "target", "schedule must not be mutated when adding controls to each config")
}

func TestReleaseQualification(t *testing.T) {
	t.Parallel()
	for name, qualification := range map[string]string{"success": `{"publishable":true}`, "false": `{"publishable":false}`,
		"missing field": `{}`, "malformed": "{", "missing file": ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			options := Options{Tier: Release, Workload: "memory", Intensity: StandardIntensity, Output: t.TempDir()}
			err := Run(context.Background(), options, validControls(t), func(_ context.Context, spec process.Spec) (int, error) {
				require.Equal(t, "1", environmentValue(spec, "BENCHMARK_HOSTED_RELEASE"))
				if qualification != "" {
					output := environmentValue(spec, "BENCHMARK_OUTPUT")
					require.NoError(t, os.MkdirAll(output, directoryMode))
					require.NoError(t, os.WriteFile(filepath.Join(output, "qualification.json"), []byte(qualification), fileMode))
				}
				return 0, nil
			})
			if name == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestRunErrorsAndCancellation(t *testing.T) {
	t.Parallel()
	options := Options{Tier: Nightly, Workload: MixedWorkload, Intensity: StandardIntensity, Output: t.TempDir()}
	good := func(context.Context, process.Spec) (int, error) { return 0, nil }
	require.Error(t, Run(context.Background(), Options{}, nil, good))
	require.Error(t, Run(context.Background(), Options{Tier: Release, Workload: MixedWorkload}, nil, good))
	for _, filename := range []string{"schedule.json", "outcomes.json", "nightly-mixed-standard.json"} {
		broken := options
		broken.Output = t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(broken.Output, filename), directoryMode))
		require.Error(t, Run(context.Background(), broken, nil, good))
	}
	broken := options
	broken.Output = filepath.Join(t.TempDir(), "not-directory")
	require.NoError(t, os.WriteFile(broken.Output, []byte("file"), fileMode))
	require.Error(t, Run(context.Background(), broken, nil, good))
	require.Error(t, WriteJSON(filepath.Join(t.TempDir(), "bad"), make(chan int)))
	for _, status := range []int{0, 7} {
		require.Error(t, Run(context.Background(), options, nil, func(context.Context, process.Spec) (int, error) {
			return status, errors.New("run failed")
		}))
		var outcomes []Outcome
		loadJSON(t, filepath.Join(options.Output, "outcomes.json"), &outcomes)
		require.Equal(t, max(1, status), outcomes[0].ExitCode)
		require.Equal(t, "run failed", outcomes[0].Reason)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.ErrorIs(t, Run(ctx, options, nil, func(context.Context, process.Spec) (int, error) { cancel(); return 0, nil }), context.Canceled)
	var outcomes []Outcome
	loadJSON(t, filepath.Join(options.Output, "outcomes.json"), &outcomes)
	require.Len(t, outcomes, 1)
	require.Equal(t, 1, outcomes[0].ExitCode)
	require.Contains(t, outcomes[0].Reason, "cancellation")
}

func TestEnvironmentReplacement(t *testing.T) {
	t.Setenv("BENCHMARK_CONFIG", "old")
	t.Setenv("BENCHMARK_OUTPUT", "old")
	t.Setenv("BENCHMARK_HOSTED_RELEASE", "old")
	env := caseEnvironment("config", "output", "0")
	for _, key := range []string{"BENCHMARK_CONFIG", "BENCHMARK_OUTPUT", "BENCHMARK_HOSTED_RELEASE"} {
		count := 0
		for _, entry := range env {
			if strings.HasPrefix(entry, key+"=") {
				count++
			}
		}
		require.Equal(t, 1, count)
	}
}

func TestRemovedWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Remove(root))
	require.Error(t, Run(context.Background(), Options{Tier: Nightly, Workload: MixedWorkload, Output: "relative"}, nil, nil))
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestRunProcess(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"printf output; printf diagnostic >&2", "exit 7"} {
		var stdout, stderr strings.Builder
		status, err := RunProcess(context.Background(), process.Spec{Path: "sh", Args: []string{"-c", command}}, &stdout, &stderr)
		if command == "exit 7" {
			require.Equal(t, 7, status)
			require.Error(t, err)
		} else {
			require.Zero(t, status)
			require.NoError(t, err)
			require.Equal(t, "output", stdout.String())
			require.Equal(t, "diagnostic", stderr.String())
		}
	}
	_, err := RunProcess(context.Background(), process.Spec{Path: "/missing"}, io.Discard, io.Discard)
	require.Error(t, err)
	_, err = RunProcess(context.Background(), process.Spec{Path: "sh", Args: []string{"-c", "printf output"}}, failingWriter{}, io.Discard)
	require.ErrorContains(t, err, "write failed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = RunProcess(ctx, process.Spec{Path: "/missing"}, io.Discard, io.Discard)
	require.ErrorIs(t, err, context.Canceled)
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = RunProcess(ctx, process.Spec{Path: "sh", Args: []string{"-c", "sleep 60"}}, io.Discard, io.Discard)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
