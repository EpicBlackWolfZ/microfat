package cigate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDelegationTeardownPrecedesEvidenceAndCannotSkipFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ file, job, cleanup, evidence, condition string }{
		{"benchmarks.yml", "sustained", "Drain owned delegation", "Preserve sustained evidence", "always()"},
		{"benchmark-kernel.yml", "kernel", "Drain native delegation", "Retain kernel evidence", "always() && matrix.mode != 'v1-tcg'"},
	} {
		t.Run(test.job, func(t *testing.T) {
			t.Parallel()
			job := readWorkflow(t, test.file).Jobs[test.job]
			cleanup, evidence := -1, -1
			for i, step := range job.Steps {
				if step.Name == test.cleanup {
					cleanup = i
					assert.Equal(t, test.condition, step.If)
					assert.Equal(t, "bash scripts/benchmark-cleanup.sh", step.Run)
					assert.Nil(t, step.ContinueOnError, "cleanup failures must stay visible")
				}
				if step.Name == test.evidence {
					evidence = i
					assert.Equal(t, "always()", step.If, "failed and cancelled measurements retain incomplete evidence")
				}
			}
			require.NotEqual(t, -1, cleanup)
			require.Greater(t, evidence, cleanup, "no surviving controller may continue writing during artifact upload")
		})
	}
}

func TestNativeDelegationProofIsMandatoryAndExternal(t *testing.T) {
	t.Parallel()
	job := readWorkflow(t, "benchmark-kernel.yml").Jobs["kernel"]
	var proof *workflowStep
	for _, step := range job.Steps {
		if step.Name == "Prove native orphan delegation cleanup" {
			proof = &step
			break
		}
	}
	require.NotNil(t, proof)
	assert.Equal(t, "matrix.mode != 'v1-tcg'", proof.If)
	assert.Nil(t, proof.ContinueOnError)
	assert.Contains(t, proof.Run, "set -o pipefail", "tee must not mask a failed native proof")
	assert.Contains(t, proof.Run, "MICROFAT_BENCH_CLEANUP_PROOF=required")
	assert.Contains(t, proof.Run, "-test.run '^TestNativeCleanup$'")
	assert.NotContains(t, proof.Run, "benchmark-enter", "the cleanup proof must stay outside the delegation it removes")
	var nativeRunners []string
	for _, item := range job.Strategy.Matrix.Include {
		if strings.HasPrefix(item["mode"], "v2-") {
			nativeRunners = append(nativeRunners, item["runner"])
		}
	}
	assert.ElementsMatch(t, []string{"ubuntu-24.04", "ubuntu-24.04-arm"}, nativeRunners)
}
