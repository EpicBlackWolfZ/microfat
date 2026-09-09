package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExternalCLIPackagingExample(t *testing.T) {
	t.Parallel()
	if runtime.GOARCH != archAMD64 {
		t.Skip("README example targets linux/amd64")
	}
	readme, err := os.ReadFile("../../README.md")
	require.NoError(t, err)
	_, section, found := strings.Cut(string(readme), "### Programmatic CLI Integration")
	require.True(t, found)
	_, code, found := strings.Cut(section, "```go\n")
	require.True(t, found)
	code, _, found = strings.Cut(code, "\n```")
	require.True(t, found)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), privateFilePerm))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/external-pack\n\ngo 1.27.1\n"), privateFilePerm))
	outputPath := filepath.Join(dir, "example.fat")
	const timeout = 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".", cliPath, stubPath, goldenVariantBins["v1"], outputPath)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	for _, command := range []string{"inspect", "verify"} {
		output, err = exec.CommandContext(ctx, cliPath, command, outputPath).CombinedOutput()
		require.NoError(t, err, string(output))
	}
	output, err = exec.CommandContext(ctx, outputPath).CombinedOutput()
	require.NoError(t, err, string(output))
}
