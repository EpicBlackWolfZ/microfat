package cigate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestWorkflowsReadGoVersionFromCheckedOutModule(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob("../../.github/workflows/*.yml")
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	for _, path := range paths {
		w := readWorkflow(t, filepath.Base(path))
		for name, job := range w.Jobs {
			modulePath := ""
			for _, step := range job.Steps {
				if strings.HasPrefix(step.Uses, "actions/checkout@") {
					root, _ := step.With["path"].(string)
					if modulePath == "" || root == "" {
						modulePath = filepath.Join(root, "go.mod")
					}
				}
				if strings.HasPrefix(step.Uses, "actions/setup-go@") {
					require.Equal(t, modulePath, fmt.Sprint(step.With["go-version-file"]), "%s/%s", path, name)
					require.NotContains(t, step.With, "go-version", "%s/%s", path, name)
				}
			}
		}
	}
}

const (
	permissionRead        = "read"
	nonPushCodeCondition  = "github.event_name != 'push' && needs.lint.outputs.run-code-checks == 'true'"
	prTitleTimeoutMinutes = 5
	ciTestCondition       = "!cancelled() && needs.lint.result == 'success' && " +
		"needs.lint.outputs.run-code-checks == 'true' && " +
		"((github.event_name == 'push' && needs.gitleaks.result == 'skipped' && needs.vulncheck.result == 'skipped') || " +
		"(github.event_name != 'push' && needs.gitleaks.result == 'success' && needs.vulncheck.result == 'success'))"
	// Explicit !cancelled() is required so GitHub Actions' implicit success() does not
	// transitively skip codeql on push when upstream ancestors gitleaks/vulncheck are skipped.
	ciCodeQLCondition = "!cancelled() && needs.lint.result == 'success' && " +
		"needs.test.result == 'success' && needs.lint.outputs.run-code-checks == 'true'"
)

type pullRequestTrigger struct {
	Branches []string `yaml:"branches"`
	Types    []string `yaml:"types"`
}

func decodePullRequestTrigger(t *testing.T, w workflow) pullRequestTrigger {
	t.Helper()
	node, ok := w.On[eventPullRequest]
	require.True(t, ok, "expected pull_request trigger")
	var trigger pullRequestTrigger
	require.NoError(t, node.Decode(&trigger))
	return trigger
}

