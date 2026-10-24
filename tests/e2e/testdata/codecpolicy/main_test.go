//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSchedule(t *testing.T) {
	t.Parallel()
	plan := schedule()
	require.Equal(t, 264+11880, len(plan))
	require.Equal(t, plan, schedule())
	counts := map[cell]int{}
	rounds := map[cell]map[int]bool{}
	packCount, nativeCount, fatCount := 0, 0, 0
	for i, visit := range plan {
		require.Equal(t, i, visit.Sequence)
		c := cellOf(visit)
		counts[c]++
		if rounds[c] == nil {
			rounds[c] = map[int]bool{}
		}
		require.False(t, rounds[c][visit.Round], "duplicate scheduled cell/round")
		rounds[c][visit.Round] = true
		switch {
		case visit.Phase == "pack":
			packCount++
		case visit.Setting == nativeSetting:
			nativeCount++
		default:
			fatCount++
		}
	}
	require.Equal(t, 264, packCount)
	require.Equal(t, 1320, nativeCount)
	require.Equal(t, 10560, fatCount)
	for c, count := range counts {
		want := launchRounds
		if c.Phase == "pack" {
			want = packRounds
		}
		require.Equal(t, want, count, "cell %v", c)
	}
	require.NotEqual(t, rotated(settings(), 0), rotated(settings(), 1))
	for _, setting := range settings() {
		require.Empty(t, setting.Level, "raw trainer compression level must remain empty")
	}
}

func TestCleanEnvironment(t *testing.T) {
	for _, item := range []string{"MICROFAT_LOG=json", "GOGC=off", "GOMEMLIMIT=1MiB", "GODEBUG=bad", "GOMAXPROCS=99",
		"GOAMD64=v4", "GOARM64=v9.5", "GOFLAGS=-race", "SOURCE_DATE_EPOCH=5", "LD_PRELOAD=bad", "XDG_CACHE_HOME=/bad"} {
		key, value, _ := strings.Cut(item, "=")
		t.Setenv(key, value)
	}
	env := map[string]string{}
	for _, item := range cleanEnv("MICROFAT_AUTOTUNE=0") {
		key, value, _ := strings.Cut(item, "=")
		_, exists := env[key]
		require.False(t, exists, "duplicate %s", key)
		env[key] = value
	}
	for _, key := range []string{"MICROFAT_LOG", "GOGC", "GOMEMLIMIT", "GODEBUG", "GOAMD64", "GOARM64", "GOFLAGS",
		"LD_PRELOAD", "XDG_CACHE_HOME"} {
		_, exists := env[key]
		require.False(t, exists, "inherited %s", key)
	}
	require.Equal(t, "4", env["GOMAXPROCS"])
	require.Equal(t, "0", env["SOURCE_DATE_EPOCH"])
	require.Equal(t, "0", env["MICROFAT_AUTOTUNE"])
}

func TestPairedStatistics(t *testing.T) {
	t.Parallel()
	require.Equal(t, 2.5, median([]float64{4, 1, 3, 2}))
	require.Equal(t, distribution{4, 2.5, 1, 4}, describe([]float64{4, 1, 3, 2}))
	pairs := [][2]float64{{50, 100}, {100, 200}, {150, 300}, {200, 400}}
	ratio, saving := pairedBootstrap(pairs)
	require.Equal(t, interval{0.5, 0.5, 0.5}, ratio)
	require.Equal(t, 125.0, saving.Median)
	ratio2, saving2 := pairedBootstrap(pairs)
	require.Equal(t, ratio, ratio2)
	require.Equal(t, saving, saving2)
	require.False(t, ratioDefined([][2]float64{{0, 0}}))
	_, zeroSaving := pairedBootstrap([][2]float64{{1, 0}})
	require.Equal(t, -1.0, zeroSaving.Median)
}

func smallCohort() ([]step, []observation) {
	var plan []step
	var observations []observation
	for round := range 2 {
		for _, name := range []string{balancedProfile, "size"} {
			visit := step{group: group{"task", "stripped"}, Phase: "launch", Setting: name, Stub: fullStub,
				Mode: memfdMode, Round: round, Sequence: len(plan)}
			plan = append(plan, visit)
			observations = append(observations, observation{step: visit, Success: true, ResourcesObserved: true,
				WallNS: 100, CPUNS: 10, MaxRSSKiB: 20})
		}
	}
	return plan, observations
}

