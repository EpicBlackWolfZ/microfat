package format

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	json "encoding/json/v2"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testCompression   = "zstd"
	testArchAMD64     = "amd64"
	testOSLinux       = "linux"
	testAppName       = "testapp"
	testLevelV80      = "v8.0"
	testSHA256Sample  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testSHA256Sample2 = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

func TestFormatRoundTrip(t *testing.T) {
	buf := bytes.NewBuffer(make([]byte, 1024))
	initialOffset := int64(1024)

	originalIdx := &Index{
		AppName:     testAppName,
		TargetOS:    testOSLinux,
		TargetArch:  testArchAMD64,
		CreatedUnix: 1724540000,
		Variants: []VariantEntry{
			{
				Level:            "v1",
				Offset:           200,
				CompressedSize:   300,
				UncompressedSize: 800,
				SHA256:           testSHA256Sample,
				Compression:      testCompression,
			},
			{
				Level:            "v3",
				Offset:           500,
				CompressedSize:   400,
				UncompressedSize: 900,
				SHA256:           testSHA256Sample2,
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
			{
				Level:            "v1",
				Offset:           100,
				CompressedSize:   200,
				UncompressedSize: 400,
				SHA256:           testSHA256Sample,
				Compression:      testCompression,
			},
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
	idxInvalidVersion := &Index{
		Version:    99,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
	}
	err := idxInvalidVersion.ValidateBounds(1000)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("expected ErrUnsupportedVersion, got %v", err)
	}

	idxInvalidDimensions := &Index{
		Version:    FormatVersionCurrent,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: []VariantEntry{
			{Level: "v1", Offset: -10, CompressedSize: 100, UncompressedSize: 100},
		},
	}
	err = idxInvalidDimensions.ValidateBounds(1000)
	if !errors.Is(err, ErrOutOfBounds) {
		t.Errorf("expected ErrOutOfBounds for negative offset, got %v", err)
	}

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

	err = idxOutOfBounds.ValidateBounds(1200)
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
				SHA256:           testSHA256Sample,
				Compression:      testCompression,
			},
			{
				Level:            "v3",
				Offset:           250, // Overlaps with v1 (ends at 400)
				CompressedSize:   200,
				UncompressedSize: 500,
				SHA256:           testSHA256Sample,
				Compression:      testCompression,
			},
		},
	}
	err = idxOverlapping.ValidateBounds(1200)
	if !errors.Is(err, ErrOverlappingVariant) {
		t.Errorf("expected ErrOverlappingVariant, got %v", err)
	}
}

