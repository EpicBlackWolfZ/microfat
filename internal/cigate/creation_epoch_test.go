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
	creationEpochLog     = ".work/runtime-qualification/e2e-qualification.log"
	creationEpochMarker  = "creation epoch qualification ran:"
	creationEpochFakeGo  = `#!/bin/bash
set -euo pipefail
IFS=$'\n\t'
printf 'creation epoch qualification ran: %s\n' "$*"
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
		if step.Name == "Qualify native E2E fixtures, epochs, cache descriptors and telemetry" {
			require.Nil(t, qualification, "the native E2E qualification step must be unique")
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
	assert.Equal(t, "${{ matrix.arch }}", qualification.Env["MATRIX_ARCH"])
	assert.Contains(t, qualification.Run, "-race")
	assert.Contains(t, qualification.Run, "ReproducibleEpoch|CreationEpochNumericMetadata")
	assert.Contains(t, qualification.Run, "RuntimeFixture")
	assert.Contains(t, qualification.Run, "NativeARM64HighFeatureDetection$")
	assert.Contains(t, qualification.Run, "VerifiedCacheDescriptorCompatibility$")
	assert.Contains(t, qualification.Run, "Telemetry")
	assert.Contains(t, qualification.Run, creationEpochLog)
	assert.Greater(t, evidencePosition, qualificationPosition, "retain epoch logs after successful or failed qualification")

	for _, tc := range []struct {
		name string
		arch string
		exit int
	}{
		{"failed test amd64", handoffAMD64, creationEpochFailure},
		{"successful test amd64", handoffAMD64, 0},
		{"successful test arm64", handoffARM64, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tools := filepath.Join(dir, "tools")
			require.NoError(t, os.Mkdir(tools, 0o755))
			writeExecutableTestStub(t, filepath.Join(tools, "go"), creationEpochFakeGo)
			script := filepath.Join(dir, "qualification.sh")
			require.NoError(t, os.WriteFile(script, []byte(qualification.Run), 0o600))

			// GitHub's unspecified Linux shell runs bash -e without enabling pipefail.
			// Execute the real workflow body so a successful tee cannot hide a failing go test.
			cmd := exec.CommandContext(t.Context(), "bash", "-e", script)
			cmd.Dir = dir
			cmd.Env = []string{
				"PATH=" + tools + string(os.PathListSeparator) + os.Getenv("PATH"),
				"MATRIX_ARCH=" + tc.arch,
				"CREATION_EPOCH_TEST_EXIT=" + strconv.Itoa(tc.exit),
			}
			output, err := combinedOutputSafe(cmd)
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
			assert.Contains(t, string(log), creationEpochMarker, "tee must retain evidence even when go test fails")
			if tc.arch == handoffARM64 {
				assert.Contains(t, string(log), "NativeARM64HighFeatureDetection$")
			} else {
				assert.NotContains(t, string(log), "NativeARM64HighFeatureDetection$")
			}
		})
	}
}
