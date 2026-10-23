package main

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func successfulRunner(ctx context.Context, binary string, args, env []string) processResult {
	if err := ctx.Err(); err != nil {
		return processResult{Stats: processStats{ExitCode: -1, Error: err.Error(), TimedOut: errors.Is(err, context.DeadlineExceeded)}}
	}
	if binary == "/tool" {
		return processResult{Stats: processStats{WallNS: 10, CPUNS: 5}, Stdout: []byte(`{"results":[{"level":"v1","valid":true}]}`)}
	}
	telemetry := fmt.Sprintf(`[microfat] {"event":"dispatch","host_arch":%q,"selected_variant":"v1",`+
		`"selected_sha256":%q,"selected_size_bytes":3,"exec_mode":"cache"}`, runtime.GOARCH, strings.Repeat("a", 64))
	return processResult{Stats: processStats{WallNS: 1, CPUNS: 2, StdoutSHA256: strings.Repeat("a", 64)}, Stderr: []byte(telemetry)}
}

func TestEpisodeJournalRejectsNonfiniteAccounting(t *testing.T) {
	t.Parallel()
	require.Error(t, appendEpisode(filepath.Join(t.TempDir(), "journal"), episode{ByteTime: math.NaN()}))
}

func TestFirstReadyIsEarliestSuccessfulCompletionRatherThanIndexZero(t *testing.T) {
	t.Parallel()
	ep := episode{InitialDelayNS: 10, Preparation: &processStats{CPUNS: 5}, Launches: []launch{
		{Index: 0, ReadyNS: 100, ScheduledToReadyNS: 100, Process: processStats{CPUNS: 1}},
		{Index: 1, ReadyNS: 20, ScheduledToReadyNS: 20, Process: processStats{CPUNS: 2}},
		{Index: 2, ReadyNS: 5, ScheduledToReadyNS: 5, Failed: true}}}
	recordLaunchCosts(&ep)
	require.NotNil(t, ep.DeploymentFirstReadyNS)
	assert.Equal(t, int64(30), *ep.DeploymentFirstReadyNS)
	assert.Equal(t, int64(110), ep.DeploymentSpanNS)
	assert.Equal(t, int64(8), ep.TotalCPUNS)
	assert.True(t, ep.Failed)
	ep = episode{Launches: []launch{{Failed: true, ReadyNS: 1, ScheduledToReadyNS: 1}}}
	recordLaunchCosts(&ep)
	assert.Nil(t, ep.DeploymentFirstReadyNS)
}

func TestDispatchEvidenceRejectsMissingMismatchedAndRepeatedEvents(t *testing.T) {
	t.Parallel()
	app := fixtureRequest().Apps[0]
	good := successfulRunner(context.Background(), "/artifact", nil, nil).Stderr
	for name, data := range map[string][]byte{"missing": []byte("application stderr"), "malformed": []byte("[microfat] {"),
		"wrong event": []byte(`[microfat] {"event":"other"}`),
		"repeated":    append(append([]byte(nil), good...), append([]byte("\n"), good...)...)} {
		t.Run(name, func(t *testing.T) { t.Parallel(); require.Error(t, readDispatch(&launch{}, data, app, armBaseline)) })
	}
	var value format.DispatchTelemetry
	require.NoError(t, json.Unmarshal(good[len("[microfat] "):], &value))
	for name, change := range map[string]func(*format.DispatchTelemetry){
		"digest":       func(v *format.DispatchTelemetry) { v.SelectedSHA256 = "other" },
		"level":        func(v *format.DispatchTelemetry) { v.SelectedVariant = "v2" },
		"size":         func(v *format.DispatchTelemetry) { v.SelectedSizeBytes = 4 },
		"arch":         func(v *format.DispatchTelemetry) { v.HostArch = "other" },
		"mode":         func(v *format.DispatchTelemetry) { v.ExecMode = "other" },
		"prewarm miss": func(v *format.DispatchTelemetry) { v.ExecMode = format.ExecModeMemfd },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := value
			change(&v)
			data, err := json.Marshal(v)
			require.NoError(t, err)
			require.Error(t, readDispatch(&launch{}, append([]byte("[microfat] "), data...), app, armPrewarm))
		})
	}
	value.ExecMode = format.ExecModeMemfd
	data, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, readDispatch(&launch{}, append([]byte("[microfat] "), data...), app, armBaseline))
}

