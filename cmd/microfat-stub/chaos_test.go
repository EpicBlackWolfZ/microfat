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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"golang.org/x/sys/unix"
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
		target := argv0
		if link, err := os.Readlink(argv0); err == nil {
			target = link
		}
		if target != expectedTarget {
			t.Errorf("execve target mismatch: got %q, want %q", target, expectedTarget)
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
		target := argv0
		if link, err := os.Readlink(argv0); err == nil {
			target = link
		}
		if target == expectedTarget {
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

func TestDecompressionBombPayloadBlocked(t *testing.T) {
	const (
		bombPayloadBytes  = 1024 * 1024 // 1 MB
		declaredFakeBytes = 256         // 256 bytes declared
	)

	largePayload := bytes.Repeat([]byte("MICROFAT_HOSTILE_DECOMPRESSION_BOMB_PAYLOAD_TEST_0123456789\n"), bombPayloadBytes/60)

	fatFile, _, idx := createSyntheticFatFile(t, largePayload)

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

	var execveCalled bool
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		execveCalled = true
		return nil
	}

	// Craft hostile entry with declared fake size much smaller than true payload
	h := sha256.Sum256(largePayload)
	hostileEntry := &format.VariantEntry{
		Level:            "v3",
		Offset:           64,
		CompressedSize:   idx.Variants[0].CompressedSize,
		UncompressedSize: declaredFakeBytes,
		SHA256:           hex.EncodeToString(h[:]),
		Compression:      codec.AlgorithmZstd,
	}

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	t.Run("Blocked on memfd path", func(t *testing.T) {
		execveCalled = false
		err := executeViaMemfd(fatFile, hostileEntry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, time.Now())
		if err == nil {
			t.Fatal("expected error decompressing bomb payload into memfd, got nil")
		}
		if !errors.Is(err, codec.ErrSizeMismatch) {
			t.Fatalf("expected ErrSizeMismatch, got %v", err)
		}
		if execveCalled {
			t.Fatal("execve was called despite decompression bomb error")
		}
	})

	t.Run("Blocked on cache path", func(t *testing.T) {
		execveCalled = false
		err := executeViaCache(fatFile, hostileEntry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, nil, time.Now())
		if err == nil {
			t.Fatal("expected error decompressing bomb payload into cache, got nil")
		}
		if !errors.Is(err, format.ErrCacheExtract) || !errors.Is(err, codec.ErrSizeMismatch) {
			t.Fatalf("expected ErrCacheExtract wrapping ErrSizeMismatch, got %v", err)
		}
		if execveCalled {
			t.Fatal("execve was called despite cache decompression error")
		}
	})
}

func TestPayloadChecksumMismatchAbort(t *testing.T) {
	payload := []byte("#!/bin/sh\necho 'microfat-payload-hash-integrity-test'\n")
	fatFile, entry, idx := createSyntheticFatFile(t, payload)

	cacheDir := t.TempDir()
	origResolve := resolveCacheDirFunc
	origExecve := execveFunc
	origMemfd := memfdCreateFunc
	t.Cleanup(func() {
		resolveCacheDirFunc = origResolve
		execveFunc = origExecve
		memfdCreateFunc = origMemfd
	})

	resolveCacheDirFunc = func(string) (string, error) {
		return cacheDir, nil
	}

	var execveCalled bool
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		execveCalled = true
		return nil
	}

	// Tampered entry with mismatched SHA256 checksum
	fakeSHA := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tamperedEntry := &format.VariantEntry{
		Level:            entry.Level,
		Offset:           entry.Offset,
		CompressedSize:   entry.CompressedSize,
		UncompressedSize: entry.UncompressedSize,
		SHA256:           fakeSHA,
		Compression:      entry.Compression,
	}

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	t.Run("Mismatched hash fails in extractVariantToWriter", func(t *testing.T) {
		var out bytes.Buffer
		err := extractVariantToWriter(fatFile, tamperedEntry, idx, &out)
		if err == nil {
			t.Fatal("expected error from extractVariantToWriter on tampered hash, got nil")
		}
		if !errors.Is(err, format.ErrPayloadCorrupted) {
			t.Fatalf("expected ErrPayloadCorrupted, got: %v", err)
		}
	})

	t.Run("Blocked on memfd path", func(t *testing.T) {
		execveCalled = false
		err := executeViaMemfd(fatFile, tamperedEntry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, time.Now())
		if err == nil {
			t.Fatal("expected error executing tampered entry via memfd, got nil")
		}
		if !errors.Is(err, format.ErrPayloadCorrupted) {
			t.Fatalf("expected ErrPayloadCorrupted, got: %v", err)
		}
		if execveCalled {
			t.Fatal("execve was called despite payload checksum mismatch on memfd path")
		}
	})

	t.Run("Blocked on cache path", func(t *testing.T) {
		execveCalled = false
		err := executeViaCache(fatFile, tamperedEntry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, nil, time.Now())
		if err == nil {
			t.Fatal("expected error executing tampered entry via cache, got nil")
		}
		if !errors.Is(err, format.ErrCacheExtract) || !errors.Is(err, format.ErrPayloadCorrupted) {
			t.Fatalf("expected ErrCacheExtract wrapping ErrPayloadCorrupted, got: %v", err)
		}
		if execveCalled {
			t.Fatal("execve was called despite payload checksum mismatch on cache path")
		}
	})

	t.Run("executeVariant aborts fast without attempting cache fallback", func(t *testing.T) {
		execveCalled = false
		err := executeVariant(fatFile, tamperedEntry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, time.Now())
		if err == nil {
			t.Fatal("expected error executing tampered entry via executeVariant, got nil")
		}
		if !errors.Is(err, format.ErrPayloadCorrupted) {
			t.Fatalf("expected ErrPayloadCorrupted, got: %v", err)
		}
		if execveCalled {
			t.Fatal("execve was called despite payload checksum mismatch in executeVariant")
		}
	})
}

