package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/klauspost/compress/zstd"
)

const (
	testArchAMD64 = "amd64"
	testPathEnv   = "PATH=/bin"
	testAppArg    = "app"
)

func TestPrintHelpAndInfo(t *testing.T) {
	idx := &format.Index{
		Version:     1,
		AppName:     "testapp",
		TargetOS:    "linux",
		TargetArch:  testArchAMD64,
		CreatedUnix: 1700000000,
		Variants: []format.VariantEntry{
			{
				Level:            "v1",
				Offset:           1000,
				CompressedSize:   500,
				UncompressedSize: 1000,
				SHA256:           "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			},
		},
	}
	hostInfo := microarch.Info{
		OS:       "linux",
		Arch:     testArchAMD64,
		Level:    "v3",
		Features: []string{"avx", "avx2"},
	}

	// Should execute without panic
	printHelp(idx, hostInfo, &idx.Variants[0])
	printInfo(idx, hostInfo, &idx.Variants[0], 2000)
}

func TestBuildAutoTunedEnviron(t *testing.T) {
	base := []string{"PATH=/usr/bin", "USER=test"}

	// 1. Standard auto-tune with metadata injection
	env := buildAutoTunedEnviron(base, "v3", format.ExecModeMemfd)
	hasVariant := false
	hasExecMode := false
	for _, e := range env {
		if e == "MICROFAT_SELECTED_VARIANT=v3" {
			hasVariant = true
		}
		if e == "MICROFAT_EXEC_MODE=memfd" {
			hasExecMode = true
		}
	}
	if !hasVariant || !hasExecMode {
		t.Errorf("expected telemetry env vars in env: %v", env)
	}

	// 2. Opt-out via MICROFAT_AUTOTUNE=0
	t.Setenv("MICROFAT_AUTOTUNE", "0")
	envOptOut := buildAutoTunedEnviron(base, "v1", format.ExecModeCache)
	if len(envOptOut) != len(base)+2 { // base + 2 injected metadata vars
		t.Errorf("expected opt-out env to have len %d, got %d", len(base)+2, len(envOptOut))
	}

	// 3. Opt-out via MICROFAT_AUTOTUNE=false
	t.Setenv("MICROFAT_AUTOTUNE", "false")
	envFalse := buildAutoTunedEnviron(base, "v1", format.ExecModeCache)
	if len(envFalse) != len(base)+2 {
		t.Errorf("expected opt-out false to match base + 2 length")
	}

	// 4. Preserve existing GOMEMLIMIT and GOMAXPROCS
	t.Setenv("MICROFAT_AUTOTUNE", "1")
	existing := []string{"GOMEMLIMIT=1GiB", "GOMAXPROCS=8"}
	envPreserve := buildAutoTunedEnviron(existing, "v3", format.ExecModeMemfd)
	var foundMem, foundProcs bool
	for _, e := range envPreserve {
		if e == "GOMEMLIMIT=1GiB" {
			foundMem = true
		}
		if e == "GOMAXPROCS=8" {
			foundProcs = true
		}
	}
	if !foundMem || !foundProcs {
		t.Errorf("failed to preserve existing user env: %v", envPreserve)
	}

	// 5. Custom memory ratio variations
	t.Setenv("MICROFAT_MEM_RATIO", "0.85")
	_ = buildAutoTunedEnviron(base, "v3", format.ExecModeMemfd)

	t.Setenv("MICROFAT_MEM_RATIO", "invalid")
	_ = buildAutoTunedEnviron(base, "v3", format.ExecModeMemfd)

	t.Setenv("MICROFAT_MEM_RATIO", "1.5")
	_ = buildAutoTunedEnviron(base, "v3", format.ExecModeMemfd)

	t.Setenv("MICROFAT_MEM_RATIO", "-0.2")
	_ = buildAutoTunedEnviron(base, "v3", format.ExecModeMemfd)

	// 6. Test with mocked active cgroup limits
	oldReadCgroup := readCgroupLimitsFunc
	defer func() { readCgroupLimitsFunc = oldReadCgroup }()

	// cgroup read error
	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{}, errors.New("cgroup error")
	}
	_ = buildAutoTunedEnviron([]string{testPathEnv}, "v3", format.ExecModeMemfd)

	// cgroup unknown version
	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{CgroupVersion: cgroup.VersionUnknown}, nil
	}
	_ = buildAutoTunedEnviron([]string{testPathEnv}, "v3", format.ExecModeMemfd)

	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{
			CgroupVersion:    cgroup.VersionV2,
			MemoryLimitBytes: 1024 * 1024 * 1024,
			CPUQuota:         4.0,
			CPUs:             4,
		}, nil
	}

	envCgroup := buildAutoTunedEnviron([]string{testPathEnv}, "v3", format.ExecModeMemfd)
	var foundAutoMem, foundAutoCPU bool
	for _, e := range envCgroup {
		if strings.HasPrefix(e, "GOMEMLIMIT=") {
			foundAutoMem = true
		}
		if strings.HasPrefix(e, "GOMAXPROCS=4") {
			foundAutoCPU = true
		}
	}
	if !foundAutoMem || !foundAutoCPU {
		t.Errorf("expected auto-tuned GOMEMLIMIT and GOMAXPROCS in env: %v", envCgroup)
	}

	// 7. Test printInfo with mocked limits
	idx := &format.Index{
		AppName:    "testapp",
		TargetOS:   "linux",
		TargetArch: testArchAMD64,
		Variants: []format.VariantEntry{
			{Level: "v3", Offset: 100, CompressedSize: 200, UncompressedSize: 300, SHA256: "hash"},
		},
	}
	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3", Features: []string{"avx2"}}
	printInfo(idx, hostInfo, &idx.Variants[0], 1000)

	// Test printInfo with unlimited limits
	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{
			CgroupVersion:    cgroup.VersionV1,
			MemoryLimitBytes: 0,
			CPUQuota:         0,
			CPUs:             0,
		}, nil
	}
	printInfo(idx, hostInfo, &idx.Variants[0], 1000)
}

