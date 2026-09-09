package schema_test

import (
	"math"
	"strings"
	"testing"

	json "encoding/json/v2"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/internal/testfixture"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const missingReference = "missing"

func TestV2RoundTrip(t *testing.T) {
	t.Parallel()
	exp := testfixture.Experiment()
	require.NoError(t, schema.ValidateV2(exp))
	data, err := schema.CanonicalV2(exp)
	require.NoError(t, err)
	var restored schema.ExperimentV2
	require.NoError(t, json.Unmarshal(data, &restored))
	require.NoError(t, schema.ValidateV2(&restored))
	again, err := schema.CanonicalV2(&restored)
	require.NoError(t, err)
	assert.Equal(t, data, again)
	a, err := schema.ConfigDigest([]byte(`{"b": 2, "a":1}`))
	require.NoError(t, err)
	b, err := schema.ConfigDigest([]byte(`{ "a": 1, "b": 2 }`))
	require.NoError(t, err)
	assert.Equal(t, a, b)
	_, err = schema.ConfigDigest([]byte(`{`))
	require.Error(t, err)
	_, err = schema.CanonicalV2(make(chan int))
	require.Error(t, err)
}

func TestV2RejectsInvalidEvidence(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*schema.ExperimentV2){
		"version":                 func(e *schema.ExperimentV2) { e.SchemaVersion = "future" },
		"id":                      func(e *schema.ExperimentV2) { e.ID = "../escape" },
		"time":                    func(e *schema.ExperimentV2) { e.CreatedAt = "bad" },
		"digest":                  func(e *schema.ExperimentV2) { e.ConfigSHA256 = strings.Repeat("b", 64) },
		"configurations":          func(e *schema.ExperimentV2) { e.Configurations = nil },
		"schedule":                func(e *schema.ExperimentV2) { e.Schedule = nil },
		"artifact":                func(e *schema.ExperimentV2) { e.Artifacts[0].Bytes = 0 },
		"duplicate artifact":      func(e *schema.ExperimentV2) { e.Artifacts = append(e.Artifacts, e.Artifacts[0]) },
		"reference":               func(e *schema.ExperimentV2) { e.Configurations[0].ArtifactID = missingReference },
		"duplicate configuration": func(e *schema.ExperimentV2) { e.Configurations[1].ID = "base" },
		"duplicate schedule":      func(e *schema.ExperimentV2) { e.Schedule[1] = e.Schedule[0] },
		"negative block":          func(e *schema.ExperimentV2) { e.Schedule[0].Block = -1 },
		"missing configuration":   func(e *schema.ExperimentV2) { e.Schedule[0].ConfigurationID = missingReference },
		"trial identity":          func(e *schema.ExperimentV2) { e.Trials[0].ID = missingReference },
		"duplicate trial":         func(e *schema.ExperimentV2) { e.Trials[1] = e.Trials[0] },
		"outcome":                 func(e *schema.ExperimentV2) { e.Trials[0].Outcome = "unknown" },
		"reason":                  func(e *schema.ExperimentV2) { e.Trials[0].Outcome = schema.OutcomeFailed },
		"duration":                func(e *schema.ExperimentV2) { e.Trials[0].DurationNS = -1 },
		"trial timestamp":         func(e *schema.ExperimentV2) { e.Trials[0].StartedAt = "no" },
		"metric":                  func(e *schema.ExperimentV2) { e.Trials[0].Metrics["bad"] = schema.Measurement{} },
		"sample":                  func(e *schema.ExperimentV2) { e.Trials[0].Samples = []schema.ResourceSample{{ElapsedNS: -1}} },
		"sample metric": func(e *schema.ExperimentV2) {
			e.Trials[0].Samples = []schema.ResourceSample{{Phase: "startup", Metrics: map[string]schema.Measurement{"x": {}}}}
		},
		"load":          func(e *schema.ExperimentV2) { e.Trials[0].Load = &schema.HTTPResult{} },
		"incomplete":    func(e *schema.ExperimentV2) { e.Trials = e.Trials[:1] },
		"release state": func(e *schema.ExperimentV2) { e.ReleaseEligible = true; e.Dirty = true },
		"comparison":    func(e *schema.ExperimentV2) { e.Comparisons = []schema.Comparison{{Baseline: missingReference}} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			exp := testfixture.Experiment()
			mutate(exp)
			require.Error(t, schema.ValidateV2(exp))
		})
	}
	require.Error(t, schema.ValidateV2(nil))
	exp := testfixture.Experiment()
	exp.Complete = false
	exp.Trials[0].Outcome, exp.Trials[0].Reason = schema.OutcomeCancelled, "cancelled"
	exp.Trials[0].Samples = []schema.ResourceSample{{Phase: "startup", Metrics: map[string]schema.Measurement{
		"rss": schema.Unavailable("bytes", "startup", "proc", "unavailable")}}}
	http := testfixture.HTTP()
	exp.Trials[1].Load = &http
	require.NoError(t, schema.ValidateV2(exp))
}

func TestMeasurementsAndHTTPValidation(t *testing.T) {
	t.Parallel()
	require.NoError(t, schema.ValidateMeasurement(schema.Measured(0, "bytes", "startup", "proc")))
	require.NoError(t, schema.ValidateMeasurement(schema.Unavailable("bytes", "startup", "proc", "not supported")))
	for name, m := range map[string]schema.Measurement{
		missingReference: {}, "kind": {Unit: "ns", Phase: "startup", Source: "test", Kind: "unknown"},
		"unavailable reason": {Unit: "ns", Phase: "startup", Source: "test", Kind: "measured"},
		"nan":                schema.Measured(math.NaN(), "ns", "startup", "test"),
	} {
		t.Run(name, func(t *testing.T) { require.Error(t, schema.ValidateMeasurement(m)) })
	}
	require.False(t, schema.Finite(math.Inf(1)))
	require.False(t, schema.ValidDigest("bad"))
	for _, name := range []string{"", ".", "..", "../a", "/a", "a/../b", "a\\b", "a\nb", "nul\x00"} {
		assert.False(t, schema.SafeReference(name))
	}
	http := testfixture.HTTP()
	require.NoError(t, schema.ValidateHTTP(&http))
	for name, mutate := range map[string]func(*schema.HTTPResult){
		"empty":           func(h *schema.HTTPResult) { h.Requests = 0 },
		"negative status": func(h *schema.HTTPResult) { h.StatusCodes["200"] = -1 },
		"status mismatch": func(h *schema.HTTPResult) { h.StatusCodes["200"] = 0 },
		"bucket":          func(h *schema.HTTPResult) { h.Buckets[0].End = -1 },
		"bucket count":    func(h *schema.HTTPResult) { h.Buckets[0].Count = 0 },
		"percentile":      func(h *schema.HTTPResult) { h.PercentilesSeconds["p50"] = math.NaN() },
	} {
		t.Run(name, func(t *testing.T) { h := testfixture.HTTP(); mutate(&h); require.Error(t, schema.ValidateHTTP(&h)) })
	}
}
