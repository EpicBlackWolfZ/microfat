//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleIsCompleteAdjacentAndCounterbalanced(t *testing.T) {
	t.Parallel()
	plan := schedule()
	require.Len(t, cells(), 19)
	require.Len(t, plan, pairs*19*2)
	seen := map[scheduled]bool{}
	counts := map[string]int{}
	for i := 0; i < len(plan); i += 2 {
		a, b := plan[i], plan[i+1]
		assert.Equal(t, a.cell, b.cell)
		assert.Equal(t, a.Round, b.Round)
		assert.NotEqual(t, a.Arm, b.Arm)
		assert.Equal(t, i, a.Sequence)
		assert.Equal(t, i+1, b.Sequence)
		wantFirst := baseline
		if a.Round%2 != 0 {
			wantFirst = candidateArm
		}
		assert.Equal(t, wantFirst, a.Arm)
		for _, step := range []scheduled{a, b} {
			assert.False(t, seen[step])
			seen[step] = true
			counts[fmt.Sprintf("%v/%s", step.cell, step.Arm)]++
		}
	}
	assert.Equal(t, cells()[0], plan[0].cell)
	assert.Equal(t, cells()[len(cells())-1], plan[2*len(cells())].cell)
	for key, count := range counts {
		assert.Equal(t, pairs, count, key)
	}
}

func TestEnvironmentRejectsInheritedControls(t *testing.T) {
	for _, key := range []string{"MICROFAT_LOG", "MICROFAT_DEBUG", "MICROFAT_EXEC_MODE", "MICROFAT_CACHE_DIR",
		"XDG_CACHE_HOME", "GODEBUG", "GOMEMLIMIT", "GOGC", "GOMAXPROCS", "GOFLAGS", "GOENV"} {
		t.Setenv(key, "untrusted")
	}
	env := map[string]string{}
	for _, item := range cleanEnv("MICROFAT_LOG=json") {
		key, value, ok := strings.Cut(item, "=")
		require.True(t, ok)
		env[key] = value
	}
	assert.Equal(t, "json", env["MICROFAT_LOG"])
	assert.Equal(t, "off", env["GOENV"])
	assert.Equal(t, "local", env["GOTOOLCHAIN"])
	for _, key := range []string{"MICROFAT_DEBUG", "XDG_CACHE_HOME", "GODEBUG", "GOMEMLIMIT", "GOGC", "GOMAXPROCS", "GOFLAGS"} {
		assert.NotContains(t, env, key)
	}
}

func TestCgroupMembershipRemainsBoundedToUnifiedMount(t *testing.T) {
	t.Parallel()
	path, err := currentCgroupPath("0::/user.slice/session.scope\n")
	require.NoError(t, err)
	assert.Equal(t, "/sys/fs/cgroup/user.slice/session.scope", path)
	for _, input := range []string{"0::/../../private\n", "0::relative\n", "1:memory:/session\n"} {
		_, err := currentCgroupPath(input)
		assert.Error(t, err)
		assert.Contains(t, observeCgroups(input, nil), "unknown")
	}
}