func TestLogDiagnostics(t *testing.T) {
	entry := &format.VariantEntry{Level: "v3"}
	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	env := []string{"GOMEMLIMIT=1000B", "GOMAXPROCS=4"}

	// Text debug output
	t.Setenv("MICROFAT_DEBUG", "1")
	t.Setenv("MICROFAT_LOG", "")
	logDiagnostics(entry, format.ExecModeMemfd, hostInfo, env)

	// JSON log output
	t.Setenv("MICROFAT_DEBUG", "0")
	t.Setenv("MICROFAT_LOG", "json")
	logDiagnostics(entry, format.ExecModeCache, hostInfo, env)

	// Debug false without log
	t.Setenv("MICROFAT_DEBUG", "0")
	t.Setenv("MICROFAT_LOG", "")
	logDiagnostics(entry, format.ExecModeMemfd, hostInfo, env)
}

func TestGetSelfExecutablePath(t *testing.T) {
	path, err := getSelfExecutablePathFunc()
	if err != nil {
		t.Fatalf("getSelfExecutablePathFunc failed: %v", err)
	}
	if path == "" {
		t.Errorf("expected non-empty executable path")
	}
}

func TestHelperFunctions(t *testing.T) {
	if !isPrefixOrExact("--microfat:trim", "--microfat:trim") {
		t.Errorf("expected exact match true")
	}
	if !isPrefixOrExact("--microfat:trim=dest", "--microfat:trim") {
		t.Errorf("expected prefix match true")
	}
	if isPrefixOrExact("--microfat:other", "--microfat:trim") {
		t.Errorf("expected mismatch false")
	}
	if isPrefixOrExact("anything", "") {
		t.Errorf("expected empty flag to return false")
	}

	target, err := extractTargetPath("--microfat:trim-to=/path/to/target", "--microfat:trim-to", "")
	if err != nil || target != "/path/to/target" {
		t.Errorf("extractTargetPath with = failed: %v, got %s", err, target)
	}

	targetAlias, err := extractTargetPath("--microfat:specialize-to=/path/alias", "--microfat:trim-to", "--microfat:specialize-to")
	if err != nil || targetAlias != "/path/alias" {
		t.Errorf("extractTargetPath with alias failed: %v, got %s", err, targetAlias)
	}

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{testAppArg, "--microfat:trim-to"}

	_, err = extractTargetPath("--microfat:trim-to", "--microfat:trim-to", "")
	if err == nil {
		t.Errorf("expected error when no target path provided")
	}
}

