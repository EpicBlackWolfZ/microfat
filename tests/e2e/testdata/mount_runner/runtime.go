package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"golang.org/x/sys/unix"
)

const (
	maxWorkers      = 16
	batchTimeout    = time.Minute
	tmpfsBytes      = 16 << 20
	readyFD         = 4
	gateFD          = 5
	socketFD        = 6
	runtimePrefix   = "--runtime"
	privateDataMode = 0o600
)

func validateRuntime(req mountfixture.Request) error {
	if req.Runtime == nil {
		return nil
	}
	r := req.Runtime
	if req.Pause || req.Binfmt != nil || r.Workers < 1 || r.Workers > maxWorkers {
		return errors.New("runtime fixture requires 1..16 workers without ptrace or binfmt")
	}
	switch r.Policy {
	case "", "create-eperm", "create-enosys", "seal", "exec-first", "exec-all", "fd-pressure", "observe":
	default:
		return errors.New("unknown runtime policy")
	}
	switch r.Cache {
	case "", "readonly", "noexec", "full":
	default:
		return errors.New("unknown runtime cache")
	}
	if r.BusyName != "" && (filepath.Base(r.BusyName) != r.BusyName || len(r.BusyName) != sha256.Size*2) {
		return errors.New("busy cache name must be a SHA-256 filename")
	}
	if r.Workers > 1 && (r.Policy != "" && r.Policy != "create-eperm" && r.Policy != "create-enosys") {
		return errors.New("concurrent batches cannot use supervised policies")
	}
	return nil
}

