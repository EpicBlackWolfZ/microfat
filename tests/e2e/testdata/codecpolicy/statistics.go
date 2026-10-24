//go:build linux

package main

import (
	"math"
	"math/rand/v2"
	"slices"
)

type cell struct {
	group
	Phase, Setting, Stub, Mode string
}
type distribution struct {
	Count            int
	Median, Min, Max float64
}
type interval struct{ Median, Lower95, Upper95 float64 }
type comparison struct {
	Reference                   string
	CompletePairs, PlannedPairs int
	Ratios, AbsoluteSavings     map[string]interval
	ConditionalOnSuccess        bool
	UndefinedRatios             []string
}
type cellSummary struct {
	cell
	Planned, Recorded, Successful int
	Metrics                       map[string]distribution
	Comparisons                   []comparison
}

func cellOf(s step) cell { return cell{s.group, s.Phase, s.Setting, s.Stub, s.Mode} }

func validObservation(o observation) bool {
	ceiling := int64(launchRSSKiB)
	if o.Phase == "pack" {
		ceiling = packRSSKiB
	}
	return o.Success && o.Error == "" && o.ResourcesObserved && o.WallNS > 0 && o.CPUNS >= 0 &&
		o.MaxRSSKiB >= 0 && o.MaxRSSKiB <= ceiling
}

func metrics(o observation) map[string]float64 {
	result := map[string]float64{"wall_ns": float64(o.WallNS), "cpu_ns": float64(o.CPUNS), "max_rss_kib": float64(o.MaxRSSKiB)}
	if o.Phase == "pack" {
		result["complete_bytes"] = float64(o.Bytes)
	}
	return result
}

func summarize(plan []step, observations []observation) (map[string]any, bool) {
	valid := len(plan) == len(observations)
	planned := map[cell]int{}
	var order []cell
	for _, s := range plan {
		c := cellOf(s)
		if planned[c] == 0 {
			order = append(order, c)
		}
		planned[c]++
	}
	byCell := map[cell]map[int]observation{}
	failures := 0
	for i, o := range observations {
		if !validObservation(o) || i >= len(plan) || o.step != plan[i] {
			valid = false
			failures++
		}
		c := cellOf(o.step)
		if byCell[c] == nil {
			byCell[c] = map[int]observation{}
		}
		if _, exists := byCell[c][o.Round]; exists {
			valid = false
		}
		byCell[c][o.Round] = o
	}
	var cells []cellSummary
	for _, c := range order {
		result := cellSummary{cell: c, Planned: planned[c], Recorded: len(byCell[c]), Metrics: map[string]distribution{}}
		values := map[string][]float64{}
		for round := range planned[c] {
			o, exists := byCell[c][round]
			if !exists || !validObservation(o) {
				continue
			}
			result.Successful++
			for name, value := range metrics(o) {
				values[name] = append(values[name], value)
			}
		}
		for name, value := range values {
			result.Metrics[name] = describe(value)
		}
		if c.Setting != nativeSetting {
			for _, ref := range []string{"latency", balancedProfile, "size"} {
				refCell := c
				refCell.Setting = ref
				pairs := map[string][][2]float64{}
				complete := 0
				for round := range planned[c] {
					a, aOK := byCell[c][round]
					b, bOK := byCell[refCell][round]
					if !aOK || !bOK || !validObservation(a) || !validObservation(b) {
						continue
					}
					complete++
					bm := metrics(b)
					for name, av := range metrics(a) {
						pairs[name] = append(pairs[name], [2]float64{av, bm[name]})
					}
				}
				comparison := comparison{Reference: ref, PlannedPairs: planned[c], CompletePairs: complete,
					Ratios: map[string]interval{}, AbsoluteSavings: map[string]interval{}, ConditionalOnSuccess: complete != planned[c]}
				for name, pair := range pairs {
					ratio, saving := pairedBootstrap(pair)
					comparison.AbsoluteSavings[name] = saving
					if ratioDefined(pair) {
						comparison.Ratios[name] = ratio
					} else {
						comparison.UndefinedRatios = append(comparison.UndefinedRatios, name)
					}
				}
				result.Comparisons = append(result.Comparisons, comparison)
			}
		}
		cells = append(cells, result)
	}
	return map[string]any{"planned_attempts": len(plan), "recorded_attempts": len(observations), "failed_attempts": failures,
		"cohort_pass": valid, "cells": cells, "default_policy_decision": "retain current defaults",
		"scope": "single native host; descriptive cellwise intervals; no fleet, p99, memory-cap or default-policy qualification",
		"rss_scope": "wait4 can include pre-exec parent/shared-mm high-water RSS; " +
			"not isolated encoder/decoder/payload memory or cgroup peak",
		"bootstrap":      "2000 paired-round resamples, seed42; 95% percentile endpoints floor(p*(N-1)); no multiplicity adjustment",
		"failure_policy": "all failures retained; successful-only metrics explicitly conditional; missing pairs never qualify"}, valid
}

func describe(values []float64) distribution {
	if len(values) == 0 {
		return distribution{}
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	return distribution{len(values), median(ordered), ordered[0], ordered[len(ordered)-1]}
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	mid := len(ordered) / halves
	if len(ordered)%halves == 1 {
		return ordered[mid]
	}
	return (ordered[mid-1] + ordered[mid]) / halves
}

func pairedBootstrap(pairs [][2]float64) (interval, interval) {
	if len(pairs) == 0 {
		return interval{}, interval{}
	}
	const resamples = bootstrapResamples
	// #nosec G404 -- fixed statistical resampling, no security purpose.
	rng := rand.New(rand.NewPCG(bootstrapSeed, bootstrapSeed))
	ratios, savings := make([]float64, len(pairs)), make([]float64, len(pairs))
	for i, pair := range pairs {
		if pair[1] > 0 {
			ratios[i] = pair[0] / pair[1]
		}
		savings[i] = pair[1] - pair[0]
	}
	ratioCenter, savingCenter := median(ratios), median(savings)
	ratioEstimates, savingEstimates := make([]float64, resamples), make([]float64, resamples)
	for i := range resamples {
		for j := range pairs {
			pair := pairs[rng.IntN(len(pairs))]
			if pair[1] > 0 {
				ratios[j] = pair[0] / pair[1]
			} else {
				ratios[j] = 0
			}
			savings[j] = pair[1] - pair[0]
		}
		ratioEstimates[i], savingEstimates[i] = median(ratios), median(savings)
	}
	slices.Sort(ratioEstimates)
	slices.Sort(savingEstimates)
	lower, upper := int(math.Floor(lowerTail*(resamples-1))), int(math.Floor(upperTail*(resamples-1)))
	return interval{ratioCenter, ratioEstimates[lower], ratioEstimates[upper]},
		interval{savingCenter, savingEstimates[lower], savingEstimates[upper]}
}

func ratioDefined(pairs [][2]float64) bool {
	for _, pair := range pairs {
		if pair[1] <= 0 {
			return false
		}
	}
	return len(pairs) > 0
}
