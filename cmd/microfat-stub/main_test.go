package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ghostnetorg/microfat/internal/format"
	"github.com/ghostnetorg/microfat/internal/pack"
	"github.com/ghostnetorg/pkg/microarch"
	"github.com/klauspost/compress/zstd"
)

func TestPrintHelpAndInfo(t *testing.T) {
	idx := &format.Index{
		Version:     1,
		AppName:     "testapp",
		TargetOS:    "linux",
		TargetArch:  "amd64",
		CreatedUnix: 1700000000,
		Variants: []format.VariantEntry{
			{
				Level:            "v1",
				Offset:           1000,
				CompressedSize:   500,
				UncompressedSize: 1000,
				SHA256:           "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			},
		},
	}
	hostInfo := microarch.Info{
		OS:       "linux",
		Arch:     "amd64",
		Level:    "v3",
		Features: []string{"avx", "avx2"},
	}

	// Should execute without panic
	printHelp(idx, hostInfo, &idx.Variants[0])
	printInfo(idx, hostInfo, &idx.Variants[0], 2000)
}

func TestBuildAutoTunedEnviron(t *testing.T) {
	base := []string{"PATH=/usr/bin", "USER=test"}

	// 1. Standard auto-tune with metadata injection
	env := buildAutoTunedEnviron(base, "v3", format.ExecModeMemfd)
	hasVariant := false
	hasExecMode := false
	for _, e := range env {
		if e == "MICROFAT_SELECTED_VARIANT=v3" {
			hasVariant = true
		}
		if e == "MICROFAT_EXEC_MODE=memfd" {
			hasExecMode = true
		}
	}
	if !hasVariant || !hasExecMode {
		t.Errorf("expected telemetry env vars in env: %v", env)
	}

	// 2. Opt-out via MICROFAT_AUTOTUNE=0
	t.Setenv("MICROFAT_AUTOTUNE", "0")
	envOptOut := buildAutoTunedEnviron(base, "v1", format.ExecModeCache)
	if len(envOptOut) != len(base)+2 { // base + 2 injected metadata vars
		t.Errorf("expected opt-out env to have len %d, got %d", len(base)+2, len(envOptOut))
	}

	// 3. Opt-out via MICROFAT_AUTOTUNE=false
	t.Setenv("MICROFAT_AUTOTUNE", "false")
	envFalse := buildAutoTunedEnviron(base, "v1", format.ExecModeCache)
	if len(envFalse) != len(base)+2 {
		t.Errorf("expected opt-out false to match base + 2 length")
	}

	// 4. Preserve existing GOMEMLIMIT and GOMAXPROCS
	t.Setenv("MICROFAT_AUTOTUNE", "1")
	existing := []string{"GOMEMLIMIT=1GiB", "GOMAXPROCS=8"}
	envPreserve := buildAutoTunedEnviron(existing, "v3", format.ExecModeMemfd)
	var foundMem, foundProcs bool
	for _, e := range envPreserve {
		if e == "GOMEMLIMIT=1GiB" {
			foundMem = true
		}
		if e == "GOMAXPROCS=8" {
			foundProcs = true
		}
	}
	if !foundMem || !foundProcs {
		t.Errorf("failed to preserve existing user env: %v", envPreserve)
	}

	// 5. Custom memory ratio
	t.Setenv("MICROFAT_MEM_RATIO", "0.85")
	_ = buildAutoTunedEnviron(base, "v3", format.ExecModeMemfd)
}

func TestLogDiagnostics(t *testing.T) {
	entry := &format.VariantEntry{Level: "v3"}
	hostInfo := microarch.Info{Arch: "amd64", Level: "v3"}
	env := []string{"GOMEMLIMIT=1000B", "GOMAXPROCS=4"}

	// Text debug output
	t.Setenv("MICROFAT_DEBUG", "1")
	t.Setenv("MICROFAT_LOG", "")
	logDiagnostics(entry, format.ExecModeMemfd, hostInfo, env)

	// JSON log output
	t.Setenv("MICROFAT_DEBUG", "0")
	t.Setenv("MICROFAT_LOG", "json")
	logDiagnostics(entry, format.ExecModeCache, hostInfo, env)
}

func TestGetSelfExecutablePath(t *testing.T) {
	path, err := getSelfExecutablePath()
	if err != nil {
		t.Fatalf("getSelfExecutablePath failed: %v", err)
	}
	if path == "" {
		t.Errorf("expected non-empty executable path")
	}
}

func TestExtractVariantAndOptimizeTo(t *testing.T) {
	tempDir := t.TempDir()

	// Create dummy compressed file
	payloadData := []byte("HELLO EXECUTABLE PAYLOAD")
	entry, rawFile := createDummyVariantFile(t, tempDir, payloadData)
	defer func() { _ = rawFile.Close() }()

	// Test extractVariantToWriter
	var buf bytes.Buffer
	if err := extractVariantToWriter(rawFile, entry, &buf); err != nil {
		t.Fatalf("extractVariantToWriter failed: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), payloadData) {
		t.Errorf("extracted payload mismatch: got %q, expected %q", buf.String(), string(payloadData))
	}

	// Test optimizeTo
	destPath := filepath.Join(tempDir, "sub", "extracted_binary")
	if err := optimizeTo(destPath, rawFile, entry); err != nil {
		t.Fatalf("optimizeTo failed: %v", err)
	}

	readBack, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading back optimized binary: %v", err)
	}
	if !bytes.Equal(readBack, payloadData) {
		t.Errorf("materialized binary content mismatch: got %q", string(readBack))
	}
}

func TestTrimToAndInPlace(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("stub-bytes-data"), 0o755)

	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("variant-v1-bytes-long-content"), 0o755)

	fatPath := filepath.Join(tempDir, "fat_app")
	_, err := pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "testapp",
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Path},
	})
	if err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	fatFile, err := os.Open(fatPath)
	if err != nil {
		t.Fatalf("open fat file: %v", err)
	}
	defer func() { _ = fatFile.Close() }()

	stat, _ := fatFile.Stat()

	// Test trimTo
	trimmedTarget := filepath.Join(tempDir, "sub2", "trimmed_app")
	if err := trimTo(trimmedTarget, fatFile, stat.Size(), "v1"); err != nil {
		t.Fatalf("trimTo failed: %v", err)
	}

	// Test trimInPlace on a copy
	copyPath := filepath.Join(tempDir, "fat_copy")
	data, _ := os.ReadFile(fatPath)
	_ = os.WriteFile(copyPath, data, 0o755)
	copyFile, _ := os.Open(copyPath)
	defer func() { _ = copyFile.Close() }()

	if err := trimInPlace(copyPath, copyFile, stat.Size(), "v1"); err != nil {
		t.Fatalf("trimInPlace failed: %v", err)
	}

	// Test trimInPlace via symlink
	symPath := filepath.Join(tempDir, "fat_symlink")
	if err := os.Symlink(copyPath, symPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}
	if err := trimInPlace(symPath, copyFile, stat.Size(), "v1"); err != nil {
		t.Fatalf("trimInPlace on symlink failed: %v", err)
	}
}

