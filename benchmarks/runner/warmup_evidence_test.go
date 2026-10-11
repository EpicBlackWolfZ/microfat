package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/compare"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/internal/testfixture"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/report"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	warmupFixtureMS       = 10
	warmupSteadyFixtureMS = 20
	warmupFixtureCount    = int64(992)
	warmupFixtureStderr   = "warmup diagnostic\n"
	warmupFixtureLimit    = 5 * time.Second
	warmupFixturePoll     = 10 * time.Millisecond
)

func TestWarmupFailureEvidence(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile("../load/testdata/fortio-1.75.2.json")
	require.NoError(t, err)
	invalid := bytes.Replace(fixture, []byte(`"ActualDuration": 1007219534`), []byte(`"ActualDuration": 0`), 1)
	require.NotEqual(t, fixture, invalid)
	for _, tc := range []struct {
		name, reason, finish string
		raw                  []byte
	}{
		{name: "child exits", reason: "Fortio execution", finish: "exit 1\n", raw: []byte("warmup failed\n")},
		{name: "malformed JSON", reason: "Fortio JSON", raw: []byte("{")},
		{name: "parsed invalid measurements", reason: "invalid HTTP measurements", raw: invalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			rawPath := filepath.Join(root, "warmup-output")
			require.NoError(t, os.WriteFile(rawPath, tc.raw, evidenceFilePerm))
			tool := filepath.Join(root, "fortio")
			script := "#!/bin/sh\ncat '" + rawPath + "'\nprintf '%s\\n' 'warmup diagnostic' >&2\n" + tc.finish
			require.NoError(t, testfixture.WriteExecutable(tool, []byte(script)))
			exp := testfixture.Experiment()
			trial, files := measureWarmupFixture(t, context.Background(), tool, exp)
			assert.Contains(t, trial.Reason, "warmup: "+tc.reason)
			assert.Contains(t, string(files["trials/"+trial.ID+"/warmup-error.txt"]), tc.reason)
			assert.Nil(t, trial.Warmup, "unparsed or invalid HTTP output is raw evidence only")
			assert.Nil(t, trial.Load)
			assert.NotContains(t, files, "trials/"+trial.ID+"/fortio.json")
			assert.Equal(t, tc.raw, files["trials/"+trial.ID+"/warmup.json"])
			assert.Equal(t, []byte(warmupFixtureStderr), files["trials/"+trial.ID+"/warmup.stderr"])
			identity := assertWarmupProcessEvidence(t, trial, tool)
			assertWarmupIdentityEqual(t, identity, trial.Generator)
			assert.Contains(t, trial.Metrics, "warmup_generator_user_cpu_seconds")
			assertWarmupFailureBundle(t, exp, trial, files)
		})
	}
}

func TestWarmupStartFailureEvidence(t *testing.T) {
	t.Parallel()
	exp := testfixture.Experiment()
	historical, err := schema.CanonicalV2(exp)
	require.NoError(t, err)
	assert.NotContains(t, string(historical), `"warmup_generator"`)
	trial := exp.Trials[0]
	trial.Outcome, trial.Metrics = schema.OutcomeFailed, make(map[string]schema.Measurement)
	files := make(map[string][]byte)
	opts := RunOptions{Helper: filepath.Join(t.TempDir(), "missing-helper"), Fortio: "/bin/true"}
	measureTrial(context.Background(), warmupEvidenceConfig(), opts, &system.Sandbox{}, nil,
		"http://127.0.0.1:1", &trial, files)
	assert.Contains(t, trial.Reason, "warmup:")
	assert.Contains(t, string(files["trials/"+trial.ID+"/warmup-error.txt"]), "missing-helper")
	assert.Nil(t, trial.Warmup)
	assert.Nil(t, trial.Load)
	assert.Empty(t, trial.Generator)
	assert.Nil(t, trial.WarmupGenerator)
	assert.Empty(t, trial.Metrics)
	for _, name := range []string{"warmup.json", "warmup.stderr"} {
		assert.NotContains(t, files, "trials/"+trial.ID+"/"+name)
	}
	assertWarmupFailureBundle(t, exp, &trial, files)
}