func TestTimeoutTerminatesOwnedDescendant(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	start := time.Now()
	args := []string{"-c", `sleep 60 & child=$!; printf '%s' "$child" > "$1"; wait`, "owned-timeout-test", pidFile}
	obs := execute("/bin/sh", args, cleanEnv(), 250*time.Millisecond)
	require.False(t, obs.Success)
	require.Contains(t, obs.Error, "context deadline exceeded")
	require.Less(t, time.Since(start), 3*time.Second)
	pidData, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	pid, err := strconv.Atoi(string(pidData))
	require.NoError(t, err)
	state, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) {
		return
	}
	require.NoError(t, err)
	fields := strings.Fields(string(state)[strings.LastIndex(string(state), ")")+1:])
	require.NotEmpty(t, fields)
	require.Contains(t, []string{"Z", "X"}, fields[0], "owned descendant must be terminated")
}

func TestFailuresNeverQualify(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func([]observation) []observation
	}{
		{"missing", func(v []observation) []observation { return v[:len(v)-1] }},
		{"failed", func(v []observation) []observation { v[0].Success = false; v[0].Error = "timeout"; return v }},
		{"resource", func(v []observation) []observation { v[0].MaxRSSKiB = launchRSSKiB + 1; return v }},
		{"missing-wait4", func(v []observation) []observation { v[0].ResourcesObserved = false; return v }},
		{"duplicate-round", func(v []observation) []observation { v[2].Round = 0; return v }},
		{"order", func(v []observation) []observation { v[0], v[1] = v[1], v[0]; return v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan, observations := smallCohort()
			observations = tc.mutate(observations)
			summary, pass := summarize(plan, observations)
			require.False(t, pass)
			require.Equal(t, len(observations), summary["recorded_attempts"])
			require.Equal(t, "retain current defaults", summary["default_policy_decision"])
		})
	}
	plan, observations := smallCohort()
	observations[0].CPUNS, observations[0].MaxRSSKiB = 0, 0
	_, pass := summarize(plan, observations)
	require.True(t, pass, "observed kernel zeros are not missing wait4 data")
}

func TestPrivateCacheValidation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "selected")
	require.NoError(t, os.WriteFile(path, []byte("selected payload"), privateDir))
	id, err := identify(path)
	require.NoError(t, err)
	require.NoError(t, os.Rename(path, filepath.Join(dir, id.SHA256)))
	v := variant{SHA256: id.SHA256, Bytes: id.Bytes}
	require.NoError(t, checkCache(dir, v, false))
	require.Error(t, checkCache(dir, v, true))
	require.NoError(t, os.Chmod(filepath.Join(dir, id.SHA256), 0o644))
	require.Error(t, checkCache(dir, v, false))
}

func TestFrozenInputRecheck(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "frozen")
	require.NoError(t, os.WriteFile(path, []byte("before"), privateFile))
	id, err := identify(path)
	require.NoError(t, err)
	require.NoError(t, saveJSON(filepath.Join(dir, "freeze.json"), map[string]any{"identities": []identity{id}}))
	require.NoError(t, recheckInputs(dir, "same"))
	require.NoError(t, os.WriteFile(path, []byte("changed"), privateFile))
	require.Error(t, recheckInputs(dir, "changed"))
}

func TestReadinessCommandsUseFixedLocalDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, prepareVersionDB(dir))
	require.Equal(t, []string{"-db", "file://" + filepath.Join(dir, "version-db"), "-version"}, versionArgs("govulncheck", dir))
	require.Equal(t, []string{"--version"}, versionArgs("task", dir))
	require.Equal(t, []string{"-version"}, versionArgs("actionlint", dir))
	require.Equal(t, []string{"version"}, versionArgs("fortio", dir))
	require.Empty(t, versionArgs(boundaryFamily, dir))
	data, err := os.ReadFile(filepath.Join(dir, "version-db/index/db.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), "1970-01-01T00:00:00Z")
	require.True(t, reflect.DeepEqual(schedule(), schedule()))
}