func TestExtractVariantAndOptimize(t *testing.T) {
	tempDir := t.TempDir()

	// Create dummy compressed file
	payloadData := []byte("HELLO EXECUTABLE PAYLOAD")
	entry, rawFile := createDummyVariantFile(t, tempDir, payloadData)
	defer func() { _ = rawFile.Close() }()

	// Test extractVariantToWriter
	var buf bytes.Buffer
	if err := extractVariantToWriter(rawFile, entry, &buf); err != nil {
		t.Fatalf("extractVariantToWriter failed: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), payloadData) {
		t.Errorf("extracted payload mismatch: got %q, expected %q", buf.String(), string(payloadData))
	}

	// Test optimizeTo
	destPath := filepath.Join(tempDir, "sub", "extracted_binary")
	if err := optimizeTo(destPath, rawFile, entry); err != nil {
		t.Fatalf("optimizeTo failed: %v", err)
	}

	readBack, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading back optimized binary: %v", err)
	}
	if !bytes.Equal(readBack, payloadData) {
		t.Errorf("materialized binary content mismatch: got %q", string(readBack))
	}

	// Test optimizeInPlace
	replaceTarget := filepath.Join(tempDir, "to_replace")
	_ = os.WriteFile(replaceTarget, []byte("old-data"), 0o755)
	if err := optimizeInPlace(replaceTarget, rawFile, entry); err != nil {
		t.Fatalf("optimizeInPlace failed: %v", err)
	}
	readReplaced, _ := os.ReadFile(replaceTarget)
	if !bytes.Equal(readReplaced, payloadData) {
		t.Errorf("optimizeInPlace content mismatch: got %q", string(readReplaced))
	}
}

func TestTrimToAndInPlace(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("stub-bytes-data"), 0o755)

	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("variant-v1-bytes-long-content"), 0o755)

	fatPath := filepath.Join(tempDir, "fat_app")
	_, err := pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "testapp",
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Path},
	})
	if err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	fatFile, err := os.Open(fatPath)
	if err != nil {
		t.Fatalf("open fat file: %v", err)
	}
	defer func() { _ = fatFile.Close() }()

	stat, _ := fatFile.Stat()

	// Test trimTo
	trimmedTarget := filepath.Join(tempDir, "sub2", "trimmed_app")
	if err := trimTo(trimmedTarget, fatFile, stat.Size(), "v1"); err != nil {
		t.Fatalf("trimTo failed: %v", err)
	}

	// Test trimInPlace on a copy
	copyPath := filepath.Join(tempDir, "fat_copy")
	data, _ := os.ReadFile(fatPath)
	_ = os.WriteFile(copyPath, data, 0o755)
	copyFile, _ := os.Open(copyPath)
	defer func() { _ = copyFile.Close() }()

	if err := trimInPlace(copyPath, copyFile, stat.Size(), "v1"); err != nil {
		t.Fatalf("trimInPlace failed: %v", err)
	}

	// Test trimInPlace via symlink
	symPath := filepath.Join(tempDir, "fat_symlink")
	if err := os.Symlink(copyPath, symPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}
	if err := trimInPlace(symPath, copyFile, stat.Size(), "v1"); err != nil {
		t.Fatalf("trimInPlace on symlink failed: %v", err)
	}
}

