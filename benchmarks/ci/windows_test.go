package ci

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/report"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/require"
)

func TestHostedObservedWindows(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*schema.ProcessTrial){
		"one millisecond measurement": func(t *schema.ProcessTrial) { t.Load.DurationSeconds = 0.001 },
		"positive short measurement":  func(t *schema.ProcessTrial) { t.Load.DurationSeconds = 20 },
		"short total duration":        func(t *schema.ProcessTrial) { t.DurationNS = int64(time.Millisecond) },
		"load exceeds total":          func(t *schema.ProcessTrial) { t.Load.DurationSeconds = 60 },
		"sum exceeds total":           func(t *schema.ProcessTrial) { t.Warmup.DurationSeconds = 20 },
		"warmup unavailable":          func(t *schema.ProcessTrial) { t.Warmup = nil },
		"warmup short":                func(t *schema.ProcessTrial) { t.Warmup.DurationSeconds = 1 },
		"warmup errors":               func(t *schema.ProcessTrial) { t.Warmup.Successful-- },
		"load unavailable":            func(t *schema.ProcessTrial) { t.Load = nil },
		"samples unavailable":         func(t *schema.ProcessTrial) { t.Samples = nil },
		"sample count mismatch":       func(t *schema.ProcessTrial) { t.SampleCount++ },
		"effective interval slow":     func(t *schema.ProcessTrial) { t.SampleIntervalMS = 500 },
		"effective interval differs":  func(t *schema.ProcessTrial) { t.SampleIntervalMS = 200 },
		"duplicate timestamps":        func(t *schema.ProcessTrial) { t.Samples[1].ElapsedNS = t.Samples[0].ElapsedNS },
		"sample gap": func(t *schema.ProcessTrial) {
			t.Samples = append(t.Samples[:10], t.Samples[20:]...)
			t.SampleCount = len(t.Samples)
		},
		"partial tail": func(t *schema.ProcessTrial) {
			t.Samples = t.Samples[:len(t.Samples)/2]
			t.SampleCount = len(t.Samples)
		},
		"two sample claim": func(t *schema.ProcessTrial) {
			t.Samples = []schema.ResourceSample{t.Samples[0], t.Samples[len(t.Samples)-1]}
			t.SampleCount = len(t.Samples)
		},
		"warmup phase missing": func(t *schema.ProcessTrial) {
			for i := range t.Samples {
				t.Samples[i].Phase = "steady_state"
			}
		},
		"phase reversed":    func(t *schema.ProcessTrial) { t.Samples[len(t.Samples)-1].Phase = "warmup" },
		"outside precision": func(t *schema.ProcessTrial) { t.Load.DurationSeconds = 29.94 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			exp := hostedFixture()
			mutate(&exp.Trials[0])
			q := QualifyHosted(exp)
			require.False(t, q.Publishable, q)
			require.NotEmpty(t, q.Reasons)
		})
	}
	t.Run("timer precision", func(t *testing.T) {
		exp := hostedFixture()
		exp.Trials[0].Load.DurationSeconds = 29.96
		exp.Trials[0].Warmup.DurationSeconds = 9.96
		q := QualifyHosted(exp)
		require.True(t, q.Publishable, q)
	})
}

func TestHostedOfflineObservedWindows(t *testing.T) {
	t.Parallel()
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "short observed window"}[partial], func(t *testing.T) {
			exp := hostedFixture()
			if partial {
				exp.Trials[0].Load.DurationSeconds = 0.001
				exp.Trials[0].Samples = exp.Trials[0].Samples[:2]
				exp.Trials[0].SampleCount = 2
			}
			files := make(map[string][]byte)
			for i := range exp.Trials {
				trial := &exp.Trials[i]
				var err error
				files[trial.TelemetryPath], err = schema.CanonicalV2(trial.Samples)
				require.NoError(t, err)
				files[trial.Load.RawPath] = []byte(`{"synthetic":true}`)
				files[trial.Warmup.RawPath] = []byte(`{"synthetic":true}`)
				trial.Samples = nil
			}
			root := filepath.Join(t.TempDir(), "bundle")
			require.NoError(t, report.WriteBundle(exp, root, files))
			loaded, err := report.ReadBundle(root)
			require.NoError(t, err)
			require.Len(t, loaded.Trials[0].Samples, exp.Trials[0].SampleCount)
			q := QualifyHosted(loaded)
			require.Equal(t, !partial, q.Publishable, q)
		})
	}
}
