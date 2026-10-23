package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

const (
	summaryMaximumObservations = 4096
	summaryHashLength          = 64
	summaryMedianDivisor       = 2
	summaryMaximumExactInteger = 1 << 53
	summaryCurrent             = "current"
	summarySerial              = "serial1"
	summaryPool                = "pool"
	summaryFullStub            = "full"
)

var ErrSummaryInput = errors.New("invalid or ambiguous campaign summary input")

type summaryCohort struct {
	Family        string `json:"family"`
	CPU           int    `json:"cpu_budget"`
	MemoryMiB     int64  `json:"hard_memory_mib"`
	Format        int    `json:"format"`
	Codec         string `json:"codec"`
	Level         string `json:"compression_level"`
	Dictionary    bool   `json:"dictionary"`
	StubProfile   string `json:"stub_profile"`
	AdmissionMiB  int64  `json:"admission_mib_override"`
	PriorOutput   bool   `json:"prior_output_sentinel"`
	WorkspaceMiB  int64  `json:"workspace_mib,omitempty"`
	CorpusProfile string `json:"corpus_profile,omitempty"`
	Primary       bool   `json:"primary_confirmation,omitempty"`
}

type summarySeries struct {
	N      int      `json:"n"`
	Median *float64 `json:"median"`
	Min    *float64 `json:"min"`
	Max    *float64 `json:"max"`
}

type summaryFailure struct {
	Block     int    `json:"block"`
	ExitCode  int    `json:"exit_code"`
	OOMKilled bool   `json:"oom_killed"`
	Reason    string `json:"reason"`
}

type summaryGroup struct {
	Cohort                        summaryCohort    `json:"cohort"`
	Mode                          string           `json:"mode"`
	Attempts                      int              `json:"attempts"`
	Succeeded                     int              `json:"succeeded"`
	Failed                        int              `json:"failed"`
	OOMKilled                     int              `json:"oom_killed"`
	TelemetryUnavailable          int              `json:"telemetry_unavailable"`
	FailedPriorOutputNotPreserved int              `json:"failed_prior_output_not_preserved"`
	Failures                      []summaryFailure `json:"failures"`
	PackWallNS                    summarySeries    `json:"successful_pack_wall_ns"`
	PackCPUNS                     summarySeries    `json:"successful_pack_cpu_ns"`
	WorkerRSSKiB                  summarySeries    `json:"successful_worker_rss_kib"`
	CgroupPeakBytes               summarySeries    `json:"successful_cgroup_peak_bytes"`
	ArtifactBytes                 summarySeries    `json:"successful_complete_artifact_bytes"`
	AllAttemptCgroupPeakBytes     summarySeries    `json:"all_attempt_recorded_cgroup_peak_bytes"`
	FailedAttemptCgroupPeakBytes  summarySeries    `json:"failed_attempt_recorded_cgroup_peak_bytes"`
	LogicalStatsSamples           int              `json:"logical_stats_samples"`
	LogicalBoundViolations        int              `json:"logical_bound_violations"`
	PeakAdmittedBytes             summarySeries    `json:"recorded_peak_logical_admitted_bytes"`
	LogicalMemoryBudgetBytes      summarySeries    `json:"recorded_logical_memory_budget_bytes"`
	PeakActiveWorkers             summarySeries    `json:"recorded_peak_active_workers"`
}

type summaryComparison struct {
	Cohort                         summaryCohort `json:"cohort"`
	Baseline                       string        `json:"baseline"`
	Candidate                      string        `json:"candidate"`
	BaselineAttempts               int           `json:"baseline_attempts"`
	CandidateAttempts              int           `json:"candidate_attempts"`
	MatchedAttemptBlocks           int           `json:"matched_attempt_blocks"`
	CompletePairs                  int           `json:"complete_successful_pairs"`
	FailedPairs                    int           `json:"matched_blocks_with_failure"`
	BaselineOnlyBlocks             int           `json:"baseline_only_blocks"`
	CandidateOnlyBlocks            int           `json:"candidate_only_blocks"`
	PackWallSpeedRatio             summarySeries `json:"baseline_over_candidate_pack_wall_ratio"`
	PairedWallDifferenceNS         summarySeries `json:"candidate_minus_baseline_pack_wall_ns"`
	ArtifactBytesRatio             summarySeries `json:"candidate_over_baseline_artifact_bytes_ratio"`
	PayloadBitstreamEqualPairs     int           `json:"compressed_payload_bitstreams_equal_pairs"`
	DictionaryEqualPairs           int           `json:"dictionary_hash_equal_pairs"`
	CompleteArtifactSizeEqualPairs int           `json:"complete_artifact_size_equal_pairs"`
	CompleteArtifactHashEqualPairs int           `json:"complete_artifact_hash_equal_pairs"`
}

