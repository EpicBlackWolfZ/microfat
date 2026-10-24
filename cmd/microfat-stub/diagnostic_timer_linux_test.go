//go:build linux

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/require"
)

const (
	diagnosticFalseValue = "false"
	diagnosticJSONValue  = "json"
	diagnosticMemoryEnv  = "GOMEMLIMIT=1000B"
	diagnosticProcsEnv   = "GOMAXPROCS=4"
)

func TestRejectedC1DiagnosticsEnabled(t *testing.T) {
	// Launcher environment is process-wide. Keep this matrix serial in both profiles.
	for _, tc := range []struct {
		name, debug, log string
		want             bool
	}{
		{"unset", "", "", false},
		{"debug zero", "0", "", false},
		{"debug false", diagnosticFalseValue, "", false},
		{"debug false case", "FaLsE", "", false},
		{"debug one", "1", "", true},
		{"debug true case", "TrUe", "", true},
		{"debug arbitrary text", "verbose", "", true},
		{"debug whitespace", " ", "", true},
		{"debug untrimmed false", "false ", "", true},
		{diagnosticJSONValue, "", diagnosticJSONValue, true},
		{"json case", "0", "JsOn", true},
		{"json overrides false", diagnosticFalseValue, diagnosticJSONValue, true},
		{"text overrides zero", "0", "text", true},
		{"log zero is text", "", "0", true},
		{"log false is text", "", diagnosticFalseValue, true},
		{"log arbitrary text", "", "verbose", true},
		{"log whitespace is text", "", " ", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(format.EnvDebug, tc.debug)
			t.Setenv(format.EnvLog, tc.log)
			require.Equal(t, tc.want, c1DiagnosticsEnabled())
		})
	}
}

func TestRejectedC1DiagnosticTimer(t *testing.T) {
	t.Parallel()
	start := c1StartDiagnosticTimer(false)
	require.True(t, start.IsZero())
	require.Zero(t, c1DiagnosticElapsed(start))

	before := time.Now()
	start = c1StartDiagnosticTimer(true)
	after := time.Now()
	require.False(t, start.IsZero())
	require.False(t, start.Before(before))
	require.False(t, start.After(after))
	require.GreaterOrEqual(t, c1DiagnosticElapsed(start), time.Duration(0))
}

func TestRejectedC1DisabledDiagnosticsDoNotFormat(t *testing.T) {
	t.Setenv(format.EnvDebug, diagnosticFalseValue)
	t.Setenv(format.EnvLog, "")
	// A nil entry is safe only if disabled diagnostics return before formatting.
	allocations := testing.AllocsPerRun(100, func() {
		start := c1StartDiagnosticTimer(c1DiagnosticsEnabled())
		logDiagnostics(launchDiagnostics{}, nil, format.ExecModeMemfd, microarch.Info{}, microarch.PolicyResult{}, nil, nil,
			c1DiagnosticElapsed(start), c1DiagnosticElapsed(start))
	})
	require.Zero(t, allocations, "the disabled diagnostics probe must not allocate")
}

func TestLaunchDiagnosticsModeSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, debug, log string
		want             diagnosticMode
	}{
		{"unset", "", "", diagnosticOff},
		{"zero", "0", "", diagnosticOff},
		{"mixed false", "FaLsE", "", diagnosticOff},
		{"debug true", "TrUe", "", diagnosticText},
		{"arbitrary debug", "verbose", "", diagnosticText},
		{"untrimmed false", "false ", "", diagnosticText},
		{"false log", "0", diagnosticFalseValue, diagnosticText},
		{"whitespace log", "", " ", diagnosticText},
		{"json precedence", "1", "JsOn", diagnosticJSON},
		{"json with false debug", diagnosticFalseValue, diagnosticJSONValue, diagnosticJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(format.EnvDebug, tc.debug)
			t.Setenv(format.EnvLog, tc.log)
			before := time.Now()
			diagnostics := newLaunchDiagnostics()
			after := time.Now()
			require.Equal(t, tc.want, diagnostics.mode)
			if tc.want == diagnosticOff {
				require.True(t, diagnostics.start.IsZero())
				require.True(t, diagnostics.startTimer().IsZero())
				require.Zero(t, diagnostics.elapsed(before))
				require.Zero(t, diagnostics.totalElapsed())
			} else {
				require.False(t, diagnostics.start.IsZero())
				require.False(t, diagnostics.start.Before(before))
				require.False(t, diagnostics.start.After(after))
				timer := diagnostics.startTimer()
				require.False(t, timer.IsZero())
				require.GreaterOrEqual(t, diagnostics.elapsed(timer), time.Duration(0))
				require.GreaterOrEqual(t, diagnostics.totalElapsed(), time.Duration(0))
				require.Zero(t, diagnostics.elapsed(time.Time{}))
			}

			// A later environment change cannot reclassify the same launch, and
			// the next launch must observe the new settings rather than a cache.
			nextMode := diagnosticOff
			t.Setenv(format.EnvDebug, "0")
			t.Setenv(format.EnvLog, "")
			if tc.want == diagnosticOff {
				t.Setenv(format.EnvLog, diagnosticJSONValue)
				nextMode = diagnosticJSON
			}
			output := captureLaunchDiagnosticOutput(t, func() {
				var entry *format.VariantEntry
				if tc.want != diagnosticOff {
					entry = &format.VariantEntry{Level: "v1"}
				}
				logDiagnostics(diagnostics, entry, format.ExecModeMemfd, microarch.Info{}, microarch.PolicyResult{},
					nil, nil, time.Microsecond, 2*time.Microsecond)
			})
			switch tc.want {
			case diagnosticOff:
				require.Empty(t, output)
			case diagnosticText:
				require.Contains(t, output, "[microfat:debug]")
				require.NotContains(t, output, `"event":"dispatch"`)
			case diagnosticJSON:
				require.Contains(t, output, `"event":"dispatch"`)
				require.NotContains(t, output, "[microfat:debug]")
			}
			require.Equal(t, nextMode, newLaunchDiagnostics().mode)
		})
	}
}

func TestDisabledLaunchDiagnosticsDoNotClockOrAllocate(t *testing.T) {
	t.Setenv(format.EnvDebug, diagnosticFalseValue)
	t.Setenv(format.EnvLog, "")
	var lastLaunch launchDiagnostics
	var lastTimer time.Time
	allocations := testing.AllocsPerRun(100, func() {
		lastLaunch = newLaunchDiagnostics()
		lastTimer = lastLaunch.startTimer()
		logDiagnostics(lastLaunch, nil, format.ExecModeMemfd, microarch.Info{}, microarch.PolicyResult{}, nil, nil,
			lastLaunch.elapsed(lastTimer), lastLaunch.totalElapsed())
	})
	require.Equal(t, diagnosticOff, lastLaunch.mode)
	require.True(t, lastLaunch.start.IsZero())
	require.True(t, lastTimer.IsZero())
	require.Zero(t, allocations, "the actual disabled snapshot/timing/logging path must not allocate")
}

func captureLaunchDiagnosticOutput(t *testing.T, emit func()) string {
	t.Helper()
	capture, err := os.CreateTemp(t.TempDir(), "launch-diagnostics")
	require.NoError(t, err)
	previous := os.Stderr
	os.Stderr = capture
	defer func() {
		os.Stderr = previous
		require.NoError(t, capture.Close())
	}()
	emit()
	data, err := os.ReadFile(capture.Name())
	require.NoError(t, err)
	return string(data)
}

