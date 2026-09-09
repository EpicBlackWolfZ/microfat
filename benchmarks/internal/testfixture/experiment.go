// Package testfixture supplies synthetic data for validation tests, never performance evidence.
package testfixture

import (
	"fmt"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const (
	Blocks       = 5
	shaLength    = 40
	digestLength = 64
)

func Experiment() *schema.ExperimentV2 {
	config := []byte(`{}`)
	exp := &schema.ExperimentV2{SchemaVersion: schema.VersionV2, ID: "fixture", CreatedAt: "2026-09-09T00:00:00Z",
		SourceSHA: strings.Repeat("a", shaLength), Config: config, ConfigSHA256: schema.Digest(config), Complete: true, Seed: 1,
		Artifacts: []schema.Artifact{{ID: "app", Path: "/fixture/app", SHA256: strings.Repeat("a", digestLength), Bytes: 1}},
		Configurations: []schema.Configuration{{ID: "base", ArtifactID: "app", Mode: "native", Level: "v1", Tuning: "matched"},
			{ID: "candidate", ArtifactID: "app", Mode: "native", Level: "v1", Tuning: "matched"}}}
	for block := range Blocks {
		for _, cfg := range exp.Configurations {
			scheduled := schema.ScheduledTrial{ID: fmt.Sprintf("trial-%d", len(exp.Schedule)), Block: block,
				Position: len(exp.Schedule), ConfigurationID: cfg.ID}
			exp.Schedule = append(exp.Schedule, scheduled)
			exp.Trials = append(exp.Trials, schema.ProcessTrial{ScheduledTrial: scheduled, StartedAt: exp.CreatedAt,
				Outcome: schema.OutcomeOK, DurationNS: 1, Metrics: map[string]schema.Measurement{
					"startup_ns":     schema.Measured(float64(block+1), "ns", "startup", "fixture"),
					"throughput_qps": schema.Measured(float64(block+1), "requests/s", "steady_state", "fixture")}})
		}
	}
	return exp
}

func HTTP() schema.HTTPResult {
	return schema.HTTPResult{Requests: 1, Successful: 1, StatusCodes: map[string]int64{"200": 1},
		DurationSeconds: 1, ActualQPS: 1, ResolutionSeconds: 1, RawPath: "trials/trial-0/fortio.json",
		Buckets:            []schema.HistogramBucket{{Start: 0, End: 1, Count: 1}},
		PercentilesSeconds: map[string]float64{"p50": 1}}
}
