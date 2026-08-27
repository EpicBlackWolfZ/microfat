package format

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testCompression  = "zstd"
	testArchAMD64    = "amd64"
	testOSLinux      = "linux"
	testAppName      = "testapp"
	testSHA256Sample = "abcdef123456"
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
				SHA256:           "123456abcdef",
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
				SHA256:           "123456abcdef",
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
		TargetOS:   "linux",
		TargetArch: "amd64",
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
		TargetOS:    "linux",
		TargetArch:  "amd64",
		CreatedUnix: 123456,
		Variants: []VariantEntry{
			{Level: "v1", Offset: 100, CompressedSize: 200, UncompressedSize: 300, SHA256: "abc", Compression: "zstd"},
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
				"sha256": "abc",
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

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
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

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
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
		TargetArch:       "amd64",
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

	t.Run("Valid with dictionary", func(t *testing.T) {
		t.Parallel()
		idx := &Index{
			Version:          FormatVersion2,
			DictionaryOffset: 1000,
			DictionarySize:   500,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 1500, CompressedSize: 500, UncompressedSize: 1000},
				{Level: "v2", Offset: 2000, CompressedSize: 500, UncompressedSize: 1000},
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
			DictionaryOffset: 2500,
			DictionarySize:   1000,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 1500, CompressedSize: 500, UncompressedSize: 1000},
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
			DictionaryOffset: 1000,
			DictionarySize:   1000,
			Variants: []VariantEntry{
				{Level: "v1", Offset: 1500, CompressedSize: 500, UncompressedSize: 1000},
			},
		}
		if err := idx.ValidateBounds(3000); !errors.Is(err, ErrOverlappingVariant) {
			t.Fatalf("expected ErrOverlappingVariant when variant overlaps dict, got %v", err)
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


