//go:build linux

package cigate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestSetupTaskActionAvoidsGitHubReleaseRateLimits(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)

	setupTaskUses := 0
	for _, path := range paths {
		base := filepath.Base(path)
		w := readWorkflow(t, base)
		for jobName, job := range w.Jobs {
			for _, step := range job.Steps {
				assert.False(t, strings.HasPrefix(step.Uses, "go-task/setup-task@"),
					"%s/%s must use ./.github/actions/setup-task instead of unauthenticated go-task/setup-task", base, jobName)
				if step.Uses == "./.github/actions/setup-task" {
					setupTaskUses++
					assert.Equal(t, "${{ steps.toolchain.outputs.task }}", fmt.Sprint(step.With["version"]),
						"%s/%s must pass the shared pinned Task version", base, jobName)
				}
			}
		}
	}
	assert.Positive(t, setupTaskUses, "expected workflows to use ./.github/actions/setup-task")

	actionPath := filepath.Join("..", "..", ".github", "actions", "setup-task", "action.yml")
	data, err := os.ReadFile(actionPath)
	require.NoError(t, err)
	var action struct {
		Runs struct {
			Using string         `yaml:"using"`
			Steps []workflowStep `yaml:"steps"`
		} `yaml:"runs"`
	}
	require.NoError(t, yaml.Unmarshal(data, &action))
	require.Equal(t, "composite", action.Runs.Using)

	pin := regexp.MustCompile(`^actions/cache@[0-9a-f]{40}$`)
	var sawCache, sawInstall bool
	for _, step := range action.Runs.Steps {
		if strings.HasPrefix(step.Uses, "actions/cache@") {
			sawCache = true
			assert.Regexp(t, pin, step.Uses)
			assert.Equal(t, "inputs.cache == 'true'", step.If)
			assert.Contains(t, fmt.Sprint(step.With["key"]), "inputs.version")
			assert.Contains(t, fmt.Sprint(step.With["key"]), "runner.arch")
		}
		if strings.Contains(step.Run, "scripts/install-task.sh") {
			sawInstall = true
			assert.Contains(t, step.Run, `GITHUB_PATH`)
		}
	}
	assert.True(t, sawCache, "setup-task action must cache the static binary when enabled")
	assert.True(t, sawInstall, "setup-task action must install and verify via scripts/install-task.sh")
}

func TestInstallTaskScriptContract(t *testing.T) {
	t.Parallel()
	scriptPath := filepath.Join("..", "..", "scripts", "install-task.sh")
	data, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	script := string(data)
	assert.Contains(t, script, "CGO_ENABLED=0")
	assert.Contains(t, script, `github.com/go-task/task/v3/cmd/task@v${TASK_VERSION}`)

	pinned := microfat.ToolPin("TASK_VERSION")
	require.NotEmpty(t, pinned)

	t.Run("cache_miss_builds_and_verifies_pinned_version", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		tools := filepath.Join(dir, "tools")
		dest := filepath.Join(dir, "dest")
		require.NoError(t, os.MkdirAll(tools, 0o755))
		logFile := filepath.Join(dir, "go.log")
		writeExecutableTestStub(t, filepath.Join(tools, "go"), fmt.Sprintf(
			"#!/usr/bin/env bash\n"+
				"set -euo pipefail\n"+
				"printf 'cgo=%%s|args=%%s\\n' \"${CGO_ENABLED:-}\" \"$*\" >> %q\n"+
				"cat > \"${GOBIN}/task\" <<'TASK'\n"+
				"#!/usr/bin/env bash\n"+
				"printf '%%s\\n' %q\n"+
				"TASK\n"+
				"chmod 0755 \"${GOBIN}/task\"\n",
			logFile, pinned,
		))

		cmd := exec.CommandContext(t.Context(), "bash", scriptPath, dest, pinned)
		cmd.Env = []string{"PATH=" + tools + string(os.PathListSeparator) + os.Getenv("PATH")}
		output, runErr := combinedOutputSafe(cmd)
		require.NoError(t, runErr, "%s", output)
		assert.Contains(t, string(output), "Installed Task "+pinned)

		logData, readErr := os.ReadFile(logFile)
		require.NoError(t, readErr)
		assert.Contains(t, string(logData), "cgo=0|args=install -trimpath -ldflags=-s -w github.com/go-task/task/v3/cmd/task@v"+pinned)

		// Second call against populated dest must reuse the cached binary without invoking go.
		require.NoError(t, os.Remove(filepath.Join(tools, "go")))
		cmd2 := exec.CommandContext(t.Context(), "bash", scriptPath, dest, pinned)
		cmd2.Env = []string{"PATH=" + tools + string(os.PathListSeparator) + os.Getenv("PATH")}
		output2, runErr2 := combinedOutputSafe(cmd2)
		require.NoError(t, runErr2, "%s", output2)
	})

	t.Run("rejects_mismatched_expected_or_cached_version", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		dest := filepath.Join(dir, "dest")
		require.NoError(t, os.MkdirAll(dest, 0o755))
		writeExecutableTestStub(t, filepath.Join(dest, "task"), "#!/usr/bin/env bash\nprintf '0.0.1\\n'\n")

		cmd := exec.CommandContext(t.Context(), "bash", scriptPath, dest, pinned)
		output, runErr := combinedOutputSafe(cmd)
		require.Error(t, runErr)
		assert.Contains(t, string(output), "Expected Task "+pinned)

		cmdBadArg := exec.CommandContext(t.Context(), "bash", scriptPath, dest, "0.0.1")
		outputBadArg, runErrBadArg := combinedOutputSafe(cmdBadArg)
		require.Error(t, runErrBadArg)
		assert.Contains(t, string(outputBadArg), "does not match shared pin")
	})
}
