package main

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testVersionArgument = "--version"
	testPrewarmArgument = "prewarm"
	testModeFlag        = "--mode"
	testOutputFlag      = "--output"
	testKnownFamily     = "known"
	testRequestFlag     = "--request"
	testOther           = "other"
)

func fixtureRequest() request {
	sha := strings.Repeat("a", 64)
	return request{SchemaVersion: 1, Phase: phasePilot, ProtocolID: "pilot-v1", SourceRevision: "test-source", Host: "test-host",
		CorpusSHA256: sha, Tool: "/tool", ToolSHA256: sha, SamplesPerArm: 2, TimeoutNS: int64(time.Second),
		CampaignBudgetNS: int64(time.Minute), GOMAXPROCS: 1, SLONS: 100, Margin: .1, Alpha: .05, FamilySize: 1,
		Apps: []application{{Family: "test", Artifact: "/artifact", ArtifactSHA256: sha, Level: "v1",
			Args: []string{testVersionArgument}, StdoutSHA256: sha, PayloadSHA256: sha, PayloadBytes: 3}},
		Scenarios: []scenario{{Name: "one-off", OffsetsNS: []int64{0}, RetentionNS: int64(time.Second)}}}
}

func TestCostSummaryExcludesFailedLatencyButKeepsRiskDenominators(t *testing.T) {
	t.Parallel()
	episodes := []episode{{Preparation: &processStats{WallNS: 10, InputBlocks: 2}, MaxReadyNS: 5, TotalCPUNS: 20, ByteTime: 4,
		Launches: []launch{{Process: processStats{InputBlocks: 1, OutputBlocks: 3}}}},
		{Failed: true, MaxReadyNS: 1},
		{Preparation: &processStats{WallNS: 30, InputBlocks: 4}, MaxReadyNS: 15, TotalCPUNS: 40, ByteTime: 8,
			Launches: []launch{{Process: processStats{InputBlocks: 3, OutputBlocks: 5}}}}}
	costs := summarizeCosts(episodes)
	require.NotNil(t, costs)
	assert.Equal(t, 2, costs.Samples)
	assert.Equal(t, float64(20), costs.PreparationWall)
	assert.Equal(t, float64(30), costs.CPU)
	assert.Equal(t, float64(6), costs.ByteTime)
	assert.Equal(t, int64(10), costs.InputBlocks)
	assert.Equal(t, int64(8), costs.OutputBlocks)
	assert.Equal(t, float64(2), median([]float64{3, 1, 2}))
	assert.Nil(t, summarizeCosts([]episode{{TimedOut: true}}))
	in := fixtureReport()
	in.Complete = false
	app := in.Request.Apps[0]
	app.Family = testOther
	in.Request.Apps = append(in.Request.Apps, app)
	in.Request.FamilySize = 2
	result, err := analyzeReport(in)
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Zero(t, result[1].BaselineAttempts)
	in = fixtureReport()
	in.Episodes[2].ID = in.Episodes[0].ID
	_, err = analyzeReport(in)
	require.Error(t, err)
}

func TestRequestBoundsAndIdentity(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateRequest(fixtureRequest()))
	for name, change := range map[string]func(*request){
		"schema": func(in *request) { in.SchemaVersion = 0 }, "phase": func(in *request) { in.Phase = "adaptive" },
		"source": func(in *request) { in.SourceRevision = "" }, "tool": func(in *request) { in.Tool = "relative" },
		"digest": func(in *request) { in.ToolSHA256 = "zz" }, "confirmation needs pilot": func(in *request) { in.Phase = phaseConfirmation },
		"sample cap": func(in *request) { in.SamplesPerArm = maximumEpisodes + 1 },
		"process cap": func(in *request) {
			in.SamplesPerArm = maximumEpisodes
			in.Scenarios[0].OffsetsNS = make([]int64, maximumLaunches)
		},
		"timeout cap":  func(in *request) { in.TimeoutNS = int64(maximumTimeout) + 1 },
		"campaign cap": func(in *request) { in.CampaignBudgetNS = int64(maximumCampaign) + 1 },
		"gomaxprocs":   func(in *request) { in.GOMAXPROCS = 0 }, "slo": func(in *request) { in.SLONS = 0 },
		"no apps": func(in *request) { in.Apps = nil }, "no scenarios": func(in *request) { in.Scenarios = nil },
		"family": func(in *request) { in.FamilySize = 0 }, "alpha": func(in *request) { in.Alpha = math.NaN() },
		"margin":             func(in *request) { in.Margin = 0 },
		"repeated app":       func(in *request) { in.Apps = append(in.Apps, in.Apps[0]); in.FamilySize = 2 },
		"app identity":       func(in *request) { in.Apps[0].StdoutSHA256 = "" },
		"repeated scenario":  func(in *request) { in.Scenarios = append(in.Scenarios, in.Scenarios[0]); in.FamilySize = 2 },
		"empty schedule":     func(in *request) { in.Scenarios[0].OffsetsNS = nil },
		"negative offset":    func(in *request) { in.Scenarios[0].OffsetsNS = []int64{-1} },
		"unordered schedule": func(in *request) { in.Scenarios[0].OffsetsNS = []int64{10, 0} },
		"beyond horizon":     func(in *request) { in.Scenarios[0].OffsetsNS = []int64{int64(time.Second) + 1} },
		"horizon cap":        func(in *request) { in.Scenarios[0].RetentionNS = int64(maximumHorizon) + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in := fixtureRequest()
			change(&in)
			require.Error(t, validateRequest(in))
		})
	}
}

