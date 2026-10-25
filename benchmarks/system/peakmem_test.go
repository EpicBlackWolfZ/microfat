package system

import (
	"bytes"
	"debug/elf"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testCgroupPeakV2Bytes = 6291456
	testCgroupPeakV1Bytes = 8388608
	testExitCodeFailure   = 7
	testSignalKilled      = 9
)

func TestReadCgroupPeakBytesAndSandboxLifecycle(t *testing.T) {
	t.Parallel()

	var nilSandbox *Sandbox
	_, _, err := nilSandbox.PeakMemoryBytes()
	require.ErrorIs(t, err, ErrCgroupPeakUnavailable)

	_, _, err = ReadCgroupPeakBytes([]string{"", filepath.Join(t.TempDir(), "missing")})
	require.ErrorIs(t, err, ErrCgroupPeakUnavailable)

	v2Dir := filepath.Join(t.TempDir(), "leaf-v2")
	require.NoError(t, os.MkdirAll(v2Dir, defaultDirMode))
	require.NoError(t, os.WriteFile(filepath.Join(v2Dir, "memory.peak"), []byte("6291456\n"), controlMode))

	v1Dir := filepath.Join(t.TempDir(), "leaf-v1")
	require.NoError(t, os.MkdirAll(v1Dir, defaultDirMode))
	require.NoError(t, os.WriteFile(filepath.Join(v1Dir, "memory.max_usage_in_bytes"), []byte("8388608\n"), controlMode))

	invalidDir := filepath.Join(t.TempDir(), "leaf-invalid")
	require.NoError(t, os.MkdirAll(invalidDir, defaultDirMode))
	require.NoError(t, os.WriteFile(filepath.Join(invalidDir, "memory.peak"), []byte("-5\n"), controlMode))
	require.NoError(t, os.WriteFile(filepath.Join(invalidDir, "memory.max_usage_in_bytes"), []byte("not-a-number\n"), controlMode))

	_, _, err = ReadCgroupPeakBytes([]string{invalidDir})
	require.ErrorIs(t, err, ErrCgroupPeakUnavailable)

	peakV2, srcV2, err := ReadCgroupPeakBytes([]string{invalidDir, v2Dir})
	require.NoError(t, err)
	assert.Equal(t, int64(testCgroupPeakV2Bytes), peakV2)
	assert.Equal(t, "cgroup/memory.peak", srcV2)

	sandbox := &Sandbox{Paths: []string{v2Dir, v1Dir}}
	peakMax, srcMax, err := sandbox.PeakMemoryBytes()
	require.NoError(t, err)
	assert.Equal(t, int64(testCgroupPeakV1Bytes), peakMax)
	assert.Equal(t, "cgroup/memory.max_usage_in_bytes", srcMax)

	// Remove controller files so rmdir in Close succeeds, then verify post-teardown readback fails closed.
	require.NoError(t, os.Remove(filepath.Join(v2Dir, "memory.peak")))
	require.NoError(t, os.Remove(filepath.Join(v1Dir, "memory.max_usage_in_bytes")))
	require.NoError(t, sandbox.Close())

	_, _, err = sandbox.PeakMemoryBytes()
	require.ErrorIs(t, err, ErrCgroupPeakUnavailable)
}

func TestBuildAndWriteTrampolineELF(t *testing.T) {
	t.Parallel()

	_, err := BuildTrampolineELF("riscv64")
	require.ErrorContains(t, err, "unsupported trampoline architecture")

	_, err = WriteTrampolineELF("relative/dir", "amd64")
	require.ErrorContains(t, err, "must be absolute")

	_, err = WriteTrampolineELF(t.TempDir(), "mips64")
	require.ErrorContains(t, err, "unsupported trampoline architecture")

	_, err = WriteTrampolineELF(filepath.Join(t.TempDir(), "nonexistent-subdir"), "amd64")
	require.Error(t, err)

	cases := []struct {
		arch    string
		machine elf.Machine
	}{
		{arch: "amd64", machine: elf.EM_X86_64},
		{arch: "arm64", machine: elf.EM_AARCH64},
	}
	for _, tc := range cases {
		t.Run(tc.arch, func(t *testing.T) {
			t.Parallel()
			outPath, writeErr := WriteTrampolineELF(t.TempDir(), tc.arch)
			require.NoError(t, writeErr)
			data, readErr := os.ReadFile(outPath)
			require.NoError(t, readErr)

			parsed, parseErr := elf.NewFile(bytes.NewReader(data))
			require.NoError(t, parseErr)
			defer func() { _ = parsed.Close() }()

			assert.Equal(t, elf.ET_EXEC, parsed.Type)
			assert.Equal(t, tc.machine, parsed.Machine)
			require.Len(t, parsed.Progs, 1)
			assert.Equal(t, elf.PT_LOAD, parsed.Progs[0].Type)
		})
	}
}

func TestIsolatedExitErrorFormatting(t *testing.T) {
	t.Parallel()
	exitErr := &IsolatedExitError{ExitCode: testExitCodeFailure}
	assert.Contains(t, exitErr.Error(), "exited with code 7")

	sigErr := &IsolatedExitError{ExitCode: -1, Signal: testSignalKilled}
	assert.Contains(t, sigErr.Error(), "terminated by signal 9")
}
