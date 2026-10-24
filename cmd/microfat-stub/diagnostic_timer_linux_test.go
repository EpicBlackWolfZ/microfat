//go:build linux

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/require"
)

const (
	diagnosticFalseValue = "false"
	diagnosticJSONValue  = "json"
)

func TestDiagnosticsEnabled(t *testing.T) {
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
			require.Equal(t, tc.want, diagnosticsEnabled())
		})
	}
}

func TestDiagnosticTimer(t *testing.T) {
	t.Parallel()
	start := startDiagnosticTimer(false)
	require.True(t, start.IsZero())
	require.Zero(t, diagnosticElapsed(start))

	before := time.Now()
	start = startDiagnosticTimer(true)
	after := time.Now()
	require.False(t, start.IsZero())
	require.False(t, start.Before(before))
	require.False(t, start.After(after))
	require.GreaterOrEqual(t, diagnosticElapsed(start), time.Duration(0))
}

func TestDisabledDiagnosticsDoNotFormat(t *testing.T) {
	t.Setenv(format.EnvDebug, diagnosticFalseValue)
	t.Setenv(format.EnvLog, "")
	// A nil entry is safe only if disabled diagnostics return before formatting.
	allocations := testing.AllocsPerRun(100, func() {
		start := startDiagnosticTimer(diagnosticsEnabled())
		logDiagnostics(nil, format.ExecModeMemfd, microarch.Info{}, microarch.PolicyResult{}, nil, nil,
			diagnosticElapsed(start), diagnosticElapsed(start))
	})
	require.Zero(t, allocations, "the disabled diagnostics probe must not allocate")
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
			logDiagnostics(&format.VariantEntry{Level: "v1"}, format.ExecModeMemfd,
				microarch.Info{Arch: "amd64", Level: "v1"}, microarch.PolicyResult{},
				[]string{"GOMEMLIMIT=1000B", "GOMAXPROCS=4", "GOGC=75", format.EnvCgroupGCProfile + "=latency_critical"},
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
	host := microarch.Info{Arch: "amd64", Level: "v1"}
	env := []string{"GOMEMLIMIT=1000B", "GOMAXPROCS=4", "GOGC=75", format.EnvCgroupGCProfile + "=latency_critical"}
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
									start := startDiagnosticTimer(diagnosticsEnabled())
									decompStart := startDiagnosticTimer(!start.IsZero())
									decompDuration := diagnosticElapsed(decompStart)
									if !start.IsZero() {
										logDiagnostics(entry, format.ExecModeMemfd, host, microarch.PolicyResult{}, env, nil,
											decompDuration, diagnosticElapsed(start))
									}
								}
								return
							}
							for b.Loop() {
								start := time.Now()
								decompStart := time.Now()
								decompDuration := time.Since(decompStart)
								logDiagnostics(entry, format.ExecModeMemfd, host, microarch.PolicyResult{}, env, nil,
									decompDuration, time.Since(start))
							}
						})
					}
				})
			}
		})
	}
}
