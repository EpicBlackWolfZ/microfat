package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const resourceFixtureWorker = "/known/worker"

func resourceTestConfig(t *testing.T) trialResourceConfig {
	t.Helper()
	return trialResourceConfig{UnitName: resourceUnitPrefix + "fixture" + resourceUnitSuffix,
		CPUQuotaPercent: 100, MemoryMaxBytes: 128 << 20, GOMAXPROCS: 1, GoMemLimitBytes: 112 << 20, TrialDir: t.TempDir()}
}

type resourceFixture struct {
	config        trialResourceConfig
	state         string
	values        map[string]string
	calls         [][]string
	startErr      error
	lateStartErr  error
	startOutput   []byte
	showErr       error
	readErr       error
	cleanupErr    error
	absentCleanup bool
	stdout        string
	stderr        string
	removeOutput  bool
	runningFirst  bool
	shows         int
	stdoutPath    string
	stderrPath    string
}

func newResourceFixture(t *testing.T) *resourceFixture {
	t.Helper()
	config := resourceTestConfig(t)
	return &resourceFixture{config: config, state: fixtureSuccess, stdout: "worker report", stderr: "worker diagnostics",
		values: map[string]string{
			"memory.max": strconv.FormatInt(config.MemoryMaxBytes, 10), "memory.swap.max": "0", "memory.peak": "67108864",
			"memory.events": "low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\n",
			"cpu.max":       "100000 100000", "cpu.stat": "usage_usec 12345\nnr_periods 8\nnr_throttled 3\nthrottled_usec 234\n",
		}}
}

func (fixture *resourceFixture) backend(t *testing.T) resourceBackend {
	t.Helper()
	return resourceBackend{cgroupRoot: t.TempDir(), command: fixture.command,
		read: func(path string, limit int64) ([]byte, error) {
			if fixture.readErr != nil {
				return nil, fixture.readErr
			}
			value, ok := fixture.values[filepath.Base(path)]
			if !ok || limit != resourceTelemetryLimit {
				return nil, errors.New("unexpected telemetry file or bound")
			}
			return []byte(value), nil
		}}
}

