package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ghostnetorg/microfat/internal/format"
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

	// 1. Standard auto-tune
	env := buildAutoTunedEnviron(base)
	if len(env) < len(base) {
		t.Errorf("expected env to contain at least base items")
	}

	// 2. Opt-out via MICROFAT_AUTOTUNE=0
	t.Setenv("MICROFAT_AUTOTUNE", "0")
	envOptOut := buildAutoTunedEnviron(base)
	if len(envOptOut) != len(base) {
		t.Errorf("expected opt-out env to match base length, got %d vs %d", len(envOptOut), len(base))
	}

	// 3. Opt-out via MICROFAT_AUTOTUNE=false
	t.Setenv("MICROFAT_AUTOTUNE", "false")
	envFalse := buildAutoTunedEnviron(base)
	if len(envFalse) != len(base) {
		t.Errorf("expected opt-out false to match base length")
	}

	// 4. Preserve existing GOMEMLIMIT and GOMAXPROCS
	t.Setenv("MICROFAT_AUTOTUNE", "1")
	existing := []string{"GOMEMLIMIT=1GiB", "GOMAXPROCS=8"}
	envPreserve := buildAutoTunedEnviron(existing)
	if len(envPreserve) != 2 || envPreserve[0] != "GOMEMLIMIT=1GiB" || envPreserve[1] != "GOMAXPROCS=8" {
		t.Errorf("failed to preserve existing env: %v", envPreserve)
	}

	// 5. Custom memory ratio
	t.Setenv("MICROFAT_MEM_RATIO", "0.85")
	_ = buildAutoTunedEnviron(base)
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