func TestDiagnosticOutputModes(t *testing.T) {
	for _, tc := range []struct {
		name, debug, log, want string
	}{
		{"disabled", "", "", ""},
		{"false", "FALSE", "", ""},
		{"debug", "1", "", "[microfat:debug]"},
		{"noncanonical debug", "verbose", "", "[microfat:debug]"},
		{"text log", diagnosticFalseValue, "text", "[microfat:debug]"},
		{"noncanonical log", "0", diagnosticFalseValue, "[microfat:debug]"},
		{diagnosticJSONValue, "0", "JsOn", `"event":"dispatch"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(format.EnvDebug, tc.debug)
			t.Setenv(format.EnvLog, tc.log)
			capture, err := os.CreateTemp(t.TempDir(), "diagnostics")
			require.NoError(t, err)
			previous := os.Stderr
			os.Stderr = capture
			t.Cleanup(func() {
				os.Stderr = previous
				require.NoError(t, capture.Close())
			})
			logDiagnostics(newLaunchDiagnostics(), &format.VariantEntry{Level: "v1"}, format.ExecModeMemfd,
				microarch.Info{Arch: testArchAMD64, Level: "v1"}, microarch.PolicyResult{},
				[]string{diagnosticMemoryEnv, diagnosticProcsEnv, "GOGC=75", format.EnvCgroupGCProfile + "=latency_critical"},
				nil, time.Microsecond, 2*time.Microsecond)
			data, err := os.ReadFile(capture.Name())
			require.NoError(t, err)
			if tc.want == "" {
				require.Empty(t, data)
				return
			}
			require.Contains(t, string(data), tc.want)
			if strings.EqualFold(tc.log, diagnosticJSONValue) {
				require.Contains(t, string(data), `"decompression_duration_us":1`)
				require.Contains(t, string(data), `"total_launcher_us":2`)
				require.Contains(t, string(data), `"gogc":"75"`)
				require.Contains(t, string(data), `"gc_profile":"latency_critical"`)
			} else {
				require.Contains(t, string(data), "decompress_us=1 total_us=2")
				require.Contains(t, string(data), "gogc=75 gc_profile=latency_critical")
			}
		})
	}
}

func BenchmarkDispatchDiagnostics(b *testing.B) {
	// These probes exclude extraction, environment construction, and exec. Both
	// enabled arms include four timer clock reads, plus the JSON event timestamp.
	// Each mode runs both orders so an arm is not always measured first.
	sink, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	require.NoError(b, err)
	previous := os.Stderr
	os.Stderr = sink
	b.Cleanup(func() {
		os.Stderr = previous
		require.NoError(b, sink.Close())
	})
	entry := &format.VariantEntry{Level: "v1", SHA256: strings.Repeat("a", 64), UncompressedSize: 2048}
	host := microarch.Info{Arch: testArchAMD64, Level: "v1"}
	env := []string{diagnosticMemoryEnv, diagnosticProcsEnv, "GOGC=75", format.EnvCgroupGCProfile + "=latency_critical"}
	for _, tc := range []struct{ name, debug, log string }{
		{"disabled", "", ""},
		{diagnosticJSONValue, "", diagnosticJSONValue},
		{"debug", "1", ""},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.Setenv(format.EnvDebug, tc.debug)
			b.Setenv(format.EnvLog, tc.log)
			for _, order := range []struct {
				name        string
				conditional []bool
			}{
				{"legacy_first", []bool{false, true}},
				{"conditional_first", []bool{true, false}},
			} {
				b.Run(order.name, func(b *testing.B) {
					for _, conditional := range order.conditional {
						name := "legacy"
						if conditional {
							name = "conditional"
						}
						b.Run(name, func(b *testing.B) {
							b.ReportAllocs()
							if conditional {
								for b.Loop() {
									start := c1StartDiagnosticTimer(c1DiagnosticsEnabled())
									decompStart := c1StartDiagnosticTimer(!start.IsZero())
									decompDuration := c1DiagnosticElapsed(decompStart)
									if !start.IsZero() {
										legacyLogDiagnostics(entry, format.ExecModeMemfd, host, microarch.PolicyResult{}, env, nil,
											decompDuration, c1DiagnosticElapsed(start))
									}
								}
								return
							}
							for b.Loop() {
								start := time.Now()
								decompStart := time.Now()
								decompDuration := time.Since(decompStart)
								legacyLogDiagnostics(entry, format.ExecModeMemfd, host, microarch.PolicyResult{}, env, nil,
									decompDuration, time.Since(start))
							}
						})
					}
				})
			}
		})
	}
}

func BenchmarkLaunchDiagnosticsSnapshot(b *testing.B) {
	// Descriptive probes of the actual C2 classification/timer/logger calls.
	// Cold uses four clocks when enabled; warm uses two. Neither includes
	// extraction, payload environment construction, cgroup detection or exec.
	sink, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	require.NoError(b, err)
	previous := os.Stderr
	os.Stderr = sink
	b.Cleanup(func() {
		os.Stderr = previous
		require.NoError(b, sink.Close())
	})
	entry := &format.VariantEntry{Level: "v1", SHA256: strings.Repeat("a", 64), UncompressedSize: 2048}
	host := microarch.Info{Arch: testArchAMD64, Level: "v1"}
	env := []string{diagnosticMemoryEnv, diagnosticProcsEnv, "GOGC=75", format.EnvCgroupGCProfile + "=latency_critical"}
	for _, tc := range []struct{ name, debug, log string }{
		{"disabled", "", ""},
		{diagnosticJSONValue, "", diagnosticJSONValue},
		{"debug", "1", ""},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.Setenv(format.EnvDebug, tc.debug)
			b.Setenv(format.EnvLog, tc.log)
			for _, cold := range []bool{true, false} {
				path := "warm"
				if cold {
					path = "cold"
				}
				b.Run(path, func(b *testing.B) {
					for _, order := range []struct {
						name     string
						snapshot []bool
					}{
						{"legacy_first", []bool{false, true}},
						{"snapshot_first", []bool{true, false}},
					} {
						b.Run(order.name, func(b *testing.B) {
							for _, snapshot := range order.snapshot {
								arm := "legacy"
								if snapshot {
									arm = "snapshot"
								}
								b.Run(arm, func(b *testing.B) {
									b.ReportAllocs()
									if snapshot {
										for b.Loop() {
											diagnostics := newLaunchDiagnostics()
											var duration time.Duration
											if cold {
												duration = diagnostics.elapsed(diagnostics.startTimer())
											}
											logDiagnostics(diagnostics, entry, format.ExecModeMemfd, host, microarch.PolicyResult{},
												env, nil, duration, diagnostics.totalElapsed())
										}
										return
									}
									for b.Loop() {
										start := time.Now()
										var duration time.Duration
										if cold {
											duration = time.Since(time.Now())
										}
										legacyLogDiagnostics(entry, format.ExecModeMemfd, host, microarch.PolicyResult{}, env, nil,
											duration, time.Since(start))
									}
								})
							}
						})
					}
				})
			}
		})
	}
}

// Preserve the baseline/C1 logger's two getenv calls and enabled-mode selection
// for its historical probes. Both arms use the same record formatting/output.
func legacyLogDiagnostics(
	entry *format.VariantEntry, execMode string, hostInfo microarch.Info, policyRes microarch.PolicyResult,
	env []string, limits *cgroup.Limits, decompDuration, totalDuration time.Duration,
) {
	debugOpt := os.Getenv(format.EnvDebug)
	logOpt := os.Getenv(format.EnvLog)
	if logOpt == "" && (debugOpt == "" || debugOpt == "0" || strings.EqualFold(debugOpt, diagnosticFalseValue)) {
		return
	}
	mode := diagnosticText
	if strings.EqualFold(logOpt, diagnosticJSONValue) {
		mode = diagnosticJSON
	}
	logDiagnostics(launchDiagnostics{mode: mode}, entry, execMode, hostInfo, policyRes, env, limits, decompDuration, totalDuration)
}

// These helpers preserve the rejected C1 candidate solely for its historical
// controlled allocation/timing probes, independently of the C2 production API.
// c1DiagnosticsEnabled preserves the dispatch logger's environment semantics,
// including text diagnostics for nonempty, unrecognized log/debug values.
func c1DiagnosticsEnabled() bool {
	return c1DiagnosticOptionsEnabled(os.Getenv(format.EnvDebug), os.Getenv(format.EnvLog))
}

func c1DiagnosticOptionsEnabled(debugOpt, logOpt string) bool {
	return logOpt != "" || (debugOpt != "" && debugOpt != "0" && !strings.EqualFold(debugOpt, "false"))
}

// A zero start disables timing without consulting a clock. Keep the decision
// local to a launch rather than caching process environment in package state.
func c1StartDiagnosticTimer(enabled bool) time.Time {
	if !enabled {
		return time.Time{}
	}
	return time.Now()
}

func c1DiagnosticElapsed(start time.Time) time.Duration {
	if start.IsZero() {
		return 0
	}
	return time.Since(start)
}