func TestWriteIndexAndTrailerErrors(t *testing.T) {
	var buf bytes.Buffer
	idx := &Index{Version: FormatVersionCurrent}
	_, err := WriteIndexAndTrailer(&buf, idx, -1)
	if err == nil {
		t.Errorf("expected error for negative offset")
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

	// Invalid index offset (offset > trailerOffset)
	binary.LittleEndian.PutUint64(trailer[0:OffsetLen], 500)
	binary.LittleEndian.PutUint64(trailer[OffsetLen:OffsetLen*2], 50)
	copy(data[200:], trailer)
	_, err = ReadTrailerAndIndex(bytes.NewReader(data), int64(len(data)))
	if !errors.Is(err, ErrInvalidIndexOffset) {
		t.Errorf("expected ErrInvalidIndexOffset, got %v", err)
	}

	// Valid hash over invalid JSON bytes
	invalidJSONBytes := []byte("INVALID_JSON_BYTES_123456789012")
	invalidJSONHash := sha256.Sum256(invalidJSONBytes)
	trailerJSON := make([]byte, TrailerSize)
	binary.LittleEndian.PutUint64(trailerJSON[0:OffsetLen], 100)
	binary.LittleEndian.PutUint64(trailerJSON[OffsetLen:OffsetLen*2], uint64(len(invalidJSONBytes)))
	copy(trailerJSON[OffsetLen*2:OffsetLen*2+HashLen], invalidJSONHash[:])
	copy(trailerJSON[OffsetLen*2+HashLen:], []byte(MagicString))

	dataJSON := make([]byte, 100+len(invalidJSONBytes)+TrailerSize)
	copy(dataJSON[100:], invalidJSONBytes)
	copy(dataJSON[100+len(invalidJSONBytes):], trailerJSON)
	_, err = ReadTrailerAndIndex(bytes.NewReader(dataJSON), int64(len(dataJSON)))
	if err == nil {
		t.Errorf("expected error unmarshaling invalid JSON index")
	}

	// Valid hash and JSON, but failing bounds validation
	outOfBoundsIdx := &Index{
		Version:  FormatVersionCurrent,
		Variants: []VariantEntry{{Level: "v1", Offset: 500, CompressedSize: 200, UncompressedSize: 300}},
	}
	outOfBoundsJSON, _ := json.Marshal(outOfBoundsIdx)
	outOfBoundsHash := sha256.Sum256(outOfBoundsJSON)
	trailerBounds := make([]byte, TrailerSize)
	binary.LittleEndian.PutUint64(trailerBounds[0:OffsetLen], 100)
	binary.LittleEndian.PutUint64(trailerBounds[OffsetLen:OffsetLen*2], uint64(len(outOfBoundsJSON)))
	copy(trailerBounds[OffsetLen*2:OffsetLen*2+HashLen], outOfBoundsHash[:])
	copy(trailerBounds[OffsetLen*2+HashLen:], []byte(MagicString))

	dataBounds := make([]byte, 100+len(outOfBoundsJSON)+TrailerSize)
	copy(dataBounds[100:], outOfBoundsJSON)
	copy(dataBounds[100+len(outOfBoundsJSON):], trailerBounds)
	_, err = ReadTrailerAndIndex(bytes.NewReader(dataBounds), int64(len(dataBounds)))
	if err == nil {
		t.Errorf("expected error when index bounds validation fails")
	}
}

type errReader struct{}

func (r *errReader) ReadAt(p []byte, off int64) (n int, err error) {
	return 0, errors.New("read error")
}

func TestErrReader(t *testing.T) {
	if IsFatBinary(&errReader{}, 100) {
		t.Errorf("expected IsFatBinary on failing reader to return false")
	}

	_, err := ReadTrailerAndIndex(&errReader{}, 100)
	if err == nil {
		t.Errorf("expected ReadTrailerAndIndex on failing reader to return error")
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

func TestWriteIndexAndTrailerWriterErrors(t *testing.T) {
	idx := &Index{Version: FormatVersionCurrent}

	// Fail on 1st write (index json)
	_, err := WriteIndexAndTrailer(&errWriter{failOnWrite: 1}, idx, 100)
	if err == nil {
		t.Errorf("expected error on failing index write")
	}

	// Fail on 2nd write (trailer)
	_, err = WriteIndexAndTrailer(&errWriter{failOnWrite: 2}, idx, 100)
	if err == nil {
		t.Errorf("expected error on failing trailer write")
	}

	// Negative offset
	var dummyBuf bytes.Buffer
	_, err = WriteIndexAndTrailer(&dummyBuf, idx, -5)
	if err == nil {
		t.Errorf("expected error for negative offset")
	}

	// Oversized index > MaxIndexSize
	largeIdx := &Index{
		Version: FormatVersionCurrent,
		AppName: string(make([]byte, MaxIndexSize+100)),
	}
	_, err = WriteIndexAndTrailer(&dummyBuf, largeIdx, 100)
	if err == nil {
		t.Errorf("expected error for oversized index exceeding MaxIndexSize")
	}
}

func TestTelemetryStructs(t *testing.T) {
	t.Parallel()

	dispatch := DispatchTelemetry{
		Event:                   EventDispatch,
		TimestampUnixNano:       1724540000000000000,
		HostArch:                testArchAMD64,
		HostLevel:               "v3",
		SelectedVariant:         "v3",
		SelectedSHA256:          testSHA256Sample,
		SelectedSizeBytes:       1024,
		ExecMode:                ExecModeMemfd,
		CgroupVersion:           2,
		CgroupMemLimitBytes:     1073741824,
		CgroupCPUQuota:          4.0,
		GOMEMLIMIT:              "966367641B",
		GOMAXPROCS:              "4",
		DecompressionDurationUs: 1500,
		TotalLauncherUs:         3200,
	}

	data, err := json.Marshal(dispatch)
	if err != nil {
		t.Fatalf("marshaling DispatchTelemetry: %v", err)
	}

	var roundTrip DispatchTelemetry
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("unmarshaling DispatchTelemetry: %v", err)
	}

	if roundTrip.Event != EventDispatch || roundTrip.SelectedVariant != "v3" || roundTrip.DecompressionDurationUs != 1500 {
		t.Errorf("DispatchTelemetry roundtrip mismatch: %+v", roundTrip)
	}

	errTelem := ErrorTelemetry{
		Event:             EventError,
		TimestampUnixNano: 1724540000000000000,
		HostArch:          testArchAMD64,
		HostLevel:         "v3",
		SelectedVariant:   "v3",
		Stage:             "memfd_create",
		Error:             "permission denied",
		Details:           "memfd restricted in container",
	}

	errData, err := json.Marshal(errTelem)
	if err != nil {
		t.Fatalf("marshaling ErrorTelemetry: %v", err)
	}

	var roundTripErr ErrorTelemetry
	if err := json.Unmarshal(errData, &roundTripErr); err != nil {
		t.Fatalf("unmarshaling ErrorTelemetry: %v", err)
	}

	if roundTripErr.Event != EventError || roundTripErr.Stage != "memfd_create" {
		t.Errorf("ErrorTelemetry roundtrip mismatch: %+v", roundTripErr)
	}

	binInfo := BinaryInfo{
		AppName:         testAppName,
		TargetOS:        testOSLinux,
		TargetArch:      testArchAMD64,
		FatBinarySize:   2048,
		HostOS:          testOSLinux,
		HostArch:        testArchAMD64,
		HostLevel:       "v3",
		SelectedVariant: "v3",
		SelectedSize:    1024,
		ExecMode:        ExecModeMemfd,
		Cgroup: &CgroupInfo{
			Version:          2,
			MemoryLimitBytes: 1073741824,
			CPUQuota:         4.0,
			GOMEMLIMIT:       "966367641B",
			GOMAXPROCS:       4,
		},
		Variants: []VariantEntry{
			{Level: "v1", Offset: 100, CompressedSize: 200, UncompressedSize: 500, Compression: testCompression},
		},
		HostFeatures: []string{"avx2", "bmi2"},
	}

	infoData, err := json.Marshal(binInfo)
	if err != nil {
		t.Fatalf("marshaling BinaryInfo: %v", err)
	}

	var roundTripInfo BinaryInfo
	if err := json.Unmarshal(infoData, &roundTripInfo); err != nil {
		t.Fatalf("unmarshaling BinaryInfo: %v", err)
	}

	if roundTripInfo.AppName != testAppName || roundTripInfo.Cgroup == nil || roundTripInfo.Cgroup.Version != 2 {
		t.Errorf("BinaryInfo roundtrip mismatch: %+v", roundTripInfo)
	}
}

func TestPrewarmTelemetryStructs(t *testing.T) {
	t.Parallel()

	prewarm := PrewarmTelemetry{
		Event:             EventPrewarm,
		TimestampUnixNano: 1724540000000000000,
		AppName:           testAppName,
		CacheDir:          "/tmp/cache",
		Results: []PrewarmResult{
			{
				Level:            "v3",
				SHA256:           testSHA256Sample,
				UncompressedSize: 1024,
				CachedPath:       "/tmp/cache/abcdef123456",
				AlreadyCached:    false,
				DecompressionUs:  1200,
			},
		},
	}

	data, err := json.Marshal(prewarm)
	if err != nil {
		t.Fatalf("marshaling PrewarmTelemetry: %v", err)
	}

	var roundTrip PrewarmTelemetry
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("unmarshaling PrewarmTelemetry: %v", err)
	}

	if roundTrip.Event != EventPrewarm || roundTrip.AppName != testAppName || len(roundTrip.Results) != 1 {
		t.Errorf("PrewarmTelemetry roundtrip mismatch: %+v", roundTrip)
	}
	if roundTrip.Results[0].Level != "v3" || roundTrip.Results[0].AlreadyCached {
		t.Errorf("PrewarmResult mismatch: %+v", roundTrip.Results[0])
	}
}

func TestResolveCacheDir(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Custom directory argument
	customDir := filepath.Join(tempDir, "custom")
	dir, err := ResolveCacheDir(customDir)
	if err != nil || dir != customDir {
		t.Errorf("expected %s, got %s (err: %v)", customDir, dir, err)
	}

	// 2. MICROFAT_CACHE_DIR environment variable
	envDir := filepath.Join(tempDir, "from_env")
	t.Setenv(EnvCacheDir, envDir)
	dir, err = ResolveCacheDir("")
	if err != nil || dir != envDir {
		t.Errorf("expected %s from env, got %s (err: %v)", envDir, dir, err)
	}
	t.Setenv(EnvCacheDir, "")

	// 3. XDG_CACHE_HOME environment variable
	xdgDir := filepath.Join(tempDir, "xdg_cache")
	t.Setenv("XDG_CACHE_HOME", xdgDir)
	dir, err = ResolveCacheDir("")
	expectedXDG := filepath.Join(xdgDir, "microfat")
	if err != nil || dir != expectedXDG {
		t.Errorf("expected %s from XDG_CACHE_HOME, got %s (err: %v)", expectedXDG, dir, err)
	}
	t.Setenv("XDG_CACHE_HOME", "")

	// 4. Fallback to user home directory
	homeDir := filepath.Join(tempDir, "home")
	_ = os.MkdirAll(homeDir, 0o755)
	oldHomeFunc := userHomeDirFunc
	defer func() { userHomeDirFunc = oldHomeFunc }()
	userHomeDirFunc = func() (string, error) {
		return homeDir, nil
	}
	dir, err = ResolveCacheDir("")
	expectedHome := filepath.Join(homeDir, ".cache", "microfat")
	if err != nil || dir != expectedHome {
		t.Errorf("expected %s from user home, got %s (err: %v)", expectedHome, dir, err)
	}

	// 5. Fallback when userHomeDirFunc fails
	userHomeDirFunc = func() (string, error) {
		return "", errors.New("no home")
	}
	dir, err = ResolveCacheDir("")
	if err != nil || dir == "" {
		t.Errorf("expected success with os.TempDir fallback, got %v", err)
	}

	// 6. Error when custom dir cannot be created (e.g. parent is a regular file)
	blockFile := filepath.Join(tempDir, "blocker_file")
	_ = os.WriteFile(blockFile, []byte("data"), 0o600)
	badCustomDir := filepath.Join(blockFile, "sub")
	_, err = ResolveCacheDir(badCustomDir)
	if err == nil {
		t.Errorf("expected error for impossible custom directory")
	}

	// 7. Error when EnvCacheDir cannot be created
	t.Setenv(EnvCacheDir, badCustomDir)
	_, err = ResolveCacheDir("")
	if err == nil {
		t.Errorf("expected error for impossible MICROFAT_CACHE_DIR")
	}
}

func TestFormatEmptyCompressionDefaultsToZstd(t *testing.T) {
	t.Parallel()

	buf := bytes.NewBuffer(make([]byte, 1024))
	originalIdx := &Index{
		AppName:     "legacy-app",
		TargetOS:    testOSLinux,
		TargetArch:  testArchAMD64,
		CreatedUnix: 1724540000,
		Variants: []VariantEntry{
			{
				Level:            "v1",
				Offset:           200,
				CompressedSize:   300,
				UncompressedSize: 800,
				SHA256:           testSHA256Sample,
				Compression:      "", // legacy empty compression
			},
		},
	}

	written, err := WriteIndexAndTrailer(buf, originalIdx, 1024)
	if err != nil {
		t.Fatalf("WriteIndexAndTrailer failed: %v", err)
	}

	reader := bytes.NewReader(buf.Bytes())
	idx, err := ReadTrailerAndIndex(reader, 1024+written)
	if err != nil {
		t.Fatalf("ReadTrailerAndIndex failed: %v", err)
	}

	if len(idx.Variants) != 1 {
		t.Fatalf("expected 1 variant, got %d", len(idx.Variants))
	}
	if idx.Variants[0].Compression != testCompression {
		t.Errorf("expected empty compression to default to 'zstd', got %q", idx.Variants[0].Compression)
	}
}

func TestFormatVersion1JSONRoundTrip(t *testing.T) {
	t.Parallel()

	buf := bytes.NewBuffer(make([]byte, 1024))
	originalIdx := &Index{
		Version:     FormatVersion1,
		AppName:     "json-legacy-app",
		TargetOS:    testOSLinux,
		TargetArch:  testArchAMD64,
		CreatedUnix: 1724541234,
		Variants: []VariantEntry{
			{
				Level:            "v1",
				Offset:           200,
				CompressedSize:   300,
				UncompressedSize: 800,
				SHA256:           testSHA256Sample,
				Compression:      "lz4",
			},
			{
				Level:            "v3",
				Offset:           500,
				CompressedSize:   400,
				UncompressedSize: 900,
				SHA256:           testSHA256Sample2,
				Compression:      testCompression,
			},
		},
	}

	writtenBytes, err := WriteIndexAndTrailerWithVersion(buf, originalIdx, 1024, FormatVersion1)
	if err != nil {
		t.Fatalf("WriteIndexAndTrailerWithVersion(v1) failed: %v", err)
	}

	reader := bytes.NewReader(buf.Bytes())
	readIdx, err := ReadTrailerAndIndex(reader, 1024+writtenBytes)
	if err != nil {
		t.Fatalf("ReadTrailerAndIndex for v1 failed: %v", err)
	}

	if readIdx.Version != FormatVersion1 {
		t.Errorf("expected Version %d, got %d", FormatVersion1, readIdx.Version)
	}
	if readIdx.AppName != originalIdx.AppName {
		t.Errorf("expected AppName %s, got %s", originalIdx.AppName, readIdx.AppName)
	}
	if len(readIdx.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(readIdx.Variants))
	}
	if readIdx.Variants[0].Compression != "lz4" || readIdx.Variants[1].Compression != testCompression {
		t.Errorf("variant compressions mismatch: %+v", readIdx.Variants)
	}
}

func TestFormatVersion2BinaryRoundTrip(t *testing.T) {
	t.Parallel()

	buf := bytes.NewBuffer(make([]byte, 1024))
	originalIdx := &Index{
		Version:     FormatVersion2,
		AppName:     "binary-v2-app",
		TargetOS:    testOSLinux,
		TargetArch:  testArchAMD64,
		CreatedUnix: 1724545678,
		Variants: []VariantEntry{
			{
				Level:            "v1",
				Offset:           200,
				CompressedSize:   300,
				UncompressedSize: 800,
				SHA256:           testSHA256Sample,
				Compression:      "none",
			},
			{
				Level:            "v4",
				Offset:           500,
				CompressedSize:   400,
				UncompressedSize: 950,
				SHA256:           testSHA256Sample2,
				Compression:      testCompression,
			},
		},
	}

	writtenBytes, err := WriteIndexAndTrailerWithVersion(buf, originalIdx, 1024, FormatVersion2)
	if err != nil {
		t.Fatalf("WriteIndexAndTrailerWithVersion(v2) failed: %v", err)
	}

	reader := bytes.NewReader(buf.Bytes())
	readIdx, err := ReadTrailerAndIndex(reader, 1024+writtenBytes)
	if err != nil {
		t.Fatalf("ReadTrailerAndIndex for v2 failed: %v", err)
	}

	if readIdx.Version != FormatVersion2 {
		t.Errorf("expected Version %d, got %d", FormatVersion2, readIdx.Version)
	}
	if readIdx.AppName != originalIdx.AppName {
		t.Errorf("expected AppName %s, got %s", originalIdx.AppName, readIdx.AppName)
	}
	if len(readIdx.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(readIdx.Variants))
	}
	if readIdx.Variants[0].Compression != "none" || readIdx.Variants[1].Compression != defaultCompressionAlgorithm {
		t.Errorf("variant compressions mismatch: %+v", readIdx.Variants)
	}
}

