package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const processWaitDelay = 2 * time.Second

type processResult struct {
	Stats  processStats
	Stdout []byte
	Stderr []byte
}
type processRunner func(context.Context, string, []string, []string) processResult

type boundedCapture struct {
	prefix bytes.Buffer
	digest hash.Hash
	count  int64
}

func newCapture() *boundedCapture { return &boundedCapture{digest: sha256.New()} }

func (capture *boundedCapture) Write(data []byte) (int, error) {
	capture.count += int64(len(data))
	_, _ = capture.digest.Write(data)
	remaining := maximumOutput - capture.prefix.Len()
	if remaining > 0 {
		_, _ = capture.prefix.Write(data[:min(remaining, len(data))])
	}
	return len(data), nil
}

func execute(ctx context.Context, binary string, args, env []string) processResult {
	stdout, stderr := newCapture(), newCapture()
	// #nosec G204 -- executable and structured arguments come from the bounded, digest-verified research request.
	command := exec.CommandContext(ctx, binary, args...)
	command.Env, command.Stdout, command.Stderr = env, stdout, stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = processWaitDelay
	command.Cancel = func() error {
		return killProcessGroup(command.Process)
	}
	start := time.Now()
	err := command.Run()
	if err != nil && command.Process != nil {
		cleanupErr := killProcessGroup(command.Process)
		if cleanupErr != nil && !errors.Is(cleanupErr, os.ErrProcessDone) {
			err = errors.Join(err, cleanupErr)
		}
	}
	stats := processStats{WallNS: time.Since(start).Nanoseconds(), ExitCode: -1,
		TimedOut:     errors.Is(ctx.Err(), context.DeadlineExceeded),
		StdoutSHA256: hex.EncodeToString(stdout.digest.Sum(nil)), StderrSHA256: hex.EncodeToString(stderr.digest.Sum(nil)),
		StdoutBytes: stdout.count, StderrBytes: stderr.count}
	if err != nil {
		stats.Error = err.Error()
	}
	if state := command.ProcessState; state != nil {
		stats.ExitCode = state.ExitCode()
		stats.CPUNS = (state.UserTime() + state.SystemTime()).Nanoseconds()
		if usage, ok := state.SysUsage().(*syscall.Rusage); ok {
			stats.MaxRSSKiB, stats.InputBlocks, stats.OutputBlocks = usage.Maxrss, usage.Inblock, usage.Oublock
			stats.MinorFaults, stats.MajorFaults = usage.Minflt, usage.Majflt
		}
	}
	return processResult{Stats: stats, Stdout: stdout.prefix.Bytes(), Stderr: stderr.prefix.Bytes()}
}

func processEnvironment(cache, level string, processes int) []string {
	env := []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC", "MICROFAT_LOG=json"}
	return append(env, "MICROFAT_EXEC_MODE=auto", "MICROFAT_CACHE_DIR="+cache,
		"MICROFAT_FORCE_LEVEL="+level, "GOMAXPROCS="+strconv.Itoa(processes), "GOMEMLIMIT=off")
}

func killProcessGroup(process *os.Process) error {
	if process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func invoke(parent context.Context, timeout int64, runner processRunner, binary string, args, env []string) processResult {
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout))
	defer cancel()
	return runner(ctx, binary, args, env)
}

func hashFile(path string) (string, error) {
	file, err := openBounded(path, maximumExecutable)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(file, maximumExecutable+1))
	if err != nil {
		return "", err
	}
	if n > maximumExecutable {
		return "", errors.New("executable grew beyond size bound")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
