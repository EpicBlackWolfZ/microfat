package pack

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

func TestTruncatedTrailersAtVaryingOffsets(t *testing.T) {
	t.Parallel()

	stubPath, variants, tempDir := createTestFixtures(t)
	outFat := filepath.Join(tempDir, "truncated.fat")

	opts := DefaultOptions()
	opts.StubPath = stubPath
	opts.OutputPath = outFat
	opts.Variants = variants
	opts.TargetOS = testOSLinux
	opts.TargetArch = testArchAMD64
	opts.Compression = codec.AlgorithmZstd
	opts.SkipELFValidation = true

	_, err := Pack(opts)
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	fullData, err := os.ReadFile(outFat)
	if err != nil {
		t.Fatalf("failed to read packed binary: %v", err)
	}

	// Truncate from 1 byte up to full trailer size + 10 bytes from EOF
	for truncateBytes := 1; truncateBytes <= format.TrailerSize+10; truncateBytes++ {
		truncatedLen := len(fullData) - truncateBytes
		if truncatedLen <= 0 {
			break
		}
		truncatedData := fullData[:truncatedLen]
		reader := bytes.NewReader(truncatedData)

		_, _, vErr := VerifyBinary(reader, int64(len(truncatedData)))
		if vErr == nil {
			t.Fatalf("expected error on truncated binary (-%d bytes), got nil", truncateBytes)
		}
	}
}

func TestBitFlippedPayloadIntegrity(t *testing.T) {
	t.Parallel()

	stubPath, variants, tempDir := createTestFixtures(t)
	outFat := filepath.Join(tempDir, "bitflip.fat")

	opts := DefaultOptions()
	opts.StubPath = stubPath
	opts.OutputPath = outFat
	opts.Variants = variants
	opts.TargetOS = testOSLinux
	opts.TargetArch = testArchAMD64
	opts.Compression = codec.AlgorithmZstd
	opts.SkipELFValidation = true

	idx, err := Pack(opts)
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	fullData, err := os.ReadFile(outFat)
	if err != nil {
		t.Fatalf("failed to read packed binary: %v", err)
	}

	// Target variant 0 payload
	v0 := idx.Variants[0]
	flipOffset := v0.Offset + (v0.CompressedSize / 2)

	corruptedData := make([]byte, len(fullData))
	copy(corruptedData, fullData)
	corruptedData[flipOffset] ^= 0xFF // Flip bits

	reader := bytes.NewReader(corruptedData)
	_, results, vErr := VerifyBinary(reader, int64(len(corruptedData)))
	// VerifyBinary should either return an error or mark the variant invalid
	if vErr == nil {
		hasFailure := false
		for _, res := range results {
			if !res.Valid || res.Error != nil {
				hasFailure = true
				break
			}
		}
		if !hasFailure {
			t.Fatalf("expected verification failure for bit-flipped payload at offset %d", flipOffset)
		}
	}
}

func TestDictionaryTampering(t *testing.T) {
	t.Parallel()

	stubPath, variants, tempDir := createTestFixtures(t)
	outFat := filepath.Join(tempDir, "dict_tamper.fat")

	opts := DefaultOptions()
	opts.StubPath = stubPath
	opts.OutputPath = outFat
	opts.Variants = variants
	opts.TargetOS = testOSLinux
	opts.TargetArch = testArchAMD64
	opts.Compression = codec.AlgorithmZstd
	opts.SkipELFValidation = true
	opts.EnableDict = true
	opts.DictSize = 1024

	idx, err := Pack(opts)
	if err != nil {
		t.Fatalf("Pack with dictionary failed: %v", err)
	}

	if idx.DictionarySize == 0 {
		t.Skip("dictionary training skipped for small fixtures")
	}

	fullData, err := os.ReadFile(outFat)
	if err != nil {
		t.Fatalf("failed to read packed binary: %v", err)
	}

	// Tamper dictionary bytes
	corruptedData := make([]byte, len(fullData))
	copy(corruptedData, fullData)
	dictOffset := idx.DictionaryOffset + (idx.DictionarySize / 2)
	corruptedData[dictOffset] ^= 0xAA

	reader := bytes.NewReader(corruptedData)
	_, _, vErr := VerifyBinary(reader, int64(len(corruptedData)))
	if vErr == nil {
		t.Fatal("expected error on tampered dictionary checksum, got nil")
	}
	if !errors.Is(vErr, format.ErrDictionaryCorrupted) {
		t.Fatalf("expected ErrDictionaryCorrupted, got: %v", vErr)
	}
}