func TestRunBinaryMetaCommands(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("stub-bytes-data"), 0o755)

	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("v1-bytes-data"), 0o755)

	fatPath := filepath.Join(tempDir, "fat_runner_app")
	_, err := pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "runnertest",
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Path},
	})
	if err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// 1. --microfat:help
	os.Args = []string{fatPath, "--microfat:help"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary --microfat:help failed: %v", err)
	}

	// 2. --microfat:info
	os.Args = []string{fatPath, "--microfat:info"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary --microfat:info failed: %v", err)
	}

	// 3. --microfat:trim-to
	destTrim := filepath.Join(tempDir, "trimmed_via_run")
	os.Args = []string{fatPath, "--microfat:trim-to=" + destTrim}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary --microfat:trim-to failed: %v", err)
	}

	// 3b. --microfat:specialize-to alias
	destSpec := filepath.Join(tempDir, "specialized_via_run")
	os.Args = []string{fatPath, "--microfat:specialize-to=" + destSpec}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary --microfat:specialize-to failed: %v", err)
	}

	// 3c. --microfat:trim-to without argument
	os.Args = []string{fatPath, "--microfat:trim-to"}
	if err := runBinary(fatPath); err == nil {
		t.Errorf("expected error when --microfat:trim-to has no target path")
	}

	// 3d. --microfat:trim-to with separate target path argument
	destTrimSep := filepath.Join(tempDir, "trimmed_via_run_sep")
	os.Args = []string{fatPath, "--microfat:trim-to", destTrimSep}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary --microfat:trim-to separate arg failed: %v", err)
	}

	// 4. --microfat:optimize-to
	destOpt := filepath.Join(tempDir, "optimized_via_run")
	os.Args = []string{fatPath, "--microfat:optimize-to=" + destOpt}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary --microfat:optimize-to failed: %v", err)
	}

	// 5. --microfat:trim in-place on a copy
	copyFat := filepath.Join(tempDir, "fat_for_trim")
	data, _ := os.ReadFile(fatPath)
	_ = os.WriteFile(copyFat, data, 0o755)
	os.Args = []string{copyFat, "--microfat:trim"}
	if err := runBinary(copyFat); err != nil {
		t.Fatalf("runBinary --microfat:trim failed: %v", err)
	}

	// 6. --microfat:optimize in-place on a copy
	copyFat2 := filepath.Join(tempDir, "fat_for_opt")
	_ = os.WriteFile(copyFat2, data, 0o755)
	os.Args = []string{copyFat2, "--microfat:optimize"}
	if err := runBinary(copyFat2); err != nil {
		t.Fatalf("runBinary --microfat:optimize failed: %v", err)
	}

	// 7. Transparent application argument forwarding
	oldExec := execveFunc
	defer func() { execveFunc = oldExec }()
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		return nil
	}
	os.Args = []string{fatPath, "myarg1", "--flag"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary standard execution failed: %v", err)
	}

	// 8. Error cases
	os.Args = []string{stubPath, "--microfat:info"}
	if err := runBinary(stubPath); err == nil {
		t.Errorf("expected error running non-fat binary")
	}

	if err := runBinary(filepath.Join(tempDir, "nonexistent")); err == nil {
		t.Errorf("expected error running non-existent binary")
	}

	// 9. Incompatible architecture fat binary
	incompatibleFat := filepath.Join(tempDir, "fat_incompatible")
	_, _ = pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        incompatibleFat,
		TargetArch:        "unknown_arch_xyz",
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Path},
	})
	if err := runBinary(incompatibleFat); err == nil {
		t.Errorf("expected error running fat binary with incompatible arch")
	}
}

