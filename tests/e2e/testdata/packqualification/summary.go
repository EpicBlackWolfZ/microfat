//go:build linux

package main

import (
	"fmt"
	"math"
	"slices"
)

const (
	decisionPass         = "pass"
	decisionRegression   = "regression"
	decisionInconclusive = "inconclusive"
	familyAlpha          = 0.05
	maxRegression        = 1.05
	usefulRatio          = 0.90
	binomialHalf         = 0.5
)

type cellSummary struct {
	Cell               cell     `json:"cell"`
	Decision           string   `json:"decision"`
	ExpectedPairs      int      `json:"expected_pairs"`
	CompletePairs      int      `json:"complete_pairs"`
	MissingPairs       int      `json:"missing_pairs"`
	FailedPairs        int      `json:"failed_pairs"`
	BaselineOOMPairs   int      `json:"baseline_oom_pairs"`
	DefaultOOMPairs    int      `json:"default_oom_pairs"`
	FallbackPairs      int      `json:"default_serial_fallback_pairs"`
	ParallelPairs      int      `json:"default_parallel_pairs"`
	ArtifactMismatches int      `json:"artifact_mismatches"`
	PeakOvershoots     int      `json:"memory_peak_overshoots"`
	MedianRatio        *float64 `json:"median_default_over_serial_ratio"`
	LowerRatio         *float64 `json:"lower_ratio_bound"`
	UpperRatio         *float64 `json:"upper_ratio_bound"`
	Reasons            []string `json:"reasons"`
}

type campaignSummary struct {
	Decision           string        `json:"decision"`
	PlannedComparisons int           `json:"planned_comparisons_across_all_vms"`
	FamilyAlpha        float64       `json:"family_alpha"`
	IntervalRank       int           `json:"symmetric_order_statistic_rank"`
	Cells              []cellSummary `json:"cells"`
}

type pairKey struct {
	Cell  cell
	Block int
	Arm   string
}

func summarize(results []attempt, trials []pair) campaignSummary {
	out := campaignSummary{Decision: decisionPass, FamilyAlpha: familyAlpha}
	out.PlannedComparisons = len(binaryProfiles) * len(packProfiles) * len(cpuBudgets) *
		len(memoryBudgets) * familyCount * replicas * architectures
	out.IntervalRank = medianRank(blocks, familyAlpha/float64(out.PlannedComparisons))
	index := make(map[pairKey]attempt, len(results))
	for _, result := range results {
		index[pairKey{result.Cell, result.Block, result.Arm}] = result
	}
	var cells []cell
	perCell := make(map[cell][]pair)
	for _, p := range trials {
		if len(perCell[p.Cell]) == 0 {
			cells = append(cells, p.Cell)
		}
		perCell[p.Cell] = append(perCell[p.Cell], p)
	}
	for _, key := range cells {
		report := summarizeCell(key, perCell[key], index, out.IntervalRank)
		out.Cells = append(out.Cells, report)
		if report.Decision == decisionRegression || out.Decision == decisionPass {
			out.Decision = report.Decision
		}
	}
	return out
}

func summarizeCell(key cell, trials []pair, index map[pairKey]attempt, rank int) cellSummary {
	s := cellSummary{Cell: key, ExpectedPairs: len(trials), Decision: decisionPass}
	var ratios []float64
	for _, p := range trials {
		serial, serialFound := index[pairKey{p.Cell, p.Block, serialArm}]
		candidate, candidateFound := index[pairKey{p.Cell, p.Block, defaultArm}]
		if !serialFound || !candidateFound {
			s.MissingPairs++
			continue
		}
		if ratio, ok := s.evaluatePair(serial, candidate, p.Block); ok {
			ratios = append(ratios, ratio)
		}
	}
	s.finish(ratios, rank)
	return s
}

