package pack

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/klauspost/compress/zstd"
)

const (
	testOSLinux            = "linux"
	testArchAMD64          = "amd64"
	testCompressionFastest = "fastest"
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
		TargetOS:          testOSLinux,
		TargetArch:        testArchAMD64,
		SkipELFValidation: true,
		CompressionLevel:  testCompressionFastest,
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

	// Pack with default OS/Arch/Permissions and invalid destination directory
	v1Real := filepath.Join(tempDir, "v1_real")
	_ = os.WriteFile(v1Real, []byte("v1"), 0o755)
	_, err = Pack(Options{
		StubPath:          stubPath,
		OutputPath:        filepath.Join(tempDir, "nonexistent_dir", "out"),
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Real},
	})
	if err == nil {
		t.Errorf("expected error packing into nonexistent directory")
	}

	// Empty stub path
	_, err = Pack(Options{
		StubPath:          "",
		OutputPath:        filepath.Join(tempDir, "out"),
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Real},
	})
	if !errors.Is(err, ErrStubMissing) {
		t.Errorf("expected ErrStubMissing for empty stub path, got %v", err)
	}

	// Invalid ELF stub with ELF validation enabled
	_, err = Pack(Options{
		StubPath:          stubPath,
		OutputPath:        filepath.Join(tempDir, "out"),
		SkipELFValidation: false,
		Variants:          map[string]string{"v1": v1Real},
	})
	if err == nil || !errors.Is(err, ErrInvalidELF) {
		t.Errorf("expected ErrInvalidELF validating non-ELF stub, got %v", err)
	}

	// Valid ELF stub, invalid ELF variant with ELF validation enabled
	amd64StubValid := createDummyELF(t, tempDir, "amd64_stub_val", 62, 2)
	_, err = Pack(Options{
		StubPath:          amd64StubValid,
		OutputPath:        filepath.Join(tempDir, "out"),
		SkipELFValidation: false,
		Variants:          map[string]string{"v1": v1Real},
	})
	if err == nil || !errors.Is(err, ErrInvalidELF) {
		t.Errorf("expected ErrInvalidELF validating non-ELF variant, got %v", err)
	}

	// Read-only directory for OutputPath
	roDir := filepath.Join(tempDir, "ro_pack_dir")
	_ = os.MkdirAll(roDir, 0o755)
	_ = os.Chmod(roDir, 0o555)
	defer func() { _ = os.Chmod(roDir, 0o755) }()
	_, err = Pack(Options{
		StubPath:          stubPath,
		OutputPath:        filepath.Join(roDir, "out"),
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Real},
	})
	if err == nil {
		t.Errorf("expected error packing into read-only directory")
	}

	// Missing variant file when SkipELFValidation is true
	_, err = Pack(Options{
		StubPath:          stubPath,
		OutputPath:        filepath.Join(tempDir, "out_missing_var"),
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": filepath.Join(tempDir, "missing_v1")},
	})
	if err == nil || !errors.Is(err, ErrVariantNotFound) {
		t.Errorf("expected ErrVariantNotFound packing with missing variant file, got %v", err)
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

	// Test size and checksum mismatches in verification with valid zstd payloads
	var zstdPayload bytes.Buffer
	enc, _ := zstd.NewWriter(&zstdPayload)
	_, _ = enc.Write([]byte("valid-uncompressed-content"))
	_ = enc.Close()

	payloadBytes := zstdPayload.Bytes()
	idxSizeMismatch := &format.Index{
		Version:     format.FormatVersionCurrent,
		CreatedUnix: 1000,
		Variants: []format.VariantEntry{
			{
				Level:            "v1",
				Offset:           0,
				CompressedSize:   int64(len(payloadBytes)),
				UncompressedSize: int64(len("valid-uncompressed-content")) + 50, // wrong uncompressed size
				SHA256:           "hash",
			},
		},
	}
	var bufSize bytes.Buffer
	_, _ = bufSize.Write(payloadBytes)
	_, _ = format.WriteIndexAndTrailer(&bufSize, idxSizeMismatch, int64(len(payloadBytes)))
	_, resSize, _ := VerifyBinary(bytes.NewReader(bufSize.Bytes()), int64(bufSize.Len()))
	if len(resSize) != 1 || resSize[0].Valid || !errors.Is(resSize[0].Error, ErrSizeMismatch) {
		t.Errorf("expected ErrSizeMismatch, got %+v", resSize)
	}

	idxHashMismatch := &format.Index{
		Version:     format.FormatVersionCurrent,
		CreatedUnix: 1000,
		Variants: []format.VariantEntry{
			{
				Level:            "v1",
				Offset:           0,
				CompressedSize:   int64(len(payloadBytes)),
				UncompressedSize: int64(len("valid-uncompressed-content")),
				SHA256:           "0000000000000000000000000000000000000000000000000000000000000000", // wrong hash
			},
		},
	}
	var bufHash bytes.Buffer
	_, _ = bufHash.Write(payloadBytes)
	_, _ = format.WriteIndexAndTrailer(&bufHash, idxHashMismatch, int64(len(payloadBytes)))
	_, resHash, _ := VerifyBinary(bytes.NewReader(bufHash.Bytes()), int64(bufHash.Len()))
	if len(resHash) != 1 || resHash[0].Valid || !errors.Is(resHash[0].Error, ErrChecksumMismatch) {
		t.Errorf("expected ErrChecksumMismatch, got %+v", resHash)
	}

	// VerifyBinary error reading index
	_, _, err = VerifyBinary(bytes.NewReader([]byte("not-a-fat-binary")), int64(len("not-a-fat-binary")))
	if err == nil {
		t.Errorf("expected VerifyBinary to fail on non-fat binary")
	}
}

