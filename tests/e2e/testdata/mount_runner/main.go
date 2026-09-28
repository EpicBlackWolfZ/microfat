// Mount runner is a disposable native qualification helper. Invoke it only
// as PID 1 inside private mount and PID namespaces; it is never installed with microfat.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"golang.org/x/sys/unix"
)

const (
	outputLimit        = 256 << 10
	requestLimit       = 1 << 20
	startupLimit       = 4096
	requestArgCount    = 2
	childFailureCode   = 125
	childTimeout       = 30 * time.Second
	outputDrainTimeout = 250 * time.Millisecond
)

// A noisy child must not turn a bounded qualification into unbounded memory use.
type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := outputLimit - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--child" {
		if err := child(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "mount-fixture child:", err)
			os.Exit(childFailureCode)
		}
		return
	}
	result := mountfixture.Result{Schema: 1, Stage: "request"}
	err := run(&result)
	if err != nil {
		result.Error = err.Error()
	}
	if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil {
		fmt.Fprintln(os.Stderr, encodeErr)
		os.Exit(1)
	}
	if err != nil {
		os.Exit(1)
	}
}

func child(args []string) error {
	if len(args) == 0 {
		return errors.New("missing child command")
	}
	// All capability changes and exec occur on the same OS thread. The wrapper
	// enters chroot before this point; the payload runs with ordinary credentials.
	runtime.LockOSThread()
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return err
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	data := [2]unix.CapUserData{}
	if err := unix.Capset(&header, &data[0]); err != nil {
		return err
	}
	command := args[0]
	if !strings.ContainsRune(command, '/') {
		var err error
		command, err = exec.LookPath(command)
		if err != nil {
			return err
		}
	}
	// #nosec G204 G702 -- intentional execution of a test-selected binary inside the disposable chroot, without a shell.
	return syscall.Exec(command, args, os.Environ())
}

func run(result *mountfixture.Result) error {
	if len(os.Args) != requestArgCount {
		return errors.New("usage: mount-runner request.json")
	}
	req, err := readRequest(os.Args[1])
	if err != nil {
		return err
	}
	result.Stage = "namespace"
	result.Namespace, err = os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		return err
	}
	if result.Namespace == req.ParentNamespace {
		return errors.New("refusing to mount in the parent namespace")
	}
	result.PIDNamespace, err = os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return err
	}
	if req.ParentPIDNamespace == "" || os.Getpid() != 1 || result.PIDNamespace == req.ParentPIDNamespace {
		return errors.New("fixture supervisor must be PID 1 in a new PID namespace")
	}
	if err := checkBinfmtNamespace(req, result); err != nil {
		return err
	}
	for path, target := range map[string]*string{"uid_map": &result.UIDMap, "gid_map": &result.GIDMap} {
		// #nosec G304 -- path is one of the two fixed namespace map names above.
		data, readErr := os.ReadFile("/proc/self/" + path)
		if readErr != nil {
			return readErr
		}
		*target = string(data)
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return err
	}
	result.Stage = "setup"
	if err := setup(req); err != nil {
		return err
	}
	if err := setupBinfmt(req, result); err != nil {
		return err
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		// Mountinfo escapes spaces, tabs, backslashes and newlines.
		escaped := strings.NewReplacer("\\", "\\134", " ", "\\040", "\t", "\\011", "\n", "\\012").Replace(req.Root)
		if strings.Contains(line, escaped) {
			result.MountInfo += line + "\n"
		}
	}
	result.Stage = "execution"
	if req.Binfmt != nil && req.Binfmt.Preflight {
		result.Stage = "complete"
		return nil
	}
	for range req.Runs {
		run, err := execute(req)
		result.Executions = append(result.Executions, run)
		if err != nil {
			return err
		}
	}
	result.Stage = "complete"
	return nil
}

