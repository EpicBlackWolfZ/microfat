package e2e_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	bogusMagicString             = "\x00\xFA\x7FBOGUS"
	trailerOffsetIndexOffset     = format.TrailerSize
	trailerOffsetIndexSize       = format.TrailerSize - 8
	trailerOffsetIndexSHA        = format.TrailerSize - 16
	invalidOffsetBeyondEOF       = 999999999
	invalidSmallSize             = 1
	tamperedPayloadOffsetPadding = 100
	tamperedDictOffsetPadding    = 16
	tamperedInvalidSize          = 10
)

func TestCorruptionAndSecurityBoundary(t *testing.T) {
	t.Parallel()

	t.Run("Scenario10_MutatedMagicTrailer", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "corrupt_magic.fat")
		fileSize := copyFile(t, goldenFatBin, corruptPath)

		// Mutate 8-byte trailer magic at EOF
		magicOffset := fileSize - trailerMagicSizeBytes
		mutateFileBytes(t, corruptPath, magicOffset, []byte(bogusMagicString))

		_, stderr, exitCode, err := executeFatBinary(t, corruptPath, nil)
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected failure for corrupted trailer magic, but execution succeeded")
		}
		if !strings.Contains(stderr, "missing magic trailer") && !strings.Contains(stderr, "not a valid microfat fat binary") {
			t.Fatalf("expected missing magic trailer error in stderr (exitCode %d, err: %v), got:\n%s", exitCode, err, stderr)
		}
	})

	t.Run("Scenario11_MutatedIndexOffset", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "corrupt_offset.fat")
		fileSize := copyFile(t, goldenFatBin, corruptPath)

		// Mutate uint64 index offset at fileSize - 56
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, uint64(fileSize+invalidOffsetBeyondEOF))
		mutateFileBytes(t, corruptPath, fileSize-trailerOffsetIndexOffset, buf)

		_, stderr, exitCode, err := executeFatBinary(t, corruptPath, nil)
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected failure for out-of-bounds index offset, but execution succeeded")
		}
		if !strings.Contains(stderr, "reading binary manifest") && !strings.Contains(stderr, "out of bounds") && exitCode == 0 {
			t.Fatalf("expected index offset error diagnostics, got:\n%s", stderr)
		}
	})

	t.Run("Scenario12_MutatedIndexSize", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "corrupt_size.fat")
		fileSize := copyFile(t, goldenFatBin, corruptPath)

		// Mutate uint64 index size at fileSize - 48 to 1 byte
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, invalidSmallSize)
		mutateFileBytes(t, corruptPath, fileSize-trailerOffsetIndexSize, buf)

		_, stderr, exitCode, err := executeFatBinary(t, corruptPath, nil)
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected failure for truncated index size, but execution succeeded")
		}
		if exitCode == 0 {
			t.Fatalf("expected non-zero exit code, stderr: %s", stderr)
		}
	})

	t.Run("Scenario13_MutatedIndexBytes", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "corrupt_index_bytes.fat")
		_ = copyFile(t, goldenFatBin, corruptPath)

		trailer, _ := readTrailerAndIndex(t, corruptPath)

		// Flip a byte in the middle of the serialized index table
		mutateFileBytes(t, corruptPath, trailer.IndexOffset+8, []byte{0xFF})

		_, stderr, exitCode, err := executeFatBinary(t, corruptPath, nil)
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected failure for tampered index bytes, but execution succeeded")
		}
		if !strings.Contains(stderr, "index checksum mismatch") && !strings.Contains(stderr, "reading binary manifest") {
			t.Fatalf("expected index checksum error diagnostics, got:\n%s", stderr)
		}
	})

	t.Run("Scenario14_MutatedPayloadBytes", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "corrupt_payload.fat")
		_ = copyFile(t, goldenFatBin, corruptPath)

		_, idx := readTrailerAndIndex(t, corruptPath)
		entry, found := idx.FindVariant(currentHostLevel)
		if !found {
			entry = &idx.Variants[0]
		}

		// Mutate a byte in the payload stream of the selected variant
		payloadTarget := entry.Offset + tamperedPayloadOffsetPadding
		mutateFileBytes(t, corruptPath, payloadTarget, []byte{0xEE})

		_, stderr, exitCode, err := executeFatBinary(t, corruptPath, []string{envDebugTrue})
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected failure for corrupted payload stream, but execution succeeded")
		}
		if !strings.Contains(stderr, "corrupted") && !strings.Contains(stderr, "checksum") &&
			!strings.Contains(stderr, "error") && !strings.Contains(stderr, "decompress") {
			t.Fatalf("expected payload corruption error diagnostics (exitCode %d, err %v), got:\n%s", exitCode, err, stderr)
		}
	})

	t.Run("Scenario15_MutatedMetadataWithRecomputedTrailerChecksum", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "tampered_meta_recomputed.fat")
		_ = copyFile(t, goldenFatBin, corruptPath)

		trailer, idx := readTrailerAndIndex(t, corruptPath)
		entry, found := idx.FindVariant(currentHostLevel)
		if !found {
			entry = &idx.Variants[0]
		}

		// Maliciously tamper with uncompressed size in metadata
		entry.UncompressedSize = tamperedInvalidSize

		// Truncate file back to index offset and re-write index and valid trailer checksum
		f, err := os.OpenFile(corruptPath, os.O_RDWR, 0)
		if err != nil {
			t.Fatalf("open %s: %v", corruptPath, err)
		}

		if err := f.Truncate(trailer.IndexOffset); err != nil {
			_ = f.Close()
			t.Fatalf("truncating %s: %v", corruptPath, err)
		}
		if _, err := f.Seek(trailer.IndexOffset, ioSeekStart); err != nil {
			_ = f.Close()
			t.Fatalf("seeking in %s: %v", corruptPath, err)
		}

		if _, err := format.WriteIndexAndTrailer(f, idx, trailer.IndexOffset); err != nil {
			_ = f.Close()
			t.Fatalf("writing tampered index with valid trailer: %v", err)
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			t.Fatalf("syncing %s: %v", corruptPath, err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("closing %s: %v", corruptPath, err)
		}

		// Outer trailer checksum is valid, but internal execution boundary must reject decompression/size mismatch
		_, stderr, exitCode, err := executeFatBinary(t, corruptPath, []string{envDebugTrue})
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected failure for tampered metadata with valid outer checksum, but succeeded")
		}
		if !strings.Contains(stderr, "decompression") && !strings.Contains(stderr, "size") &&
			!strings.Contains(stderr, "corrupted") && !strings.Contains(stderr, "error") {
			t.Fatalf("expected inner validation error, got:\n%s", stderr)
		}
	})

	t.Run("Scenario16_SharedDictionaryTampering", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "corrupt_dict.fat")
		_ = copyFile(t, goldenDictFatBin, corruptPath)

		_, idx := readTrailerAndIndex(t, corruptPath)
		if idx.DictionarySize <= 0 {
			t.Skipf("dictionary size is 0, skipping dictionary tamper test")
		}

		// Mutate a byte inside the shared dictionary region
		dictTarget := idx.DictionaryOffset + tamperedDictOffsetPadding
		mutateFileBytes(t, corruptPath, dictTarget, []byte{0xCC})

		_, stderr, exitCode, err := executeFatBinary(t, corruptPath, []string{envDebugTrue})
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected failure for tampered shared dictionary, but execution succeeded")
		}
		if !strings.Contains(stderr, "dictionary") && !strings.Contains(stderr, "corrupted") && !strings.Contains(stderr, "error") {
			t.Fatalf("expected dictionary corruption error diagnostics, got:\n%s", stderr)
		}
	})

	t.Run("Scenario17_PlausibleMalformedSemanticIndexWithValidTrailerChecksum", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "malformed_semantic_index.fat")
		_ = copyFile(t, goldenFatBin, corruptPath)

		trailer, idx := readTrailerAndIndex(t, corruptPath)

		// Create a structurally plausible index with invalid semantics: overlapping variant payload offsets
		if len(idx.Variants) >= 2 {
			idx.Variants[1].Offset = idx.Variants[0].Offset + 8 // overlapping payload
		}

		f, err := os.OpenFile(corruptPath, os.O_RDWR, 0)
		if err != nil {
			t.Fatalf("open %s: %v", corruptPath, err)
		}

		if err := f.Truncate(trailer.IndexOffset); err != nil {
			_ = f.Close()
			t.Fatalf("truncating %s: %v", corruptPath, err)
		}
		if _, err := f.Seek(trailer.IndexOffset, ioSeekStart); err != nil {
			_ = f.Close()
			t.Fatalf("seeking in %s: %v", corruptPath, err)
		}

		if _, err := format.WriteIndexAndTrailer(f, idx, trailer.IndexOffset); err != nil {
			_ = f.Close()
			t.Fatalf("writing malformed semantic index with valid trailer: %v", err)
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			t.Fatalf("syncing %s: %v", corruptPath, err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("closing %s: %v", corruptPath, err)
		}

		// Outer checksum matches, but semantic validation must reject overlapping payloads
		_, stderr, exitCode, err := executeFatBinary(t, corruptPath, []string{envDebugTrue})
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected failure for overlapping variant payload index, but execution succeeded")
		}
		if !strings.Contains(stderr, "invalid") && !strings.Contains(stderr, "overlap") &&
			!strings.Contains(stderr, "bounds") && !strings.Contains(stderr, "error") {
			t.Fatalf("expected semantic validation error diagnostics (exitCode %d, err %v), got:\n%s", exitCode, err, stderr)
		}
	})
}

const ioSeekStart = 0
