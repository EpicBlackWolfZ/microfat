package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestRuntimeRequestBoundaries(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateRuntime(mountfixture.Request{}))
	for _, tc := range []struct {
		name   string
		change func(*mountfixture.Request)
	}{
		{"zero-workers", func(r *mountfixture.Request) { r.Runtime.Workers = 0 }},
		{"too-many-workers", func(r *mountfixture.Request) { r.Runtime.Workers = maxWorkers + 1 }},
		{"ptrace", func(r *mountfixture.Request) { r.Pause = true }},
		{"qemu", func(r *mountfixture.Request) { r.Binfmt = &mountfixture.Binfmt{} }},
		{"policy", func(r *mountfixture.Request) { r.Runtime.Policy = "unknown" }},
		{"mount", func(r *mountfixture.Request) { r.Runtime.Cache = "host" }},
		{"writer-path", func(r *mountfixture.Request) { r.Runtime.BusyName = "../writer" }},
		{"traced-race", func(r *mountfixture.Request) { r.Runtime.Workers = 16; r.Runtime.Policy = "observe" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := mountfixture.Request{Runtime: &mountfixture.Runtime{Workers: 1}}
			tc.change(&r)
			require.Error(t, validateRuntime(r))
		})
	}
	require.NoError(t, validateRuntime(mountfixture.Request{Runtime: &mountfixture.Runtime{
		Workers: 16, Policy: "create-eperm", BusyName: strings.Repeat("a", 64)}}))
	require.NoError(t, requireNoSpace(unix.ENOSPC))
	require.Error(t, requireNoSpace(nil))
	require.Error(t, requireNoSpace(unix.EACCES))
	args, err := runtimeChild([]string{"/application"})
	require.NoError(t, err)
	require.Equal(t, []string{"/application"}, args)
	_, err = runtimeChild([]string{runtimePrefix})
	require.Error(t, err)
}

func TestRuntimeOutputOverflow(t *testing.T) {
	t.Parallel()
	var out boundedBuffer
	n, err := out.Write(make([]byte, outputLimit))
	require.NoError(t, err)
	require.Equal(t, outputLimit, n)
	require.False(t, out.truncated)
	n, err = out.Write([]byte("overflow"))
	require.NoError(t, err)
	require.Equal(t, len("overflow"), n)
	require.True(t, out.truncated)
	require.Equal(t, outputLimit, out.Len())
}

// Execute the small classic BPF program against syscall inputs. This checks the
// actual generated jumps, including unrelated fcntl commands and a wrong ABI.
func filterAction(t *testing.T, policy string, arch, number, command uint32) uint32 {
	t.Helper()
	program := runtimeFilter(policy)
	var accumulator uint32
	for pc := 0; pc < len(program); pc++ {
		instruction := program[pc]
		switch instruction.Code {
		case unix.BPF_LD | unix.BPF_W | unix.BPF_ABS:
			switch instruction.K {
			case 0:
				accumulator = number
			case seccompArchOffset:
				accumulator = arch
			case seccompCommandOffset:
				accumulator = command
			default:
				t.Fatal("unexpected load offset")
			}
		case unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K:
			if accumulator == instruction.K {
				pc += int(instruction.Jt)
			} else {
				pc += int(instruction.Jf)
			}
		case unix.BPF_RET | unix.BPF_K:
			return instruction.K
		default:
			t.Fatal("unexpected BPF instruction")
		}
	}
	t.Fatal("filter has no return")
	return 0
}

