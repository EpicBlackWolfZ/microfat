package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
)

func fixtureBaseline(t *testing.T, dictionary bool) (baseline, group, [][]byte, []byte, []byte) {
	t.Helper()
	m, _, stubPath := fixtureCorpus(t)
	groups, err := validateManifest(m)
	require.NoError(t, err)
	g := groups[0]
	inputs, _, err := readGroup(g, m.Architecture)
	require.NoError(t, err)
	stub, err := os.ReadFile(stubPath)
	require.NoError(t, err)
	b := packBaseline(g, m.Architecture, stubPath, filepath.Join(t.TempDir(), "baseline.fat"), dictionary, inputs, stub)
	require.Empty(t, b.Error)
	data, err := os.ReadFile(b.Path)
	require.NoError(t, err)
	return b, g, inputs, stub, data
}

func TestCompleteBaselineAndRepresentationAccounting(t *testing.T) {
	t.Parallel()
	for _, dictionary := range []bool{false, true} {
		b, g, inputs, stub, data := fixtureBaseline(t, dictionary)
		require.Equal(t, maxVariants, b.Exact)
		require.EqualValues(t, len(data), b.Bytes)
		require.Greater(t, b.MetadataBytes, int64(format.TrailerSize))
		var total int64
		for _, v := range b.Index.Variants {
			total += v.CompressedSize
		}
		require.Equal(t, b.Bytes, int64(len(stub))+b.Index.DictionarySize+total+b.MetadataBytes)
		levels := []string{"v1", "v2", "v3"}
		inv, err := buildInventory(inputs, methodSmall)
		require.NoError(t, err)
		m := sharingModel(inv, levels, inputs, methodSmall)
		c := evaluateModel(m, inv, int64(len(stub)), b, filepath.Join(t.TempDir(), "candidate"))
		require.Empty(t, c.Error)
		require.Equal(t, maxVariants, c.Exact)
		require.Equal(t, c.OptimisticEstimate+decoderReserve, c.CompleteEstimate)
		require.Equal(t, c.OptimisticEstimate+largeReserve, c.LargeEstimate)
		require.Equal(t, c.OptimisticEstimate, c.ContainerBytes+int64(len(stub)))
		require.GreaterOrEqual(t, c.OptimisticSaving, c.Saving)
		require.GreaterOrEqual(t, c.Saving, c.LargeSaving)
		for _, cost := range c.Selected {
			require.GreaterOrEqual(t, cost.WorkspaceBytes, int64(codecReserve+c.Header.Pool.RawBytes+len(inputs[0])))
			require.GreaterOrEqual(t, cost.DecodedBytes, int64(c.Header.Pool.RawBytes))
			require.Greater(t, cost.ReadBytes, int64(c.Header.Pool.Bytes))
		}
		expensive := *c.Header
		expensive.Pool.RawBytes = workspaceBudget
		for _, cost := range selectedCosts(expensive, int(c.ContainerBytes), b) {
			require.False(t, cost.WithinScreeningBudget)
			require.Greater(t, cost.WorkspaceBytes, int64(workspaceBudget))
		}
		require.Error(t, verifyBaseline([]byte("bad"), g, inputs, stub, &baseline{}))
		require.Error(t, verifyBaseline(data, g, inputs[:1], stub, &baseline{}))
		require.Error(t, verifyBaseline(data, g, inputs, []byte("wrong"), &baseline{}))
		wrong := append([][]byte(nil), inputs...)
		wrong[0] = []byte("wrong")
		require.ErrorContains(t, verifyBaseline(data, g, wrong, stub, &baseline{}), "byte comparison")
		wrongGroup := g
		wrongGroup.variants = append([]corpusVariant(nil), g.variants...)
		wrongGroup.variants[0].SHA256 = digest([]byte("wrong"))
		require.ErrorContains(t, verifyBaseline(data, wrongGroup, inputs, stub, &baseline{}), "pinned corpus")
		// A proper prefix is insufficient: exact first payload/dictionary boundaries matter.
		require.ErrorContains(t, verifyBaseline(data, g, inputs, stub[:1], &baseline{}), "boundary")
	}
}

func TestCandidateFailuresRetainNegativeResults(t *testing.T) {
	t.Parallel()
	b, _, _, stub, _ := fixtureBaseline(t, false)
	m := fixtureModel(t)
	path := filepath.Join(t.TempDir(), "exists")
	require.NoError(t, os.WriteFile(path, []byte("retained"), privateFileMode))
	c := evaluateModel(m, inventory{}, int64(len(stub)), b, path)
	require.NotEmpty(t, c.Error)
	retained, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "retained", string(retained))
	m.method = string([]byte{0xff})
	c = evaluateModel(m, inventory{}, int64(len(stub)), b, filepath.Join(t.TempDir(), "bad"))
	require.NotEmpty(t, c.Error)
	m = fixtureModel(t)
	m.targets[0].path = []span{{shared: true, offset: len(m.pool), size: 1}}
	c = evaluateModel(m, inventory{}, int64(len(stub)), b, filepath.Join(t.TempDir(), "bad"))
	require.NotEmpty(t, c.Error)
}

