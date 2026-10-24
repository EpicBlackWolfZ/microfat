//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestExecutableMemfdRetryPolicy(t *testing.T) {
	const legacyFlags = unix.MFD_CLOEXEC | unix.MFD_ALLOW_SEALING
	for _, tc := range []struct {
		name  string
		err   error
		calls int
	}{
		{"current kernel", nil, 1}, {"old kernel", unix.EINVAL, 2},
		{"wrapped unsupported flag", errors.Join(unix.EINVAL), 2},
		{"scope 2 denial", unix.EACCES, 1}, {"seccomp denial", unix.EPERM, 1},
		{"missing syscall", unix.ENOSYS, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := memfdCreateFunc
			t.Cleanup(func() { memfdCreateFunc = old })
			calls := 0
			memfdCreateFunc = func(name string, flags int) (int, error) {
				calls++
				require.Equal(t, "microfat_payload", name)
				if calls == 1 {
					require.Equal(t, legacyFlags|unix.MFD_EXEC, flags)
					return -1, tc.err
				}
				require.Equal(t, legacyFlags, flags)
				return -1, unix.EMFILE
			}
			_, err := createExecutableMemfd()
			require.Equal(t, tc.calls, calls)
			if tc.calls == 2 {
				require.ErrorIs(t, err, unix.EMFILE, "legacy retry failure must propagate")
			} else {
				require.ErrorIs(t, err, tc.err)
			}
		})
	}
}

func TestExecutableMemfdRealDispatch(t *testing.T) {
	const helperEnv = "MICROFAT_TEST_MEMFD_POLICY"
	const inputEnv = "MICROFAT_TEST_MEMFD_INPUT"
	if policy := os.Getenv(helperEnv); policy != "" {
		// Emulate policy defaults/old flag support at the syscall boundary while
		// using real kernel descriptors, seals, verified bytes and execve. This
		// does not change or claim to exercise host vm.memfd_noexec sysctls.
		memfdCreateFunc = func(name string, flags int) (int, error) {
			if policy == "old" && flags&unix.MFD_EXEC != 0 {
				return -1, unix.EINVAL
			}
			if policy == "scope1" && flags&unix.MFD_EXEC == 0 {
				flags |= unix.MFD_NOEXEC_SEAL
			}
			return unix.MemfdCreate(name, flags)
		}
		readCgroupLimitsFunc = func() (cgroup.Limits, error) { return cgroup.Limits{}, nil }
		path := os.Getenv(inputEnv)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		f, err := os.Open(path)
		require.NoError(t, err)
		defer f.Close()
		hash := sha256.Sum256(data)
		entry := &format.VariantEntry{
			Compression: "none", CompressedSize: int64(len(data)), UncompressedSize: int64(len(data)), SHA256: hex.EncodeToString(hash[:]),
		}
		seal := memfdSealFunc
		memfdSealFunc = func(fd, seals int) error {
			require.Equal(t, memfdTargetSeals, seals)
			err := seal(fd, seals)
			require.NoError(t, err)
			actual, err := unix.FcntlInt(uintptr(fd), unix.F_GET_SEALS, 0)
			require.NoError(t, err)
			require.Equal(t, memfdTargetSeals, actual&memfdTargetSeals)
			return nil
		}
		err = executeViaMemfd(path, f, entry, nil, []string{"true"}, os.Environ(), microarch.Info{}, microarch.PolicyResult{}, time.Now())
		t.Fatalf("real exec must replace the helper process: %v", err)
	}
	data, err := os.ReadFile("/bin/true")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "payload")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	for _, policy := range []string{"scope0", "scope1", "old"} {
		t.Run(policy, func(t *testing.T) {
			t.Parallel()
			const timeout = 5 * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutableMemfdRealDispatch$")
			cmd.Env = append(os.Environ(), helperEnv+"="+policy, inputEnv+"="+path)
			output, err := cmd.CombinedOutput()
			require.NoError(t, ctx.Err())
			require.NoError(t, err, string(output))
		})
	}
}
