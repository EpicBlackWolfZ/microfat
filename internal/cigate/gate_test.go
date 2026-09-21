package cigate

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func pipeline(t *testing.T, docs bool) map[string]Job {
	t.Helper()
	jobs := map[string]Job{}
	for _, name := range RequiredJobs() {
		result := Success
		if docs && name != "pr-lint" && name != "lint" && name != "gitleaks" {
			result = Skipped
		}
		jobs[name] = Job{Result: result}
	}
	code := "true"
	if docs {
		code = "false"
	}
	jobs["lint"] = Job{Result: Success, Outputs: map[string]string{"run-code-checks": code}}
	return jobs
}

func encode(t *testing.T, jobs map[string]Job) []byte {
	t.Helper()
	data, err := json.Marshal(jobs)
	require.NoError(t, err)
	return data
}

func TestAcceptedPipelines(t *testing.T) {
	t.Parallel()
	for _, event := range []string{"pull_request", "push", "workflow_dispatch"} {
		t.Run(event, func(t *testing.T) { t.Parallel(); require.NoError(t, Evaluate(encode(t, pipeline(t, false)), event)) })
	}
	require.NoError(t, Evaluate(encode(t, pipeline(t, true)), "pull_request"))
}

func TestEveryPrerequisiteFailsClosed(t *testing.T) {
	t.Parallel()
	for _, name := range RequiredJobs() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, result := range []string{"failure", "cancelled", Skipped, "", "unknown"} {
				t.Run(result, func(t *testing.T) {
					t.Parallel()
					jobs := pipeline(t, false)
					job := jobs[name]
					job.Result = result
					jobs[name] = job
					require.Error(t, Evaluate(encode(t, jobs), "pull_request"))
				})
			}
		})
	}
}

func TestDocsCannotHideFailure(t *testing.T) {
	t.Parallel()
	for _, name := range RequiredJobs() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			jobs := pipeline(t, true)
			job := jobs[name]
			job.Result = "failure"
			jobs[name] = job
			require.Error(t, Evaluate(encode(t, jobs), "pull_request"))
		})
	}
	for _, event := range []string{"push", "workflow_dispatch", "unknown"} {
		require.Error(t, Evaluate(encode(t, pipeline(t, true)), event))
	}
}

func TestMissingAndUnexpectedPrerequisites(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(map[string]Job){
		"missing":                func(j map[string]Job) { delete(j, "kernel") },
		"extra":                  func(j map[string]Job) { j["extra"] = Job{Result: Success} },
		"renamed":                func(j map[string]Job) { delete(j, "kernel"); j["renamed"] = Job{Result: Success} },
		"missing_classification": func(j map[string]Job) { j["lint"] = Job{Result: Success} },
		"invalid_classification": func(j map[string]Job) {
			j["lint"] = Job{Result: Success, Outputs: map[string]string{"run-code-checks": "yes"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			jobs := pipeline(t, false)
			mutate(jobs)
			require.Error(t, Evaluate(encode(t, jobs), "pull_request"))
		})
	}
	for _, data := range [][]byte{nil, []byte("{}"), []byte("null"), []byte("[]"), make([]byte, MaxNeedsBytes+1)} {
		require.Error(t, Evaluate(data, "pull_request"))
	}
}