func TestScreenGroupKeepsReadBaselineAndSectionFailures(t *testing.T) {
	t.Parallel()
	m, _, stubPath := fixtureCorpus(t)
	groups, err := validateManifest(m)
	require.NoError(t, err)
	stub, err := os.ReadFile(stubPath)
	require.NoError(t, err)
	g := groups[0]
	result := screenGroup(g, m.Architecture, stubPath, t.TempDir(), stub)
	require.Empty(t, result.Error)
	require.Len(t, result.Baselines, 2)
	require.Len(t, result.Candidates, 4+maxVariants)
	for _, c := range result.Candidates {
		require.Empty(t, c.Error)
		require.Equal(t, maxVariants, c.Exact)
	}
	bad := g
	bad.variants = append([]corpusVariant(nil), g.variants...)
	bad.variants[0].Path = filepath.Join(t.TempDir(), "missing")
	require.NotEmpty(t, screenGroup(bad, m.Architecture, stubPath, t.TempDir(), stub).Error)
	require.Contains(t, screenGroup(g, m.Architecture, "missing", t.TempDir(), stub).Error, "control failed")
	// One-tier diagnostics retain dictionary failure rather than silently omitting it.
	one := g
	one.variants = g.variants[:1]
	oneResult := screenGroup(one, m.Architecture, stubPath, t.TempDir(), stub)
	require.Empty(t, oneResult.Error)
	require.NotEmpty(t, oneResult.Baselines[1].Error)
}

func gateGroups() []groupResult {
	var groups []groupResult
	for _, family := range []string{"alpha", "beta", "gamma", "delta"} {
		for _, profile := range []string{stripped, unstripped} {
			groups = append(groups, groupResult{Family: family, Profile: profile, Candidates: []candidate{{
				Method: methodCDC, SizePass: true, OptimisticSizePass: true, ScreeningPass: true}}})
		}
	}
	return groups
}

func TestGateRequiresBothProfilesAndThreeIndependentFamilies(t *testing.T) {
	t.Parallel()
	groups := gateGroups()
	result := summarizeGate(groups)
	require.Equal(t, maxFamilies, result.CostedSizePass)
	require.True(t, result.ModeledScreenPass)
	// A profile loss removes one family, not half an observation.
	groups[0].Candidates[0].SizePass, groups[0].Candidates[0].ScreeningPass = false, false
	require.True(t, summarizeGate(groups).CostedOpportunityPass)
	groups[2].Candidates[0].SizePass = false
	result = summarizeGate(groups)
	require.False(t, result.CostedOpportunityPass)
	require.Contains(t, result.Decision, "decoder cost")
	groups = gateGroups()
	groups[0].Candidates[0].ScreeningPass = false
	groups[2].Candidates[0].ScreeningPass = false
	result = summarizeGate(groups)
	require.True(t, result.CostedOpportunityPass)
	require.False(t, result.ModeledScreenPass)
	require.Contains(t, result.Decision, "size opportunity exists")
	groups = gateGroups()
	groups[0].Error, groups[2].Error = "retained failure", "retained failure"
	result = summarizeGate(groups)
	require.Contains(t, result.Decision, "even zero-decoder")
	groups = gateGroups()
	for i := range groups {
		groups[i].Candidates[0].Method = methodBase
	}
	require.Zero(t, summarizeGate(groups).CostedSizePass)
	groups = gateGroups()
	groups[0].Candidates[0].Error, groups[2].Candidates[0].Error = "retained", "retained"
	require.False(t, summarizeGate(groups).CostedOpportunityPass)
	require.Contains(t, summarizeGate(groups[:2]).Decision, "incomplete")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCLIProducesRetainedReportAndRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	_, manifest, stub := fixtureCorpus(t)
	parent := t.TempDir()
	output := filepath.Join(parent, "report")
	args := []string{"--manifest", manifest, "--stub", stub, "--source-revision", fixtureRevision, "--output", output}
	var stdout, stderr bytes.Buffer
	require.Zero(t, run(args, &stdout, &stderr), stderr.String())
	require.Contains(t, stdout.String(), "decision")
	report, err := os.ReadFile(filepath.Join(output, "report.json"))
	require.NoError(t, err)
	require.Contains(t, string(report), "real_startup_and_rss_gate")
	require.Equal(t, 1, run(args, &stdout, &stderr))
	for _, invalid := range [][]string{{"--unknown"}, {"positional"}, nil, {"--manifest", "missing"}} {
		require.NotZero(t, run(invalid, io.Discard, io.Discard))
	}
	baseOptions := screenOptions{manifest, stub, filepath.Join(parent, "another"), fixtureRevision}
	for _, edit := range []func(*screenOptions){
		func(o *screenOptions) { o.Source = "bad" },
		func(o *screenOptions) { o.Manifest = "missing" },
		func(o *screenOptions) { o.Stub = "missing" },
		func(o *screenOptions) { o.Stub = manifest },
		func(o *screenOptions) { o.Output = filepath.Join(parent, "missing", "out") },
	} {
		o := baseOptions
		edit(&o)
		_, err := runScreen(o)
		require.Error(t, err)
	}
	args[len(args)-1] = filepath.Join(parent, "writer-error")
	require.Equal(t, 1, run(args, failingWriter{}, io.Discard))
}
