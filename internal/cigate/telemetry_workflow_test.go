//go:build linux

package cigate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	telemetryStudyCondition = "(github.head_ref || github.ref_name) == 'perf/disabled-telemetry' && " +
		"!(github.event_name == 'pull_request' && github.event.action == 'edited' && github.event.changes.base == null)"
	telemetryProbeLog = ".work/runtime-qualification/telemetry-probes.log"
	telemetryMarker   = "telemetry study ran"
	telemetryHead     = "candidate-head"
)

func telemetryStudyStep(t *testing.T) workflowStep {
	t.Helper()
	var study *workflowStep
	for _, step := range readWorkflow(t, "ci.yml").Jobs["runtime"].Steps {
		if strings.Contains(step.Run, "task qualify-telemetry") {
			require.Nil(t, study)
			study = &step
		}
	}
	require.NotNil(t, study)
	return *study
}

func TestTelemetryStudyFreezesSourcesAndRetainsFailures(t *testing.T) {
	t.Parallel()
	study := telemetryStudyStep(t)
	assert.Equal(t, telemetryStudyCondition, study.If)
	assert.Equal(t, handoffExpectedSource, study.Env["EXPECTED_SOURCE"])
	assert.Nil(t, study.ContinueOnError)
	var baseline, matrix, evidence bool
	for _, step := range readWorkflow(t, "ci.yml").Jobs["runtime"].Steps {
		if step.With["path"] == ".work/telemetry-base" {
			baseline = true
			assert.Equal(t, "6eb862b914e8b3dfdf820dcbd1bfa079e4275dd6", step.With["ref"])
			assert.Equal(t, telemetryStudyCondition, step.If)
		}
		if strings.Contains(step.Run, "^TestTelemetry") {
			matrix = true
			assert.Empty(t, step.If, "compatibility remains required after the delivery branch is merged")
			assert.Contains(t, step.Run, "set -o pipefail")
			assert.Nil(t, step.ContinueOnError)
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			paths, ok := step.With["path"].(string)
			require.True(t, ok)
			if strings.Contains(paths, ".work/telemetry-confirmation/") {
				evidence = true
				assert.Equal(t, "always()", step.If)
			}
		}
	}
	assert.True(t, baseline)
	assert.True(t, matrix)
	assert.True(t, evidence)
}

func TestTelemetryStudyPropagatesProbeAndStudyFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, arch, source  string
		probeExit, taskExit int
		wantExit            int
		invoked             bool
	}{
		{"success", handoffAMD64, telemetryHead, 0, 0, 0, true},
		{"probe failure", handoffAMD64, telemetryHead, 73, 0, 73, false},
		{"study failure", handoffAMD64, telemetryHead, 0, 74, 74, true},
		{"wrong architecture", handoffARM64, telemetryHead, 0, 0, 1, false},
		{"wrong source", handoffAMD64, "different-head", 0, 0, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, body := range map[string]string{
				"go": "#!/bin/bash\nif [[ $1 == env ]]; then printf '%s\\n' \"$PROBE_ARCH\"; " +
					"else printf 'telemetry probe ran\\n'; exit \"$PROBE_EXIT\"; fi\n",
				"git":  "#!/bin/bash\nprintf '%s\\n' \"$PROBE_SOURCE\"\n",
				"task": "#!/bin/bash\nprintf 'telemetry study ran\\n'\nexit \"$STUDY_EXIT\"\n",
			} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755))
			}
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, telemetryProbeLog)), 0o700))
			script := filepath.Join(dir, "study.sh")
			require.NoError(t, os.WriteFile(script, []byte(telemetryStudyStep(t).Run), 0o600))
			// Execute the real bash -e workflow body: tee must not turn a
			// failed benchmark into a successful study or discard its log.
			cmd := exec.CommandContext(t.Context(), "bash", "-e", script)
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"EXPECTED_ARCH=amd64", "EXPECTED_SOURCE=candidate-head", "PROBE_ARCH=" + tc.arch,
				"PROBE_SOURCE=" + tc.source, "PROBE_EXIT=" + strconv.Itoa(tc.probeExit), "STUDY_EXIT=" + strconv.Itoa(tc.taskExit)}
			output, err := cmd.CombinedOutput()
			if tc.wantExit == 0 {
				require.NoError(t, err, "%s", output)
			} else {
				var exitError *exec.ExitError
				require.ErrorAs(t, err, &exitError, "%s", output)
			}
			assert.Equal(t, tc.wantExit, cmd.ProcessState.ExitCode(), "%s", output)
			assert.Equal(t, tc.invoked, strings.Contains(string(output), telemetryMarker))
			if tc.arch == handoffAMD64 && tc.source == telemetryHead {
				log, readErr := os.ReadFile(filepath.Join(dir, telemetryProbeLog))
				require.NoError(t, readErr)
				assert.Equal(t, "telemetry probe ran\n", string(log))
			}
		})
	}
}
