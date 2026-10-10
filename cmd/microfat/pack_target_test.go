package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/builder"
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

func TestCLIPackTargetOperatingSystem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		targetOS string
		valid    bool
		omitFlag bool
	}{
		{name: "default", valid: true, omitFlag: true},
		{name: "empty", valid: true},
		{name: "linux", targetOS: testOSLinux, valid: true},
		{name: "uppercase", targetOS: "LINUX", valid: true},
		{name: "freebsd", targetOS: "freebsd"},
		{name: "windows", targetOS: "windows"},
		{name: "unknown", targetOS: "banana"},
		{name: "whitespace", targetOS: " linux "},
	}
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("format%d/%s", version, tc.name), func(t *testing.T) {
				t.Parallel()

				tempDir := t.TempDir()
				stubPath := filepath.Join(tempDir, "stub")
				payloadPath := filepath.Join(tempDir, "payload")
				outputPath := filepath.Join(tempDir, "output.fat")
				require.NoError(t, os.WriteFile(stubPath, []byte("stub"), 0o755))
				require.NoError(t, os.WriteFile(payloadPath, []byte("payload"), 0o755))
				sentinel := []byte("existing output")
				require.NoError(t, os.WriteFile(outputPath, sentinel, 0o600))

				cmd := newPackCmd()
				args := []string{
					flagStub, stubPath,
					flagOutput, outputPath,
					flagFormatVersion, fmt.Sprint(version),
					"-v", "v1=" + payloadPath,
					flagSkipELF,
				}
				if !tc.omitFlag {
					args = append(args, "--os", tc.targetOS)
				}
				cmd.SetArgs(args)
				err := cmd.Execute()
				if !tc.valid {
					require.ErrorIs(t, err, pack.ErrUnsupportedOS)
					data, readErr := os.ReadFile(outputPath)
					require.NoError(t, readErr)
					assert.Equal(t, sentinel, data)
					return
				}

				require.NoError(t, err)
				idx := readCLIPackIndex(t, outputPath)
				assert.Equal(t, testOSLinux, idx.TargetOS)
				assert.Equal(t, version, idx.Version)
			})
		}
	}
}

func TestCLIManifestUnsupportedOperatingSystem(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"pack", "pgo-pack"} {
		for _, targetOS := range []string{"freebsd", "windows", "banana", " linux "} {
			for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
				t.Run(fmt.Sprintf("%s/%s/format%d", command, targetOS, version), func(t *testing.T) {
					t.Parallel()

					tempDir := t.TempDir()
					manifestPath := filepath.Join(tempDir, "manifest.yaml")
					outputPath := filepath.Join(tempDir, "output.fat")
					sentinel := []byte("existing output")
					require.NoError(t, os.WriteFile(outputPath, sentinel, 0o600))
					manifest := "target_os: " + strconv.Quote(targetOS) + "\nvariants:\n  - level: v1\n"
					require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o600))

					cmd := newPackCmd()
					if command == "pgo-pack" {
						cmd = newPgoPackCmd()
					}
					cmd.SetArgs([]string{
						flagManifest, manifestPath,
						flagOutput, outputPath,
						flagFormatVersion, fmt.Sprint(version),
						flagSkipELF,
					})
					require.ErrorIs(t, cmd.Execute(), builder.ErrUnsupportedOS)
					data, err := os.ReadFile(outputPath)
					require.NoError(t, err)
					assert.Equal(t, sentinel, data)
				})
			}
		}
	}
}

func TestCLIPackUnsupportedOperatingSystemBeforeStubDiscovery(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "output.fat")
	sentinel := []byte("existing output")
	require.NoError(t, os.WriteFile(outputPath, sentinel, 0o600))

	cmd := newPackCmd()
	cmd.SetArgs([]string{
		"--os", "freebsd",
		flagStub, filepath.Join(tempDir, "missing-stub"),
		flagOutput, outputPath,
		"-v", "v1=" + filepath.Join(tempDir, "missing-payload"),
	})
	require.ErrorIs(t, cmd.Execute(), pack.ErrUnsupportedOS)
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Equal(t, sentinel, data)
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
