package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/homebrew"
	"github.com/stretchr/testify/require"
)

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("write failure") }

func TestCommands(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := service{
		discover: func(context.Context) (string, error) { return "v0.3.0", nil },
		prepare: func(_ context.Context, tag, output string) (homebrew.Evidence, error) {
			require.Equal(t, "v0.3.0", tag)
			require.Equal(t, "out", output)
			return homebrew.Evidence{Tag: tag}, nil
		},
		check: func(_ context.Context, cask string) error { require.Equal(t, "cask", cask); return nil },
	}
	for _, args := range [][]string{nil, {"bad"}, {"discover", "--bad"}, {"discover", "extra"}, {"prepare"},
		{"check"}, {"update-needed"}} {
		require.Error(t, run(ctx, args, io.Discard, svc))
	}
	for _, args := range [][]string{{"discover"}, {"prepare", "--tag", "v0.3.0", "--output", "out"}, {"check", "--cask", "cask"}} {
		require.NoError(t, run(ctx, args, io.Discard, svc))
		if args[0] != "check" {
			require.Error(t, run(ctx, args, brokenWriter{}, svc))
		}
	}
	svc.discover = func(context.Context) (string, error) { return "", errors.New("discovery rejected") }
	svc.prepare = func(context.Context, string, string) (homebrew.Evidence, error) {
		return homebrew.Evidence{}, errors.New("authentication rejected")
	}
	require.Error(t, run(ctx, []string{"discover"}, io.Discard, svc))
	require.Error(t, run(ctx, []string{"prepare", "--tag", "v0.3.0", "--output", "out"}, io.Discard, svc))
	root := t.TempDir()
	current, candidate := filepath.Join(root, "current"), filepath.Join(root, "candidate")
	args := []string{"update-needed", "--cask", current, "--candidate", candidate}
	require.Error(t, run(ctx, args, io.Discard, svc))
	data, err := homebrew.Render(homebrew.Recipe{Version: "0.3.0", AMD64: strings.Repeat("a", 64), ARM64: strings.Repeat("b", 64)})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(candidate, data, 0o600))
	require.NoError(t, run(ctx, args, io.Discard, svc))
	require.Error(t, run(ctx, args, brokenWriter{}, svc))
	require.NoError(t, os.WriteFile(current, []byte("bad"), 0o600))
	require.Error(t, run(ctx, args, io.Discard, svc))
	require.Error(t, updateNeeded(root, candidate, io.Discard))
}

func TestCommandMain(t *testing.T) {
	if os.Getenv("MICROFAT_HOMEBREW_COMMAND_HELPER") == "1" {
		os.Args = []string{"homebrew-release"}
		main()
		return
	}
	t.Parallel()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCommandMain$")
	cmd.Env = append(os.Environ(), "MICROFAT_HOMEBREW_COMMAND_HELPER=1")
	output, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "usage: homebrew-release")
}
