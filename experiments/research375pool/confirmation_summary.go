package main

import (
	"errors"
	"math/big"
	"slices"
)

type confirmationCell struct {
	Family         string   `json:"family"`
	Architecture   string   `json:"architecture"`
	HostID         string   `json:"host_id"`
	ExpectedPairs  int      `json:"expected_pairs"`
	CompletePairs  int      `json:"complete_pairs"`
	MissingPairs   int      `json:"missing_pairs"`
	FailedPairs    int      `json:"failed_or_compressed_compatibility_gate_pairs"`
	MedianRatio    *float64 `json:"median_current_over_policy_ratio"`
	LowerRatio     *float64 `json:"one_sided_median_ratio_lower_bound"`
	LowerRank      int      `json:"lower_order_statistic_rank"`
	Decision       string   `json:"decision"`
	PolicyActive   bool     `json:"source_eligible_for_pool"`
	NativeVerified bool     `json:"both_first_artifacts_native_verified"`
}

type confirmationSummary struct {
	ProtocolSHA256           string               `json:"protocol_sha256"`
	PlannedComparisons       int                  `json:"planned_app_arch_vm_comparisons"`
	FamilyAlpha              string               `json:"family_error_budget"`
	UsefulRatio              string               `json:"strict_useful_ratio_threshold"`
	Cells                    []confirmationCell   `json:"primary_cells"`
	FallbackRegressions      int                  `json:"fallback_functional_or_resource_regressions"`
	Warnings                 []string             `json:"warnings"`
	Descriptive              campaignSummary      `json:"descriptive"`
	EligibleFamilies         int                  `json:"eligible_families"`
	VMCandidate              bool                 `json:"vm_candidate_pending_other_native_cohorts"`
	UnsupportedFallbackPairs int                  `json:"unsupported_baseline_fallback_pairs"`
	FallbackDiagnostics      []fallbackDiagnostic `json:"fallback_diagnostics"`
}

type fallbackDiagnostic struct {
	Family      string `json:"family"`
	CPU         int    `json:"cpu_budget"`
	MemoryMiB   int64  `json:"hard_memory_mib"`
	Block       int    `json:"block"`
	Outcome     string `json:"outcome"`
	CurrentExit int    `json:"current_exit_code"`
	PolicyExit  int    `json:"policy_exit_code"`
	CurrentOOM  bool   `json:"current_oom"`
	PolicyOOM   bool   `json:"policy_oom"`
}

func summarizeConfirmation(report campaignReport) (confirmationSummary, error) {
	out := confirmationSummary{ProtocolSHA256: protocolHash(), PlannedComparisons: plannedConfirmationComparisons,
		FamilyAlpha: "1/20", UsefulRatio: "10/9", Warnings: []string{
			"One-sided order-statistic median bounds assume independent stationary block ratios; measured controls do not establish independence.",
			"The family budget covers four programs on two native architectures and two fresh VM replicas; VM blocks are never pooled.",
			"Any failed, missing or byte-inexact primary pair vetoes a useful-improvement decision; failures remain in the denominator.",
			"Logical reservations do not bound preparation, retained heap, RSS or cgroup charges. No population p99 claim.",
			"Input validation is not authentication of supplied metrics or deleted on-disk artifacts; retained records preserve provenance.",
		}}
	if report.Study == nil || report.Study.Stage != studyConfirmation || report.Study.ProtocolSHA256 != protocolHash() ||
		report.Study.Protocol != policyProtocol || !safeName.MatchString(report.Study.HostID) {
		return out, errors.New("confirmation summary requires the frozen confirmation protocol")
	}
	if err := validateFrozenPolicy(report.Study.Policy, report.HostArch); err != nil {
		return out, err
	}
	if report.Study.Policy.TrainingHostID != report.Study.HostID ||
		report.Study.Policy.TrainingProducerRevision != report.ProducerRevision ||
		report.Study.Policy.TrainingProducerSHA256 != report.ProducerSHA256 {
		return out, errors.New("confirmation producer/host differs from frozen training")
	}
	descriptive, err := summarizeReport(report)
	if err != nil {
		return out, err
	}
	out.Descriptive = descriptive
	out.FallbackRegressions, out.UnsupportedFallbackPairs, out.FallbackDiagnostics = countFallbackFailures(report)
	for _, family := range []string{familyActionlint, familyFortio, familyGovulncheck, familyTask} {
		cell, err := summarizeConfirmationCell(report, family, out.FallbackRegressions)
		if err != nil {
			return out, err
		}
		out.Cells = append(out.Cells, cell)
	}
	out.VMCandidate = out.FallbackRegressions == 0 && report.Study.Profile == profileStripped
	for _, cell := range out.Cells {
		if cell.PolicyActive {
			out.EligibleFamilies++
		}
		if cell.Decision != "useful: lower bound exceeds 10 percent packing-time reduction" &&
			cell.Decision != "safe fallback: lower bound excludes more than 5 percent packing-time regression" {
			out.VMCandidate = false
		}
	}
	if out.EligibleFamilies < minimumEligibleFamilies {
		out.VMCandidate = false
	}
	return out, nil
}

