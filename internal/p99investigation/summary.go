package p99investigation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/ci"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/load"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/report"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

type Summary struct {
	Plan          *Plan             `json:"plan"`
	Bundle        string            `json:"bundle"`
	Complete      bool              `json:"complete"`
	Reasons       []string          `json:"reasons"`
	Qualification *ci.Qualification `json:"qualification"`
	Analysis      *Analysis         `json:"analysis"`
	Disposition   string            `json:"disposition"`
}

func VerifyPlan(plan Plan, exp *schema.ExperimentV2) error {
	if err := schema.ValidateV2(exp); err != nil {
		return err
	}
	if exp.SourceSHA != plan.HarnessSHA || exp.Dirty || exp.Environment.Host.Arch != "amd64" || exp.Seed != plan.Config.Seed ||
		!reflect.DeepEqual(exp.Schedule, plan.Schedule) {
		return errors.New("source, native architecture or schedule differs from frozen plan")
	}
	expected, err := schema.CanonicalV2(plan.Config)
	if err != nil {
		return err
	}
	digest, err := schema.ConfigDigest(expected)
	if err != nil {
		return err
	}
	if exp.ConfigSHA256 != digest {
		return errors.New("recorded configuration differs from frozen plan")
	}
	if err := verifyArtifacts(plan, exp); err != nil {
		return err
	}
	if err := verifyConfigurations(plan, exp); err != nil {
		return err
	}
	if err := verifyControls(exp); err != nil {
		return err
	}
	return verifyEffectiveRuntime(exp)
}

func Summarize(output string) error {
	summary := Summary{Disposition: "Maintainer review required; this cohort alone does not resolve the historical signals."}
	plan, err := readPlan(output)
	if err != nil {
		summary.Reasons = append(summary.Reasons, "no valid frozen plan: "+err.Error())
	} else {
		summary.Plan = &plan
	}
	var bundles []string
	root := filepath.Join(output, "evidence")
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == "SHA256SUMS" {
			bundles = append(bundles, filepath.Dir(path))
		}
		return nil
	})
	if walkErr != nil || len(bundles) != 1 {
		summary.Reasons = append(summary.Reasons, "exactly one completed checksummed bundle required; partial evidence remains retained")
	} else if summary.Plan != nil {
		summary.Bundle = bundles[0]
		analyzeBundle(&summary)
	}
	slices.Sort(summary.Reasons)
	if err := os.MkdirAll(output, directoryMode); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(output, "summary.json"), summary); err != nil {
		return err
	}
	text := fmt.Sprintf("# Historical service-p99 investigation\n\nComplete qualified cohort: %t.\n\n%s\n",
		summary.Complete, summary.Disposition)
	for _, reason := range summary.Reasons {
		text += "\n- " + reason + "\n"
	}
	text += "\nSee summary.json for per-trial p99 paired comparisons, controls and conditional interval decisions.\n"
	if err := os.WriteFile(filepath.Join(output, "summary.md"), []byte(text), fileMode); err != nil {
		return err
	}
	if !summary.Complete {
		return errors.New("investigation cohort is incomplete or unqualified; see retained summary.json")
	}
	return nil
}

func analyzeBundle(summary *Summary) {
	exp, err := report.ReadBundle(summary.Bundle)
	if err != nil {
		summary.Reasons = append(summary.Reasons, "bundle verification: "+err.Error())
		return
	}
	if err := VerifyPlan(*summary.Plan, exp); err != nil {
		summary.Reasons = append(summary.Reasons, "protocol verification: "+err.Error())
		return
	}
	qualification := ci.QualifyHosted(exp)
	summary.Qualification = &qualification
	if !qualification.Publishable {
		summary.Reasons = append(summary.Reasons, qualification.Reasons...)
	}
	analysis, err := Analyze(exp, summary.Plan.Scenario)
	if err != nil {
		summary.Reasons = append(summary.Reasons, "analysis: "+err.Error())
		return
	}
	summary.Analysis = &analysis
	summary.Reasons = append(summary.Reasons, analysis.Reasons...)
	summary.Complete = qualification.Publishable && exp.Complete && analysis.Complete && len(summary.Reasons) == 0
}

