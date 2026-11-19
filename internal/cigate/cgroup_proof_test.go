package cigate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCgroupMemoryProofGate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		workflow, job, condition string
	}{
		{"ci.yml", handoffLintJob, "steps.changes.outputs.run-code-checks == 'true'"},
		{"release.yml", "gate", ""},
	} {
		t.Run(tc.workflow, func(t *testing.T) {
			t.Parallel()
			job, ok := readWorkflow(t, tc.workflow).Jobs[tc.job]
			require.True(t, ok)
			assert.Empty(t, job.If)
			var proofSteps []workflowStep
			for _, step := range job.Steps {
				if step.ID == "cgroup-memory-proof" {
					proofSteps = append(proofSteps, step)
				}
			}
			require.Len(t, proofSteps, 1, "the cgroup memory proof must run in this gate")
			step := proofSteps[0]
			assert.Equal(t, "task verify-cgroup-memory", step.Run)
			assert.Equal(t, tc.condition, step.If)
			assert.Nil(t, step.ContinueOnError, "proof failure must fail the job")
			assert.Positive(t, step.TimeoutMinutes)
		})
	}
}