func TestToolchainEvidenceBindsCompilerLinkerAndHarness(t *testing.T) {
	t.Setenv("PATH", filepath.Join(runtime.GOROOT(), "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	require.NoError(t, verifyToolchain(dir))
	data, err := os.ReadFile(filepath.Join(dir, "toolchain.json"))
	require.NoError(t, err)
	var evidence struct {
		Executables map[string]identity `json:"executable_identities"`
	}
	require.NoError(t, json.Unmarshal(data, &evidence))
	for _, label := range []string{"resolved_path_go", "goroot_go", "compile", "link", "harness"} {
		id, ok := evidence.Executables[label]
		require.True(t, ok, label)
		assert.Len(t, id.SHA256, 64)
		assert.Positive(t, id.Bytes)
	}
}

func TestOutputValidatesModeAndNeverRequiresDisabledTelemetry(t *testing.T) {
	t.Parallel()
	item := artifacts{Level: "v1", SelectedSHA256: "fixture-sha256"}
	jsonData, err := json.Marshal(format.DispatchTelemetry{Event: format.EventDispatch, TimestampUnixNano: 1,
		SelectedVariant: item.Level, SelectedSHA256: item.SelectedSHA256, ExecMode: memfd})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, logging, stdout, stderr string
		pass                          bool
	}{
		{"disabled", off, ready, "", true},
		{"disabled unexpectedly logs", off, ready, "unexpected", false},
		{"wrong payload", off, "READY\n", "", false},
		{"JSON", "json", ready, "[microfat] " + string(jsonData) + "\n", true},
		{"JSON missing", "json", ready, "", false},
		{"JSON duplicate", "json", ready, "[microfat] " + string(jsonData) + "\n[microfat] " + string(jsonData) + "\n", false},
		{"debug", "debug", ready,
			"[microfat:debug] host_arch=amd64 selected_variant=v1 exec_mode=memfd decompress_us=1 total_us=2\n", true},
		{"debug wrong mode", "debug", ready,
			"[microfat:debug] host_arch=amd64 selected_variant=v1 exec_mode=cache decompress_us=1 total_us=2\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateOutput(scheduled{cell: cell{"full", memfd, tc.logging}}, item, tc.stdout, tc.stderr)
			if tc.pass {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestCacheChecksBytesModeAndUnexpectedEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, checkCache(dir, "unused", true))
	path := filepath.Join(dir, "fixture")
	require.NoError(t, os.WriteFile(path, []byte("payload"), privateDirectory))
	id, err := identify(path)
	require.NoError(t, err)
	hashed := filepath.Join(dir, id.SHA256)
	require.NoError(t, os.Rename(path, hashed))
	require.NoError(t, checkCache(dir, id.SHA256, false))
	assert.Error(t, checkCache(dir, id.SHA256, true))
	require.NoError(t, os.Chmod(hashed, privateFile))
	assert.Error(t, checkCache(dir, id.SHA256, false))
	require.NoError(t, os.Chmod(hashed, privateDirectory))
	require.NoError(t, os.WriteFile(hashed, []byte("tampered"), privateDirectory))
	assert.Error(t, checkCache(dir, id.SHA256, false))
}

func TestBundleMatchingNormalizesOnlyOffsetsAndRejectsPayloadChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBundle := func(name, stub, payload string) string {
		t.Helper()
		var data bytes.Buffer
		_, err := data.WriteString(stub + payload)
		require.NoError(t, err)
		index := &format.Index{TargetOS: "linux", TargetArch: "amd64", Variants: []format.VariantEntry{{
			Level: "v1", Offset: int64(len(stub)), CompressedSize: int64(len(payload)), UncompressedSize: int64(len(payload)),
			Compression: "none", SHA256: strings.Repeat("a", 64),
		}}}
		_, err = format.WriteIndexAndTrailerWithVersion(&data, index, int64(data.Len()), format.FormatVersion2)
		require.NoError(t, err)
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, data.Bytes(), privateFile))
		return path
	}
	base := writeBundle("base", "base-stub", "payload")
	candidate := writeBundle("candidate", "longer-candidate-stub", "payload")
	matched, err := matchBundles(base, candidate)
	require.NoError(t, err)
	assert.Positive(t, matched["complete_size_delta_bytes"])
	changed := writeBundle("changed", "longer-candidate-stub", "PAYLOAD")
	_, err = matchBundles(base, changed)
	assert.ErrorContains(t, err, "compressed payload")
}

func TestBootstrapAndEquivalenceRetainUncertainty(t *testing.T) {
	t.Parallel()
	values := make([]float64, pairs)
	for i := range values {
		values[i] = 0.9
		if i%2 != 0 {
			values[i] = 1.1
		}
	}
	a := bootstrap(values)
	assert.Equal(t, a, bootstrap(values))
	assert.Equal(t, "inconclusive", classify(a))
	assert.Equal(t, "equivalent", classify(interval{1, 0.95, 1.05}))
	assert.Equal(t, "regression", classify(interval{1.1, 1.06, 1.14}))
	assert.Equal(t, "improvement_outside_equivalence", classify(interval{0.9, 0.88, 0.94}))
	assert.Equal(t, interval{}, bootstrap(nil))
}

