package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const transformationTrimToFlag = "--microfat:trim-to"

type transformationDestinationPath struct {
	name     string
	basename string
	absolute bool
}

type transformationFileSnapshot struct {
	info   os.FileInfo
	digest [sha256.Size]byte
}

func TestLauncherTransformationDestinationPaths(t *testing.T) {
	t.Parallel()

	expectedOutput, stderr, err := runTransformationPathProcess(t, t.TempDir(), goldenFatBin)
	require.NoError(t, err, "source payload failed to execute: %s", stderr)
	require.Contains(t, expectedOutput, "golden:variant=")

	testTransformationDestinationMatrix(t, func(t *testing.T, dir, source, destination string, args []string) {
		before := snapshotTransformationFile(t, source)
		expectedEntries := []string{filepath.Base(source), filepath.Base(destination)}
		trimmedName := strings.TrimSpace(filepath.Base(destination))
		const decoyContents = "preserve the distinct trimmed-name destination"
		if trimmedName != "" {
			decoy := filepath.Join(dir, trimmedName)
			require.NoError(t, os.WriteFile(decoy, []byte(decoyContents), privateFilePerm))
			expectedEntries = append(expectedEntries, trimmedName)
		}

		stdout, stderr, err := runTransformationPathProcess(t, dir, source, args...)
		require.NoError(t, err, "transformation failed; stdout: %s; stderr: %s", stdout, stderr)
		require.NotContains(t, stdout, "golden:variant=", "transformation must not execute the payload")
		requireTransformationFileUnchanged(t, source, before)

		destinationInfo, err := os.Stat(destination)
		require.NoError(t, err, "requested destination path must exist exactly")
		require.True(t, destinationInfo.Mode().IsRegular())
		require.NotZero(t, destinationInfo.Mode().Perm()&0o111, "destination must be executable")
		if trimmedName != "" {
			decoyData, readErr := os.ReadFile(filepath.Join(dir, trimmedName))
			require.NoError(t, readErr)
			require.Equal(t, decoyContents, string(decoyData), "trimmed-name collision must remain untouched")
		}
		requireTransformationDirectoryEntries(t, dir, expectedEntries)

		stdout, stderr, err = runTransformationPathProcess(t, dir, destination)
		require.NoError(t, err, "transformed destination failed to execute: %s", stderr)
		require.Equal(t, expectedOutput, stdout, "must execute the same payload as the source")
		requireTransformationFileUnchanged(t, source, before)
		requireTransformationDirectoryEntries(t, dir, expectedEntries)
	})
}

func TestLauncherTransformationExactDestinationCollision(t *testing.T) {
	t.Parallel()

	testTransformationDestinationMatrix(t, func(t *testing.T, dir, source, destination string, args []string) {
		const destinationContents = "preserve the exact requested destination"
		require.NoError(t, os.WriteFile(destination, []byte(destinationContents), privateFilePerm))
		beforeSource := snapshotTransformationFile(t, source)
		beforeDestination := snapshotTransformationFile(t, destination)

		_, stderr, err := runTransformationPathProcess(t, dir, source, args...)
		require.Error(t, err, "existing exact destination must be refused")
		require.Contains(t, stderr, "destination already exists")
		requireTransformationFileUnchanged(t, source, beforeSource)
		requireTransformationFileUnchanged(t, destination, beforeDestination)
		requireTransformationDirectoryEntries(t, dir, []string{filepath.Base(source), filepath.Base(destination)})
	})
}

func testTransformationDestinationMatrix(t *testing.T, test func(*testing.T, string, string, string, []string)) {
	t.Helper()
	paths := []transformationDestinationPath{
		{name: "LeadingSpacesRelative", basename: "  leading-output"},
		{name: "TrailingSpacesAbsolute", basename: "output  ", absolute: true},
		{name: "TrailingTabRelative", basename: "output\t"},
		{name: "SpacesOnlyRelative", basename: "  "},
		{name: "TabOnlyRelative", basename: "\t"},
	}
	forms := []struct {
		name   string
		equals bool
	}{
		{name: "Equals", equals: true},
		{name: "Separate"},
	}
	for _, command := range []string{"--microfat:optimize-to", transformationTrimToFlag, "--microfat:specialize-to"} {
		for _, form := range forms {
			for _, path := range paths {
				t.Run(strings.TrimPrefix(command, "--microfat:")+"/"+form.name+"/"+path.name, func(t *testing.T) {
					t.Parallel()
					dir := t.TempDir()
					source := filepath.Join(dir, "source.fat")
					// TestMain builds goldenFatBin with the full launcher even in the minimal-tagged test suite.
					copyFile(t, goldenFatBin, source)
					destination := filepath.Join(dir, path.basename)
					argument := path.basename
					if path.absolute {
						argument = destination
					}
					args := []string{command}
					if form.equals {
						args[0] += "=" + argument
					} else {
						args = append(args, argument)
					}
					test(t, dir, source, destination, args)
				})
			}
		}
	}
}

func snapshotTransformationFile(t *testing.T, path string) transformationFileSnapshot {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return transformationFileSnapshot{info: info, digest: sha256.Sum256(data)}
}

func requireTransformationFileUnchanged(t *testing.T, path string, before transformationFileSnapshot) {
	t.Helper()
	after := snapshotTransformationFile(t, path)
	require.True(t, os.SameFile(before.info, after.info), "file inode changed: %q", path)
	require.Equal(t, before.info.Mode(), after.info.Mode(), "file mode changed: %q", path)
	require.Equal(t, before.info.ModTime(), after.info.ModTime(), "file timestamp changed: %q", path)
	require.Equal(t, before.digest, after.digest, "file contents changed: %q", path)
}

func requireTransformationDirectoryEntries(t *testing.T, dir string, expected []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	require.ElementsMatch(t, expected, names, "destination directory must have no staging files or unintended paths")
}

func runTransformationPathProcess(t *testing.T, dir, path string, args ...string) (string, string, error) {
	t.Helper()
	const timeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	err := runFixtureCommand(func() *exec.Cmd {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "MICROFAT_EXEC_MODE=memfd")
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		return cmd
	})
	require.NoError(t, ctx.Err(), "transformation or payload blocked: %s", stderr.String())
	return stdout.String(), stderr.String(), err
}
