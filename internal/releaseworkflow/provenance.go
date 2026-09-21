// Package releaseworkflow enforces source, measurement and publication provenance.
package releaseworkflow

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

const (
	BenchmarkWorkflow = ".github/workflows/benchmarks.yml"
	ReleaseWorkflow   = ".github/workflows/release.yml"
	VerificationJob   = "Independently verify hosted release matrix"
	success           = "success"
	completed         = "completed"
)

var positiveID = regexp.MustCompile(`^[1-9][0-9]*$`)
var sourceSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
var releaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)
var repositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type WorkflowRun struct {
	ID         int64  `json:"id"`
	Attempt    int64  `json:"run_attempt"`
	HeadSHA    string `json:"head_sha"`
	HeadBranch string `json:"head_branch"`
	Path       string `json:"path"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type Job struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

func MeasurementJobs() []string {
	var names []string
	for _, arch := range []string{"amd64", "arm64"} {
		for _, workload := range []string{"mixed", "cpu", "memory"} {
			for _, intensity := range []string{"standard", "heavy"} {
				names = append(names, fmt.Sprintf("Benchmark sustained (%s, %s, %s)", arch, workload, intensity))
			}
		}
	}
	for _, runner := range []string{"ubuntu-24.04", "ubuntu-24.04-arm"} {
		names = append(names, "Benchmark compatibility ("+runner+")")
	}
	return names
}

// SourceRevision permits recovery after verification failure, but every measurement
// and compatibility job must have succeeded in this exact completed run attempt.
func SourceRevision(run WorkflowRun, jobs []Job, runID, attempt string) (string, error) {
	if !positiveID.MatchString(runID) || !positiveID.MatchString(attempt) || strconv.FormatInt(run.ID, 10) != runID ||
		strconv.FormatInt(run.Attempt, 10) != attempt || run.Path != BenchmarkWorkflow || run.Status != completed ||
		!sourceSHA.MatchString(run.HeadSHA) {
		return "", errors.New("invalid completed measurement run provenance")
	}
	expected := make(map[string]bool)
	for _, name := range MeasurementJobs() {
		expected[name] = false
	}
	for _, job := range jobs {
		seen, wanted := expected[job.Name]
		if !wanted {
			continue
		}
		if seen || job.Conclusion != success {
			return "", errors.New("duplicate or unsuccessful measurement/compatibility job")
		}
		expected[job.Name] = true
	}
	for _, seen := range expected {
		if !seen {
			return "", errors.New("all 12 measurement and both compatibility jobs must pass in the requested attempt")
		}
	}
	return run.HeadSHA, nil
}

func ValidateBuild(runs []WorkflowRun, tag, source string) error {
	matched := 0
	for _, run := range runs {
		if run.HeadSHA != source || run.HeadBranch != tag || run.Event != "push" || run.Path != ReleaseWorkflow {
			continue
		}
		matched++
		if run.Status != completed || run.Conclusion != success {
			return errors.New("exact tag release workflow has not succeeded")
		}
	}
	if matched != 1 {
		return errors.New("expected exactly one completed successful tag release workflow")
	}
	return nil
}
