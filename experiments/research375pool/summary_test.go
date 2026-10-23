package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func summaryFixtureObservation(mode string, block int, wall int64) observation {
	s := trialSpec{Family: "demo", Mode: mode, CPU: 4, MemoryMiB: 512, Block: block, Format: 2, Codec: "zstd", StubProfile: "full"}
	r := request{AppName: s.Family, Arch: "amd64", Compression: s.Codec, Format: s.Format, Level: "default"}
	w := &workerResult{PackNS: wall, PackCPUNS: wall / 2, MaxRSSKiB: 1024,
		GoVersion: "go1.27.3", GOMAXPROCS: s.CPU, GoMemLimitBytes: s.MemoryMiB * mib * goMemoryNumerator / goMemoryDenominator}
	if mode != summaryCurrent {
		workers := 3
		if mode == summarySerial {
			workers = 1
		}
		r.Pool = &pack.EncoderPoolOptions{MaxWorkers: workers, InnerConcurrency: 1, CPUBudget: 4,
			MemoryBudgetBytes: 384 * mib, WorkspaceBytesPerEncoder: 64 * mib}
		w.Pool = &pack.EncoderPoolStats{JobsTotal: 3, JobsStarted: 3, JobsCompleted: 3, PeakActiveWorkers: workers,
			PeakAdmittedBytes: 80 * mib, MemoryBudgetBytes: 384 * mib}
	}
	return observation{Spec: s, Request: r, Worker: w,
		Resource: resourceTrialResult{TelemetryAvailable: true, MemoryPeakBytes: 64 << 20,
			ActualCG: resourceControls{MemoryMaxBytes: 512 << 20, CPUQuotaUsec: 400000, CPUPeriodUsec: 100000}},
		Artifact: &artifactResult{Bytes: 1000, SHA256: strings.Repeat("a", summaryHashLength),
			CompressedSHA256: map[string]string{"v1": strings.Repeat("b", summaryHashLength)}, Index: &format.Index{}},
	}
}

func summaryFixtureReport(observations ...observation) campaignReport {
	return campaignReport{SchemaVersion: 1, ProducerRevision: campaignTestRevision,
		ProducerSHA256: strings.Repeat("c", summaryHashLength), GoVersion: "go1.27.3", Observations: observations}
}

func TestSummaryUsesPairedRatiosAndRetainsEveryDenominator(t *testing.T) {
	t.Parallel()
	var observations []observation
	for block, durations := range [][3]int64{{100, 80, 10}, {1000, 500, 900}, {20, 10, 40}} {
		for i, mode := range []string{summaryCurrent, summarySerial, summaryPool} {
			observations = append(observations, summaryFixtureObservation(mode, block, durations[i]))
		}
	}
	observations[5].Artifact.Bytes = 1200
	observations[5].Artifact.SHA256 = strings.Repeat("d", summaryHashLength)
	observations[5].Artifact.CompressedSHA256["v1"] = strings.Repeat("e", summaryHashLength)
	failed := summaryFixtureObservation(summaryCurrent, 3, 1)
	failed.Error, failed.Resource.ExitCode, failed.Resource.OOMKilled = "kernel OOM", 137, true
	failed.Worker, failed.Artifact, failed.Resource.MemoryPeakBytes = nil, nil, 512<<20
	observations = append(observations, failed, summaryFixtureObservation(summaryPool, 3, 20),
		summaryFixtureObservation(summaryCurrent, 4, 70), summaryFixtureObservation(summaryPool, 5, 30))
	report := summaryFixtureReport(observations...)
	summary, err := summarizeReport(report)
	require.NoError(t, err)
	reversed := report
	reversed.Observations = slices.Clone(report.Observations)
	slices.Reverse(reversed.Observations)
	again, err := summarizeReport(reversed)
	require.NoError(t, err)
	assert.Equal(t, summary, again, "report ordering must not change deterministic grouped or paired results")
	require.Len(t, summary.Groups, 3)
	require.Len(t, summary.Comparisons, 3)
	for _, group := range summary.Groups {
		if group.Mode == summaryCurrent {
			assert.Equal(t, 5, group.Attempts)
			assert.Equal(t, 4, group.Succeeded)
			assert.Equal(t, 1, group.Failed)
			assert.Equal(t, 1, group.OOMKilled)
			assert.Equal(t, 4, group.PackWallNS.N)
			assert.Equal(t, 5, group.AllAttemptCgroupPeakBytes.N)
			assert.Equal(t, float64(512<<20), *group.FailedAttemptCgroupPeakBytes.Median)
		}
	}
	for _, comparison := range summary.Comparisons {
		if comparison.Baseline == summaryCurrent && comparison.Candidate == summaryPool {
			assert.Equal(t, 4, comparison.MatchedAttemptBlocks)
			assert.Equal(t, 3, comparison.CompletePairs)
			assert.Equal(t, 1, comparison.FailedPairs)
			assert.Equal(t, 1, comparison.BaselineOnlyBlocks)
			assert.Equal(t, 1, comparison.CandidateOnlyBlocks)
			assert.InDelta(t, 1000.0/900, *comparison.PackWallSpeedRatio.Median, 1e-12)
			assert.Equal(t, 2, comparison.PayloadBitstreamEqualPairs)
			assert.Equal(t, 3, comparison.DictionaryEqualPairs)
			assert.Equal(t, 2, comparison.CompleteArtifactSizeEqualPairs)
			assert.Equal(t, 2, comparison.CompleteArtifactHashEqualPairs)
		}
	}
}

