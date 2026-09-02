package e2e_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	expectedExitCode42  = 42
	expectedExitCode101 = 101
	highVolumeArgCount  = 200
)

func TestProcessFidelityAndExecutionInvariants(t *testing.T) {
	t.Parallel()

	t.Run("Scenario27_ComplexArgumentsForwarding", func(t *testing.T) {
		t.Parallel()
		args := []string{
			"--echo-args",
			"arg with spaces",
			"--flag=val",
			"quote's test",
			"unicode:🚀-⚡",
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, nil, args...)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		expectedEcho := "arg with spaces||--flag=val||quote's test||unicode:🚀-⚡"
		if stdout != expectedEcho {
			t.Fatalf("expected stdout %q, got %q", expectedEcho, stdout)
		}
	})

	t.Run("Scenario28_HighVolumeArguments", func(t *testing.T) {
		t.Parallel()
		args := make([]string, 0, highVolumeArgCount+1)
		args = append(args, "--echo-args")
		expectedParts := make([]string, 0, highVolumeArgCount)
		for i := 0; i < highVolumeArgCount; i++ {
			argVal := fmt.Sprintf("arg_%03d", i)
			args = append(args, argVal)
			expectedParts = append(expectedParts, argVal)
		}

		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, nil, args...)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		expectedEcho := strings.Join(expectedParts, "||")
		if stdout != expectedEcho {
			t.Fatalf("high volume arguments mismatch, length expected %d, got %d", len(expectedEcho), len(stdout))
		}
	})

	t.Run("Scenario29_EnvironmentForwarding", func(t *testing.T) {
		t.Parallel()
		expectedVal := "secret_token_42_xyz"
		env := []string{
			"TEST_CUSTOM_KEY=" + expectedVal,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env, "--echo-env", "TEST_CUSTOM_KEY")
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		if stdout != expectedVal {
			t.Fatalf("expected environment value %q, got %q", expectedVal, stdout)
		}
	})

	t.Run("Scenario30_ExitCodePropagation", func(t *testing.T) {
		t.Parallel()

		testCases := []struct {
			name         string
			codeArg      string
			expectedCode int
		}{
			{"ExitCode_0", "0", defaultExitCode},
			{"ExitCode_42", "42", expectedExitCode42},
			{"ExitCode_101", "101", expectedExitCode101},
		}

		for _, tc := range testCases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				_, _, exitCode, _ := executeFatBinary(t, goldenFatBin, nil, "--exit-code", tc.codeArg)
				if exitCode != tc.expectedCode {
					t.Fatalf("expected exit code %d, got %d", tc.expectedCode, exitCode)
				}
			})
		}
	})

	t.Run("Scenario31_StdinPipeline", func(t *testing.T) {
		t.Parallel()
		inputData := "hello from piped stdin stream to microfat launcher payload\nsecond line"
		cmd := exec.Command(goldenFatBin, "--cat-stdin")
		cmd.Stdin = strings.NewReader(inputData)

		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf

		err := cmd.Run()
		if err != nil {
			t.Fatalf("execution with stdin failed: %v\nstderr: %s", err, stderrBuf.String())
		}
		if stdoutBuf.String() != inputData {
			t.Fatalf("expected stdout %q, got %q", inputData, stdoutBuf.String())
		}
	})

	t.Run("Scenario32_PathWithSpaces", func(t *testing.T) {
		t.Parallel()
		spacedDir := filepath.Join(t.TempDir(), "space test dir", "nested sub dir")
		if err := os.MkdirAll(spacedDir, defaultFilePerm); err != nil {
			t.Fatalf("mkdir %s: %v", spacedDir, err)
		}

		spacedFatBin := filepath.Join(spacedDir, "my fat app with spaces.fat")
		_ = copyFile(t, goldenFatBin, spacedFatBin)

		stdout, stderr, exitCode, err := executeFatBinary(t, spacedFatBin, nil)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("executing fat binary with spaces in path failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		if !strings.Contains(stdout, "golden:variant="+currentHostLevel) {
			t.Fatalf("unexpected output executing from spaced path:\n%s", stdout)
		}
	})
}
