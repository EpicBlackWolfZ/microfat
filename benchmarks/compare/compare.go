// Package compare builds counterbalanced schedules and compares matched trial observations.
package compare

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/analysis"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const scheduleStream = uint64(0x853c49e6748fea9b)

func Schedule(configs []schema.Configuration, blocks int, seed uint64) ([]schema.ScheduledTrial, error) {
	if len(configs) < 2 || blocks <= 0 || blocks > schema.MaxTrials/len(configs) {
		return nil, errors.New("invalid schedule dimensions")
	}
	seen := make(map[string]bool)
	for _, cfg := range configs {
		if cfg.ID == "" || seen[cfg.ID] {
			return nil, errors.New("empty or duplicate configuration id")
		}
		seen[cfg.ID] = true
	}
	rng := rand.New(rand.NewPCG(seed, seed^scheduleStream)) // #nosec G404 -- recorded experimental schedule seed.
	order := rng.Perm(len(configs))
	result := make([]schema.ScheduledTrial, 0, blocks*len(configs))
	for block := range blocks {
		if block > 0 && block%len(configs) == 0 {
			order = rng.Perm(len(configs))
		}
		for position := range configs {
			cfg := configs[order[(position+block)%len(configs)]]
			result = append(result, schema.ScheduledTrial{ID: fmt.Sprintf("trial-%06d", len(result)),
				Block: block, Position: len(result), ConfigurationID: cfg.ID})
		}
	}
	return result, nil
}

// Revisions pairs base/head configurations inside one counterbalanced experiment schedule.
func Revisions(exp *schema.ExperimentV2) ([]schema.Comparison, error) {
	if err := schema.ValidateV2(exp); err != nil {
		return nil, err
	}
	var result []schema.Comparison
	for _, cfg := range exp.Configurations {
		if !strings.HasPrefix(cfg.ID, "head-") {
			continue
		}
		baseline := "base-" + strings.TrimPrefix(cfg.ID, "head-")
		metrics := make(map[string]bool)
		for _, trial := range exp.Trials {
			if trial.ConfigurationID == cfg.ID || trial.ConfigurationID == baseline {
				for name := range trial.Metrics {
					metrics[name] = true
				}
			}
		}
		keys := make([]string, 0, len(metrics))
		for key := range metrics {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			comparison, err := Metric(exp, baseline, cfg.ID, key)
			if err != nil {
				return nil, err
			}
			result = append(result, comparison)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("no paired revision configurations")
	}
	return result, nil
}

func All(exp *schema.ExperimentV2, baseline string) ([]schema.Comparison, error) {
	if err := schema.ValidateV2(exp); err != nil {
		return nil, err
	}
	var found bool
	for _, cfg := range exp.Configurations {
		found = found || cfg.ID == baseline
	}
	if !found {
		return nil, errors.New("baseline configuration not found")
	}
	var result []schema.Comparison
	for _, cfg := range exp.Configurations {
		if cfg.ID == baseline {
			continue
		}
		metrics := make(map[string]bool)
		for _, t := range exp.Trials {
			if t.ConfigurationID == baseline || t.ConfigurationID == cfg.ID {
				for name := range t.Metrics {
					metrics[name] = true
				}
			}
		}
		keys := make([]string, 0, len(metrics))
		for name := range metrics {
			keys = append(keys, name)
		}
		slices.Sort(keys)
		for _, metric := range keys {
			comparison, err := Metric(exp, baseline, cfg.ID, metric)
			if err != nil {
				return nil, err
			}
			result = append(result, comparison)
		}
	}
	return result, nil
}

func Metric(exp *schema.ExperimentV2, baseline, candidate, metric string) (schema.Comparison, error) {
	base := make(map[int]schema.ProcessTrial)
	cand := make(map[int]schema.ProcessTrial)
	for _, trial := range exp.Trials {
		if trial.ConfigurationID == baseline {
			base[trial.Block] = trial
		}
		if trial.ConfigurationID == candidate {
			cand[trial.Block] = trial
		}
	}
	blocks := make(map[int]bool)
	for _, trial := range exp.Schedule {
		if trial.ConfigurationID == baseline || trial.ConfigurationID == candidate {
			blocks[trial.Block] = true
		}
	}
	var pairs []schema.Pair
	unit := "unavailable"
	for block := range blocks {
		a, b := base[block], cand[block]
		av, bv := a.Metrics[metric], b.Metrics[metric]
		if av.Unit != "" {
			unit = av.Unit
		} else if bv.Unit != "" {
			unit = bv.Unit
		}
		if metric != "trial_success" && (a.Outcome != schema.OutcomeOK || b.Outcome != schema.OutcomeOK) || av.Value == nil || bv.Value == nil {
			continue
		}
		if av.Unit != bv.Unit || av.Phase != bv.Phase || av.Source != bv.Source {
			return schema.Comparison{}, fmt.Errorf("incompatible measurement scopes for %s", metric)
		}
		pairs = append(pairs, schema.Pair{Block: block, BaselineTrial: a.ID, CandidateTrial: b.ID,
			Baseline: *av.Value, Candidate: *bv.Value})
	}
	slices.SortFunc(pairs, func(a, b schema.Pair) int { return a.Block - b.Block })
	var result schema.Comparison
	if len(pairs) > 0 {
		var err error
		result, err = analysis.Paired(pairs, exp.Seed, analysis.BootstrapResamples)
		if err != nil {
			return result, err
		}
	}
	result.Baseline, result.Candidate, result.Metric, result.Unit = baseline, candidate, metric, unit
	result.Direction = "lower"
	if metric == "trial_success" || strings.HasPrefix(metric, "throughput_") || metric == "packaging_ratio" {
		result.Direction = "higher"
	}
	if len(pairs) != len(blocks) || len(pairs) == 0 {
		result.Status, result.Reason, result.Significant = "inconclusive", "missing, failed or unavailable paired observations", false
	}
	return result, nil
}
