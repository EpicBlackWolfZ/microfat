package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/stretchr/testify/require"
)

const testLock = `{"fortio":{"module":"fortio.org/fortio","version":"v1.75.2","module_sum":"h1:module",
"go_mod_sum":"h1:mod","source_commit":"source","toolchain":"go1.27.1"}}`
const testModule = `{"Path":"fortio.org/fortio","Version":"v1.75.2","Sum":"h1:module","GoModSum":"h1:mod","Origin":{"Hash":"source"}}`
const testGoVersion = "go version go1.27.1 linux/amd64"
const kernelSuccess = "MICROFAT_KERNEL_RESULT=0\n"

func TestFortioIdentity(t *testing.T) {
	t.Parallel()
	version, err := fortioVersion([]byte(testLock), testGoVersion+"\n")
	require.NoError(t, err)
	require.Equal(t, "v1.75.2", version)
	require.NoError(t, verifyFortio([]byte(testLock), []byte(testModule)))
	for _, replacement := range []struct{ old, new string }{
		{"fortio.org/fortio", "example.org/other"}, {"v1.75.2", "v1.75.2-rc1"}, {"go1.27.1", "go1.26.1"},
		{"h1:module", ""}, {"h1:mod", ""}, {"source", ""},
	} {
		t.Run("lock "+replacement.old, func(t *testing.T) {
			t.Parallel()
			lock := []byte(strings.ReplaceAll(testLock, replacement.old, replacement.new))
			_, err := fortioVersion(lock, testGoVersion)
			require.Error(t, err)
			require.Error(t, verifyFortio(lock, []byte(testModule)))
		})
	}
	for _, invalid := range []string{"{", "{}", "null"} {
		_, err := readPin([]byte(invalid))
		require.Error(t, err)
	}
	for _, invalid := range []string{"", "go version go1.27.10 linux/amd64", "go version go1.26.1 linux/amd64",
		"go version go1.27.1", "unexpected go1.27.1 text", testGoVersion + " trailing"} {
		_, err := fortioVersion([]byte(testLock), invalid)
		require.Error(t, err)
	}
	for _, field := range []string{"Path", "Version", "Sum", "GoModSum", "Origin", "Error"} {
		t.Run("module "+field, func(t *testing.T) {
			t.Parallel()
			var module map[string]any
			require.NoError(t, json.Unmarshal([]byte(testModule), &module))
			module[field] = "mismatch"
			data, err := json.Marshal(module)
			require.NoError(t, err)
			require.Error(t, verifyFortio([]byte(testLock), data))
		})
	}
	require.Error(t, verifyFortio([]byte(testLock), []byte("{")))
	require.Error(t, verifyFortio([]byte(testLock), []byte("{}")))
}

func baselineGit(args ...string) ([]byte, error) {
	switch strings.Join(args, " ") {
	case "rev-parse HEAD", "rev-parse v0.2.5^{commit}":
		return []byte("current\n"), nil
	case "tag --merged HEAD --sort=-version:refname":
		return []byte("v1.0.0-rc1\ninvalid\nv0.2.5\nv0.2.4\nv0.2.3\n"), nil
	case "rev-parse v0.2.4^{commit}":
		return []byte("previous\n"), nil
	default:
		return nil, errors.New("unexpected git command")
	}
}

func TestPreviousRelease(t *testing.T) {
	t.Parallel()
	value, err := previousRelease(baselineGit)
	require.NoError(t, err)
	require.Equal(t, "previous", value)
	for _, failed := range []string{"rev-parse HEAD", "tag --merged HEAD --sort=-version:refname", "rev-parse v0.2.5^{commit}"} {
		t.Run(failed, func(t *testing.T) {
			t.Parallel()
			_, err := previousRelease(func(args ...string) ([]byte, error) {
				if strings.Join(args, " ") == failed {
					return nil, errors.New("git failed")
				}
				return baselineGit(args...)
			})
			require.ErrorContains(t, err, "git failed")
		})
	}
	_, err = previousRelease(func(...string) ([]byte, error) { return nil, nil })
	require.ErrorContains(t, err, "no previous stable release ancestor")
	version, err := gitOutput("--version")
	require.NoError(t, err)
	require.Contains(t, string(version), "git version")
}