func TestSummaryFailuresAndLogicalBoundsAreVisible(t *testing.T) {
	t.Parallel()
	failed := summaryFixtureObservation(summaryPool, 0, 1)
	failed.Worker.Error, failed.Resource.ExitCode = "admission rejected", 1
	failed.Worker.Pool.PeakAdmittedBytes = failed.Worker.Pool.MemoryBudgetBytes + 1
	failed.Worker.Pool.ActiveWorkers, failed.Worker.Pool.AdmittedBytes = 1, 1
	failed.PriorOutputPreserved = new(false)
	missing := summaryFixtureObservation(summaryPool, 1, 1)
	missing.Resource.TelemetryAvailable, missing.Resource.MemoryPeakBytes, missing.Worker, missing.Artifact = false, 0, nil, nil
	result, err := summarizeReport(summaryFixtureReport(failed, missing))
	require.NoError(t, err)
	require.Len(t, result.Groups, 1)
	group := result.Groups[0]
	assert.Equal(t, 2, group.Attempts)
	assert.Equal(t, 2, group.Failed)
	assert.Equal(t, 1, group.TelemetryUnavailable)
	assert.Equal(t, 1, group.FailedPriorOutputNotPreserved)
	assert.Equal(t, 1, group.LogicalBoundViolations)
	assert.Equal(t, 1, group.LogicalStatsSamples)
	assert.Nil(t, group.PackWallNS.Median)
	assert.Equal(t, "admission rejected", group.Failures[0].Reason)
	assert.NotEmpty(t, group.Failures[1].Reason)
}

