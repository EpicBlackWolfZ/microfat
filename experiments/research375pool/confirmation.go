package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"runtime"
	"slices"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

const (
	studyCalibration               = "calibrate"
	studyConfirmation              = "confirm"
	modePolicy                     = "policy"
	maxConfirmationBlocks          = 30
	calibrationBlocks              = 3
	plannedConfirmationComparisons = 16
	confidenceDenominator          = 20 // Family error budget exactly 1/20 across app/architecture/VM claims.
	policySharedMiB                = int64(16)
	policyProtocol                 = "pool-confirm-v1:train=demo,microfat;workspace=32,64,96MiB;freeze=largest-passing;cpu=4;memory>=512MiB;" +
		"inner=1;workers=3;shared=16MiB;admission=3/4;N=30;family=16;alpha=1/20;one-sided-median-ratio>10/9;" +
		"eligible>=3of4;fallback-lower>20/21;all-four-native-VMs;primary-failures-veto;fallback-baseline-feasible-controls;" +
		"native-four-modes-complete;govulncheck-offline-version-db;disk-preflight=2GiB"
)

type studyRecord struct {
	Stage                string               `json:"stage"`
	Protocol             string               `json:"protocol"`
	ProtocolSHA256       string               `json:"protocol_sha256"`
	Profile              string               `json:"profile"`
	PlannedPrimaryBlocks int                  `json:"planned_primary_blocks"`
	Policy               *frozenEncoderPolicy `json:"frozen_policy,omitempty"`
	HostID               string               `json:"host_id"`
}

type frozenEncoderPolicy struct {
	SchemaVersion            int    `json:"schema_version"`
	ProtocolSHA256           string `json:"protocol_sha256"`
	Architecture             string `json:"architecture"`
	TrainingReportSHA256     string `json:"training_report_sha256"`
	TrainingManifestSHA256   string `json:"training_manifest_sha256"`
	TrainingProducerRevision string `json:"training_producer_revision"`
	TrainingProducerSHA256   string `json:"training_producer_sha256"`
	WorkspaceMiB             int64  `json:"workspace_mib"`
	Enabled                  bool   `json:"enabled"`
	Reason                   string `json:"reason"`
	TrainingHostID           string `json:"training_host_id"`
}

func protocolHash() string {
	h := sha256.Sum256([]byte(policyProtocol))
	return hex.EncodeToString(h[:])
}

func recordStudy(opts campaignOptions) *studyRecord {
	return &studyRecord{Stage: opts.Study, Protocol: policyProtocol, ProtocolSHA256: protocolHash(), Profile: opts.Profile,
		PlannedPrimaryBlocks: maxConfirmationBlocks, Policy: opts.Policy, HostID: opts.HostID}
}

func validateStudyCorpus(opts campaignOptions, m corpusManifest, families []string) error {
	if (opts.Study != studyCalibration && opts.Study != studyConfirmation) ||
		(opts.Profile != profileStripped && opts.Profile != profileUnstripped) || !safeName.MatchString(opts.HostID) ||
		corpusArchitecture(m) != runtime.GOARCH || runtime.GOOS != "linux" {
		return errors.New("study requires a known profile and matching native Linux architecture")
	}
	if opts.Study == studyCalibration {
		if !slices.Equal(families, []string{familyDemo, familyMicrofat}) || opts.Trials != calibrationBlocks || opts.Profile != profileStripped {
			return errors.New("calibration requires only demo/microfat stripped training inputs and exactly three blocks")
		}
	} else {
		if m.SchemaVersion != portableCorpusSchema ||
			!slices.Equal(families, []string{familyActionlint, familyFortio, familyGovulncheck, familyTask}) {
			return errors.New("confirmation requires all four pinned held-out families in schema2")
		}
		if err := validateFrozenPolicy(opts.Policy, corpusArchitecture(m)); err != nil {
			return err
		}
		if opts.Policy.TrainingHostID != opts.HostID {
			return errors.New("confirmation host must match its training-only frozen policy")
		}
	}
	selected := corpusProfile(m, opts.Profile)
	for _, family := range families {
		if len(familyVariants(selected, family)) != maxPoolWidth {
			return errors.New("study requires three distinct corpus tiers per family/profile")
		}
	}
	info, err := os.Stat(opts.Stub)
	if err != nil {
		return err
	}
	if info.Size() > policySharedMiB*mib {
		return errors.New("study stub exceeds frozen conservative shared-byte reserve")
	}
	return nil
}

