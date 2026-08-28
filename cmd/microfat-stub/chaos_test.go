//go:build !minimal

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

// createSyntheticFatFile creates a synthetic fat binary file with a dummy payload.
func createSyntheticFatFile(t *testing.T, payload []byte) (*os.File, *format.VariantEntry, *format.Index) {
	t.Helper()
	tempDir := t.TempDir()
	fatPath := filepath.Join(tempDir, "sample.fat")

	zCodec := codec.NewZstdCodec()
	var compBuf bytes.Buffer
	if err := zCodec.Compress(&compBuf, payload, "default"); err != nil {
		t.Fatalf("failed to compress payload: %v", err)
	}

	compBytes := compBuf.Bytes()
	payloadOffset := int64(64) // 64 bytes mock stub header

	h := sha256.Sum256(payload)
	shaHex := hex.EncodeToString(h[:])

	entry := format.VariantEntry{
		Level:            "v3",
		Offset:           payloadOffset,
		CompressedSize:   int64(len(compBytes)),
		UncompressedSize: int64(len(payload)),
		SHA256:           shaHex,
		Compression:      codec.AlgorithmZstd,
	}

	idx := &format.Index{
		Version:     format.FormatVersion2,
		AppName:     "chaos-app",
		TargetOS:    testOSLinux,
		TargetArch:  testArchAMD64,
		CreatedUnix: time.Now().Unix(),
		Variants:    []format.VariantEntry{entry},
	}

	var fatBuf bytes.Buffer
	fatBuf.Write(make([]byte, payloadOffset)) // Mock stub
	fatBuf.Write(compBytes)                   // Payload

	indexStart := int64(fatBuf.Len())
	if _, err := format.WriteIndexAndTrailerWithVersion(&fatBuf, idx, indexStart, format.FormatVersion2); err != nil {
		t.Fatalf("failed to write index and trailer: %v", err)
	}

	if err := os.WriteFile(fatPath, fatBuf.Bytes(), 0o755); err != nil {
		t.Fatalf("failed to write fat file: %v", err)
	}

	f, err := os.Open(fatPath)
	if err != nil {
		t.Fatalf("failed to open fat file: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	return f, &entry, idx
}

func TestConcurrentCacheRacingStress(t *testing.T) {

	payload := []byte("#!/bin/sh\necho 'microfat-concurrent-execution-payload'\n")
	fatFile, entry, idx := createSyntheticFatFile(t, payload)

	cacheDir := t.TempDir()
	origResolve := resolveCacheDirFunc
	origExecve := execveFunc
	t.Cleanup(func() {
		resolveCacheDirFunc = origResolve
		execveFunc = origExecve
	})

	resolveCacheDirFunc = func(string) (string, error) {
		return cacheDir, nil
	}

	var execCount int64
	var mu sync.Mutex
	expectedTarget := filepath.Join(cacheDir, entry.SHA256)
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		mu.Lock()
		execCount++
		mu.Unlock()
		if argv0 != expectedTarget {
			t.Errorf("execve target mismatch: got %q, want %q", argv0, expectedTarget)
		}
		return nil
	}

	const concurrentWorkers = 50
	var wg sync.WaitGroup
	errCh := make(chan error, concurrentWorkers)

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	for i := 0; i < concurrentWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			err := executeViaCache(fatFile, entry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, nil, time.Now())
			if err != nil {
				errCh <- fmt.Errorf("worker %d failed: %w", workerID, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent execution worker error: %v", err)
		}
	}

	if execCount != int64(concurrentWorkers) {
		t.Fatalf("expected %d executions, got %d", concurrentWorkers, execCount)
	}

	// Verify cached file exists, is valid, and matches payload exactly
	cachedFile := filepath.Join(cacheDir, entry.SHA256)
	content, err := os.ReadFile(cachedFile)
	if err != nil {
		t.Fatalf("failed to read cached binary: %v", err)
	}
	if !bytes.Equal(payload, content) {
		t.Fatalf("cached binary content mismatch: got %q, want %q", string(content), string(payload))
	}
}

func TestCorruptedCacheEvictionAndRecovery(t *testing.T) {

	payload := []byte("#!/bin/sh\necho 'microfat-recovery-test'\n")
	fatFile, entry, idx := createSyntheticFatFile(t, payload)

	cacheDir := t.TempDir()
	origResolve := resolveCacheDirFunc
	origExecve := execveFunc
	t.Cleanup(func() {
		resolveCacheDirFunc = origResolve
		execveFunc = origExecve
	})

	resolveCacheDirFunc = func(string) (string, error) {
		return cacheDir, nil
	}

	// 1. Plant a truncated/corrupt cache file with wrong size
	cachedFile := filepath.Join(cacheDir, entry.SHA256)
	if err := os.WriteFile(cachedFile, []byte("corrupt-short"), 0o755); err != nil {
		t.Fatalf("failed to plant corrupt cache: %v", err)
	}

	var executed bool
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		executed = true
		return nil
	}

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	// Execute should detect size mismatch, re-extract cleanly, and execute
	err := executeViaCache(fatFile, entry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, nil, time.Now())
	if err != nil {
		t.Fatalf("executeViaCache failed on corrupted cache recovery: %v", err)
	}
	if !executed {
		t.Fatal("expected execve to be called after recovery")
	}

	// Verify content was restored to real payload
	content, err := os.ReadFile(cachedFile)
	if err != nil {
		t.Fatalf("failed to read recovered cache file: %v", err)
	}
	if !bytes.Equal(payload, content) {
		t.Fatalf("recovered cache content mismatch: got %q, want %q", string(content), string(payload))
	}
}

