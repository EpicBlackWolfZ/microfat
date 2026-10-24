//go:build linux

package main

import (
	"context"
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
	"golang.org/x/sys/unix"
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

func TestLeaderExitTerminatesOwnedDescendants(t *testing.T) {
	requirePidfdObservation(t)
	t.Parallel()
	for _, caller := range []string{"execute", "preparation"} {
		for _, redirected := range []bool{false, true} {
			name := "inherited-pipes"
			background := "sleep 60 &"
			if redirected {
				name, background = "redirected-pipes", "sleep 60 >/dev/null 2>&1 &"
			}
			t.Run(caller+"/"+name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				pidFile, releaseFile := filepath.Join(dir, "child.pid"), filepath.Join(dir, "release")
				script := background + ` child=$!; printf '%s' "$child" > "$1"; while [ ! -e "$2" ]; do sleep 0.01; done`
				args := []string{"-c", script, "owned-exit-test", pidFile, releaseFile}
				result := make(chan error, 1)
				done := make(chan struct{})
				go func() {
					defer close(done)
					if caller == "execute" {
						obs := execute("/bin/sh", args, cleanEnv(), 3*time.Second)
						if !obs.Success {
							result <- errors.New(obs.Error)
							return
						}
						result <- nil
						return
					}
					_, err := command(studyConfig{}, "", "/bin/sh", args...)
					result <- err
				}()
				t.Cleanup(func() {
					require.NoError(t, os.WriteFile(releaseFile, nil, privateFile))
					select {
					case <-done:
					case <-time.After(metadataTimeout + time.Second):
						t.Error("owned test command did not finish within its timeout")
					}
				})
				var pid int
				require.Eventually(t, func() bool {
					data, err := os.ReadFile(pidFile)
					if err != nil {
						return false
					}
					pid, err = strconv.Atoi(string(data))
					return err == nil
				}, time.Second, 10*time.Millisecond)
				// Open a stable handle while the parent still owns the live descendant.
				// Even a failing regression test must never signal a reused PID.
				pidfd, err := unix.PidfdOpen(pid, 0)
				require.NoError(t, err)
				t.Cleanup(func() {
					signalErr := unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
					require.True(t, signalErr == nil || errors.Is(signalErr, unix.ESRCH), "cleanup owned descendant: %v", signalErr)
					require.NoError(t, unix.Close(pidfd))
				})
				require.NoError(t, os.WriteFile(releaseFile, nil, privateFile))
				require.NoError(t, <-result, "leader exit must not wait for inherited pipes to hit WaitDelay")
				require.Eventually(t, func() bool {
					fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
					n, pollErr := unix.Poll(fds, 0)
					return pollErr == nil && n == 1 && fds[0].Revents&unix.POLLIN != 0
				}, time.Second, 10*time.Millisecond, "owned descendant survived leader exit")
			})
		}
	}
}

func requirePidfdObservation(t *testing.T) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if (errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES)) &&
			os.Getenv("MICROFAT_CODEC_FIXTURES") != "required" {
			t.Skipf("pidfd observation unavailable on this host: %v", err)
		}
		require.NoError(t, err)
	}
	pidfd, err := unix.PidfdOpen(os.Getpid(), 0)
	check(err)
	defer func() { require.NoError(t, unix.Close(pidfd)) }()
	check(unix.PidfdSendSignal(pidfd, 0, nil, 0))
}

func TestCancellationDrainsBeforeLeaderReap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	readyFile, releaseFile := filepath.Join(dir, "ready"), filepath.Join(dir, "release")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := ownedCommand(ctx, "/bin/sh", "-c", `printf ready > "$1"; while [ ! -e "$2" ]; do sleep 0.01; done`,
		"owned-cancellation-test", readyFile, releaseFile)
	cmd.Env = cleanEnv()
	entered, allowCancel := make(chan int, 1), make(chan struct{}, 1)
	cmd.Cancel = func() error {
		entered <- cmd.Process.Pid
		<-allowCancel
		return os.ErrProcessDone
	}
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() { defer close(done); result <- runOwned(cmd) }()
	t.Cleanup(func() {
		cancel()
		select {
		case allowCancel <- struct{}{}:
		default:
		}
		require.NoError(t, os.WriteFile(releaseFile, nil, privateFile))
		select {
		case <-done:
		case <-time.After(metadataTimeout):
			t.Error("owned cancellation test did not finish")
		}
	})
	require.Eventually(t, func() bool {
		info, err := os.Stat(readyFile)
		return err == nil && info.Size() > 0
	}, time.Second, 10*time.Millisecond)
	cancel()
	var pid int
	select {
	case pid = <-entered:
	case <-time.After(time.Second):
		t.Fatal("custom cancellation callback did not start")
	}
	require.NoError(t, os.WriteFile(releaseFile, nil, privateFile))
	observe := func() (unix.Siginfo, error) {
		var info unix.Siginfo
		for {
			err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT|unix.WNOHANG, nil)
			if !errors.Is(err, unix.EINTR) {
				return info, err
			}
		}
	}
	require.Eventually(t, func() bool {
		info, err := observe()
		return err == nil && info.Signo == int32(unix.SIGCHLD)
	}, time.Second, time.Millisecond, "leader must exit without being reaped while cancellation is in flight")
	require.Never(t, func() bool {
		_, err := observe()
		return err != nil
	}, 100*time.Millisecond, time.Millisecond, "leader was reaped before the cancellation callback drained")
	allowCancel <- struct{}{}
	require.NoError(t, <-result)
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