func TestWriteIndexAndTrailerUnsupportedVersion(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	idx := &Index{
		Version:  99,
		TargetOS: testOSLinux,
	}
	_, err := WriteIndexAndTrailerWithVersion(&buf, idx, 100, 99)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("expected ErrUnsupportedVersion for version 99, got %v", err)
	}
}

func TestMarshalBinaryIndexFieldLimits(t *testing.T) {
	t.Parallel()

	longString := string(make([]byte, 300))
	idxBadOS := &Index{TargetOS: longString}
	if _, err := MarshalBinaryIndex(idxBadOS); err == nil {
		t.Errorf("expected error for oversized TargetOS")
	}

	idxBadLevel := &Index{
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: []VariantEntry{
			{Level: longString},
		},
	}
	if _, err := MarshalBinaryIndex(idxBadLevel); err == nil {
		t.Errorf("expected error for oversized Variant.Level")
	}
}

func TestUnmarshalBinaryIndexErrors(t *testing.T) {
	t.Parallel()

	// Short data
	if _, err := UnmarshalBinaryIndex([]byte("short")); !errors.Is(err, ErrTruncatedIndex) {
		t.Errorf("expected ErrTruncatedIndex for short data, got %v", err)
	}

	// Bad magic
	badMagic := make([]byte, 40)
	copy(badMagic, []byte("BADM"))
	if _, err := UnmarshalBinaryIndex(badMagic); !errors.Is(err, ErrInvalidMagic) {
		t.Errorf("expected ErrInvalidMagic for bad magic, got %v", err)
	}

	// Bad version
	badVersion := make([]byte, 40)
	copy(badVersion, []byte(IndexMagicV2))
	binary.LittleEndian.PutUint16(badVersion[4:6], 99)
	if _, err := UnmarshalBinaryIndex(badVersion); !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("expected ErrUnsupportedVersion for version 99, got %v", err)
	}

	// Truncated at various offsets
	goodIdx := &Index{
		Version:     FormatVersion2,
		AppName:     "test",
		TargetOS:    testOSLinux,
		TargetArch:  testArchAMD64,
		CreatedUnix: 123456,
		Variants: []VariantEntry{
			{Level: "v1", Offset: 100, CompressedSize: 200, UncompressedSize: 300, SHA256: testSHA256Sample, Compression: testCompression},
		},
	}
	goodData, err := MarshalBinaryIndex(goodIdx)
	if err != nil {
		t.Fatalf("MarshalBinaryIndex failed: %v", err)
	}

	for cut := 14; cut < len(goodData)-1; cut++ {
		truncated := goodData[:cut]
		if _, err := UnmarshalBinaryIndex(truncated); !errors.Is(err, ErrTruncatedIndex) {
			t.Errorf("expected ErrTruncatedIndex when cutting at %d, got %v", cut, err)
		}
	}
}

func TestUnmarshalJSONIndexErrorsAndEdges(t *testing.T) {
	t.Parallel()

	// Not an object
	if _, err := unmarshalJSONIndex([]byte("[]")); !errors.Is(err, ErrInvalidJSONSyntax) {
		t.Errorf("expected ErrInvalidJSONSyntax for array root, got %v", err)
	}

	// Unclosed root
	if _, err := unmarshalJSONIndex([]byte("{ \"version\": 1")); !errors.Is(err, ErrInvalidJSONSyntax) {
		t.Errorf("expected ErrInvalidJSONSyntax for unclosed root, got %v", err)
	}

	// Escapes and strings
	jsonWithEscapes := []byte(`{
		"version": 1,
		"app_name": "app\"with\nquotes\\and\/slashes\b\f\r\t",
		"os": "linux",
		"arch": "amd64",
		"created_unix": -12345,
		"unknown_field": {"nested": [1, 2, "three", true, false, null]},
		"variants": [
			{
				"level": "v1",
				"offset": 100,
				"compressed_size": 200,
				"uncompressed_size": 300,
				"sha256": "` + testSHA256Sample + `",
				"compression": "lz4",
				"extra": "ignore"
			}
		]
	}`)

	idx, err := unmarshalJSONIndex(jsonWithEscapes)
	if err != nil {
		t.Fatalf("unmarshalJSONIndex with escapes failed: %v", err)
	}
	if idx.AppName != "app\"with\nquotes\\and/slashes\b\f\r\t" {
		t.Errorf("escaped AppName mismatch: %q", idx.AppName)
	}
	if idx.CreatedUnix != -12345 {
		t.Errorf("negative CreatedUnix mismatch: %d", idx.CreatedUnix)
	}
	if len(idx.Variants) != 1 || idx.Variants[0].Compression != "lz4" {
		t.Errorf("variant mismatch: %+v", idx.Variants)
	}
}

func BenchmarkUnmarshalJSONIndex(b *testing.B) {
	idx := &Index{
		Version:     FormatVersion1,
		AppName:     "benchmark-demo-service",
		TargetOS:    testOSLinux,
		TargetArch:  testArchAMD64,
		CreatedUnix: 1724540000,
		Variants: []VariantEntry{
			{
				Level: "v1", Offset: 1000, CompressedSize: 2000000, UncompressedSize: 5000000,
				SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Compression: testCompression,
			},
			{
				Level: "v2", Offset: 2001000, CompressedSize: 2100000, UncompressedSize: 5100000,
				SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Compression: testCompression,
			},
			{
				Level: "v3", Offset: 4101000, CompressedSize: 2200000, UncompressedSize: 5200000,
				SHA256: "11223344556677889900aabbccddeeff11223344556677889900aabbccddeeff", Compression: testCompression,
			},
			{
				Level: "v4", Offset: 6301000, CompressedSize: 2300000, UncompressedSize: 5300000,
				SHA256: "ffeeddccbbaa00998877665544332211ffeeddccbbaa00998877665544332211", Compression: testCompression,
			},
		},
	}
	jsonBytes, err := json.Marshal(idx)
	if err != nil {
		b.Fatalf("json.Marshal failed: %v", err)
	}

	b.ReportAllocs()
	for b.Loop() {
		_, err := unmarshalJSONIndex(jsonBytes)
		if err != nil {
			b.Fatalf("unmarshalJSONIndex failed: %v", err)
		}
	}
}

func BenchmarkUnmarshalBinaryIndex(b *testing.B) {
	idx := &Index{
		Version:     FormatVersion2,
		AppName:     "benchmark-demo-service",
		TargetOS:    testOSLinux,
		TargetArch:  testArchAMD64,
		CreatedUnix: 1724540000,
		Variants: []VariantEntry{
			{
				Level: "v1", Offset: 1000, CompressedSize: 2000000, UncompressedSize: 5000000,
				SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Compression: testCompression,
			},
			{
				Level: "v2", Offset: 2001000, CompressedSize: 2100000, UncompressedSize: 5100000,
				SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Compression: testCompression,
			},
			{
				Level: "v3", Offset: 4101000, CompressedSize: 2200000, UncompressedSize: 5200000,
				SHA256: "11223344556677889900aabbccddeeff11223344556677889900aabbccddeeff", Compression: testCompression,
			},
			{
				Level: "v4", Offset: 6301000, CompressedSize: 2300000, UncompressedSize: 5300000,
				SHA256: "ffeeddccbbaa00998877665544332211ffeeddccbbaa00998877665544332211", Compression: testCompression,
			},
		},
	}
	binBytes, err := MarshalBinaryIndex(idx)
	if err != nil {
		b.Fatalf("MarshalBinaryIndex failed: %v", err)
	}

	b.ReportAllocs()
	for b.Loop() {
		_, err := UnmarshalBinaryIndex(binBytes)
		if err != nil {
			b.Fatalf("UnmarshalBinaryIndex failed: %v", err)
		}
	}
}

func TestEscapeJSONString(t *testing.T) {
	input := "test\b\f\n\r\t\"\\hello\x01world"
	escaped := escapeJSONString(input)
	hasAll := bytes.Contains([]byte(escaped), []byte(`\b`)) &&
		bytes.Contains([]byte(escaped), []byte(`\n`)) &&
		bytes.Contains([]byte(escaped), []byte(`\u0001`))
	if !hasAll {
		t.Fatalf("unexpected escape string result: %s", escaped)
	}
}

func TestJSONParser_EdgeCases(t *testing.T) {
	t.Run("escaped_characters", func(t *testing.T) {
		jsonStr := `{"app_name":"app\n\t\r\b\f\"\\\/\u0041","version":1,"os":"linux","arch":"amd64","created_unix":100,"variants":[]}`
		idx, err := unmarshalJSONIndex([]byte(jsonStr))
		if err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if idx.AppName != "app\n\t\r\b\f\"\\/A" {
			t.Fatalf("unexpected app name: %q", idx.AppName)
		}
	})

	t.Run("skip_values", func(t *testing.T) {
		jsonStr := `{"extra_bool":true,"extra_false":false,"extra_null":null,` +
			`"extra_obj":{"k":"v","nested":[1,2,3]},"version":1,"os":"linux","arch":"amd64","created_unix":-100,"variants":[]}`
		idx, err := unmarshalJSONIndex([]byte(jsonStr))
		if err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if idx.CreatedUnix != -100 {
			t.Fatalf("unexpected created unix: %d", idx.CreatedUnix)
		}
	})

	t.Run("invalid_escape_sequences", func(t *testing.T) {
		invalidCases := []string{
			`{"app_name":"\u00"`,
			`{"app_name":"\uZZZZ"`,
			`{"app_name":"\x"`,
			`{"app_name":"unterminated`,
			`{"created_unix":-}`,
			`{"variants":[unknown]}`,
		}
		for _, c := range invalidCases {
			_, err := unmarshalJSONIndex([]byte(c))
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", c)
			}
		}
	})
}