func TestControls(t *testing.T) {
	t.Parallel()
	cpus := []int{7, 2, 7, 5}
	data, err := controls("/sys/fs/cgroup/owned", cpus)
	require.NoError(t, err)
	require.Equal(t, []int{7, 2, 7, 5}, cpus, "do not mutate the caller's affinity")
	var got struct{ Target, Generator system.Options }
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, system.Options{Affinity: []int{2}, CgroupRoot: "/sys/fs/cgroup/owned", Version: "v2",
		CPUQuotaUS: 100000, CPUPeriodUS: 100000, MemoryBytes: 2147483648}, got.Target)
	require.Equal(t, []int{7}, got.Generator.Affinity)
	got.Generator.Affinity = got.Target.Affinity
	require.Equal(t, got.Target, got.Generator)
	for _, cpus := range [][]int{nil, {1}, {1, 1}, {-1, 1}, {1, system.MaxCPU}} {
		_, err := controls("/cgroup", cpus)
		require.Error(t, err)
	}
	for _, root := range []string{"", "root\nINJECT=1", "root\rINJECT=1"} {
		_, err := controls(root, []int{1, 2})
		require.Error(t, err)
	}
}

func TestKernelResult(t *testing.T) {
	t.Parallel()
	for _, good := range []string{kernelSuccess, "boot\r\nPASS\r\nMICROFAT_KERNEL_RESULT=0\r\nshutdown\r\n"} {
		require.NoError(t, kernelResult([]byte(good)))
	}
	for name, bad := range map[string]string{
		"missing": "PASS\n", "failed": "MICROFAT_KERNEL_RESULT=1\n", "duplicate": kernelSuccess + kernelSuccess,
		"mixed": kernelSuccess + "MICROFAT_KERNEL_RESULT=1\n", "skip": "--- SKIP: required\n" + kernelSuccess,
		"test failed": "--- FAIL: required\n" + kernelSuccess, "suffix": "MICROFAT_KERNEL_RESULT=01\n",
		"embedded": "echo MICROFAT_KERNEL_RESULT=0\n",
	} {
		t.Run(name, func(t *testing.T) { t.Parallel(); require.Error(t, kernelResult([]byte(bad))) })
	}
}

func allowedCPUs([]int) ([]int, error) { return []int{1, 3}, nil }

func TestCommands(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lock, module, serial := filepath.Join(root, "lock"), filepath.Join(root, "module"), filepath.Join(root, "serial")
	require.NoError(t, os.WriteFile(lock, []byte(testLock), 0o600))
	require.NoError(t, os.WriteFile(module, []byte(testModule), 0o600))
	require.NoError(t, os.WriteFile(serial, []byte(kernelSuccess), 0o600))
	for name, args := range map[string][]string{
		baselineCommand: {baselineCommand}, controlsCommand: {controlsCommand, "/cgroup"}, "kernel": {kernelCommand, serial},
		"version": {versionCommand, lock, testGoVersion}, "module": {verifyCommand, lock, module},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			require.NoError(t, run(args, &out, baselineGit, allowedCPUs))
			switch name {
			case baselineCommand:
				require.Equal(t, "previous\n", out.String())
			case "version":
				require.Equal(t, "v1.75.2\n", out.String())
			case controlsCommand:
				require.JSONEq(t, `{"target":{"affinity":[1],"cgroup_root":"/cgroup","memory_root":"","version":"v2",
				"cpu_quota_us":100000,"cpu_period_us":100000,"memory_bytes":2147483648},"generator":{"affinity":[3],
				"cgroup_root":"/cgroup","memory_root":"","version":"v2","cpu_quota_us":100000,"cpu_period_us":100000,
				"memory_bytes":2147483648}}`, out.String())
			default:
				require.Empty(t, out.String())
			}
		})
	}
	for _, args := range [][]string{nil, {"unknown"}, {baselineCommand, "extra"}, {kernelCommand}, {verifyCommand, lock},
		{versionCommand, lock, "bad"}, {controlsCommand, ""}, {kernelCommand, root}, {verifyCommand, lock, root}} {
		require.Error(t, run(args, io.Discard, baselineGit, allowedCPUs))
	}
	require.Error(t, run([]string{baselineCommand}, io.Discard, func(...string) ([]byte, error) {
		return nil, errors.New("git unavailable")
	}, allowedCPUs))
	require.Error(t, run([]string{controlsCommand, "/cgroup"}, io.Discard, baselineGit, func([]int) ([]int, error) {
		return nil, errors.New("affinity unavailable")
	}))
	for _, args := range [][]string{{baselineCommand}, {controlsCommand, "/cgroup"}, {versionCommand, lock, testGoVersion}} {
		require.ErrorContains(t, run(args, failingWriter{}, baselineGit, allowedCPUs), "write failed")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestMain(t *testing.T) {
	if os.Getenv("MICROFAT_BENCHMARK_TOOLS_HELPER") == "1" {
		os.Args = []string{"benchmark-tools"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMain$")
	cmd.Env = append(os.Environ(), "MICROFAT_BENCHMARK_TOOLS_HELPER=1")
	output, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "usage: benchmark-tools")
}