func TestTrimBinaryEdgeCases(t *testing.T) {
	// Index with no variants
	idxNoVariants := &format.Index{
		Version:     format.FormatVersionCurrent,
		CreatedUnix: 1000,
		Variants:    []format.VariantEntry{},
	}
	var bufEmpty bytes.Buffer
	_, _ = format.WriteIndexAndTrailer(&bufEmpty, idxNoVariants, 0)
	var out bytes.Buffer
	_, err := TrimBinary(bytes.NewReader(bufEmpty.Bytes()), int64(bufEmpty.Len()), "v1", &out)
	if err == nil {
		t.Errorf("expected error trimming empty variants index")
	}

	// Index with invalid stub offset (stubSize <= 0)
	idxInvalidStub := &format.Index{
		Version:     format.FormatVersionCurrent,
		CreatedUnix: 1000,
		Variants: []format.VariantEntry{
			{Level: "v1", Offset: 0, CompressedSize: 10, UncompressedSize: 10},
		},
	}
	var bufInvalidStub bytes.Buffer
	_, _ = bufInvalidStub.Write([]byte("0123456789"))
	_, _ = format.WriteIndexAndTrailer(&bufInvalidStub, idxInvalidStub, 10)
	var outDummy bytes.Buffer
	_, err = TrimBinary(bytes.NewReader(bufInvalidStub.Bytes()), int64(bufInvalidStub.Len()), "v1", &outDummy)
	if err == nil {
		t.Errorf("expected error for invalid stub size <= 0")
	}

	// Error copying stub / variant / trailer when writer fails
	idxValid := &format.Index{
		Version:     format.FormatVersionCurrent,
		CreatedUnix: 1000,
		Variants: []format.VariantEntry{
			{Level: "v1", Offset: 5, CompressedSize: 5, UncompressedSize: 5},
		},
	}
	var bufValid bytes.Buffer
	_, _ = bufValid.Write([]byte("0123456789"))
	_, _ = format.WriteIndexAndTrailer(&bufValid, idxValid, 10)
	validReader := bytes.NewReader(bufValid.Bytes())
	validSize := int64(bufValid.Len())

	for i := 1; i <= 3; i++ {
		_, err = TrimBinary(validReader, validSize, "v1", &errWriter{failOnWrite: i})
		if err == nil {
			t.Errorf("expected TrimBinary to fail when writer fails on write %d", i)
		}
	}
}

type errWriter struct {
	failOnWrite int
	writes      int
}

func (w *errWriter) Write(p []byte) (n int, err error) {
	w.writes++
	if w.writes >= w.failOnWrite {
		return 0, errors.New("write failed")
	}
	return len(p), nil
}

func TestELFValidation(t *testing.T) {
	tempDir := t.TempDir()

	// Non-existent file
	if err := ValidateELFBinary(filepath.Join(tempDir, "nonexistent"), testOSLinux, testArchAMD64); err == nil {
		t.Errorf("expected ValidateELFBinary to fail on nonexistent file")
	}

	// 1. Create a valid 64-bit AMD64 ELF dummy
	amd64Stub := createDummyELF(t, tempDir, "amd64_stub", 62, 2) // EM_X86_64, ELFCLASS64
	amd64V1 := createDummyELF(t, tempDir, "amd64_v1", 62, 2)
	arm64Stub := createDummyELF(t, tempDir, "arm64_stub", 183, 2) // EM_AARCH64, ELFCLASS64
	arm64V1 := createDummyELF(t, tempDir, "arm64_v1", 183, 2)     // EM_AARCH64, ELFCLASS64
	elf32Bit := createDummyELF(t, tempDir, "elf32", 62, 1)        // ELFCLASS32
	nonELF := filepath.Join(tempDir, "script.sh")
	_ = os.WriteFile(nonELF, []byte("#!/bin/sh\necho hi\n"), 0o755)

	// Valid AMD64 pack
	outPath := filepath.Join(tempDir, "fat_amd64")
	_, err := Pack(Options{
		StubPath:   amd64Stub,
		OutputPath: outPath,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants:   map[string]string{"v1": amd64V1},
	})
	if err != nil {
		t.Fatalf("expected valid AMD64 pack to succeed, got: %v", err)
	}

	// Valid ARM64 pack
	outARM64Path := filepath.Join(tempDir, "fat_arm64")
	_, err = Pack(Options{
		StubPath:   arm64Stub,
		OutputPath: outARM64Path,
		TargetOS:   testOSLinux,
		TargetArch: "arm64",
		Variants:   map[string]string{"v8.0": arm64V1},
	})
	if err != nil {
		t.Fatalf("expected valid ARM64 pack to succeed, got: %v", err)
	}

	// Reject 32-bit ELF
	if err := ValidateELFBinary(elf32Bit, testOSLinux, testArchAMD64); !errors.Is(err, ErrInvalidELF) {
		t.Errorf("expected ErrInvalidELF for 32-bit ELF, got %v", err)
	}

	// Reject non-ELF file
	outNonELF := filepath.Join(tempDir, "fat_nonelf")
	_, err = Pack(Options{
		StubPath:   nonELF,
		OutputPath: outNonELF,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
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
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants:   map[string]string{"v1": arm64V1},
	})
	if !errors.Is(err, ErrInvalidELF) {
		t.Errorf("expected ErrInvalidELF for mismatched architecture variant, got %v", err)
	}

	// Test aliases x86_64 and aarch64
	if err := ValidateELFBinary(amd64V1, testOSLinux, "x86_64"); err != nil {
		t.Errorf("expected x86_64 alias to succeed: %v", err)
	}
	if err := ValidateELFBinary(arm64V1, testOSLinux, "aarch64"); err != nil {
		t.Errorf("expected aarch64 alias to succeed: %v", err)
	}
}

func TestFinalizeOutputFile(t *testing.T) {
	tempDir := t.TempDir()

	// Success case
	tmp1, _ := os.CreateTemp(tempDir, "tmp1")
	tmpPath1 := tmp1.Name()
	dest1 := filepath.Join(tempDir, "dest1")
	if err := finalizeOutputFile(tmp1, tmpPath1, dest1, 0o755); err != nil {
		t.Fatalf("finalizeOutputFile success case failed: %v", err)
	}

	// Error case: destination is an existing non-empty directory
	tmp2, _ := os.CreateTemp(tempDir, "tmp2")
	tmpPath2 := tmp2.Name()
	if err := finalizeOutputFile(tmp2, tmpPath2, tempDir, 0o755); err == nil {
		t.Errorf("expected error renaming into existing directory")
	}

	// Error case: file is already closed (sync/chmod fails)
	tmp3, _ := os.CreateTemp(tempDir, "tmp3")
	tmpPath3 := tmp3.Name()
	_ = tmp3.Close()
	if err := finalizeOutputFile(tmp3, tmpPath3, filepath.Join(tempDir, "dest3"), 0o755); err == nil {
		t.Errorf("expected error finalizing closed file")
	}

	// Error case: tmpPath is removed before rename
	tmp5, _ := os.CreateTemp(tempDir, "tmp5")
	tmpPath5 := tmp5.Name()
	_ = os.Remove(tmpPath5)
	if err := finalizeOutputFile(tmp5, tmpPath5, filepath.Join(tempDir, "dest5"), 0o755); err == nil {
		t.Errorf("expected error when tmpPath does not exist on rename")
	}

	// Error case: writeVariantPayload with closed file
	validPayload := filepath.Join(tempDir, "valid_payload")
	_ = os.WriteFile(validPayload, []byte("payload"), 0o644)
	packOpts := &Options{}
	if _, _, err := writeVariantPayload(tmp3, "v1", validPayload, packOpts, 0, nil); err == nil {
		t.Errorf("expected error writing payload to closed file")
	}

	// Error case: writeVariantPayload with nonexistent file
	tmp4, _ := os.CreateTemp(tempDir, "tmp4")
	defer func() {
		_ = tmp4.Close()
		_ = os.Remove(tmp4.Name())
	}()
	if _, _, err := writeVariantPayload(tmp4, "v1", filepath.Join(tempDir, "nonexistent"), packOpts, 0, nil); err == nil {
		t.Errorf("expected error writing nonexistent payload")
	}
}