func countFallbackFailures(report campaignReport) (int, int, []fallbackDiagnostic) {
	failures, unsupported := 0, 0
	var diagnostics []fallbackDiagnostic
	for _, family := range []string{familyActionlint, familyFortio, familyGovulncheck, familyTask} {
		for block := range calibrationBlocks {
			for _, resource := range []struct {
				cpu    int
				memory int64
			}{{1, 512}, {4, 128}, {4, 256}} {
				base, candidate := findStudyPair(report.Observations, family, resource.cpu, resource.memory, 0, block)
				d := fallbackDiagnostic{Family: family, CPU: resource.cpu, MemoryMiB: resource.memory, Block: block, Outcome: "incomplete diagnostic"}
				switch {
				case base == nil || candidate == nil:
					failures++
				default:
					d.CurrentExit, d.PolicyExit = base.Resource.ExitCode, candidate.Resource.ExitCode
					d.CurrentOOM, d.PolicyOOM = base.Resource.OOMKilled, candidate.Resource.OOMKilled
					_, decision := selectFrozenPool(report.Study.Policy, report.Corpus, candidate.Spec)
					switch {
					case candidate.Spec.Mode != modePolicy || candidate.Request.Pool != nil || candidate.PolicyDecision != decision:
						d.Outcome = "veto: fallback request changed current packing path"
						failures++
					case !summarySucceeded(*base):
						d.Outcome = "unsupported baseline resource class; no fallback speed or equivalence claim"
						unsupported++
					case !studyPairValid(*base, *candidate) || !policyObservationValid(report, *candidate):
						d.Outcome = "veto: current succeeded but fallback failed its compatibility/resource gate"
						failures++
					default:
						d.Outcome = "both succeeded; current path and compressed compatibility verified"
					}
				}
				diagnostics = append(diagnostics, d)
			}
		}
	}
	return failures, unsupported, diagnostics
}

func policyObservationValid(report campaignReport, o observation) bool {
	expected, decision := selectFrozenPool(report.Study.Policy, report.Corpus, o.Spec)
	if o.PolicyDecision != decision {
		return false
	}
	if expected == nil {
		return o.Request.Pool == nil && o.Worker != nil && o.Worker.Pool == nil
	}
	if o.Request.Pool == nil || *o.Request.Pool != *expected || o.Worker == nil || o.Worker.Pool == nil {
		return false
	}
	s := o.Worker.Pool
	return s.JobsTotal == maxPoolWidth && s.JobsStarted == maxPoolWidth && s.JobsCompleted == maxPoolWidth &&
		s.ActiveWorkers == 0 && s.AdmittedBytes == 0 && s.PeakActiveWorkers <= expected.MaxWorkers &&
		s.PeakAdmittedBytes <= expected.MemoryBudgetBytes && s.MemoryBudgetBytes == expected.MemoryBudgetBytes &&
		s.CPUBudget == expected.CPUBudget && s.InnerConcurrency == expected.InnerConcurrency &&
		s.WorkspaceBytesPerEncoder == expected.WorkspaceBytesPerEncoder
}