func TestCampaignRetainsPartialAttemptAndWriteFailure(t *testing.T) {
	in := campaignFixture(t)
	original := producerRevision
	producerRevision = in.SourceRevision
	t.Cleanup(func() { producerRevision = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := func(ctx context.Context, binary string, args, env []string) processResult {
		cancel()
		return successfulRunner(ctx, binary, args, env)
	}
	result, err := runCampaign(ctx, in, in.CorpusSHA256, filepath.Join(t.TempDir(), "partial"), runner)
	require.NoError(t, err)
	assert.False(t, result.Complete)
	require.Len(t, result.Episodes, 1)
	assert.True(t, result.Episodes[0].Failed)
	directory := filepath.Join(t.TempDir(), "write-failure")
	runner = func(ctx context.Context, binary string, args, env []string) processResult {
		_ = os.RemoveAll(directory)
		return successfulRunner(ctx, binary, args, env)
	}
	_, err = runCampaign(context.Background(), in, in.CorpusSHA256, directory, runner)
	require.Error(t, err)
	in.ToolSHA256 = strings.Repeat("b", 64)
	_, err = runCampaign(context.Background(), in, in.CorpusSHA256, filepath.Join(t.TempDir(), "digest"), runner)
	require.Error(t, err)
}

func TestEpisodeAccountsPreparationAndCompleteBurst(t *testing.T) {
	t.Parallel()
	in := fixtureRequest()
	sc := in.Scenarios[0]
	sc.OffsetsNS = []int64{0, 0, 0}
	for _, arm := range []string{armBaseline, armPrewarm} {
		t.Run(arm, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			ep := runEpisode(context.Background(), in, in.Apps[0], sc, arm, 0, directory, successfulRunner)
			assert.False(t, ep.Failed)
			require.Len(t, ep.Launches, 3)
			assert.Equal(t, int64(6), ep.ForegroundCPUNS)
			assert.Positive(t, ep.MaxReadyNS)
			assert.Zero(t, ep.ByteTime)
			if arm == armPrewarm {
				require.NotNil(t, ep.Preparation)
				assert.Equal(t, int64(10), ep.InitialDelayNS)
				require.NotNil(t, ep.Verification)
			}
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			assert.Empty(t, entries)
		})
	}
}

func TestEpisodeRetainsPreparationFailureOutputMismatchAndTimeout(t *testing.T) {
	t.Parallel()
	in := fixtureRequest()
	failed := func(context.Context, string, []string, []string) processResult {
		return processResult{Stats: processStats{ExitCode: 1, Error: "failed", TimedOut: true}}
	}
	ep := runEpisode(context.Background(), in, in.Apps[0], in.Scenarios[0], armPrewarm, 0, t.TempDir(), failed)
	assert.True(t, ep.Failed)
	assert.True(t, ep.TimedOut)
	require.Len(t, ep.Launches, 1)
	require.NotNil(t, ep.Preparation)
	assert.Contains(t, ep.Error, "verification")
	in.Apps[0].StdoutSHA256 = strings.Repeat("b", 64)
	ep = runEpisode(context.Background(), in, in.Apps[0], in.Scenarios[0], armBaseline, 0, t.TempDir(), successfulRunner)
	assert.True(t, ep.Failed)
	ep = runEpisode(context.Background(), in, in.Apps[0], in.Scenarios[0], armBaseline, 0, "/does-not-exist/research380", successfulRunner)
	assert.True(t, ep.Failed)
	assert.Empty(t, ep.Launches)
}

func TestScheduledCancellationAndCacheInventory(t *testing.T) {
	t.Parallel()
	in := fixtureRequest()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	launches := runLaunches(ctx, in, in.Apps[0], in.Scenarios[0], armBaseline, nil, successfulRunner)
	require.Len(t, launches, 1)
	assert.True(t, launches[0].Failed)
	assert.Contains(t, launches[0].Process.Error, "canceled")
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "payload"), []byte("abc"), 0o600))
	size, count, err := cacheSize(directory)
	require.NoError(t, err)
	assert.Equal(t, int64(3), size)
	assert.Equal(t, 1, count)
	require.NoError(t, os.Symlink("payload", filepath.Join(directory, "link")))
	_, _, err = cacheSize(directory)
	require.Error(t, err)
	_, _, err = cacheSize(filepath.Join(directory, "missing"))
	require.Error(t, err)
	directory = t.TempDir()
	for index := 0; index <= maximumCacheFiles; index++ {
		require.NoError(t, os.WriteFile(filepath.Join(directory, strings.Repeat("a", index+1)), nil, 0o600))
	}
	_, _, err = cacheSize(directory)
	require.Error(t, err)
}