func TestWarmupInterruptedEvidence(t *testing.T) {
	t.Parallel()
	for _, interruption := range []string{"cancel", "deadline"} {
		t.Run(interruption, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			ready := filepath.Join(root, "ready")
			tool := filepath.Join(root, "fortio")
			script := "#!/bin/sh\nprintf '%s\\n' 'warmup running'\nprintf '%s\\n' 'warmup diagnostic' >&2\n" +
				"printf ready > '" + ready + "'\nexec sleep 20\n"
			require.NoError(t, testfixture.WriteExecutable(tool, []byte(script)))
			ctx, cancel := context.WithTimeout(context.Background(), warmupFixtureLimit)
			defer cancel()
			exp := testfixture.Experiment()
			helper, err := os.Executable()
			require.NoError(t, err)
			trial := exp.Trials[0]
			trial.Outcome, trial.Metrics = schema.OutcomeFailed, make(map[string]schema.Measurement)
			files := make(map[string][]byte)
			finished := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(warmupFixtureLimit):
					t.Error("warmup fixture did not finish during cleanup")
				}
			})
			go func() {
				measureTrial(ctx, warmupEvidenceConfig(), RunOptions{Helper: helper, Fortio: tool},
					&system.Sandbox{}, nil, "http://127.0.0.1:1", &trial, files)
				close(finished)
			}()
			require.Eventually(t, func() bool {
				_, err := os.Stat(ready)
				return err == nil
			}, warmupFixtureLimit, warmupFixturePoll, "fixture must start before interruption")
			if interruption == "cancel" {
				cancel()
			}
			select {
			case <-finished:
			case <-time.After(warmupFixtureLimit):
				t.Fatal("interrupted warmup did not stop")
			}
			assert.Contains(t, trial.Reason, "warmup: Fortio execution")
			assert.Contains(t, string(files["trials/"+trial.ID+"/warmup-error.txt"]), "Fortio execution")
			assert.Nil(t, trial.Warmup)
			assert.Nil(t, trial.Load)
			assert.Equal(t, []byte("warmup running\n"), files["trials/"+trial.ID+"/warmup.json"])
			assert.Equal(t, []byte(warmupFixtureStderr), files["trials/"+trial.ID+"/warmup.stderr"])
			assertWarmupIdentityEqual(t, assertWarmupProcessEvidence(t, &trial, tool), trial.Generator)
			finalizeTrial(ctx, time.Now(), &trial, files)
			assert.Equal(t, schema.OutcomeCancelled, trial.Outcome)
			assert.Equal(t, ctx.Err().Error(), trial.Reason)
			assertWarmupFailureBundle(t, exp, &trial, files)
		})
	}
}

func TestValidWarmupEvidence(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile("../load/testdata/fortio-1.75.2.json")
	require.NoError(t, err)
	httpFailure := bytes.ReplaceAll(fixture, []byte(`"200"`), []byte(`"500"`))
	for _, tc := range []struct {
		name, finish string
		warmup, load []byte
		successful   int64
		outcome      string
	}{
		{name: "successful load", warmup: fixture, load: fixture, successful: warmupFixtureCount, outcome: schema.OutcomeOK},
		{name: "HTTP failure remains parsed", warmup: httpFailure, load: httpFailure, outcome: schema.OutcomeFailed},
		{name: "steady state execution failure", warmup: fixture, load: fixture, successful: warmupFixtureCount,
			finish: "exit 1\n", outcome: schema.OutcomeFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			warmPath, loadPath, state := filepath.Join(root, "warmup-output"), filepath.Join(root, "load-output"), filepath.Join(root, "state")
			require.NoError(t, os.WriteFile(warmPath, tc.warmup, evidenceFilePerm))
			require.NoError(t, os.WriteFile(loadPath, tc.load, evidenceFilePerm))
			tool := filepath.Join(root, "fortio")
			script := "#!/bin/sh\nif [ ! -e '" + state + "' ]; then\n: > '" + state + "'\ncat '" + warmPath +
				"'\nprintf '%s\\n' 'warmup diagnostic' >&2\nelse\ncat '" + loadPath +
				"'\nprintf '%s\\n' 'steady diagnostic' >&2\n" + tc.finish + "fi\n"
			require.NoError(t, testfixture.WriteExecutable(tool, []byte(script)))
			exp := testfixture.Experiment()
			trial, files := measureWarmupFixture(t, context.Background(), tool, exp)
			require.NotNil(t, trial.Warmup)
			require.NoError(t, schema.ValidateHTTP(trial.Warmup))
			assert.Equal(t, warmupFixtureCount, trial.Warmup.Requests)
			assert.Equal(t, tc.successful, trial.Warmup.Successful)
			assert.Equal(t, "trials/"+trial.ID+"/warmup.json", trial.Warmup.RawPath)
			assert.Equal(t, tc.warmup, files[trial.Warmup.RawPath])
			assert.Equal(t, []byte(warmupFixtureStderr), files["trials/"+trial.ID+"/warmup.stderr"])
			identity := assertWarmupProcessEvidence(t, trial, tool)
			if tc.successful == warmupFixtureCount {
				assert.NotEqual(t, identity.PID, trial.Generator.PID, "steady state has its own generator process")
				assert.Contains(t, trial.Generator.Arguments, "20ms")
			} else {
				assert.Contains(t, trial.Warmup.Warnings, "HTTP or transport failures")
			}
			assert.Equal(t, tc.outcome, trial.Outcome)
			if tc.outcome == schema.OutcomeFailed {
				if tc.finish != "" {
					assert.Nil(t, trial.Load)
					assert.Contains(t, trial.Reason, "Fortio execution")
				} else {
					assert.Contains(t, trial.Reason, "HTTP or transport errors")
				}
				assertWarmupFailureBundle(t, exp, trial, files)
			} else {
				require.NotNil(t, trial.Load)
				assert.Equal(t, warmupFixtureCount, trial.Load.Successful)
				assert.Empty(t, trial.Reason)
				assert.Contains(t, trial.Metrics, "warmup_generator_user_cpu_seconds")
				assert.Contains(t, trial.Metrics, "generator_user_cpu_seconds")
				assert.NotContains(t, files, "trials/"+trial.ID+"/warmup-error.txt")
			}
		})
	}
}