func TestPackEdgeCasesAndValidation(t *testing.T) {
	tempDir := t.TempDir()

	// 1. ValidateELFBinary with unsupported/mismatched machine types
	amd64ELF := createDummyELF(t, tempDir, "amd64_mismatch", 62, 2)  // EM_X86_64
	arm64ELF := createDummyELF(t, tempDir, "arm64_mismatch", 183, 2) // EM_AARCH64
	i386ELF := createDummyELF(t, tempDir, "i386_elf", 3, 2)          // EM_386 with 64-bit class

	// Test AMD64 target with ARM64 ELF
	if err := ValidateELFBinary(arm64ELF, testOSLinux, "amd64"); !errors.Is(err, ErrInvalidELF) {
		t.Errorf("expected ErrInvalidELF validating ARM64 binary against amd64 target, got %v", err)
	}

	// Test ARM64 target with AMD64 ELF
	if err := ValidateELFBinary(amd64ELF, testOSLinux, "arm64"); !errors.Is(err, ErrInvalidELF) {
		t.Errorf("expected ErrInvalidELF validating AMD64 binary against arm64 target, got %v", err)
	}

	// Test AMD64 target with i386 ELF
	if err := ValidateELFBinary(i386ELF, testOSLinux, "amd64"); !errors.Is(err, ErrInvalidELF) {
		t.Errorf("expected ErrInvalidELF validating i386 binary against amd64 target, got %v", err)
	}

	// 2. VerifyBinary with corrupted zstd stream in variant payload
	corruptPayload := []byte("INVALID_ZSTD_STREAM_CONTENT_1234567890")
	idxCorrupt := &format.Index{
		Version:     format.FormatVersionCurrent,
		CreatedUnix: 1000,
		Variants: []format.VariantEntry{
			{
				Level:            "v1",
				Offset:           0,
				CompressedSize:   int64(len(corruptPayload)),
				UncompressedSize: 100,
				SHA256:           "hash",
			},
		},
	}
	var bufCorrupt bytes.Buffer
	_, _ = bufCorrupt.Write(corruptPayload)
	_, _ = format.WriteIndexAndTrailer(&bufCorrupt, idxCorrupt, int64(len(corruptPayload)))

	_, resCorrupt, err := VerifyBinary(bytes.NewReader(bufCorrupt.Bytes()), int64(bufCorrupt.Len()))
	if err != nil {
		t.Fatalf("unexpected error reading index in VerifyBinary: %v", err)
	}
	if len(resCorrupt) != 1 || resCorrupt[0].Valid || resCorrupt[0].Error == nil {
		t.Errorf("expected VerifyBinary to report error on corrupted zstd stream, got %+v", resCorrupt)
	}
}

func TestPackVerifyTrimARM64MultiVariant(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	arm64Stub := createDummyELF(t, tempDir, "arm64_stub_multi", 183, 2)
	arm64V80 := createDummyELF(t, tempDir, "arm64_v80", 183, 2)
	arm64V82 := createDummyELF(t, tempDir, "arm64_v82", 183, 2)
	arm64V90 := createDummyELF(t, tempDir, "arm64_v90", 183, 2)

	outPath := filepath.Join(tempDir, "fat_arm64_multi")
	idx, err := Pack(Options{
		StubPath:   arm64Stub,
		OutputPath: outPath,
		AppName:    "arm64-test-app",
		TargetOS:   testOSLinux,
		TargetArch: "arm64",
		Variants: map[string]string{
			"v8.0": arm64V80,
			"v8.2": arm64V82,
			"v9.0": arm64V90,
		},
	})
	if err != nil {
		t.Fatalf("Pack failed for multi-variant ARM64: %v", err)
	}

	if len(idx.Variants) != 3 {
		t.Fatalf("expected 3 variants, got %d", len(idx.Variants))
	}
	// Verify sorted order (v8.0 < v8.2 < v9.0)
	if idx.Variants[0].Level != "v8.0" || idx.Variants[1].Level != "v8.2" || idx.Variants[2].Level != "v9.0" {
		t.Errorf("unexpected variant order: %+v", idx.Variants)
	}

	// Verify binary
	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("open fat binary: %v", err)
	}
	defer f.Close()

	stat, _ := f.Stat()
	vIdx, results, err := VerifyBinary(f, stat.Size())
	if err != nil {
		t.Fatalf("VerifyBinary failed: %v", err)
	}
	if vIdx.AppName != "arm64-test-app" {
		t.Errorf("expected AppName arm64-test-app, got %s", vIdx.AppName)
	}
	for _, r := range results {
		if !r.Valid {
			t.Errorf("expected variant %s to be valid, got error %v", r.Level, r.Error)
		}
	}

	// Trim binary to v8.2
	var trimBuf bytes.Buffer
	trimmedIdx, err := TrimBinary(f, stat.Size(), "v8.2", &trimBuf)
	if err != nil {
		t.Fatalf("TrimBinary failed for v8.2: %v", err)
	}
	if len(trimmedIdx.Variants) != 1 || trimmedIdx.Variants[0].Level != "v8.2" {
		t.Errorf("unexpected trimmed index: %+v", trimmedIdx)
	}
}

