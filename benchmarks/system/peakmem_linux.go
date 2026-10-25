//go:build linux

package system

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
)

const (
	trampolineReportBytes  = 160
	trampolineErrnoBytes   = 8
	reportStatusOffset     = 8
	reportUtimeSecOffset   = 16
	reportUtimeUsecOffset  = 24
	reportStimeSecOffset   = 32
	reportStimeUsecOffset  = 40
	reportMaxRSSOffset     = 48
	reportMinFltOffset     = 80
	reportMajFltOffset     = 88
	reportVolCSWOffset     = 144
	reportInvolCSWOffset   = 152
	microsecondsToDuration = 1000
)

var (
	newIsolatedPipe   = os.Pipe
	killProcessGroup  = syscall.Kill
	maxIsolatedOutput = process.MaxOutput
)

// RunIsolated executes spec through trampolinePath so the target process forks from a 2-page static
// ELF rather than inheriting a high-RSS Go parent's VmHWM in Linux wait4(2) rusage accounting.
func RunIsolated(ctx context.Context, trampolinePath string, spec IsolatedSpec) (IsolatedResult, error) {
	if !filepath.IsAbs(trampolinePath) || !filepath.IsAbs(spec.Path) {
		return IsolatedResult{}, errors.New("trampoline and target paths must be absolute")
	}
	reportR, reportW, err := newIsolatedPipe()
	if err != nil {
		return IsolatedResult{}, err
	}
	execErrR, execErrW, err := newIsolatedPipe()
	if err != nil {
		return IsolatedResult{}, errors.Join(err, reportR.Close(), reportW.Close())
	}

	cmdArgs := append([]string{spec.Path}, spec.Args...)
	// #nosec G204 -- explicitly validated absolute benchmark trampoline and target executable.
	cmd := exec.CommandContext(ctx, trampolinePath, cmdArgs...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.WaitDelay = process.WaitDelay
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return cancelIsolatedProcess(cmd)
	}
	cmd.ExtraFiles = []*os.File{reportW, execErrW}

	var stdout, stderr limitedBuffer
	stdout.limit = maxIsolatedOutput
	stderr.limit = maxIsolatedOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return IsolatedResult{}, errors.Join(err, reportR.Close(), reportW.Close(), execErrR.Close(), execErrW.Close())
	}
	_ = reportW.Close()
	_ = execErrW.Close()

	reportBytes, reportReadErr := io.ReadAll(io.LimitReader(reportR, trampolineReportBytes+1))
	execErrBytes, errnoReadErr := io.ReadAll(io.LimitReader(execErrR, trampolineErrnoBytes+1))
	waitErr := cmd.Wait()
	wall := time.Since(start)
	if cmd.Process != nil && cmd.Process.Pid > 0 {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	closeErr := errors.Join(reportR.Close(), execErrR.Close(), reportReadErr, errnoReadErr, stdout.err, stderr.err)

	res := IsolatedResult{
		WallDuration: wall,
		Stdout:       stdout.buf.Bytes(),
		Stderr:       stderr.buf.Bytes(),
	}
	if len(spec.CgroupPaths) > 0 {
		if peak, source, peakErr := ReadCgroupPeakBytes(spec.CgroupPaths); peakErr == nil {
			res.CgroupPeak = &peak
			res.CgroupSource = source
		}
	}
	if closeErr != nil {
		return res, closeErr
	}
	if len(execErrBytes) == trampolineErrnoBytes {
		errnoVal := binary.LittleEndian.Uint64(execErrBytes)
		return res, fmt.Errorf("isolated child exec %s: %w", spec.Path, syscall.Errno(errnoVal))
	}
	if ctx.Err() != nil {
		return res, errors.Join(waitErr, ctx.Err())
	}
	if len(reportBytes) != trampolineReportBytes {
		return res, errors.Join(waitErr, fmt.Errorf("incomplete isolated wait4 report (%d bytes)", len(reportBytes)))
	}
	return decodeIsolatedReport(res, reportBytes)
}

func cancelIsolatedProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return nil
	}
	err := killProcessGroup(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func decodeIsolatedReport(res IsolatedResult, report []byte) (IsolatedResult, error) {
	le := binary.LittleEndian
	// #nosec G115 -- Linux PIDs and kernel rusage fields are signed 64-bit values serialized verbatim.
	res.PID = int(int64(le.Uint64(report[0:reportStatusOffset])))
	rawStatus := syscall.WaitStatus(le.Uint32(report[reportStatusOffset : reportStatusOffset+4]))
	utimeSec := int64(le.Uint64(report[reportUtimeSecOffset:reportUtimeUsecOffset]))      // #nosec G115 -- signed timeval tv_sec.
	utimeUsec := int64(le.Uint64(report[reportUtimeUsecOffset:reportStimeSecOffset]))     // #nosec G115 -- signed timeval tv_usec.
	stimeSec := int64(le.Uint64(report[reportStimeSecOffset:reportStimeUsecOffset]))      // #nosec G115 -- signed timeval tv_sec.
	stimeUsec := int64(le.Uint64(report[reportStimeUsecOffset:reportMaxRSSOffset]))       // #nosec G115 -- signed timeval tv_usec.
	maxRSSKiB := int64(le.Uint64(report[reportMaxRSSOffset : reportMaxRSSOffset+8]))      // #nosec G115 -- signed ru_maxrss (KiB).
	res.MinorFaults = int64(le.Uint64(report[reportMinFltOffset:reportMajFltOffset]))     // #nosec G115 -- signed ru_minflt.
	res.MajorFaults = int64(le.Uint64(report[reportMajFltOffset : reportMajFltOffset+8])) // #nosec G115 -- signed ru_majflt.
	res.VoluntaryCSW = int64(le.Uint64(report[reportVolCSWOffset:reportInvolCSWOffset]))  // #nosec G115 -- signed ru_nvcsw.
	res.InvolCSW = int64(le.Uint64(report[reportInvolCSWOffset:trampolineReportBytes]))   // #nosec G115 -- signed ru_nivcsw.

	res.UserTime = time.Duration(utimeSec)*time.Second + time.Duration(utimeUsec*microsecondsToDuration)
	res.SystemTime = time.Duration(stimeSec)*time.Second + time.Duration(stimeUsec*microsecondsToDuration)
	res.MaxRSSBytes = maxRSSKiB * kibibyte

	if rawStatus.Signaled() {
		res.Signal = int(rawStatus.Signal())
		res.ExitCode = -1
		return res, &IsolatedExitError{ExitCode: res.ExitCode, Signal: res.Signal}
	}
	res.ExitCode = rawStatus.ExitStatus()
	if res.ExitCode != 0 {
		return res, &IsolatedExitError{ExitCode: res.ExitCode}
	}
	return res, nil
}

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
	err   error
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buf.Len() {
		b.err = process.ErrOutputLimit
		return 0, process.ErrOutputLimit
	}
	return b.buf.Write(p)
}