func setupRuntime(req mountfixture.Request) error {
	if req.Runtime == nil {
		return nil
	}
	if req.Runtime.Capability {
		const capabilityBytes = 24
		const capabilityRootOffset = 20
		const capabilityRevision3Effective = 0x03000001
		data := make([]byte, capabilityBytes)
		binary.LittleEndian.PutUint32(data, capabilityRevision3Effective)
		binary.LittleEndian.PutUint32(data[4:], 1<<unix.CAP_NET_BIND_SERVICE)
		// #nosec G115 -- validate requires an ordinary UID within uint32 bounds.
		binary.LittleEndian.PutUint32(data[capabilityRootOffset:], uint32(req.UID))
		if err := unix.Setxattr(filepath.Join(req.Root, "rootfs", req.Command), "security.capability", data, 0); err != nil {
			return err
		}
	}
	cache := filepath.Join(req.Root, "rootfs/cache")
	flags := uintptr(unix.MS_BIND | unix.MS_REMOUNT | unix.MS_NOSUID | unix.MS_NODEV)
	switch req.Runtime.Cache {
	case "readonly":
		return unix.Mount("", cache, "", flags|unix.MS_RDONLY, "")
	case "noexec":
		return unix.Mount("", cache, "", flags|unix.MS_NOEXEC, "")
	case "full":
		if err := unix.Mount("tmpfs", cache, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV,
			fmt.Sprintf("size=%d,mode=0700,uid=%d,gid=%d", tmpfsBytes, req.UID, req.GID)); err != nil {
			return err
		}
		// #nosec G304 -- fixed name in the newly mounted bounded private tmpfs.
		file, err := os.OpenFile(filepath.Join(cache, ".space-reservation"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, privateDataMode)
		if err != nil {
			return err
		}
		// Only this newly mounted, size-limited tmpfs is filled. Never follow a host path.
		block := make([]byte, os.Getpagesize())
		for written := 0; written <= tmpfsBytes; written += len(block) {
			if _, err = file.Write(block); err != nil {
				break
			}
		}
		return errors.Join(file.Close(), requireNoSpace(err))
	}
	return nil
}

func requireNoSpace(err error) error {
	if errors.Is(err, unix.ENOSPC) {
		return nil
	}
	return fmt.Errorf("bounded tmpfs did not produce ENOSPC: %w", err)
}

// runtimeChild runs after ordinary credentials and capabilities are installed.
// All setup descriptors are closed before the final exec, except startup FD 3.
func runtimeChild(args []string) ([]string, error) {
	if args[0] != runtimePrefix {
		return args, nil
	}
	const prefixArgs = 2
	if len(args) <= prefixArgs {
		return nil, errors.New("missing runtime child arguments")
	}
	policy := args[1]
	if err := installRuntimePolicy(policy, socketFD); err != nil {
		return nil, err
	}
	if err := unix.Close(socketFD); err != nil {
		return nil, err
	}
	if _, err := unix.Write(readyFD, []byte{1}); err != nil {
		return nil, err
	}
	if err := unix.Close(readyFD); err != nil {
		return nil, err
	}
	var b [1]byte
	n, err := unix.Read(gateFD, b[:])
	if err != nil || n != 0 {
		return nil, errors.New("invalid runtime start barrier")
	}
	if err := unix.Close(gateFD); err != nil {
		return nil, err
	}
	return args[prefixArgs:], nil
}

type runtimeProcess struct {
	cmd                    *exec.Cmd
	startup, ready, socket *os.File
	stdout, stderr         boundedBuffer
	events                 chan policyResult
}

func (p *runtimeProcess) close() {
	for _, f := range []*os.File{p.startup, p.ready, p.socket} {
		if f != nil {
			_ = f.Close()
		}
	}
}

func startRuntimeProcess(ctx context.Context, req mountfixture.Request, gate *os.File) (*runtimeProcess, error) {
	p := &runtimeProcess{}
	startRead, startWrite, err := os.Pipe()
	if err != nil {
		return p, err
	}
	p.startup = startRead
	defer func() { _ = startWrite.Close() }()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return p, err
	}
	p.ready = readyRead
	defer func() { _ = readyWrite.Close() }()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return p, err
	}
	p.socket = os.NewFile(uintptr(pair[0]), "policy-parent")
	childSocket := os.NewFile(uintptr(pair[1]), "policy-child")
	defer func() { _ = childSocket.Close() }()
	args := append([]string{"--child", runtimePrefix, req.Runtime.Policy, req.Command}, req.Args...)
	// #nosec G204 -- fixed fixture wrapper in a private chroot, ordinary credentials.
	p.cmd = exec.CommandContext(ctx, "/control", args...)
	p.cmd.Dir, p.cmd.Env, p.cmd.Stdin = "/", req.Env, strings.NewReader(req.Stdin)
	p.cmd.Stdout, p.cmd.Stderr = &p.stdout, &p.stderr
	p.cmd.ExtraFiles = []*os.File{startWrite, readyWrite, gate, childSocket}
	p.cmd.WaitDelay = outputDrainTimeout
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: filepath.Join(req.Root, "rootfs"), Pdeathsig: syscall.SIGKILL}
	if os.Geteuid() == 0 {
		// Request validation has already constrained these IDs.
		// #nosec G115 -- positive validated ordinary IDs, supplied by the native controller.
		p.cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(req.UID), Gid: uint32(req.GID), Groups: []uint32{}}
	}
	if err := p.cmd.Start(); err != nil {
		return p, err
	}
	p.events = make(chan policyResult, 1)
	go func() { p.events <- superviseRuntime(ctx, req.Runtime.Policy, int(p.socket.Fd()), p.cmd.Process.Pid) }()
	return p, nil
}

func runtimeBatch(req mountfixture.Request, phase int) ([]mountfixture.Execution, error) {
	parent, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, batchTimeout)
	defer cancel()
	gateRead, gateWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer func() { _ = gateRead.Close(); _ = gateWrite.Close() }()
	var processes []*runtimeProcess
	defer func() {
		cancel()
		for _, p := range processes {
			if p.cmd != nil && p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
				_ = p.cmd.Wait()
			}
			p.close()
		}
	}()
	for range req.Runtime.Workers {
		p, err := startRuntimeProcess(ctx, req, gateRead)
		processes = append(processes, p)
		if err != nil {
			return nil, err
		}
	}
	for _, p := range processes {
		if err := p.ready.SetReadDeadline(time.Now().Add(childTimeout)); err != nil {
			return nil, err
		}
		var value [1]byte
		if _, err := io.ReadFull(p.ready, value[:]); err != nil {
			return nil, fmt.Errorf("worker not ready: %w", err)
		}
		if value[0] != 1 {
			return nil, errors.New("invalid readiness byte")
		}
	}
	if err := gateWrite.Close(); err != nil {
		return nil, err
	}
	type completed struct {
		execution mountfixture.Execution
		err       error
	}
	results := make(chan completed, len(processes))
	for _, p := range processes {
		go func() {
			run, err := finishRuntimeProcess(ctx, p, phase)
			results <- completed{run, err}
		}()
	}
	var executions []mountfixture.Execution
	var failures error
	for range processes {
		result := <-results
		executions = append(executions, result.execution)
		failures = errors.Join(failures, result.err)
	}
	return executions, failures
}

