package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func studyTestPolicy() *frozenEncoderPolicy {
	return &frozenEncoderPolicy{SchemaVersion: 1, ProtocolSHA256: protocolHash(), Architecture: runtime.GOARCH,
		TrainingReportSHA256: strings.Repeat("a", summaryHashLength), TrainingManifestSHA256: strings.Repeat("b", summaryHashLength),
		TrainingProducerRevision: campaignTestRevision, TrainingProducerSHA256: strings.Repeat("c", summaryHashLength),
		WorkspaceMiB: 64, Enabled: true, TrainingHostID: fixtureHost}
}

func studyTestManifest(families []string) corpusManifest {
	m := corpusManifest{SchemaVersion: 1, Architecture: runtime.GOARCH}
	levels := []string{"v1", "v2", "v3"}
	if runtime.GOARCH == "arm64" {
		levels = []string{"v8.0", "v8.1", "v8.2"}
	}
	for _, family := range families {
		for _, level := range levels {
			m.Variants = append(m.Variants, corpusVariant{Family: family, Profile: profileStripped, Level: level, Bytes: 1024,
				Path: tTempPlaceholder + "/" + family + "-" + level, SHA256: strings.Repeat("b", summaryHashLength)})
		}
	}
	return m
}

func studyTestReport(stage string, wall int64) campaignReport {
	families := []string{familyDemo, familyMicrofat}
	blocks := calibrationBlocks
	if stage == studyConfirmation {
		families = []string{familyActionlint, familyFortio, familyGovulncheck, familyTask}
		blocks = maxConfirmationBlocks
	}
	m := studyTestManifest(families)
	opts := campaignOptions{Study: stage, Trials: blocks, Profile: profileStripped, Policy: studyTestPolicy(), HostID: fixtureHost}
	r := summaryFixtureReport()
	r.HostArch, r.ManifestSHA256, r.Corpus, r.Study = runtime.GOARCH, strings.Repeat("d", summaryHashLength), m, recordStudy(opts)
	for _, spec := range makeStudySpecs(families, opts) {
		o := summaryFixtureObservation(spec.Mode, spec.Block, 100)
		o.Spec = spec
		o.Request = makeRequest(opts, m, spec, tTempPlaceholder)
		if spec.Mode == modePolicy {
			o.Request.Pool, o.PolicyDecision = selectFrozenPool(opts.Policy, m, spec)
		}
		if spec.Mode != modeCurrent && spec.Primary {
			o.Worker.PackNS = wall
		}
		if stage == studyCalibration && spec.Mode == modePool {
			o.Worker.PackNS = wall
		}
		o.Worker.GOMAXPROCS, o.Worker.GoMemLimitBytes = spec.CPU, spec.MemoryMiB*mib*goMemoryNumerator/goMemoryDenominator
		o.Resource.ActualCG = resourceControls{MemoryMaxBytes: uint64(spec.MemoryMiB * mib),
			CPUQuotaUsec: uint64(spec.CPU) * 100000, CPUPeriodUsec: 100000}
		if o.Request.Pool == nil {
			o.Worker.Pool = nil
		} else {
			o.Worker.Pool = &pack.EncoderPoolStats{JobsTotal: 3, JobsStarted: 3, JobsCompleted: 3, PeakActiveWorkers: 3,
				PeakAdmittedBytes: 80 * mib, MemoryBudgetBytes: o.Request.Pool.MemoryBudgetBytes, CPUBudget: spec.CPU,
				InnerConcurrency: 1, WorkspaceBytesPerEncoder: o.Request.Pool.WorkspaceBytesPerEncoder}
		}
		if spec.Primary && spec.Block == 0 {
			o.Launches = studyTestNativeRecords(o, m)
		}
		r.Observations = append(r.Observations, o)
	}
	return r
}