func readRequest(path string) (mountfixture.Request, error) {
	var req mountfixture.Request
	// #nosec G304 G703 -- the E2E controller supplies this private fixture request, never product input.
	file, err := os.Open(path)
	if err != nil {
		return req, err
	}
	requestData, readErr := io.ReadAll(io.LimitReader(file, requestLimit+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return req, err
	}
	if len(requestData) > requestLimit {
		return req, errors.New("oversized fixture request")
	}
	decoder := json.NewDecoder(bytes.NewReader(requestData))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return req, errors.New("trailing fixture request data")
	}
	if err := validate(req); err != nil {
		return req, err
	}
	return req, nil
}

func validate(req mountfixture.Request) error {
	if !filepath.IsAbs(req.Root) || filepath.Clean(req.Root) != req.Root || req.Root == "/" {
		return errors.New("fixture root must be a clean absolute private directory")
	}
	info, err := os.Lstat(req.Root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || int(info.Sys().(*syscall.Stat_t).Uid) != req.UID {
		return errors.New("fixture root ownership or permissions mismatch")
	}
	if req.UID <= 0 || req.GID <= 0 || req.ParentNamespace == "" || req.Runs < 1 || req.Runs > 2 {
		return errors.New("ordinary UID/GID, namespace identity and one or two runs are required")
	}
	if req.Pause && req.Runs != 1 {
		return errors.New("mutation cases must run exactly once")
	}
	if req.Binfmt != nil && (req.Binfmt.ParentUserNamespace == "" || req.Pause) {
		return errors.New("binfmt requires a parent user namespace and cannot use ptrace mutation")
	}
	for _, mount := range req.Mounts {
		if _, err := sourcePath(req.Root, mount.Source); err != nil {
			return err
		}
		if !filepath.IsAbs(mount.Target) || filepath.Clean(mount.Target) != mount.Target || mount.Target == "/" {
			return errors.New("invalid fixture mount target")
		}
	}
	return validateChanges(req)
}

func validateChanges(req mountfixture.Request) error {
	for _, rename := range req.Renames {
		if _, err := sourcePath(req.Root, rename.From); err != nil {
			return err
		}
		if _, err := sourcePath(req.Root, rename.To); err != nil {
			return err
		}
	}
	for _, path := range req.Remove {
		if _, err := sourcePath(req.Root, path); err != nil {
			return err
		}
	}
	return nil
}

func sourcePath(root, relative string) (string, error) {
	if !filepath.IsLocal(relative) || relative == "." {
		return "", errors.New("fixture source must stay beneath its root")
	}
	return filepath.Join(root, relative), nil
}

func bind(source, target string, readOnly bool) error {
	if err := unix.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind %s: %w", target, err)
	}
	if readOnly {
		if err := unix.Mount("", target, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV, ""); err != nil {
			return fmt.Errorf("read-only bind %s: %w", target, err)
		}
	}
	return nil
}

func setup(req mountfixture.Request) error {
	jail := filepath.Join(req.Root, "rootfs")
	if err := bind(jail, jail, false); err != nil {
		return err
	}
	for _, mount := range req.Mounts {
		source, err := sourcePath(req.Root, mount.Source)
		if err != nil {
			return err
		}
		if err := bind(source, filepath.Join(jail, mount.Target), mount.ReadOnly); err != nil {
			return err
		}
	}
	proc := filepath.Join(jail, "proc")
	switch req.Proc {
	case "normal", "noexec":
		// Preserve locked submounts when entering a less privileged user
		// namespace. A shallow bind of procfs can be rejected with EINVAL.
		if err := unix.Mount("/proc", proc, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
			return fmt.Errorf("proc recursive bind: %w", err)
		}
		flags := uintptr(unix.MS_BIND | unix.MS_REMOUNT | unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC | unix.MS_RDONLY)
		// Host procfs normally already has noexec. Preserve that restriction in
		// both positive cases; record mountinfo rather than assuming its flags.
		if err := unix.Mount("", proc, "", flags, ""); err != nil {
			return fmt.Errorf("proc bind remount: %w", err)
		}
	case "missing":
	case "inaccessible":
		if err := os.Chmod(proc, 0); err != nil {
			return err
		}
	default:
		return errors.New("unknown proc fixture")
	}
	if req.ReadOnlyRoot {
		if err := unix.Mount("", jail, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
			return err
		}
	}
	return nil
}