func verifyArtifacts(plan Plan, exp *schema.ExperimentV2) error {
	artifacts := make(map[string]schema.Artifact)
	for _, artifact := range exp.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	for _, id := range []string{"native-v1", "native-" + plan.Config.SpecializedLevel, "stub", "head-packer", "fat",
		"base-stub", "base-packer", "base-fat", "harness", "fortio"} {
		a, ok := artifacts[id]
		if !ok {
			return fmt.Errorf("missing identified artifact %s", id)
		}
		sha := HeadSHA
		switch {
		case strings.HasPrefix(id, "base-"):
			sha = BaseSHA
		case id == "harness":
			sha = plan.HarnessSHA
		case id == "fortio":
			sha = load.SourceCommit()
		}
		if a.SourceSHA != sha || a.GoVersion != "go1.27.3" || a.Settings["vcs.modified"] == "true" ||
			a.Settings["vcs.revision"] != "" && a.Settings["vcs.revision"] != sha {
			return fmt.Errorf("artifact %s source/tool identity differs from frozen plan", id)
		}
	}
	return nil
}

func verifyConfigurations(plan Plan, exp *schema.ExperimentV2) error {
	if len(exp.Configurations) != len(configurationIDs()) {
		return errors.New("historical ten-arm comparison is incomplete")
	}
	byID := make(map[string]schema.Configuration)
	for _, cfg := range exp.Configurations {
		byID[cfg.ID] = cfg
	}
	for _, id := range configurationIDs() {
		cfg, ok := byID[id.ID]
		if !ok {
			return fmt.Errorf("missing configuration %s", id.ID)
		}
		if strings.Contains(id.ID, "native-") {
			level := plan.Config.SpecializedLevel
			if strings.HasSuffix(id.ID, "generic") {
				level = "v1"
			}
			if cfg.ArtifactID != "native-"+level || cfg.Level != level || cfg.Mode != "native" {
				return errors.New("native controls must use identical historical-head payload bytes")
			}
		} else {
			artifactID, mode, cache := "fat", "cache", "cold"
			if strings.HasPrefix(id.ID, "base-") {
				artifactID = "base-fat"
			}
			if strings.HasSuffix(id.ID, "memfd") {
				mode, cache = "memfd", "absent"
			} else if strings.HasSuffix(id.ID, "warm") {
				cache = "warm"
			}
			if cfg.Level != plan.Config.SpecializedLevel || cfg.ArtifactID != artifactID || cfg.Mode != mode || cfg.Cache != cache {
				return errors.New("fat configuration changed historical source, ISA level or execution path")
			}
		}
		expectedEnvironment := map[string]string{"MICROFAT_AUTOTUNE": "0", "GOMAXPROCS": "2", "GOMEMLIMIT": "1073741824", "GOGC": "100"}
		if cfg.Tuning != "matched" || !reflect.DeepEqual(cfg.Environment, expectedEnvironment) {
			return errors.New("configuration changed historical runtime tuning")
		}
	}
	return nil
}

func verifyControls(exp *schema.ExperimentV2) error {
	expected := map[string]string{"target.affinity": "0", "generator.affinity": "3",
		"target.cpu.max": "100000 100000", "generator.cpu.max": "100000 100000",
		"target.memory.max": "2147483648", "generator.memory.max": "2147483648"}
	for _, trial := range exp.Trials {
		if trial.Outcome != schema.OutcomeOK {
			continue
		}
		for name, value := range expected {
			found := false
			for _, control := range trial.Controls {
				if control.Name == name {
					if found || control.Requested != value || control.Effective != value || control.State != "applied" {
						return fmt.Errorf("trial %s changed frozen control %s", trial.ID, name)
					}
					found = true
				}
			}
			if !found {
				return fmt.Errorf("trial %s missing frozen control %s", trial.ID, name)
			}
		}
	}
	return nil
}

func verifyEffectiveRuntime(exp *schema.ExperimentV2) error {
	configs := make(map[string]schema.Configuration)
	artifacts := make(map[string]schema.Artifact)
	for _, cfg := range exp.Configurations {
		configs[cfg.ID] = cfg
	}
	for _, artifact := range exp.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	for _, trial := range exp.Trials {
		if trial.Outcome != schema.OutcomeOK {
			continue
		}
		cfg := configs[trial.ConfigurationID]
		expected := map[string]string{"go_version": "go1.27.3", "level": cfg.Level,
			"gomaxprocs": "2", "gomemlimit": "1073741824", "gogc": "100"}
		if cfg.Mode != "native" {
			expected["MICROFAT_EXEC_MODE"] = cfg.Mode
			expected["MICROFAT_SELECTED_VARIANT"] = cfg.Level
			expected["MICROFAT_SELECTED_SHA256"] = artifacts["native-"+cfg.Level].SHA256
		}
		for key, value := range expected {
			if trial.Target.Effective[key] != value {
				return fmt.Errorf("trial %s changed effective runtime or selected payload: %s", trial.ID, key)
			}
		}
	}
	return nil
}
