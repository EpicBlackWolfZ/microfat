package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

const (
	quotaScale           = 100
	admissionNumerator   = 3
	admissionDenominator = 4
	goMemoryNumerator    = 9
	goMemoryDenominator  = 10
	maxPoolWidth         = 3
	trialTimeout         = 45 * time.Second
	fullCPU              = 4
	maxTrialMemoryMiB    = 512
	modeCurrent          = "current"
	modePool             = "pool"
	codecZstd            = "zstd"
)

type campaignOptions struct {
	Manifest, Stub, Minimal, Output, Source string
	Trials                                  int
	Matrix                                  bool
	Execute                                 bool
	Limits                                  bool
	Study                                   string
	Profile                                 string
	Policy                                  *frozenEncoderPolicy
	HostID                                  string
	Family                                  string
	MatrixMemoryMiB                         int64
	DiskProbe                               diskProbe
}

type trialSpec struct {
	Family        string `json:"family"`
	Mode          string `json:"mode"`
	CPU           int    `json:"cpu_budget"`
	MemoryMiB     int64  `json:"hard_memory_mib"`
	Block         int    `json:"block"`
	Format        int    `json:"format"`
	Codec         string `json:"codec"`
	Dictionary    bool   `json:"dictionary"`
	StubProfile   string `json:"stub_profile"`
	AdmissionMiB  int64  `json:"admission_mib_override,omitempty"`
	PriorOutput   bool   `json:"prior_output_sentinel,omitempty"`
	WorkspaceMiB  int64  `json:"workspace_mib,omitempty"`
	CorpusProfile string `json:"corpus_profile,omitempty"`
	Primary       bool   `json:"primary_confirmation,omitempty"`
}

type artifactResult struct {
	Bytes            int64             `json:"complete_bytes"`
	SHA256           string            `json:"complete_sha256"`
	CompressedSHA256 map[string]string `json:"compressed_sha256"`
	Index            *format.Index     `json:"index"`
}

type observation struct {
	Spec                 trialSpec           `json:"spec"`
	Request              request             `json:"request"`
	Resource             resourceTrialResult `json:"resource"`
	Worker               *workerResult       `json:"worker,omitempty"`
	Artifact             *artifactResult     `json:"artifact,omitempty"`
	Error                string              `json:"error,omitempty"`
	Launches             []launchObservation `json:"launches,omitempty"`
	PriorOutputPreserved *bool               `json:"prior_output_preserved,omitempty"`
	PolicyDecision       string              `json:"policy_decision,omitempty"`
	ArtifactRetention    string              `json:"artifact_retention,omitempty"`
}

type campaignReport struct {
	SchemaVersion     int              `json:"schema_version"`
	ProducerRevision  string           `json:"producer_revision"`
	ProducerSHA256    string           `json:"producer_sha256"`
	GoVersion         string           `json:"go_version"`
	HostArch          string           `json:"host_arch"`
	Corpus            corpusManifest   `json:"corpus"`
	ManifestSHA256    string           `json:"manifest_sha256"`
	StubSHA256        string           `json:"stub_sha256"`
	MinimalStubSHA256 string           `json:"minimal_stub_sha256,omitempty"`
	Warnings          []string         `json:"warnings"`
	Observations      []observation    `json:"observations"`
	Study             *studyRecord     `json:"study,omitempty"`
	DiskChecks        []studyDiskCheck `json:"disk_checks,omitempty"`
	StopReason        string           `json:"stop_reason,omitempty"`
}

type trialRunner func(context.Context, trialResourceConfig, string, []string, []string) (resourceTrialResult, error)

