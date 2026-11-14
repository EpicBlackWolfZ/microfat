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
	handoffBranchCondition = "(github.head_ref || github.ref_name) == 'perf/verified-cache-descriptor' && " +
		"!(" + ciMetadataCondition + ")"
	handoffBaselineSHA    = "c28bbe73fb4a43ddab85443865ba52c6f3ef4e01"
	handoffExpectedSource = "${{ github.event.pull_request.head.sha || github.sha }}"
	handoffEvidencePath   = ".work/fd-handoff-confirmation/"
	handoffMarker         = "verified descriptor confirmation ran\n"
	handoffFailure        = 73
	handoffAMD64          = "amd64"
	handoffARM64          = "arm64"
	handoffLintJob        = "lint"
)

func TestMetadataEditsCannotReplaceOrCancelSourceQualification(t *testing.T) {
	t.Parallel()
	w := readWorkflow(t, "ci.yml")
	assert.Equal(t, "${{ github.workflow }}-${{ github.ref }}-${{ "+ciMetadataCondition+" && 'metadata' || 'source' }}",
		w.Concurrency.Group)
	assert.Equal(t, true, w.Concurrency.CancelInProgress)
	assert.Equal(t, "${{ "+ciMetadataCondition+" && 'CI Metadata Complete' || 'CI Complete' }}", w.Jobs["complete"].Name)
	assert.Equal(t, "${{ "+ciMetadataCondition+
		" && 'Native metadata runtime qualification' || 'Native runtime qualification' }} (${{ matrix.arch }})",
		w.Jobs["runtime"].Name)
}

func TestVerifiedCacheConfirmationSharesFastGatesAndRetainsEveryOutcome(t *testing.T) {
	t.Parallel()
	w := readWorkflow(t, "ci.yml")
	job := w.Jobs["runtime"]
	assert.ElementsMatch(t, []string{handoffLintJob, "test"}, needs(t, job))
	assert.Contains(t, needs(t, w.Jobs["complete"]), "runtime")
	assert.Equal(t, "needs.lint.outputs.run-code-checks == 'true'", job.If)
	assert.ElementsMatch(t, []string{handoffAMD64, handoffARM64}, job.Strategy.Matrix.Arch)
	require.NotNil(t, job.Strategy.FailFast)
	assert.False(t, *job.Strategy.FailFast)
	require.NotEmpty(t, job.Steps)
	assert.True(t, strings.HasPrefix(job.Steps[0].Uses, "actions/checkout@"))
	assert.Equal(t, handoffExpectedSource, job.Steps[0].With["ref"])
	assert.Equal(t, false, job.Steps[0].With["persist-credentials"])

	runtimeIndex, baselineIndex, confirmIndex, evidenceIndex := -1, -1, -1, -1
	for index, step := range job.Steps {
		if strings.Contains(step.Run, "task qualify-runtime") {
			runtimeIndex = index
			assert.Empty(t, step.If)
			assert.Contains(t, step.Run, "RUNTIME_TESTS=required")
			assert.Nil(t, step.ContinueOnError)
		}
		if strings.HasPrefix(step.Uses, "actions/checkout@") && step.With["path"] == ".work/fd-handoff-base" {
			require.Equal(t, -1, baselineIndex, "baseline checkout must be unique")
			baselineIndex = index
			assert.Equal(t, handoffBaselineSHA, step.With["ref"])
			assert.Equal(t, handoffBranchCondition, step.If)
			assert.Equal(t, false, step.With["persist-credentials"])
			assert.Nil(t, step.ContinueOnError)
		}
		if strings.Contains(step.Run, "task qualify-cache-handoff") {
			require.Equal(t, -1, confirmIndex, "confirmation must run only once per native job")
			confirmIndex = index
			assert.Equal(t, handoffBranchCondition, step.If)
			assert.Equal(t, "${{ matrix.arch }}", step.Env["EXPECTED_ARCH"])
			assert.Equal(t, handoffExpectedSource, step.Env["EXPECTED_SOURCE"])
			assert.Contains(t, step.Run, `test "$(go env GOARCH)" = "$EXPECTED_ARCH"`)
			assert.Contains(t, step.Run, `test "$(git rev-parse HEAD)" = "$EXPECTED_SOURCE"`)
			assert.Nil(t, step.ContinueOnError)
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			paths, ok := step.With["path"].(string)
			require.True(t, ok)
			if slices.Contains(strings.Fields(paths), handoffEvidencePath) {
				evidenceIndex = index
				assert.Equal(t, "always()", step.If)
				assert.ElementsMatch(t, []string{".work/runtime-qualification/", handoffEvidencePath}, strings.Fields(paths))
				assert.Equal(t, true, step.With["include-hidden-files"])
				assert.Equal(t, "error", step.With["if-no-files-found"])
				assert.Equal(t, 30, step.With["retention-days"])
				assert.Contains(t, step.With["name"], "github.run_attempt")
				assert.Contains(t, step.With["name"], "matrix.arch")
				assert.Nil(t, step.ContinueOnError)
			}
		}
	}
	require.GreaterOrEqual(t, runtimeIndex, 0)
	assert.Greater(t, baselineIndex, runtimeIndex)
	assert.Greater(t, confirmIndex, baselineIndex)
	assert.Greater(t, evidenceIndex, confirmIndex)
}

func TestVerifiedCacheConfirmationCannotHideQualificationOrIdentityFailure(t *testing.T) {
	t.Parallel()
	var body string
	for _, step := range readWorkflow(t, "ci.yml").Jobs["runtime"].Steps {
		if strings.Contains(step.Run, "task qualify-cache-handoff") {
			body = step.Run
		}
	}
	require.NotEmpty(t, body)
	for _, tc := range []struct {
		name, arch, source string
		taskExit, wantExit int
		invoked            bool
	}{
		{"qualification failure", handoffAMD64, "candidate-head", handoffFailure, handoffFailure, true},
		{"success", handoffAMD64, "candidate-head", 0, 0, true},
		{"wrong architecture", handoffARM64, "candidate-head", 0, 1, false},
		{"wrong source", handoffAMD64, "different-head", 0, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, script := range map[string]string{
				"go":   "#!/bin/bash\nprintf '%s\\n' \"$HANDOFF_GO_ARCH\"\n",
				"git":  "#!/bin/bash\nprintf '%s\\n' \"$HANDOFF_GIT_SOURCE\"\n",
				"task": "#!/bin/bash\nprintf 'verified descriptor confirmation ran\\n'\nexit \"$HANDOFF_TASK_EXIT\"\n",
			} {
				writeExecutableTestStub(t, filepath.Join(dir, name), script)
			}
			script := filepath.Join(dir, "qualification.sh")
			require.NoError(t, os.WriteFile(script, []byte(body), 0o600))
			cmd := exec.CommandContext(t.Context(), "bash", "-e", script)
			cmd.Env = []string{
				"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"EXPECTED_ARCH=amd64", "EXPECTED_SOURCE=candidate-head", "HANDOFF_GO_ARCH=" + tc.arch,
				"HANDOFF_GIT_SOURCE=" + tc.source, "HANDOFF_TASK_EXIT=" + strconv.Itoa(tc.taskExit),
			}
			output, err := combinedOutputSafe(cmd)
			if tc.wantExit == 0 {
				require.NoError(t, err, "%s", output)
			} else {
				var exitError *exec.ExitError
				require.ErrorAs(t, err, &exitError, "%s", output)
			}
			assert.Equal(t, tc.wantExit, cmd.ProcessState.ExitCode(), "%s", output)
			assert.Equal(t, tc.invoked, strings.Contains(string(output), handoffMarker))
		})
	}
}
