package pack

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func FuzzValidateELFBinary(f *testing.F) {
	// Seed with 64-bit ELF magic header (AMD64)
	validAMD64 := []byte{
		0x7f, 'E', 'L', 'F',
		2, 1, 1, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
		2, 0, // EXEC
		62, 0, // EM_X86_64
		1, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 64, 0, 0, 0,
		0, 0, 0, 0,
	}
	f.Add(validAMD64, "linux", "amd64")

	// Seed 32-bit ELF (should be rejected)
	valid32 := []byte{
		0x7f, 'E', 'L', 'F',
		1, 1, 1, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
	}
	f.Add(valid32, "linux", "amd64")

	// Seed arbitrary garbage
	f.Add([]byte("not an elf binary"), "linux", "amd64")
	f.Add([]byte{}, "linux", "arm64")

	f.Fuzz(func(t *testing.T, elfData []byte, targetOS, targetArch string) {
		tempDir := t.TempDir()
		binPath := filepath.Join(tempDir, "test.elf")
		if err := os.WriteFile(binPath, elfData, 0o755); err != nil {
			return
		}

		_ = ValidateELFBinary(binPath, targetOS, targetArch)
	})
}

func FuzzVerifyBinary(f *testing.F) {
	// Seed with synthetic valid fat binary from pack_test
	stubPath, variants, tempDir := createTestFixtures(f)
	outFat := filepath.Join(tempDir, "seed.fat")

	opts := DefaultOptions()
	opts.StubPath = stubPath
	opts.OutputPath = outFat
	opts.Variants = variants
	opts.TargetOS = testOSLinux
	opts.TargetArch = testArchAMD64
	opts.Compression = testCompressionZstd

	if _, err := Pack(opts); err == nil {
		if data, rErr := os.ReadFile(outFat); rErr == nil {
			f.Add(data)
		}
	}

	// Seed corrupt data
	f.Add([]byte("corrupt-fat-binary-payload"))
	f.Add(make([]byte, 128))

	f.Fuzz(func(t *testing.T, fatBinaryData []byte) {
		reader := bytes.NewReader(fatBinaryData)
		_, _, _ = VerifyBinary(reader, int64(len(fatBinaryData)))
	})
}

func createTestFixtures(tb testing.TB) (string, map[string]string, string) {
	tempDir := tb.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	v1Path := filepath.Join(tempDir, "v1")
	v2Path := filepath.Join(tempDir, "v2")

	elfHeader := []byte{
		0x7f, 'E', 'L', 'F',
		2, 1, 1, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
		2, 0,
		62, 0,
		1, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 64, 0, 0, 0,
		0, 0, 0, 0,
	}

	if err := os.WriteFile(stubPath, elfHeader, 0o755); err != nil {
		tb.Fatalf("failed to write stub: %v", err)
	}
	if err := os.WriteFile(v1Path, append(elfHeader, []byte("payload-v1")...), 0o755); err != nil {
		tb.Fatalf("failed to write v1: %v", err)
	}
	if err := os.WriteFile(v2Path, append(elfHeader, []byte("payload-v2")...), 0o755); err != nil {
		tb.Fatalf("failed to write v2: %v", err)
	}

	return stubPath, map[string]string{"v1": v1Path, "v2": v2Path}, tempDir
}
