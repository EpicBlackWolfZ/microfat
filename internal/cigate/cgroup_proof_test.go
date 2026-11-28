package cigate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProductionGobraProofGates(t *testing.T) {
	t.Parallel()
	workflows := []struct {
		workflow, job, condition string
	}{
		{"ci.yml", handoffLintJob, "steps.changes.outputs.run-code-checks == 'true'"},
		{"release.yml", "gate", ""},
	}
	proofs := []struct {
		stepID, command string
	}{
		{"cgroup-memory-proof", "task verify-cgroup-memory"},
		{"format-bounds-proof", "task verify-format-bounds"},
		{"pack-layout-proof", "task verify-pack-layout"},
		{"cache-metadata-proof", "task verify-cache-metadata"},
		{"install-metadata-proof", "task verify-install-metadata"},
		{"microarch-policy-proof", "task verify-microarch-policy"},
		{"lifecycle-policy-proof", "task verify-lifecycle-policy"},
	}
	for _, tc := range workflows {
		t.Run(tc.workflow, func(t *testing.T) {
			t.Parallel()
			job, ok := readWorkflow(t, tc.workflow).Jobs[tc.job]
			require.True(t, ok)
			assert.Empty(t, job.If)
			for _, proof := range proofs {
				var proofSteps []workflowStep
				for _, step := range job.Steps {
					if step.ID == proof.stepID {
						proofSteps = append(proofSteps, step)
					}
				}
				require.Len(t, proofSteps, 1, "the %s proof must run in this gate", proof.stepID)
				step := proofSteps[0]
				assert.Equal(t, proof.command, step.Run)
				assert.Equal(t, tc.condition, step.If)
				assert.Nil(t, step.ContinueOnError, "proof failure must fail the job")
				assert.Positive(t, step.TimeoutMinutes)
			}
		})
	}
}
