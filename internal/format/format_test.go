package format

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
)

const (
	testCompression = "zstd"
	testArchAMD64   = "amd64"
	testOSLinux     = "linux"
)

func TestFormatRoundTrip(t *testing.T) {
	buf := bytes.NewBuffer(make([]byte, 1024))
	initialOffset := int64(1024)

	originalIdx := &Index{
		AppName:     "testapp",
		TargetOS:    testOSLinux,
		TargetArch:  testArchAMD64,
		CreatedUnix: 1724540000,
		Variants: []VariantEntry{
			{
				Level:            "v1",
				Offset:           200,
				CompressedSize:   300,
				UncompressedSize: 800,
				SHA256:           "abcdef123456",
				Compression:      testCompression,
			},
			{
				Level:            "v3",
				Offset:           500,
				CompressedSize:   400,
				UncompressedSize: 900,
				SHA256:           "123456abcdef",
				Compression:      testCompression,
			},
		},
	}

	writtenBytes, err := WriteIndexAndTrailer(buf, originalIdx, initialOffset)
	if err != nil {
		t.Fatalf("WriteIndexAndTrailer failed: %v", err)
	}

	data := buf.Bytes()
	totalSize := int64(len(data))
	if totalSize != initialOffset+writtenBytes {
		t.Fatalf("totalSize %d != initialOffset+writtenBytes %d", totalSize, initialOffset+writtenBytes)
	}

	reader := bytes.NewReader(data)
	if !IsFatBinary(reader, totalSize) {
		t.Errorf("expected IsFatBinary to be true")
	}

	readIdx, err := ReadTrailerAndIndex(reader, totalSize)
	if err != nil {
		t.Fatalf("ReadTrailerAndIndex failed: %v", err)
	}

	if readIdx.AppName != originalIdx.AppName {
		t.Errorf("expected AppName %s, got %s", originalIdx.AppName, readIdx.AppName)
	}
	if readIdx.TargetOS != originalIdx.TargetOS || readIdx.TargetArch != originalIdx.TargetArch {
		t.Errorf("target OS/Arch mismatch: got %s/%s", readIdx.TargetOS, readIdx.TargetArch)
	}
	if len(readIdx.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(readIdx.Variants))
	}
	if readIdx.Variants[0].Level != "v1" || readIdx.Variants[1].Level != "v3" {
		t.Errorf("variant levels mismatch: %v", readIdx.Variants)
	}

	levels := readIdx.VariantLevels()
	if len(levels) != 2 || levels[0] != "v1" || levels[1] != "v3" {
		t.Errorf("VariantLevels returned unexpected slice: %v", levels)
	}

	v3, found := readIdx.FindVariant("v3")
	if !found || v3.UncompressedSize != 900 {
		t.Errorf("FindVariant(v3) failed or had incorrect size: %+v", v3)
	}

	_, foundMissing := readIdx.FindVariant("v4")
	if foundMissing {
		t.Errorf("expected FindVariant(v4) to be false")
	}
}

func TestReadTrailerErrors(t *testing.T) {
	readerSmall := bytes.NewReader([]byte("too small"))
	_, err := ReadTrailerAndIndex(readerSmall, int64(readerSmall.Len()))
	if !errors.Is(err, ErrBinaryTooSmall) {
		t.Errorf("expected ErrBinaryTooSmall, got %v", err)
	}

	// Corrupt magic
	corruptMagic := make([]byte, TrailerSize+100)
	readerCorruptMagic := bytes.NewReader(corruptMagic)
	_, err = ReadTrailerAndIndex(readerCorruptMagic, int64(len(corruptMagic)))
	if !errors.Is(err, ErrInvalidMagic) {
		t.Errorf("expected ErrInvalidMagic, got %v", err)
	}

	// Valid magic but corrupted index hash
	buf := bytes.NewBuffer(make([]byte, 500))
	idx := &Index{
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: []VariantEntry{
			{Level: "v1", Offset: 100, CompressedSize: 200, UncompressedSize: 400, Compression: testCompression},
		},
	}
	_, err = WriteIndexAndTrailer(buf, idx, 500)
	if err != nil {
		t.Fatalf("WriteIndexAndTrailer failed: %v", err)
	}
	data := buf.Bytes()
	// Tamper with index json
	data[505] ^= 0xFF
	tamperedReader := bytes.NewReader(data)
	_, err = ReadTrailerAndIndex(tamperedReader, int64(len(data)))
	if !errors.Is(err, ErrIndexCorrupted) {
		t.Errorf("expected ErrIndexCorrupted, got %v", err)
	}
}

func TestValidateBoundsErrors(t *testing.T) {
	idxOutOfBounds := &Index{
		Version:    FormatVersionCurrent,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: []VariantEntry{
			{
				Level:            "v1",
				Offset:           1000,
				CompressedSize:   500,
				UncompressedSize: 1000,
				Compression:      testCompression,
			},
		},
	}

	err := idxOutOfBounds.ValidateBounds(1200)
	if !errors.Is(err, ErrOutOfBounds) {
		t.Errorf("expected ErrOutOfBounds, got %v", err)
	}

	idxTooLarge := &Index{
		Version:    FormatVersionCurrent,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: []VariantEntry{
			{
				Level:            "v1",
				Offset:           100,
				CompressedSize:   200,
				UncompressedSize: MaxPayloadSize + 1,
				Compression:      testCompression,
			},
		},
	}
	err = idxTooLarge.ValidateBounds(1200)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("expected ErrPayloadTooLarge, got %v", err)
	}

	idxOverlapping := &Index{
		Version:    FormatVersionCurrent,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: []VariantEntry{
			{
				Level:            "v1",
				Offset:           100,
				CompressedSize:   300,
				UncompressedSize: 500,
				Compression:      testCompression,
			},
			{
				Level:            "v3",
				Offset:           250, // Overlaps with v1 (ends at 400)
				CompressedSize:   200,
				UncompressedSize: 500,
				Compression:      testCompression,
			},
		},
	}
	err = idxOverlapping.ValidateBounds(1200)
	if !errors.Is(err, ErrOverlappingVariant) {
		t.Errorf("expected ErrOverlappingVariant, got %v", err)
	}
}

func TestIsFatBinary(t *testing.T) {
	if IsFatBinary(bytes.NewReader([]byte("short")), 5) {
		t.Errorf("expected short binary to return false")
	}

	buf := make([]byte, TrailerSize)
	copy(buf[TrailerSize-MagicLen:], []byte(MagicString))
	if !IsFatBinary(bytes.NewReader(buf), TrailerSize) {
		t.Errorf("expected valid trailer to return true")
	}
}

func TestTrailerOffsetAlignment(t *testing.T) {
	trailer := make([]byte, TrailerSize)
	binary.LittleEndian.PutUint64(trailer[0:OffsetLen], 100)
	binary.LittleEndian.PutUint64(trailer[OffsetLen:OffsetLen*2], 50)
	hash := sha256.Sum256([]byte("{}"))
	copy(trailer[OffsetLen*2:OffsetLen*2+HashLen], hash[:])
	copy(trailer[OffsetLen*2+HashLen:], []byte(MagicString))

	data := make([]byte, 200+TrailerSize)
	copy(data[200:], trailer)

	_, err := ReadTrailerAndIndex(bytes.NewReader(data), int64(len(data)))
	if !errors.Is(err, ErrInvalidIndexSize) {
		t.Errorf("expected ErrInvalidIndexSize, got %v", err)
	}
}