func warmupEvidenceConfig() ExperimentConfig {
	cfg := DefaultExperimentConfig()
	cfg.WarmupMS, cfg.DurationMS = warmupFixtureMS, warmupSteadyFixtureMS
	return cfg
}

func measureWarmupFixture(t *testing.T, ctx context.Context, tool string,
	exp *schema.ExperimentV2) (*schema.ProcessTrial, map[string][]byte) {
	t.Helper()
	helper, err := os.Executable()
	require.NoError(t, err)
	trial := exp.Trials[0]
	trial.Outcome, trial.Metrics = schema.OutcomeFailed, make(map[string]schema.Measurement)
	files := make(map[string][]byte)
	measureTrial(ctx, warmupEvidenceConfig(), RunOptions{Helper: helper, Fortio: tool}, &system.Sandbox{}, nil,
		"http://127.0.0.1:1", &trial, files)
	return &trial, files
}

func assertWarmupProcessEvidence(t *testing.T, trial *schema.ProcessTrial, tool string) schema.ProcessIdentity {
	t.Helper()
	require.NotNil(t, trial.WarmupGenerator, "warmup process identity must be retained")
	identity := *trial.WarmupGenerator
	assert.Positive(t, identity.PID)
	assert.Equal(t, tool, identity.Executable)
	assert.Contains(t, identity.Arguments, "10ms")
	return identity
}

func assertWarmupFailureBundle(t *testing.T, exp *schema.ExperimentV2, trial *schema.ProcessTrial, files map[string][]byte) {
	t.Helper()
	finalizeTrial(context.Background(), time.Now(), trial, files)
	exp.Trials[0] = *trial
	require.NoError(t, schema.ValidateV2(exp), "failed measurements must remain valid evidence")
	comparisons, err := compare.All(exp, "base")
	require.NoError(t, err)
	require.NotEmpty(t, comparisons)
	for _, comparison := range comparisons {
		assert.Equal(t, "inconclusive", comparison.Status)
		assert.False(t, comparison.Significant)
	}
	exp.Comparisons = comparisons
	bundle := filepath.Join(t.TempDir(), "bundle")
	require.NoError(t, report.WriteBundle(exp, bundle, files))
	read, err := report.ReadBundle(bundle)
	require.NoError(t, err)
	assert.Equal(t, trial.Outcome, read.Trials[0].Outcome)
	assert.Equal(t, trial.Reason, read.Trials[0].Reason)
	assertWarmupIdentityEqual(t, trial.Generator, read.Trials[0].Generator)
	if trial.WarmupGenerator != nil {
		require.NotNil(t, read.Trials[0].WarmupGenerator)
		assertWarmupIdentityEqual(t, *trial.WarmupGenerator, *read.Trials[0].WarmupGenerator)
	} else {
		assert.Nil(t, read.Trials[0].WarmupGenerator)
	}
	assert.Equal(t, trial.Warmup, read.Trials[0].Warmup)
	verified, err := report.VerifyFiles(bundle)
	require.NoError(t, err)
	for path, raw := range files {
		assert.Equal(t, raw, verified[path], path)
	}
	_, err = report.RenderInput(bundle, "markdown")
	require.NoError(t, err)
}

func assertWarmupIdentityEqual(t *testing.T, expected, actual schema.ProcessIdentity) {
	t.Helper()
	// JSON v2 decodes empty arrays/maps instead of preserving their original nil representation.
	want, err := schema.CanonicalV2(expected)
	require.NoError(t, err)
	got, err := schema.CanonicalV2(actual)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}