func validateFrozenPolicy(p *frozenEncoderPolicy, arch string) error {
	if p == nil || p.SchemaVersion != 1 || p.ProtocolSHA256 != protocolHash() || p.Architecture != arch ||
		!summaryValidHash(p.TrainingReportSHA256) || !summaryValidHash(p.TrainingManifestSHA256) ||
		!validRevision(p.TrainingProducerRevision) || !summaryValidHash(p.TrainingProducerSHA256) ||
		!safeName.MatchString(p.TrainingHostID) ||
		(p.Enabled && !slices.Contains([]int64{32, 64, 96}, p.WorkspaceMiB)) {
		return errors.New("invalid, unbound or foreign-architecture frozen policy")
	}
	return nil
}

func makeStudySpecs(families []string, opts campaignOptions) []trialSpec {
	var cohorts []trialSpec
	for _, family := range families {
		if opts.Study == studyCalibration {
			for _, memory := range []int64{128, 256, 512} {
				for _, workspace := range []int64{32, 64, 96} {
					cohorts = append(cohorts, trialSpec{Family: family, CPU: fullCPU, MemoryMiB: memory, WorkspaceMiB: workspace})
				}
			}
		} else {
			cohorts = append(cohorts, trialSpec{Family: family, CPU: fullCPU, MemoryMiB: maxTrialMemoryMiB, Primary: true})
		}
	}
	var out []trialSpec
	for block := range opts.Trials {
		for offset := range cohorts {
			s := cohorts[(offset+block)%len(cohorts)]
			modes := []string{modeCurrent, modePool}
			if opts.Study == studyConfirmation {
				modes[1] = modePolicy
			}
			for j := range modes {
				s.Mode = modes[(j+block+offset)%len(modes)]
				s.Block, s.Format, s.Codec, s.StubProfile, s.CorpusProfile, s.PriorOutput = block, 2, codecZstd, summaryFullStub, opts.Profile, true
				out = append(out, s)
			}
		}
	}
	// Three diagnostic blocks in fallback classes are separate from primary confirmation.
	if opts.Study == studyConfirmation {
		for block := range calibrationBlocks {
			for _, family := range families {
				for _, resource := range []struct {
					cpu    int
					memory int64
				}{{1, 512}, {4, 128}, {4, 256}} {
					for j := range 2 {
						mode := []string{modeCurrent, modePolicy}[(j+block)%2]
						out = append(out, trialSpec{Family: family, Mode: mode, CPU: resource.cpu, MemoryMiB: resource.memory,
							Block: block, Format: format.FormatVersion2, Codec: codecZstd,
							StubProfile: summaryFullStub, CorpusProfile: opts.Profile, PriorOutput: true})
					}
				}
			}
		}
	}
	return out
}

func selectFrozenPool(p *frozenEncoderPolicy, m corpusManifest, spec trialSpec) (*pack.EncoderPoolOptions, string) {
	if p == nil || !p.Enabled {
		return nil, "current: training did not qualify a pool"
	}
	if spec.CPU != fullCPU || spec.MemoryMiB < maxTrialMemoryMiB {
		return nil, "current: CPU1 or low-memory class"
	}
	var raw int64
	count := 0
	for _, v := range m.Variants {
		if v.Family == spec.Family {
			raw += v.Bytes
			count++
		}
	}
	budget := spec.MemoryMiB * mib * admissionNumerator / admissionDenominator
	if count != maxPoolWidth || raw+policySharedMiB*mib+int64(count)*p.WorkspaceMiB*mib > budget {
		return nil, "current: conservative aggregate reservations do not fit three jobs"
	}
	return &pack.EncoderPoolOptions{MaxWorkers: maxPoolWidth, InnerConcurrency: 1, CPUBudget: spec.CPU,
		MemoryBudgetBytes: budget, WorkspaceBytesPerEncoder: p.WorkspaceMiB * mib}, "pool: frozen three-job high-memory policy"
}

