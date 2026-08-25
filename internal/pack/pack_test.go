package pack

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ghostnetorg/microfat/internal/format"
)

func TestPackAndVerify(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create fake stub binary
	stubPath := filepath.Join(tempDir, "microfat-stub")
	stubContent := []byte("#!/bin/sh\necho Stub Launcher\n")
	if err := os.WriteFile(stubPath, stubContent, 0o755); err != nil {
		t.Fatalf("failed to write stub: %v", err)
	}

	// 2. Create fake variant binaries
	v1Path := filepath.Join(tempDir, "bin-v1")
	v1Content := []byte("#!/bin/sh\necho Executing Variant v1 (Baseline SSE2)\n")
	if err := os.WriteFile(v1Path, v1Content, 0o755); err != nil {
		t.Fatalf("failed to write v1: %v", err)
	}

	v3Path := filepath.Join(tempDir, "bin-v3")
	v3Content := []byte("#!/bin/sh\necho Executing Variant v3 (AVX2/FMA/BMI2)\n")
	if err := os.WriteFile(v3Path, v3Content, 0o755); err != nil {
		t.Fatalf("failed to write v3: %v", err)
	}

	v4Path := filepath.Join(tempDir, "bin-v4")
	v4Content := []byte("#!/bin/sh\necho Executing Variant v4 (AVX-512)\n")
	if err := os.WriteFile(v4Path, v4Content, 0o755); err != nil {
		t.Fatalf("failed to write v4: %v", err)
	}

	// 3. Pack into fat binary
	fatBinaryPath := filepath.Join(tempDir, "output-fat-app")
	opts := Options{
		StubPath:          stubPath,
		OutputPath:        fatBinaryPath,
		AppName:           "test-fat-app",
		TargetOS:          "linux",
		TargetArch:        "amd64",
		SkipELFValidation: true,
		Variants: map[string]string{
			"v1": v1Path,
			"v3": v3Path,
			"v4": v4Path,
		},
	}

	idx, err := Pack(opts)
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	if len(idx.Variants) != 3 {
		t.Fatalf("expected 3 variants in index, got %d", len(idx.Variants))
	}
	if idx.Variants[0].Level != "v1" || idx.Variants[1].Level != "v3" || idx.Variants[2].Level != "v4" {
		t.Errorf("variants not ordered ascending: %+v", idx.Variants)
	}

	// 4. Verify the generated binary
	file, err := os.Open(fatBinaryPath)
	if err != nil {
		t.Fatalf("failed to open packed binary: %v", err)
	}
	defer func() { _ = file.Close() }()

	stat, err := file.Stat()
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}

	if !format.IsFatBinary(file, stat.Size()) {
		t.Errorf("expected IsFatBinary to be true")
	}

	verifiedIdx, results, err := VerifyBinary(file, stat.Size())
	if err != nil {
		t.Fatalf("VerifyBinary failed: %v", err)
	}

	if verifiedIdx.AppName != "test-fat-app" {
		t.Errorf("expected AppName test-fat-app, got %s", verifiedIdx.AppName)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 verification results, got %d", len(results))
	}

	for _, res := range results {
		if !res.Valid || res.Error != nil {
			t.Errorf("variant %s failed verification: %v", res.Level, res.Error)
		}
	}
}

func TestTrimBinary(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("stub-header-code"), 0o755)

	v1Path := filepath.Join(tempDir, "v1")
	v3Path := filepath.Join(tempDir, "v3")
	_ = os.WriteFile(v1Path, []byte("v1-binary-payload"), 0o755)
	_ = os.WriteFile(v3Path, []byte("v3-binary-payload-larger-data"), 0o755)

	fatPath := filepath.Join(tempDir, "fat")
	_, err := Pack(Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		SkipELFValidation: true,
		Variants: map[string]string{
			"v1": v1Path,
			"v3": v3Path,
		},
	})
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	fatFile, err := os.Open(fatPath)
	if err != nil {
		t.Fatalf("open fat: %v", err)
	}
	defer func() { _ = fatFile.Close() }()

	stat, _ := fatFile.Stat()

	// 1. Trim to v3
	var trimmedBuf bytes.Buffer
	newIdx, err := TrimBinary(fatFile, stat.Size(), "v3", &trimmedBuf)
	if err != nil {
		t.Fatalf("TrimBinary failed: %v", err)
	}

	if len(newIdx.Variants) != 1 || newIdx.Variants[0].Level != "v3" {
		t.Fatalf("expected 1 variant 'v3', got %+v", newIdx.Variants)
	}

	// 2. Verify the trimmed buffer
	trimmedReader := bytes.NewReader(trimmedBuf.Bytes())
	verifiedIdx, results, err := VerifyBinary(trimmedReader, int64(trimmedBuf.Len()))
	if err != nil {
		t.Fatalf("VerifyBinary on trimmed binary failed: %v", err)
	}
	if len(verifiedIdx.Variants) != 1 || !results[0].Valid {
		t.Errorf("trimmed binary verification failed: %+v", results)
	}

	// 3. Error case: trim to non-existent variant
	var dummy bytes.Buffer
	_, err = TrimBinary(fatFile, stat.Size(), "v99", &dummy)
	if err == nil {
		t.Errorf("expected error trimming non-existent variant")
	}
}

