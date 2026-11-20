// Package cigate checks the explicit GitHub Actions prerequisite contract.
package cigate

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const (
	Success       = "success"
	Skipped       = "skipped"
	MaxNeedsBytes = 64 << 10
)

// Job is the result GitHub supplies through the needs context.
type Job struct {
	Result  string            `json:"result"`
	Outputs map[string]string `json:"outputs"`
}

// RequiredJobs must match the final CI job's explicit needs list. Keeping every
// expected job here prevents a removed dependency from silently approving a PR.
func RequiredJobs() []string {
	return []string{"lint", "gitleaks", "vulncheck", "test", "integration", "dx", "build",
		"benchmark-smoke", "kernel", "codeql", "installer", "mounts", "qemu", "homebrew", "runtime"}
}

// PushBaselineJobs lists the jobs that run on push to main to maintain default-branch
// lint, Codecov, and CodeQL baselines without duplicating the full PR qualification matrix.
func PushBaselineJobs() []string {
	return []string{"lint", "test", "codeql"}
}

func expectedResult(name, event, code string) string {
	if event == "push" {
		if slices.Contains(PushBaselineJobs(), name) {
			return Success
		}
		return Skipped
	}
	if code == "false" && name != "lint" && name != "gitleaks" {
		return Skipped
	}
	return Success
}

// Evaluate accepts only a complete, successful code pipeline, the documented
// docs-only PR skip set, or the default-branch push baseline set. A failed or
// cancelled matrix is represented by its job result.
func Evaluate(data []byte, event string) error {
	if !slices.Contains([]string{"pull_request", "push", "workflow_dispatch"}, event) {
		return fmt.Errorf("unsupported CI event %q", event)
	}
	if len(data) > MaxNeedsBytes {
		return errors.New("CI prerequisite document exceeds its size bound")
	}
	var jobs map[string]Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return fmt.Errorf("decoding CI prerequisites: %w", err)
	}
	required := RequiredJobs()
	if len(jobs) != len(required) {
		return errors.New("CI prerequisite set is incomplete or contains unknown jobs")
	}
	for name := range jobs {
		if !slices.Contains(required, name) {
			return fmt.Errorf("unknown CI prerequisite %s", name)
		}
	}
	code := jobs["lint"].Outputs["run-code-checks"]
	if code != "true" && code != "false" {
		return errors.New("missing or invalid CI change classification")
	}
	if code == "false" && event != "pull_request" {
		return errors.New("only classified pull requests may skip code validation")
	}
	var failures []error
	for _, name := range required {
		want := expectedResult(name, event, code)
		if jobs[name].Result != want {
			failures = append(failures, fmt.Errorf("CI prerequisite %s: got %q, require %q", name, jobs[name].Result, want))
		}
	}
	return errors.Join(failures...)
}
