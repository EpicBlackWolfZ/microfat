//go:build linux

package system

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"golang.org/x/sys/unix"
)

const (
	traceInterval = time.Millisecond
	traceDuration = 2 * time.Second
)

// TraceExec is a separate diagnostic pass; ptrace-disturbed timing never enters performance trials.
func TraceExec(ctx context.Context, spec process.Spec) ExecDiagnostic {
	result := ExecDiagnostic{Status: "unavailable", Qualification: "ptrace diagnostic only; sampled maxima can miss peaks; " +
		"exec-event RSS is cumulative through that boundary, not an allocation attribution or a difference of peaks"}
	ctx, cancel := context.WithTimeout(ctx, traceDuration)
	defer cancel()
	// Linux tracer relationship belongs to a specific OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cmd := exec.Command(spec.Path, spec.Args...) // #nosec G204 -- approved benchmark artifact, diagnostic execution.
	cmd.Env, cmd.Dir = spec.Env, spec.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Ptrace: true, Setpgid: true}
	// Child inherits /dev/null streams, so no pipe readers or unbounded log buffers are created.
	if err := cmd.Start(); err != nil {
		result.Reason = err.Error()
		return result
	}
	pid := cmd.Process.Pid
	defer func() { _ = cmd.Process.Release() }()
	start := time.Now()
	return observeTrace(ctx, pid, start, result, traceCalls{unix.Wait4, unix.PtraceSetOptions, unix.PtraceCont, unix.Kill})
}

// Syscalls are injected at this boundary for deterministic permission, disappearance and cleanup tests.
type traceCalls struct {
	wait    func(int, *unix.WaitStatus, int, *unix.Rusage) (int, error)
	options func(int, int) error
	resume  func(int, int) error
	kill    func(int, unix.Signal) error
}

func observeTrace(ctx context.Context, pid int, start time.Time, result ExecDiagnostic, calls traceCalls) ExecDiagnostic {
	var status unix.WaitStatus
	var usage unix.Rusage
	if _, err := calls.wait(pid, &status, 0, &usage); err != nil {
		result.Reason = err.Error()
		_ = calls.kill(-pid, unix.SIGKILL)
		_, _ = calls.wait(pid, &status, 0, nil)
		return result
	}
	if !status.Stopped() {
		result.Reason = "ptrace child did not stop at executable entry"
		return result
	}
	if err := calls.options(pid, unix.PTRACE_O_TRACEEXEC|unix.PTRACE_O_EXITKILL); err != nil {
		result.Reason = err.Error()
		_ = calls.kill(-pid, unix.SIGKILL)
		_, _ = calls.wait(pid, &status, 0, nil)
		return result
	}
	result.Events = append(result.Events, execEvent(pid, start, usage))
	if err := calls.resume(pid, 0); err != nil {
		result.Reason = err.Error()
		_ = calls.kill(-pid, unix.SIGKILL)
		_, _ = calls.wait(pid, &status, 0, nil)
		return result
	}
	return traceLoop(ctx, pid, start, result, calls)
}

func execEvent(pid int, start time.Time, usage unix.Rusage) ExecEvent {
	executable, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		executable = "unavailable"
	}
	var peak *int64
	if usage.Maxrss > 0 {
		value := usage.Maxrss * kibibyte
		peak = &value
	}
	return ExecEvent{ElapsedNS: time.Since(start).Nanoseconds(), Executable: executable, PeakRSSBytes: peak}
}

func traceLoop(ctx context.Context, pid int, start time.Time, result ExecDiagnostic, calls traceCalls) ExecDiagnostic {
	ticker := time.NewTicker(traceInterval)
	defer ticker.Stop()
	result.Status = "observed"
	for {
		var status unix.WaitStatus
		var usage unix.Rusage
		waited, err := calls.wait(pid, &status, unix.WNOHANG, &usage)
		if err != nil {
			result.Status, result.Reason = "unavailable", err.Error()
			if !errors.Is(err, unix.ECHILD) {
				_ = calls.kill(-pid, unix.SIGKILL)
				_, _ = calls.wait(pid, &status, 0, nil)
			}
			return result
		}
		if waited == pid {
			if status.Exited() || status.Signaled() {
				if status.Signaled() || status.ExitStatus() != 0 {
					result.Status, result.Reason = "failed", "diagnostic child exited unsuccessfully"
				}
				return result
			}
			if status.Stopped() {
				signal := int(status.StopSignal())
				if status.TrapCause() == unix.PTRACE_EVENT_EXEC {
					result.Events = append(result.Events, execEvent(pid, start, usage))
					signal = 0
				} else if status.StopSignal() == unix.SIGTRAP {
					signal = 0
				}
				if err := calls.resume(pid, signal); err != nil {
					result.Status, result.Reason = "unavailable", err.Error()
					_ = calls.kill(-pid, unix.SIGKILL)
					_, _ = calls.wait(pid, &status, 0, nil)
					return result
				}
			}
		}
		phase := "exec-stage-" + strconv.Itoa(len(result.Events))
		result.Samples = append(result.Samples, schema.ResourceSample{ElapsedNS: time.Since(start).Nanoseconds(), Phase: phase,
			Metrics: ReadProcess("/proc", pid, phase)})
		select {
		case <-ctx.Done():
			_ = calls.kill(-pid, unix.SIGKILL)
			_, waitErr := calls.wait(pid, &status, 0, nil)
			if waitErr != nil && !errors.Is(waitErr, unix.ECHILD) {
				result.Reason = waitErr.Error()
			}
			return result
		case <-ticker.C:
		}
	}
}