func TestDictionaryBinaryIndexSerialization(t *testing.T) {
	t.Parallel()

	idx := &Index{
		Version:          FormatVersion2,
		AppName:          "dict-test-app",
		TargetOS:         "linux",
		TargetArch:       testArchAMD64,
		CreatedUnix:      1700000000,
		DictionaryOffset: 4096,
		DictionarySize:   110592,
		DictionarySHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		DictionaryID:     0x4D464154,
		Variants: []VariantEntry{
			{
				Level:            "v1",
				Offset:           114688,
				CompressedSize:   10000,
				UncompressedSize: 25000,
				SHA256:           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Compression:      "zstd",
			},
			{
				Level:            "v3",
				Offset:           124688,
				CompressedSize:   9000,
				UncompressedSize: 26000,
				SHA256:           "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Compression:      "zstd",
			},
		},
	}

	data, err := MarshalBinaryIndex(idx)
	if err != nil {
		t.Fatalf("MarshalBinaryIndex failed: %v", err)
	}

	decoded, err := UnmarshalBinaryIndex(data)
	if err != nil {
		t.Fatalf("UnmarshalBinaryIndex failed: %v", err)
	}

	if decoded.Version != idx.Version {
		t.Errorf("expected version %d, got %d", idx.Version, decoded.Version)
	}
	if decoded.DictionaryOffset != idx.DictionaryOffset {
		t.Errorf("expected DictOffset %d, got %d", idx.DictionaryOffset, decoded.DictionaryOffset)
	}
	if decoded.DictionarySize != idx.DictionarySize {
		t.Errorf("expected DictSize %d, got %d", idx.DictionarySize, decoded.DictionarySize)
	}
	if decoded.DictionarySHA256 != idx.DictionarySHA256 {
		t.Errorf("expected DictSHA256 %s, got %s", idx.DictionarySHA256, decoded.DictionarySHA256)
	}
	if decoded.DictionaryID != idx.DictionaryID {
		t.Errorf("expected DictID 0x%x, got 0x%x", idx.DictionaryID, decoded.DictionaryID)
	}
	if len(decoded.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(decoded.Variants))
	}
	if decoded.Variants[0].Level != "v1" || decoded.Variants[1].Level != "v3" {
		t.Errorf("variant levels mismatch: %v", decoded.Variants)
	}
}

func TestDictionaryJSONIndexSerialization(t *testing.T) {
	t.Parallel()

	idx := &Index{
		Version:          FormatVersion1,
		AppName:          "dict-json-app",
		TargetOS:         "linux",
		TargetArch:       "amd64",
		CreatedUnix:      1700000000,
		DictionaryOffset: 8192,
		DictionarySize:   65536,
		DictionarySHA256: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
		DictionaryID:     0x12345678,
		Variants: []VariantEntry{
			{
				Level:            "v1",
				Offset:           73728,
				CompressedSize:   5000,
				UncompressedSize: 15000,
				SHA256:           "1111111111111111111111111111111111111111111111111111111111111111",
				Compression:      "zstd",
			},
		},
	}

	data, err := marshalJSONIndex(idx)
	if err != nil {
		t.Fatalf("marshalJSONIndex failed: %v", err)
	}

	decoded, err := unmarshalJSONIndex(data)
	if err != nil {
		t.Fatalf("unmarshalJSONIndex failed: %v", err)
	}

	if decoded.DictionaryOffset != idx.DictionaryOffset {
		t.Errorf("expected DictOffset %d, got %d", idx.DictionaryOffset, decoded.DictionaryOffset)
	}
	if decoded.DictionarySize != idx.DictionarySize {
		t.Errorf("expected DictSize %d, got %d", idx.DictionarySize, decoded.DictionarySize)
	}
	if decoded.DictionarySHA256 != idx.DictionarySHA256 {
		t.Errorf("expected DictSHA256 %s, got %s", idx.DictionarySHA256, decoded.DictionarySHA256)
	}
	if decoded.DictionaryID != idx.DictionaryID {
		t.Errorf("expected DictID 0x%x, got 0x%x", idx.DictionaryID, decoded.DictionaryID)
	}
}

func TestDictionaryBoundsValidation(t *testing.T) {
	t.Parallel()

	t.Run("Valid dictionary bounds", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: 1000,
			DictionarySize:   500,
			DictionarySHA256: testSHA256Sample,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 1500, CompressedSize: 500, UncompressedSize: 1000, SHA256: testSHA256Sample},
				{Level: "v2", Offset: 2000, CompressedSize: 500, UncompressedSize: 1000, SHA256: testSHA256Sample},
			},
		}
		if err := idx.ValidateBounds(3000); err != nil {
			t.Fatalf("expected valid bounds, got %v", err)
		}
	})

	t.Run("Negative dictionary offset", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: -10,
			DictionarySize:   500,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 1500, CompressedSize: 500, UncompressedSize: 1000},
			},
		}
		if err := idx.ValidateBounds(3000); !errors.Is(err, ErrInvalidDictionary) {
			t.Fatalf("expected ErrInvalidDictionary for negative offset, got %v", err)
		}
	})

	t.Run("Dictionary extends past index offset", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: 2500,
			DictionarySize:   1000,
			DictionarySHA256: testSHA256Sample,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 1500, CompressedSize: 500, UncompressedSize: 1000, SHA256: testSHA256Sample},
			},
		}
		if err := idx.ValidateBounds(3000); !errors.Is(err, ErrOutOfBounds) {
			t.Fatalf("expected ErrOutOfBounds when dict extends past index, got %v", err)
		}
	})

	t.Run("Variant overlaps with dictionary", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: 1000,
			DictionarySize:   1000,
			DictionarySHA256: testSHA256Sample,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 1500, CompressedSize: 500, UncompressedSize: 1000, SHA256: testSHA256Sample},
			},
		}
		if err := idx.ValidateBounds(3000); !errors.Is(err, ErrOverlappingVariant) {
			t.Fatalf("expected ErrOverlappingVariant when variant overlaps dict, got %v", err)
		}
	})

	t.Run("Negative dictionary size", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: 1000,
			DictionarySize:   -1,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 1500, CompressedSize: 500, UncompressedSize: 1000},
			},
		}
		if err := idx.ValidateBounds(3000); !errors.Is(err, ErrInvalidDictionary) {
			t.Fatalf("expected ErrInvalidDictionary for negative dictionary size, got %v", err)
		}
	})

	t.Run("Max allowed dictionary size boundary", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: 1000,
			DictionarySize:   MaxDictionarySize,
			DictionarySHA256: testSHA256Sample,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 1000 + MaxDictionarySize, CompressedSize: 500, UncompressedSize: 1000, SHA256: testSHA256Sample},
			},
		}
		if err := idx.ValidateBounds(1000 + MaxDictionarySize + 1000); err != nil {
			t.Fatalf("expected valid bounds for exact MaxDictionarySize, got %v", err)
		}
	})

	t.Run("Exceeded MaxDictionarySize boundary", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: 1000,
			DictionarySize:   MaxDictionarySize + 1,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 1000 + MaxDictionarySize + 1000, CompressedSize: 500, UncompressedSize: 1000},
			},
		}
		if err := idx.ValidateBounds(1000 + MaxDictionarySize + 2000); !errors.Is(err, ErrInvalidDictionary) {
			t.Fatalf("expected ErrInvalidDictionary when DictionarySize exceeds MaxDictionarySize, got %v", err)
		}
	})

	t.Run("Extreme oversized dictionary size DoS protection", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: 1000,
			DictionarySize:   500 * 1024 * 1024,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 600 * 1024 * 1024, CompressedSize: 500, UncompressedSize: 1000},
			},
		}
		if err := idx.ValidateBounds(700 * 1024 * 1024); !errors.Is(err, ErrInvalidDictionary) {
			t.Fatalf("expected ErrInvalidDictionary for massive dictionary size, got %v", err)
		}
	})

	t.Run("Oversized DictionarySHA256 limit error", func(t *testing.T) {
		t.Parallel()
		longSHA := strings.Repeat("A", 300)
		idx := &Index{
			Version:          FormatVersion2,
			DictionarySHA256: longSHA,
		}
		if _, err := MarshalBinaryIndex(idx); err == nil {
			t.Fatalf("expected error for oversized DictionarySHA256")
		}
	})
}

func TestValidateBounds_IntegerOverflow(t *testing.T) {
	t.Parallel()

	t.Run("Variant offset near MaxInt64 overflowing addition", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersion2,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{
					Level:            "v1",
					Offset:           math.MaxInt64 - 100,
					CompressedSize:   200,
					UncompressedSize: 1000,
				},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrOutOfBounds) {
			t.Fatalf("expected ErrOutOfBounds for near-MaxInt64 offset, got %v", err)
		}
	})

	t.Run("Variant compressed size near MaxInt64 overflowing addition", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersion2,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{
					Level:            "v1",
					Offset:           100,
					CompressedSize:   math.MaxInt64 - 50,
					UncompressedSize: 1000,
				},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrOutOfBounds) {
			t.Fatalf("expected ErrOutOfBounds for near-MaxInt64 compressed size, got %v", err)
		}
	})

	t.Run("Dictionary offset near MaxInt64 overflowing addition", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: math.MaxInt64 - 50,
			DictionarySize:   100,
			DictionarySHA256: testSHA256Sample,
			Variants: []VariantEntry{
				{
					Level:            "v1",
					Offset:           200,
					CompressedSize:   300,
					UncompressedSize: 1000,
					SHA256:           testSHA256Sample,
				},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrOutOfBounds) {
			t.Fatalf("expected ErrOutOfBounds for near-MaxInt64 dictionary offset, got %v", err)
		}
	})

	t.Run("Negative index offset", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersion2,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{
					Level:            "v1",
					Offset:           100,
					CompressedSize:   200,
					UncompressedSize: 1000,
				},
			},
		}
		err := idx.ValidateBounds(-500)
		if !errors.Is(err, ErrOutOfBounds) {
			t.Fatalf("expected ErrOutOfBounds for negative index offset, got %v", err)
		}
	})

	t.Run("ReadTrailerAndIndex with overflowing trailer offset check", func(t *testing.T) {
		t.Parallel()
		// Construct full file buffer where indexOffset is near MaxInt64
		fileBuf := make([]byte, TrailerSize+1000)
		trailerBuf := fileBuf[1000:]
		binary.LittleEndian.PutUint64(trailerBuf[0:OffsetLen], uint64(math.MaxInt64-100))
		binary.LittleEndian.PutUint64(trailerBuf[OffsetLen:OffsetLen+SizeLen], 200)
		copy(trailerBuf[TrailerSize-MagicLen:], []byte(MagicString))

		totalSize := int64(len(fileBuf))
		_, err := ReadTrailerAndIndex(bytes.NewReader(fileBuf), totalSize)
		if !errors.Is(err, ErrInvalidIndexOffset) {
			t.Fatalf("expected ErrInvalidIndexOffset for extreme indexOffset, got %v", err)
		}
	})

	t.Run("ReadTrailerAndIndex with mismatched index size vs trailer boundary", func(t *testing.T) {
		t.Parallel()
		fileBuf := make([]byte, TrailerSize+1000)
		trailerBuf := fileBuf[1000:]
		// trailerOffset = 1000; set indexOffset = 200, indexSize = 900 (200 + 900 = 1100 != 1000)
		binary.LittleEndian.PutUint64(trailerBuf[0:OffsetLen], 200)
		binary.LittleEndian.PutUint64(trailerBuf[OffsetLen:OffsetLen+SizeLen], 900)
		copy(trailerBuf[TrailerSize-MagicLen:], []byte(MagicString))

		totalSize := int64(len(fileBuf))
		_, err := ReadTrailerAndIndex(bytes.NewReader(fileBuf), totalSize)
		if !errors.Is(err, ErrInvalidIndexSize) {
			t.Fatalf("expected ErrInvalidIndexSize for boundary mismatch, got %v", err)
		}
	})
}