type campaignSummary struct {
	SchemaVersion    int                 `json:"schema_version"`
	ProducerRevision string              `json:"producer_revision"`
	ProducerSHA256   string              `json:"producer_sha256"`
	Warnings         []string            `json:"warnings"`
	Groups           []summaryGroup      `json:"groups"`
	Comparisons      []summaryComparison `json:"comparisons"`
}

type summaryGroupKey struct {
	cohort summaryCohort
	mode   string
}

type summaryAccumulator struct {
	group                                           summaryGroup
	observations                                    map[int]observation
	wall, cpu, rss, peak, size, allPeak, failedPeak []float64
	admitted, budget, active                        []float64
	settings                                        string
}

// summarizeReport is deterministic descriptive analysis of retained observations.
// It reads no artifacts and cannot authenticate producer-supplied measurements.
func summarizeReport(report campaignReport) (campaignSummary, error) {
	out := campaignSummary{SchemaVersion: 1, ProducerRevision: report.ProducerRevision, ProducerSHA256: report.ProducerSHA256,
		Warnings: []string{
			"Descriptive medians and matched-block ratios only; no population p99, confidence interval or causal claim.",
			"Principal metrics include successful complete artifacts only; every failure remains in counts and paired-block denominators.",
			"Cgroup peaks include file cache/startup/keeper; worker RSS is separate. Logical admission is not an RSS bound.",
			"Ratios are computed within successful block pairs before taking their median; missing and failed pairs are reported.",
			"Whole-artifact hashes can differ from metadata timestamps even when all compressed payloads match.",
			"This analysis validates reported structure and controls, not the authenticity of supplied data or raw on-disk artifacts.",
		}}
	if report.SchemaVersion != 1 || !validRevision(report.ProducerRevision) || !summaryValidHash(report.ProducerSHA256) ||
		report.GoVersion != researchGoVersion || len(report.Observations) == 0 || len(report.Observations) > summaryMaximumObservations {
		return out, ErrSummaryInput
	}
	groups := make(map[summaryGroupKey]*summaryAccumulator)
	var keys []summaryGroupKey
	for _, observed := range report.Observations {
		key, settings, err := validateSummaryObservation(observed, report.GoVersion)
		if err != nil {
			return out, err
		}
		accumulator, ok := groups[key]
		if !ok {
			accumulator = &summaryAccumulator{group: summaryGroup{Cohort: key.cohort, Mode: key.mode},
				observations: make(map[int]observation), settings: settings}
			groups[key], keys = accumulator, append(keys, key)
		}
		if _, duplicate := accumulator.observations[observed.Spec.Block]; duplicate || accumulator.settings != settings {
			return out, fmt.Errorf("%w: duplicate block or changing within-mode settings", ErrSummaryInput)
		}
		accumulator.add(observed)
	}
	slices.SortFunc(keys, func(a, b summaryGroupKey) int { return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)) })
	for _, key := range keys {
		out.Groups = append(out.Groups, groups[key].finish())
		if key.mode == summaryCurrent {
			continue
		}
		for _, baseline := range []string{summaryCurrent, summarySerial} {
			if baseline == key.mode || (baseline == summarySerial && key.mode != summaryPool) {
				continue
			}
			if reference := groups[summaryGroupKey{cohort: key.cohort, mode: baseline}]; reference != nil {
				out.Comparisons = append(out.Comparisons, compareSummaryGroups(reference, groups[key]))
			}
		}
	}
	return out, nil
}