func (s *cellSummary) evaluatePair(serial, candidate attempt, block int) (float64, bool) {
	serialOOM, candidateOOM := serial.oom(), candidate.oom()
	if serialOOM {
		s.BaselineOOMPairs++
	}
	if candidateOOM {
		s.DefaultOOMPairs++
	}
	switch candidate.Worker.Workers {
	case 0:
		// A failed worker may leave no admission record.
	case 1:
		s.FallbackPairs++
	default:
		s.ParallelPairs++
	}
	overshoot := serial.overshoot() || candidate.overshoot()
	if overshoot {
		s.PeakOvershoots++
		s.regression(block, "memory.peak exceeded memory.max")
	}
	serialOK, defaultOK := serial.succeeded(), candidate.succeeded()
	if !serialOK || !defaultOK {
		s.FailedPairs++
		if (serialOK && !defaultOK && candidate.ControlError == "") || (candidateOOM && !serialOOM) {
			s.regression(block, "default Pack introduced a failure or OOM")
		}
		return 0, false
	}
	if serial.ArtifactSHA256 != candidate.ArtifactSHA256 || serial.ArtifactBytes != candidate.ArtifactBytes {
		s.ArtifactMismatches++
		s.regression(block, "complete artifacts differ")
		return 0, false
	}
	s.CompletePairs++
	return float64(candidate.Worker.PackNS) / float64(serial.Worker.PackNS), !overshoot
}

func (s *cellSummary) regression(block int, reason string) {
	s.Decision = decisionRegression
	s.Reasons = append(s.Reasons, fmt.Sprintf("block %d: %s", block, reason))
}

func (s *cellSummary) finish(ratios []float64, rank int) {
	if len(ratios) > 0 {
		slices.Sort(ratios)
		s.MedianRatio = new((ratios[len(ratios)/2] + ratios[(len(ratios)-1)/2]) * binomialHalf)
	}
	if len(ratios) != blocks || rank == 0 {
		if s.Decision != decisionRegression {
			s.Decision = decisionInconclusive
		}
		return
	}
	s.LowerRatio, s.UpperRatio = &ratios[rank-1], &ratios[blocks-rank]
	switch {
	case s.Decision == decisionRegression:
	case *s.LowerRatio > maxRegression:
		s.Decision = decisionRegression
		s.Reasons = append(s.Reasons, "paired lower bound exceeds 5% regression")
	case s.FallbackPairs > 0 && s.ParallelPairs > 0:
		s.Decision = decisionInconclusive
		s.Reasons = append(s.Reasons, "default admission varied across blocks")
	case *s.UpperRatio > maxRegression:
		s.Decision = decisionInconclusive
		s.Reasons = append(s.Reasons, "paired upper bound does not exclude a greater than 5% regression")
	case s.ParallelPairs == blocks && *s.UpperRatio < usefulRatio:
		s.Reasons = append(s.Reasons, "paired upper bound establishes more than 10% packing-time reduction")
	}
}

func (a attempt) succeeded() bool {
	if a.ExitCode == nil || *a.ExitCode != 0 || a.TimedOut || a.oom() ||
		a.ArtifactSHA256 == "" || a.ArtifactBytes <= 0 || a.Worker.PackNS <= 0 || a.Worker.Error != "" ||
		a.Failure != "" || a.ControlError != "" || !hasTelemetry(a) {
		return false
	}
	maxWorkers := min(a.Cell.CPUs, variantsPerGroup)
	if a.Arm == serialArm {
		maxWorkers = 1
	}
	return a.Worker.Workers >= 1 && a.Worker.Workers <= maxWorkers
}

func (a attempt) oom() bool {
	for _, key := range []string{cgroupOOMEvents, cgroupOOMKills} {
		if value := a.Cgroup[key].Value; value != nil && *value > 0 {
			return true
		}
	}
	return false
}

func (a attempt) overshoot() bool {
	peak := a.Cgroup["memory.peak"].Value
	return peak != nil && a.Cell.MemoryMiB > 0 && *peak > float64(a.Cell.MemoryMiB*mebibyte)
}

// medianRank uses two-sided exact binomial tails as in #389's order-statistic analysis.
func medianRank(n int, alpha float64) int {
	rank, tail := 0, 0.0
	probability := math.Pow(binomialHalf, float64(n))
	for k := 1; k <= (n+1)/2; k++ {
		tail += probability
		if 2*tail > alpha {
			break
		}
		rank = k
		probability *= float64(n-k+1) / float64(k)
	}
	return rank
}