func TestWarmCacheVerifyOption(t *testing.T) {
	payload := []byte("#!/bin/sh\necho 'microfat-warm-cache-verify-payload'\n")
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

	cachedBinary := filepath.Join(cacheDir, entry.SHA256)

	// 1. Plant a modified/tampered file with matching size but different content
	tamperedContent := bytes.Repeat([]byte("X"), len(payload))
	if err := os.WriteFile(cachedBinary, tamperedContent, 0o755); err != nil {
		t.Fatalf("failed to plant tampered cache file: %v", err)
	}

	var executedPath string
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		if link, err := os.Readlink(argv0); err == nil {
			executedPath = link
		} else {
			executedPath = argv0
		}
		return nil
	}

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	t.Run("Warm cache hit without verification uses existing disk file", func(t *testing.T) {
		t.Setenv(format.EnvVerifyCache, "0")
		executedPath = ""
		err := executeViaCache(fatFile, entry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, nil, time.Now())
		if err != nil {
			t.Fatalf("executeViaCache failed: %v", err)
		}
		if executedPath != cachedBinary {
			t.Fatalf("expected execution of %q, got %q", cachedBinary, executedPath)
		}
		diskBytes, _ := os.ReadFile(cachedBinary)
		if !bytes.Equal(diskBytes, tamperedContent) {
			t.Fatalf("expected tampered file to remain untouched when verification is disabled")
		}
	})

	t.Run("Warm cache hit with MICROFAT_VERIFY_CACHE=1 invalidates and re-extracts", func(t *testing.T) {
		t.Setenv(format.EnvVerifyCache, "1")
		t.Setenv(format.EnvDebug, "1")
		executedPath = ""

		err := executeViaCache(fatFile, entry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, nil, time.Now())
		if err != nil {
			t.Fatalf("executeViaCache with verify failed: %v", err)
		}
		if executedPath != cachedBinary {
			t.Fatalf("expected execution of %q, got %q", cachedBinary, executedPath)
		}

		// Check that the corrupted cache file was replaced with the real payload
		diskBytes, err := os.ReadFile(cachedBinary)
		if err != nil {
			t.Fatalf("failed to read cached binary after re-extraction: %v", err)
		}
		if !bytes.Equal(diskBytes, payload) {
			t.Fatalf("cached binary content mismatch: got %q, want %q", string(diskBytes), string(payload))
		}
	})
}

