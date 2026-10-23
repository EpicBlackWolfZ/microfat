package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

const workerConfigLimit = 16 << 10

type request struct {
	Mode        string
	Input       string
	Output      string
	Dictionary  string
	SHA256      string
	Bytes       int
	Threads     int
	Level       string
	Execute     bool
	Args        []string
	ReadyOutput string
}

type observation struct {
	MeasurementKind  string `json:"measurement_kind"`
	State            string `json:"state"`
	BlockID          string `json:"block_id"`
	InputIndex       int    `json:"input_index"`
	RequestedThreads int    `json:"requested_threads"`
	EffectiveThreads int    `json:"effective_threads"`
	GOMAXPROCS       int    `json:"gomaxprocs"`
	BurstSize        int    `json:"burst_size"`
	ReleaseNS        int64  `json:"release_ns"`
	CompletionNS     int64  `json:"completion_ns"`
	DurationNS       int64  `json:"duration_ns"`
	DecodeHashNS     int64  `json:"decode_hash_ns,omitempty"`
	ApplicationNS    int64  `json:"application_ns,omitempty"`
	ProcessWallNS    int64  `json:"process_wall_ns"`
	ProcessCPUNS     int64  `json:"process_cpu_ns"`
	MaxRSSKiB        int64  `json:"worker_max_rss_kib"`
	CompressedBytes  int    `json:"compressed_bytes"`
	CompressedSHA256 string `json:"compressed_sha256"`
	Failed           bool   `json:"failed"`
	Error            string `json:"error,omitempty"`
}

func worker(ctx context.Context, path string, gate io.Reader, out io.Writer, ready io.Writer) error {
	configData, err := readBounded(path, workerConfigLimit)
	if err != nil {
		return err
	}
	var r request
	if err := json.Unmarshal(configData, &r); err != nil {
		return err
	}
	if r.Bytes < 1 || r.Bytes > maxInputBytes || (r.Threads != -1 && (r.Threads < 1 || r.Threads > maxParallel)) {
		return errors.New("invalid worker bounds")
	}
	if _, err := ready.Write([]byte{1}); err != nil {
		return err
	}
	if _, err := io.ReadFull(gate, make([]byte, 1)); err != nil {
		return err
	}
	o := observation{RequestedThreads: r.Threads, EffectiveThreads: effective(r.Threads, r.Mode == modeDecode),
		GOMAXPROCS: runtime.GOMAXPROCS(0)}
	err = operate(ctx, r, &o)
	if err != nil {
		o.Failed = true
		o.Error = err.Error()
	}
	return json.NewEncoder(out).Encode(o)
}

func operate(ctx context.Context, r request, o *observation) error {
	limit := maxInputBytes
	if r.Mode == modeDecode {
		limit = maxFrameBytes
	}
	data, err := readBounded(r.Input, int64(limit))
	if err != nil {
		return err
	}
	var dict []byte
	if r.Dictionary != "" {
		dict, err = readBounded(r.Dictionary, maxDictBytes)
		if err != nil {
			return err
		}
	}
	if r.Mode == modeEncode {
		if len(data) != r.Bytes || digest(data) != r.SHA256 {
			return errors.New("encoder input changed")
		}
		start := time.Now()
		compressed, encodeErr := encode(data, dict, r.Level, r.Threads)
		o.DurationNS = time.Since(start).Nanoseconds()
		if encodeErr != nil {
			return encodeErr
		}
		o.CompressedBytes = len(compressed)
		o.CompressedSHA256 = digest(compressed)
		if _, _, err := decode(compressed, dict, r.Bytes, r.SHA256, 1); err != nil {
			return err
		}
		return os.WriteFile(r.Output, compressed, privateMode)
	}
	if r.Mode != modeDecode {
		return errors.New("unknown worker mode")
	}
	start := time.Now()
	decoded, duration, err := decode(data, dict, r.Bytes, r.SHA256, r.Threads)
	o.DecodeHashNS = duration
	o.DurationNS = time.Since(start).Nanoseconds()
	if err != nil {
		return err
	}
	if r.Execute {
		applicationStart := time.Now()
		err = execute(ctx, decoded, r)
		o.ApplicationNS = time.Since(applicationStart).Nanoseconds()
		o.DurationNS = time.Since(start).Nanoseconds()
	}
	return err
}

func execute(ctx context.Context, data []byte, r request) (result error) {
	dir, err := os.MkdirTemp("", "microfat-research375-ready-")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(dir)) }()
	path := filepath.Join(dir, "trusted-payload")
	if err := os.WriteFile(path, data, executableMode); err != nil { // #nosec G306,G703 -- private explicit native executable.
		return err
	}
	cmd := exec.CommandContext(ctx, path, r.Args...) // #nosec G204 -- explicit trusted corpus execution; no shell.
	stdout := &boundedBuffer{limit: maxOutputBytes}
	stderr := &boundedBuffer{limit: maxOutputBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("application readiness: %w: %s", err, stderr.String())
	}
	if stdout.String() != r.ReadyOutput {
		return errors.New("application readiness stdout mismatch")
	}
	return nil
}

func process(
	ctx context.Context, executable, path string, release <-chan struct{}, ready chan<- struct{}, batchStart time.Time,
) observation {
	notified := false
	defer func() {
		if !notified {
			ready <- struct{}{}
		}
	}()
	start := time.Now()
	var releaseTime time.Time
	cmd := exec.CommandContext(ctx, executable, "_worker", path) // #nosec G204 -- executable is this harness.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	stdout, stderr := &boundedBuffer{limit: workerConfigLimit}, &boundedBuffer{limit: workerConfigLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		return observation{Failed: true, Error: err.Error()}
	}
	defer func() { _ = readyReader.Close() }()
	cmd.ExtraFiles = []*os.File{readyWriter}
	reader, writer, err := os.Pipe()
	if err != nil {
		_ = readyWriter.Close()
		return observation{Failed: true, Error: err.Error()}
	}
	cmd.Stdin = reader
	err = cmd.Start()
	_ = reader.Close()
	_ = readyWriter.Close()
	if err == nil {
		_, err = io.ReadFull(readyReader, make([]byte, 1))
		ready <- struct{}{}
		notified = true
	}
	if err == nil {
		select {
		case <-release:
			releaseTime = time.Now()
			_, err = writer.Write([]byte{1})
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	_ = writer.Close()
	if cmd.Process != nil {
		err = errors.Join(err, cmd.Wait())
	}
	o := observation{}
	if err == nil {
		err = json.Unmarshal(stdout.Bytes(), &o)
	}
	if err != nil {
		o.Failed = true
		o.Error = fmt.Sprintf("%v: %s", err, stderr.String())
	}
	o.ProcessWallNS = time.Since(start).Nanoseconds()
	if !releaseTime.IsZero() {
		o.ReleaseNS = releaseTime.Sub(batchStart).Nanoseconds()
	}
	o.CompletionNS = time.Since(batchStart).Nanoseconds()
	if cmd.ProcessState != nil {
		o.ProcessCPUNS = (cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()).Nanoseconds()
		if usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			o.MaxRSSKiB = usage.Maxrss
		}
	}
	return o
}