func execute(req mountfixture.Request) (mountfixture.Execution, error) {
	r := mountfixture.Execution{ExitCode: -1}
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, childTimeout)
	defer cancel()
	reader, writer, err := os.Pipe()
	if err != nil {
		return r, err
	}
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	// #nosec G204 -- controlled test wrapper; chroot and ordinary credentials are set before execution.
	cmd := exec.CommandContext(ctx, "/control", append([]string{"--child", req.Command}, req.Args...)...)
	// chroot alone retains the old working directory; never leave a reference
	// to the host checkout as the payload's current directory.
	cmd.Dir = "/"
	cmd.Env = req.Env
	cmd.Stdin = strings.NewReader(req.Stdin)
	cmd.ExtraFiles = []*os.File{writer}
	// A descendant may retain the standard streams after the direct child is
	// killed. Finish draining promptly so PID 1 can exit and tear down the tree.
	cmd.WaitDelay = outputDrainTimeout
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: filepath.Join(req.Root, "rootfs"), Pdeathsig: syscall.SIGKILL, Ptrace: req.Pause}
	if os.Geteuid() == 0 {
		uid, gid := req.UID, req.GID
		if uid <= 0 || uid > math.MaxUint32 || gid <= 0 || gid > math.MaxUint32 {
			return r, errors.New("invalid child credentials")
		}
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{},
			NoSetGroups: req.Binfmt != nil}
	}
	var stdout, stderr boundedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if req.Pause {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}
	if err := cmd.Start(); err != nil {
		return r, err
	}
	r.PID = cmd.Process.Pid
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if err := writer.Close(); err != nil {
		return r, err
	}
	if req.Pause {
		if err := pauseTarget(cmd.Process.Pid); err != nil {
			return r, err
		}
		for _, change := range req.Renames {
			from, _ := sourcePath(req.Root, change.From)
			to, _ := sourcePath(req.Root, change.To)
			if err := os.Rename(from, to); err != nil {
				return r, err
			}
		}
		for _, path := range req.Remove {
			full, _ := sourcePath(req.Root, path)
			if err := os.RemoveAll(full); err != nil {
				return r, err
			}
		}
		if err := unix.PtraceDetach(cmd.Process.Pid); err != nil {
			return r, err
		}
	}
	err = cmd.Wait()
	r.ExitCode = cmd.ProcessState.ExitCode()
	if err != nil {
		r.Error = err.Error()
	}
	r.Stdout, r.Stderr = stdout.String(), stderr.String()
	if deadlineErr := reader.SetReadDeadline(time.Now().Add(outputDrainTimeout)); deadlineErr != nil {
		return r, deadlineErr
	}
	started, readErr := io.ReadAll(io.LimitReader(reader, startupLimit))
	r.Started = string(started)
	if errors.Is(err, exec.ErrWaitDelay) {
		return r, errors.Join(err, readErr, ctx.Err())
	}
	return r, errors.Join(readErr, ctx.Err())
}

func pauseTarget(pid int) error {
	var status unix.WaitStatus
	if _, err := unix.Wait4(pid, &status, 0, nil); err != nil {
		return err
	}
	if !status.Stopped() || status.StopSignal() != unix.SIGTRAP {
		return fmt.Errorf("wrapper did not stop at exec: %v", status)
	}
	if err := unix.PtraceSetOptions(pid, unix.PTRACE_O_TRACEEXEC|unix.PTRACE_O_EXITKILL); err != nil {
		return err
	}
	if err := unix.PtraceCont(pid, 0); err != nil {
		return err
	}
	for {
		if _, err := unix.Wait4(pid, &status, 0, nil); err != nil {
			return err
		}
		if !status.Stopped() {
			return fmt.Errorf("target did not reach exec barrier: %v", status)
		}
		if status.TrapCause() == unix.PTRACE_EVENT_EXEC {
			return nil
		}
		if err := unix.PtraceCont(pid, int(status.StopSignal())); err != nil {
			return err
		}
	}
}