func (fixture *resourceFixture) command(ctx context.Context, command string, args ...string) ([]byte, error) {
	fixture.calls = append(fixture.calls, append([]string{command}, args...))
	if command == "systemd-run" {
		if fixture.startErr != nil {
			return fixture.startOutput, fixture.startErr
		}
		for _, arg := range args {
			if strings.HasPrefix(arg, "--unit=") {
				fixture.config.UnitName = strings.TrimPrefix(arg, "--unit=")
			}
			if path, found := strings.CutPrefix(arg, "--property=StandardOutput=append:"); found {
				fixture.stdoutPath = path
			}
			if path, found := strings.CutPrefix(arg, "--property=StandardError=append:"); found {
				fixture.stderrPath = path
			}
		}
		if err := os.WriteFile(fixture.stdoutPath, []byte(fixture.stdout), 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(fixture.stderrPath, []byte(fixture.stderr), 0o600); err != nil {
			return nil, err
		}
		return nil, fixture.lateStartErr
	}
	if len(args) > 1 && args[1] == "show" {
		fixture.shows++
		if fixture.showErr != nil {
			return nil, fixture.showErr
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if fixture.removeOutput {
			_ = os.Remove(fixture.stdoutPath)
		}
		if fixture.runningFirst && fixture.shows == 1 {
			return []byte("ActiveState=active\nSubState=running\n"), nil
		}
		group := "/user.slice/" + fixture.config.UnitName
		code, status, result := "1", "0", fixtureSuccess
		switch fixture.state {
		case fixtureFailure:
			status, result = "42", "exit-code"
		case fixtureOOM:
			code, status, result = "2", "9", "oom-kill"
		case "oom-no-keeper":
			code, status, result, group = "2", "9", "oom-kill", ""
		case "not-executed":
			code, result = "0", "resources"
		case "malformed":
			return []byte("malformed"), nil
		case "vanished":
			return []byte("LoadState=not-found\nActiveState=inactive\nSubState=dead\n"), nil
		}
		return fmt.Appendf(nil, "ActiveState=deactivating\nSubState=stop-post\nExecMainCode=%s\nExecMainStatus=%s\n"+
			"Result=%s\nControlGroup=%s\nMemoryPeak=987654\nCPUUsageNSec=12345000\n", code, status, result, group), nil
	}
	if fixture.absentCleanup {
		return []byte("Unit was not loaded."), errors.New("unit absent")
	}
	return nil, fixture.cleanupErr
}

func TestResourceTrialPreservesWorkerOutcomeAndControls(t *testing.T) {
	t.Parallel()
	for _, state := range []string{fixtureSuccess, fixtureFailure, fixtureOOM, "oom-no-keeper"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			fixture := newResourceFixture(t)
			fixture.state, fixture.runningFirst = state, true
			if state == fixtureOOM {
				fixture.values["memory.events"] = "oom 2\noom_kill 1\n"
			}
			result, err := runResourceTrial(context.Background(), fixture.config, resourceFixtureWorker,
				[]string{"--worker", "literal;$(not-a-shell)"}, []string{"GOMAXPROCS=99", "GOMEMLIMIT=1B", "PLAIN=value"}, fixture.backend(t))
			require.NoError(t, err)
			assert.Equal(t, fixture.stdout, result.Stdout)
			assert.Equal(t, fixture.stderr, result.Stderr)
			assert.Greater(t, result.WallNS, int64(0))
			assert.GreaterOrEqual(t, fixture.shows, 2)
			assert.Equal(t, uint64(12345), result.CPUUsageUsec)
			assert.Contains(t, fixture.calls[0], "--setenv=GOMAXPROCS=1")
			assert.Contains(t, fixture.calls[0], "--setenv=GOMEMLIMIT=117440512B")
			assert.NotContains(t, fixture.calls[0], "--setenv=GOMAXPROCS=99")
			assert.Equal(t, []string{"--", resourceFixtureWorker, "--worker", "literal;$(not-a-shell)"}, fixture.calls[0][len(fixture.calls[0])-4:])
			assert.Equal(t, fixture.config.UnitName, fixture.calls[len(fixture.calls)-1][len(fixture.calls[len(fixture.calls)-1])-1])
			_, err = os.Stat(filepath.Dir(fixture.stdoutPath))
			require.True(t, os.IsNotExist(err), "only the owned trial subdirectory should be removed")
			_, err = os.Stat(fixture.config.TrialDir)
			require.NoError(t, err, "caller-owned parent must survive cleanup")
			switch state {
			case fixtureSuccess:
				assert.Zero(t, result.ExitCode)
				assert.False(t, result.OOMKilled)
			case fixtureFailure:
				assert.Equal(t, 42, result.ExitCode)
			case fixtureOOM, "oom-no-keeper":
				assert.Equal(t, 137, result.ExitCode)
				assert.Equal(t, 9, result.Signal)
				assert.True(t, result.OOMKilled)
			}
			if state == "oom-no-keeper" {
				assert.False(t, result.TelemetryAvailable)
				assert.NotEmpty(t, result.TelemetryError)
				assert.Equal(t, uint64(987654), result.MemoryPeakBytes)
				assert.Zero(t, result.OOMKills, "missing raw counters must not be invented")
				assert.Zero(t, result.ActualCG.MemoryMaxBytes)
			} else {
				assert.True(t, result.TelemetryAvailable)
				assert.Equal(t, uint64(fixture.config.MemoryMaxBytes), result.ActualCG.MemoryMaxBytes)
				assert.Zero(t, result.ActualCG.MemorySwapMaxBytes)
				assert.Equal(t, uint64(3), result.CPUThrottledPeriods)
				assert.Equal(t, uint64(234), result.CPUThrottledUsec)
			}
		})
	}
}

