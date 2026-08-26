package format

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const (
	testCompression = "zstd"
	testArchAMD64   = "amd64"
	testOSLinux     = "linux"
	testAppName     = "testapp"
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
		SelectedSHA256:          "abcdef123456",
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
				SHA256:           "abcdef123456",
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