func validateSummaryObservation(o observation, goVersion string) (summaryGroupKey, string, error) {
	s, r := o.Spec, o.Request
	key := summaryGroupKey{cohort: summaryCohort{s.Family, s.CPU, s.MemoryMiB, s.Format, s.Codec, r.Level,
		s.Dictionary, s.StubProfile, s.AdmissionMiB, s.PriorOutput, s.WorkspaceMiB, s.CorpusProfile, s.Primary}, mode: s.Mode}
	if !validSummarySpec(s, r) {
		return key, "", ErrSummaryInput
	}
	settings := "upstream-default"
	if r.Pool != nil {
		settings = fmt.Sprint(*r.Pool)
	}
	if o.Resource.TelemetryAvailable {
		controls := o.Resource.ActualCG
		expectedMemory := uint64(s.MemoryMiB * mib) // #nosec G115 -- validSummarySpec proved 16..1024 MiB above.
		expectedCPU := uint64(s.CPU)                // #nosec G115 -- validSummarySpec proved 1..4 above.
		if controls.MemoryMaxBytes != expectedMemory || controls.MemorySwapMaxBytes != 0 || controls.CPUPeriodUsec == 0 ||
			controls.CPUQuotaUsec/controls.CPUPeriodUsec != expectedCPU || controls.CPUQuotaUsec%controls.CPUPeriodUsec != 0 {
			return key, settings, fmt.Errorf("%w: actual controls do not match cohort", ErrSummaryInput)
		}
	}
	if summarySucceeded(o) && !validSummaryMetrics(o, goVersion) {
		return key, settings, fmt.Errorf("%w: incomplete successful metrics", ErrSummaryInput)
	}
	if o.Resource.MemoryPeakBytes > summaryMaximumExactInteger {
		return key, settings, fmt.Errorf("%w: metric exceeds exact float64 integer range", ErrSummaryInput)
	}
	return key, settings, nil
}

func validSummarySpec(s trialSpec, r request) bool {
	return safeName.MatchString(s.Family) && slices.Contains([]string{summaryCurrent, summarySerial, summaryPool, modePolicy}, s.Mode) &&
		s.CPU >= 1 && s.CPU <= fullCPU && s.MemoryMiB >= resourceMinimumMemory/mib && s.MemoryMiB <= resourceMaximumMemory/mib &&
		s.Block >= 0 && s.Block < maxConfirmationBlocks && slices.Contains([]int{1, 2}, s.Format) &&
		slices.Contains([]string{"none", "lz4", "zstd"}, s.Codec) && slices.Contains([]string{summaryFullStub, "minimal"}, s.StubProfile) &&
		s.AdmissionMiB >= 0 && s.AdmissionMiB <= resourceMaximumMemory/mib &&
		r.AppName == s.Family && r.Format == s.Format && r.Compression == s.Codec && r.Dictionary == s.Dictionary && r.Level != ""
}

func validSummaryMetrics(o observation, goVersion string) bool {
	s := o.Spec
	return o.Worker.PackNS >= 1 && o.Worker.PackNS <= summaryMaximumExactInteger &&
		o.Worker.PackCPUNS >= 0 && o.Worker.PackCPUNS <= summaryMaximumExactInteger &&
		o.Worker.MaxRSSKiB >= 1 && o.Worker.MaxRSSKiB <= summaryMaximumExactInteger &&
		o.Artifact.Bytes >= 1 && o.Artifact.Bytes <= summaryMaximumExactInteger &&
		o.Worker.GoVersion == goVersion && o.Worker.GOMAXPROCS == s.CPU &&
		o.Worker.GoMemLimitBytes == s.MemoryMiB*mib*goMemoryNumerator/goMemoryDenominator &&
		summaryValidHash(o.Artifact.SHA256) && o.Artifact.Index != nil && len(o.Artifact.CompressedSHA256) > 0
}

func summarySucceeded(o observation) bool {
	return o.Error == "" && o.Resource.ExitCode == 0 && !o.Resource.OOMKilled && o.Resource.TelemetryAvailable &&
		o.Worker != nil && o.Worker.Error == "" && o.Artifact != nil && !o.Resource.OutputTruncated
}

func (a *summaryAccumulator) add(o observation) {
	a.observations[o.Spec.Block] = o
	a.group.Attempts++
	if o.Resource.OOMKilled {
		a.group.OOMKilled++
	}
	if !o.Resource.TelemetryAvailable {
		a.group.TelemetryUnavailable++
	}
	if o.Resource.MemoryPeakBytes > 0 {
		a.allPeak = append(a.allPeak, float64(o.Resource.MemoryPeakBytes))
	}
	if summarySucceeded(o) {
		a.group.Succeeded++
		a.wall, a.cpu = append(a.wall, float64(o.Worker.PackNS)), append(a.cpu, float64(o.Worker.PackCPUNS))
		a.rss, a.peak = append(a.rss, float64(o.Worker.MaxRSSKiB)), append(a.peak, float64(o.Resource.MemoryPeakBytes))
		a.size = append(a.size, float64(o.Artifact.Bytes))
	} else {
		a.group.Failed++
		a.group.Failures = append(a.group.Failures,
			summaryFailure{o.Spec.Block, o.Resource.ExitCode, o.Resource.OOMKilled, summaryFailureReason(o)})
		if o.Resource.MemoryPeakBytes > 0 {
			a.failedPeak = append(a.failedPeak, float64(o.Resource.MemoryPeakBytes))
		}
		if o.PriorOutputPreserved != nil && !*o.PriorOutputPreserved {
			a.group.FailedPriorOutputNotPreserved++
		}
	}
	if o.Worker != nil && o.Worker.Pool != nil {
		stats := o.Worker.Pool
		a.group.LogicalStatsSamples++
		a.admitted, a.budget = append(a.admitted, float64(stats.PeakAdmittedBytes)), append(a.budget, float64(stats.MemoryBudgetBytes))
		a.active = append(a.active, float64(stats.PeakActiveWorkers))
		if stats.ActiveWorkers != 0 || stats.AdmittedBytes != 0 || stats.PeakAdmittedBytes > stats.MemoryBudgetBytes ||
			(o.Request.Pool != nil && stats.PeakActiveWorkers > o.Request.Pool.MaxWorkers) {
			a.group.LogicalBoundViolations++
		}
	}
}