func TestExecuteVariantExecutionPaths(t *testing.T) {
	tempDir := t.TempDir()
	payloadData := []byte("PAYLOAD_EXEC_TEST")
	entry, rawFile := createDummyVariantFile(t, tempDir, payloadData)
	defer func() { _ = rawFile.Close() }()

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	oldExec := execveFunc
	defer func() { execveFunc = oldExec }()

	// 1. Successful memfd execve
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		return nil
	}
	if err := executeVariant(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo); err != nil {
		t.Errorf("expected success for executeVariant, got %v", err)
	}

	// 2. Fallback to cache when memfd execution returns error
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		return errors.New("simulated execve error")
	}
	err := executeVariant(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo)
	if err == nil || !strings.Contains(err.Error(), "cache fallback execve failed") {
		t.Errorf("expected cache fallback error, got %v", err)
	}

	// 3. Fallback to cache when cached binary already exists
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		return nil
	}
	if err := executeViaCache(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, errors.New("memfd err")); err != nil {
		t.Errorf("expected success for existing cached binary, got %v", err)
	}

	// 4. Fallback to cache with fresh cache directory extraction
	freshCacheDir := filepath.Join(tempDir, "fresh_cache_dir")
	t.Setenv("XDG_CACHE_HOME", freshCacheDir)
	if err := executeViaCache(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, errors.New("memfd err")); err != nil {
		t.Errorf("expected success for fresh cache directory extraction, got %v", err)
	}

	// 5. Fallback when XDG_CACHE_HOME is empty (uses user home dir)
	t.Setenv("XDG_CACHE_HOME", "")
	if err := executeViaCache(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, errors.New("memfd err")); err != nil {
		t.Errorf("expected success when XDG_CACHE_HOME is empty, got %v", err)
	}

	// 5b. Fallback when XDG_CACHE_HOME is empty and userHomeDirFunc fails (uses os.TempDir())
	oldHome := userHomeDirFunc
	defer func() { userHomeDirFunc = oldHome }()
	userHomeDirFunc = func() (string, error) {
		return "", errors.New("no home dir")
	}
	if err := executeViaCache(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, errors.New("memfd err")); err != nil {
		t.Errorf("expected success when userHomeDir fails, got %v", err)
	}
	userHomeDirFunc = oldHome

	// 6. Fallback when primary cacheDir cannot be created (forbidden path)
	t.Setenv("XDG_CACHE_HOME", "/dev/null/forbidden_path")
	if err := executeViaCache(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, errors.New("memfd err")); err != nil {
		t.Errorf("expected success falling back to /tmp/.microfat-uid, got %v", err)
	}

	// 6b. Fallback failure when both primary and secondary cacheDir cannot be created
	t.Setenv("XDG_CACHE_HOME", "/dev/null/forbidden_primary")
	t.Setenv("TMPDIR", "/dev/null/forbidden_secondary")
	if err := executeViaCache(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, errors.New("memfd err")); err == nil {
		t.Errorf("expected error when both cache directories cannot be created")
	}
	t.Setenv("TMPDIR", "")

	// 7. Error extracting variant to cache fallback
	badEntryCache := &format.VariantEntry{Level: "v1", Offset: 0, CompressedSize: 10, UncompressedSize: 50, SHA256: "unique_bad_hash_123"}
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tempDir, "bad_cache_extract"))
	err = executeViaCache(rawFile, badEntryCache, []string{testAppArg}, []string{testPathEnv}, hostInfo, errors.New("memfd err"))
	if err == nil {
		t.Errorf("expected error extracting invalid entry in executeViaCache")
	}

	// 8. Error creating temp file in read-only cache dir
	roCacheDir := filepath.Join(tempDir, "ro_cache_dir")
	_ = os.MkdirAll(filepath.Join(roCacheDir, "microfat"), 0o755)
	_ = os.Chmod(filepath.Join(roCacheDir, "microfat"), 0o555)
	defer func() { _ = os.Chmod(filepath.Join(roCacheDir, "microfat"), 0o755) }()
	t.Setenv("XDG_CACHE_HOME", roCacheDir)
	newHashEntry := &format.VariantEntry{Level: "v1", Offset: 0, CompressedSize: 10, UncompressedSize: 50, SHA256: "brand_new_hash_456"}
	err = executeViaCache(rawFile, newHashEntry, []string{testAppArg}, []string{testPathEnv}, hostInfo, errors.New("memfd err"))
	if err == nil {
		t.Errorf("expected error when cache dir is not writable")
	}

	// 9. Error in executeViaMemfd when variant extraction fails
	if err := executeViaMemfd(rawFile, badEntryCache, []string{testAppArg}, []string{testPathEnv}, hostInfo); err == nil {
		t.Errorf("expected error in executeViaMemfd when extractVariantToWriter fails")
	}

	// 10. Error in executeViaMemfd when execve fails
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		return errors.New("memfd execve failed")
	}
	if err := executeViaMemfd(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo); err == nil {
		t.Errorf("expected error in executeViaMemfd when execve returns error")
	}
}