func TestDecisionsSeparateUsefulEffectControlsAndFailures(t *testing.T) {
	t.Parallel()
	var observations []observation
	for _, step := range schedule() {
		obs := observation{scheduled: step, Success: true, WallNS: 1_000_000, CPUNS: 1_000_000, MaxRSSKiB: 1024}
		if step.Profile != native && step.Logging == off && step.Arm == candidateArm {
			obs.WallNS = 980_000
		}
		observations = append(observations, obs)
	}
	decisions, correct, resource, equivalent := evaluate(observations)
	require.True(t, correct)
	require.True(t, resource)
	require.True(t, equivalent)
	require.Len(t, decisions, len(cells()))
	for _, decision := range decisions {
		assert.Equal(t, decision.Profile != native && decision.Logging == off, decision.UsefulImprovement)
		assert.False(t, decision.ConditionalOnSuccess)
	}
	// A failed attempt stays in its denominator; a native-control change also stays visible.
	observations[0].Success = false
	observations[0].CleanupError = "retained cleanup failure"
	for i := range observations {
		if observations[i].Profile == native && observations[i].Arm == candidateArm {
			observations[i].WallNS = 900_000
		}
	}
	decisions, correct, resource, equivalent = evaluate(observations)
	assert.False(t, correct)
	assert.False(t, resource)
	assert.False(t, equivalent)
	assert.Equal(t, pairs-1, decisions[0].CompletePairs)
	assert.Equal(t, "incomplete", decisions[0].WallClassification)
	assert.True(t, decisions[0].ConditionalOnSuccess)
}

func TestResourceAndSequenceFailuresCannotBeAccepted(t *testing.T) {
	t.Parallel()
	obs := observation{Success: true, WallNS: 1, CPUNS: 1, MaxRSSKiB: 1}
	assert.True(t, validObservation(obs))
	obs.MaxRSSKiB = maxRSSKiB + 1
	assert.False(t, validObservation(obs))
	_, correct, resource, equivalent := evaluate(nil)
	assert.False(t, correct)
	assert.False(t, resource)
	assert.False(t, equivalent)
	assert.Error(t, run("", "", ""))
}

func TestGlobalPreparationFailureRetainsScheduleAndError(t *testing.T) {
	t.Parallel()
	output := filepath.Join(t.TempDir(), "evidence")
	assert.Error(t, run(filepath.Join(t.TempDir(), "missing-baseline"), ".", output))
	data, err := os.ReadFile(filepath.Join(output, "schedule.json"))
	require.NoError(t, err)
	var retained []scheduled
	require.NoError(t, json.Unmarshal(data, &retained))
	assert.Equal(t, schedule(), retained)
	_, err = os.Stat(filepath.Join(output, "failure.txt"))
	require.NoError(t, err)
	// The first failed cohort remains intact and cannot be overwritten by another run.
	assert.Error(t, run(".", ".", output))
}

func TestSourceRecheckRejectsDirtyAndChangedFrozenHeads(t *testing.T) {
	t.Parallel()
	dir, output := t.TempDir(), t.TempDir()
	_, err := command(dir, cleanEnv(), "git", "init", "--quiet")
	require.NoError(t, err)
	commit := func() {
		t.Helper()
		_, err := command(dir, cleanEnv(), "git", "-c", "user.name=Telemetry fixture", "-c", "user.email=fixture@example.invalid",
			"-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "test: source identity fixture")
		require.NoError(t, err)
	}
	commit()
	head, err := command(dir, cleanEnv(), "git", "rev-parse", "HEAD")
	require.NoError(t, err)
	require.NoError(t, saveJSON(filepath.Join(output, "freeze.json"), map[string]string{
		"baseline_commit": strings.TrimSpace(string(head)), "candidate_commit": strings.TrimSpace(string(head)),
	}))
	require.NoError(t, recheckSources(dir, dir, output, "clean"))
	changed := filepath.Join(dir, "changed")
	require.NoError(t, os.WriteFile(changed, []byte("untracked source change"), privateFile))
	assert.ErrorContains(t, recheckSources(dir, dir, output, "dirty"), "source changed or is dirty")
	require.NoError(t, os.Remove(changed))
	commit()
	assert.ErrorContains(t, recheckSources(dir, dir, output, "new-head"), "source changed or is dirty")
	data, err := os.ReadFile(filepath.Join(output, "source-check-new-head.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "source changed or is dirty")
}
