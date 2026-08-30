package format

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"testing"
)

func FuzzUnmarshalBinaryIndex(f *testing.F) {
	// Seed 1: Valid binary index from round-trip
	seedIdx := &Index{
		Version:     FormatVersion2,
		AppName:     "fuzz-app",
		TargetOS:    testOSLinux,
		TargetArch:  testArchAMD64,
		CreatedUnix: 1724540000,
		Variants: []VariantEntry{
			{
				Level:            "v1",
				Offset:           100,
				CompressedSize:   200,
				UncompressedSize: 500,
				SHA256:           testSHA256Sample,
				Compression:      testCompression,
			},
			{
				Level:            "v3",
				Offset:           300,
				CompressedSize:   250,
				UncompressedSize: 600,
				SHA256:           testSHA256Sample2,
				Compression:      "lz4",
			},
		},
	}
	seedBytes, err := MarshalBinaryIndex(seedIdx)
	if err == nil {
		f.Add(seedBytes)
	}

	// Seed 2: Binary index with dictionary
	seedDictIdx := &Index{
		Version:          FormatVersion2,
		AppName:          "fuzz-dict-app",
		TargetOS:         testOSLinux,
		TargetArch:       "arm64",
		CreatedUnix:      1724545000,
		DictionaryOffset: 50,
		DictionarySize:   128,
		DictionarySHA256: testSHA256Sample,
		DictionaryID:     0x12345678,
		Variants: []VariantEntry{
			{
				Level:            testLevelV80,
				Offset:           178,
				CompressedSize:   300,
				UncompressedSize: 800,
				SHA256:           testSHA256Sample2,
				Compression:      testCompression,
			},
		},
	}
	seedDictBytes, err := MarshalBinaryIndex(seedDictIdx)
	if err == nil {
		f.Add(seedDictBytes)
	}

	// Seed 3: Minimal valid header
	minHeader := make([]byte, minBinaryHeaderSize)
	copy(minHeader[0:4], []byte(IndexMagicV2))
	binary.LittleEndian.PutUint16(minHeader[4:6], uint16(FormatVersion2))
	f.Add(minHeader)

	// Seed 4: Corrupt magic
	f.Add([]byte("\x00\x00\x00\x00\x02\x00garbage"))

	// Seed 5: Truncated buffer
	f.Add([]byte("\x00\xFAM2\x02\x00"))

	// Seed 6: Oversized dictionary size
	seedOversizedDictIdx := &Index{
		Version:          FormatVersion2,
		AppName:          "fuzz-oversized-dict",
		TargetOS:         testOSLinux,
		TargetArch:       "amd64",
		DictionaryOffset: 50,
		DictionarySize:   MaxDictionarySize + 1024,
	}
	if seedOversizedBytes, err := MarshalBinaryIndex(seedOversizedDictIdx); err == nil {
		f.Add(seedOversizedBytes)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		idx, err := UnmarshalBinaryIndex(data)
		if err != nil {
			if idx != nil {
				t.Fatalf("expected nil index when err != nil, got %+v", idx)
			}
			return
		}
		if idx == nil {
			t.Fatal("expected non-nil index when err == nil")
		}

		// Verify bounds checking does not panic on unmarshaled index
		_ = idx.ValidateBounds(1 << 40)
		_ = idx.VariantLevels()
		for _, v := range idx.Variants {
			found, ok := idx.FindVariant(v.Level)
			if !ok || found == nil {
				t.Fatalf("failed to find variant %s in parsed index", v.Level)
			}
		}

		// Re-marshal should succeed and produce matching unmarshaled struct if inputs are valid
		marshaled, mErr := MarshalBinaryIndex(idx)
		if mErr == nil {
			reparsed, rErr := UnmarshalBinaryIndex(marshaled)
			if rErr != nil {
				t.Fatalf("failed to re-parse marshaled binary index: %v", rErr)
			}
			if reparsed.Version != idx.Version || reparsed.AppName != idx.AppName ||
				reparsed.TargetOS != idx.TargetOS || reparsed.TargetArch != idx.TargetArch ||
				len(reparsed.Variants) != len(idx.Variants) {
				t.Fatalf("mismatch after re-marshaling index: original=%+v, reparsed=%+v", idx, reparsed)
			}
		}
	})
}