func studyTestNativeRecords(o observation, m corpusManifest) []launchObservation {
	var source corpusVariant
	for _, v := range m.Variants {
		if v.Family == o.Spec.Family {
			source = v
		}
	}
	readiness, err := expectedNativeReadiness(o.Spec.Family, filepath.Join(tTempPlaceholder, nativeDBDirectory))
	if err != nil {
		panic(err)
	}
	var records []launchObservation
	for _, mode := range []string{nativeLaunchMode, format.ExecModeMemfd, format.ExecModeCache, format.ExecModeAuto} {
		actual := mode
		if mode == format.ExecModeAuto {
			actual = format.ExecModeCache
		}
		data, err := json.Marshal(format.DispatchTelemetry{Event: format.EventDispatch, HostArch: corpusArchitecture(m),
			SelectedVariant: source.Level, SelectedSHA256: source.SHA256, SelectedSizeBytes: source.Bytes, ExecMode: actual})
		if err != nil {
			panic(err)
		}
		records = append(records, launchObservation{Readiness: readiness,
			MeasurementKind: nativeLaunchKind, RequestedMode: mode, ExecMode: actual,
			ExpectedArch: corpusArchitecture(m), ExpectedVariant: source.Level, SelectedVariant: source.Level,
			ExpectedSHA256: source.SHA256, SelectedSHA256: source.SHA256, ExpectedSizeBytes: source.Bytes, SelectedSizeBytes: source.Bytes,
			NativePayloadPath: source.Path, ArtifactPath: o.Request.Output, Prewarmed: mode == format.ExecModeAuto, OutputMatched: true,
			Stdout: "version fixture\n", Stderr: "[microfat] " + string(data) + "\n"})
	}
	return records
}

func TestStudyNativePhaseCannotDisappearFromSummary(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"absent", "partial", "output", "identity", "mode", "diagnostic", fixtureDuplicate, "prewarm"} {
		t.Run(fault, func(t *testing.T) {
			r := studyTestReport(studyConfirmation, 80)
			i := slices.IndexFunc(r.Observations, func(o observation) bool { return o.Spec.Primary && o.Spec.Block == 0 })
			o := &r.Observations[i]
			switch fault {
			case "absent":
				o.Launches = nil
			case "partial":
				o.Launches = o.Launches[:1]
			case "output":
				o.Launches[1].Stdout = "different output"
			case "identity":
				o.Launches[1].SelectedSHA256 = "different hash"
			case "mode":
				o.Launches[1].RequestedMode = "unknown"
			case "diagnostic":
				o.Launches[1].Stderr = "missing diagnostic"
			case fixtureDuplicate:
				o.Launches[1].RequestedMode = nativeLaunchMode
			case "prewarm":
				o.Launches[3].Prewarmed = false
			}
			out, err := summarizeConfirmation(r)
			require.NoError(t, err)
			assert.False(t, out.VMCandidate)
			assert.False(t, out.Cells[0].NativeVerified)
		})
	}
	r := studyTestReport(studyConfirmation, 80)
	o := r.Observations[0]
	o.Launches[0].ExecMode = fixtureWrong
	assert.False(t, completeStudyNative(o, r.Corpus))
	o = r.Observations[2]
	assert.False(t, completeStudyNative(o, corpusManifest{}))
	_, err := finishConfirmationCell(confirmationCell{CompletePairs: maxConfirmationBlocks, NativeVerified: true}, nil, profileStripped, 0)
	require.Error(t, err, "a complete-count assertion cannot manufacture an interval without samples")
}

const tTempPlaceholder = "/synthetic-only-no-files"

func TestStudyExactMedianGateAndFailureDenominators(t *testing.T) {
	t.Parallel()
	rank, err := medianOrderInterval(maxConfirmationBlocks)
	require.NoError(t, err)
	assert.Equal(t, 8, rank)
	for _, n := range []int{0, 1, maxConfirmationBlocks + 1} {
		_, err := medianOrderInterval(n)
		require.Error(t, err)
	}
	for _, tc := range []struct {
		name   string
		wall   int64
		useful bool
	}{{"clear margin", 80, true}, {"exact10percent", 90, false}, {"insufficient", 95, false}} {
		t.Run(tc.name, func(t *testing.T) {
			r := studyTestReport(studyConfirmation, tc.wall)
			out, err := summarizeConfirmation(r)
			require.NoError(t, err)
			assert.Equal(t, tc.useful, out.VMCandidate)
			assert.Equal(t, 4, out.EligibleFamilies)
			for _, cell := range out.Cells {
				assert.Equal(t, 30, cell.CompletePairs)
				assert.Equal(t, 8, cell.LowerRank)
				require.NotNil(t, cell.LowerRatio)
			}
		})
	}
	for _, fault := range []string{"missing", "failed", fixtureBytes, "fallback", "policy", fixtureProfile} {
		t.Run(fault, func(t *testing.T) {
			r := studyTestReport(studyConfirmation, 80)
			i := slices.IndexFunc(r.Observations, func(o observation) bool { return o.Spec.Primary && o.Spec.Mode == modePolicy })
			switch fault {
			case "missing":
				r.Observations = append(r.Observations[:i], r.Observations[i+1:]...)
			case "failed":
				r.Observations[i].Error = "retained resource failure"
			case fixtureBytes:
				r.Observations[i].Artifact.CompressedSHA256["v1"] = fixtureWrong
			case "fallback":
				r.Observations = r.Observations[:len(r.Observations)-1]
			case "policy":
				r.Observations[i].Request.Pool.WorkspaceBytesPerEncoder++
			case fixtureProfile:
				r.Study.Profile = profileUnstripped
			}
			out, err := summarizeConfirmation(r)
			if fault == "policy" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.False(t, out.VMCandidate)
		})
	}
}