func runCampaign(ctx context.Context, opts campaignOptions, runner trialRunner) (campaignReport, error) {
	r := campaignReport{SchemaVersion: 1, ProducerRevision: opts.Source, GoVersion: runtime.Version(), HostArch: runtime.GOARCH,
		Warnings: []string{
			"Exploratory fixed blocks on one desktop; no population p99 or production policy claim.",
			"Pack time excludes process startup, controller, systemd and later verification; orchestration wall is separate.",
			"Cgroup peak includes allocator/file-cache charges and the stop-post keeper; worker max RSS is separate.",
			"Pool admission is raw bytes plus a declared workspace reserve, not a process-memory guarantee.",
			"Inputs were hashed before timing; page cache is warm or unspecified, not cold disk.",
			"Current packer retains upstream inner concurrency; serial1/pool use explicit inner concurrency 1.",
		}}
	if err := validateCampaignOptions(opts); err != nil {
		return r, err
	}
	var err error
	opts, err = absoluteCampaignPaths(opts)
	if err != nil {
		return r, err
	}
	m, families, err := loadCorpus(opts.Manifest)
	if err != nil {
		return r, err
	}
	opts, m, families, err = prepareStudyCampaign(opts, m, families)
	if err != nil {
		return r, err
	}
	if opts.Study != "" {
		r.Study = recordStudy(opts)
	}
	r.Corpus = m
	r.ManifestSHA256, _, err = digestFile(opts.Manifest, maxJSON)
	if err != nil {
		return r, err
	}
	r.StubSHA256, _, err = digestFile(opts.Stub, maxInput)
	if err != nil {
		return r, err
	}
	if opts.Matrix {
		r.MinimalStubSHA256, _, err = digestFile(opts.Minimal, maxInput)
		if err != nil {
			return r, err
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return r, err
	}
	r.ProducerSHA256, _, err = digestFile(exe, maxInput)
	if err != nil {
		return r, err
	}
	if opts.Study == studyConfirmation && (opts.Policy.TrainingProducerRevision != r.ProducerRevision ||
		opts.Policy.TrainingProducerSHA256 != r.ProducerSHA256) {
		return r, errors.New("confirmation producer differs from frozen training producer")
	}
	if err = os.Mkdir(opts.Output, privateDirMode); err != nil {
		return r, err
	}
	if opts.Study != "" {
		if err = writeDurableStudyJSON(filepath.Join(opts.Output, "study.json"), r); err != nil {
			return r, err
		}
	}
	err = collectCampaign(ctx, opts, m, families, exe, runner, &r)
	writeErr := writeJSON(filepath.Join(opts.Output, "report.json"), r)
	return r, errors.Join(err, writeErr)
}

func validateCampaignOptions(opts campaignOptions) error {
	limit := maxTrials
	if opts.Study != "" {
		limit = maxConfirmationBlocks
	}
	if !validRevision(opts.Source) || opts.Trials < 1 || opts.Trials > limit || opts.Output == "" ||
		runtime.Version() != researchGoVersion || (opts.Execute && !opts.Matrix) ||
		(producerRevision != "" && opts.Source != producerRevision) {
		return errors.New("matching exact revision, Go 1.27.3, new output and bounded trials required")
	}
	return nil
}

func absoluteCampaignPaths(opts campaignOptions) (campaignOptions, error) {
	for _, p := range []*string{&opts.Manifest, &opts.Stub, &opts.Minimal, &opts.Output} {
		if *p == "" {
			continue
		}
		path, err := filepath.Abs(*p)
		if err != nil {
			return opts, err
		}
		*p = path
	}
	return opts, nil
}

func makeSpecs(families []string, opts campaignOptions) []trialSpec {
	if opts.Study != "" {
		return makeStudySpecs(families, opts)
	}
	var out []trialSpec
	if opts.Limits {
		for _, memory := range []int64{32, 64} {
			for _, mode := range []string{modeCurrent, modePool} {
				out = append(out, trialSpec{Family: familyMicrofat, Mode: mode, CPU: fullCPU, MemoryMiB: memory,
					Format: format.FormatVersion2, Codec: codecZstd, StubProfile: summaryFullStub, PriorOutput: true})
			}
		}
		return append(out, trialSpec{Family: familyMicrofat, Mode: modePool, CPU: fullCPU, MemoryMiB: maxTrialMemoryMiB,
			Format: format.FormatVersion2, Codec: codecZstd, StubProfile: summaryFullStub, AdmissionMiB: 1, PriorOutput: true})
	}
	if opts.Matrix {
		memory := int64(maxTrialMemoryMiB)
		if opts.MatrixMemoryMiB > 0 {
			memory = opts.MatrixMemoryMiB
		}
		for _, family := range families {
			for _, version := range []int{1, 2} {
				for _, stub := range []string{summaryFullStub, "minimal"} {
					for _, algorithm := range []string{"none", "lz4", codecZstd, "zstd-dict"} {
						for _, mode := range []string{modeCurrent, modePool} {
							out = append(out, trialSpec{Family: family, Mode: mode, CPU: fullCPU, MemoryMiB: memory, Format: version,
								Codec: mapCodec(algorithm), Dictionary: algorithm == "zstd-dict", StubProfile: stub})
						}
					}
				}
			}
		}
		return out
	}
	var cohorts []trialSpec
	for _, family := range families {
		for _, cpu := range []int{1, 4} {
			for _, memory := range []int64{128, 256, 512} {
				cohorts = append(cohorts, trialSpec{Family: family, CPU: cpu, MemoryMiB: memory,
					Format: format.FormatVersion2, Codec: codecZstd, StubProfile: summaryFullStub})
			}
		}
	}
	for block := range opts.Trials {
		for offset := range len(cohorts) {
			s := cohorts[(offset+block)%len(cohorts)]
			modes := []string{modeCurrent, "serial1", modePool}
			if s.CPU == 1 {
				modes = []string{modeCurrent, modePool}
			}
			for j := range len(modes) {
				s.Mode = modes[(j+block+offset)%len(modes)]
				s.Block = block
				out = append(out, s)
			}
		}
	}
	return out
}

func mapCodec(s string) string {
	if s == "zstd-dict" {
		return codecZstd
	}
	return s
}

func runObservation(
	ctx context.Context, opts campaignOptions, m corpusManifest, spec trialSpec, sequence int, exe string, runner trialRunner,
) (observation, error) {
	o := observation{Spec: spec}
	dir := filepath.Join(opts.Output, fmt.Sprintf("trial-%03d", sequence))
	if err := os.Mkdir(dir, privateDirMode); err != nil {
		return o, err
	}
	tmp := filepath.Join(dir, "tmp")
	if err := os.Mkdir(tmp, privateDirMode); err != nil {
		return o, err
	}
	req := makeRequest(opts, m, spec, dir)
	if spec.Mode == modePolicy {
		req.Pool, o.PolicyDecision = selectFrozenPool(opts.Policy, m, spec)
	}
	const priorOutput = "existing output preserved on rejected or failed packing\n"
	if spec.PriorOutput {
		if err := os.WriteFile(req.Output, []byte(priorOutput), privateFileMode); err != nil {
			return o, err
		}
	}
	o.Request = req
	requestPath := filepath.Join(dir, "request.json")
	if err := writeJSON(requestPath, req); err != nil {
		return o, err
	}
	trialCtx, cancel := context.WithTimeout(ctx, trialTimeout)
	defer cancel()
	var err error
	o.Resource, err = runner(trialCtx, trialResourceConfig{CPUQuotaPercent: spec.CPU * quotaScale,
		MemoryMaxBytes: spec.MemoryMiB * mib, GOMAXPROCS: spec.CPU,
		GoMemLimitBytes: spec.MemoryMiB * mib * goMemoryNumerator / goMemoryDenominator, TrialDir: dir}, exe,
		[]string{"--mode=worker", "--request", requestPath}, []string{"TMPDIR=" + tmp})
	if !errors.Is(err, ErrResourceCleanup) {
		err = errors.Join(err, cleanupWorkerTemps(dir))
	}
	if spec.PriorOutput {
		preserved, readErr := matchesPriorOutput(req.Output, priorOutput)
		if readErr != nil {
			err = errors.Join(err, readErr)
		}
		o.PriorOutputPreserved = &preserved
	}
	if err != nil {
		o.Error = err.Error()
		return o, err
	}
	if !o.Resource.OutputTruncated && o.Resource.Stdout != "" {
		var w workerResult
		if decodeErr := json.Unmarshal([]byte(o.Resource.Stdout), &w, json.RejectUnknownMembers(true)); decodeErr != nil {
			o.Error = "worker output: " + decodeErr.Error()
		} else {
			o.Worker = &w
		}
	}
	if o.Resource.ExitCode != 0 || o.Resource.OOMKilled || o.Worker == nil || o.Worker.Error != "" {
		if o.Error == "" {
			o.Error = "worker failed, timed out, rejected admission, or was OOM-killed; retained in denominator"
		}
		return o, nil
	}
	if o.Worker.GoVersion != runtime.Version() || o.Worker.GOMAXPROCS != spec.CPU ||
		o.Worker.GoMemLimitBytes != spec.MemoryMiB*mib*goMemoryNumerator/goMemoryDenominator {
		o.Error = "worker runtime controls differ from request"
		return o, errors.New(o.Error)
	}
	artifact, verifyErr := verifyArtifact(req, m)
	if verifyErr != nil {
		o.Error = verifyErr.Error()
		return o, verifyErr
	}
	o.Artifact = &artifact
	if opts.Execute {
		o.Launches, err = verifyNativeLaunches(ctx, req.Output, m, spec.Family, dir)
		if err != nil {
			o.Error = err.Error()
			return o, err
		}
	}
	return o, nil
}

func matchesPriorOutput(path, sentinel string) (bool, error) {
	f, err := inputfile.Open(path)
	if err != nil {
		return false, err
	}
	b, readErr := io.ReadAll(io.LimitReader(f, int64(len(sentinel))+1))
	return string(b) == sentinel, errors.Join(readErr, f.Close())
}

func verifyArtifact(req request, m corpusManifest) (artifactResult, error) {
	out := artifactResult{CompressedSHA256: map[string]string{}}
	f, err := inputfile.Open(req.Output)
	if err != nil {
		return out, err
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return out, err
	}
	idx, checks, err := pack.VerifyBinary(f, stat.Size())
	if err != nil {
		return out, err
	}
	if err := verifyEnvelope(f, idx, req); err != nil {
		return out, err
	}
	expected := map[string]corpusVariant{}
	var levels []string
	for _, v := range m.Variants {
		if v.Family == req.AppName {
			expected[v.Level] = v
			levels = append(levels, v.Level)
		}
	}
	slices.Sort(levels)
	if len(idx.Variants) != len(expected) || len(checks) != len(expected) {
		return out, errors.New("variant count mismatch")
	}
	for i, v := range idx.Variants {
		want, ok := expected[v.Level]
		if !ok || v.Level != levels[i] || v.SHA256 != want.SHA256 || v.UncompressedSize != want.Bytes || !checks[i].Valid ||
			v.Compression != req.Compression {
			return out, errors.New("canonical order or corpus payload hash/size mismatch")
		}
		h := sha256.New()
		if _, err = io.Copy(h, io.NewSectionReader(f, v.Offset, v.CompressedSize)); err != nil {
			return out, err
		}
		out.CompressedSHA256[v.Level] = hex.EncodeToString(h.Sum(nil))
	}
	artifactLimit := maxArtifact
	if m.SchemaVersion == portableCorpusSchema {
		artifactLimit = maxTrialMemoryMiB * mib
	}
	out.SHA256, out.Bytes, err = digestFile(req.Output, artifactLimit)
	out.Index = idx
	return out, err
}

func makeRequest(opts campaignOptions, m corpusManifest, spec trialSpec, dir string) request {
	stub := opts.Stub
	if spec.StubProfile == "minimal" {
		stub = opts.Minimal
	}
	req := request{Stub: stub, Output: filepath.Join(dir, "artifact"), AppName: spec.Family, Arch: "amd64",
		Variants: familyVariants(m, spec.Family), Compression: spec.Codec, Level: "default", Dictionary: spec.Dictionary, Format: spec.Format}
	req.Arch = corpusArchitecture(m)
	if spec.Mode != modeCurrent {
		workers := min(maxPoolWidth, spec.CPU)
		if spec.Mode == "serial1" {
			workers = 1
		}
		req.Pool = &pack.EncoderPoolOptions{MaxWorkers: workers, InnerConcurrency: 1, CPUBudget: spec.CPU,
			MemoryBudgetBytes:        spec.MemoryMiB * mib * admissionNumerator / admissionDenominator,
			WorkspaceBytesPerEncoder: defaultWorkspace}
		if spec.AdmissionMiB > 0 {
			req.Pool.MemoryBudgetBytes = spec.AdmissionMiB * mib
		}
		if spec.WorkspaceMiB > 0 {
			req.Pool.WorkspaceBytesPerEncoder = spec.WorkspaceMiB * mib
		}
	}
	return req
}

// These paths belong to one newly created trial directory. The worker unit has
// already stopped; retain requests and final artifacts, including prior outputs.
func cleanupWorkerTemps(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var cleanupErr error
	for _, entry := range entries {
		if entry.Name() == "tmp" || strings.HasPrefix(entry.Name(), ".microfat-spools-") ||
			(strings.HasPrefix(entry.Name(), ".microfat-pack-") && strings.HasSuffix(entry.Name(), ".tmp")) {
			cleanupErr = errors.Join(cleanupErr, os.RemoveAll(filepath.Join(dir, entry.Name())))
		}
	}
	return cleanupErr
}

func verifyEnvelope(f *os.File, idx *format.Index, req request) error {
	stubHash, stubSize, err := digestFile(req.Stub, maxInput)
	if err != nil {
		return err
	}
	prefix := sha256.New()
	if n, copyErr := io.Copy(prefix, io.NewSectionReader(f, 0, stubSize)); copyErr != nil || n != stubSize {
		return errors.Join(copyErr, errors.New("stub prefix is truncated"))
	}
	if hex.EncodeToString(prefix.Sum(nil)) != stubHash {
		return errors.New("stub prefix differs from request")
	}
	if (idx.DictionarySize > 0) != req.Dictionary {
		return errors.New("dictionary presence differs from request")
	}
	if idx.Version != req.Format || idx.AppName != req.AppName || idx.TargetArch != req.Arch {
		return errors.New("artifact metadata differs from request")
	}
	if len(idx.Variants) == 0 || idx.Variants[0].Offset != stubSize+idx.DictionarySize ||
		(idx.DictionarySize > 0 && idx.DictionaryOffset != stubSize) {
		return errors.New("stub or dictionary boundary differs from request")
	}
	return nil
}

func collectCampaign(ctx context.Context, opts campaignOptions, m corpusManifest, families []string, exe string,
	runner trialRunner, r *campaignReport) error {
	var err error
	specs := makeSpecs(families, opts)
	for i, spec := range specs {
		if err = ctx.Err(); err != nil {
			break
		}
		if opts.Study != "" {
			var check studyDiskCheck
			check, err = checkStudyDisk(opts.Output, i, opts.DiskProbe)
			r.DiskChecks = append(r.DiskChecks, check)
			if err != nil {
				r.StopReason = err.Error()
				break
			}
		}
		var obs observation
		obs, err = runObservation(ctx, opts, m, spec, i, exe, runner)
		if opts.Study != "" && err == nil {
			err = retainStudyObservation(opts.Output, i, &obs)
		}
		r.Observations = append(r.Observations, obs)
		if err != nil {
			break
		} // Setup/control failures cannot silently become measurements.
	}
	if opts.Matrix && err == nil {
		for _, obs := range r.Observations {
			if obs.Error != "" {
				err = errors.New("correctness matrix contains failed observations")
				break
			}
		}
	}
	if opts.Study == studyConfirmation && err == nil {
		err = checkStudyNative(ctx, r, m, verifyNativeLaunches)
	}
	return err
}