func FuzzUnmarshalJSONIndex(f *testing.F) {
	// Seed 1: Standard valid format v1 JSON
	seedJSON1 := `{"version":1,"app_name":"demo","os":"linux","arch":"amd64","created_unix":1724540000,` +
		`"variants":[{"level":"v1","offset":100,"compressed_size":200,"uncompressed_size":500,` +
		`"sha256":"` + testSHA256Sample + `","compression":"zstd"}]}`
	f.Add([]byte(seedJSON1))

	// Seed 2: JSON with dictionary
	seedJSON2 := `{"version":1,"app_name":"demo-dict","os":"linux","arch":"amd64","created_unix":1724540000,` +
		`"dictionary_offset":64,"dictionary_size":256,"dictionary_sha256":"` + testSHA256Sample + `","dictionary_id":42,` +
		`"variants":[{"level":"v2","offset":320,"compressed_size":150,"uncompressed_size":400,` +
		`"sha256":"` + testSHA256Sample2 + `","compression":"lz4"}]}`
	f.Add([]byte(seedJSON2))

	// Seed 3: Whitespace and empty fields
	f.Add([]byte(`  {  "version" : 1 , "variants" : [ ] }  `))

	// Seed 4: Escaped strings and unknown extra fields
	f.Add([]byte(`{"version":1,"app_name":"escaped\"name\\test\n","extra_field":{"nested":true},"variants":[]}`))

	// Seed 5: Malformed JSON syntax
	f.Add([]byte(`{"version": 1, "app_name": `))
	f.Add([]byte(`{"version": "not-an-int"}`))
	f.Add([]byte(`[1, 2, 3]`))
	f.Add([]byte(`{"variants": [{"offset": -1}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		idx, err := unmarshalJSONIndex(data)
		if err != nil {
			if idx != nil {
				t.Fatalf("expected nil index when unmarshalJSONIndex failed, got %+v", idx)
			}
			return
		}
		if idx == nil {
			t.Fatal("expected non-nil index on success")
		}

		_ = idx.ValidateBounds(1 << 40)
		_ = idx.VariantLevels()
	})
}

func FuzzReadTrailer(f *testing.F) {
	// Build valid seed trailer
	validTrailer := make([]byte, TrailerSize)
	binary.LittleEndian.PutUint64(validTrailer[0:8], 1024)
	binary.LittleEndian.PutUint64(validTrailer[8:16], 256)
	testSum := sha256.Sum256([]byte("seed-index-content"))
	copy(validTrailer[16:48], testSum[:])
	copy(validTrailer[48:56], []byte(MagicString))

	f.Add(validTrailer)
	f.Add(make([]byte, TrailerSize))
	f.Add([]byte("too-short"))
	f.Add(append(validTrailer, []byte("trailing-garbage")...))

	f.Fuzz(func(t *testing.T, trailerBytes []byte) {
		if len(trailerBytes) < TrailerSize {
			return
		}
		offset := len(trailerBytes) - TrailerSize
		magic := string(trailerBytes[offset+48 : offset+56])
		idxOffset := int64(binary.LittleEndian.Uint64(trailerBytes[offset : offset+8]))
		idxSize := int64(binary.LittleEndian.Uint64(trailerBytes[offset+8 : offset+16]))

		if magic == MagicString && idxOffset >= 0 && idxSize > 0 && idxSize <= MaxIndexSize {
			if idxOffset+idxSize <= int64(len(trailerBytes))-TrailerSize {
				_ = trailerBytes[offset+16 : offset+48]
			}
		}
	})
}

func FuzzReadTrailerAndIndex(f *testing.F) {
	idx := &Index{
		Version:     FormatVersion2,
		AppName:     "fuzz-fat",
		TargetOS:    testOSLinux,
		TargetArch:  testArchAMD64,
		CreatedUnix: 1724540000,
		Variants: []VariantEntry{
			{
				Level:            "v1",
				Offset:           16,
				CompressedSize:   32,
				UncompressedSize: 64,
				SHA256:           testSHA256Sample,
				Compression:      "none",
			},
		},
	}

	buf := bytes.NewBuffer(make([]byte, 16+32))
	_, err := WriteIndexAndTrailerWithVersion(buf, idx, int64(buf.Len()), FormatVersion2)
	if err == nil {
		f.Add(buf.Bytes())
	}

	bufV1 := bytes.NewBuffer(make([]byte, 16+32))
	_, err = WriteIndexAndTrailerWithVersion(bufV1, idx, int64(bufV1.Len()), FormatVersion1)
	if err == nil {
		f.Add(bufV1.Bytes())
	}

	f.Add([]byte("small"))
	f.Add(make([]byte, TrailerSize+10))

	f.Fuzz(func(t *testing.T, fatBinary []byte) {
		reader := bytes.NewReader(fatBinary)
		parsedIdx, err := ReadTrailerAndIndex(reader, int64(len(fatBinary)))
		if err != nil {
			if parsedIdx != nil {
				t.Fatalf("expected nil index on read failure, got %+v", parsedIdx)
			}
			return
		}
		if parsedIdx == nil {
			t.Fatal("expected non-nil index on read success")
		}

		_ = parsedIdx.ValidateBounds(int64(len(fatBinary)) - TrailerSize)
	})
}
