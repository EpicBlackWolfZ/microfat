//go:build linux

package system

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestTraceFaultBoundaries(t *testing.T) {
	t.Parallel()
	stopped := unix.WaitStatus(int(unix.SIGTRAP)<<8 | 0x7f)
	for _, name := range []string{"initial wait", "initial exit", "options", "resume", "wait disappeared", "wait failure",
		"continued failure", "nonzero exit", "signal"} {
		t.Run(name, func(t *testing.T) {
			waits, resumes, kills := 0, 0, 0
			calls := traceCalls{
				wait: func(pid int, status *unix.WaitStatus, _ int, _ *unix.Rusage) (int, error) {
					waits++
					*status = stopped
					if name == "initial wait" && waits == 1 || name == "wait failure" && waits == 2 {
						return 0, errors.New("injected wait error")
					}
					if name == "wait disappeared" && waits == 2 {
						return 0, unix.ECHILD
					}
					if name == "initial exit" {
						*status = 0
					}
					if name == "nonzero exit" && waits == 2 {
						*status = 1 << 8
					}
					if name == "signal" && waits == 2 {
						*status = unix.WaitStatus(unix.SIGKILL)
					}
					return pid, nil
				},
				options: func(int, int) error {
					if name == "options" {
						return errors.New("ptrace denied")
					}
					return nil
				},
				resume: func(int, int) error {
					resumes++
					if name == "resume" || name == "continued failure" && resumes == 2 {
						return errors.New("resume failed")
					}
					return nil
				},
				kill: func(int, unix.Signal) error { kills++; return nil },
			}
			result := observeTrace(context.Background(), 2147483647, time.Now(), ExecDiagnostic{Status: "unavailable"}, calls)
			assert.NotEqual(t, "observed", result.Status)
			assert.NotEmpty(t, result.Reason)
			if name == "initial wait" || name == "options" || name == "resume" || name == "continued failure" || name == "wait failure" {
				assert.Positive(t, kills)
			}
		})
	}
}

func TestExecBoundaryDiagnostic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := TraceExec(ctx, process.Spec{Path: "/bin/sh", Args: []string{"-c", "exec /bin/true"}})
	if result.Status == "unavailable" {
		t.Skipf("ptrace unavailable: %s", result.Reason)
	}
	assert.Equal(t, "observed", result.Status)
	require.GreaterOrEqual(t, len(result.Events), 2)
	assert.Contains(t, result.Qualification, "diagnostic only")
	result = TraceExec(ctx, process.Spec{Path: missingExecutable})
	assert.Equal(t, "unavailable", result.Status)
	short, stop := context.WithTimeout(context.Background(), traceInterval)
	defer stop()
	result = TraceExec(short, process.Spec{Path: "/bin/sleep", Args: []string{"1"}})
	assert.NotEmpty(t, result.Events)
}