func TestStudyCorpusPhaseValidationAndPortableLoader(t *testing.T) {
	t.Parallel()
	families := []string{familyDemo, familyMicrofat}
	m := studyTestManifest(families)
	stub := filepath.Join(t.TempDir(), "stub")
	require.NoError(t, os.WriteFile(stub, []byte("stub"), privateFileMode))
	o := campaignOptions{Study: studyCalibration, Profile: profileStripped, HostID: fixtureHost, Trials: calibrationBlocks, Stub: stub}
	require.NoError(t, validateStudyCorpus(o, m, families))
	for _, fault := range []string{"stage", fixtureProfile, "host", "trials", "families", "tiers", "missing-stub", "oversized-stub"} {
		t.Run(fault, func(t *testing.T) {
			opts, manifest, apps := o, m, slices.Clone(families)
			switch fault {
			case "stage":
				opts.Study = fixtureUnknown
			case fixtureProfile:
				opts.Profile = fixtureUnknown
			case "host":
				opts.HostID = ""
			case "trials":
				opts.Trials = 1
			case "families":
				apps = []string{familyTask}
			case "tiers":
				manifest.Variants = manifest.Variants[:1]
			case "missing-stub":
				opts.Stub = filepath.Join(t.TempDir(), fixtureMissing)
			case "oversized-stub":
				opts.Stub = filepath.Join(t.TempDir(), "large")
				file, err := os.Create(opts.Stub)
				require.NoError(t, err)
				require.NoError(t, file.Truncate(policySharedMiB*mib+1))
				require.NoError(t, file.Close())
			}
			require.Error(t, validateStudyCorpus(opts, manifest, apps))
		})
	}
	apps := []string{familyActionlint, familyFortio, familyGovulncheck, familyTask}
	m = studyTestManifest(apps)
	m.SchemaVersion = 2
	o.Study, o.Policy = studyConfirmation, studyTestPolicy()
	require.NoError(t, validateStudyCorpus(o, m, apps))
	o.HostID = "another-vm"
	require.Error(t, validateStudyCorpus(o, m, apps))
	o.HostID = fixtureHost
	o.Policy = nil
	require.Error(t, validateStudyCorpus(o, m, apps))
	m.SchemaVersion = 1
	require.Error(t, validateStudyCorpus(o, m, apps))
	assert.True(t, validCorpusLevel("arm64", "v8.0"))
	assert.False(t, validCorpusLevel("arm64", "v3"))
	armDir := t.TempDir()
	for _, family := range families {
		for _, level := range []string{"v8.0", "v8.1", "v8.2"} {
			require.NoError(t, os.WriteFile(filepath.Join(armDir, family+"-"+level), []byte("fixture"), privateFileMode))
		}
	}
	arm, err := buildManifestForArch(armDir, campaignTestRevision, "arm64")
	require.NoError(t, err)
	assert.Equal(t, "arm64", arm.Architecture)
}