func summarizeConfirmationCell(report campaignReport, family string, fallbackFailures int) (confirmationCell, error) {
	expected, _ := selectFrozenPool(report.Study.Policy, report.Corpus,
		trialSpec{Family: family, CPU: fullCPU, MemoryMiB: maxTrialMemoryMiB})
	cell := confirmationCell{Family: family, Architecture: report.HostArch, HostID: report.Study.HostID,
		ExpectedPairs: maxConfirmationBlocks, Decision: "inconclusive", PolicyActive: expected != nil}
	firstCurrent, firstPolicy := findStudyPair(report.Observations, family, fullCPU, maxTrialMemoryMiB, 0, 0)
	cell.NativeVerified = firstCurrent != nil && firstPolicy != nil && completeStudyNative(*firstCurrent, report.Corpus) &&
		completeStudyNative(*firstPolicy, report.Corpus)
	var ratios []*big.Rat
	for block := range maxConfirmationBlocks {
		base, candidate := findStudyPair(report.Observations, family, fullCPU, maxTrialMemoryMiB, 0, block)
		if base == nil || candidate == nil {
			cell.MissingPairs++
			continue
		}
		if !base.Spec.Primary || !candidate.Spec.Primary || candidate.Spec.Mode != modePolicy ||
			!studyPairValid(*base, *candidate) || !policyObservationValid(report, *candidate) {
			cell.FailedPairs++
			continue
		}
		cell.CompletePairs++
		ratios = append(ratios, new(big.Rat).SetFrac64(base.Worker.PackNS, candidate.Worker.PackNS))
	}
	return finishConfirmationCell(cell, ratios, report.Study.Profile, fallbackFailures)
}

func finishConfirmationCell(cell confirmationCell, ratios []*big.Rat, profile string, fallbackFailures int) (confirmationCell, error) {
	if len(ratios) > 0 {
		slices.SortFunc(ratios, func(a, b *big.Rat) int { return a.Cmp(b) })
		middle := len(ratios) / medianDivisor
		median := new(big.Rat).Set(ratios[middle])
		if len(ratios)%medianDivisor == 0 {
			median.Add(median, ratios[middle-1])
			median.Quo(median, big.NewRat(medianDivisor, 1))
		}
		value, _ := median.Float64()
		cell.MedianRatio = &value
	}
	if cell.FailedPairs > 0 || fallbackFailures > 0 {
		cell.Decision = "veto: failure, compressed-frame identity/size gate, or fallback regression"
	}
	if !cell.NativeVerified {
		cell.Decision = "veto: incomplete or mismatched native correctness evidence"
	}
	if cell.CompletePairs == maxConfirmationBlocks && cell.FailedPairs == 0 && cell.MissingPairs == 0 &&
		fallbackFailures == 0 && profile == profileStripped && cell.NativeVerified {
		rank, intervalErr := medianOrderInterval(len(ratios))
		if intervalErr != nil {
			return cell, intervalErr
		}
		cell.LowerRank = rank
		value, _ := ratios[rank-1].Float64()
		cell.LowerRatio = &value
		if ratios[rank-1].Cmp(big.NewRat(usefulRatioNumerator, usefulRatioDenominator)) > 0 && cell.PolicyActive {
			cell.Decision = "useful: lower bound exceeds 10 percent packing-time reduction"
		} else if !cell.PolicyActive && ratios[rank-1].Cmp(big.NewRat(fallbackRatioNumerator, fallbackRatioDenominator)) > 0 {
			cell.Decision = "safe fallback: lower bound excludes more than 5 percent packing-time regression"
		}
	}
	return cell, nil
}
