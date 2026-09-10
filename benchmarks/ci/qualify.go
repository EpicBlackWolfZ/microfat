package ci

import (
	json "encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/compare"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const HostedRelease = "hosted-release"
const ReleaseBlocks = 20
const releaseDurationMS = 30000
const releaseWarmupMS = 10000
const releaseSampleMS = 250

type Qualification struct {
	Policy      string   `json:"policy"`
	Class       string   `json:"evidence_class"`
	Publishable bool     `json:"publishable"`
	Reasons     []string `json:"reasons"`
	Limitations []string `json:"limitations"`
}

// QualifyHosted evaluates publication completeness, never whether performance improved.
func QualifyHosted(exp *schema.ExperimentV2) Qualification {
	q := Qualification{Policy: HostedRelease, Class: "hosted-comparative", Reasons: []string{},
		Limitations: []string{"shared hosted VM; physical hardware and frequency stability are not established"}}
	if err := schema.ValidateV2(exp); err != nil {
		q.Reasons = append(q.Reasons, err.Error())
		return q
	}
	q.Limitations = append(q.Limitations, exp.Warnings...)
	r := exp.Runner
	if r == nil || r.Provider != schema.HostedProvider || r.Protocol != schema.StartupProtocol || r.Image == "" ||
		r.ImageVersion == "" || r.RunID == "" || r.Attempt == "" || r.Repository == "" || exp.ReleaseEligible {
		q.Reasons = append(q.Reasons, "missing or contradictory hosted runner provenance")
	}
	var cfg struct {
		Blocks          int  `json:"blocks"`
		DurationMS      int  `json:"duration_ms"`
		WarmupMS        int  `json:"warmup_ms"`
		SampleMS        int  `json:"sample_ms"`
		DisableObserver bool `json:"disable_observer"`
	}
	if err := json.Unmarshal(exp.Config, &cfg); err != nil || cfg.Blocks < ReleaseBlocks || cfg.DisableObserver ||
		cfg.DurationMS < releaseDurationMS || cfg.WarmupMS < releaseWarmupMS || cfg.SampleMS <= 0 || cfg.SampleMS > releaseSampleMS ||
		!exp.Complete || exp.Dirty || exp.SourceSHA == "" || exp.SourceSHA == "unknown" {
		q.Reasons = append(q.Reasons, "release schedule or clean source requirements not met")
	}
	q.Reasons = append(q.Reasons, qualifyArtifacts(exp)...)
	for _, t := range exp.Trials {
		q.Reasons = append(q.Reasons, qualifyTrial(t)...)
	}
	comparisons, err := compare.Revisions(exp)
	if err != nil {
		q.Reasons = append(q.Reasons, err.Error())
	} else {
		for _, c := range comparisons {
			if slices.Contains(coreMetrics(), c.Metric) && (c.Status != "complete" || len(c.Pairs) < ReleaseBlocks) {
				q.Reasons = append(q.Reasons, "incomplete core comparison: "+c.Candidate+"/"+c.Metric)
			}
		}
	}
	slices.Sort(q.Reasons)
	q.Reasons = slices.Compact(q.Reasons)
	q.Publishable = len(q.Reasons) == 0
	return q
}

func coreMetrics() []string {
	return []string{startupMetric, "artifact_bytes", "throughput_qps", "peak_rss_bytes", "user_cpu_seconds", "system_cpu_seconds"}
}

func qualifyTrial(t schema.ProcessTrial) []string {
	var reasons []string
	if t.Outcome != schema.OutcomeOK || t.SampleCount < 2 || t.TelemetryPath == "" || t.Load == nil ||
		t.Load.Requests == 0 || t.Load.Successful != t.Load.Requests {
		reasons = append(reasons, t.ID+": successful trial and complete telemetry required")
	}
	for _, name := range coreMetrics() {
		m := t.Metrics[name]
		if m.Value == nil || name == startupMetric && m.Source != schema.StartupProtocol {
			reasons = append(reasons, t.ID+": missing core measurement "+name)
		}
	}
	for _, role := range []string{"target", "generator"} {
		for _, name := range []string{"affinity", "cpu.max", "memory.max"} {
			found := false
			for _, c := range t.Controls {
				if c.Name != role+"."+name || c.State != "applied" || c.Effective == "" {
					continue
				}
				found = true
				if name != "affinity" {
					fields := strings.Fields(c.Effective)
					if len(fields) == 0 {
						found = false
						continue
					}
					value, err := strconv.ParseInt(fields[0], 10, 64)
					found = err == nil && value > 0
				}
			}
			if !found {
				reasons = append(reasons, fmt.Sprintf("%s: required effective control %s.%s", t.ID, role, name))
			}
		}
	}
	return reasons
}

func qualifyArtifacts(exp *schema.ExperimentV2) []string {
	var reasons []string
	harness := false
	for _, a := range exp.Artifacts {
		if a.SourceSHA == "" || a.SourceSHA == "unknown" || !schema.ValidDigest(a.SHA256) {
			reasons = append(reasons, "unidentified artifact: "+a.ID)
		}
		if a.ID == "harness" {
			harness = a.Settings["vcs.modified"] == "false" && a.SourceSHA == exp.SourceSHA
		}
	}
	if !harness {
		reasons = append(reasons, "clean matching harness build required")
	}

	return reasons
}