func TestOversizedDictionaryChaos(t *testing.T) {
	payload := []byte("#!/bin/sh\necho 'microfat-oversized-dict-payload'\n")
	fatFile, entry, idx := createSyntheticFatFile(t, payload)

	var execveCalled bool
	origExecve := execveFunc
	t.Cleanup(func() {
		execveFunc = origExecve
	})
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		execveCalled = true
		return nil
	}

	oversizedIdx := *idx
	oversizedIdx.DictionarySize = format.MaxDictionarySize + 1024
	oversizedIdx.DictionaryOffset = 0

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	t.Run("Oversized dictionary fails in extractVariantToWriter", func(t *testing.T) {
		var out bytes.Buffer
		err := extractVariantToWriter(fatFile, entry, &oversizedIdx, &out)
		if err == nil {
			t.Fatal("expected error from extractVariantToWriter on oversized dictionary, got nil")
		}
		if !errors.Is(err, format.ErrInvalidDictionary) {
			t.Fatalf("expected ErrInvalidDictionary, got: %v", err)
		}
	})

	t.Run("Blocked on memfd path", func(t *testing.T) {
		execveCalled = false
		err := executeViaMemfd(fatFile, entry, &oversizedIdx, []string{testAppArg}, []string{}, hostInfo, policyRes, time.Now())
		if err == nil {
			t.Fatal("expected error executing oversized dictionary entry via memfd, got nil")
		}
		if !errors.Is(err, format.ErrInvalidDictionary) {
			t.Fatalf("expected ErrInvalidDictionary, got: %v", err)
		}
		if execveCalled {
			t.Fatal("execve was called despite oversized dictionary size on memfd path")
		}
	})

	t.Run("Blocked on cache path", func(t *testing.T) {
		execveCalled = false
		err := executeViaCache(fatFile, entry, &oversizedIdx, []string{testAppArg}, []string{}, hostInfo, policyRes, nil, time.Now())
		if err == nil {
			t.Fatal("expected error executing oversized dictionary entry via cache, got nil")
		}
		if !errors.Is(err, format.ErrCacheExtract) || !errors.Is(err, format.ErrInvalidDictionary) {
			t.Fatalf("expected ErrCacheExtract wrapping ErrInvalidDictionary, got: %v", err)
		}
		if execveCalled {
			t.Fatal("execve was called despite oversized dictionary size on cache path")
		}
	})
}

func TestMemfdSealingVerificationAndImmutability(t *testing.T) {
	payload := []byte("#!/bin/sh\necho 'memfd-sealing-test'\n")
	fatFile, entry, idx := createSyntheticFatFile(t, payload)

	origExecve := execveFunc
	origSeal := memfdSealFunc
	t.Cleanup(func() {
		execveFunc = origExecve
		memfdSealFunc = origSeal
	})

	var sealedFD int
	var execveCalled bool
	var actualSeals int
	var writeErr error

	execveFunc = func(argv0 string, argv []string, envv []string) error {
		execveCalled = true
		// argv0 is /proc/self/fd/<fd>
		parts := strings.Split(argv0, "/")
		if len(parts) > 0 {
			fdNum, err := strconv.Atoi(parts[len(parts)-1])
			if err == nil {
				sealedFD = fdNum
				// Query active seals on fd
				seals, getErr := unix.FcntlInt(uintptr(sealedFD), unix.F_GET_SEALS, 0)
				if getErr == nil {
					actualSeals = seals
				}
				// Attempt to write to sealed memfd
				_, writeErr = unix.Write(sealedFD, []byte("tampered"))
			}
		}
		return nil
	}

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	err := executeViaMemfd(fatFile, entry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, time.Now())
	if err != nil {
		t.Fatalf("executeViaMemfd failed: %v", err)
	}
	if !execveCalled {
		t.Fatal("expected execve to be called")
	}

	expectedSeals := unix.F_SEAL_WRITE | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_SEAL
	if actualSeals != expectedSeals {
		t.Errorf("active seals mismatch: got %d, want %d", actualSeals, expectedSeals)
	}

	if writeErr == nil {
		t.Error("expected write to sealed memfd to fail with EPERM/EBUSY, but it succeeded")
	} else if !errors.Is(writeErr, syscall.EPERM) && !errors.Is(writeErr, syscall.EBUSY) {
		t.Errorf("expected write error to be EPERM or EBUSY, got: %v", writeErr)
	}
}