func createDummyELF(t *testing.T, dir, name string, machine uint16, class byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	hdr := make([]byte, 64)
	copy(hdr[0:4], []byte{0x7f, 'E', 'L', 'F'})
	hdr[4] = class // EI_CLASS (2 = 64-bit, 1 = 32-bit)
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

func TestPrewarmVariantAndBinary(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("stub-payload"), 0o755)

	v1Path := filepath.Join(tempDir, "v1")
	v1Content := []byte("v1-binary-content-12345")
	_ = os.WriteFile(v1Path, v1Content, 0o755)

	v3Path := filepath.Join(tempDir, "v3")
	v3Content := []byte("v3-binary-content-67890-avx2")
	_ = os.WriteFile(v3Path, v3Content, 0o755)

	fatPath := filepath.Join(tempDir, "fat.bin")
	opts := Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "prewarm-app",
		TargetOS:          testOSLinux,
		TargetArch:        testArchAMD64,
		SkipELFValidation: true,
		Variants: map[string]string{
			"v1": v1Path,
			"v3": v3Path,
		},
	}

	_, err := Pack(opts)
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	f, err := os.Open(fatPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	cacheDir := filepath.Join(tempDir, "prewarm_cache")

	// 1. Prewarm specific level "v3"
	idx, results, err := PrewarmBinary(f, stat.Size(), []string{"v3"}, cacheDir)
	if err != nil {
		t.Fatalf("PrewarmBinary(v3) failed: %v", err)
	}
	if len(results) != 1 || results[0].Level != "v3" {
		t.Fatalf("unexpected results for v3: %+v", results)
	}
	if results[0].AlreadyCached {
		t.Errorf("expected alreadyCached=false on first prewarm")
	}

	// Verify cached file content
	cachedData, err := os.ReadFile(results[0].CachedPath)
	if err != nil {
		t.Fatalf("failed reading cached file: %v", err)
	}
	if !bytes.Equal(cachedData, v3Content) {
		t.Errorf("cached content mismatch: expected %q, got %q", v3Content, cachedData)
	}

	// 2. Prewarm again (should be cache hit)
	_, results2, err := PrewarmBinary(f, stat.Size(), []string{"v3"}, cacheDir)
	if err != nil {
		t.Fatalf("PrewarmBinary second time failed: %v", err)
	}
	if len(results2) != 1 || !results2[0].AlreadyCached {
		t.Errorf("expected alreadyCached=true on second prewarm, got %+v", results2)
	}

	// 3. Prewarm all variants (v3 is hit, v1 is extracted)
	_, resultsAll, err := PrewarmBinary(f, stat.Size(), nil, cacheDir)
	if err != nil {
		t.Fatalf("PrewarmBinary(all) failed: %v", err)
	}
	if len(resultsAll) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resultsAll))
	}
	var v1Res, v3Res format.PrewarmResult
	for _, r := range resultsAll {
		if r.Level == "v1" {
			v1Res = r
		} else if r.Level == "v3" {
			v3Res = r
		}
	}
	if v1Res.AlreadyCached {
		t.Errorf("expected v1 alreadyCached=false")
	}
	if !v3Res.AlreadyCached {
		t.Errorf("expected v3 alreadyCached=true")
	}

	// 4. Prewarm with non-existent level
	_, _, err = PrewarmBinary(f, stat.Size(), []string{"v4"}, cacheDir)
	if err == nil {
		t.Errorf("expected error when prewarming non-existent level v4")
	}

	// 5. Prewarm with auto-resolved cacheDir (empty string)
	t.Setenv(format.EnvCacheDir, filepath.Join(tempDir, "auto_cache"))
	_, _, err = PrewarmBinary(f, stat.Size(), []string{"v1"}, "")
	if err != nil {
		t.Fatalf("PrewarmBinary with empty cacheDir failed: %v", err)
	}
	t.Setenv(format.EnvCacheDir, "")

	// 6. PrewarmVariant missing SHA256 error
	badEntry := &format.VariantEntry{Level: "v1", SHA256: ""}
	_, _, _, err = PrewarmVariant(f, badEntry, cacheDir)
	if err == nil {
		t.Errorf("expected error for missing SHA256")
	}

	// 7. PrewarmVariant bad cache directory error
	blocker := filepath.Join(tempDir, "blocker")
	_ = os.WriteFile(blocker, []byte("x"), 0o600)
	_, _, _, err = PrewarmVariant(f, &idx.Variants[0], filepath.Join(blocker, "sub"))
	if err == nil {
		t.Errorf("expected error for unwritable cache directory")
	}

	// 8. PrewarmVariant with bad compression payload
	corruptEntry := &format.VariantEntry{
		Level:            "v1",
		Offset:           0,
		CompressedSize:   10,
		UncompressedSize: 100,
		SHA256:           "0123456789abcdef",
	}
	_, _, _, err = PrewarmVariant(f, corruptEntry, cacheDir)
	if err == nil {
		t.Errorf("expected error decompressing corrupt entry")
	}

	// 9. PrewarmVariant size mismatch
	sizeMismatchEntry := &format.VariantEntry{
		Level:            "v1",
		Offset:           idx.Variants[0].Offset,
		CompressedSize:   idx.Variants[0].CompressedSize,
		UncompressedSize: idx.Variants[0].UncompressedSize + 10,
		SHA256:           idx.Variants[0].SHA256,
	}
	_, _, _, err = PrewarmVariant(f, sizeMismatchEntry, cacheDir)
	if err == nil {
		t.Errorf("expected error for size mismatch")
	}

	// 10. PrewarmVariant hash mismatch
	hashMismatchEntry := &format.VariantEntry{
		Level:            "v1",
		Offset:           idx.Variants[0].Offset,
		CompressedSize:   idx.Variants[0].CompressedSize,
		UncompressedSize: idx.Variants[0].UncompressedSize,
		SHA256:           "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	}
	_, _, _, err = PrewarmVariant(f, hashMismatchEntry, cacheDir)
	if err == nil {
		t.Errorf("expected error for hash mismatch")
	}
}

