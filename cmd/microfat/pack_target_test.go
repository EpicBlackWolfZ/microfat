package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const flagFormatVersion = "--format-version"

func TestCLIPackTargetArchitecture(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		arch      string
		canonical string
		level     string
	}{
		{name: "amd64 uppercase", arch: "AMD64", canonical: testArchAMD64, level: "v1"},
		{name: "amd64 alias", arch: "x86-64", canonical: testArchAMD64, level: "v1"},
		{name: "arm64 uppercase", arch: "ARM64", canonical: testArchARM64, level: arm64LevelV80},
		{name: "arm64 alias", arch: "AARCH64", canonical: testArchARM64, level: arm64LevelV80},
		{name: "unsupported baseline", arch: "banana", level: "v1"},
	}
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("format%d/%s", version, tc.name), func(t *testing.T) {
				t.Parallel()

				tempDir := t.TempDir()
				stubPath := filepath.Join(tempDir, "stub")
				payloadPath := filepath.Join(tempDir, "payload")
				outputPath := filepath.Join(tempDir, "output.fat")
				require.NoError(t, os.WriteFile(stubPath, []byte("#!/bin/sh\necho stub\n"), 0o755))
				require.NoError(t, os.WriteFile(payloadPath, []byte("#!/bin/sh\necho payload\n"), 0o755))
				sentinel := []byte("existing output")
				require.NoError(t, os.WriteFile(outputPath, sentinel, 0o600))

				cmd := newPackCmd()
				cmd.SetArgs([]string{
					flagStub, stubPath,
					flagOutput, outputPath,
					"--arch", tc.arch,
					flagFormatVersion, fmt.Sprint(version),
					"-v", tc.level + "=" + payloadPath,
					flagSkipELF,
				})
				err := cmd.Execute()
				if tc.canonical == "" {
					require.ErrorIs(t, err, pack.ErrUnsupportedArch)
					data, readErr := os.ReadFile(outputPath)
					require.NoError(t, readErr)
					assert.Equal(t, sentinel, data)
					return
				}

				require.NoError(t, err)
				idx := readCLIPackIndex(t, outputPath)
				assert.Equal(t, tc.canonical, idx.TargetArch)
				assert.Equal(t, version, idx.Version)
				assert.Equal(t, []string{tc.level}, idx.VariantLevels())
			})
		}
	}
}

func oppositeHostPackTarget(t *testing.T) (string, string) {
	t.Helper()

	switch runtime.GOARCH {
	case testArchAMD64:
		return testArchARM64, arm64LevelV80
	case testArchARM64:
		return testArchAMD64, "v1"
	default:
		t.Skipf("opposite-host fixture requires amd64 or arm64, got %s", runtime.GOARCH)
		return "", ""
	}
}

func readCLIPackIndex(t *testing.T, path string) *format.Index {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	idx, err := format.ReadTrailerAndIndex(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	return idx
}
