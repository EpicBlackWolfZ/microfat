package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/releasesbom"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	flagOutput     = "--output"
	testSPDXOutput = "out.spdx.json"
	testArchive    = "archive"
)

func TestParseArgs(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"GoReleaser":      {"archive.tar.gz", flagOutput, "spdx-json=out.spdx.json"},
		"Explicit":        {"--archive", "archive.tar.gz", flagOutput, testSPDXOutput, "--format", "spdx-json"},
		"Inferred":        {"archive.tar.gz", flagOutput, testSPDXOutput},
		"EqualsFlags":     {"--archive=archive.tar.gz", "--output=out.spdx.json", "--format=spdx-json"},
		"SingleDash":      {"-archive=archive.tar.gz", "-output=out.spdx.json", "-format=spdx"},
		"TrailingArchive": {flagOutput, testSPDXOutput, "archive.tar.gz"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			archive, output, format, err := parseArgs(args)
			require.NoError(t, err)
			assert.Equal(t, "archive.tar.gz", archive)
			assert.Equal(t, testSPDXOutput, output)
			assert.Equal(t, releasesbom.FormatSPDX, format)
		})
	}
	_, output, format, err := parseArgs([]string{testArchive, flagOutput, "cyclonedx-json=out.json"})
	require.NoError(t, err)
	assert.Equal(t, "out.json", output)
	assert.Equal(t, releasesbom.FormatCycloneDX, format)
	for _, args := range [][]string{
		{}, {testArchive}, {flagOutput, testSPDXOutput}, {testArchive, "--unknown"}, {testArchive, flagOutput, "out"},
		{testArchive, "--archive", "duplicate", flagOutput, testSPDXOutput}, {"one", "two"}, {flagOutput, testSPDXOutput, "one", "two"},
		{testArchive, flagOutput, "spdx-json="}, {testArchive, flagOutput, "out", "--format", "unsupported"}} {
		_, _, _, err := parseArgs(args)
		require.Error(t, err)
	}
	for _, name := range []string{"out.cyclonedx.json", "out=custom.cyclonedx.json"} {
		got, out, err := resolveOutput("", name)
		require.NoError(t, err)
		assert.Equal(t, name, out)
		assert.Equal(t, releasesbom.FormatCycloneDX, got)
	}
	got, _, err := resolveOutput("cyclonedx", "out")
	require.NoError(t, err)
	assert.Equal(t, releasesbom.FormatCycloneDX, got)
}

func TestRunAtomicOutputAndFailures(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "result.spdx.json")
	generate := func(context.Context, string, string) ([]byte, error) { return []byte("validated document"), nil }
	var stderr bytes.Buffer
	assert.Equal(t, 0, run(t.Context(), []string{testArchive, flagOutput, path}, &stderr, generate))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "validated document", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	assert.Equal(t, 1, run(t.Context(), nil, &stderr, generate))
	failingGenerator := func(context.Context, string, string) ([]byte, error) { return nil, errors.New("conversion rejected") }
	assert.Equal(t, 1, run(t.Context(), []string{testArchive, flagOutput, path}, &stderr, failingGenerator))
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "validated document", string(data), "failed conversion must preserve existing output")
	assert.Contains(t, stderr.String(), "conversion rejected")
	assert.Equal(t, 1, run(t.Context(), []string{testArchive, flagOutput, path + "/child.spdx.json"}, &stderr, generate))
	require.Error(t, writeAtomic(filepath.Dir(path), []byte("cannot replace directory")))
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".sbom-tmp-*"))
	require.NoError(t, err)
	assert.Empty(t, matches)
	_, err = generateMissingArchive(t.Context())
	require.Error(t, err)
}

func generateMissingArchive(ctx context.Context) ([]byte, error) {
	return generate(ctx, "missing", releasesbom.FormatSPDX)
}
