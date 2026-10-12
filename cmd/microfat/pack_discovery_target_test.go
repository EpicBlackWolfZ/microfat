package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/builder"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	flagArch          = "--arch"
	flagAll           = "--all"
	testWhitespaceApp = "whitespace-app"
)

func TestCLIPackArchitectureDiscovery(t *testing.T) {
	t.Parallel()
	buildDir := t.TempDir()
	cliPath := filepath.Join(buildDir, "microfat")
	build := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=false", "-o", cliPath, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)

	for _, discovery := range []string{"sibling", "PATH"} {
		for _, tc := range []struct {
			arch, canonical, level string
			machine                elf.Machine
		}{
			{"amd64", testArchAMD64, "v1", elf.EM_X86_64},
			{"AMD64", testArchAMD64, "v1", elf.EM_X86_64},
			{"x86-64", testArchAMD64, "v1", elf.EM_X86_64},
			{"X86_64", testArchAMD64, "v1", elf.EM_X86_64},
			{"arm64", testArchARM64, arm64LevelV80, elf.EM_AARCH64},
			{"ARM64", testArchARM64, arm64LevelV80, elf.EM_AARCH64},
			{"aarch64", testArchARM64, arm64LevelV80, elf.EM_AARCH64},
			{"AARCH64", testArchARM64, arm64LevelV80, elf.EM_AARCH64},
		} {
			t.Run(discovery+"/"+tc.arch, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				installedCLI := filepath.Join(dir, "microfat")
				copyExecutableFile(t, cliPath, installedCLI)
				stubDir := dir
				if discovery == "PATH" {
					stubDir = t.TempDir()
				}
				const elfHeaderSize = 64
				header := make([]byte, elfHeaderSize)
				copy(header, []byte{0x7f, 'E', 'L', 'F'})
				header[4] = byte(elf.ELFCLASS64)
				header[5] = byte(elf.ELFDATA2LSB)
				header[6] = byte(elf.EV_CURRENT)
				binary.LittleEndian.PutUint16(header[18:20], uint16(tc.machine))
				binary.LittleEndian.PutUint16(header[52:54], elfHeaderSize)
				stubPath := filepath.Join(stubDir, builder.StubBinaryFull)
				require.NoError(t, os.WriteFile(stubPath, header, 0o755))
				payloadPath := filepath.Join(dir, "payload")
				require.NoError(t, os.WriteFile(payloadPath, []byte("payload"), 0o755))
				fatPath := filepath.Join(dir, testWhitespaceApp)
				cmd := exec.CommandContext(t.Context(), installedCLI, "pack", flagArch, tc.arch,
					"-v", tc.level+"="+payloadPath, flagOutput, fatPath, flagSkipELF)
				cmd.Env = buildChildEnv(map[string]string{"PATH": stubDir})
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s", output)
				assert.Contains(t, string(output), "Using auto-discovered launcher stub: "+stubPath)
				assert.Equal(t, tc.canonical, readCLIPackIndex(t, fatPath).TargetArch)
			})
		}
	}
}

func TestCLIPackUnsupportedArchitectureBeforeStubDiscovery(t *testing.T) {
	t.Parallel()
	cmd := newPackCmd()
	cmd.SetArgs([]string{flagArch, "banana", flagStub, filepath.Join(t.TempDir(), "missing-stub")})
	require.ErrorIs(t, cmd.Execute(), pack.ErrUnsupportedArch)
}

func TestCLIPrewarmWhitespaceCacheDirectory(t *testing.T) {
	// Relative pathnames and working-directory changes run serially.
	for _, spelling := range []string{"relative", "cleaned-relative", "absolute"} {
		t.Run(spelling, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			const directoryName = "   "
			require.NoError(t, os.Mkdir(directoryName, 0o700))
			directory := directoryName
			if spelling == "cleaned-relative" {
				directory = "./" + directoryName
			} else if spelling == "absolute" {
				directory = filepath.Join(root, directoryName)
			}
			require.NoError(t, os.WriteFile("stub", []byte("stub"), 0o700))
			payload := []byte("cache payload")
			require.NoError(t, os.WriteFile("payload", payload, 0o700))
			idx, err := pack.Pack(pack.Options{
				StubPath: "stub", OutputPath: testWhitespaceApp, TargetArch: testArchAMD64,
				Variants: map[string]string{"v1": "payload"}, SkipELFValidation: true,
			})
			require.NoError(t, err)
			cmd := newPrewarmCmd()
			cmd.SetArgs([]string{testWhitespaceApp, flagAll, flagCacheDir, directory})
			require.NoError(t, cmd.Execute(), "cold prewarm must preserve legal pathname bytes")
			cached, err := os.ReadFile(filepath.Join(directoryName, idx.Variants[0].SHA256))
			require.NoError(t, err)
			assert.Equal(t, payload, cached)
			fatBytes, err := os.ReadFile(testWhitespaceApp)
			require.NoError(t, err)
			_, results, err := pack.PrewarmBinary(bytes.NewReader(fatBytes), int64(len(fatBytes)), nil, directoryName)
			require.NoError(t, err)
			require.Len(t, results, 1)
			assert.True(t, results[0].AlreadyCached)
			assert.Equal(t, int64(0), results[0].DecompressionUs)
			cmd = newPrewarmCmd()
			cmd.SetArgs([]string{testWhitespaceApp, flagAll, flagCacheDir, directoryName, flagVerify})
			require.NoError(t, cmd.Execute())
			entries, err := os.ReadDir(directoryName)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			assert.Equal(t, format.PrewarmStatusValid, pack.VerifyCacheVariant(&idx.Variants[0], directoryName).Status)
		})
	}
}
