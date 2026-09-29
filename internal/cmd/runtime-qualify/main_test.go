package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/releaseaudit"
	"github.com/stretchr/testify/require"
)

func TestMainReleasePolicy(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = []string{"runtime-qualify", "required-tag", "v0.3.0"}
	main()
}

func TestRequiredReleaseLineIncludingPrereleases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ tag, want string }{
		{"v0.2.9", "false\n"}, {"v0.3.0", "true\n"}, {"v0.3.0-rc.1", "true\n"}, {"v0.4.0-alpha", "true\n"}, {"v1.0.0", "true\n"},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, run(context.Background(), []string{"required-tag", tc.tag}, &out, nil))
			require.Equal(t, tc.want, out.String())
		})
	}
}

func TestArgumentsAndExecutionFailure(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"required-tag", "latest"}, {"--unknown"}, {"positional"}, {"--input", "candidate"}, {"--backend", "host"},
	} {
		var out bytes.Buffer
		require.Error(t, run(context.Background(), args, &out, nil))
	}
	var out bytes.Buffer
	path := filepath.Join(t.TempDir(), "output")
	injected := errors.New("missing exact toolchain")
	require.ErrorIs(t, run(context.Background(), []string{"--output", path}, &out,
		func(context.Context, process.Spec) (releaseaudit.Result, error) {
			return releaseaudit.Result{}, injected
		}), injected)
	require.Contains(t, out.String(), "Runtime qualification evidence:")
}