func TestVerifyCacheVariantAndBinary(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("stub-payload"), 0o755)

	v1Path := filepath.Join(tempDir, "v1")
	v1Content := []byte("v1-binary-content-for-cache-verify")
	_ = os.WriteFile(v1Path, v1Content, 0o755)

	v3Path := filepath.Join(tempDir, "v3")
	v3Content := []byte("v3-binary-content-for-cache-verify-avx2")
	_ = os.WriteFile(v3Path, v3Content, 0o755)

	fatPath := filepath.Join(tempDir, "fat_verify.bin")
	opts := Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "verify-cache-app",
		TargetOS:          testOSLinux,
		TargetArch:        testArchAMD64,
		SkipELFValidation: true,
		Variants: map[string]string{
			"v1": v1Path,
			"v3": v3Path,
		},
	}

	idx, err := Pack(opts)
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	f, err := os.Open(fatPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	cacheDir := filepath.Join(tempDir, "cache_verify_dir")

	// 1. Clean cache: VerifyCacheBinary should report missing for all variants
	_, missingResults, err := VerifyCacheBinary(f, stat.Size(), nil, cacheDir)
	if err != nil {
		t.Fatalf("VerifyCacheBinary failed on clean cache: %v", err)
	}
	if len(missingResults) != 2 {
		t.Fatalf("expected 2 results, got %d", len(missingResults))
	}
	for _, r := range missingResults {
		if r.Valid || r.Status != format.PrewarmStatusMissing {
			t.Errorf("expected status 'missing' and valid=false for clean cache, got %+v", r)
		}
	}

	// 2. Prewarm v1 and verify: v1 should be valid, v3 should be missing
	_, _, err = PrewarmBinary(f, stat.Size(), []string{"v1"}, cacheDir)
	if err != nil {
		t.Fatalf("prewarming v1 failed: %v", err)
	}

	_, mixedResults, err := VerifyCacheBinary(f, stat.Size(), nil, cacheDir)
	if err != nil {
		t.Fatalf("VerifyCacheBinary failed on mixed cache: %v", err)
	}
	for _, r := range mixedResults {
		if r.Level == "v1" {
			if !r.Valid || r.Status != format.PrewarmStatusValid {
				t.Errorf("expected v1 to be valid, got %+v", r)
			}
		} else if r.Level == "v3" {
			if r.Valid || r.Status != format.PrewarmStatusMissing {
				t.Errorf("expected v3 to be missing, got %+v", r)
			}
		}
	}

	// 3. Prewarm all variants: both v1 and v3 should be valid
	_, _, err = PrewarmBinary(f, stat.Size(), nil, cacheDir)
	if err != nil {
		t.Fatalf("prewarming all failed: %v", err)
	}

	_, allValidResults, err := VerifyCacheBinary(f, stat.Size(), nil, cacheDir)
	if err != nil {
		t.Fatalf("VerifyCacheBinary failed on fully populated cache: %v", err)
	}
	for _, r := range allValidResults {
		if !r.Valid || r.Status != format.PrewarmStatusValid {
			t.Errorf("expected variant %s to be valid, got %+v", r.Level, r)
		}
	}

	// 4. Corrupt size of v1 (truncate to 5 bytes)
	v1Entry, _ := idx.FindVariant("v1")
	v1CachedFile := filepath.Join(cacheDir, v1Entry.SHA256)
	_ = os.WriteFile(v1CachedFile, []byte("short"), 0o755)

	resTrunc := VerifyCacheVariant(v1Entry, cacheDir)
	if resTrunc.Valid || resTrunc.Status != format.PrewarmStatusCorrupted {
		t.Errorf("expected status 'corrupted' for truncated cache entry, got %+v", resTrunc)
	}

	// 5. Corrupt content of v3 (same size, wrong SHA-256 hash)
	v3Entry, _ := idx.FindVariant("v3")
	v3CachedFile := filepath.Join(cacheDir, v3Entry.SHA256)
	corruptBytes := make([]byte, v3Entry.UncompressedSize)
	for i := range corruptBytes {
		corruptBytes[i] = 'X'
	}
	_ = os.WriteFile(v3CachedFile, corruptBytes, 0o755)

	resCorruptHash := VerifyCacheVariant(v3Entry, cacheDir)
	if resCorruptHash.Valid || resCorruptHash.Status != format.PrewarmStatusCorrupted {
		t.Errorf("expected status 'corrupted' for hash mismatch cache entry, got %+v", resCorruptHash)
	}

	// 6. VerifyCacheBinary with specific nonexistent variant level
	_, _, err = VerifyCacheBinary(f, stat.Size(), []string{"v99"}, cacheDir)
	if err == nil {
		t.Errorf("expected error for nonexistent variant level v99")
	}

	// 7. VerifyCacheBinary with empty cacheDir (uses resolved default)
	t.Setenv(format.EnvCacheDir, cacheDir)
	_, _, err = VerifyCacheBinary(f, stat.Size(), []string{"v1"}, "")
	if err != nil {
		t.Errorf("expected success with auto-resolved cacheDir: %v", err)
	}
	t.Setenv(format.EnvCacheDir, "")

	// 8. VerifyCacheVariant with invalid cacheDir
	resBadDir := VerifyCacheVariant(v1Entry, filepath.Join(tempDir, "nonexistent_parent", "sub"))
	if resBadDir.Valid || resBadDir.Status != format.PrewarmStatusMissing {
		t.Errorf("expected missing status for nonexistent cache directory: %+v", resBadDir)
	}

	// 9. VerifyCacheVariant unreadable file (open error)
	_ = os.Chmod(v1CachedFile, 0o000)
	defer func() { _ = os.Chmod(v1CachedFile, 0o755) }()
	resUnreadable := VerifyCacheVariant(v1Entry, cacheDir)
	if os.Getuid() != 0 { // Skip permission check if running as root in container
		if resUnreadable.Valid || resUnreadable.Status != format.PrewarmStatusCorrupted {
			t.Errorf("expected corrupted status for unreadable file: %+v", resUnreadable)
		}
	}

	// 10. VerifyCacheVariant and VerifyCacheBinary when cache resolution fails
	t.Setenv("XDG_CACHE_HOME", "/dev/null/forbidden_primary")
	t.Setenv("TMPDIR", "/dev/null/forbidden_secondary")
	resResolveErr := VerifyCacheVariant(v1Entry, "")
	if resResolveErr.Valid || resResolveErr.Status != format.PrewarmStatusMissing {
		t.Errorf("expected missing status when cache resolution fails: %+v", resResolveErr)
	}

	_, _, err = VerifyCacheBinary(f, stat.Size(), []string{"v1"}, "")
	if err == nil {
		t.Errorf("expected error from VerifyCacheBinary when cache resolution fails")
	}
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("TMPDIR", "")
}

