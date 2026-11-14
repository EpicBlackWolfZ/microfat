//go:build linux

package e2e_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These full-process observations provide scale, not a mask/current comparison.
// Child memory and service latency are not measured by Go benchmark counters.
func BenchmarkSelectionCompleteProcessContext(b *testing.B) {
	const marker = "selection-context-executed"
	const markerKey = "MICROFAT_SELECTION_BENCHMARK_MARKER"
	const launchTimeout = 10 * time.Second
	for _, mode := range []string{execModeNative, execModeMemfd, telemetryColdCache, telemetryWarmCache} {
		b.Run(mode, func(b *testing.B) {
			b.StopTimer()
			cacheRoot := filepath.Join(b.TempDir(), "cache")
			path := goldenFatBin
			execMode := execModeCache
			if mode == execModeNative {
				path = goldenVariantBins[creationEpochLevels()[0]]
			} else if mode == execModeMemfd {
				execMode = execModeMemfd
			}
			env := stubInfoProcessEnvironment(cacheRoot, "MICROFAT_EXEC_MODE="+execMode, markerKey+"="+marker)
			launch := func() {
				ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
				cmd := exec.CommandContext(ctx, path, "--echo-env", markerKey)
				cmd.Env = env
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				// Output fails on nonzero exit; the supplied buffer retains child stderr.
				b.StartTimer()
				output, err := cmd.Output()
				b.StopTimer()
				cancel()
				require.NoError(b, err, stderr.String())
				require.Empty(b, stderr.String())
				require.Equal(b, marker, string(output))
			}
			if mode == telemetryWarmCache {
				// Seed outside timing without adding an unreported timed invocation.
				cmd := exec.Command(path, "--echo-env", markerKey)
				cmd.Env = env
				output, err := cmd.CombinedOutput()
				require.NoError(b, err)
				require.Equal(b, marker, string(output))
			}
			b.ResetTimer()
			for range b.N {
				if mode == telemetryColdCache {
					require.NoError(b, os.RemoveAll(cacheRoot))
				}
				launch()
			}
		})
	}
}