func TestOptimizeInPlaceSymlink(t *testing.T) {
	tempDir := t.TempDir()
	payloadData := []byte("OPTIMIZE_SYMLINK_TEST")
	entry, rawFile := createDummyVariantFile(t, tempDir, payloadData)
	defer func() { _ = rawFile.Close() }()

	realTarget := filepath.Join(tempDir, "real_opt_file")
	_ = os.WriteFile(realTarget, []byte("original"), 0o755)

	symTarget := filepath.Join(tempDir, "sym_opt_file")
	if err := os.Symlink(realTarget, symTarget); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	if err := optimizeInPlace(symTarget, rawFile, entry); err != nil {
		t.Fatalf("optimizeInPlace on symlink failed: %v", err)
	}

	content, _ := os.ReadFile(realTarget)
	if !bytes.Equal(content, payloadData) {
		t.Errorf("optimizeInPlace on symlink did not update target: got %q", string(content))
	}

	// Test extractVariantToWriter with wrong UncompressedSize
	entrySizeMismatch := &format.VariantEntry{
		Level:            entry.Level,
		Offset:           entry.Offset,
		CompressedSize:   entry.CompressedSize,
		UncompressedSize: entry.UncompressedSize + 100, // wrong size
	}
	var dummyBuf bytes.Buffer
	if err := extractVariantToWriter(rawFile, entrySizeMismatch, &dummyBuf); err == nil {
		t.Errorf("expected error on uncompressed size mismatch")
	}

	// Test optimizeTo and trimTo error on invalid destination directory
	if err := optimizeTo("/dev/null/forbidden/bin", rawFile, entry); err == nil {
		t.Errorf("expected optimizeTo to fail on invalid destination directory")
	}
	if err := trimTo("/dev/null/forbidden/bin", rawFile, 1000, "v1"); err == nil {
		t.Errorf("expected trimTo to fail on invalid destination directory")
	}

	// Test optimizeInPlace and trimInPlace in read-only directory
	roDir := filepath.Join(tempDir, "readonly_dir")
	_ = os.MkdirAll(roDir, 0o755)
	roFile := filepath.Join(roDir, "ro_app")
	_ = os.WriteFile(roFile, []byte("data"), 0o755)
	_ = os.Chmod(roDir, 0o555)
	defer func() { _ = os.Chmod(roDir, 0o755) }()

	if err := optimizeInPlace(roFile, rawFile, entry); err == nil {
		t.Errorf("expected optimizeInPlace to fail in read-only directory")
	}
	if err := trimInPlace(roFile, rawFile, 1000, "v1"); err == nil {
		t.Errorf("expected trimInPlace to fail in read-only directory")
	}

	// Test optimizeInPlace and trimInPlace when selfPath is a directory (rename error)
	dirTarget := filepath.Join(tempDir, "existing_target_dir")
	_ = os.MkdirAll(dirTarget, 0o755)
	if err := optimizeInPlace(dirTarget, rawFile, entry); err == nil {
		t.Errorf("expected optimizeInPlace to fail when selfPath is directory")
	}
	if err := trimInPlace(dirTarget, rawFile, 1000, "v1"); err == nil {
		t.Errorf("expected trimInPlace to fail when selfPath is directory")
	}

	// Test optimizeTo with invalid entry
	badEntry := &format.VariantEntry{Level: "v1", Offset: 0, CompressedSize: 10, UncompressedSize: 50}
	if err := optimizeTo(filepath.Join(tempDir, "bad_opt"), rawFile, badEntry); err == nil {
		t.Errorf("expected optimizeTo with bad entry to fail")
	}

	// Test optimizeTo when destination is an existing directory
	if err := optimizeTo(tempDir, rawFile, entry); err == nil {
		t.Errorf("expected optimizeTo to fail when target is directory")
	}

	// Test trimTo with invalid variant level
	if err := trimTo(filepath.Join(tempDir, "bad_trim"), rawFile, 1000, "nonexistent"); err == nil {
		t.Errorf("expected trimTo with nonexistent variant to fail")
	}

	// Test trimTo when destination is an existing directory
	if err := trimTo(tempDir, rawFile, 1000, "v1"); err == nil {
		t.Errorf("expected trimTo to fail when target is directory")
	}
}