func TestPackValidationErrors(t *testing.T) {
	tempDir := t.TempDir()

	// No variants
	_, err := Pack(Options{StubPath: "stub", OutputPath: "out", SkipELFValidation: true})
	if !errors.Is(err, ErrNoVariantsSpecified) {
		t.Errorf("expected ErrNoVariantsSpecified, got %v", err)
	}

	// Empty output path
	_, err = Pack(Options{
		StubPath:          "stub",
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": "dummy"},
	})
	if err == nil {
		t.Errorf("expected error for empty output path")
	}

	// Missing stub
	_, err = Pack(Options{
		StubPath:          filepath.Join(tempDir, "nonexistent-stub"),
		OutputPath:        filepath.Join(tempDir, "out"),
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": filepath.Join(tempDir, "nonexistent-v1")},
	})
	if !errors.Is(err, ErrStubMissing) {
		t.Errorf("expected ErrStubMissing, got %v", err)
	}

	// Valid stub, missing variant
	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("stub"), 0o755)
	_, err = Pack(Options{
		StubPath:          stubPath,
		OutputPath:        filepath.Join(tempDir, "out"),
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": filepath.Join(tempDir, "nonexistent-v1")},
	})
	if !errors.Is(err, ErrVariantNotFound) {
		t.Errorf("expected ErrVariantNotFound, got %v", err)
	}
}

func TestVerifyCorruptedVariant(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("stub-data-12345"), 0o755)

	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("v1-binary-payload-data"), 0o755)

	fatPath := filepath.Join(tempDir, "fat")
	_, err := Pack(Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Path},
	})
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	data, err := os.ReadFile(fatPath)
	if err != nil {
		t.Fatalf("failed to read fat file: %v", err)
	}

	// Corrupt payload data inside the file (byte at index 20)
	corruptData := make([]byte, len(data))
	copy(corruptData, data)
	corruptData[len("stub-data-12345")+2] ^= 0xFF

	reader := bytes.NewReader(corruptData)
	_, results, err := VerifyBinary(reader, int64(len(corruptData)))
	if err != nil {
		t.Fatalf("VerifyBinary failed unexpectedly at index stage: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Valid {
		t.Errorf("expected corrupted variant to be invalid")
	}
}

func TestELFValidation(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create a valid 64-bit AMD64 ELF dummy
	amd64Stub := createDummyELF(t, tempDir, "amd64_stub", 62, 2) // EM_X86_64, ELFCLASS64
	amd64V1 := createDummyELF(t, tempDir, "amd64_v1", 62, 2)
	arm64V1 := createDummyELF(t, tempDir, "arm64_v1", 183, 2)   // EM_AARCH64, ELFCLASS64
	nonELF := filepath.Join(tempDir, "script.sh")
	_ = os.WriteFile(nonELF, []byte("#!/bin/sh\necho hi\n"), 0o755)

	// Valid AMD64 pack
	outPath := filepath.Join(tempDir, "fat_amd64")
	_, err := Pack(Options{
		StubPath:   amd64Stub,
		OutputPath: outPath,
		TargetOS:   "linux",
		TargetArch: "amd64",
		Variants:   map[string]string{"v1": amd64V1},
	})
	if err != nil {
		t.Fatalf("expected valid AMD64 pack to succeed, got: %v", err)
	}

	// Reject non-ELF file
	outNonELF := filepath.Join(tempDir, "fat_nonelf")
	_, err = Pack(Options{
		StubPath:   nonELF,
		OutputPath: outNonELF,
		TargetOS:   "linux",
		TargetArch: "amd64",
		Variants:   map[string]string{"v1": amd64V1},
	})
	if !errors.Is(err, ErrInvalidELF) {
		t.Errorf("expected ErrInvalidELF for non-ELF stub, got %v", err)
	}

	// Reject mismatched architecture (ARM64 binary for AMD64 target)
	outMismatch := filepath.Join(tempDir, "fat_mismatch")
	_, err = Pack(Options{
		StubPath:   amd64Stub,
		OutputPath: outMismatch,
		TargetOS:   "linux",
		TargetArch: "amd64",
		Variants:   map[string]string{"v1": arm64V1},
	})
	if !errors.Is(err, ErrInvalidELF) {
		t.Errorf("expected ErrInvalidELF for mismatched architecture variant, got %v", err)
	}
}

func createDummyELF(t *testing.T, dir, name string, machine uint16, class byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	hdr := make([]byte, 64)
	copy(hdr[0:4], []byte{0x7f, 'E', 'L', 'F'})
	hdr[4] = class // EI_CLASS (2 = 64-bit)
	hdr[5] = 1     // EI_DATA (1 = little endian)
	hdr[6] = 1     // EI_VERSION (1 = current)
	hdr[7] = 0     // EI_OSABI
	// e_type = 2 (ET_EXEC)
	hdr[16] = 2
	// e_machine
	hdr[18] = byte(machine)
	hdr[19] = byte(machine >> 8)
	// e_version = 1
	hdr[20] = 1
	// e_ehsize = 64
	hdr[52] = 64

	if err := os.WriteFile(path, hdr, 0o755); err != nil {
		t.Fatalf("writing dummy elf: %v", err)
	}
	return path
}
