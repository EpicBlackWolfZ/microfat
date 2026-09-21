package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCoverageCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	input := filepath.Join(root, "input")
	output := filepath.Join(root, "output")
	const content = "mode: atomic\np/a.go:1.1,2.1 95 1\np/a.go:3.1,4.1 5 0\n"
	require.NoError(t, os.WriteFile(input, []byte(content), 0o600))
	args := []string{output, input, input}
	var out bytes.Buffer
	require.NoError(t, run(args, "", &out))
	require.Equal(t, "Exact coverage: 95/100 statements (95.000000%)\n", out.String())
	profile, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, content, string(profile))
	require.ErrorContains(t, run(args, "95.000001", &out), "without rounding")
	require.Error(t, run(args, "invalid", &out))
	require.Error(t, run(nil, "95", &out))
	require.Error(t, run([]string{output, input, "missing"}, "95", &out))
	require.Error(t, run([]string{root, input, input}, "95", &out))
	require.Error(t, run(args, "95", failingWriter{}))
	require.NoError(t, os.WriteFile(input, []byte("mode: atomic\np/a.go:1.1,2.1 0 0\n"), 0o600))
	require.ErrorContains(t, run(args, "95", &out), "below")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestMainCommand(t *testing.T) {
	if os.Getenv("MICROFAT_COVERAGE_HELPER") == "1" {
		os.Args = []string{"coverage"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainCommand$")
	cmd.Env = append(os.Environ(), "MICROFAT_COVERAGE_HELPER=1")
	output, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "usage: coverage")
}