func TestResourceTrialRejectsUnverifiedTelemetryAndSetup(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*resourceFixture){
		"start":           func(f *resourceFixture) { f.startErr = errors.New("start failed") },
		"missing command": func(f *resourceFixture) { f.startErr = exec.ErrNotFound },
		"missing bus": func(f *resourceFixture) {
			f.startErr = errors.New("bus")
			f.startOutput = []byte("Failed to connect to bus")
		},
		"state":             func(f *resourceFixture) { f.showErr = errors.New("state failed") },
		"malformed state":   func(f *resourceFixture) { f.state = "malformed" },
		"vanished unit":     func(f *resourceFixture) { f.state = "vanished" },
		"worker never ran":  func(f *resourceFixture) { f.state = "not-executed" },
		"missing output":    func(f *resourceFixture) { f.removeOutput = true },
		"missing telemetry": func(f *resourceFixture) { f.readErr = errors.New("read failed") },
		"memory mismatch":   func(f *resourceFixture) { f.values["memory.max"] = "1" },
		"swap mismatch":     func(f *resourceFixture) { f.values["memory.swap.max"] = "1" },
		"CPU mismatch":      func(f *resourceFixture) { f.values["cpu.max"] = "200000 100000" },
		"invalid controls":  func(f *resourceFixture) { f.values["memory.max"] = "max" },
		"invalid peak":      func(f *resourceFixture) { f.values["memory.peak"] = "max" },
		"invalid events":    func(f *resourceFixture) { f.values["memory.events"] = "oom_kill bad" },
		"invalid CPU":       func(f *resourceFixture) { f.values["cpu.stat"] = "usage_usec bad" },
		"missing OOM count": func(f *resourceFixture) { f.values["memory.events"] = "oom 1" },
		"missing CPU count": func(f *resourceFixture) { f.values["cpu.stat"] = "usage_usec 1" },
		"cleanup":           func(f *resourceFixture) { f.cleanupErr = errors.New("cleanup failed") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newResourceFixture(t)
			mutate(fixture)
			result, err := runResourceTrial(context.Background(), fixture.config, resourceFixtureWorker, nil, nil, fixture.backend(t))
			require.Error(t, err)
			if name == "missing command" || name == "missing bus" {
				require.ErrorIs(t, err, ErrResourceUnavailable)
			}
			if errors.Is(fixture.startErr, exec.ErrNotFound) {
				assert.Len(t, fixture.calls, 1, "never clean up a unit that failed creation")
			} else {
				assert.Greater(t, len(fixture.calls), 1)
			}
			if name == "cleanup" {
				require.ErrorIs(t, err, ErrResourceCleanup)
				assert.NotEmpty(t, result.CleanupError)
				assert.Equal(t, filepath.Dir(fixture.stdoutPath), result.RetainedLogDir)
				_, statErr := os.Stat(result.RetainedLogDir)
				require.NoError(t, statErr, "uncertain unit termination must preserve logs")
			}
		})
	}
	fixture := newResourceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runResourceTrial(ctx, fixture.config, resourceFixtureWorker, nil, nil, fixture.backend(t))
	require.ErrorIs(t, err, ErrResourceTimeout)
	fixture = newResourceFixture(t)
	fixture.config.TrialDir = filepath.Join(t.TempDir(), fixtureMissing)
	_, err = runResourceTrial(context.Background(), fixture.config, resourceFixtureWorker, nil, nil, fixture.backend(t))
	require.ErrorIs(t, err, ErrResourceSetup)
}

