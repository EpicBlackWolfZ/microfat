package releaseworkflow

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const failedConclusion = "failure"
const invalidValue = "bad"

const fixtureSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const fixtureTag = "v0.2.5"
const fixtureRepo = "owner/repo"

func completeJobs() []Job {
	names := []string{
		"Benchmark sustained (amd64, mixed, standard)", "Benchmark sustained (amd64, mixed, heavy)",
		"Benchmark sustained (amd64, cpu, standard)", "Benchmark sustained (amd64, cpu, heavy)",
		"Benchmark sustained (amd64, memory, standard)", "Benchmark sustained (amd64, memory, heavy)",
		"Benchmark sustained (arm64, mixed, standard)", "Benchmark sustained (arm64, mixed, heavy)",
		"Benchmark sustained (arm64, cpu, standard)", "Benchmark sustained (arm64, cpu, heavy)",
		"Benchmark sustained (arm64, memory, standard)", "Benchmark sustained (arm64, memory, heavy)",
		"Benchmark compatibility (ubuntu-24.04)", "Benchmark compatibility (ubuntu-24.04-arm)",
	}
	var jobs []Job
	for _, name := range names {
		jobs = append(jobs, Job{Name: name, Conclusion: success})
	}
	return jobs
}

func measurementRun() WorkflowRun {
	return WorkflowRun{ID: 1, Attempt: 2, HeadSHA: fixtureSHA, HeadBranch: fixtureTag, Path: BenchmarkWorkflow,
		Status: completed, Conclusion: failedConclusion}
}

func TestExactMeasurementCohort(t *testing.T) {
	t.Parallel()
	jobs := append(completeJobs(), Job{Name: VerificationJob, Conclusion: failedConclusion})
	source, err := SourceRevision(measurementRun(), jobs, "1", "2")
	require.NoError(t, err, "failed downstream verification may be recovered when all measurements passed")
	require.Equal(t, fixtureSHA, source)
	for name, change := range map[string]func(*WorkflowRun){
		"run": func(r *WorkflowRun) { r.ID = 2 }, "attempt": func(r *WorkflowRun) { r.Attempt = 1 },
		"source": func(r *WorkflowRun) { r.HeadSHA = invalidValue }, "workflow": func(r *WorkflowRun) { r.Path = "other.yml" },
		"running": func(r *WorkflowRun) { r.Status = "in_progress" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run := measurementRun()
			change(&run)
			_, err := SourceRevision(run, jobs, "1", "2")
			require.Error(t, err)
		})
	}
	for _, ids := range [][2]string{{"0", "2"}, {"01", "2"}, {"1", "0"}, {"1", "02"}, {invalidValue, "2"}} {
		_, err := SourceRevision(measurementRun(), jobs, ids[0], ids[1])
		require.Error(t, err)
	}
	for index := range completeJobs() {
		changed := completeJobs()
		changed[index].Conclusion = failedConclusion
		_, err := SourceRevision(measurementRun(), changed, "1", "2")
		require.Error(t, err)
	}
	for _, changed := range [][]Job{nil, completeJobs()[1:], append(completeJobs(), completeJobs()[0])} {
		_, err := SourceRevision(measurementRun(), changed, "1", "2")
		require.Error(t, err)
	}
}

func successfulBuild() WorkflowRun {
	return WorkflowRun{HeadSHA: fixtureSHA, HeadBranch: fixtureTag, Event: "push", Path: ReleaseWorkflow,
		Status: completed, Conclusion: success}
}

func TestExactTagBuild(t *testing.T) {
	t.Parallel()
	run := successfulBuild()
	require.NoError(t, ValidateBuild([]WorkflowRun{{HeadSHA: "unrelated"}, run}, fixtureTag, fixtureSHA))
	for _, runs := range [][]WorkflowRun{nil, {run, run}} {
		require.Error(t, ValidateBuild(runs, fixtureTag, fixtureSHA))
	}
	for _, change := range []func(*WorkflowRun){func(r *WorkflowRun) { r.HeadSHA = "wrong" }, func(r *WorkflowRun) { r.HeadBranch = "wrong" },
		func(r *WorkflowRun) { r.Event = "pull_request" }, func(r *WorkflowRun) { r.Path = "other.yml" },
		func(r *WorkflowRun) { r.Status = "in_progress" }, func(r *WorkflowRun) { r.Conclusion = failedConclusion }} {
		changed := run
		change(&changed)
		require.Error(t, ValidateBuild([]WorkflowRun{changed}, fixtureTag, fixtureSHA))
	}
}