func TestMultiCodecPackagingAndVerification(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "microfat-stub")
	stubContent := []byte("#!/bin/sh\necho Stub Launcher\n")
	if err := os.WriteFile(stubPath, stubContent, 0o755); err != nil {
		t.Fatalf("failed to write stub: %v", err)
	}

	// v1: tiny payload (100KB) -> under ProfileLatency should auto-promote to none
	v1Path := filepath.Join(tempDir, "bin-v1")
	v1Content := make([]byte, 100*1024)
	for i := range v1Content {
		v1Content[i] = byte(i % 256)
	}
	_ = os.WriteFile(v1Path, v1Content, 0o755)

	// v3: large payload (600KB) -> under ProfileLatency defaults to lz4
	v3Path := filepath.Join(tempDir, "bin-v3")
	v3Content := make([]byte, 600*1024)
	for i := range v3Content {
		v3Content[i] = byte((i % 256) ^ (i / 1024))
	}
	_ = os.WriteFile(v3Path, v3Content, 0o755)

	// v4: 200KB payload with explicit per-variant zstd:best
	v4Path := filepath.Join(tempDir, "bin-v4")
	v4Content := make([]byte, 200*1024)
	for i := range v4Content {
		v4Content[i] = byte((i * 7) % 256)
	}
	_ = os.WriteFile(v4Path, v4Content, 0o755)

	fatBinaryPath := filepath.Join(tempDir, "multi-codec-fat")
	opts := Options{
		StubPath:          stubPath,
		OutputPath:        fatBinaryPath,
		AppName:           "multi-codec-app",
		TargetOS:          testOSLinux,
		TargetArch:        testArchAMD64,
		SkipELFValidation: true,
		Profile:           "latency",
		VariantCompression: map[string]VariantCompressionOptions{
			"v4": {
				Compression: "zstd",
				Level:       "best",
			},
		},
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

	v1Entry, _ := idx.FindVariant("v1")
	if v1Entry.Compression != "none" {
		t.Errorf("expected v1 to auto-promote to 'none', got %q", v1Entry.Compression)
	}

	v3Entry, _ := idx.FindVariant("v3")
	if v3Entry.Compression != "lz4" {
		t.Errorf("expected v3 to default to 'lz4', got %q", v3Entry.Compression)
	}

	v4Entry, _ := idx.FindVariant("v4")
	if v4Entry.Compression != "zstd" {
		t.Errorf("expected v4 to override to 'zstd', got %q", v4Entry.Compression)
	}

	// Verify the binary
	f, err := os.Open(fatBinaryPath)
	if err != nil {
		t.Fatalf("failed to open fat binary: %v", err)
	}
	defer func() { _ = f.Close() }()

	stat, _ := f.Stat()
	_, results, err := VerifyBinary(f, stat.Size())
	if err != nil {
		t.Fatalf("VerifyBinary failed: %v", err)
	}
	for _, r := range results {
		if !r.Valid {
			t.Errorf("variant %s verification failed: %v", r.Level, r.Error)
		}
	}

	// Prewarm all variants
	cacheDir := filepath.Join(tempDir, "cache")
	_, prewarmResults, err := PrewarmBinary(f, stat.Size(), nil, cacheDir)
	if err != nil {
		t.Fatalf("PrewarmBinary failed: %v", err)
	}
	if len(prewarmResults) != 3 {
		t.Fatalf("expected 3 prewarm results, got %d", len(prewarmResults))
	}
	for _, pr := range prewarmResults {
		if !pr.Valid {
			t.Errorf("prewarm invalid for %s: %s", pr.Level, pr.Error)
		}
	}

	// Trim binary to v3 (lz4)
	trimmedPath := filepath.Join(tempDir, "trimmed-v3")
	trimmedFile, _ := os.Create(trimmedPath)
	trimmedIdx, err := TrimBinary(f, stat.Size(), "v3", trimmedFile)
	_ = trimmedFile.Close()
	if err != nil {
		t.Fatalf("TrimBinary failed: %v", err)
	}
	if len(trimmedIdx.Variants) != 1 || trimmedIdx.Variants[0].Compression != "lz4" {
		t.Errorf("trimmed variant compression mismatch: %+v", trimmedIdx.Variants)
	}

	// Test Pack with invalid profile
	badProfileOpts := opts
	badProfileOpts.Profile = "invalid_profile"
	badProfileOpts.OutputPath = filepath.Join(tempDir, "bad-profile-out")
	if _, err := Pack(badProfileOpts); err == nil {
		t.Errorf("expected error packing with invalid profile")
	}

	// Test VerifyBinary with unknown compression algorithm
	idxUnknownCodec := &format.Index{
		Version:     format.FormatVersionCurrent,
		CreatedUnix: 1000,
		Variants: []format.VariantEntry{
			{
				Level:            "v1",
				Offset:           0,
				CompressedSize:   10,
				UncompressedSize: 10,
				SHA256:           "hash",
				Compression:      "nonexistent_codec",
			},
		},
	}
	var bufUnknown bytes.Buffer
	_, _ = bufUnknown.Write(make([]byte, 10))
	_, _ = format.WriteIndexAndTrailer(&bufUnknown, idxUnknownCodec, 10)
	_, resUnknown, _ := VerifyBinary(bytes.NewReader(bufUnknown.Bytes()), int64(bufUnknown.Len()))
	if len(resUnknown) != 1 || resUnknown[0].Valid || resUnknown[0].Error == nil {
		t.Errorf("expected error on unknown codec in VerifyBinary: %+v", resUnknown)
	}

	// Test PrewarmVariant with unknown compression algorithm
	entryUnknown := &format.VariantEntry{
		Level:            "v1",
		Offset:           0,
		CompressedSize:   10,
		UncompressedSize: 10,
		SHA256:           "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Compression:      "nonexistent_codec",
	}
	if _, _, _, err := PrewarmVariant(bytes.NewReader(make([]byte, 10)), entryUnknown, cacheDir); err == nil {
		t.Errorf("expected error in PrewarmVariant with unknown codec")
	}

	// Test PrewarmVariant with decompression error
	entryCorrupted := &format.VariantEntry{
		Level:            "v1",
		Offset:           0,
		CompressedSize:   10,
		UncompressedSize: 10,
		SHA256:           "1111111111111111111111111111111111111111111111111111111111111111",
		Compression:      "lz4",
	}
	if _, _, _, err := PrewarmVariant(bytes.NewReader([]byte("not-valid-lz4-bytes")), entryCorrupted, cacheDir); err == nil {
		t.Errorf("expected error in PrewarmVariant with corrupted lz4 payload")
	}

	// Test PrewarmVariant with size mismatch (none codec with wrong size)
	entrySizeMismatch := &format.VariantEntry{
		Level:            "v1",
		Offset:           0,
		CompressedSize:   5,
		UncompressedSize: 10,
		SHA256:           "1111111111111111111111111111111111111111111111111111111111111111",
		Compression:      "none",
	}
	if _, _, _, err := PrewarmVariant(bytes.NewReader([]byte("12345")), entrySizeMismatch, cacheDir); err == nil {
		t.Errorf("expected error in PrewarmVariant with size mismatch")
	}

	// Test PrewarmVariant with hash mismatch (none codec with wrong hash)
	entryHashMismatch := &format.VariantEntry{
		Level:            "v1",
		Offset:           0,
		CompressedSize:   5,
		UncompressedSize: 5,
		SHA256:           "0000000000000000000000000000000000000000000000000000000000000000",
		Compression:      "none",
	}
	if _, _, _, err := PrewarmVariant(bytes.NewReader([]byte("12345")), entryHashMismatch, cacheDir); err == nil {
		t.Errorf("expected error in PrewarmVariant with hash mismatch")
	}
}

func createBenchmarkFixture(b *testing.B, variantCount int) (stubPath string, variants map[string]string, tempDir string) {
	b.Helper()
	tempDir = b.TempDir()
	stubPath = filepath.Join(tempDir, "stub")
	if err := os.WriteFile(stubPath, []byte("#!/bin/sh\necho Stub\n"), 0o755); err != nil {
		b.Fatalf("failed to create stub: %v", err)
	}

	variants = make(map[string]string, variantCount)
	for i := 1; i <= variantCount; i++ {
		lvl := "v" + strconv.Itoa(i)
		p := filepath.Join(tempDir, "bin-"+lvl)
		payload := bytes.Repeat([]byte("benchmark-payload-data-line-"+lvl+"\n"), 256)
		if err := os.WriteFile(p, payload, 0o755); err != nil {
			b.Fatalf("failed to create variant %s: %v", lvl, err)
		}
		variants[lvl] = p
	}
	return stubPath, variants, tempDir
}