func TestCacheDirectoryPermissions(t *testing.T) {
	tempDir := t.TempDir()
	customCache := filepath.Join(tempDir, "custom_cache")
	t.Setenv("XDG_CACHE_HOME", customCache)

	// Simulate cache dir creation logic
	cacheDir := filepath.Join(customCache, "microfat")
	if err := os.MkdirAll(cacheDir, privateCacheDirMode); err != nil {
		t.Fatalf("failed to create cache dir: %v", err)
	}

	info, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatalf("stat cache dir: %v", err)
	}
	if info.Mode().Perm() != privateCacheDirMode {
		t.Errorf("expected cache permissions %o, got %o", privateCacheDirMode, info.Mode().Perm())
	}
}

func createDummyVariantFile(t *testing.T, dir string, content []byte) (*format.VariantEntry, *os.File) {
	t.Helper()
	filePath := filepath.Join(dir, "dummy_container")
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0o755)
	if err != nil {
		t.Fatalf("creating dummy file: %v", err)
	}

	// Compress content with zstd
	var zstdBuf bytes.Buffer
	enc, err := zstd.NewWriter(&zstdBuf)
	if err != nil {
		t.Fatalf("creating zstd encoder: %v", err)
	}
	if _, err := enc.Write(content); err != nil {
		t.Fatalf("writing zstd: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("closing zstd: %v", err)
	}

	offset := int64(100)
	if _, err := f.WriteAt(zstdBuf.Bytes(), offset); err != nil {
		t.Fatalf("writing at offset: %v", err)
	}

	entry := &format.VariantEntry{
		Level:            "v1",
		Offset:           offset,
		CompressedSize:   int64(zstdBuf.Len()),
		UncompressedSize: int64(len(content)),
	}

	return entry, f
}
