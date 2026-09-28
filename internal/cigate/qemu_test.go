package cigate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQemuQualificationCannotSilentlySkip(t *testing.T) {
	t.Parallel()
	job := readWorkflow(t, "ci.yml").Jobs["qemu"]
	assert.Equal(t, 30, job.Timeout)
	assert.Equal(t, "needs.lint.outputs.run-code-checks == 'true'", job.If)
	qualification, acquisition, evidence := false, false, false
	for _, step := range job.Steps {
		if strings.Contains(step.Run, "task qualify-qemu") {
			qualification = true
			assert.Contains(t, step.Run, "QEMU_TESTS=required")
			assert.Contains(t, step.Run, "QEMU_BACKEND=sudo")
			assert.Contains(t, step.Run, "QEMU_AARCH64=")
			assert.Empty(t, step.If)
			assert.Nil(t, step.ContinueOnError)
		}
		if strings.Contains(step.Run, "scripts/acquire-qemu.sh") {
			acquisition = true
			assert.Empty(t, step.If)
			assert.Nil(t, step.ContinueOnError)
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			evidence = true
			assert.Equal(t, "always()", step.If)
			assert.Equal(t, true, step.With["include-hidden-files"])
			assert.Equal(t, "error", step.With["if-no-files-found"])
		}
	}
	assert.True(t, qualification)
	assert.True(t, acquisition)
	assert.True(t, evidence)
}