func TestResourceStartTimeoutStillCleansFreshOwnedUnit(t *testing.T) {
	t.Parallel()
	for _, cleanupFails := range []bool{false, true} {
		fixture := newResourceFixture(t)
		fixture.lateStartErr = context.DeadlineExceeded // Unit creation happened before the client lost its reply.
		if cleanupFails {
			fixture.cleanupErr = errors.New("control bus unavailable")
		}
		requestedName := fixture.config.UnitName
		result, err := runResourceTrial(context.Background(), fixture.config, resourceFixtureWorker, nil, nil, fixture.backend(t))
		require.ErrorIs(t, err, ErrResourceTimeout)
		assert.NotEqual(t, requestedName, result.UnitName, "every cleanup target must have an internally fresh random suffix")
		assert.Len(t, fixture.calls, 4, "creation failure still attempts owned kill, stop and reset")
		for _, command := range fixture.calls[1:] {
			assert.Equal(t, result.UnitName, command[len(command)-1])
		}
		if cleanupFails {
			require.ErrorIs(t, err, ErrResourceCleanup)
			data, readErr := os.ReadFile(filepath.Join(result.RetainedLogDir, "stdout"))
			require.NoError(t, readErr)
			assert.Equal(t, fixture.stdout, string(data))
		} else {
			assert.Empty(t, result.RetainedLogDir)
			_, statErr := os.Stat(filepath.Dir(fixture.stdoutPath))
			require.True(t, os.IsNotExist(statErr))
		}
	}
	fixture := newResourceFixture(t)
	fixture.startErr, fixture.absentCleanup = errors.New("unit could not start"), true
	result, err := runResourceTrial(context.Background(), fixture.config, resourceFixtureWorker, nil, nil, fixture.backend(t))
	require.ErrorIs(t, err, ErrResourceSetup)
	assert.NotErrorIs(t, err, ErrResourceCleanup)
	assert.Empty(t, result.RetainedLogDir)
}

func TestResourceConfigBoundsAndLiteralArguments(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*trialResourceConfig){
		"memory below floor":       func(c *trialResourceConfig) { c.MemoryMaxBytes = 1 },
		"memory above cap":         func(c *trialResourceConfig) { c.MemoryMaxBytes = resourceMaximumMemory + 1 },
		"CPU zero":                 func(c *trialResourceConfig) { c.CPUQuotaPercent = 0 },
		"CPU cap":                  func(c *trialResourceConfig) { c.CPUQuotaPercent = resourceMaximumCPU + 1 },
		"procs zero":               func(c *trialResourceConfig) { c.GOMAXPROCS = 0 },
		"procs cap":                func(c *trialResourceConfig) { c.GOMAXPROCS = resourceMaximumProcs + 1 },
		"Go limit zero":            func(c *trialResourceConfig) { c.GoMemLimitBytes = 0 },
		"Go limit above hard cap":  func(c *trialResourceConfig) { c.GoMemLimitBytes = c.MemoryMaxBytes + 1 },
		"foreign unit":             func(c *trialResourceConfig) { c.UnitName = "foreign.service" },
		"bad suffix":               func(c *trialResourceConfig) { c.UnitName = resourceUnitPrefix + "bad.scope" },
		"bad unit character":       func(c *trialResourceConfig) { c.UnitName = resourceUnitPrefix + "bad/name.service" },
		"relative trial directory": func(c *trialResourceConfig) { c.TrialDir = fixtureRelative },
		"unit name cap": func(c *trialResourceConfig) {
			c.UnitName = resourceUnitPrefix + strings.Repeat("a", resourceMaximumUnit) + resourceUnitSuffix
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := resourceTestConfig(t)
			mutate(&config)
			require.ErrorIs(t, validateResourceConfig(config, resourceFixtureWorker, nil, nil), ErrResourceConfig)
		})
	}
	config := resourceTestConfig(t)
	for _, entry := range []struct {
		command   string
		args, env []string
	}{
		{fixtureRelative, nil, nil}, {resourceFixtureWorker, make([]string, resourceMaximumArgs+1), nil},
		{resourceFixtureWorker, nil, make([]string, resourceMaximumArgs+1)},
		{resourceFixtureWorker, []string{strings.Repeat("x", resourceTelemetryLimit+1)}, nil},
		{resourceFixtureWorker, []string{"contains\x00nul"}, nil}, {resourceFixtureWorker, nil, []string{"NO-EQUALS"}},
		{resourceFixtureWorker, nil, []string{"=empty"}}, {resourceFixtureWorker, nil, []string{"BAD\nKEY=value"}},
	} {
		require.ErrorIs(t, validateResourceConfig(config, entry.command, entry.args, entry.env), ErrResourceConfig)
	}
	fixture := newResourceFixture(t)
	fixture.config.UnitName = ""
	result, err := runResourceTrial(context.Background(), fixture.config, resourceFixtureWorker, nil, nil, fixture.backend(t))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(result.UnitName, resourceUnitPrefix))
	config.MemoryMaxBytes = 0
	_, err = runResourceTrial(context.Background(), config, resourceFixtureWorker, nil, nil, resourceBackend{})
	require.ErrorIs(t, err, ErrResourceConfig)
}

