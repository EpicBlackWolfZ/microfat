package runner

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/stretchr/testify/require"
)

func TestExperimentObserverIntervalBounds(t *testing.T) {
	t.Parallel()
	const durationMS = 3000
	for _, tc := range []struct {
		name     string
		interval int
		disabled bool
		valid    bool
	}{
		{"enabled maximum", system.MaxObserverIntervalMS, false, true},
		{"enabled above maximum", system.MaxObserverIntervalMS + 1, false, false},
		{"enabled slow interval", durationMS / 2, false, false},
		{"disabled above maximum", system.MaxObserverIntervalMS + 1, true, true},
		{"disabled slow interval", durationMS / 2, true, true},
		{"disabled above duration", durationMS + 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := DefaultExperimentConfig()
			cfg.DurationMS, cfg.SampleMS, cfg.DisableObserver = durationMS, tc.interval, tc.disabled
			err := cfg.Validate()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if tc.disabled {
				return
			}
			phase := filepath.Join(t.TempDir(), "phase")
			require.NoError(t, os.WriteFile(phase, []byte("startup"), evidenceFilePerm))
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			err = system.Observe(ctx, system.ObserverConfig{PID: os.Getpid(), PhaseFile: phase, IntervalMS: tc.interval}, io.Discard)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