func TestStudyCLIAnalysisHappyPathsAndInvalidGates(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"freeze", "confirm-summary"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "report.json")
		stage := studyCalibration
		if mode == "confirm-summary" {
			stage = studyConfirmation
		}
		storeCampaignJSON(t, path, studyTestReport(stage, 50))
		var stdout, stderr bytes.Buffer
		args := []string{"--mode=" + mode, "--request", path}
		if mode == "freeze" {
			args = append(args, "--output", filepath.Join(dir, "policy.json"))
		}
		require.Zero(t, run(context.Background(), args, &stdout, &stderr), stderr.String())
		assert.NotEmpty(t, stdout.String())
	}
	r := studyTestReport(studyConfirmation, 80)
	r.Study.Policy.ProtocolSHA256 = fixtureWrong
	_, err := summarizeConfirmation(r)
	require.Error(t, err)
	r = studyTestReport(studyConfirmation, 80)
	r.Observations[0].Spec.Mode = "invalid"
	_, err = summarizeConfirmation(r)
	require.Error(t, err)
	r = studyTestReport(studyConfirmation, 80)
	fallback := slices.IndexFunc(r.Observations, func(o observation) bool { return !o.Spec.Primary && o.Spec.Mode == modeCurrent })
	r.Observations[fallback].Error = "baseline resource failure"
	out, err := summarizeConfirmation(r)
	require.NoError(t, err)
	assert.Equal(t, 1, out.UnsupportedFallbackPairs)
	assert.Zero(t, out.FallbackRegressions)
}

func TestStudyTrainingFreezeAndPolicyDecisions(t *testing.T) {
	t.Parallel()
	r := studyTestReport(studyCalibration, 50)
	p, err := freezeEncoderPolicy(r, strings.Repeat("e", summaryHashLength))
	require.NoError(t, err)
	assert.True(t, p.Enabled)
	assert.Equal(t, int64(96), p.WorkspaceMiB, "largest passing reserve, never tuned on held-out programs")
	r = studyTestReport(studyCalibration, 100)
	p, err = freezeEncoderPolicy(r, strings.Repeat("e", summaryHashLength))
	require.NoError(t, err)
	assert.False(t, p.Enabled)
	r.Observations = r.Observations[:len(r.Observations)-1]
	_, err = freezeEncoderPolicy(r, strings.Repeat("e", summaryHashLength))
	require.Error(t, err)
	r = studyTestReport(studyCalibration, 50)
	r.Observations[0].Spec.Family = familyTask
	_, err = freezeEncoderPolicy(r, strings.Repeat("e", summaryHashLength))
	require.Error(t, err)
	pointer := studyTestPolicy()
	m := studyTestManifest([]string{familyTask})
	spec := trialSpec{Family: familyTask, CPU: 4, MemoryMiB: 512}
	pool, reason := selectFrozenPool(pointer, m, spec)
	require.NotNil(t, pool)
	assert.Contains(t, reason, "pool:")
	for _, fault := range []string{"disabled", "cpu", fixtureMemory, "large", "count"} {
		t.Run(fault, func(t *testing.T) {
			p := *pointer
			current := m
			current.Variants = slices.Clone(m.Variants)
			s := spec
			switch fault {
			case "disabled":
				p.Enabled = false
			case "cpu":
				s.CPU = 1
			case fixtureMemory:
				s.MemoryMiB = 256
			case "large":
				for i := range current.Variants {
					current.Variants[i].Bytes = maxPortableInput
				}
			case "count":
				current.Variants = current.Variants[:1]
			}
			pool, _ := selectFrozenPool(&p, current, s)
			assert.Nil(t, pool)
		})
	}
	bad := *pointer
	bad.ProtocolSHA256 = fixtureWrong
	require.Error(t, validateFrozenPolicy(&bad, runtime.GOARCH))
	_, err = freezeEncoderPolicy(campaignReport{}, fixtureWrong)
	require.Error(t, err)
	_, err = summarizeConfirmation(campaignReport{})
	require.Error(t, err)
}

func TestStudyLargeInputFallbackHasSeparatePracticalGate(t *testing.T) {
	t.Parallel()
	for _, wall := range []int64{100, 105, 106} {
		r := studyTestReport(studyConfirmation, 80)
		for i := range r.Corpus.Variants {
			if r.Corpus.Variants[i].Family == familyTask {
				r.Corpus.Variants[i].Bytes = maxPortableInput
			}
		}
		for i := range r.Observations {
			o := &r.Observations[i]
			if o.Spec.Family != familyTask {
				continue
			}
			if o.Spec.Mode == modePolicy {
				o.Request.Pool, o.PolicyDecision = selectFrozenPool(r.Study.Policy, r.Corpus, o.Spec)
				o.Worker.Pool = nil
				if o.Spec.Primary {
					o.Worker.PackNS = wall
				}
			}
			if o.Spec.Primary && o.Spec.Block == 0 {
				o.Launches = studyTestNativeRecords(*o, r.Corpus)
			}
		}
		out, err := summarizeConfirmation(r)
		require.NoError(t, err)
		assert.Equal(t, 3, out.EligibleFamilies)
		assert.Equal(t, wall == 100, out.VMCandidate)
		assert.False(t, out.Cells[3].PolicyActive)
	}
}