func TestResourceParsersFiniteAndNonduplicated(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", fixtureMissing, "key=value\nkey=again"} {
		_, err := parseResourceProperties([]byte(input))
		require.ErrorIs(t, err, ErrResourceTelemetry)
	}
	for _, input := range []string{"", fixtureMissing, "key 1\nkey 2", "key -1", "key 18446744073709551616"} {
		_, err := parseResourceCounters([]byte(input))
		require.ErrorIs(t, err, ErrResourceTelemetry)
	}
	for _, input := range []struct{ memory, swap, cpu string }{
		{"max", "0", "1 1"}, {"1", "max", "1 1"}, {"1", "0", "max 100000"},
		{"1", "0", "1"}, {"1", "0", "0 1"}, {"1", "0", "1 0"},
		{"1", "0", "400000001 100000"}, {"1", "0", "1 1000001"},
	} {
		_, err := parseResourceControls([]byte(input.memory), []byte(input.swap), []byte(input.cpu))
		require.ErrorIs(t, err, ErrResourceTelemetry)
	}
	fixture := newResourceFixture(t)
	for _, group := range []string{"", fixtureRelative, "/foreign.service", "/x/../" + fixture.config.UnitName} {
		err := collectResourceTelemetry(&resourceTrialResult{}, group, fixture.config, fixture.backend(t))
		require.ErrorIs(t, err, ErrResourceTelemetry)
	}
	result := resourceTrialResult{ExitCode: -1}
	applyResourceStatus(&result, map[string]string{"ExecMainCode": "3", "ExecMainStatus": "11", "Result": "core-dump"})
	assert.Equal(t, 139, result.ExitCode)
	assert.Equal(t, 11, result.Signal)
}

func TestResourceBoundedFilesAndControlCommands(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, fixtureBytes)
	require.NoError(t, os.WriteFile(path, []byte("fixture"), 0o600))
	data, err := resourceRead(path, 7)
	require.NoError(t, err)
	assert.Equal(t, "fixture", string(data))
	_, err = resourceRead(path, 6)
	require.ErrorIs(t, err, ErrResourceTelemetry)
	for _, path := range []string{directory, filepath.Join(directory, fixtureMissing)} {
		_, err := resourceRead(path, 100)
		require.Error(t, err)
		_, _, err = readResourceOutput(path)
		require.Error(t, err)
	}
	fixture := newResourceFixture(t)
	fixture.stdout = strings.Repeat("a", resourceOutputLimit+1)
	fixture.stderr = strings.Repeat("b", resourceOutputLimit+1)
	result, err := runResourceTrial(context.Background(), fixture.config, resourceFixtureWorker, nil, nil, fixture.backend(t))
	require.NoError(t, err)
	assert.True(t, result.OutputTruncated)
	assert.Len(t, result.Stdout, resourceOutputLimit)
	assert.Len(t, result.Stderr, resourceOutputLimit)
	output, err := resourceCommand(context.Background(), "/usr/bin/printf", "%s", "literal;$(not-a-shell)")
	require.NoError(t, err)
	assert.Equal(t, "literal;$(not-a-shell)", string(output))
	_, err = resourceCommand(context.Background(), "/nonexistent-control-command")
	require.Error(t, err)
	writer := resourceLimitedWriter{remaining: 3}
	n, err := writer.Write([]byte("abcdef"))
	require.NoError(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, "abc", string(writer.data))
}