func summaryFailureReason(o observation) string {
	if o.Error != "" {
		return o.Error
	}
	if o.Worker != nil && o.Worker.Error != "" {
		return o.Worker.Error
	}
	return "nonzero/OOM/truncated/missing worker, artifact or telemetry; incomplete outcome retained"
}

func (a *summaryAccumulator) finish() summaryGroup {
	g := a.group
	slices.SortFunc(g.Failures, func(a, b summaryFailure) int { return a.Block - b.Block })
	g.PackWallNS, g.PackCPUNS, g.WorkerRSSKiB = summarizeSeries(a.wall), summarizeSeries(a.cpu), summarizeSeries(a.rss)
	g.CgroupPeakBytes, g.ArtifactBytes = summarizeSeries(a.peak), summarizeSeries(a.size)
	g.AllAttemptCgroupPeakBytes, g.FailedAttemptCgroupPeakBytes = summarizeSeries(a.allPeak), summarizeSeries(a.failedPeak)
	g.PeakAdmittedBytes, g.LogicalMemoryBudgetBytes, g.PeakActiveWorkers =
		summarizeSeries(a.admitted), summarizeSeries(a.budget), summarizeSeries(a.active)
	return g
}

func compareSummaryGroups(baseline, candidate *summaryAccumulator) summaryComparison {
	out := summaryComparison{Cohort: candidate.group.Cohort, Baseline: baseline.group.Mode, Candidate: candidate.group.Mode,
		BaselineAttempts: baseline.group.Attempts, CandidateAttempts: candidate.group.Attempts}
	var ratios, differences, sizeRatios []float64
	for block, base := range baseline.observations {
		other, exists := candidate.observations[block]
		if !exists {
			out.BaselineOnlyBlocks++
			continue
		}
		out.MatchedAttemptBlocks++
		if !summarySucceeded(base) || !summarySucceeded(other) {
			out.FailedPairs++
			continue
		}
		out.CompletePairs++
		ratios = append(ratios, float64(base.Worker.PackNS)/float64(other.Worker.PackNS))
		differences = append(differences, float64(other.Worker.PackNS)-float64(base.Worker.PackNS))
		sizeRatios = append(sizeRatios, float64(other.Artifact.Bytes)/float64(base.Artifact.Bytes))
		if maps.Equal(base.Artifact.CompressedSHA256, other.Artifact.CompressedSHA256) {
			out.PayloadBitstreamEqualPairs++
		}
		if base.Artifact.Index.DictionarySHA256 == other.Artifact.Index.DictionarySHA256 {
			out.DictionaryEqualPairs++
		}
		if base.Artifact.Bytes == other.Artifact.Bytes {
			out.CompleteArtifactSizeEqualPairs++
		}
		if base.Artifact.SHA256 == other.Artifact.SHA256 {
			out.CompleteArtifactHashEqualPairs++
		}
	}
	out.CandidateOnlyBlocks = candidate.group.Attempts - out.MatchedAttemptBlocks
	out.PackWallSpeedRatio, out.PairedWallDifferenceNS, out.ArtifactBytesRatio =
		summarizeSeries(ratios), summarizeSeries(differences), summarizeSeries(sizeRatios)
	return out
}

func summarizeSeries(values []float64) summarySeries {
	if len(values) == 0 {
		return summarySeries{}
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	middle := len(ordered) / summaryMedianDivisor
	median := ordered[middle]
	if len(ordered)%summaryMedianDivisor == 0 {
		median = ordered[middle-1] + (ordered[middle]-ordered[middle-1])/summaryMedianDivisor
	}
	return summarySeries{N: len(ordered), Median: &median, Min: &ordered[0], Max: &ordered[len(ordered)-1]}
}

func summaryValidHash(value string) bool {
	if len(value) != summaryHashLength {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