func TestSummaryRejectsAmbiguousOrInvalidInputs(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*campaignReport){
		"schema":            func(r *campaignReport) { r.SchemaVersion = 0 },
		"revision":          func(r *campaignReport) { r.ProducerRevision = "bad" },
		"producer hash":     func(r *campaignReport) { r.ProducerSHA256 = strings.Repeat("z", summaryHashLength) },
		"toolchain":         func(r *campaignReport) { r.GoVersion = "go1.26" },
		"empty":             func(r *campaignReport) { r.Observations = nil },
		"cap":               func(r *campaignReport) { r.Observations = make([]observation, summaryMaximumObservations+1) },
		"family":            func(r *campaignReport) { r.Observations[0].Spec.Family = "" },
		"mode":              func(r *campaignReport) { r.Observations[0].Spec.Mode = "unknown" },
		"CPU":               func(r *campaignReport) { r.Observations[0].Spec.CPU = 0 },
		"memory":            func(r *campaignReport) { r.Observations[0].Spec.MemoryMiB = 0 },
		"block":             func(r *campaignReport) { r.Observations[0].Spec.Block = -1 },
		"format":            func(r *campaignReport) { r.Observations[0].Spec.Format = 0 },
		"codec":             func(r *campaignReport) { r.Observations[0].Spec.Codec = "deflate" },
		"stub":              func(r *campaignReport) { r.Observations[0].Spec.StubProfile = "hybrid" },
		"admission":         func(r *campaignReport) { r.Observations[0].Spec.AdmissionMiB = -1 },
		"request mismatch":  func(r *campaignReport) { r.Observations[0].Request.AppName = "other" },
		"level":             func(r *campaignReport) { r.Observations[0].Request.Level = "" },
		"memory controls":   func(r *campaignReport) { r.Observations[0].Resource.ActualCG.MemoryMaxBytes = 1 },
		"swap controls":     func(r *campaignReport) { r.Observations[0].Resource.ActualCG.MemorySwapMaxBytes = 1 },
		"CPU period":        func(r *campaignReport) { r.Observations[0].Resource.ActualCG.CPUPeriodUsec = 0 },
		"CPU quota":         func(r *campaignReport) { r.Observations[0].Resource.ActualCG.CPUQuotaUsec = 1 },
		"wall":              func(r *campaignReport) { r.Observations[0].Worker.PackNS = 0 },
		"CPU metric":        func(r *campaignReport) { r.Observations[0].Worker.PackCPUNS = -1 },
		"RSS":               func(r *campaignReport) { r.Observations[0].Worker.MaxRSSKiB = 0 },
		"worker Go":         func(r *campaignReport) { r.Observations[0].Worker.GoVersion = "bad" },
		"worker procs":      func(r *campaignReport) { r.Observations[0].Worker.GOMAXPROCS = 1 },
		"worker Go memory":  func(r *campaignReport) { r.Observations[0].Worker.GoMemLimitBytes = 0 },
		"artifact bytes":    func(r *campaignReport) { r.Observations[0].Artifact.Bytes = 0 },
		"artifact hash":     func(r *campaignReport) { r.Observations[0].Artifact.SHA256 = "bad" },
		"artifact index":    func(r *campaignReport) { r.Observations[0].Artifact.Index = nil },
		"artifact payloads": func(r *campaignReport) { r.Observations[0].Artifact.CompressedSHA256 = nil },
		"peak precision":    func(r *campaignReport) { r.Observations[0].Resource.MemoryPeakBytes = summaryMaximumExactInteger + 1 },
		"duplicate block":   func(r *campaignReport) { r.Observations = append(r.Observations, r.Observations[0]) },
		"changing settings": func(r *campaignReport) {
			next := summaryFixtureObservation(summaryPool, 1, 100)
			next.Request.Pool.WorkspaceBytesPerEncoder++
			r.Observations = append(r.Observations, next)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			report := summaryFixtureReport(summaryFixtureObservation(summaryPool, 0, 100))
			mutate(&report)
			_, err := summarizeReport(report)
			require.ErrorIs(t, err, ErrSummaryInput)
		})
	}
}

func TestSummarySeriesAndDictionaryComparison(t *testing.T) {
	t.Parallel()
	assert.Nil(t, summarizeSeries(nil).Median)
	assert.Equal(t, 1.0, *summarizeSeries([]float64{1}).Median)
	assert.Equal(t, 2.0, *summarizeSeries([]float64{3, 1}).Median)
	baseline, candidate := summaryFixtureObservation(summaryCurrent, 0, 100), summaryFixtureObservation(summaryPool, 0, 50)
	candidate.Artifact.Index.DictionarySHA256 = strings.Repeat("f", summaryHashLength)
	result, err := summarizeReport(summaryFixtureReport(baseline, candidate))
	require.NoError(t, err)
	assert.Zero(t, result.Comparisons[0].DictionaryEqualPairs)
	candidate.Resource.ExitCode = 1
	result, err = summarizeReport(summaryFixtureReport(baseline, candidate))
	require.NoError(t, err)
	assert.Zero(t, result.Comparisons[0].CompletePairs)
	assert.Nil(t, result.Comparisons[0].PackWallSpeedRatio.Median)
}
