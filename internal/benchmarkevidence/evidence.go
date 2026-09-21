// Package benchmarkevidence independently replays and archives hosted measurements.
package benchmarkevidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/EpicBlackWolfZ/microfat/internal/releaseworkflow"
)

const (
	Calibration        = "calibration"
	Archive            = "archive"
	Shard              = "shard"
	standardIterations = 4
	heavyIterations    = 32
	fileMode           = 0o600
	directoryMode      = 0o700
)

type Options struct{ Root, Mode, WorkDir string }
type Execute func(...string) ([]byte, error)
type Environment = releaseworkflow.Environment

type experiment struct {
	Source          string `json:"source_sha"`
	ReleaseEligible bool   `json:"release_eligible"`
	Config          struct {
		Workload   string `json:"workload"`
		Iterations int    `json:"iterations"`
	} `json:"config"`
	Environment struct {
		Host struct {
			Arch string `json:"arch"`
		} `json:"host"`
	} `json:"environment"`
	Runner struct {
		RunID   string `json:"run_id"`
		Attempt string `json:"attempt"`
	} `json:"runner"`
}

type VerifiedBundle struct {
	Bundle           string `json:"bundle"`
	Source           string `json:"source"`
	ChecksumManifest string `json:"checksum_manifest"`
}

type scenario [3]string

func discover(root string) ([]string, error) {
	var bundles []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == "SHA256SUMS" && !entry.IsDir() {
			bundles = append(bundles, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(bundles) == 0 {
		return nil, errors.New("no completed evidence bundles")
	}
	slices.Sort(bundles)
	return bundles, nil
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path) // #nosec G304 -- explicit local evidence input.
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func SourceCohort(root, runID, attempt string) (string, error) {
	var run releaseworkflow.WorkflowRun
	if err := readJSON(filepath.Join(root, "source-run.json"), &run); err != nil {
		return "", err
	}
	var jobs struct {
		Jobs []releaseworkflow.Job `json:"jobs"`
	}
	if err := readJSON(filepath.Join(root, "source-jobs.json"), &jobs); err != nil {
		return "", err
	}
	return releaseworkflow.SourceRevision(run, jobs.Jobs, runID, attempt)
}

func Run(options Options, env Environment, execute Execute, out io.Writer) error {
	if !slices.Contains([]string{Calibration, Archive, Shard}, options.Mode) {
		return errors.New("select exactly one evidence mode")
	}
	bundles, err := discover(options.Root)
	if err != nil {
		return err
	}
	observed := make(map[scenario]bool)
	var verified []VerifiedBundle
	for _, bundle := range bundles {
		exp, err := verifyReplay(bundle, execute)
		if err != nil {
			return err
		}
		if options.Mode != Calibration {
			key, err := qualify(bundle, exp, env, execute)
			if err != nil {
				return err
			}
			if observed[key] {
				return fmt.Errorf("duplicate release shard: %v", key)
			}
			observed[key] = true
		}
		hash, err := releaseworkflow.HashFile(filepath.Join(bundle, "SHA256SUMS"))
		if err != nil {
			return err
		}
		verified = append(verified, VerifiedBundle{Bundle: bundle, Source: exp.Source, ChecksumManifest: hash})
	}
	if options.Mode == Calibration {
		if err := calibrate(options.WorkDir, bundles, execute, out); err != nil {
			return err
		}
	} else if options.Mode == Archive {
		if err := checkMatrix(observed); err != nil {
			return err
		}
		if err := archive(options, verified, env); err != nil {
			return err
		}
	}
	return appendSummary(env("GITHUB_STEP_SUMMARY"), len(bundles), options.Mode)
}

func verifyReplay(bundle string, execute Execute) (experiment, error) {
	if _, err := execute("verify", bundle); err != nil {
		return experiment{}, err
	}
	for _, report := range []struct{ format, name string }{{"json", "report.json"}, {"markdown", "report.md"}} {
		actual, err := execute("report", "--input", bundle, "--format", report.format)
		if err != nil {
			return experiment{}, err
		}
		expected, err := os.ReadFile(filepath.Join(bundle, report.name)) // #nosec G304 -- report beneath selected evidence bundle.
		if err != nil {
			return experiment{}, err
		}
		// The CLI adds exactly one newline; do not trim or normalize report bytes.
		if !bytes.Equal(actual, append(expected, '\n')) {
			return experiment{}, fmt.Errorf("offline report replay differs: %s/%s", bundle, report.name)
		}
	}
	var exp experiment
	err := readJSON(filepath.Join(bundle, "raw.json"), &exp)
	return exp, err
}

func qualify(bundle string, exp experiment, env Environment, execute Execute) (scenario, error) {
	data, err := execute("qualify", "--input", bundle, "--policy", "hosted-release")
	if err != nil {
		return scenario{}, err
	}
	var verdict struct {
		Publishable bool `json:"publishable"`
	}
	if err := json.Unmarshal(data, &verdict); err != nil {
		return scenario{}, err
	}
	if !verdict.Publishable || exp.ReleaseEligible {
		return scenario{}, errors.New("hosted qualification failed")
	}
	intensity := ""
	switch exp.Config.Iterations {
	case standardIterations:
		intensity = "standard"
	case heavyIterations:
		intensity = "heavy"
	}
	key := scenario{exp.Environment.Host.Arch, exp.Config.Workload, intensity}
	if exp.Source != fallback(env("SOURCE_SHA"), env("GITHUB_SHA"), exp.Source) {
		return key, errors.New("source revision differs from requested workflow revision")
	}
	runID, attempt := measurement(env)
	if runID != "" && (exp.Runner.RunID != runID || exp.Runner.Attempt != attempt) {
		return key, errors.New("measurement run or attempt differs from requested evidence cohort")
	}
	return key, nil
}

func fallback(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func measurement(env Environment) (string, string) {
	return fallback(env("EVIDENCE_RUN_ID"), env("GITHUB_RUN_ID")), fallback(env("EVIDENCE_RUN_ATTEMPT"), env("GITHUB_RUN_ATTEMPT"), "1")
}

func checkMatrix(observed map[scenario]bool) error {
	const expectedCount = 12
	if len(observed) != expectedCount {
		return errors.New("release matrix must contain exactly 12 distinct shards")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, workload := range []string{"mixed", "cpu", "memory"} {
			for _, intensity := range []string{"standard", "heavy"} {
				if !observed[scenario{arch, workload, intensity}] {
					return errors.New("release matrix missing an expected shard")
				}
			}
		}
	}
	return nil
}

func calibrate(work string, bundles []string, execute Execute, out io.Writer) error {
	var paths []string
	for _, bundle := range bundles {
		absolute, err := filepath.Abs(bundle)
		if err != nil {
			return err
		}
		paths = append(paths, absolute)
	}
	path := filepath.Join(work, "calibration-bundles.json")
	if err := writeJSON(path, paths); err != nil {
		return err
	}
	policy, err := execute("calibrate", "--input", path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "calibration-policy.json"), append(bytes.Clone(policy), '\n'), fileMode); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(policy))
	return err
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), fileMode) // #nosec G304 -- selected evidence metadata output.
}

func appendSummary(path string, count int, mode string) error {
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, fileMode) // #nosec G304 -- GitHub-provided summary path.
	if err != nil {
		return err
	}
	text := fmt.Sprintf("\nVerified %d evidence bundles with byte-identical offline report replay.\n", count)
	if mode != Calibration {
		text += "Hosted comparative evidence; dedicated-hardware qualification remains false.\n"
	}
	_, writeErr := io.WriteString(file, text)
	return errors.Join(writeErr, file.Close())
}