func TestValidateChecksum(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "empty string allowed for optional fields",
			input:    "",
			expected: true,
		},
		{
			name:     "valid lowercase 64 hex characters",
			input:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			expected: true,
		},
		{
			name:     "valid uppercase 64 hex characters",
			input:    "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF",
			expected: true,
		},
		{
			name:     "valid mixed-case 64 hex characters",
			input:    "0123456789aBcDeF0123456789AbCdEf0123456789aBcDeF0123456789AbCdEf",
			expected: true,
		},
		{
			name:     "single character hex string rejected",
			input:    "a",
			expected: false,
		},
		{
			name:     "truncated 63 hex characters rejected",
			input:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde",
			expected: false,
		},
		{
			name:     "oversized 65 hex characters rejected",
			input:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0",
			expected: false,
		},
		{
			name:     "64 characters containing invalid character g",
			input:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeg",
			expected: false,
		},
		{
			name:     "64 characters containing path traversal characters",
			input:    "../../../../tmp/malicious_payload_with_enough_padding_to_reach_64",
			expected: false,
		},
		{
			name:     "64 characters containing control or punctuation characters",
			input:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde-",
			expected: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ValidateChecksum(tc.input)
			if got != tc.expected {
				t.Fatalf("ValidateChecksum(%q) = %v, expected %v", tc.input, got, tc.expected)
			}
		})
	}
}