func freezeEncoderPolicy(report campaignReport, reportHash string) (frozenEncoderPolicy, error) {
	p := frozenEncoderPolicy{SchemaVersion: 1, ProtocolSHA256: protocolHash(), Architecture: report.HostArch,
		TrainingReportSHA256: reportHash, TrainingManifestSHA256: report.ManifestSHA256,
		TrainingProducerRevision: report.ProducerRevision, TrainingProducerSHA256: report.ProducerSHA256,
		Reason: "no training reserve cleared every predeclared high-memory gate"}
	if report.Study == nil || report.Study.Stage != studyCalibration || report.Study.ProtocolSHA256 != protocolHash() ||
		report.Study.Protocol != policyProtocol || report.Study.Profile != profileStripped || !summaryValidHash(reportHash) {
		return p, errors.New("freeze requires an untouched calibration report with the current protocol")
	}
	p.TrainingHostID = report.Study.HostID
	if _, err := summarizeReport(report); err != nil {
		return p, err
	}
	expected := makeStudySpecs([]string{familyDemo, familyMicrofat},
		campaignOptions{Study: studyCalibration, Trials: calibrationBlocks, Profile: profileStripped})
	if len(report.Observations) != len(expected) {
		return p, errors.New("calibration is incomplete; missing attempts cannot be discarded")
	}
	seen := map[trialSpec]bool{}
	for _, spec := range expected {
		seen[spec] = true
	}
	for _, o := range report.Observations {
		if !seen[o.Spec] {
			return p, errors.New("unexpected or held-out training cohort")
		}
		delete(seen, o.Spec)
	}
	for _, workspace := range []int64{32, 64, 96} {
		qualified := true
		for _, family := range []string{familyDemo, familyMicrofat} {
			var ratios []*big.Rat
			for block := range calibrationBlocks {
				base, candidate := findStudyPair(report.Observations, family, fullCPU, maxTrialMemoryMiB, workspace, block)
				if !validTrainingPair(base, candidate, workspace) {
					qualified = false
					continue
				}
				ratios = append(ratios, new(big.Rat).SetFrac64(base.Worker.PackNS, candidate.Worker.PackNS))
			}
			slices.SortFunc(ratios, func(a, b *big.Rat) int { return a.Cmp(b) })
			if len(ratios) != calibrationBlocks || ratios[len(ratios)/2].Cmp(big.NewRat(usefulRatioNumerator, usefulRatioDenominator)) <= 0 {
				qualified = false
			}
		}
		if qualified {
			p.Enabled, p.WorkspaceMiB = true, workspace
			p.Reason = "largest tested reservation clearing training speed, correctness and measured headroom gates"
		}
	}
	return p, validateFrozenPolicy(&p, report.HostArch)
}

func validTrainingPair(base, candidate *observation, workspace int64) bool {
	if base == nil || candidate == nil || !studyPairValid(*base, *candidate) || candidate.Worker.Pool == nil {
		return false
	}
	s := candidate.Worker.Pool
	return s.PeakActiveWorkers == maxPoolWidth && s.ActiveWorkers == 0 && s.AdmittedBytes == 0 &&
		s.JobsStarted == maxPoolWidth && s.JobsCompleted == maxPoolWidth && s.PeakAdmittedBytes <= s.MemoryBudgetBytes &&
		s.CPUBudget == fullCPU && s.InnerConcurrency == 1 && s.WorkspaceBytesPerEncoder == workspace*mib &&
		candidate.Resource.MemoryPeakBytes <= uint64(maxTrialMemoryMiB*mib*admissionNumerator/admissionDenominator)
}

func findStudyPair(observations []observation, family string, cpu int, memory, workspace int64, block int) (*observation, *observation) {
	var baseline, candidate *observation
	for i := range observations {
		o := &observations[i]
		if o.Spec.Family != family || o.Spec.CPU != cpu || o.Spec.MemoryMiB != memory ||
			o.Spec.WorkspaceMiB != workspace || o.Spec.Block != block {
			continue
		}
		if o.Spec.Mode == modeCurrent {
			if baseline != nil {
				return nil, nil
			}
			baseline = o
		} else {
			if candidate != nil {
				return nil, nil
			}
			candidate = o
		}
	}
	return baseline, candidate
}

func studyPairValid(a, b observation) bool {
	return summarySucceeded(a) && summarySucceeded(b) && a.Worker.PackNS > 0 && b.Worker.PackNS > 0 &&
		a.Artifact.Bytes == b.Artifact.Bytes && equalArtifactPayloads(a.Artifact, b.Artifact)
}

func equalArtifactPayloads(a, b *artifactResult) bool {
	if a == nil || b == nil || a.Index == nil || b.Index == nil || a.Index.DictionarySHA256 != b.Index.DictionarySHA256 ||
		len(a.CompressedSHA256) != len(b.CompressedSHA256) {
		return false
	}
	for k, v := range a.CompressedSHA256 {
		if b.CompressedSHA256[k] != v {
			return false
		}
	}
	return true
}

// medianOrderInterval uses exact integer binomial tails for n<=30. With sixteen
// predeclared app/architecture/VM claims, each lower bound spends at most 1/(20*16).
// Coverage assumes independent stationary block ratios; telemetry cannot prove it.
func medianOrderInterval(n int) (int, error) {
	if n < 1 || n > maxConfirmationBlocks {
		return 0, errors.New("median interval sample count outside fixed bound")
	}
	var tail, comb uint64
	comb = 1
	chosen := 0
	for k := 1; k <= n/2; k++ {
		tail += comb
		if tail*confidenceDenominator*plannedConfirmationComparisons <= uint64(1)<<n {
			chosen = k
		}
		comb = comb * uint64(n-k+1) / uint64(k) // #nosec G115 -- n and k are positive and bounded by30 above.
	}
	if chosen == 0 {
		return 0, fmt.Errorf("%d blocks cannot resolve the allocated median interval", n)
	}
	return chosen, nil
}