type workflow struct {
	On          map[string]yaml.Node   `yaml:"on"`
	Permissions map[string]string      `yaml:"permissions"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
	Concurrency struct {
		Group            string `yaml:"group"`
		CancelInProgress any    `yaml:"cancel-in-progress"`
	} `yaml:"concurrency"`
}

type workflowJob struct {
	Permissions map[string]string `yaml:"permissions"`
	Name        string            `yaml:"name"`
	Needs       yaml.Node         `yaml:"needs"`
	If          string            `yaml:"if"`
	Uses        string            `yaml:"uses"`
	Timeout     int               `yaml:"timeout-minutes"`
	Env         map[string]string `yaml:"env"`
	Steps       []workflowStep    `yaml:"steps"`
	Strategy    struct {
		FailFast *bool `yaml:"fail-fast"`
		Matrix   struct {
			Include []map[string]string `yaml:"include"`
			Arch    []string            `yaml:"arch"`
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
	assert.NotContains(t, w.Jobs, "pr-lint", "PR title validation belongs in standalone pr-title.yml")
	assert.Empty(t, w.Jobs["lint"].If)
	assert.Equal(t, "github.event_name != 'push'", w.Jobs["gitleaks"].If)
	assert.Equal(t, nonPushCodeCondition, w.Jobs["vulncheck"].If)
	assert.ElementsMatch(t, []string{"lint", "gitleaks", "vulncheck"}, needs(t, w.Jobs["test"]))
	assert.Equal(t, ciTestCondition, w.Jobs["test"].If)
	assert.Equal(t, ciCodeQLCondition, w.Jobs["codeql"].If)
	for _, name := range PushBaselineJobs() {
		job := w.Jobs[name]
		if len(needs(t, job)) > 0 {
			assert.Contains(t, job.If, "!cancelled()",
				"push baseline job %s with upstream needs must override implicit success() transitive skip", name)
		}
	}
	assert.ElementsMatch(t, []string{"lint", "test", "build"}, needs(t, w.Jobs["homebrew"]))
	assert.Equal(t, nonPushCodeCondition, w.Jobs["homebrew"].If)
	assert.Equal(t, nonPushCodeCondition+" && inputs.failure_probe != 'skip'", w.Jobs["integration"].If)
	for _, name := range []string{
		"integration", "dx", "build", "benchmark-smoke", "kernel", "codeql", "installer", "mounts", "qemu", "runtime",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.ElementsMatch(t, []string{"lint", "test"}, needs(t, w.Jobs[name]))
			if name != "codeql" && name != "integration" {
				assert.Equal(t, nonPushCodeCondition, w.Jobs[name].If)
			}
		})
	}
}

func TestOnlySharedCIAndTitleWorkflowHandlePullRequests(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	require.NoError(t, err)
	expectedBranches := []string{"main", "fix/**", "feat/**"}
	for _, path := range files {
		base := filepath.Base(path)
		t.Run(base, func(t *testing.T) {
			t.Parallel()
			w := readWorkflow(t, base)
			_, pr := w.On[eventPullRequest]
			wantPR := base == "ci.yml" || base == "pr-title.yml"
			assert.Equal(t, wantPR, pr, "only shared CI and standalone PR title validation may handle pull_request")
			assert.NotContains(t, w.On, "pull_request_target", "untrusted PR code must not receive privileged target events")
		})
	}

	ci := readWorkflow(t, "ci.yml")
	ciPR := decodePullRequestTrigger(t, ci)
	assert.Equal(t, expectedBranches, ciPR.Branches)
	assert.ElementsMatch(t, []string{"opened", "synchronize", "reopened"}, ciPR.Types)
	assert.NotContains(t, ciPR.Types, "edited", "PR metadata edits must not restart full CI")

	title := readWorkflow(t, "pr-title.yml")
	assert.Len(t, title.On, 1, "pr-title.yml must only trigger on pull_request")
	titlePR := decodePullRequestTrigger(t, title)
	assert.Equal(t, expectedBranches, titlePR.Branches)
	assert.ElementsMatch(t, []string{"opened", "edited", "reopened", "synchronize"}, titlePR.Types)
	assert.Equal(t, map[string]string{"pull-requests": permissionRead}, title.Permissions)
	assert.Equal(t, "${{ github.workflow }}-${{ github.ref }}", title.Concurrency.Group)
	assert.Equal(t, true, title.Concurrency.CancelInProgress)
	require.Len(t, title.Jobs, 1)
	job, ok := title.Jobs["pr-lint"]
	require.True(t, ok)
	assert.Equal(t, "Validate Conventional PR Title", job.Name)
	assert.Empty(t, job.If)
	assert.Equal(t, prTitleTimeoutMinutes, job.Timeout)
	require.Len(t, job.Steps, 1)
	assert.True(t, strings.HasPrefix(job.Steps[0].Uses, "amannn/action-semantic-pull-request@"))
	assert.Empty(t, job.Steps[0].If)
	assert.Equal(t, false, job.Steps[0].With["requireScope"])
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
	for _, name := range []string{"ci.yml", "pr-title.yml", "benchmark-smoke.yml", "benchmark-kernel.yml", "codeql.yml"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := readWorkflow(t, name)
			if name == "pr-title.yml" {
				assert.Equal(t, map[string]string{"pull-requests": permissionRead}, w.Permissions)
			} else {
				assert.Equal(t, permissionRead, w.Permissions["contents"])
			}
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
		if step.Uses == "./.github/actions/toolchain-config" {
			require.FileExists(t, "../../.github/actions/toolchain-config/action.yml")
			continue // Local code is bound to the checkout commit.
		}
		assert.Regexp(t, pin, step.Uses, "actions must use full commit pins")
		if strings.HasPrefix(step.Uses, "actions/checkout@") {
			assert.Equal(t, false, step.With["persist-credentials"], "untrusted subprocesses must not inherit checkout credentials")
		}
	}
}

func TestSharedToolchainAction(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../.github/actions/toolchain-config/action.yml")
	require.NoError(t, err)
	var action struct {
		Outputs map[string]struct{ Value string }
		Runs    struct {
			Using string
			Steps []workflowStep
		}
	}
	require.NoError(t, yaml.Unmarshal(data, &action))
	require.Equal(t, "composite", action.Runs.Using)
	require.Len(t, action.Runs.Steps, 1)
	step := action.Runs.Steps[0]
	require.Equal(t, "pins", step.ID)
	require.Empty(t, step.Uses)
	require.Contains(t, step.Run, "source .config/tool-versions.env")
	require.Equal(t, "${{ steps.pins.outputs.codecov }}", action.Outputs["codecov"].Value)
	require.Contains(t, step.Run, `echo "codecov=$CODECOV_VERSION"`)
}
