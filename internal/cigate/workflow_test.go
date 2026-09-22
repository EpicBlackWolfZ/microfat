package cigate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type workflow struct {
	On          map[string]yaml.Node   `yaml:"on"`
	Permissions map[string]string      `yaml:"permissions"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
	Concurrency struct {
		Group string `yaml:"group"`
	} `yaml:"concurrency"`
}

type workflowJob struct {
	Name     string            `yaml:"name"`
	Needs    yaml.Node         `yaml:"needs"`
	If       string            `yaml:"if"`
	Uses     string            `yaml:"uses"`
	Timeout  int               `yaml:"timeout-minutes"`
	Env      map[string]string `yaml:"env"`
	Steps    []workflowStep    `yaml:"steps"`
	Strategy struct {
		FailFast *bool `yaml:"fail-fast"`
		Matrix   struct {
			Include []map[string]string `yaml:"include"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
}

type workflowStep struct {
	ID              string            `yaml:"id"`
	Name            string            `yaml:"name"`
	If              string            `yaml:"if"`
	Uses            string            `yaml:"uses"`
	Run             string            `yaml:"run"`
	With            map[string]any    `yaml:"with"`
	TimeoutMinutes  int               `yaml:"timeout-minutes"`
	ContinueOnError *bool             `yaml:"continue-on-error"`
	Env             map[string]string `yaml:"env"`
}

func readWorkflow(t *testing.T, name string) workflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	require.NoError(t, err)
	var result workflow
	require.NoError(t, yaml.Unmarshal(data, &result))
	return result
}

func needs(t *testing.T, job workflowJob) []string {
	t.Helper()
	if job.Needs.Kind == 0 {
		return nil
	}
	if job.Needs.Kind == yaml.ScalarNode {
		return []string{job.Needs.Value}
	}
	var result []string
	require.NoError(t, job.Needs.Decode(&result))
	return result
}

func TestWorkflowDependencyContract(t *testing.T) {
	t.Parallel()
	w := readWorkflow(t, "ci.yml")
	complete := w.Jobs["complete"]
	assert.Equal(t, "CI Complete", complete.Name)
	assert.Equal(t, "always()", complete.If)
	assert.ElementsMatch(t, RequiredJobs(), needs(t, complete))
	var jobs []string
	for name := range w.Jobs {
		if name != "complete" {
			jobs = append(jobs, name)
		}
	}
	assert.ElementsMatch(t, RequiredJobs(), jobs, "every job must be accounted for by the aggregate")
	assert.ElementsMatch(t, []string{"pr-lint", "lint", "gitleaks", "vulncheck"}, needs(t, w.Jobs["test"]))
	assert.Empty(t, w.Jobs["pr-lint"].If, "title job must succeed explicitly for non-PR events")
	for _, name := range []string{"integration", "dx", "build", "benchmark-smoke", "kernel", "codeql", "archive-contracts"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.ElementsMatch(t, []string{"lint", "test"}, needs(t, w.Jobs[name]))
		})
	}
}

func TestOnlySharedCIHandlesPullRequests(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	require.NoError(t, err)
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			w := readWorkflow(t, filepath.Base(path))
			_, pr := w.On["pull_request"]
			assert.Equal(t, filepath.Base(path) == "ci.yml", pr, "expensive PR work must share the CI gates")
			assert.NotContains(t, w.On, "pull_request_target", "untrusted PR code must not receive privileged target events")
		})
	}
}

func TestReusableStagesCannotSilentlySkip(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, job string
		matrix    int
	}{
		{"benchmark-smoke.yml", "smoke", 0}, {"benchmark-kernel.yml", "kernel", 3}, {"codeql.yml", "analyze", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			w := readWorkflow(t, test.name)
			assert.Contains(t, w.On, "workflow_call")
			require.Len(t, w.Jobs, 1)
			job, ok := w.Jobs[test.job]
			require.True(t, ok)
			assert.Empty(t, job.If, "only the caller may classify whether a stage is required")
			assert.Empty(t, needs(t, job))
			if test.matrix > 0 {
				require.NotNil(t, job.Strategy.FailFast)
				assert.False(t, *job.Strategy.FailFast)
				assert.Len(t, job.Strategy.Matrix.Include, test.matrix)
			}
			assert.NotEqual(t, "${{ github.workflow }}-${{ github.ref }}", w.Concurrency.Group, "callee must not cancel CI")
		})
	}
}

func TestCIExecutionBoundaries(t *testing.T) {
	t.Parallel()
	pin := regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)
	for _, name := range []string{"ci.yml", "benchmark-smoke.yml", "benchmark-kernel.yml", "codeql.yml"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := readWorkflow(t, name)
			assert.Equal(t, "read", w.Permissions["contents"])
			for _, job := range w.Jobs {
				checkJobBoundaries(t, job, pin)
			}
		})
	}
}

func checkJobBoundaries(t *testing.T, job workflowJob, pin *regexp.Regexp) {
	t.Helper()
	if job.Uses != "" {
		assert.Contains(t, []string{"./.github/workflows/benchmark-smoke.yml",
			"./.github/workflows/benchmark-kernel.yml", "./.github/workflows/codeql.yml"}, job.Uses)
		return
	} // Reusable jobs are checked at their own execution boundary.
	assert.Positive(t, job.Timeout, "every executing job needs an explicit timeout")
	for _, step := range job.Steps {
		if step.Uses == "" {
			continue
		}
		assert.Regexp(t, pin, step.Uses, "actions must use full commit pins")
		if strings.HasPrefix(step.Uses, "actions/checkout@") {
			assert.Equal(t, false, step.With["persist-credentials"], "untrusted subprocesses must not inherit checkout credentials")
		}
	}
}
