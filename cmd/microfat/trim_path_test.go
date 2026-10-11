package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/require"
)

func TestTrimOutputPreservesPathBytes(t *testing.T) {
	t.Parallel()
	for _, name := range []string{" leading", "trailing ", "   ", "=literal", "="} {
		for _, equals := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/separate", true: "/equals"}[equals], func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				stub, payload, source := filepath.Join(dir, "stub"), filepath.Join(dir, "payload"), filepath.Join(dir, "source")
				require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o700))
				require.NoError(t, os.WriteFile(payload, []byte("payload"), 0o700))
				_, err := pack.Pack(pack.Options{
					StubPath: stub, OutputPath: source, TargetArch: testArchAMD64,
					SkipELFValidation: true, Variants: map[string]string{"v1": payload},
				})
				require.NoError(t, err)
				before, err := os.ReadFile(source)
				require.NoError(t, err)
				dest := filepath.Join(dir, name)
				args := []string{source, flagLevel, "v1", flagOutput, dest}
				if equals {
					args = []string{source, flagLevel, "v1", flagOutput + "=" + dest}
				}
				cmd := newTrimCmd()
				cmd.SetArgs(args)
				require.NoError(t, cmd.Execute())
				readCLIPackIndex(t, dest)
				written, err := os.ReadFile(dest)
				require.NoError(t, err)
				cmd = newTrimCmd()
				cmd.SetArgs(args)
				require.Error(t, cmd.Execute(), "create-only trim must preserve an existing destination")
				after, err := os.ReadFile(dest)
				require.NoError(t, err)
				require.Equal(t, written, after)
				after, err = os.ReadFile(source)
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
		}
	}
}

func TestTrimOutputRelativePathBytes(t *testing.T) {
	// Working-directory changes are process-wide, so these raw flag-value cases run serially.
	for _, name := range []string{" leading", "trailing ", "   ", "=literal", "="} {
		for _, equals := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/separate", true: "/equals"}[equals], func(t *testing.T) {
				t.Chdir(t.TempDir())
				stub, payload, source := "stub", "payload", "source"
				require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o700))
				require.NoError(t, os.WriteFile(payload, []byte("payload"), 0o700))
				_, err := pack.Pack(pack.Options{
					StubPath: stub, OutputPath: source, TargetArch: testArchAMD64,
					SkipELFValidation: true, Variants: map[string]string{"v1": payload},
				})
				require.NoError(t, err)
				args := []string{source, flagLevel, "v1", flagOutput, name}
				if equals {
					args = []string{source, flagLevel, "v1", flagOutput + "=" + name}
				}
				cmd := newTrimCmd()
				cmd.SetArgs(args)
				require.NoError(t, cmd.Execute())
				readCLIPackIndex(t, name)
				rewritten := strings.TrimSpace(strings.TrimPrefix(name, "="))
				if rewritten != "" {
					_, err = os.Stat(rewritten)
					require.ErrorIs(t, err, os.ErrNotExist)
				}
			})
		}
	}
}

func TestTrimOutputShortEqualsLiteral(t *testing.T) {
	t.Chdir(t.TempDir())
	stub, payload, source := "stub", "payload", "source"
	require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o700))
	require.NoError(t, os.WriteFile(payload, []byte("payload"), 0o700))
	_, err := pack.Pack(pack.Options{
		StubPath: stub, OutputPath: source, TargetArch: testArchAMD64,
		SkipELFValidation: true, Variants: map[string]string{"v1": payload},
	})
	require.NoError(t, err)
	cmd := newTrimCmd()
	cmd.SetArgs([]string{source, flagLevel, "v1", "-o="})
	require.NoError(t, cmd.Execute())
	readCLIPackIndex(t, "=")
}