func TestParseJSONString_Escapes(t *testing.T) {
	t.Parallel()

	t.Run("Valid JSON escapes and unicode", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`"hello\nworld\t\"quotes\"\\slash\/back\bform\ffeed\rcarriage\u0041"`)
		parsed, nextPos, err := parseJSONString(raw, 0)
		if err != nil {
			t.Fatalf("unexpected error parsing valid escapes: %v", err)
		}
		if nextPos != len(raw) {
			t.Fatalf("expected nextPos %d, got %d", len(raw), nextPos)
		}
		expected := "hello\nworld\t\"quotes\"\\slash/back\bform\ffeed\rcarriageA"
		if parsed != expected {
			t.Fatalf("expected %q, got %q", expected, parsed)
		}
	})

	t.Run("Invalid escape sequence \\q returns ErrInvalidJSONSyntax", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`"invalid \q escape"`)
		_, _, err := parseJSONString(raw, 0)
		if !errors.Is(err, ErrInvalidJSONSyntax) {
			t.Fatalf("expected ErrInvalidJSONSyntax for \\q, got %v", err)
		}
	})

	t.Run("Invalid escape sequence \\a returns ErrInvalidJSONSyntax", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`"invalid \a escape"`)
		_, _, err := parseJSONString(raw, 0)
		if !errors.Is(err, ErrInvalidJSONSyntax) {
			t.Fatalf("expected ErrInvalidJSONSyntax for \\a, got %v", err)
		}
	})

	t.Run("Invalid escape sequence \\1 returns ErrInvalidJSONSyntax", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`"invalid \1 escape"`)
		_, _, err := parseJSONString(raw, 0)
		if !errors.Is(err, ErrInvalidJSONSyntax) {
			t.Fatalf("expected ErrInvalidJSONSyntax for \\1, got %v", err)
		}
	})

	t.Run("Invalid escape sequence \\x returns ErrInvalidJSONSyntax", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`"invalid \x hex escape"`)
		_, _, err := parseJSONString(raw, 0)
		if !errors.Is(err, ErrInvalidJSONSyntax) {
			t.Fatalf("expected ErrInvalidJSONSyntax for \\x, got %v", err)
		}
	})

	t.Run("Unterminated escape sequence at EOF", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`"trailing escape \`)
		_, _, err := parseJSONString(raw, 0)
		if !errors.Is(err, ErrInvalidJSONSyntax) {
			t.Fatalf("expected ErrInvalidJSONSyntax for trailing escape, got %v", err)
		}
	})

	t.Run("Invalid unicode escape digits", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`"invalid unicode \u00GZ"`)
		_, _, err := parseJSONString(raw, 0)
		if !errors.Is(err, ErrInvalidJSONSyntax) {
			t.Fatalf("expected ErrInvalidJSONSyntax for bad unicode escape, got %v", err)
		}
	})

	t.Run("Truncated unicode escape", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`"truncated unicode \u00"`)
		_, _, err := parseJSONString(raw, 0)
		if !errors.Is(err, ErrInvalidJSONSyntax) {
			t.Fatalf("expected ErrInvalidJSONSyntax for truncated unicode escape, got %v", err)
		}
	})
}

func TestNormalizeVariant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{input: "", expected: "v1"},
		{input: "  ", expected: "v1"},
		{input: "v1", expected: "v1"},
		{input: "v2", expected: "v2"},
		{input: "v3", expected: "v3"},
		{input: "v4", expected: "v4"},
		{input: "V3", expected: "v3"},
		{input: "  v3  ", expected: "v3"},
		{input: "3", expected: "v3"},
		{input: "amd64_v1", expected: "v1"},
		{input: "amd64_v3", expected: "v3"},
		{input: "linux_amd64_v3", expected: "v3"},
		{input: "darwin_amd64_v2", expected: "v2"},
		{input: "windows_amd64_v4", expected: "v4"},
		{input: "x86_64_v3", expected: "v3"},
		{input: "arm64_v8.0", expected: testLevelV80},
		{input: "arm64-v8.2", expected: "v8.2"},
		{input: "aarch64_v9.0", expected: "v9.0"},
		{input: "aarch64-v8.0", expected: testLevelV80},
		{input: "8.0", expected: testLevelV80},
		{input: "v8.0", expected: testLevelV80},
		{input: "v9.5", expected: "v9.5"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			actual := NormalizeVariant(tt.input)
			if actual != tt.expected {
				t.Errorf("NormalizeVariant(%q) = %q; want %q", tt.input, actual, tt.expected)
			}
		})
	}
}

func TestFindVariant_NormalizedMatching(t *testing.T) {
	t.Parallel()

	idx := &Index{
		Version: FormatVersionCurrent,
		Variants: []VariantEntry{
			{Level: "v1", Offset: 100, CompressedSize: 100, UncompressedSize: 200},
			{Level: "v3", Offset: 200, CompressedSize: 100, UncompressedSize: 200},
		},
	}

	tests := []struct {
		query       string
		shouldFind  bool
		expectedLvl string
	}{
		{query: "v1", shouldFind: true, expectedLvl: "v1"},
		{query: "amd64_v1", shouldFind: true, expectedLvl: "v1"},
		{query: "AMD64_V3", shouldFind: true, expectedLvl: "v3"},
		{query: "linux_amd64_v3", shouldFind: true, expectedLvl: "v3"},
		{query: "3", shouldFind: true, expectedLvl: "v3"},
		{query: "v2", shouldFind: false, expectedLvl: ""},
		{query: "v4", shouldFind: false, expectedLvl: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.query, func(t *testing.T) {
			t.Parallel()
			entry, found := idx.FindVariant(tt.query)
			if found != tt.shouldFind {
				t.Fatalf("FindVariant(%q) found = %v; want %v", tt.query, found, tt.shouldFind)
			}
			if found && entry.Level != tt.expectedLvl {
				t.Errorf("FindVariant(%q).Level = %q; want %q", tt.query, entry.Level, tt.expectedLvl)
			}
		})
	}
}

func TestValidateBounds_VariantValidation(t *testing.T) {
	t.Parallel()

	t.Run("Empty variants slice returns ErrNoVariantsSpecified", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:  FormatVersionCurrent,
			Variants: []VariantEntry{},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrNoVariantsSpecified) {
			t.Fatalf("expected ErrNoVariantsSpecified, got %v", err)
		}
	})

	t.Run("Nil variants slice returns ErrNoVariantsSpecified", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:  FormatVersionCurrent,
			Variants: nil,
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrNoVariantsSpecified) {
			t.Fatalf("expected ErrNoVariantsSpecified, got %v", err)
		}
	})

	t.Run("Variant with empty level returns ErrInvalidVariant", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersionCurrent,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{Level: "   ", Offset: 100, CompressedSize: 100, UncompressedSize: 200},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrInvalidVariant) {
			t.Fatalf("expected ErrInvalidVariant for whitespace level, got %v", err)
		}
	})

	t.Run("Duplicate exact variant level returns ErrDuplicateVariant", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersionCurrent,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 100, CompressedSize: 100, UncompressedSize: 200, SHA256: testSHA256Sample},
				{Level: "v1", Offset: 200, CompressedSize: 100, UncompressedSize: 200, SHA256: testSHA256Sample},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrDuplicateVariant) {
			t.Fatalf("expected ErrDuplicateVariant for identical level, got %v", err)
		}
	})

	t.Run("Duplicate normalized variant aliases return ErrDuplicateVariant", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersionCurrent,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{Level: "v3", Offset: 100, CompressedSize: 100, UncompressedSize: 200, SHA256: testSHA256Sample},
				{Level: "amd64_v3", Offset: 200, CompressedSize: 100, UncompressedSize: 200, SHA256: testSHA256Sample},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrDuplicateVariant) {
			t.Fatalf("expected ErrDuplicateVariant for aliased levels, got %v", err)
		}
	})

	t.Run("Duplicate case-insensitive variant levels return ErrDuplicateVariant", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersionCurrent,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{Level: "v3", Offset: 100, CompressedSize: 100, UncompressedSize: 200, SHA256: testSHA256Sample},
				{Level: "V3", Offset: 200, CompressedSize: 100, UncompressedSize: 200, SHA256: testSHA256Sample},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrDuplicateVariant) {
			t.Fatalf("expected ErrDuplicateVariant for case mismatch, got %v", err)
		}
	})

	t.Run("Unrecognized target architecture returns ErrInvalidVariant", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersionCurrent,
			TargetArch: "mips64",
			Variants: []VariantEntry{
				{Level: "v1", Offset: 100, CompressedSize: 100, UncompressedSize: 200},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrInvalidVariant) {
			t.Fatalf("expected ErrInvalidVariant for unrecognized arch, got %v", err)
		}
	})

	t.Run("Empty target architecture returns ErrInvalidVariant", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersionCurrent,
			TargetArch: "",
			Variants: []VariantEntry{
				{Level: "v1", Offset: 100, CompressedSize: 100, UncompressedSize: 200},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrInvalidVariant) {
			t.Fatalf("expected ErrInvalidVariant for empty target arch, got %v", err)
		}
	})

	t.Run("AMD64 binary rejects ARM64 level", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersionCurrent,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{Level: "v8.2", Offset: 100, CompressedSize: 100, UncompressedSize: 200},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrInvalidVariant) {
			t.Fatalf("expected ErrInvalidVariant for arm64 tier in amd64 index, got %v", err)
		}
	})

	t.Run("ARM64 binary rejects AMD64 level", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersionCurrent,
			TargetArch: "arm64",
			Variants: []VariantEntry{
				{Level: "v3", Offset: 100, CompressedSize: 100, UncompressedSize: 200},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrInvalidVariant) {
			t.Fatalf("expected ErrInvalidVariant for amd64 tier in arm64 index, got %v", err)
		}
	})

	t.Run("Unknown tier level returns ErrInvalidVariant", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersionCurrent,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{Level: "v999", Offset: 100, CompressedSize: 100, UncompressedSize: 200},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrInvalidVariant) {
			t.Fatalf("expected ErrInvalidVariant for unknown tier v999, got %v", err)
		}
	})

	t.Run("Valid ARM64 variants pass ValidateBounds", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersionCurrent,
			TargetArch: "arm64",
			Variants: []VariantEntry{
				{Level: "v8.0", Offset: 100, CompressedSize: 100, UncompressedSize: 200, SHA256: testSHA256Sample},
				{Level: "v8.8", Offset: 300, CompressedSize: 100, UncompressedSize: 200, SHA256: testSHA256Sample},
				{Level: "v9.5", Offset: 500, CompressedSize: 100, UncompressedSize: 200, SHA256: testSHA256Sample},
			},
		}
		if err := idx.ValidateBounds(1000); err != nil {
			t.Fatalf("expected valid bounds for arm64 variants, got %v", err)
		}
	})

	t.Run("Format v2 rejects empty variant SHA256 in ValidateBounds", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersion2,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 100, CompressedSize: 100, UncompressedSize: 200, SHA256: ""},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrInvalidChecksum) {
			t.Fatalf("expected ErrInvalidChecksum for empty SHA256 in Format v2, got %v", err)
		}
	})

	t.Run("Format v2 rejects malformed variant SHA256 in ValidateBounds", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersion2,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 100, CompressedSize: 100, UncompressedSize: 200, SHA256: "not-a-valid-sha256"},
			},
		}
		err := idx.ValidateBounds(1000)
		if !errors.Is(err, ErrInvalidChecksum) {
			t.Fatalf("expected ErrInvalidChecksum for malformed SHA256 in Format v2, got %v", err)
		}
	})

	t.Run("Format v1 allows variant with empty SHA256 in ValidateBounds", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersion1,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 100, CompressedSize: 100, UncompressedSize: 200, SHA256: ""},
			},
		}
		if err := idx.ValidateBounds(1000); err != nil {
			t.Fatalf("expected valid bounds for Format v1 with empty SHA256, got %v", err)
		}
	})

	t.Run("Format v2 MarshalBinaryIndex rejects missing SHA256", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersion2,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 100, CompressedSize: 100, UncompressedSize: 200, SHA256: ""},
			},
		}
		_, err := MarshalBinaryIndex(idx)
		if !errors.Is(err, ErrInvalidChecksum) {
			t.Fatalf("expected ErrInvalidChecksum from MarshalBinaryIndex with empty SHA256, got %v", err)
		}
	})

	t.Run("Format v2 MarshalBinaryIndex rejects malformed SHA256", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:    FormatVersion2,
			TargetArch: testArchAMD64,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 100, CompressedSize: 100, UncompressedSize: 200, SHA256: "invalid-hex"},
			},
		}
		_, err := MarshalBinaryIndex(idx)
		if !errors.Is(err, ErrInvalidChecksum) {
			t.Fatalf("expected ErrInvalidChecksum from MarshalBinaryIndex with invalid SHA256, got %v", err)
		}
	})
}

func TestParseJSONInt64_BoundsAndOverflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantVal     int64
		wantErr     bool
		errSentinel error
	}{
		{name: "zero", input: "0", wantVal: 0, wantErr: false},
		{name: "negative zero", input: "-0", wantVal: 0, wantErr: false},
		{name: "leading zeroes", input: "000042", wantVal: 42, wantErr: false},
		{name: "standard positive", input: "123456789", wantVal: 123456789, wantErr: false},
		{name: "standard negative", input: "-123456789", wantVal: -123456789, wantErr: false},
		{name: "max int64", input: "9223372036854775807", wantVal: math.MaxInt64, wantErr: false},
		{name: "min int64", input: "-9223372036854775808", wantVal: math.MinInt64, wantErr: false},
		{name: "max int64 plus 1 overflow", input: "9223372036854775808", wantErr: true, errSentinel: ErrInvalidJSONSyntax},
		{name: "min int64 minus 1 underflow", input: "-9223372036854775809", wantErr: true, errSentinel: ErrInvalidJSONSyntax},
		{name: "huge positive overflow", input: "99999999999999999999999999999999", wantErr: true, errSentinel: ErrInvalidJSONSyntax},
		{name: "huge negative underflow", input: "-99999999999999999999999999999999", wantErr: true, errSentinel: ErrInvalidJSONSyntax},
		{name: "empty input", input: "", wantErr: true, errSentinel: ErrInvalidJSONSyntax},
		{name: "whitespace only", input: "   ", wantErr: true, errSentinel: ErrInvalidJSONSyntax},
		{name: "minus sign only", input: "-", wantErr: true, errSentinel: ErrInvalidJSONSyntax},
		{name: "minus sign with non-digit", input: "-abc", wantErr: true, errSentinel: ErrInvalidJSONSyntax},
		{name: "non-digit character", input: "abc", wantErr: true, errSentinel: ErrInvalidJSONSyntax},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, nextPos, err := parseJSONInt64([]byte(tt.input), 0)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseJSONInt64(%q) expected error, got val=%d, nextPos=%d", tt.input, got, nextPos)
				}
				if tt.errSentinel != nil && !errors.Is(err, tt.errSentinel) {
					t.Fatalf("parseJSONInt64(%q) error %v does not wrap sentinel %v", tt.input, err, tt.errSentinel)
				}
			} else {
				if err != nil {
					t.Fatalf("parseJSONInt64(%q) unexpected error: %v", tt.input, err)
				}
				if got != tt.wantVal {
					t.Fatalf("parseJSONInt64(%q) = %d; want %d", tt.input, got, tt.wantVal)
				}
			}
		})
	}
}

func TestUnmarshalJSONIndex_IntegerOverflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
	}{
		{
			name: "overflow in version",
			json: `{"version": 99999999999999999999, "variants": []}`,
		},
		{
			name: "overflow in created_unix",
			json: `{"version": 1, "created_unix": 99999999999999999999, "variants": []}`,
		},
		{
			name: "overflow in dictionary_offset",
			json: `{"version": 1, "dictionary_offset": 99999999999999999999, "variants": []}`,
		},
		{
			name: "overflow in dictionary_size",
			json: `{"version": 1, "dictionary_size": 99999999999999999999, "variants": []}`,
		},
		{
			name: "overflow in dictionary_id",
			json: `{"version": 1, "dictionary_id": 99999999999999999999, "variants": []}`,
		},
		{
			name: "overflow in variant offset",
			json: `{"version": 1, "variants": [{"level": "v1", "offset": 99999999999999999999}]}`,
		},
		{
			name: "overflow in variant compressed_size",
			json: `{"version": 1, "variants": [{"level": "v1", "compressed_size": 99999999999999999999}]}`,
		},
		{
			name: "overflow in variant uncompressed_size",
			json: `{"version": 1, "variants": [{"level": "v1", "uncompressed_size": 99999999999999999999}]}`,
		},
		{
			name: "underflow in variant offset",
			json: `{"version": 1, "variants": [{"level": "v1", "offset": -99999999999999999999}]}`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := unmarshalJSONIndex([]byte(tt.json))
			if err == nil {
				t.Fatalf("unmarshalJSONIndex expected error for %s, got nil", tt.name)
			}
			if !errors.Is(err, ErrInvalidJSONSyntax) {
				t.Fatalf("unmarshalJSONIndex expected ErrInvalidJSONSyntax for %s, got %v", tt.name, err)
			}
		})
	}
}

func TestReadTrailerAndIndex_IndexBoundsBoundaryEnforcement(t *testing.T) {
	t.Parallel()

	const (
		baseIndexOffset        = int64(1000)
		payloadStartOffset     = int64(700)
		exactBoundaryLength    = int64(300) // 700 + 300 = 1000 == baseIndexOffset (terminates exactly at index)
		overrunByOneLength     = int64(301) // 700 + 301 = 1001 == baseIndexOffset + 1 (overruns index by 1 byte)
		uncompressedTestSize   = int64(800)
		testDictOffset         = int64(100)
		testDictValidLength    = int64(200)
		testDictOverrunByOne   = int64(901) // 100 + 901 = 1001 == baseIndexOffset + 1 (overruns index by 1 byte)
		testVariantPostDict    = int64(300)
		testVariantPostDictLen = int64(700) // 300 + 700 = 1000 == baseIndexOffset (terminates exactly at index)
		dummyFutureOffset      = int64(1100)
		dummyVariantLength     = int64(50)
	)

	tests := []struct {
		name              string
		version           int
		buildIndex        func() *Index
		expectError       error
		expectErrorSubstr string
	}{
		{
			name:    "Format v2 rejects variant extending past indexOffset by 1 byte",
			version: FormatVersion2,
			buildIndex: func() *Index {
				return &Index{
					AppName:    testAppName,
					TargetOS:   testOSLinux,
					TargetArch: testArchAMD64,
					Variants: []VariantEntry{
						{
							Level:            "v1",
							Offset:           payloadStartOffset,
							CompressedSize:   overrunByOneLength,
							UncompressedSize: uncompressedTestSize,
							SHA256:           testSHA256Sample,
							Compression:      testCompression,
						},
					},
				}
			},
			expectError:       ErrOutOfBounds,
			expectErrorSubstr: "variant v1 payload extends past index offset 1000",
		},
		{
			name:    "Format v1 rejects variant extending past indexOffset by 1 byte",
			version: FormatVersion1,
			buildIndex: func() *Index {
				return &Index{
					AppName:    testAppName,
					TargetOS:   testOSLinux,
					TargetArch: testArchAMD64,
					Variants: []VariantEntry{
						{
							Level:            "v1",
							Offset:           payloadStartOffset,
							CompressedSize:   overrunByOneLength,
							UncompressedSize: uncompressedTestSize,
							SHA256:           testSHA256Sample,
							Compression:      testCompression,
						},
					},
				}
			},
			expectError:       ErrOutOfBounds,
			expectErrorSubstr: "variant v1 payload extends past index offset 1000",
		},
		{
			name:    "Format v2 rejects dictionary extending past indexOffset by 1 byte",
			version: FormatVersion2,
			buildIndex: func() *Index {
				return &Index{
					AppName:          testAppName,
					TargetOS:         testOSLinux,
					TargetArch:       testArchAMD64,
					DictionaryOffset: testDictOffset,
					DictionarySize:   testDictOverrunByOne,
					DictionarySHA256: testSHA256Sample,
					Variants: []VariantEntry{
						{
							Level:            "v1",
							Offset:           dummyFutureOffset,
							CompressedSize:   dummyVariantLength,
							UncompressedSize: uncompressedTestSize,
							SHA256:           testSHA256Sample,
							Compression:      testCompression,
						},
					},
				}
			},
			expectError:       ErrOutOfBounds,
			expectErrorSubstr: "dictionary payload extends past index offset 1000",
		},
		{
			name:    "Format v1 rejects dictionary extending past indexOffset by 1 byte",
			version: FormatVersion1,
			buildIndex: func() *Index {
				return &Index{
					AppName:          testAppName,
					TargetOS:         testOSLinux,
					TargetArch:       testArchAMD64,
					DictionaryOffset: testDictOffset,
					DictionarySize:   testDictOverrunByOne,
					DictionarySHA256: testSHA256Sample,
					Variants: []VariantEntry{
						{
							Level:            "v1",
							Offset:           dummyFutureOffset,
							CompressedSize:   dummyVariantLength,
							UncompressedSize: uncompressedTestSize,
							SHA256:           testSHA256Sample,
							Compression:      testCompression,
						},
					},
				}
			},
			expectError:       ErrOutOfBounds,
			expectErrorSubstr: "dictionary payload extends past index offset 1000",
		},
		{
			name:    "Format v2 accepts variant payload terminating exactly at indexOffset",
			version: FormatVersion2,
			buildIndex: func() *Index {
				return &Index{
					AppName:    testAppName,
					TargetOS:   testOSLinux,
					TargetArch: testArchAMD64,
					Variants: []VariantEntry{
						{
							Level:            "v1",
							Offset:           payloadStartOffset,
							CompressedSize:   exactBoundaryLength,
							UncompressedSize: uncompressedTestSize,
							SHA256:           testSHA256Sample,
							Compression:      testCompression,
						},
					},
				}
			},
			expectError: nil,
		},
		{
			name:    "Format v1 accepts variant payload terminating exactly at indexOffset",
			version: FormatVersion1,
			buildIndex: func() *Index {
				return &Index{
					AppName:    testAppName,
					TargetOS:   testOSLinux,
					TargetArch: testArchAMD64,
					Variants: []VariantEntry{
						{
							Level:            "v1",
							Offset:           payloadStartOffset,
							CompressedSize:   exactBoundaryLength,
							UncompressedSize: uncompressedTestSize,
							SHA256:           testSHA256Sample,
							Compression:      testCompression,
						},
					},
				}
			},
			expectError: nil,
		},
		{
			name:    "Format v2 accepts dictionary and variant terminating exactly at indexOffset",
			version: FormatVersion2,
			buildIndex: func() *Index {
				return &Index{
					AppName:          testAppName,
					TargetOS:         testOSLinux,
					TargetArch:       testArchAMD64,
					DictionaryOffset: testDictOffset,
					DictionarySize:   testDictValidLength,
					DictionarySHA256: testSHA256Sample,
					Variants: []VariantEntry{
						{
							Level:            "v1",
							Offset:           testVariantPostDict,
							CompressedSize:   testVariantPostDictLen,
							UncompressedSize: uncompressedTestSize,
							SHA256:           testSHA256Sample,
							Compression:      testCompression,
						},
					},
				}
			},
			expectError: nil,
		},
		{
			name:    "Format v1 accepts dictionary and variant terminating exactly at indexOffset",
			version: FormatVersion1,
			buildIndex: func() *Index {
				return &Index{
					AppName:          testAppName,
					TargetOS:         testOSLinux,
					TargetArch:       testArchAMD64,
					DictionaryOffset: testDictOffset,
					DictionarySize:   testDictValidLength,
					DictionarySHA256: testSHA256Sample,
					Variants: []VariantEntry{
						{
							Level:            "v1",
							Offset:           testVariantPostDict,
							CompressedSize:   testVariantPostDictLen,
							UncompressedSize: uncompressedTestSize,
							SHA256:           testSHA256Sample,
							Compression:      testCompression,
						},
					},
				}
			},
			expectError: nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			idx := tt.buildIndex()
			buf := bytes.NewBuffer(make([]byte, baseIndexOffset))
			writtenBytes, err := WriteIndexAndTrailerWithVersion(buf, idx, baseIndexOffset, tt.version)
			if err != nil {
				t.Fatalf("WriteIndexAndTrailerWithVersion failed: %v", err)
			}

			data := buf.Bytes()
			totalSize := baseIndexOffset + writtenBytes
			if int64(len(data)) != totalSize {
				t.Fatalf("expected totalSize %d, got %d", totalSize, len(data))
			}

			readIdx, err := ReadTrailerAndIndex(bytes.NewReader(data), totalSize)
			if tt.expectError != nil {
				if !errors.Is(err, tt.expectError) {
					t.Fatalf("expected error wrapping %v, got %v", tt.expectError, err)
				}
				if tt.expectErrorSubstr != "" && !strings.Contains(err.Error(), tt.expectErrorSubstr) {
					t.Fatalf("expected error message to contain %q, got %q", tt.expectErrorSubstr, err.Error())
				}
				if readIdx != nil {
					t.Fatalf("expected nil index on error, got %+v", readIdx)
				}
			} else {
				if err != nil {
					t.Fatalf("expected clean deserialization, got %v", err)
				}
				if readIdx == nil {
					t.Fatal("expected non-nil index on success")
				}
			}
		})
	}
}

func TestFormat_MandatoryDictionarySHA256(t *testing.T) {
	t.Parallel()

	const (
		baseOffset   = 1000
		dictOffset   = 1000
		dictSize     = 500
		variantOff   = 1500
		variantComp  = 500
		variantUncmp = 1000
		boundaryMax  = 3000
	)

	validVariant := VariantEntry{
		Level:            "v1",
		Offset:           variantOff,
		CompressedSize:   variantComp,
		UncompressedSize: variantUncmp,
		SHA256:           testSHA256Sample,
		Compression:      testCompression,
	}

	t.Run("Format v2 rejects empty dictionary SHA256 in ValidateBounds", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: dictOffset,
			DictionarySize:   dictSize,
			DictionarySHA256: "",
			Variants:         []VariantEntry{validVariant},
		}
		err := idx.ValidateBounds(boundaryMax)
		if !errors.Is(err, ErrInvalidChecksum) {
			t.Fatalf("expected ErrInvalidChecksum for empty dict SHA256 in Format v2, got %v", err)
		}
		if !strings.Contains(err.Error(), "dictionary missing or invalid sha256 checksum in Format v2") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("Format v2 rejects malformed dictionary SHA256 in ValidateBounds", func(t *testing.T) {
		t.Parallel()
		malformedCases := []struct {
			name   string
			sha256 string
		}{
			{name: "too short", sha256: "0123456789abcdef"},
			{name: "too long", sha256: testSHA256Sample + "0"},
			{name: "invalid characters", sha256: strings.Repeat("z", maxSHA256HexLen)},
		}

		for _, tc := range malformedCases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				idx := &Index{
					Version:          FormatVersion2,
					TargetArch:       testArchAMD64,
					DictionaryOffset: dictOffset,
					DictionarySize:   dictSize,
					DictionarySHA256: tc.sha256,
					Variants:         []VariantEntry{validVariant},
				}
				err := idx.ValidateBounds(boundaryMax)
				if !errors.Is(err, ErrInvalidChecksum) {
					t.Fatalf("expected ErrInvalidChecksum for %s, got %v", tc.name, err)
				}
			})
		}
	})

	t.Run("Format v2 accepts valid dictionary SHA256 in ValidateBounds", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: dictOffset,
			DictionarySize:   dictSize,
			DictionarySHA256: testSHA256Sample,
			Variants:         []VariantEntry{validVariant},
		}
		if err := idx.ValidateBounds(boundaryMax); err != nil {
			t.Fatalf("expected valid bounds, got %v", err)
		}
	})

	t.Run("Format v2 allows empty dictionary SHA256 when DictionarySize is zero", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: 0,
			DictionarySize:   0,
			DictionarySHA256: "",
			Variants:         []VariantEntry{validVariant},
		}
		if err := idx.ValidateBounds(boundaryMax); err != nil {
			t.Fatalf("expected valid bounds with zero-size dictionary, got %v", err)
		}
	})

	t.Run("Format v1 allows empty dictionary SHA256 when DictionarySize is non-zero", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion1,
			TargetArch:       testArchAMD64,
			DictionaryOffset: dictOffset,
			DictionarySize:   dictSize,
			DictionarySHA256: "",
			Variants:         []VariantEntry{validVariant},
		}
		if err := idx.ValidateBounds(boundaryMax); err != nil {
			t.Fatalf("expected valid bounds for Format v1 with empty dict SHA256, got %v", err)
		}
	})

	t.Run("Format v1 rejects malformed dictionary SHA256 when non-empty", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion1,
			TargetArch:       testArchAMD64,
			DictionaryOffset: dictOffset,
			DictionarySize:   dictSize,
			DictionarySHA256: "invalid-hash",
			Variants:         []VariantEntry{validVariant},
		}
		err := idx.ValidateBounds(boundaryMax)
		if !errors.Is(err, ErrInvalidChecksum) {
			t.Fatalf("expected ErrInvalidChecksum for malformed dict SHA in Format v1, got %v", err)
		}
		if !strings.Contains(err.Error(), "invalid dictionary sha256 checksum format") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("Format v2 MarshalBinaryIndex rejects non-zero dictionary with empty SHA256", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			AppName:          testAppName,
			TargetOS:         testOSLinux,
			TargetArch:       testArchAMD64,
			DictionaryOffset: dictOffset,
			DictionarySize:   dictSize,
			DictionarySHA256: "",
			Variants:         []VariantEntry{validVariant},
		}
		_, err := MarshalBinaryIndex(idx)
		if !errors.Is(err, ErrInvalidChecksum) {
			t.Fatalf("expected ErrInvalidChecksum from MarshalBinaryIndex, got %v", err)
		}
	})

	t.Run("Format v2 MarshalBinaryIndex rejects non-zero dictionary with malformed SHA256", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			AppName:          testAppName,
			TargetOS:         testOSLinux,
			TargetArch:       testArchAMD64,
			DictionaryOffset: dictOffset,
			DictionarySize:   dictSize,
			DictionarySHA256: "malformed-sha",
			Variants:         []VariantEntry{validVariant},
		}
		_, err := MarshalBinaryIndex(idx)
		if !errors.Is(err, ErrInvalidChecksum) {
			t.Fatalf("expected ErrInvalidChecksum from MarshalBinaryIndex, got %v", err)
		}
	})

	t.Run("Format v2 UnmarshalBinaryIndex rejects crafted non-zero dictionary with empty SHA256", func(t *testing.T) {
		t.Parallel()
		goodIdx := &Index{
			Version:          FormatVersion2,
			AppName:          testAppName,
			TargetOS:         testOSLinux,
			TargetArch:       testArchAMD64,
			DictionaryOffset: dictOffset,
			DictionarySize:   dictSize,
			DictionarySHA256: testSHA256Sample,
			Variants:         []VariantEntry{validVariant},
		}
		goodBytes, err := MarshalBinaryIndex(goodIdx)
		if err != nil {
			t.Fatalf("MarshalBinaryIndex failed: %v", err)
		}

		// In goodBytes: offset 34 is dictSHALen (64).
		// Splice out the 64 SHA bytes and set length prefix to 0.
		const dictSHALenOffset = 34
		crafted := make([]byte, 0, len(goodBytes)-maxSHA256HexLen)
		crafted = append(crafted, goodBytes[:dictSHALenOffset]...)
		crafted = append(crafted, 0)
		crafted = append(crafted, goodBytes[dictSHALenOffset+1+maxSHA256HexLen:]...)

		_, err = UnmarshalBinaryIndex(crafted)
		if !errors.Is(err, ErrInvalidChecksum) {
			t.Fatalf("expected ErrInvalidChecksum from UnmarshalBinaryIndex on crafted empty dict SHA, got %v", err)
		}
	})

	t.Run("Format v2 UnmarshalBinaryIndex rejects crafted non-zero dictionary with malformed SHA256", func(t *testing.T) {
		t.Parallel()
		goodIdx := &Index{
			Version:          FormatVersion2,
			AppName:          testAppName,
			TargetOS:         testOSLinux,
			TargetArch:       testArchAMD64,
			DictionaryOffset: dictOffset,
			DictionarySize:   dictSize,
			DictionarySHA256: testSHA256Sample,
			Variants:         []VariantEntry{validVariant},
		}
		goodBytes, err := MarshalBinaryIndex(goodIdx)
		if err != nil {
			t.Fatalf("MarshalBinaryIndex failed: %v", err)
		}

		// Mutate SHA256 bytes to non-hex
		const dictSHAStart = 35
		crafted := make([]byte, len(goodBytes))
		copy(crafted, goodBytes)
		copy(crafted[dictSHAStart:dictSHAStart+maxSHA256HexLen], []byte(strings.Repeat("?", maxSHA256HexLen)))

		_, err = UnmarshalBinaryIndex(crafted)
		if !errors.Is(err, ErrInvalidChecksum) {
			t.Fatalf("expected ErrInvalidChecksum from UnmarshalBinaryIndex on malformed dict SHA, got %v", err)
		}
	})

	t.Run("ReadTrailerAndIndex rejects crafted Format v2 binary with non-zero dictionary and empty SHA256", func(t *testing.T) {
		t.Parallel()
		goodIdx := &Index{
			Version:          FormatVersion2,
			AppName:          testAppName,
			TargetOS:         testOSLinux,
			TargetArch:       testArchAMD64,
			DictionaryOffset: dictOffset,
			DictionarySize:   dictSize,
			DictionarySHA256: testSHA256Sample,
			Variants:         []VariantEntry{validVariant},
		}
		goodBytes, err := MarshalBinaryIndex(goodIdx)
		if err != nil {
			t.Fatalf("MarshalBinaryIndex failed: %v", err)
		}

		const dictSHALenOffset = 34
		craftedIndex := make([]byte, 0, len(goodBytes)-maxSHA256HexLen)
		craftedIndex = append(craftedIndex, goodBytes[:dictSHALenOffset]...)
		craftedIndex = append(craftedIndex, 0)
		craftedIndex = append(craftedIndex, goodBytes[dictSHALenOffset+1+maxSHA256HexLen:]...)

		buf := bytes.NewBuffer(make([]byte, boundaryMax))
		idxOffset := int64(buf.Len())
		buf.Write(craftedIndex)
		idxSize := int64(len(craftedIndex))

		trailer := make([]byte, TrailerSize)
		binary.LittleEndian.PutUint64(trailer[0:8], uint64(idxOffset))
		binary.LittleEndian.PutUint64(trailer[8:16], uint64(idxSize))
		h := sha256.Sum256(craftedIndex)
		copy(trailer[16:48], h[:])
		copy(trailer[48:56], []byte(MagicString))
		buf.Write(trailer)

		totalSize := int64(buf.Len())
		_, err = ReadTrailerAndIndex(bytes.NewReader(buf.Bytes()), totalSize)
		if !errors.Is(err, ErrInvalidChecksum) {
			t.Fatalf("expected ErrInvalidChecksum from ReadTrailerAndIndex, got %v", err)
		}
	})

	t.Run("ReadTrailerAndIndex accepts Format v1 binary with non-zero dictionary and empty SHA256", func(t *testing.T) {
		t.Parallel()
		idxV1 := &Index{
			Version:          FormatVersion1,
			AppName:          testAppName,
			TargetOS:         testOSLinux,
			TargetArch:       testArchAMD64,
			CreatedUnix:      1724540000,
			DictionaryOffset: dictOffset,
			DictionarySize:   dictSize,
			DictionarySHA256: "",
			Variants:         []VariantEntry{validVariant},
		}

		buf := bytes.NewBuffer(make([]byte, boundaryMax))
		written, err := WriteIndexAndTrailerWithVersion(buf, idxV1, boundaryMax, FormatVersion1)
		if err != nil {
			t.Fatalf("WriteIndexAndTrailerWithVersion failed: %v", err)
		}

		totalSize := boundaryMax + written
		parsed, err := ReadTrailerAndIndex(bytes.NewReader(buf.Bytes()), totalSize)
		if err != nil {
			t.Fatalf("expected clean parse for Format v1 with empty dict SHA256, got %v", err)
		}
		if parsed.DictionarySize != dictSize {
			t.Fatalf("expected DictionarySize %d, got %d", dictSize, parsed.DictionarySize)
		}
		if parsed.DictionarySHA256 != "" {
			t.Fatalf("expected empty DictionarySHA256, got %q", parsed.DictionarySHA256)
		}
	})
}


