package main

import (
	"bytes"
	"context"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBoundaryImages(t *testing.T) {
	t.Parallel()
	for _, architecture := range []string{"amd64", "arm64"} {
		for _, profile := range boundaryProfiles {
			t.Run(architecture+"/"+profile.name, func(t *testing.T) {
				t.Parallel()
				image, err := buildImage(architecture, profile.bytes)
				require.NoError(t, err)
				require.Len(t, image, profile.bytes)
				repeated, err := buildImage(architecture, profile.bytes)
				require.NoError(t, err)
				require.Equal(t, image, repeated)
				parsed, err := elf.NewFile(bytes.NewReader(image))
				require.NoError(t, err)
				require.Equal(t, elf.ET_EXEC, parsed.Type)
				require.Equal(t, elf.ELFCLASS64, parsed.Class)
				expectedMachine := elf.EM_X86_64
				if architecture == "arm64" {
					expectedMachine = elf.EM_AARCH64
				}
				require.Equal(t, expectedMachine, parsed.Machine)
				require.Len(t, parsed.Progs, 1)
				segment := parsed.Progs[0]
				require.Equal(t, elf.PT_LOAD, segment.Type)
				require.Equal(t, elf.PF_R|elf.PF_X, segment.Flags)
				require.EqualValues(t, len(image), segment.Filesz)
				require.Equal(t, segment.Filesz, segment.Memsz)
				require.EqualValues(t, loadAddress+codeOffset, parsed.Entry)
			})
		}
	}
}

func TestBoundaryNativeExecution(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("fixtures execute on native amd64 or arm64 Linux")
	}
	output := t.TempDir()
	require.NoError(t, generate(runtime.GOARCH, output))
	for _, profile := range boundaryProfiles {
		t.Run(profile.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(output, "boundary-"+profile.name)
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.EqualValues(t, profile.bytes, info.Size())
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			actual, err := exec.CommandContext(ctx, path).CombinedOutput()
			require.NoError(t, err, "%s", actual)
			require.Equal(t, marker, string(actual))
		})
	}
}

func TestInvalidBoundaryInputs(t *testing.T) {
	t.Parallel()
	_, err := buildImage("unsupported", boundaryBytes)
	require.ErrorContains(t, err, "unsupported architecture")
	_, err = buildImage("amd64", 1)
	require.ErrorContains(t, err, "too small")
	require.ErrorContains(t, generate("amd64", ""), "-output is required")
	require.ErrorContains(t, generate("unsupported", t.TempDir()), "unsupported architecture")
	require.ErrorContains(t, generate("amd64", filepath.Join(t.TempDir(), "missing")), "write below")
}
