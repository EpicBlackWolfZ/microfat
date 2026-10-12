package runner

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/workloads/baseline"
	"github.com/stretchr/testify/require"
)

func TestTrialEvidenceBudget(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		trials int
		valid  bool
	}{
		{"minimum", 1, true},
		{"maximum", schema.MaxTrials, true},
		{"zero", 0, false},
		{"negative", -1, false},
		{"above_budget", schema.MaxTrials + 1, false},
		{"largest_integer", int(^uint(0) >> 1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := Config{Workload: baseline.New(), Trials: test.trials, TrialDuration: time.Nanosecond}
			if test.valid {
				require.NoError(t, cfg.Validate())
				return
			}
			called := false
			r := NewWithDetector(func() (*schema.EnvironmentSnapshot, error) {
				called = true
				return nil, nil
			})
			evidence, err := r.Run(context.Background(), cfg)
			require.ErrorIs(t, err, ErrInvalidTrialCount)
			require.ErrorContains(t, err, "allowed 1.."+strconv.Itoa(schema.MaxTrials))
			require.Nil(t, evidence)
			require.False(t, called, "invalid counts must fail before environment capture and workload setup")
		})
	}
}