func TestPrewarmMustSelectAndVerifyFrozenVariant(t *testing.T) {
	t.Parallel()
	for _, data := range []string{`not json`, `{"results":[]}`, `{"results":[{"level":"v2","valid":true}]}`,
		`{"results":[{"level":"v1","valid":false}]}`} {
		err := checkPrewarm(processResult{Stdout: []byte(data)}, "v1", true)
		require.Error(t, err)
	}
}

func campaignFixture(t *testing.T) request {
	t.Helper()
	in := fixtureRequest()
	path := filepath.Join(t.TempDir(), "executable")
	require.NoError(t, os.WriteFile(path, []byte("known test executable identity"), 0o700))
	digest, err := hashFile(path)
	require.NoError(t, err)
	in.Tool, in.ToolSHA256 = path, digest
	in.Apps[0].Artifact, in.Apps[0].ArtifactSHA256 = path, digest
	return in
}

func TestCampaignFixedDenominatorsRotationAndBudgetStop(t *testing.T) {
	in := campaignFixture(t)
	original := producerRevision
	producerRevision = in.SourceRevision
	t.Cleanup(func() { producerRevision = original })
	var lock sync.Mutex
	var order []string
	runner := func(ctx context.Context, binary string, args, env []string) processResult {
		lock.Lock()
		order = append(order, args[0])
		lock.Unlock()
		if args[0] == testPrewarmArgument {
			return successfulRunner(ctx, "/tool", args, env)
		}
		return successfulRunner(ctx, binary, args, env)
	}
	directory := filepath.Join(t.TempDir(), "campaign")
	result, err := runCampaign(context.Background(), in, in.CorpusSHA256, directory, runner)
	require.NoError(t, err)
	assert.True(t, result.Complete)
	require.Len(t, result.Episodes, 4)
	assert.Equal(t, armBaseline, result.Episodes[0].Arm)
	assert.Equal(t, armPrewarm, result.Episodes[2].Arm)
	assert.Equal(t, []string{testVersionArgument, testPrewarmArgument, testVersionArgument, testPrewarmArgument,
		testPrewarmArgument, testVersionArgument, testPrewarmArgument, testVersionArgument}, order)
	_, err = os.Stat(filepath.Join(directory, "report.json"))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = runCampaign(ctx, in, in.CorpusSHA256, filepath.Join(t.TempDir(), "stopped"), runner)
	require.NoError(t, err)
	assert.False(t, result.Complete)
	assert.Empty(t, result.Episodes)
	assert.NotEmpty(t, result.StopReason)
	_, err = runCampaign(context.Background(), in, in.CorpusSHA256, directory, runner)
	require.Error(t, err)
	in.SourceRevision = "wrong"
	_, err = runCampaign(context.Background(), in, in.CorpusSHA256, filepath.Join(t.TempDir(), "wrong"), runner)
	require.Error(t, err)
}

func TestExecutableIdentityAndCampaignErrors(t *testing.T) {
	in := campaignFixture(t)
	require.NoError(t, checkExecutables(in))
	in.Apps[0].ArtifactSHA256 = strings.Repeat("b", 64)
	require.Error(t, checkExecutables(in))
	in.Apps[0].Artifact = "/missing"
	require.Error(t, checkExecutables(in))
	in = campaignFixture(t)
	in.ToolSHA256 = strings.Repeat("b", 64)
	require.Error(t, checkExecutables(in))
	in.SLONS = 0
	_, err := runCampaign(context.Background(), in, "", "", successfulRunner)
	require.Error(t, err)
	_, err = saveCampaign("/does-not-exist/research380", report{}, time.Now())
	require.Error(t, err)
}