func TestArchitectureCheckedPolicies(t *testing.T) {
	t.Parallel()
	for _, policy := range []string{"create-eperm", "create-enosys", "seal", "exec-first", "exec-all", "observe", "fd-pressure"} {
		t.Run(policy, func(t *testing.T) {
			require.EqualValues(t, unix.SECCOMP_RET_KILL_PROCESS, filterAction(t, policy, 0, unix.SYS_WRITE, 0))
			require.EqualValues(t, unix.SECCOMP_RET_ALLOW, filterAction(t, policy, runtimeAuditArch(), unix.SYS_WRITE, 0))
		})
	}
	require.EqualValues(t, unix.SECCOMP_RET_ERRNO|uint32(unix.EPERM),
		filterAction(t, "create-eperm", runtimeAuditArch(), unix.SYS_MEMFD_CREATE, 0))
	require.EqualValues(t, unix.SECCOMP_RET_ERRNO|uint32(unix.ENOSYS),
		filterAction(t, "create-enosys", runtimeAuditArch(), unix.SYS_MEMFD_CREATE, 0))
	require.EqualValues(t, unix.SECCOMP_RET_ERRNO|uint32(unix.EPERM),
		filterAction(t, "seal", runtimeAuditArch(), unix.SYS_FCNTL, unix.F_ADD_SEALS))
	require.EqualValues(t, unix.SECCOMP_RET_ALLOW, filterAction(t, "seal", runtimeAuditArch(), unix.SYS_FCNTL, unix.F_GET_SEALS))
	require.EqualValues(t, unix.SECCOMP_RET_USER_NOTIF, filterAction(t, "observe", runtimeAuditArch(), unix.SYS_EXECVE, 0))
	require.EqualValues(t, unix.SECCOMP_RET_USER_NOTIF, filterAction(t, "observe", runtimeAuditArch(), unix.SYS_EXECVEAT, 0))
}

func TestNotificationRejectsUnexpectedInput(t *testing.T) {
	t.Parallel()
	n := notification{ID: 1, PID: 1, Data: notificationData{Arch: runtimeAuditArch(), Number: unix.SYS_EXECVE}}
	event, response, err := decideRuntimeNotification("observe", n, 0, os.Getpid())
	require.NoError(t, err)
	require.Equal(t, "bootstrap", event.Decision)
	require.EqualValues(t, unix.SECCOMP_USER_NOTIF_FLAG_CONTINUE, response.Flags)
	n.Data.Arch = 0
	_, _, err = decideRuntimeNotification("observe", n, 0, os.Getpid())
	require.Error(t, err)
	n.Data.Arch = runtimeAuditArch()
	n.Data.Number = unix.SYS_WRITE
	_, _, err = decideRuntimeNotification("observe", n, 0, os.Getpid())
	require.Error(t, err)
	_, _, err = decideRuntimeNotification("fd-pressure", n, 0, os.Getpid())
	require.Error(t, err)
	n.Data.Number = unix.SYS_MEMFD_CREATE
	_, _, err = decideRuntimeNotification("fd-pressure", n, 1, os.Getpid())
	require.Error(t, err)
	_, _, err = decideRuntimeNotification("fd-pressure", n, 0, -1)
	require.Error(t, err)
	n.Data.Args[0] = ^uint64(0)
	_, err = pinRuntimeTarget(n, &event, false)
	require.Error(t, err)
	n.Data.Args[0] = 0
	_, err = pinRuntimeTarget(n, &event, false)
	require.Error(t, err)
}