func BenchmarkPack(b *testing.B) {
	for _, count := range []int{1, 4} {
		b.Run(strconv.Itoa(count)+"Variants", func(b *testing.B) {
			stubPath, variants, tempDir := createBenchmarkFixture(b, count)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				outPath := filepath.Join(tempDir, "out-"+strconv.Itoa(i))
				opts := Options{
					StubPath:          stubPath,
					OutputPath:        outPath,
					AppName:           "bench-app",
					TargetOS:          testOSLinux,
					TargetArch:        testArchAMD64,
					SkipELFValidation: true,
					CompressionLevel:  testCompressionFastest,
					Variants:          variants,
				}
				if _, err := Pack(opts); err != nil {
					b.Fatalf("Pack failed: %v", err)
				}
				_ = os.Remove(outPath)
			}
		})
	}
}

func BenchmarkVerifyBinary(b *testing.B) {
	stubPath, variants, tempDir := createBenchmarkFixture(b, 4)
	fatPath := filepath.Join(tempDir, "fat-verify")
	opts := Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "bench-app",
		TargetOS:          testOSLinux,
		TargetArch:        testArchAMD64,
		SkipELFValidation: true,
		CompressionLevel:  testCompressionFastest,
		Variants:          variants,
	}
	if _, err := Pack(opts); err != nil {
		b.Fatalf("Pack fixture failed: %v", err)
	}

	data, err := os.ReadFile(fatPath)
	if err != nil {
		b.Fatalf("reading fat fixture: %v", err)
	}
	reader := bytes.NewReader(data)
	totalSize := int64(len(data))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, results, err := VerifyBinary(reader, totalSize)
		if err != nil || len(results) != 4 {
			b.Fatalf("VerifyBinary failed: %v", err)
		}
	}
}

func BenchmarkTrimBinary(b *testing.B) {
	stubPath, variants, tempDir := createBenchmarkFixture(b, 4)
	fatPath := filepath.Join(tempDir, "fat-trim")
	opts := Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "bench-app",
		TargetOS:          testOSLinux,
		TargetArch:        testArchAMD64,
		SkipELFValidation: true,
		CompressionLevel:  testCompressionFastest,
		Variants:          variants,
	}
	if _, err := Pack(opts); err != nil {
		b.Fatalf("Pack fixture failed: %v", err)
	}

	data, err := os.ReadFile(fatPath)
	if err != nil {
		b.Fatalf("reading fat fixture: %v", err)
	}
	reader := bytes.NewReader(data)
	totalSize := int64(len(data))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := TrimBinary(reader, totalSize, "v3", io.Discard)
		if err != nil {
			b.Fatalf("TrimBinary failed: %v", err)
		}
	}
}

func BenchmarkPrewarmBinary(b *testing.B) {
	stubPath, variants, tempDir := createBenchmarkFixture(b, 4)
	fatPath := filepath.Join(tempDir, "fat-prewarm")
	opts := Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "bench-app",
		TargetOS:          testOSLinux,
		TargetArch:        testArchAMD64,
		SkipELFValidation: true,
		CompressionLevel:  testCompressionFastest,
		Variants:          variants,
	}
	if _, err := Pack(opts); err != nil {
		b.Fatalf("Pack fixture failed: %v", err)
	}

	data, err := os.ReadFile(fatPath)
	if err != nil {
		b.Fatalf("reading fat fixture: %v", err)
	}
	reader := bytes.NewReader(data)
	totalSize := int64(len(data))

	cacheDir := filepath.Join(tempDir, "cache")
	// Prewarm once to populate cache
	if _, _, err := PrewarmBinary(reader, totalSize, nil, cacheDir); err != nil {
		b.Fatalf("prewarming failed: %v", err)
	}

	b.Run("CachedHit", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, results, err := PrewarmBinary(reader, totalSize, nil, cacheDir)
			if err != nil || len(results) != 4 {
				b.Fatalf("PrewarmBinary failed: %v", err)
			}
		}
	})

	b.Run("VerifyCacheBinary", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, results, err := VerifyCacheBinary(reader, totalSize, nil, cacheDir)
			if err != nil || len(results) != 4 {
				b.Fatalf("VerifyCacheBinary failed: %v", err)
			}
		}
	})
}