func TestStudyDurableRetentionOnlyDeletesOwnedSuccessfulArtifacts(t *testing.T) {
	t.Parallel()
	for _, block := range []int{0, 1} {
		dir := t.TempDir()
		trial := filepath.Join(dir, "trial-000")
		require.NoError(t, os.Mkdir(trial, privateDirMode))
		o := summaryFixtureObservation(modeCurrent, block, 100)
		o.Request.Output = filepath.Join(trial, "artifact")
		require.NoError(t, os.WriteFile(o.Request.Output, []byte("verified artifact"), privateFileMode))
		require.NoError(t, retainStudyObservation(dir, 0, &o))
		_, err := os.Stat(o.Request.Output)
		if block == 0 {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, os.ErrNotExist)
		}
		var saved observation
		require.NoError(t, readJSON(filepath.Join(trial, "observation.json"), &saved))
		assert.Equal(t, o.Artifact.SHA256, saved.Artifact.SHA256)
		require.Error(t, retainStudyObservation(dir, 0, &o), "existing durable records cannot be overwritten")
	}
	o := summaryFixtureObservation(modeCurrent, 1, 100)
	o.Request.Output = "/foreign/path"
	require.Error(t, retainStudyObservation(t.TempDir(), 0, &o))
	require.Error(t, writeDurableStudyJSON(filepath.Join(t.TempDir(), "missing", "record"), o))
	require.Error(t, writeDurableStudyJSON(filepath.Join(t.TempDir(), "record"), make(chan int)))
}

func TestStudyPortableSourceSchemaAndCLIRejections(t *testing.T) {
	t.Parallel()
	m := studyTestManifest([]string{familyTask})
	m.SchemaVersion = 2
	m.Build = &corpusBuild{CGOEnabled: "0", GOOS: "linux", Trimpath: true}
	m.Sources = []corpusSource{{Family: familyTask, Module: "github.com/go-task/task/v3", Version: "v3.54.0", Package: "./cmd/task",
		ModuleSum: "h1:test", GoModSum: "h1:test", SourceCommit: campaignTestRevision, ReadinessArgs: []string{"--version"}}}
	require.NoError(t, validatePortableCorpus(m))
	m.GoVersion, m.SourceRevision = researchGoVersion, campaignTestRevision
	for i := range m.Variants {
		v := &m.Variants[i]
		v.Path = filepath.Join(t.TempDir(), v.Level)
		require.NoError(t, os.WriteFile(v.Path, []byte("bounded fixture"), privateFileMode))
		var err error
		v.SHA256, v.Bytes, err = digestFile(v.Path, maxPortableInput)
		require.NoError(t, err)
	}
	portablePath := filepath.Join(t.TempDir(), "portable.json")
	storeCampaignJSON(t, portablePath, m)
	loaded, families, err := loadCorpus(portablePath)
	require.NoError(t, err)
	assert.Equal(t, []string{familyTask}, families)
	assert.Equal(t, m, loaded)
	assert.Len(t, corpusProfile(m, profileStripped).Variants, 3)
	assert.Empty(t, corpusProfile(m, profileUnstripped).Variants)
	for _, fault := range []string{"architecture", "build", "source", fixtureProfile} {
		t.Run(fault, func(t *testing.T) {
			bad := m
			bad.Variants = slices.Clone(m.Variants)
			bad.Sources = slices.Clone(m.Sources)
			switch fault {
			case "architecture":
				bad.Architecture = "foreign"
			case "build":
				bad.Build = nil
			case "source":
				bad.Sources[0].SourceCommit = "unpinned"
			case fixtureProfile:
				bad.Variants[0].Profile = fixtureUnknown
			}
			require.Error(t, validatePortableCorpus(bad))
		})
	}
	for _, mode := range []string{"freeze", "confirm-summary", "confirm", "calibrate"} {
		var stdout, stderr bytes.Buffer
		assert.Equal(t, 1, run(context.Background(), []string{"--mode=" + mode, "--request=/missing"}, &stdout, &stderr))
	}
	var encoded bytes.Buffer
	require.NoError(t, json.MarshalWrite(&encoded, m))
	assert.Contains(t, encoded.String(), "source_commit")
}