func TestListenerTransferAndLoss(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"valid", "missing", "bad-byte", "no-fd", "extra-fd", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
			require.NoError(t, err)
			defer unix.Close(pair[0])
			defer unix.Close(pair[1])
			file, err := os.Open("/dev/null")
			require.NoError(t, err)
			defer file.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			data := []byte{1}
			control := unix.UnixRights(int(file.Fd()))
			switch scenario {
			case "missing":
				require.NoError(t, unix.Shutdown(pair[1], unix.SHUT_RDWR))
			case "bad-byte":
				data[0] = 0
			case "no-fd":
				control = nil
			case "extra-fd":
				control = unix.UnixRights(int(file.Fd()), int(file.Fd()))
			case "cancelled":
				cancel()
			}
			if scenario != "missing" {
				require.NoError(t, unix.Sendmsg(pair[1], data, control, nil, 0))
			}
			fd, err := receiveRuntimeListener(ctx, pair[0])
			if scenario == "valid" {
				require.NoError(t, err)
				require.NoError(t, unix.Close(fd))
			} else {
				require.Error(t, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, superviseRuntime(ctx, "observe", -1, 0).err)
	require.NoError(t, superviseRuntime(ctx, "", -1, 0).err)
	_, err := pollPolicy(ctx, -1)
	require.Error(t, err)
	require.ErrorIs(t, notificationValid(-1, new(uint64)), unix.EBADF)
	require.ErrorIs(t, sendNotification(-1, &notificationResponse{}), unix.EBADF)
}

func TestCacheSnapshotReportsUnsafeObjectsWithoutFollowing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cache := filepath.Join(root, "rootfs", "cache")
	require.NoError(t, os.MkdirAll(cache, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(cache, "image"), []byte("bytes"), 0o700))
	require.NoError(t, os.Symlink("/missing", filepath.Join(cache, "link")))
	require.NoError(t, unix.Mkfifo(filepath.Join(cache, "fifo"), 0o700))
	snapshot, err := snapshotRuntimeCache(mountfixture.Request{Root: root}, 0)
	require.NoError(t, err)
	require.Len(t, snapshot.Entries, 3)
	require.NotZero(t, snapshot.Filesystem)
	require.Empty(t, snapshot.Entries[0].Digest)
	require.Len(t, snapshot.Entries[1].Digest, 64)
	require.Empty(t, snapshot.Entries[2].Digest)
	file, err := os.Open(cache)
	require.NoError(t, err)
	_, err = hashRuntimeFile(file)
	require.Error(t, err)
	require.NoError(t, file.Close())
	_, err = snapshotRuntimeCache(mountfixture.Request{Root: "/missing"}, 0)
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func TestLostPolicyListenerIsNotNormalHangup(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// Linux's maximum descriptor is below MaxInt32; this positive descriptor
	// reaches poll and produces POLLNVAL without risking descriptor reuse.
	ready, err := pollPolicy(ctx, math.MaxInt32)
	require.False(t, ready)
	require.ErrorContains(t, err, "policy listener failed")
}

func TestRuntimeTargetRemainsPinnedAfterTheChildClosesIt(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"cache", "sealed", "unsealed"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			var source *os.File
			if kind == "cache" {
				var err error
				source, err = os.CreateTemp(t.TempDir(), "image-")
				require.NoError(t, err)
			} else {
				fd, err := unix.MemfdCreate("microfat_payload", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
				require.NoError(t, err)
				source = os.NewFile(uintptr(fd), "image")
			}
			defer source.Close()
			payload := []byte("pinned image bytes")
			_, err := source.Write(payload)
			require.NoError(t, err)
			if kind == "sealed" {
				_, err := unix.FcntlInt(source.Fd(), unix.F_ADD_SEALS, mandatorySeals)
				require.NoError(t, err)
			}
			path := append([]byte(fmt.Sprintf("/proc/self/fd/%d", source.Fd())), 0)
			// #nosec G103 -- this live byte slice is read through this test process's procfs memory descriptor.
			address := uint64(uintptr(unsafe.Pointer(&path[0])))
			// #nosec G115 -- the operating system's positive PID fits the kernel notification field.
			notice := notification{PID: uint32(os.Getpid()), Data: notificationData{Number: unix.SYS_EXECVE, Args: [6]uint64{address}}}
			var event mountfixture.PolicyEvent
			pinned, err := pinRuntimeTarget(notice, &event, true)
			runtime.KeepAlive(path)
			if kind == "unsealed" {
				require.ErrorContains(t, err, "missing mandatory seals")
				require.Nil(t, pinned)
				return
			}
			require.NoError(t, err)
			defer pinned.Close()
			require.Equal(t, kind == "sealed", event.Immutable)
			require.Empty(t, event.Digest, "hashing must not delay the notification reply")
			require.NoError(t, source.Close())
			if kind == "cache" {
				require.NoError(t, os.Remove(source.Name()))
				require.NoError(t, os.WriteFile(source.Name(), []byte("replacement"), 0o600))
			}
			digest, err := hashRuntimeFile(pinned)
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(payload)), digest,
				"inspection must retain the notified inode after close, unlink and replacement")
		})
	}
}
