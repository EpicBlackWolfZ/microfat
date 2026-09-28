package cigate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMountQualificationCannotSilentlySkip(t *testing.T) {
	t.Parallel()
	job := readWorkflow(t, "ci.yml").Jobs["mounts"]
	assert.ElementsMatch(t, []string{"amd64", "arm64"}, job.Strategy.Matrix.Arch)
	require.NotNil(t, job.Strategy.FailFast)
	assert.False(t, *job.Strategy.FailFast)
	assert.Equal(t, "needs.lint.outputs.run-code-checks == 'true'", job.If)
	qualification, evidence := false, false
	for _, step := range job.Steps {
		if strings.Contains(step.Run, "task qualify-mounts") {
			qualification = true
			assert.Contains(t, step.Run, "MOUNT_TESTS=required")
			assert.Contains(t, step.Run, "MOUNT_BACKEND=sudo")
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
	assert.True(t, evidence)
}
