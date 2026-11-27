package cigate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const docGateWorkflowCI = "ci.yml"

func TestDocumentationGate(t *testing.T) {
	t.Parallel()
	for _, workflowFile := range []struct {
		workflow, job string
	}{
		{docGateWorkflowCI, handoffLintJob},
		{"release.yml", "gate"},
	} {
		t.Run(workflowFile.workflow, func(t *testing.T) {
			t.Parallel()
			job, ok := readWorkflow(t, workflowFile.workflow).Jobs[workflowFile.job]
			require.True(t, ok)

			var docsSteps []workflowStep
			for _, step := range job.Steps {
				if step.ID == "docs-gate" {
					docsSteps = append(docsSteps, step)
				}
			}
			require.Len(t, docsSteps, 1, "docs-gate must run in %s/%s", workflowFile.workflow, workflowFile.job)
			step := docsSteps[0]
			assert.Equal(t, "task lint-docs", step.Run)
			assert.Empty(t, step.If, "docs-gate must run on both code and docs-only PRs")
			assert.Nil(t, step.ContinueOnError, "docs-gate failure must fail the job")
			assert.Positive(t, step.TimeoutMinutes)
		})
	}

	t.Run("ci_lint_sets_up_go_and_task_for_docs_only_prs", func(t *testing.T) {
		t.Parallel()
		job, ok := readWorkflow(t, docGateWorkflowCI).Jobs[handoffLintJob]
		require.True(t, ok)
		for _, stepName := range []string{"Set up Go", "Install pinned Task"} {
			var found bool
			for _, step := range job.Steps {
				if step.Name == stepName {
					found = true
					assert.Empty(t, step.If, "%s must remain enabled for docs-only PRs", stepName)
				}
			}
			assert.True(t, found, "expected step %q in ci.yml/lint", stepName)
		}
	})
}