func finishRuntimeProcess(ctx context.Context, p *runtimeProcess, phase int) (mountfixture.Execution, error) {
	r := mountfixture.Execution{Phase: phase, PID: p.cmd.Process.Pid}
	startedAt := time.Now()
	timer := time.AfterFunc(childTimeout, func() { _ = p.cmd.Process.Kill() })
	err := p.cmd.Wait()
	timedOut := !timer.Stop()
	r.Duration, r.Timeout = time.Since(startedAt), timedOut
	r.ExitCode = p.cmd.ProcessState.ExitCode()
	if err != nil {
		r.Error = err.Error()
	}
	if state, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus); ok && state.Signaled() {
		r.Signal = state.Signal().String()
	}
	r.Stdout, r.Stderr, r.Truncated = p.stdout.String(), p.stderr.String(), p.stdout.truncated || p.stderr.truncated
	if err := p.startup.SetReadDeadline(time.Now().Add(outputDrainTimeout)); err != nil {
		return r, err
	}
	started, readErr := io.ReadAll(io.LimitReader(p.startup, startupLimit+1))
	r.Started = string(started)
	policy := <-p.events
	r.PolicyEvents = policy.events
	if timedOut {
		readErr = errors.Join(readErr, errors.New("runtime worker timed out"))
	}
	if r.Truncated || len(started) > startupLimit {
		readErr = errors.Join(readErr, errors.New("runtime output exceeded limit"))
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		readErr = errors.Join(readErr, err)
	}
	return r, errors.Join(readErr, policy.err, ctx.Err())
}

func runRuntime(req mountfixture.Request, result *mountfixture.Result) error {
	var writer *os.File
	if req.Runtime.BusyName != "" {
		var err error
		writer, err = os.OpenFile(filepath.Join(req.Root, "rootfs/cache", req.Runtime.BusyName), os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer func() { _ = writer.Close() }()
	}
	for phase := range req.Runs {
		runs, err := runtimeBatch(req, phase)
		result.Executions = append(result.Executions, runs...)
		snapshot, snapshotErr := snapshotRuntimeCache(req, phase)
		result.CacheSnapshots = append(result.CacheSnapshots, snapshot)
		if err := errors.Join(err, snapshotErr); err != nil {
			return err
		}
		if writer != nil {
			if err := writer.Close(); err != nil && phase == 0 {
				return err
			}
		}
	}
	result.Stage = "complete"
	return nil
}

func snapshotRuntimeCache(req mountfixture.Request, phase int) (mountfixture.CacheSnapshot, error) {
	result := mountfixture.CacheSnapshot{Phase: phase}
	path := filepath.Join(req.Root, "rootfs/cache")
	var fs unix.Statfs_t
	if err := unix.Statfs(path, &fs); err != nil {
		return result, err
	}
	result.Filesystem, result.Flags = fs.Type, fs.Flags
	entries, err := os.ReadDir(path)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		name := filepath.Join(path, entry.Name())
		var stat unix.Stat_t
		if err := unix.Lstat(name, &stat); err != nil {
			return result, err
		}
		item := mountfixture.CacheEntry{Name: entry.Name(), Mode: stat.Mode, UID: stat.Uid, Size: stat.Size}
		if stat.Mode&unix.S_IFMT == unix.S_IFREG && entry.Name() != ".space-reservation" {
			// #nosec G304 -- regular private cache fixture, after child execution completes.
			file, err := os.Open(name)
			if err != nil {
				return result, err
			}
			hash := sha256.New()
			_, copyErr := io.Copy(hash, file)
			if err := errors.Join(copyErr, file.Close()); err != nil {
				return result, err
			}
			item.Digest = fmt.Sprintf("%x", hash.Sum(nil))
		}
		result.Entries = append(result.Entries, item)
	}
	return result, nil
}

// Keep the architecture selection explicit in evidence and BPF construction.
func runtimeAuditArch() uint32 {
	if runtime.GOARCH == "arm64" {
		return unix.AUDIT_ARCH_AARCH64
	}
	return unix.AUDIT_ARCH_X86_64
}

func runtimeDescriptorPath(pid uint32, fd int) string {
	return "/proc/" + strconv.FormatUint(uint64(pid), 10) + "/fd/" + strconv.Itoa(fd)
}
