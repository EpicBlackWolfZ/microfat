package cigate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type requiredStepGate struct {
	stepID      string
	commandPart string
}

func assertGateStepsEnforced(t *testing.T, wfName, jobName string, gates []requiredStepGate) {
	t.Helper()
	job, ok := readWorkflow(t, wfName).Jobs[jobName]
	require.True(t, ok, "expected job %q in %s", jobName, wfName)
	assert.Positive(t, job.Timeout, "%s/%s must set timeout-minutes", wfName, jobName)

	for _, gate := range gates {
		var matched []workflowStep
		for _, step := range job.Steps {
			if step.ID == gate.stepID {
				matched = append(matched, step)
			}
		}
		require.Len(t, matched, 1, "%s/%s must define unique step ID %q", wfName, jobName, gate.stepID)
		step := matched[0]
		assert.Contains(t, step.Run, gate.commandPart, "%s/%s step %s", wfName, jobName, gate.stepID)
		assert.Empty(t, step.If, "%s/%s step %s must not skip conditionally", wfName, jobName, gate.stepID)
		assert.Nil(t, step.ContinueOnError, "%s/%s step %s failure must fail job", wfName, jobName, gate.stepID)
		assert.Positive(t, step.TimeoutMinutes, "%s/%s step %s must set timeout-minutes", wfName, jobName, gate.stepID)
	}
}

func TestFormalAndRustQualityGatesEnforced(t *testing.T) {
	t.Parallel()

	formalGates := []requiredStepGate{
		{stepID: "z3-proof", commandPart: "task verify-z3"},
		{stepID: "lean-proof", commandPart: "task verify-lean"},
		{stepID: "kani-proof", commandPart: "task verify-kani"},
		{stepID: "verus-proof", commandPart: "task verify-verus"},
	}
	rustGates := []requiredStepGate{
		{stepID: "rust-lint", commandPart: "task lint-rust"},
		{stepID: "rust-vuln", commandPart: "task vuln-rust"},
		{stepID: "rust-test", commandPart: "task test-rust"},
		{stepID: "rust-coverage", commandPart: "task coverage-rust RUST_COVERAGE_THRESHOLD=95"},
		{stepID: "rust-cross-build", commandPart: "aarch64-unknown-linux-musl"},
		{stepID: "rust-e2e", commandPart: "task test-rust-stub-e2e"},
	}

	for _, wf := range []string{docGateWorkflowCI, "release.yml"} {
		t.Run(wf, func(t *testing.T) {
			t.Parallel()
			assertGateStepsEnforced(t, wf, "formal", formalGates)
			assertGateStepsEnforced(t, wf, "rust", rustGates)
		})
	}

	release := readWorkflow(t, "release.yml")
	assert.Empty(t, release.Jobs["formal"].If)
	assert.Empty(t, release.Jobs["rust"].If)
	goreleaserNeeds := needs(t, release.Jobs["goreleaser"])
	assert.ElementsMatch(t, []string{"gate", "formal", "rust"}, goreleaserNeeds)

	formalLab := readWorkflow(t, "formal-lab.yml")
	_, hasDispatch := formalLab.On["workflow_dispatch"]
	assert.True(t, hasDispatch)
	_, hasPush := formalLab.On["push"]
	assert.False(t, hasPush, "formal-lab.yml must not duplicate push runs now that ci.yml gates PRs")
	assert.True(t, strings.Contains(formalLab.Jobs["rust-stub"].Steps[len(formalLab.Jobs["rust-stub"].Steps)-1].Run, "coverage-rust"))
}