func TestDictionaryPackingAndVerification(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("DUMMY_STUB_HEADER_DATA_1234567890"), 0o755)

	// Create 4 variants with substantial shared content (like real Go ELF binaries)
	varPaths := make(map[string]string)
	for _, lvl := range []string{"v1", "v2", "v3", "v4"} {
		p := filepath.Join(tempDir, "app_"+lvl)
		var buf bytes.Buffer
		for i := 0; i < 1200; i++ {
			buf.WriteString(fmt.Sprintf("runtime_metadata_symbol_entry_%04d_hash_%x\n", i, (i*43)^0xA5A5A5A5))
		}
		buf.WriteString(fmt.Sprintf("variant_specific_code_segment_%s_optimization_pass\n", lvl))
		_ = os.WriteFile(p, buf.Bytes(), 0o755)
		varPaths[lvl] = p
	}

	fatWithoutDict := filepath.Join(tempDir, "fat_no_dict")
	optsNoDict := Options{
		StubPath:          stubPath,
		OutputPath:        fatWithoutDict,
		AppName:           "dict-test-app",
		TargetOS:          testOSLinux,
		TargetArch:        testArchAMD64,
		Variants:          varPaths,
		EnableDict:        false,
		SkipELFValidation: true,
	}
	idxNoDict, err := Pack(optsNoDict)
	if err != nil {
		t.Fatalf("Pack without dict failed: %v", err)
	}
	if idxNoDict.DictionarySize != 0 {
		t.Errorf("expected 0 DictionarySize for no-dict pack, got %d", idxNoDict.DictionarySize)
	}
	statNoDict, err := os.Stat(fatWithoutDict)
	if err != nil {
		t.Fatalf("stat no dict failed: %v", err)
	}

	fatWithDict := filepath.Join(tempDir, "fat_with_dict")
	optsWithDict := Options{
		StubPath:          stubPath,
		OutputPath:        fatWithDict,
		AppName:           "dict-test-app",
		TargetOS:          testOSLinux,
		TargetArch:        testArchAMD64,
		Variants:          varPaths,
		EnableDict:        true,
		DictSize:          32 * 1024,
		SkipELFValidation: true,
	}
	idxWithDict, err := Pack(optsWithDict)
	if err != nil {
		t.Fatalf("Pack with dict failed: %v", err)
	}
	statWithDict, err := os.Stat(fatWithDict)
	if err != nil {
		t.Fatalf("stat with dict failed: %v", err)
	}

	if idxWithDict.DictionarySize <= 0 {
		t.Errorf("expected DictionarySize > 0, got %d", idxWithDict.DictionarySize)
	}
	if idxWithDict.DictionaryOffset <= 0 {
		t.Errorf("expected DictionaryOffset > 0, got %d", idxWithDict.DictionaryOffset)
	}
	if idxWithDict.DictionarySHA256 == "" {
		t.Errorf("expected non-empty DictionarySHA256")
	}

	// Verify size reduction
	t.Logf("Fat without dict: %d bytes | Fat with dict: %d bytes (savings: %.1f%%)",
		statNoDict.Size(), statWithDict.Size(),
		(1.0-float64(statWithDict.Size())/float64(statNoDict.Size()))*100)

	// Verify binary integrity
	fDict, err := os.Open(fatWithDict)
	if err != nil {
		t.Fatalf("open fatWithDict: %v", err)
	}
	defer func() { _ = fDict.Close() }()

	vIdx, results, err := VerifyBinary(fDict, statWithDict.Size())
	if err != nil {
		t.Fatalf("VerifyBinary failed: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	for _, r := range results {
		if !r.Valid || r.Error != nil {
			t.Errorf("expected valid verification for %s: %v", r.Level, r.Error)
		}
	}
	if vIdx.DictionarySize != idxWithDict.DictionarySize {
		t.Errorf("VerifyBinary returned wrong dictionary size")
	}

	// Verify TrimBinary on dictionary-backed fat binary
	trimmedPath := filepath.Join(tempDir, "trimmed_v3")
	trimmedFile, err := os.Create(trimmedPath)
	if err != nil {
		t.Fatalf("create trimmed file: %v", err)
	}
	trimIdx, err := TrimBinary(fDict, statWithDict.Size(), "v3", trimmedFile)
	_ = trimmedFile.Close()
	if err != nil {
		t.Fatalf("TrimBinary on dict binary failed: %v", err)
	}
	if trimIdx.DictionarySize != idxWithDict.DictionarySize {
		t.Errorf("Trimmed binary lost dictionary size: %d", trimIdx.DictionarySize)
	}

	trimmedOpen, err := os.Open(trimmedPath)
	if err != nil {
		t.Fatalf("open trimmed binary: %v", err)
	}
	defer func() { _ = trimmedOpen.Close() }()
	trimStat, _ := trimmedOpen.Stat()
	_, trimResults, err := VerifyBinary(trimmedOpen, trimStat.Size())
	if err != nil || len(trimResults) != 1 || !trimResults[0].Valid {
		t.Fatalf("verification of trimmed dict binary failed: %v (results: %v)", err, trimResults)
	}

	// Verify PrewarmBinary on dictionary-backed fat binary
	cacheDir := filepath.Join(tempDir, "prewarm_cache")
	_, prewarmRes, err := PrewarmBinary(fDict, statWithDict.Size(), []string{"v1", "v4"}, cacheDir)
	if err != nil {
		t.Fatalf("PrewarmBinary failed on dict binary: %v", err)
	}
	if len(prewarmRes) != 2 {
		t.Fatalf("expected 2 prewarm results, got %d", len(prewarmRes))
	}
	for _, pr := range prewarmRes {
		if !pr.Valid {
			t.Errorf("prewarm invalid for %s: %s", pr.Level, pr.Error)
		}
	}
}

func TestDictionaryCorruptedVerification(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("DUMMY_STUB_HEADER_DATA_1234567890"), 0o755)

	varPaths := make(map[string]string)
	for _, lvl := range []string{"v1", "v2"} {
		p := filepath.Join(tempDir, "app_"+lvl)
		var buf bytes.Buffer
		for i := 0; i < 1200; i++ {
			buf.WriteString(fmt.Sprintf("sample_payload_for_corruption_test_entry_%04d_hash_%x\n", i, (i*37)^0x5A5A5A5A))
		}
		buf.WriteString(fmt.Sprintf("level_specific_data_%s\n", lvl))
		_ = os.WriteFile(p, buf.Bytes(), 0o755)
		varPaths[lvl] = p
	}

	fatPath := filepath.Join(tempDir, "fat_dict")
	opts := Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "corrupt-test",
		TargetOS:          testOSLinux,
		TargetArch:        testArchAMD64,
		Variants:          varPaths,
		EnableDict:        true,
		SkipELFValidation: true,
	}
	idx, err := Pack(opts)
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	data, err := os.ReadFile(fatPath)
	if err != nil {
		t.Fatalf("reading fat binary: %v", err)
	}

	// Tamper with dictionary bytes
	tamperedData := make([]byte, len(data))
	copy(tamperedData, data)
	if idx.DictionarySize > 0 {
		tamperedData[idx.DictionaryOffset+10] ^= 0xFF
	}

	tamperedReader := bytes.NewReader(tamperedData)
	_, _, err = VerifyBinary(tamperedReader, int64(len(tamperedData)))
	if err == nil {
		t.Fatalf("expected error when dictionary is corrupted")
	}

	// Also verify PrewarmBinary fails on corrupted dictionary
	cacheDir := filepath.Join(tempDir, "cache_corrupt")
	_, _, err = PrewarmBinary(tamperedReader, int64(len(tamperedData)), nil, cacheDir)
	if err == nil {
		t.Fatalf("expected PrewarmBinary to fail on corrupted dictionary")
	}
}

func TestSampleVariantPayloadsEdgeCases(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	p1 := filepath.Join(tempDir, "p1")
	p2 := filepath.Join(tempDir, "p2")

	_ = os.WriteFile(p1, make([]byte, 1024), 0o644)
	_ = os.WriteFile(p2, make([]byte, 500*1024), 0o644)

	variants := map[string]string{"v1": p1, "v2": p2}
	samples, err := sampleVariantPayloads(variants, []string{"v1", "v2"})
	if err != nil {
		t.Fatalf("sampleVariantPayloads failed: %v", err)
	}
	if len(samples) == 0 {
		t.Fatalf("expected non-empty samples")
	}

	// Nonexistent file error
	_, err = sampleVariantPayloads(map[string]string{"v1": filepath.Join(tempDir, "missing")}, []string{"v1"})
	if err == nil {
		t.Fatalf("expected error for missing variant file in sampleVariantPayloads")
	}
}