func TestRunBinarySpecializeAndRun(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("stub-bytes"), 0o755)

	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("v1-bytes"), 0o755)

	fatPath := filepath.Join(tempDir, "fat_specialize_app")
	_, err := pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "specializeapp",
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Path},
	})
	if err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// 1. --microfat:specialize
	copyFat := filepath.Join(tempDir, "fat_copy_specialize")
	data, _ := os.ReadFile(fatPath)
	_ = os.WriteFile(copyFat, data, 0o755)
	os.Args = []string{copyFat, "--microfat:specialize"}
	if err := runBinary(copyFat); err != nil {
		t.Fatalf("runBinary --microfat:specialize failed: %v", err)
	}

	// 2. --microfat:specialize-to
	destSpec := filepath.Join(tempDir, "specialize_to_out")
	os.Args = []string{fatPath, "--microfat:specialize-to=" + destSpec}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary --microfat:specialize-to failed: %v", err)
	}

	// 3. run() when current test binary is not fat binary
	_ = run()

	// 3b. run() when resolving executable path fails
	oldGetSelf := getSelfExecutablePathFunc
	defer func() { getSelfExecutablePathFunc = oldGetSelf }()
	getSelfExecutablePathFunc = func() (string, error) {
		return "", errors.New("cannot determine executable path")
	}
	if err := run(); err == nil {
		t.Errorf("expected error when getSelfExecutablePathFunc fails")
	}

	// 4. main() invocation
	oldExit := exitFunc
	defer func() { exitFunc = oldExit }()
	exitCalled := false
	exitFunc = func(code int) {
		exitCalled = true
	}
	main()
	if !exitCalled {
		t.Errorf("expected exitFunc to be called from main() on non-fat binary")
	}
}

func createDummyVariantFile(t *testing.T, dir string, content []byte) (*format.VariantEntry, *os.File) {
	t.Helper()
	filePath := filepath.Join(dir, "dummy_container")
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0o755)
	if err != nil {
		t.Fatalf("creating dummy file: %v", err)
	}

	// Compress content with zstd
	var zstdBuf bytes.Buffer
	enc, err := zstd.NewWriter(&zstdBuf)
	if err != nil {
		t.Fatalf("creating zstd encoder: %v", err)
	}
	if _, err := enc.Write(content); err != nil {
		t.Fatalf("writing zstd: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("closing zstd: %v", err)
	}

	offset := int64(100)
	if _, err := f.WriteAt(zstdBuf.Bytes(), offset); err != nil {
		t.Fatalf("writing at offset: %v", err)
	}

	entry := &format.VariantEntry{
		Level:            "v1",
		Offset:           offset,
		CompressedSize:   int64(zstdBuf.Len()),
		UncompressedSize: int64(len(content)),
	}

	return entry, f
}