func TestResourceKernelFunctional(t *testing.T) {
	if os.Getenv("MICROFAT_POOL_RESOURCE_TESTS") != "required" {
		t.Skip("set MICROFAT_POOL_RESOURCE_TESTS=required to require fresh systemd user cgroup controls")
	}
	config := resourceTestConfig(t)
	config.UnitName = "" // Every real probe receives an unguessable fresh unit.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := RunResourceTrial(ctx, config, "/usr/bin/true", nil, nil)
	require.NoError(t, err)
	assert.Zero(t, result.ExitCode)
	assert.True(t, result.TelemetryAvailable)
	assert.Equal(t, uint64(config.MemoryMaxBytes), result.ActualCG.MemoryMaxBytes)
	assert.Greater(t, result.MemoryPeakBytes, uint64(0))
	t.Logf("Successful control: unit=%s exit=%d cap=%d swap=%d CPU=%d/%d peak=%d",
		result.UnitName, result.ExitCode, result.ActualCG.MemoryMaxBytes, result.ActualCG.MemorySwapMaxBytes,
		result.ActualCG.CPUQuotaUsec, result.ActualCG.CPUPeriodUsec, result.MemoryPeakBytes)
	config.CPUQuotaPercent, config.GOMAXPROCS = resourceMaximumCPU, 4
	config.MemoryMaxBytes, config.GoMemLimitBytes = 256<<20, 224<<20
	result, err = RunResourceTrial(ctx, config, "/usr/bin/false", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, result.ExitCode)
	assert.False(t, result.OOMKilled)
	assert.Equal(t, uint64(config.MemoryMaxBytes), result.ActualCG.MemoryMaxBytes)
	assert.Equal(t, uint64(400000), result.ActualCG.CPUQuotaUsec)
	assert.Equal(t, uint64(100000), result.ActualCG.CPUPeriodUsec)
	t.Logf("Ordinary failure retained: unit=%s exit=%d OOM=%t cap=%d CPU=%d/%d",
		result.UnitName, result.ExitCode, result.OOMKilled, result.ActualCG.MemoryMaxBytes,
		result.ActualCG.CPUQuotaUsec, result.ActualCG.CPUPeriodUsec)
	self, err := os.Executable()
	require.NoError(t, err)
	config.CPUQuotaPercent, config.GOMAXPROCS = 100, 1
	config.MemoryMaxBytes, config.GoMemLimitBytes = 64<<20, 56<<20
	result, err = RunResourceTrial(ctx, config, self, []string{"-test.run=^TestResourceOOMHelper$"},
		[]string{"MICROFAT_RESOURCE_OOM_HELPER=1"})
	require.NoError(t, err)
	assert.NotZero(t, result.ExitCode)
	assert.True(t, result.OOMKilled)
	assert.Greater(t, result.OOMKills, uint64(0))
	assert.True(t, result.TelemetryAvailable)
	assert.Contains(t, result.Stdout, "resource-oom-touching-pages")
	t.Logf("Hard-cap OOM retained: unit=%s exit=%d signal=%d oom_kills=%d cap=%d peak=%d CPU=%d us marker=%q",
		result.UnitName, result.ExitCode, result.Signal, result.OOMKills, result.ActualCG.MemoryMaxBytes,
		result.MemoryPeakBytes, result.CPUUsageUsec, "resource-oom-touching-pages")
}

func TestResourceOOMHelper(t *testing.T) {
	if os.Getenv("MICROFAT_RESOURCE_OOM_HELPER") != "1" {
		return
	}
	_, err := fmt.Fprintln(os.Stdout, "resource-oom-touching-pages")
	require.NoError(t, err)
	// Anonymous mmap is outside GOMEMLIMIT accounting. Touching every page must
	// hit the real kernel cap, rather than being an admission-only rejection.
	data, err := unix.Mmap(-1, 0, 256<<20, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON)
	require.NoError(t, err)
	for index := 0; index < len(data); index += os.Getpagesize() {
		data[index] = 1
	}
	_ = unix.Munmap(data)
	t.Fatal("forced OOM helper unexpectedly survived the hard memory cap")
}
