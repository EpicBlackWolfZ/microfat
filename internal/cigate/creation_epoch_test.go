//go:build linux

package cigate

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	creationEpochFailure = 73
	creationEpochLog     = ".work/runtime-qualification/creation-epochs.log"
	creationEpochMarker  = "creation epoch qualification ran\n"
	creationEpochFakeGo  = `#!/bin/bash
set -euo pipefail
IFS=$'\n\t'
printf 'creation epoch qualification ran\n'
exit "${CREATION_EPOCH_TEST_EXIT}"
`
)

func TestCreationEpochQualificationPropagatesFailuresAndRetainsEvidence(t *testing.T) {
	t.Parallel()
	job := readWorkflow(t, "ci.yml").Jobs["runtime"]
	assert.ElementsMatch(t, []string{"amd64", "arm64"}, job.Strategy.Matrix.Arch)
	var qualification *workflowStep
	qualificationPosition, evidencePosition := -1, -1
	for position, step := range job.Steps {
		if step.Name == "Qualify reproducible package epochs and native dispatch" {
			require.Nil(t, qualification, "the epoch qualification step must be unique")
			qualification = &step
			qualificationPosition = position
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			paths, ok := step.With["path"].(string)
			require.True(t, ok)
			if slices.Contains(strings.Fields(paths), ".work/runtime-qualification/") {
				evidencePosition = position
				assert.Equal(t, "always()", step.If)
				assert.Equal(t, true, step.With["include-hidden-files"])
				assert.Equal(t, "error", step.With["if-no-files-found"])
			}
		}
	}
	require.NotNil(t, qualification)
	assert.Empty(t, qualification.If, "epoch qualification must run whenever native runtime qualification runs")
	assert.Nil(t, qualification.ContinueOnError)
	assert.Contains(t, qualification.Run, "-race")
	assert.Contains(t, qualification.Run, "^Test(ReproducibleEpoch|CreationEpochNumericMetadata)")
	assert.Contains(t, qualification.Run, creationEpochLog)
	assert.Greater(t, evidencePosition, qualificationPosition, "retain epoch logs after successful or failed qualification")

	for _, tc := range []struct {
		name string
		exit int
	}{
		{"failed test", creationEpochFailure},
		{"successful test", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tools := filepath.Join(dir, "tools")
			require.NoError(t, os.Mkdir(tools, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(tools, "go"), []byte(creationEpochFakeGo), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, creationEpochLog)), 0o755))
			script := filepath.Join(dir, "qualification.sh")
			require.NoError(t, os.WriteFile(script, []byte(qualification.Run), 0o600))

			// GitHub's unspecified Linux shell runs bash -e without enabling pipefail.
			// Execute the real workflow body so a successful tee cannot hide a failing go test.
			cmd := exec.CommandContext(t.Context(), "bash", "-e", script)
			cmd.Dir = dir
			cmd.Env = []string{
				"PATH=" + tools + string(os.PathListSeparator) + os.Getenv("PATH"),
				"CREATION_EPOCH_TEST_EXIT=" + strconv.Itoa(tc.exit),
			}
			output, err := cmd.CombinedOutput()
			if tc.exit == 0 {
				require.NoError(t, err, "%s", output)
			} else {
				var exitError *exec.ExitError
				require.ErrorAs(t, err, &exitError, "%s", output)
			}
			assert.Equal(t, tc.exit, cmd.ProcessState.ExitCode(), "%s", output)
			assert.Contains(t, string(output), creationEpochMarker)
			log, err := os.ReadFile(filepath.Join(dir, creationEpochLog))
			require.NoError(t, err)
			assert.Equal(t, creationEpochMarker, string(log), "tee must retain evidence even when go test fails")
		})
	}
}
