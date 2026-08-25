package pack

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/klauspost/compress/zstd"
)

const (
	testOSLinux   = "linux"
	testArchAMD64 = "amd64"
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
		CompressionLevel:  zstd.SpeedFastest,
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

	// Error case: writeVariantPayload with closed file
	validPayload := filepath.Join(tempDir, "valid_payload")
	_ = os.WriteFile(validPayload, []byte("payload"), 0o644)
	if _, _, err := writeVariantPayload(tmp3, "v1", validPayload, zstd.SpeedDefault, 0); err == nil {
		t.Errorf("expected error writing payload to closed file")
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