func jsonBytes(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}
func lookup(values map[string]string) Environment {
	return func(name string) string { return values[name] }
}
func recoveryEnvironment() map[string]string {
	return map[string]string{"GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_REF": "refs/heads/main"}
}

func recoveryCommand(t *testing.T, run WorkflowRun, jobs []Job, source string) Command {
	t.Helper()
	return func(args ...string) ([]byte, error) {
		require.Equal(t, "api", args[0])
		switch {
		case strings.Contains(args[1], "/jobs?"):
			return jsonBytes(t, map[string]any{"jobs": jobs}), nil
		case strings.Contains(args[1], "/commits/"):
			return jsonBytes(t, map[string]string{"sha": source}), nil
		default:
			return jsonBytes(t, run), nil
		}
	}
}

func TestRecovery(t *testing.T) {
	t.Parallel()
	run := measurementRun()
	jobs := append(completeJobs(), Job{Name: VerificationJob, Conclusion: success})
	env := lookup(recoveryEnvironment())
	tag, source, err := RecoverySource(fixtureRepo, "1", "2", env, recoveryCommand(t, run, jobs, fixtureSHA))
	require.NoError(t, err)
	require.Equal(t, fixtureTag, tag)
	require.Equal(t, fixtureSHA, source)
	for name, change := range map[string]func(*WorkflowRun, *[]Job, *string){
		"measurement":        func(_ *WorkflowRun, j *[]Job, _ *string) { (*j)[0].Conclusion = failedConclusion },
		"verification":       func(_ *WorkflowRun, j *[]Job, _ *string) { (*j)[len(*j)-1].Conclusion = failedConclusion },
		"missing verifier":   func(_ *WorkflowRun, j *[]Job, _ *string) { *j = (*j)[:len(*j)-1] },
		"duplicate verifier": func(_ *WorkflowRun, j *[]Job, _ *string) { *j = append(*j, (*j)[len(*j)-1]) },
		"source":             func(_ *WorkflowRun, _ *[]Job, s *string) { *s = "wrong" },
		"branch":             func(r *WorkflowRun, _ *[]Job, _ *string) { r.HeadBranch = "main" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run := measurementRun()
			jobs := append(completeJobs(), Job{Name: VerificationJob, Conclusion: success})
			source := fixtureSHA
			change(&run, &jobs, &source)
			_, _, err := RecoverySource(fixtureRepo, "1", "2", env, recoveryCommand(t, run, jobs, source))
			require.Error(t, err)
		})
	}
	for _, values := range []map[string]string{{"GITHUB_EVENT_NAME": "push", "GITHUB_REF": "refs/heads/main"},
		{"GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_REF": "refs/heads/other"}} {
		_, _, err := RecoverySource(fixtureRepo, "1", "2", lookup(values), nil)
		require.Error(t, err)
	}
	for _, args := range [][3]string{{invalidValue, "1", "2"}, {fixtureRepo, "0", "2"}, {fixtureRepo, "1", "0"}} {
		_, _, err := RecoverySource(args[0], args[1], args[2], env, nil)
		require.Error(t, err)
	}
	for _, failAt := range []int{1, 2, 3} {
		calls := 0
		good := recoveryCommand(t, run, jobs, fixtureSHA)
		_, _, err := RecoverySource(fixtureRepo, "1", "2", env, func(args ...string) ([]byte, error) {
			calls++
			if calls == failAt {
				return nil, errors.New("API unavailable")
			}
			return good(args...)
		})
		require.ErrorContains(t, err, "API unavailable")
	}
	var value any
	require.Error(t, commandJSON(func(...string) ([]byte, error) { return []byte("{"), nil }, &value, "api"))
}
