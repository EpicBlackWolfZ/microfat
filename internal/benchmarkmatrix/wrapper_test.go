package benchmarkmatrix

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Preserve the shell entry point's exit status and evidence, even when measurement fails.
func TestWrapperPreservesFailedRun(t *testing.T) {
	t.Parallel()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	root := t.TempDir()
	tool := filepath.Join(root, "microfat")
	const script = `#!/bin/sh
case "$2" in
run) echo "$TEST_BUNDLE"; exit "$TEST_EXIT";;
verify) exit 0;;
report|gate) echo "synthetic summary";;
esac
`
	require.NoError(t, os.WriteFile(tool, []byte(script), 0o700))
	bundle := filepath.Join(root, "bundle")
	require.NoError(t, os.Mkdir(bundle, 0o700))
	for _, status := range []int{0, 7} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			output := filepath.Join(root, fmt.Sprint(status))
			cmd := exec.Command("bash", "scripts/benchmark-ci.sh")
			cmd.Dir = repository
			cmd.Env = append(os.Environ(), "BENCHMARK_BINARY="+tool, "BENCHMARK_OUTPUT="+output, "TEST_BUNDLE="+bundle,
				fmt.Sprintf("TEST_EXIT=%d", status), "BENCHMARK_BASE=", "GITHUB_STEP_SUMMARY=", "BENCHMARK_HOSTED_RELEASE=0")
			log, err := cmd.CombinedOutput()
			if status == 0 {
				require.NoError(t, err, string(log))
			} else {
				require.Error(t, err)
			}
			require.Equal(t, status, cmd.ProcessState.ExitCode())
			summary, err := os.ReadFile(filepath.Join(output, "summary.md"))
			require.NoError(t, err)
			require.Equal(t, "synthetic summary\n", string(summary))
		})
	}
}