func TestStudyDiskGuardStopsBeforeUnlaunchedUnit(t *testing.T) {
	t.Parallel()
	check, err := checkStudyDisk(t.TempDir(), 0, func(string) (uint64, error) { return minimumStudyFreeBytes - 1, nil })
	require.Error(t, err)
	assert.Equal(t, minimumStudyFreeBytes, check.MinimumBytes)
	assert.NotEmpty(t, check.Error)
	_, err = checkStudyDisk(t.TempDir(), 0, func(string) (uint64, error) { return 0, errors.New("probe failure") })
	require.Error(t, err)
	_, err = studyFreeBytes(filepath.Join(t.TempDir(), fixtureMissing))
	require.Error(t, err)
	_, err = studyFreeBytes(t.TempDir())
	require.NoError(t, err)
	opts, m := campaignFixture(t)
	m.Architecture = runtime.GOARCH
	if runtime.GOARCH == "arm64" {
		for i := range m.Variants {
			m.Variants[i].Level = []string{"v8.0", "v8.1", "v8.2"}[i%3]
		}
	}
	storeCampaignJSON(t, opts.Manifest, m)
	opts.Study, opts.Profile, opts.HostID, opts.Trials = studyCalibration, profileStripped, fixtureHost, calibrationBlocks
	started := 0
	opts.DiskProbe = func(string) (uint64, error) {
		if started == 0 {
			return minimumStudyFreeBytes, nil
		}
		return 0, nil
	}
	runner := func(_ context.Context, cfg trialResourceConfig, _ string, _ []string, _ []string) (resourceTrialResult, error) {
		started++
		return resourceTrialResult{ExitCode: 1, TelemetryAvailable: true, ActualCG: resourceControls{
			MemoryMaxBytes: uint64(cfg.MemoryMaxBytes), CPUQuotaUsec: uint64(cfg.CPUQuotaPercent) * 1000, CPUPeriodUsec: 100000}}, nil
	}
	r, err := runCampaign(context.Background(), opts, runner)
	require.Error(t, err)
	assert.Equal(t, 1, started)
	assert.Len(t, r.Observations, 1)
	assert.Len(t, r.DiskChecks, 2)
	assert.NotEmpty(t, r.StopReason)
	_, err = os.Stat(filepath.Join(opts.Output, "report.json"))
	require.NoError(t, err)
	assert.Equal(t, check.MinimumBytes, r.DiskChecks[1].MinimumBytes)
}

func TestStudyPreparedCorpusAndPostTimingNativeChecks(t *testing.T) {
	t.Parallel()
	m := studyTestManifest([]string{familyActionlint, familyTask})
	m.SchemaVersion = portableCorpusSchema
	opts, selected, families, err := prepareStudyCampaign(campaignOptions{Matrix: true, Family: familyActionlint}, m,
		[]string{familyActionlint, familyTask})
	require.NoError(t, err)
	assert.Equal(t, resourceMaximumMemory/mib, opts.MatrixMemoryMiB)
	assert.Equal(t, []string{familyActionlint}, families)
	assert.Len(t, selected.Variants, 3)
	_, _, _, err = prepareStudyCampaign(campaignOptions{Family: "absent"}, m, []string{familyActionlint})
	require.Error(t, err)
	r := studyTestReport(studyConfirmation, 80)
	checked := 0
	check := func(context.Context, string, corpusManifest, string, string) ([]launchObservation, error) {
		checked++
		return []launchObservation{{OutputMatched: true}}, nil
	}
	err = checkStudyNative(context.Background(), &r, r.Corpus, check)
	require.NoError(t, err)
	assert.Equal(t, 8, checked)
	check = func(context.Context, string, corpusManifest, string, string) ([]launchObservation, error) {
		return nil, errors.New("native check failed")
	}
	err = checkStudyNative(context.Background(), &r, r.Corpus, check)
	require.Error(t, err)
	assert.NotEmpty(t, r.Observations[0].Error)
}