func TestReadOnlyCacheDirectoryHandling(t *testing.T) {

	payload := []byte("#!/bin/sh\necho 'readonly-cache-test'\n")
	fatFile, entry, idx := createSyntheticFatFile(t, payload)

	origResolve := resolveCacheDirFunc
	t.Cleanup(func() {
		resolveCacheDirFunc = origResolve
	})

	resolveCacheDirFunc = func(string) (string, error) {
		return "", errors.New("permission denied")
	}

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	err := executeViaCache(fatFile, entry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, nil, time.Now())
	if err == nil {
		t.Fatal("expected error on read-only cache dir, got nil")
	}
	if !errors.Is(err, format.ErrCacheInit) {
		t.Fatalf("expected ErrCacheInit, got: %v", err)
	}
}

func TestSimulatedSeccompMemfdFallback(t *testing.T) {

	payload := []byte("#!/bin/sh\necho 'memfd-fallback-test'\n")
	fatFile, entry, idx := createSyntheticFatFile(t, payload)

	cacheDir := t.TempDir()
	origResolve := resolveCacheDirFunc
	origMemfd := memfdCreateFunc
	origExecve := execveFunc
	t.Cleanup(func() {
		resolveCacheDirFunc = origResolve
		memfdCreateFunc = origMemfd
		execveFunc = origExecve
	})

	resolveCacheDirFunc = func(string) (string, error) {
		return cacheDir, nil
	}

	// Simulate strict seccomp filter blocking memfd_create with EPERM
	memfdCreateFunc = func(name string, flags int) (int, error) {
		return -1, syscall.EPERM
	}

	var cacheExecuted bool
	expectedTarget := filepath.Join(cacheDir, entry.SHA256)
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		if argv0 == expectedTarget {
			cacheExecuted = true
			return nil
		}
		return errors.New("unexpected exec path: " + argv0)
	}

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	// executeVariant should try memfd, encounter EPERM, and seamlessly fall back to cache
	err := executeVariant(fatFile, entry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, time.Now())
	if err != nil {
		t.Fatalf("executeVariant failed on seccomp fallback: %v", err)
	}
	if !cacheExecuted {
		t.Fatal("expected cache execution fallback when memfd_create is blocked")
	}
}

func TestMaliciousPathTraversalChecksumBlocked(t *testing.T) {

	payload := []byte("#!/bin/sh\necho 'path-traversal-test'\n")
	fatFile, _, idx := createSyntheticFatFile(t, payload)

	cacheDir := t.TempDir()
	origResolve := resolveCacheDirFunc
	t.Cleanup(func() {
		resolveCacheDirFunc = origResolve
	})

	resolveCacheDirFunc = func(string) (string, error) {
		return cacheDir, nil
	}

	maliciousEntry := &format.VariantEntry{
		Level:            "v3",
		Offset:           64,
		CompressedSize:   100,
		UncompressedSize: int64(len(payload)),
		SHA256:           "../../../../tmp/malicious_payload",
		Compression:      codec.AlgorithmZstd,
	}

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	err := executeViaCache(fatFile, maliciousEntry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, nil, time.Now())
	if err == nil {
		t.Fatal("expected error on malicious path traversal checksum, got nil")
	}
	if !errors.Is(err, format.ErrCacheWrite) {
		t.Fatalf("expected ErrCacheWrite, got: %v", err)
	}
}
