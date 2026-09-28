package cigate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

func TestMountHelpersLintedLocallyAndInCI(t *testing.T) {
	t.Parallel()
	want := []string{"./...", "./tests/e2e/testdata/mount_runner", "./tests/e2e/testdata/mount_reporter",
		"./tests/e2e/testdata/mountfixture", "./tests/e2e/testdata/descriptor_probe", "./tests/e2e/testdata/userns_runner"}
	data, err := os.ReadFile(filepath.Join("..", "..", "Taskfile.yml"))
	require.NoError(t, err)
	var tasks struct {
		Tasks struct {
			Lint struct {
				Commands []string `yaml:"cmds"`
			} `yaml:"lint-go"`
		} `yaml:"tasks"`
	}
	require.NoError(t, yaml.Unmarshal(data, &tasks))
	var localPackages, ciPackages []string
	for _, command := range tasks.Tasks.Lint.Commands {
		for line := range strings.SplitSeq(command, "\n") {
			if args, ok := strings.CutPrefix(strings.TrimSpace(line), "golangci-lint run "); ok {
				localPackages = append(localPackages, strings.Fields(args)...)
			}
		}
	}
	for _, step := range readWorkflow(t, "ci.yml").Jobs["lint"].Steps {
		if strings.HasPrefix(step.Uses, "golangci/golangci-lint-action@") {
			args, ok := step.With["args"].(string)
			require.True(t, ok)
			assert.Nil(t, step.ContinueOnError)
			for _, arg := range strings.Fields(args) {
				if !strings.HasPrefix(arg, "-") {
					ciPackages = append(ciPackages, arg)
				}
			}
		}
	}
	assert.ElementsMatch(t, want, localPackages)
	assert.ElementsMatch(t, localPackages, ciPackages, "required CI must lint every package in the local Task")
}
