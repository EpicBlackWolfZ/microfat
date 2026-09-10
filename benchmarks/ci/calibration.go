package ci

import (
	json "encoding/json/v2"
	"errors"
	"math"
	"slices"
	"strconv"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/compare"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const TrainingRuns = 20
const HoldoutRuns = 10
const calibrationMargin = 2

type Calibration struct {
	Version string             `json:"version"`
	Classes []CalibrationClass `json:"classes"`
}

type CalibrationClass struct {
	Key      string   `json:"key"`
	FloorNS  float64  `json:"floor_ns"`
	Status   string   `json:"status"`
	Reason   string   `json:"reason"`
	Training []string `json:"training"`
	Holdout  []string `json:"holdout"`
	Bundles  []string `json:"bundle_digests"`
}

// ClassKey excludes revision, names and transient cgroup paths, but includes measurement settings.
func ClassKey(exp *schema.ExperimentV2) (string, error) {
	if exp.Runner == nil || exp.Runner.Provider != schema.HostedProvider || exp.Runner.Protocol != schema.StartupProtocol ||
		exp.Runner.Image == "" || exp.Runner.ImageVersion == "" {
		return "", errors.New("runner class unavailable")
	}
	var config map[string]any
	if err := json.Unmarshal(exp.Config, &config); err != nil {
		return "", err
	}
	for _, key := range []string{"name", "runner_identity"} {
		delete(config, key)
	}
	for _, role := range []string{"target", "generator"} {
		if controls, ok := config[role].(map[string]any); ok {
			delete(controls, "cgroup_root")
			delete(controls, "memory_root")
		}
	}
	identity := []any{exp.Runner.Image, exp.Runner.ImageVersion, exp.Runner.Protocol, exp.Environment.Host.Arch,
		exp.Environment.Host.CPU.ModelName, exp.Environment.Host.KernelRelease, exp.Environment.Process.GoVersion, config}
	data, err := schema.CanonicalV2(identity)
	return schema.Digest(data), err
}

func (c Calibration) Policy(exp *schema.ExperimentV2) (Policy, string) {
	p := DefaultPolicy()
	key, err := ClassKey(exp)
	if err != nil || c.Version != "v1" {
		return p, "runner class or calibration version unavailable"
	}
	for _, class := range c.Classes {
		if class.Key != key {
			continue
		}
		if class.Status != "validated" || len(class.Training) < TrainingRuns || len(class.Holdout) < HoldoutRuns ||
			!schema.Finite(class.FloorNS) || class.FloorNS <= 0 {
			return p, "calibration is insufficient or unstable: " + class.Reason
		}
		p.Calibrated, p.StartupFloorNS = true, class.FloorNS
		return p, "matched independent no-change calibration"
	}
	return p, "no calibration for current runner class and measurement settings"
}

type calibrationRun struct {
	id         string
	comparison []schema.Comparison
	holdout    bool
}

func Calibrate(experiments []*schema.ExperimentV2) (Calibration, error) {
	result := Calibration{Version: "v1", Classes: []CalibrationClass{}}
	groups := make(map[string][]calibrationRun)
	seen := make(map[string]bool)
	for _, exp := range experiments {
		key, run, err := calibrationInput(exp)
		if err != nil {
			return result, err
		}
		if seen[run.id] {
			return result, errors.New("duplicate calibration job")
		}
		seen[run.id] = true
		groups[key] = append(groups[key], run)
	}
	for key, runs := range groups {
		class := CalibrationClass{Key: key, Status: "report_only", Reason: "insufficient independent jobs",
			Training: []string{}, Holdout: []string{}, Bundles: []string{}}
		for _, run := range runs {
			if run.holdout {
				class.Holdout = append(class.Holdout, run.id)
				continue
			}
			class.Training = append(class.Training, run.id)
			for _, c := range run.comparison {
				if c.Metric == startupMetric {
					class.FloorNS = math.Max(class.FloorNS, calibrationMargin*math.Abs(c.MedianDifference))
				}
			}
		}
		if len(class.Training) >= TrainingRuns && len(class.Holdout) >= HoldoutRuns && class.FloorNS > 0 {
			class.Status, class.Reason = "validated", "independent holdout jobs passed"
			for _, run := range runs {
				if !run.holdout {
					continue
				}
				policy := DefaultPolicy()
				policy.Calibrated, policy.StartupFloorNS = true, class.FloorNS
				_, regression, err := Evaluate(run.comparison, policy)
				if err != nil || regression {
					class.Status, class.Reason = "report_only", "holdout regression detected"
				}
			}
		}
		slices.Sort(class.Training)
		slices.Sort(class.Holdout)
		result.Classes = append(result.Classes, class)
	}
	slices.SortFunc(result.Classes, func(a, b CalibrationClass) int {
		if a.Key < b.Key {
			return -1
		}
		if a.Key > b.Key {
			return 1
		}
		return 0
	})
	return result, nil
}

func calibrationInput(exp *schema.ExperimentV2) (string, calibrationRun, error) {
	var run calibrationRun
	key, err := ClassKey(exp)
	if err != nil {
		return key, run, err
	}
	r := exp.Runner
	repetition, err := strconv.Atoi(r.Repetition)
	if err != nil || repetition < 0 || r.RunID == "" || r.Job == "" || r.Attempt == "" || exp.Dirty || !exp.Complete {
		return key, run, errors.New("invalid calibration job provenance")
	}
	for _, artifact := range exp.Artifacts {
		if artifact.ID != "fortio" && artifact.SourceSHA != exp.SourceSHA {
			return key, run, errors.New("calibration requires identical source revisions")
		}
	}
	for _, t := range exp.Trials {
		if t.Outcome != schema.OutcomeOK || t.Metrics[startupMetric].Source != schema.StartupProtocol {
			return key, run, errors.New("calibration requires successful compatible measurements")
		}
	}
	comparisons, err := compare.Revisions(exp)
	if err != nil {
		return key, run, err
	}
	startup := false
	for _, c := range comparisons {
		if c.Metric == startupMetric {
			startup = true
			if c.Status != "complete" {
				return key, run, errors.New("incomplete calibration comparison")
			}
		}
	}
	if !startup {
		return key, run, errors.New("startup calibration absent")
	}
	run = calibrationRun{id: r.Repository + "/" + r.RunID + "/" + r.Attempt + "/" + r.Job + "/" + r.Repetition,
		comparison: comparisons, holdout: repetition >= TrainingRuns}
	return key, run, nil
}
