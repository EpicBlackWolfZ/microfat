package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const cmdBenchmark = "benchmark"

func TestBenchmarkCmd_JSONStreamHygiene(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer

	cmd.SetOut(&stdoutBuf)
	cmd.SetErr(&stderrBuf)
	cmd.SetArgs([]string{
		cmdBenchmark,
		"--json",
		"--trials", "1",
		"--trial-time", "20ms",
		"--warmup", "10ms",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("benchmark command failed: %v", err)
	}

	stdoutBytes := stdoutBuf.Bytes()
	if len(stdoutBytes) == 0 {
		t.Fatal("expected non-empty stdout")
	}

	// Stderr should receive the human logs, not stdout
	stderrOutput := stderrBuf.String()
	if !strings.Contains(stderrOutput, "[microfat]") {
		t.Errorf("expected stderr to contain progress logs, got: %s", stderrOutput)
	}

	// Stdout must NOT contain human readable logs
	stdoutOutput := string(stdoutBytes)
	if strings.Contains(stdoutOutput, "[microfat]") {
		t.Errorf("stdout polluted with log messages: %s", stdoutOutput)
	}

	// Stdout must parse as valid canonical Experiment JSON
	var exp schema.Experiment
	trimmedJSON := bytes.TrimSpace(stdoutBytes)
	if err := json.Unmarshal(trimmedJSON, &exp); err != nil {
		t.Fatalf("stdout JSON unmarshaling failed: %v\nOutput: %s", err, stdoutOutput)
	}

	if exp.SchemaVersion != schema.SchemaVersion {
		t.Errorf("expected schema version %s, got %s", schema.SchemaVersion, exp.SchemaVersion)
	}
	if len(exp.Scenarios) != 1 {
		t.Fatalf("expected 1 scenario, got %d", len(exp.Scenarios))
	}
	if exp.Scenarios[0].Workload != "baseline" {
		t.Errorf("expected workload baseline, got %s", exp.Scenarios[0].Workload)
	}

	// Verify digest match against canonical BuildEvidence
	ev, err := schema.BuildEvidence(&exp)
	if err != nil {
		t.Fatalf("BuildEvidence failed on unmarshaled experiment: %v", err)
	}

	h := sha256.Sum256(trimmedJSON)
	computedDigest := hex.EncodeToString(h[:])
	if computedDigest != ev.DigestSHA256 {
		t.Errorf("computed digest mismatch: expected %s, got %s", ev.DigestSHA256, computedDigest)
	}
	if !bytes.Equal(trimmedJSON, ev.PayloadBytes) {
		t.Error("stdout payload bytes do not match canonical payload bytes")
	}
	if err := schema.VerifyEvidence(ev); err != nil {
		t.Fatalf("evidence verification failed: %v", err)
	}
}

func TestBenchmarkCmd_HumanReadableOutputAndFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "custom_evidence.json")

	cmd := newRootCmd()
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer

	cmd.SetOut(&stdoutBuf)
	cmd.SetErr(&stderrBuf)
	cmd.SetArgs([]string{
		cmdBenchmark,
		"--trials", "1",
		"--trial-time", "20ms",
		"--warmup", "10ms",
		"-o", outputPath,
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("benchmark command failed: %v", err)
	}

	stdoutOutput := stdoutBuf.String()
	if !strings.Contains(stdoutOutput, "Benchmark Results") {
		t.Errorf("expected stdout to contain summary table header, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, "Evidence SHA-256:") {
		t.Errorf("expected stdout to contain Evidence SHA-256, got: %s", stdoutOutput)
	}
	if !strings.Contains(stdoutOutput, outputPath) {
		t.Errorf("expected stdout to contain evidence file path, got: %s", stdoutOutput)
	}

	// Verify output evidence file was written
	fileBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading output evidence file: %v", err)
	}

	evidence, err := schema.DeserializeEvidence(fileBytes)
	if err != nil {
		t.Fatalf("deserializing written evidence: %v", err)
	}

	if err := schema.VerifyEvidence(evidence); err != nil {
		t.Fatalf("verifying written evidence: %v", err)
	}
}

func TestBenchmarkCmd_DefaultResultsPath(t *testing.T) {
	tmpDir := t.TempDir()
	origResultsDir := defaultResultsDir
	defaultResultsDir = filepath.Join(tmpDir, "results/benchmarks")
	defer func() { defaultResultsDir = origResultsDir }()

	cmd := newRootCmd()
	var stdoutBuf bytes.Buffer
	cmd.SetOut(&stdoutBuf)
	cmd.SetErr(&stdoutBuf)
	cmd.SetArgs([]string{
		cmdBenchmark,
		"--trials", "1",
		"--trial-time", "10ms",
		"--warmup", "5ms",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("benchmark with default results path failed: %v", err)
	}

	stdoutStr := stdoutBuf.String()
	if !strings.Contains(stdoutStr, defaultResultsDir) {
		t.Errorf("expected default results dir in output, got: %s", stdoutStr)
	}

	entries, err := os.ReadDir(defaultResultsDir)
	if err != nil {
		t.Fatalf("reading default results dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 result file in default results dir, got %d", len(entries))
	}
	resultFilePath := filepath.Join(defaultResultsDir, entries[0].Name())
	data, err := os.ReadFile(resultFilePath)
	if err != nil {
		t.Fatalf("reading result file: %v", err)
	}
	ev, err := schema.DeserializeEvidence(data)
	if err != nil {
		t.Fatalf("deserializing default result evidence: %v", err)
	}
	if err := schema.VerifyEvidence(ev); err != nil {
		t.Fatalf("verifying default result evidence: %v", err)
	}
}

func TestBenchmarkCmd_ExecutionFailure(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{
		cmdBenchmark,
		"--trials", "0",
	})

	if err := cmd.Execute(); err == nil {
		t.Error("expected error on zero trials")
	}
}

func TestBenchmarkCmd_UnknownWorkload(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{
		cmdBenchmark,
		"--workload", "unknown_suite",
	})

	if err := cmd.Execute(); err == nil {
		t.Error("expected error on unknown workload")
	}
}