func TestMemfdSealingGracefulFallback(t *testing.T) {
	payload := []byte("#!/bin/sh\necho 'memfd-seal-fallback-test'\n")
	fatFile, entry, idx := createSyntheticFatFile(t, payload)

	cacheDir := t.TempDir()
	origResolve := resolveCacheDirFunc
	origExecve := execveFunc
	origSeal := memfdSealFunc
	t.Cleanup(func() {
		resolveCacheDirFunc = origResolve
		execveFunc = origExecve
		memfdSealFunc = origSeal
	})

	resolveCacheDirFunc = func(string) (string, error) {
		return cacheDir, nil
	}

	testCases := []struct {
		name    string
		sealErr error
	}{
		{name: "ENOSYS", sealErr: syscall.ENOSYS},
		{name: "EINVAL", sealErr: syscall.EINVAL},
		{name: "EPERM", sealErr: syscall.EPERM},
	}

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	for _, tc := range testCases {
		t.Run(tc.name+"_executeViaMemfd_fails_fast", func(t *testing.T) {
			var execveCalled bool
			execveFunc = func(argv0 string, argv []string, envv []string) error {
				execveCalled = true
				return nil
			}
			memfdSealFunc = func(fd int, seals int) error {
				return tc.sealErr
			}

			err := executeViaMemfd(fatFile, entry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, time.Now())
			if err == nil {
				t.Fatalf("expected executeViaMemfd to fail on seal error %v, got nil", tc.sealErr)
			}
			if !errors.Is(err, format.ErrMemfdSealingFailed) {
				t.Errorf("expected ErrMemfdSealingFailed in error chain, got: %v", err)
			}
			if execveCalled {
				t.Fatal("execve must NOT be called on unsealed memfd descriptor")
			}
		})

		t.Run(tc.name+"_executeVariant_auto_mode_cache_fallback", func(t *testing.T) {
			t.Setenv(format.EnvExecMode, "")
			t.Setenv(format.EnvDispatchMode, "")
			t.Setenv(format.EnvDebug, "1")

			var cacheExecuted bool
			expectedTarget := filepath.Join(cacheDir, entry.SHA256)
			execveFunc = func(argv0 string, argv []string, envv []string) error {
				target := argv0
				if link, err := os.Readlink(argv0); err == nil {
					target = link
				}
				if target == expectedTarget {
					cacheExecuted = true
					return nil
				}
				return errors.New("unexpected exec path: " + argv0)
			}
			memfdSealFunc = func(fd int, seals int) error {
				return tc.sealErr
			}

			err := executeVariant(fatFile, entry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, time.Now())
			if err != nil {
				t.Fatalf("expected executeVariant to cleanly fallback to cache on seal error %v, got: %v", tc.sealErr, err)
			}
			if !cacheExecuted {
				t.Fatal("expected cache execution fallback when memfd sealing fails in auto mode")
			}
		})

		t.Run(tc.name+"_executeVariant_explicit_memfd_mode_fails_fast", func(t *testing.T) {
			t.Setenv(format.EnvExecMode, format.ExecModeMemfd)
			t.Setenv(format.EnvDebug, "1")

			var execveCalled bool
			execveFunc = func(argv0 string, argv []string, envv []string) error {
				execveCalled = true
				return nil
			}
			memfdSealFunc = func(fd int, seals int) error {
				return tc.sealErr
			}

			err := executeVariant(fatFile, entry, idx, []string{testAppArg}, []string{}, hostInfo, policyRes, time.Now())
			if err == nil {
				t.Fatalf("expected executeVariant to fail in explicit memfd mode on seal error %v, got nil", tc.sealErr)
			}
			if !errors.Is(err, format.ErrMemfdSealingFailed) {
				t.Errorf("expected ErrMemfdSealingFailed, got: %v", err)
			}
			if execveCalled {
				t.Fatal("execve must NOT be called when explicit memfd mode fails")
			}
		})
	}
}




