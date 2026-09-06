package autoload

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	testFixtureRelPath = "runtimeinit/testdata/autoload_app/main.go"
	testAutoloadOutput = "autoload ok"
)

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	goModPath := filepath.Join(root, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		t.Fatalf("expected go.mod at %s", goModPath)
	}
	return root
}

func runFixture(t *testing.T, repoRoot string, fixtureRelPath string, extraEnv []string) (string, string, error) {
	t.Helper()

	targetPath := filepath.Join(repoRoot, fixtureRelPath)
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		t.Fatalf("expected fixture at %s", targetPath)
	}

	cmd := exec.Command("go", "run", targetPath)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	cmd.Env = append(cmd.Env, extraEnv...)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	return stdoutBuf.String(), stderrBuf.String(), err
}

func TestAutoload_InitExecuted(t *testing.T) {
	t.Parallel()

	// In the test binary, package autoload init() has already executed without panicking.
}

func TestAutoload_Subprocess(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)

	testCases := []struct {
		name              string
		env               []string
		expectedExitErr   bool
		expectedStdout    string
		expectedStderrSub string
		unexpectedStderr  string
	}{
		{
			name: "AutoTuneOnStartupWithDebug",
			env: []string{
				format.EnvDebug + "=1",
				format.EnvAutotune + "=1",
			},
			expectedExitErr:   false,
			expectedStdout:    testAutoloadOutput,
			expectedStderrSub: "[microfat:runtimeinit]",
		},
		{
			name: "DisabledViaEnv",
			env: []string{
				format.EnvDebug + "=1",
				format.EnvAutotune + "=0",
			},
			expectedExitErr:   false,
			expectedStdout:    testAutoloadOutput,
			expectedStderrSub: "auto-tuning disabled by " + format.EnvAutotune,
		},
		{
			name: "JSONLoggingOnStartup",
			env: []string{
				format.EnvLog + "=json",
				format.EnvAutotune + "=1",
			},
			expectedExitErr:   false,
			expectedStdout:    testAutoloadOutput,
			expectedStderrSub: `"event":"runtimeinit"`,
		},
		{
			name: "SilentByDefault",
			env: []string{
				format.EnvDebug + "=0",
				format.EnvLog + "=0",
				format.EnvAutotune + "=1",
			},
			expectedExitErr:  false,
			expectedStdout:   testAutoloadOutput,
			unexpectedStderr: "[microfat:runtimeinit]",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, err := runFixture(t, repoRoot, testFixtureRelPath, tc.env)
			if tc.expectedExitErr && err == nil {
				t.Fatalf("expected error but got nil")
			}
			if !tc.expectedExitErr && err != nil {
				t.Fatalf("unexpected error: %v, stderr: %s", err, stderr)
			}

			if !strings.Contains(strings.TrimSpace(stdout), tc.expectedStdout) {
				t.Errorf("stdout %q does not contain expected %q", stdout, tc.expectedStdout)
			}

			if tc.expectedStderrSub != "" && !strings.Contains(stderr, tc.expectedStderrSub) {
				t.Errorf("stderr %q does not contain expected %q", stderr, tc.expectedStderrSub)
			}
			if tc.unexpectedStderr != "" && strings.Contains(stderr, tc.unexpectedStderr) {
				t.Errorf("stderr %q unexpectedly contains %q", stderr, tc.unexpectedStderr)
			}
		})
	}
}
