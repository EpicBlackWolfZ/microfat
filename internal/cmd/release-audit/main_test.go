package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/releaseaudit"
	"github.com/stretchr/testify/require"
)

const runCommand = "run"

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("output failed") }

func TestCommandModes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, args := range [][]string{nil, {"unknown"}, {"validate-tag"}, {"validate-tag", "bad"}, {"metadata"},
		{"metadata", "bad", "missing", "missing"},
		{runCommand, "--unknown"}, {runCommand}, {runCommand, "--dist", "dist", "--output", "out", "extra"},
		{runCommand, "--dist", "dist", "--output", "out", "--version", "bad"}} {
		require.Error(t, run(ctx, args, io.Discard, nil))
	}
	require.NoError(t, run(ctx, []string{"validate-tag", "v0.2.5"}, io.Discard, nil))
	directory := t.TempDir()
	release := filepath.Join(directory, "release.json")
	source := filepath.Join(directory, "source.txt")
	args := []string{"metadata", "v0.2.4", release, source}
	require.Error(t, run(ctx, args, io.Discard, nil))
	require.NoError(t, os.WriteFile(release, []byte(`{"tagName":"v0.2.4","isDraft":false,"isImmutable":true,"publishedAt":"date"}`), 0o600))
	require.Error(t, run(ctx, args, io.Discard, nil))
	require.NoError(t, os.WriteFile(source, []byte("bad"), 0o600))
	require.Error(t, run(ctx, args, io.Discard, nil))
	require.NoError(t, os.WriteFile(source, []byte(strings.Repeat("a", 40)), 0o600))
	var output strings.Builder
	require.NoError(t, run(ctx, args, &output, nil))
	require.Contains(t, output.String(), "version=0.2.4\n")
	require.Error(t, run(ctx, args, brokenWriter{}, nil))
	args = []string{runCommand, "--dist", directory, "--output", filepath.Join(directory, "out"), "--version", "0.2.4",
		"--source", strings.Repeat("a", 40), "--arch", runtime.GOARCH}
	called := false
	require.ErrorContains(t, run(ctx, args, io.Discard, func(context.Context, process.Spec) (releaseaudit.Result, error) {
		called = true
		return releaseaudit.Result{}, errors.New("authentication rejected")
	}), "authentication rejected")
	require.True(t, called)
}
func TestMain(t *testing.T) {
	if os.Getenv("MICROFAT_AUDIT_HELPER") == "1" {
		os.Args = []string{"release-audit"}
		main()
		return
	}
	t.Parallel()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMain$")
	cmd.Env = append(os.Environ(), "MICROFAT_AUDIT_HELPER=1")
	output, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "usage: release-audit")
}