func fixtureReport() report {
	in := fixtureRequest()
	in.Phase = phaseConfirmation
	in.PilotReportSHA256 = in.CorpusSHA256
	in.IID = true
	result := report{SchemaVersion: 1, Request: in, RequestSHA256: in.CorpusSHA256,
		ProducerRevision: "source", GoVersion: "go1.27.3", Complete: true}
	for block := 0; block < in.SamplesPerArm; block++ {
		for _, arm := range []string{armBaseline, armPrewarm} {
			result.Episodes = append(result.Episodes, episode{ID: fmt.Sprintf("%s/%d", arm, block),
				Family: "test", Scenario: "one-off", Arm: arm, Block: block, MaxReadyNS: 10,
				Launches: []launch{{ReadyNS: 10, ScheduledToReadyNS: 10}}})
			if arm == armPrewarm {
				result.Episodes[len(result.Episodes)-1].Preparation = &processStats{}
			}
		}
	}
	return result
}

func TestAnalysisRetainsEpisodesAndCountsFailures(t *testing.T) {
	t.Parallel()
	in := fixtureReport()
	in.Episodes[0].Failed = true
	in.Episodes[1].TimedOut = true
	in.Episodes[2].MaxReadyNS = 101
	in.Episodes[2].Launches[0].ReadyNS = 101
	in.Episodes[2].Launches[0].ScheduledToReadyNS = 101
	result, err := analyzeReport(in)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, 2, result[0].BaselineAttempts)
	assert.Equal(t, 2, result[0].PrewarmAttempts)
	assert.Equal(t, 1, result[0].BaselineFailures)
	assert.Equal(t, 2, result[0].BaselineViolations)
	assert.Equal(t, 1, result[0].PrewarmViolations)
	require.NotNil(t, result[0].Inference)
	assert.Equal(t, "inconclusive", result[0].Inference.Decision)
	assert.Equal(t, 1, result[0].Inference.Candidate.Timeouts)
	in.Request.Phase = phasePilot
	result, err = analyzeReport(in)
	require.NoError(t, err)
	assert.Nil(t, result[0].Inference)
	in.Request.Phase = phaseConfirmation
	in.Request.IID = false
	result, err = analyzeReport(in)
	require.NoError(t, err)
	assert.Nil(t, result[0].Inference)
}

func TestAnalysisRejectsFabricatedCompletenessAndDuplicates(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*report){
		"identity": func(in *report) { in.RequestSHA256 = "" }, "bad plan": func(in *report) { in.Request.SLONS = 0 },
		"duplicate block": func(in *report) { in.Episodes = append(in.Episodes, in.Episodes[0]) },
		"empty id":        func(in *report) { in.Episodes[0].ID = "" }, "unknown group": func(in *report) { in.Episodes[0].Family = "else" },
		"unknown scenario": func(in *report) { in.Episodes[0].Scenario = "else" },
		"unknown arm":      func(in *report) { in.Episodes[0].Arm = "else" },
		"negative":         func(in *report) { in.Episodes[0].MaxReadyNS = -1 }, "block cap": func(in *report) { in.Episodes[0].Block = 2 },
		"missing sample": func(in *report) { in.Episodes = in.Episodes[1:] },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in := fixtureReport()
			change(&in)
			_, err := analyzeReport(in)
			require.Error(t, err)
		})
	}
	in := fixtureReport()
	in.Complete = false
	in.Episodes = in.Episodes[1:]
	result, err := analyzeReport(in)
	require.NoError(t, err)
	assert.Nil(t, result[0].Inference)
}

func TestRawEpisodeConsistencyRejectsFalseSuccess(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*episode){
		"missing launches":  func(ep *episode) { ep.Launches = nil },
		"missing prewarm":   func(ep *episode) { ep.Preparation = nil },
		"invalid byte-time": func(ep *episode) { ep.ByteTime = math.NaN() },
		"wrong maximum":     func(ep *episode) { ep.MaxReadyNS++ },
		"index":             func(ep *episode) { ep.Launches[0].Index = -1 },
		"schedule":          func(ep *episode) { ep.Launches[0].ScheduledNS++ },
		"arrival duration":  func(ep *episode) { ep.Launches[0].ScheduledToReadyNS++ },
		"false success":     func(ep *episode) { ep.Launches[0].Failed = true },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in := fixtureReport()
			change(&in.Episodes[1])
			_, err := analyzeReport(in)
			require.Error(t, err)
		})
	}
	in := fixtureReport()
	in.Episodes[0].Launches = nil
	in.Episodes[0].MaxReadyNS = 0
	in.Episodes[0].Failed = true
	_, err := analyzeReport(in)
	require.NoError(t, err)
}
