package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/benchmarkmatrix"
	"github.com/stretchr/testify/require"
)

func TestCommand(t *testing.T) {
	t.Parallel()
	for _, tier := range []string{"compatibility", benchmarkmatrix.Nightly, "release"} {
		var out bytes.Buffer
		require.NoError(t, run(context.Background(), []string{tier, "--list"}, &out, nil))
		var cases []any
		require.NoError(t, json.Unmarshal(out.Bytes(), &cases))
		if tier == "compatibility" {
			require.Len(t, cases, 16)
		} else {
			require.Len(t, cases, 2)
		}
	}
	for _, args := range [][]string{nil, {"bad"}, {benchmarkmatrix.Nightly, "--bad"}, {benchmarkmatrix.Nightly, "trailing"}, {"release"},
		{benchmarkmatrix.Nightly, "--controls", "/missing"}} {
		require.Error(t, run(context.Background(), args, io.Discard, nil))
	}
	controls := filepath.Join(t.TempDir(), "controls.json")
	require.NoError(t, os.WriteFile(controls, []byte("null"), 0o600))
	require.Error(t, run(context.Background(), []string{benchmarkmatrix.Nightly, "--controls", controls}, io.Discard, nil))
	require.NoError(t, os.WriteFile(controls, []byte("{}"), 0o600))
	calls := 0
	args := []string{benchmarkmatrix.Nightly, "--output", t.TempDir(), "--controls", controls}
	require.NoError(t, run(context.Background(), args, io.Discard,
		func(context.Context, process.Spec) (int, error) { calls++; return 0, nil }))
	require.Equal(t, 2, calls)
}

func TestMain(t *testing.T) {
	if mode := os.Getenv("MICROFAT_MATRIX_HELPER"); mode != "" {
		os.Args = []string{"benchmark-matrix"}
		if mode == "success" {
			os.Args = append(os.Args, benchmarkmatrix.Nightly, "--output", "output")
		}
		main()
		return
	}
	for _, mode := range []string{"usage", "success"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, "scripts"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(root, "scripts", "benchmark-ci.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o700))
			cmd := exec.Command(os.Args[0], "-test.run=^TestMain$")
			cmd.Env = append(os.Environ(), "MICROFAT_MATRIX_HELPER="+mode)
			cmd.Dir = root
			output, err := cmd.CombinedOutput()
			if mode == "usage" {
				require.Error(t, err)
				require.Contains(t, string(output), "usage: benchmark-matrix")
			} else {
				require.NoError(t, err, string(output))
				require.FileExists(t, filepath.Join(root, "output", "outcomes.json"))
			}
		})
	}
}
