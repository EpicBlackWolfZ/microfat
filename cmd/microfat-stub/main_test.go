//go:build !minimal

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/klauspost/compress/zstd"
)

const (
	testArchAMD64        = "amd64"
	testOSLinux          = "linux"
	testPolicyForceLevel = "force_level"
	testPathEnv          = "PATH=/bin"
	testAppArg           = "app"
)

func TestPrintHelpAndInfo(t *testing.T) {
	idx := &format.Index{
		Version:     1,
		AppName:     "testapp",
		TargetOS:    testOSLinux,
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
		OS:       testOSLinux,
		Arch:     testArchAMD64,
		Level:    "v3",
		Features: []string{"avx", "avx2"},
	}

	testForcedRes := microarch.PolicyResult{
		PolicyApplied:  testPolicyForceLevel,
		OverrideReason: "MICROFAT_FORCE_LEVEL=v1",
	}

	// Should execute without panic (text and JSON mode)
	printHelp(idx, hostInfo, &idx.Variants[0], testForcedRes)
	_ = printInfo(idx, hostInfo, &idx.Variants[0], microarch.PolicyResult{}, 2000, false)
	_ = printInfo(idx, hostInfo, &idx.Variants[0], testForcedRes, 2000, true)
	_ = printInfo(idx, hostInfo, &idx.Variants[0], testForcedRes, 2000, false)

	if !hasJSONFlag([]string{"--json"}) {
		t.Errorf("expected hasJSONFlag(--json) to be true")
	}
	if !hasJSONFlag([]string{"-json"}) {
		t.Errorf("expected hasJSONFlag(-json) to be true")
	}
	if hasJSONFlag([]string{"--other"}) {
		t.Errorf("expected hasJSONFlag(--other) to be false")
	}
}

func TestBuildAutoTunedEnviron(t *testing.T) {
	base := []string{"PATH=/usr/bin", "USER=test"}
	entry := &format.VariantEntry{
		Level:            "v3",
		SHA256:           "abcdef123456",
		UncompressedSize: 1000,
	}
	hostInfo := microarch.Info{
		OS:       testOSLinux,
		Arch:     testArchAMD64,
		Level:    "v3",
		Features: []string{"avx", "avx2"},
	}
	testPolicyRes := microarch.PolicyResult{
		SelectedVariant: "v3",
		PolicyApplied:   testPolicyForceLevel,
		OverrideReason:  "MICROFAT_FORCE_LEVEL=v3",
	}

	// 1. Standard auto-tune with metadata injection
	env, _ := buildAutoTunedEnviron(base, entry, format.ExecModeMemfd, hostInfo, testPolicyRes)
	hasVariant := false
	hasHostArch := false
	hasHostLevel := false
	hasExecMode := false
	hasDispatchMode := false
	hasSHA := false
	hasSize := false
	hasPolicyApplied := false
	hasOverrideReason := false
	for _, e := range env {
		if e == "MICROFAT_SELECTED_VARIANT=v3" {
			hasVariant = true
		}
		if e == "MICROFAT_HOST_ARCH=amd64" {
			hasHostArch = true
		}
		if e == "MICROFAT_HOST_LEVEL=v3" {
			hasHostLevel = true
		}
		if e == "MICROFAT_EXEC_MODE=memfd" {
			hasExecMode = true
		}
		if e == "MICROFAT_DISPATCH_MODE=memfd" {
			hasDispatchMode = true
		}
		if e == "MICROFAT_SELECTED_SHA256=abcdef123456" {
			hasSHA = true
		}
		if e == "MICROFAT_SELECTED_SIZE=1000" {
			hasSize = true
		}
		if e == "MICROFAT_POLICY_APPLIED=force_level" {
			hasPolicyApplied = true
		}
		if e == "MICROFAT_OVERRIDE_REASON=MICROFAT_FORCE_LEVEL=v3" {
			hasOverrideReason = true
		}
	}
	hasAllFlags := hasVariant && hasHostArch && hasHostLevel && hasExecMode &&
		hasDispatchMode && hasSHA && hasSize && hasPolicyApplied && hasOverrideReason
	if !hasAllFlags {
		t.Errorf("expected telemetry and policy env vars in env: %v", env)
	}

	// 2. Opt-out via MICROFAT_AUTOTUNE=0
	t.Setenv("MICROFAT_AUTOTUNE", "0")
	envOptOut, _ := buildAutoTunedEnviron(base, entry, format.ExecModeCache, hostInfo, microarch.PolicyResult{})
	if len(envOptOut) < len(base)+7 { // base + 7 injected metadata vars
		t.Errorf("expected opt-out env to have at least len %d, got %d", len(base)+7, len(envOptOut))
	}

	// 3. Opt-out via MICROFAT_AUTOTUNE=false
	t.Setenv("MICROFAT_AUTOTUNE", "false")
	envFalse, _ := buildAutoTunedEnviron(base, entry, format.ExecModeCache, hostInfo, microarch.PolicyResult{})
	if len(envFalse) != len(envOptOut) {
		t.Errorf("expected opt-out false to match length")
	}

	// 4. Preserve existing GOMEMLIMIT and GOMAXPROCS
	t.Setenv("MICROFAT_AUTOTUNE", "1")
	existing := []string{"GOMEMLIMIT=1GiB", "GOMAXPROCS=8"}
	envPreserve, _ := buildAutoTunedEnviron(existing, entry, format.ExecModeMemfd, hostInfo, microarch.PolicyResult{})
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
	_, _ = buildAutoTunedEnviron(base, entry, format.ExecModeMemfd, hostInfo, microarch.PolicyResult{})

	t.Setenv("MICROFAT_MEM_RATIO", "invalid")
	_, _ = buildAutoTunedEnviron(base, entry, format.ExecModeMemfd, hostInfo, microarch.PolicyResult{})

	t.Setenv("MICROFAT_MEM_RATIO", "1.5")
	_, _ = buildAutoTunedEnviron(base, entry, format.ExecModeMemfd, hostInfo, microarch.PolicyResult{})

	t.Setenv("MICROFAT_MEM_RATIO", "-0.2")
	_, _ = buildAutoTunedEnviron(base, entry, format.ExecModeMemfd, hostInfo, microarch.PolicyResult{})

	// 6. Test with mocked active cgroup limits
	oldReadCgroup := readCgroupLimitsFunc
	defer func() { readCgroupLimitsFunc = oldReadCgroup }()

	// cgroup read error
	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{}, errors.New("cgroup error")
	}
	_, _ = buildAutoTunedEnviron([]string{testPathEnv}, entry, format.ExecModeMemfd, hostInfo, microarch.PolicyResult{})

	// cgroup unknown version
	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{CgroupVersion: cgroup.VersionUnknown}, nil
	}
	_, _ = buildAutoTunedEnviron([]string{testPathEnv}, entry, format.ExecModeMemfd, hostInfo, microarch.PolicyResult{})

	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{
			CgroupVersion:    cgroup.VersionV2,
			MemoryLimitBytes: 1024 * 1024 * 1024,
			CPUQuota:         4.0,
			CPUs:             4,
		}, nil
	}

	envCgroup, limits := buildAutoTunedEnviron([]string{testPathEnv}, entry, format.ExecModeMemfd, hostInfo, microarch.PolicyResult{})
	if limits == nil || limits.CgroupVersion != cgroup.VersionV2 {
		t.Fatalf("expected active cgroup limits, got %+v", limits)
	}
	var foundAutoMem, foundAutoCPU, foundCgroupVer, foundCgroupMem, foundCgroupCPU bool
	for _, e := range envCgroup {
		if strings.HasPrefix(e, "GOMEMLIMIT=") {
			foundAutoMem = true
		}
		if strings.HasPrefix(e, "GOMAXPROCS=4") {
			foundAutoCPU = true
		}
		if e == "MICROFAT_CGROUP_VERSION=2" {
			foundCgroupVer = true
		}
		if e == "MICROFAT_CGROUP_LIMIT_BYTES=1073741824" {
			foundCgroupMem = true
		}
		if e == "MICROFAT_CGROUP_CPUS=4.00" {
			foundCgroupCPU = true
		}
	}
	if !foundAutoMem || !foundAutoCPU || !foundCgroupVer || !foundCgroupMem || !foundCgroupCPU {
		t.Errorf("expected auto-tuned GOMEMLIMIT, GOMAXPROCS, and cgroup telemetry in env: %v", envCgroup)
	}

	// 7. Test printInfo with mocked limits
	idx := &format.Index{
		AppName:    "testapp",
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: []format.VariantEntry{
			{Level: "v3", Offset: 100, CompressedSize: 200, UncompressedSize: 300, SHA256: "hash"},
		},
	}
	_ = printInfo(idx, hostInfo, &idx.Variants[0], microarch.PolicyResult{}, 1000, false)
	_ = printInfo(idx, hostInfo, &idx.Variants[0], microarch.PolicyResult{}, 1000, true)

	// Test printInfo with unlimited limits
	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{
			CgroupVersion:    cgroup.VersionV1,
			MemoryLimitBytes: 0,
			CPUQuota:         0,
			CPUs:             0,
		}, nil
	}
	_ = printInfo(idx, hostInfo, &idx.Variants[0], microarch.PolicyResult{}, 1000, false)
	_ = printInfo(idx, hostInfo, &idx.Variants[0], microarch.PolicyResult{}, 1000, true)
}

func TestLogDiagnostics(t *testing.T) {
	entry := &format.VariantEntry{Level: "v3", SHA256: "abc123hash", UncompressedSize: 2048}
	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	env := []string{"GOMEMLIMIT=1000B", "GOMAXPROCS=4"}
	limits := &cgroup.Limits{
		CgroupVersion:    2,
		MemoryLimitBytes: 1024 * 1024 * 1024,
		CPUQuota:         4.0,
		CPUs:             4,
	}

	policyRes := microarch.PolicyResult{PolicyApplied: testPolicyForceLevel, OverrideReason: "MICROFAT_FORCE_LEVEL=v3"}

	// Text debug output
	t.Setenv("MICROFAT_DEBUG", "1")
	t.Setenv("MICROFAT_LOG", "")
	logDiagnostics(entry, format.ExecModeMemfd, hostInfo, policyRes, env, limits, 100, 500)

	// JSON log output with limits
	t.Setenv("MICROFAT_DEBUG", "0")
	t.Setenv("MICROFAT_LOG", "json")
	logDiagnostics(entry, format.ExecModeCache, hostInfo, policyRes, env, limits, 200, 600)

	// JSON log output without limits
	logDiagnostics(entry, format.ExecModeMemfd, hostInfo, policyRes, env, nil, 200, 600)

	// Debug false without log
	t.Setenv("MICROFAT_DEBUG", "0")
	t.Setenv("MICROFAT_LOG", "")
	logDiagnostics(entry, format.ExecModeMemfd, hostInfo, policyRes, env, limits, 100, 500)

	// Test error diagnostics
	t.Setenv("MICROFAT_LOG", "json")
	logErrorDiagnostics("memfd_create", errors.New("memfd error"), hostInfo, entry, policyRes, "details test")
	logErrorDiagnostics("test_stage", errors.New("no entry error"), hostInfo, nil, policyRes, "details test")

	t.Setenv("MICROFAT_LOG", "")
	logErrorDiagnostics("test_stage", errors.New("silent error"), hostInfo, entry, policyRes, "should not log")
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

	// 4b. --microfat:optimize-to without path
	os.Args = []string{fatPath, "--microfat:optimize-to"}
	if err := runBinary(fatPath); err == nil {
		t.Errorf("expected error when --microfat:optimize-to has no target path")
	}

	// 4c. --microfat:optimize-to with separate arg
	destOptSep := filepath.Join(tempDir, "optimized_via_run_sep")
	os.Args = []string{fatPath, "--microfat:optimize-to", destOptSep}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary --microfat:optimize-to separate arg failed: %v", err)
	}

	// 4d. --microfat:optimize-to with forbidden path
	os.Args = []string{fatPath, "--microfat:optimize-to=/dev/null/forbidden/opt"}
	if err := runBinary(fatPath); err == nil {
		t.Errorf("expected error on --microfat:optimize-to with forbidden path")
	}

	// 4e. --microfat:trim-to with forbidden path
	os.Args = []string{fatPath, "--microfat:trim-to=/dev/null/forbidden/trim"}
	if err := runBinary(fatPath); err == nil {
		t.Errorf("expected error on --microfat:trim-to with forbidden path")
	}

	// 5. --microfat:trim in-place on a copy
	copyFat := filepath.Join(tempDir, "fat_for_trim")
	data, _ := os.ReadFile(fatPath)
	_ = os.WriteFile(copyFat, data, 0o755)
	os.Args = []string{copyFat, "--microfat:trim"}
	if err := runBinary(copyFat); err != nil {
		t.Fatalf("runBinary --microfat:trim failed: %v", err)
	}

	// 5b. --microfat:trim in read-only directory
	roDir := filepath.Join(tempDir, "ro_run_dir")
	_ = os.MkdirAll(roDir, 0o755)
	roFat := filepath.Join(roDir, "ro_fat")
	_ = os.WriteFile(roFat, data, 0o755)
	_ = os.Chmod(roDir, 0o555)
	defer func() { _ = os.Chmod(roDir, 0o755) }()
	os.Args = []string{roFat, "--microfat:trim"}
	if err := runBinary(roFat); err == nil {
		t.Errorf("expected error on --microfat:trim in read-only directory")
	}

	// 6. --microfat:optimize in-place on a copy
	copyFat2 := filepath.Join(tempDir, "fat_for_opt")
	_ = os.WriteFile(copyFat2, data, 0o755)
	os.Args = []string{copyFat2, "--microfat:optimize"}
	if err := runBinary(copyFat2); err != nil {
		t.Fatalf("runBinary --microfat:optimize failed: %v", err)
	}

	// 6b. --microfat:optimize in read-only directory
	os.Args = []string{roFat, "--microfat:optimize"}
	if err := runBinary(roFat); err == nil {
		t.Errorf("expected error on --microfat:optimize in read-only directory")
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
	policyRes := microarch.PolicyResult{}
	if err := executeVariant(
		rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, time.Now(),
	); err != nil {
		t.Errorf("expected success for executeVariant, got %v", err)
	}

	// 2. Fallback to cache when memfd execution returns error
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		return errors.New("simulated execve error")
	}
	err := executeVariant(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, time.Now())
	if err == nil || !strings.Contains(err.Error(), "cache fallback execve failed") {
		t.Errorf("expected cache fallback error, got %v", err)
	}

	// 3. Fallback to cache when cached binary already exists
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		return nil
	}
	if err := executeViaCache(
		rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, errors.New("memfd err"), time.Now(),
	); err != nil {
		t.Errorf("expected success for existing cached binary, got %v", err)
	}

	// 4. Fallback to cache with fresh cache directory extraction
	freshCacheDir := filepath.Join(tempDir, "fresh_cache_dir")
	t.Setenv("XDG_CACHE_HOME", freshCacheDir)
	if err := executeViaCache(
		rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, errors.New("memfd err"), time.Now(),
	); err != nil {
		t.Errorf("expected success for fresh cache directory extraction, got %v", err)
	}

	// 5. Fallback when XDG_CACHE_HOME is empty (uses user home dir)
	t.Setenv("XDG_CACHE_HOME", "")
	if err := executeViaCache(
		rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, errors.New("memfd err"), time.Now(),
	); err != nil {
		t.Errorf("expected success when XDG_CACHE_HOME is empty, got %v", err)
	}

	// 5b. Fallback when XDG_CACHE_HOME is empty and userHomeDirFunc fails (uses os.TempDir())
	oldHome := userHomeDirFunc
	defer func() { userHomeDirFunc = oldHome }()
	userHomeDirFunc = func() (string, error) {
		return "", errors.New("no home dir")
	}
	if err := executeViaCache(
		rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, errors.New("memfd err"), time.Now(),
	); err != nil {
		t.Errorf("expected success when userHomeDir fails, got %v", err)
	}
	userHomeDirFunc = oldHome

	// 6. Fallback when primary cacheDir cannot be created (forbidden path)
	t.Setenv("XDG_CACHE_HOME", "/dev/null/forbidden_path")
	if err := executeViaCache(
		rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, errors.New("memfd err"), time.Now(),
	); err != nil {
		t.Errorf("expected success falling back to /tmp/.microfat-uid, got %v", err)
	}

	// 6b. Fallback failure when both primary and secondary cacheDir cannot be created
	t.Setenv("XDG_CACHE_HOME", "/dev/null/forbidden_primary")
	t.Setenv("TMPDIR", "/dev/null/forbidden_secondary")
	if err := executeViaCache(
		rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, errors.New("memfd err"), time.Now(),
	); err == nil {
		t.Errorf("expected error when both cache directories cannot be created")
	}
	t.Setenv("TMPDIR", "")

	// 7. Error extracting variant to cache fallback
	badEntryCache := &format.VariantEntry{
		Level: "v1", Offset: 0, CompressedSize: 10, UncompressedSize: 50, SHA256: "unique_bad_hash_123",
	}
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tempDir, "bad_cache_extract"))
	err = executeViaCache(
		rawFile, badEntryCache, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, errors.New("memfd err"), time.Now(),
	)
	if err == nil {
		t.Errorf("expected error extracting invalid entry in executeViaCache")
	}

	// 8. Error creating temp file in read-only cache dir
	roCacheDir := filepath.Join(tempDir, "ro_cache_dir")
	_ = os.MkdirAll(filepath.Join(roCacheDir, "microfat"), 0o755)
	_ = os.Chmod(filepath.Join(roCacheDir, "microfat"), 0o555)
	defer func() { _ = os.Chmod(filepath.Join(roCacheDir, "microfat"), 0o755) }()
	t.Setenv("XDG_CACHE_HOME", roCacheDir)
	newHashEntry := &format.VariantEntry{
		Level: "v1", Offset: 0, CompressedSize: 10, UncompressedSize: 50, SHA256: "brand_new_hash_456",
	}
	err = executeViaCache(
		rawFile, newHashEntry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, errors.New("memfd err"), time.Now(),
	)
	if err == nil {
		t.Errorf("expected error when cache dir is not writable")
	}

	// 9. Error in executeViaMemfd when variant extraction fails
	if err := executeViaMemfd(
		rawFile, badEntryCache, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, time.Now(),
	); err == nil {
		t.Errorf("expected error in executeViaMemfd when extractVariantToWriter fails")
	}

	// 10. Error in executeViaMemfd when execve fails
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		return errors.New("memfd execve failed")
	}
	if err := executeViaMemfd(
		rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, time.Now(),
	); err == nil {
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

	// 4b. main() with MICROFAT_LOG=json
	t.Setenv("MICROFAT_LOG", "json")
	exitCalled = false
	main()
	if !exitCalled {
		t.Errorf("expected exitFunc to be called from main() with json log")
	}
	t.Setenv("MICROFAT_LOG", "")
}

func TestTrimAndOptimizeErrorPaths(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create a dummy file with corrupted payload bytes
	corruptContainer := filepath.Join(tempDir, "corrupt_container")
	corruptFile, err := os.OpenFile(corruptContainer, os.O_CREATE|os.O_RDWR, 0o755)
	if err != nil {
		t.Fatalf("creating corrupt file: %v", err)
	}
	defer func() { _ = corruptFile.Close() }()

	_, _ = corruptFile.WriteAt([]byte("THIS IS NOT VALID ZSTD DATA 1234567890"), 50)
	corruptEntry := &format.VariantEntry{
		Level:            "v1",
		Offset:           50,
		CompressedSize:   30,
		UncompressedSize: 100,
	}

	// Test extractVariantToWriter error on corrupted zstd stream
	var dummyBuf bytes.Buffer
	if err := extractVariantToWriter(corruptFile, corruptEntry, &dummyBuf); err == nil {
		t.Errorf("expected extractVariantToWriter to fail on corrupt zstd data")
	}

	// Test optimizeInPlace error when extractVariantToWriter fails
	optTarget := filepath.Join(tempDir, "opt_target")
	_ = os.WriteFile(optTarget, []byte("target"), 0o755)
	if err := optimizeInPlace(optTarget, corruptFile, corruptEntry); err == nil {
		t.Errorf("expected optimizeInPlace to fail on corrupt variant")
	}

	// Test optimizeTo error when extractVariantToWriter fails
	if err := optimizeTo(filepath.Join(tempDir, "opt_to_corrupt"), corruptFile, corruptEntry); err == nil {
		t.Errorf("expected optimizeTo to fail on corrupt variant")
	}

	// 2. Test trimInPlace error when pack.TrimBinary fails (invalid level)
	stubPath := filepath.Join(tempDir, "stub_err")
	_ = os.WriteFile(stubPath, []byte("stub-bytes"), 0o755)
	v1Path := filepath.Join(tempDir, "v1_err")
	_ = os.WriteFile(v1Path, []byte("v1-bytes"), 0o755)

	fatErrPath := filepath.Join(tempDir, "fat_err_app")
	_, err = pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        fatErrPath,
		AppName:           "errtest",
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Path},
	})
	if err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	fatErrFile, err := os.Open(fatErrPath)
	if err != nil {
		t.Fatalf("open fat file: %v", err)
	}
	defer func() { _ = fatErrFile.Close() }()

	stat, _ := fatErrFile.Stat()

	// trimTo with nonexistent variant
	if err := trimTo(filepath.Join(tempDir, "trimmed_err_out"), fatErrFile, stat.Size(), "nonexistent_variant_level"); err == nil {
		t.Errorf("expected trimTo to fail with nonexistent variant level")
	}

	// Test optimizeInPlace and trimInPlace when rename fails (target is non-empty dir)
	targetNonEmptyDir := filepath.Join(tempDir, "non_empty_dir")
	_ = os.MkdirAll(filepath.Join(targetNonEmptyDir, "nested"), 0o755)
	if err := optimizeInPlace(targetNonEmptyDir, corruptFile, corruptEntry); err == nil {
		t.Errorf("expected optimizeInPlace to fail when target is non-empty directory")
	}
	if err := trimInPlace(targetNonEmptyDir, fatErrFile, stat.Size(), "v1"); err == nil {
		t.Errorf("expected trimInPlace to fail when target is non-empty directory")
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

	hash := sha256.Sum256(content)
	entry := &format.VariantEntry{
		Level:            "v1",
		Offset:           offset,
		CompressedSize:   int64(zstdBuf.Len()),
		UncompressedSize: int64(len(content)),
		SHA256:           hex.EncodeToString(hash[:]),
	}

	return entry, f
}

func TestPolicyDispatchIntegration(t *testing.T) {
	tempDir := t.TempDir()
	fatPath := filepath.Join(tempDir, "policy_app_fat")

	stubPath := filepath.Join(tempDir, "stub_binary")
	if err := os.WriteFile(stubPath, []byte("\x7fELF_DUMMY_STUB"), 0o755); err != nil {
		t.Fatalf("writing stub: %v", err)
	}

	v1Path := filepath.Join(tempDir, "app_v1")
	v2Path := filepath.Join(tempDir, "app_v2")
	v3Path := filepath.Join(tempDir, "app_v3")
	if err := os.WriteFile(v1Path, []byte("\x7fELF_PAYLOAD_V1"), 0o755); err != nil {
		t.Fatalf("writing v1: %v", err)
	}
	if err := os.WriteFile(v2Path, []byte("\x7fELF_PAYLOAD_V2"), 0o755); err != nil {
		t.Fatalf("writing v2: %v", err)
	}
	if err := os.WriteFile(v3Path, []byte("\x7fELF_PAYLOAD_V3"), 0o755); err != nil {
		t.Fatalf("writing v3: %v", err)
	}

	opts := pack.Options{
		StubPath:   stubPath,
		OutputPath: fatPath,
		AppName:    "policyapp",
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: map[string]string{
			"v1": v1Path,
			"v2": v2Path,
			"v3": v3Path,
		},
		SkipELFValidation: true,
	}
	if _, err := pack.Pack(opts); err != nil {
		t.Fatalf("packaging fat binary: %v", err)
	}

	var lastExecVariant string
	var lastPolicyApplied string
	oldExec := execveFunc
	defer func() { execveFunc = oldExec }()
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		for _, e := range envv {
			if strings.HasPrefix(e, "MICROFAT_SELECTED_VARIANT=") {
				lastExecVariant = strings.TrimPrefix(e, "MICROFAT_SELECTED_VARIANT=")
			}
			if strings.HasPrefix(e, "MICROFAT_POLICY_APPLIED=") {
				lastPolicyApplied = strings.TrimPrefix(e, "MICROFAT_POLICY_APPLIED=")
			}
		}
		return nil
	}

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{fatPath}

	// 1. Force Level v1
	t.Setenv(format.EnvForceLevel, "v1")
	t.Setenv(format.EnvMaxLevel, "")
	t.Setenv(format.EnvDisableVariants, "")
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary force v1 failed: %v", err)
	}
	if lastExecVariant != "v1" || lastPolicyApplied != testPolicyForceLevel {
		t.Errorf("expected force v1, got variant=%s policy=%s", lastExecVariant, lastPolicyApplied)
	}

	// 2. Max Level v2
	t.Setenv(format.EnvForceLevel, "")
	t.Setenv(format.EnvMaxLevel, "v2")
	t.Setenv(format.EnvDisableVariants, "")
	lastPolicyApplied = ""
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary max v2 failed: %v", err)
	}
	if lastExecVariant != "v2" || lastPolicyApplied != "max_level" {
		t.Errorf("expected max v2, got variant=%s policy=%s", lastExecVariant, lastPolicyApplied)
	}

	// 3. Disable Variant v3
	t.Setenv(format.EnvForceLevel, "")
	t.Setenv(format.EnvMaxLevel, "")
	t.Setenv(format.EnvDisableVariants, "v3")
	lastPolicyApplied = ""
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary disable v3 failed: %v", err)
	}
	if lastExecVariant != "v2" || lastPolicyApplied != "disable_variants" {
		t.Errorf("expected disable v3 to select v2, got variant=%s policy=%s", lastExecVariant, lastPolicyApplied)
	}
}

func TestStubPrewarmAndCacheDispatch(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("#!/bin/sh\n"), 0o755)
	v1Path := filepath.Join(tempDir, "v1")
	v2Path := filepath.Join(tempDir, "v2")
	_ = os.WriteFile(v1Path, []byte("#!/bin/sh\necho v1\n"), 0o755)
	_ = os.WriteFile(v2Path, []byte("#!/bin/sh\necho v2\n"), 0o755)

	fatPath := filepath.Join(tempDir, "prewarm_stub.fat")
	_, err := pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "stubprewarmapp",
		TargetArch:        "amd64",
		SkipELFValidation: true,
		Variants: map[string]string{
			"v1": v1Path,
			"v2": v2Path,
		},
	})
	if err != nil {
		t.Fatalf("packing test binary: %v", err)
	}

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	cacheDir := filepath.Join(tempDir, "stub_cache")
	t.Setenv(format.EnvCacheDir, cacheDir)

	// 1. Default --microfat:prewarm
	os.Args = []string{fatPath, "--microfat:prewarm"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("--microfat:prewarm failed: %v", err)
	}

	// 2. Second time default --microfat:prewarm (already cached path)
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("second --microfat:prewarm failed: %v", err)
	}

	// 3. --microfat:prewarm=all
	os.Args = []string{fatPath, "--microfat:prewarm=all"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("--microfat:prewarm=all failed: %v", err)
	}

	// 4. --microfat:prewarm=v1
	os.Args = []string{fatPath, "--microfat:prewarm=v1"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("--microfat:prewarm=v1 failed: %v", err)
	}

	// 5. --microfat:prewarm=json
	os.Args = []string{fatPath, "--microfat:prewarm=json"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("--microfat:prewarm=json failed: %v", err)
	}

	// 6. --microfat:prewarm with --json flag and MICROFAT_LOG=json
	os.Args = []string{fatPath, "--microfat:prewarm", "--json"}
	t.Setenv(format.EnvLog, "json")
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("--microfat:prewarm with MICROFAT_LOG=json failed: %v", err)
	}
	t.Setenv(format.EnvLog, "")

	// 7. --microfat:prewarm with non-existent level
	os.Args = []string{fatPath, "--microfat:prewarm=v99"}
	if err := runBinary(fatPath); err == nil {
		t.Errorf("expected error for --microfat:prewarm=v99")
	}

	// 8. Cache resolution error during prewarm
	oldResolver := resolveCacheDirFunc
	defer func() { resolveCacheDirFunc = oldResolver }()
	resolveCacheDirFunc = func(string) (string, error) {
		return "", errors.New("cache init failed")
	}
	os.Args = []string{fatPath, "--microfat:prewarm"}
	if err := runBinary(fatPath); err == nil {
		t.Errorf("expected error when cache resolution fails")
	}
	resolveCacheDirFunc = oldResolver

	// 9. Runtime cache execution dispatch with MICROFAT_EXEC_MODE=cache
	var executedPath string
	oldExec := execveFunc
	defer func() { execveFunc = oldExec }()
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		executedPath = argv0
		return nil
	}

	os.Args = []string{fatPath, "arg1"}
	t.Setenv(format.EnvExecMode, format.ExecModeCache)
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary with MICROFAT_EXEC_MODE=cache failed: %v", err)
	}
	if !strings.Contains(executedPath, cacheDir) {
		t.Errorf("expected execve from cacheDir %s, got %s", cacheDir, executedPath)
	}

	// 10. Runtime cache execution dispatch with MICROFAT_DISPATCH_MODE=cache
	t.Setenv(format.EnvExecMode, "")
	t.Setenv(format.EnvDispatchMode, format.ExecModeCache)
	executedPath = ""
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("runBinary with MICROFAT_DISPATCH_MODE=cache failed: %v", err)
	}
	if !strings.Contains(executedPath, cacheDir) {
		t.Errorf("expected execve from cacheDir %s, got %s", cacheDir, executedPath)
	}
	t.Setenv(format.EnvDispatchMode, "")

	// 11. Runtime cache execution failing execve when primaryErr is nil
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		return errors.New("execve failed permission")
	}
	t.Setenv(format.EnvExecMode, format.ExecModeCache)
	if err := runBinary(fatPath); err == nil || !strings.Contains(err.Error(), "cache execve failed") {
		t.Errorf("expected 'cache execve failed' error, got %v", err)
	}
	t.Setenv(format.EnvExecMode, "")
}

func TestExecuteVariant_SyscallMocking(t *testing.T) {
	tempDir := t.TempDir()
	payloadData := []byte("PAYLOAD_SYSCALL_MOCK_TEST")
	entry, rawFile := createDummyVariantFile(t, tempDir, payloadData)
	defer func() { _ = rawFile.Close() }()

	cacheDir := filepath.Join(tempDir, "mock_cache")
	t.Setenv(format.EnvCacheDir, cacheDir)

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	oldMemfd := memfdCreateFunc
	oldExec := execveFunc
	defer func() {
		memfdCreateFunc = oldMemfd
		execveFunc = oldExec
	}()

	tests := []struct {
		name     string
		memfdErr error
	}{
		{"EPERM seccomp blocked", syscall.EPERM},
		{"ENOSYS kernel unsupported", syscall.ENOSYS},
		{"EMFILE descriptor exhaustion", syscall.EMFILE},
		{"EACCES permission denied", syscall.EACCES},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			memfdCreateFunc = func(name string, flags int) (int, error) {
				return -1, tc.memfdErr
			}

			var executedBinary string
			execveFunc = func(argv0 string, argv []string, envv []string) error {
				executedBinary = argv0
				return nil
			}

			err := executeVariant(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, time.Now())
			if err != nil {
				t.Fatalf("expected graceful fallback to cache, got error: %v", err)
			}

			if !strings.Contains(executedBinary, cacheDir) {
				t.Errorf("expected execve on cached binary in %s, got %s", cacheDir, executedBinary)
			}
		})
	}
}

func TestExecuteVariant_TruncatedCacheRecovery(t *testing.T) {
	tempDir := t.TempDir()
	payloadData := []byte("TRUNCATED_RECOVERY_PAYLOAD_CONTENT_12345")
	entry, rawFile := createDummyVariantFile(t, tempDir, payloadData)
	defer func() { _ = rawFile.Close() }()

	cacheDir := filepath.Join(tempDir, "recovery_cache")
	t.Setenv(format.EnvCacheDir, cacheDir)
	_ = os.MkdirAll(cacheDir, 0o700)

	cachedPath := filepath.Join(cacheDir, entry.SHA256)

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	oldExec := execveFunc
	defer func() { execveFunc = oldExec }()

	var executedPath string
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		executedPath = argv0
		return nil
	}

	// 1. Zero-byte truncated cache file
	_ = os.WriteFile(cachedPath, []byte{}, 0o755)

	t.Setenv(format.EnvDebug, "1")
	err := executeViaCache(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, nil, time.Now())
	if err != nil {
		t.Fatalf("executeViaCache failed on zero-byte cache file: %v", err)
	}
	if executedPath != cachedPath {
		t.Errorf("expected execution of %s, got %s", cachedPath, executedPath)
	}

	// Verify cached file was replaced with full content
	data, err := os.ReadFile(cachedPath)
	if err != nil || !bytes.Equal(data, payloadData) {
		t.Errorf("expected cached file to be restored to payload content, got %q (err: %v)", string(data), err)
	}

	// 2. Partial/corrupted size cache file (half length)
	_ = os.WriteFile(cachedPath, payloadData[:len(payloadData)/2], 0o755)
	executedPath = ""
	err = executeViaCache(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, nil, time.Now())
	if err != nil {
		t.Fatalf("executeViaCache failed on partial cache file: %v", err)
	}
	data2, err := os.ReadFile(cachedPath)
	if err != nil || !bytes.Equal(data2, payloadData) {
		t.Errorf("expected cached file to be restored after partial file corruption, got %q", string(data2))
	}
	t.Setenv(format.EnvDebug, "")
}

func TestPrewarmStub_VerifyMode(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub_vfy")
	_ = os.WriteFile(stubPath, []byte("#!/bin/sh\n"), 0o755)
	v1Path := filepath.Join(tempDir, "v1_vfy")
	v2Path := filepath.Join(tempDir, "v2_vfy")
	v1Data := []byte("#!/bin/sh\necho v1_vfy\n")
	v2Data := []byte("#!/bin/sh\necho v2_vfy\n")
	_ = os.WriteFile(v1Path, v1Data, 0o755)
	_ = os.WriteFile(v2Path, v2Data, 0o755)

	fatPath := filepath.Join(tempDir, "prewarm_verify.fat")
	idx, err := pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "verifyapp",
		TargetArch:        testArchAMD64,
		SkipELFValidation: true,
		Variants: map[string]string{
			"v1": v1Path,
			"v2": v2Path,
		},
	})
	if err != nil {
		t.Fatalf("packing test binary: %v", err)
	}

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	cacheDir := filepath.Join(tempDir, "verify_cache")
	t.Setenv(format.EnvCacheDir, cacheDir)

	// 1. Verify clean cache (should fail with missing status)
	os.Args = []string{fatPath, "--microfat:prewarm=verify"}
	if err := runBinary(fatPath); err == nil {
		t.Errorf("expected error running --microfat:prewarm=verify on clean cache")
	}

	// 2. Prewarm v1 and verify v1 (should succeed)
	os.Args = []string{fatPath, "--microfat:prewarm=v1"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("prewarming v1 failed: %v", err)
	}

	os.Args = []string{fatPath, "--microfat:prewarm=verify,v1"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("--microfat:prewarm=verify,v1 failed on valid cache: %v", err)
	}

	// 3. Verify all when only v1 is cached (should fail because v2 is missing)
	os.Args = []string{fatPath, "--microfat:prewarm=verify,all"}
	if err := runBinary(fatPath); err == nil {
		t.Errorf("expected error verifying all when v2 is missing")
	}

	// 4. Prewarm all and verify all (should succeed)
	os.Args = []string{fatPath, "--microfat:prewarm=all"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("prewarming all failed: %v", err)
	}

	os.Args = []string{fatPath, "--microfat:prewarm=verify,all"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("--microfat:prewarm=verify,all failed on complete cache: %v", err)
	}

	// 5. Verify mode with --json flag
	os.Args = []string{fatPath, "--microfat:prewarm=verify,all", "--json"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("--microfat:prewarm=verify,all with --json failed: %v", err)
	}

	// 6. Verify mode with standalone --verify in args
	os.Args = []string{fatPath, "--microfat:prewarm", "--verify"}
	if err := runBinary(fatPath); err != nil {
		t.Fatalf("--microfat:prewarm with --verify failed: %v", err)
	}

	// 7. Verify mode on corrupted cache entry (truncate v1)
	v1Entry, _ := idx.FindVariant("v1")
	v1Cached := filepath.Join(cacheDir, v1Entry.SHA256)
	_ = os.WriteFile(v1Cached, []byte("broken"), 0o755)

	os.Args = []string{fatPath, "--microfat:prewarm=verify,v1"}
	if err := runBinary(fatPath); err == nil {
		t.Errorf("expected error verifying corrupted cache entry")
	}

	// 8. Verify mode on corrupted cache entry with JSON output
	os.Args = []string{fatPath, "--microfat:prewarm=verify,v1", "--json"}
	if err := runBinary(fatPath); err == nil {
		t.Errorf("expected error verifying corrupted cache entry with JSON output")
	}
}

func TestExecuteVariant_StrictEnvironmentMatrix(t *testing.T) {
	tempDir := t.TempDir()
	payloadData := []byte("STRICT_ENV_MATRIX_PAYLOAD")
	entry, rawFile := createDummyVariantFile(t, tempDir, payloadData)
	defer func() { _ = rawFile.Close() }()

	cacheDir := filepath.Join(tempDir, "matrix_cache")
	t.Setenv(format.EnvCacheDir, cacheDir)

	hostInfo := microarch.Info{
		OS:       testOSLinux,
		Arch:     testArchAMD64,
		Level:    "v3",
		Features: []string{"avx", "avx2", "bmi1", "bmi2", "fma"},
	}

	oldExec := execveFunc
	defer func() { execveFunc = oldExec }()

	var capturedEnv []string
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		capturedEnv = envv
		return nil
	}

	t.Run("memfd execution strict env assertions", func(t *testing.T) {
		policyRes := microarch.PolicyResult{
			SelectedVariant: "v3",
			PolicyApplied:   testPolicyForceLevel,
			OverrideReason:  "MICROFAT_FORCE_LEVEL=v3",
		}
		base := []string{"USER=deployer", "LANG=en_US.UTF-8"}

		err := executeVariant(rawFile, entry, []string{testAppArg}, base, hostInfo, policyRes, time.Now())
		if err != nil {
			t.Fatalf("executeVariant failed: %v", err)
		}

		envMap := make(map[string]string)
		for _, e := range capturedEnv {
			parts := strings.SplitN(e, "=", 2)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}

		expected := map[string]string{
			"USER":                    "deployer",
			"LANG":                    "en_US.UTF-8",
			format.EnvSelectedVariant: "v1",
			format.EnvHostArch:        "amd64",
			format.EnvHostLevel:       "v3",
			format.EnvExecMode:        format.ExecModeMemfd,
			format.EnvDispatchMode:    format.ExecModeMemfd,
			format.EnvSelectedSHA256:  entry.SHA256,
			format.EnvSelectedSize:    fmt.Sprintf("%d", entry.UncompressedSize),
			format.EnvPolicyApplied:   testPolicyForceLevel,
			format.EnvOverrideReason:  "MICROFAT_FORCE_LEVEL=v3",
		}

		for k, expVal := range expected {
			if val, ok := envMap[k]; !ok || val != expVal {
				t.Errorf("env key %s: expected %q, got %q (exists: %v)", k, expVal, val, ok)
			}
		}
	})
}

func TestExecuteVariant_TelemetryJSONValidation(t *testing.T) {
	tempDir := t.TempDir()
	payloadData := []byte("TELEMETRY_JSON_VALIDATION_PAYLOAD")
	entry, rawFile := createDummyVariantFile(t, tempDir, payloadData)
	defer func() { _ = rawFile.Close() }()

	cacheDir := filepath.Join(tempDir, "telem_cache")
	t.Setenv(format.EnvCacheDir, cacheDir)

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{PolicyApplied: "test_policy", OverrideReason: "test"}

	oldExec := execveFunc
	defer func() { execveFunc = oldExec }()
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		return nil
	}

	// 1. DispatchTelemetry validation (JSON)
	t.Setenv(format.EnvLog, "json")
	var capturedOutput bytes.Buffer

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	logDiagnostics(
		entry, format.ExecModeMemfd, hostInfo, policyRes, []string{"GOMEMLIMIT=1000B"}, nil,
		150*time.Microsecond, 500*time.Microsecond,
	)
	_ = w.Close()
	os.Stderr = oldStderr

	_, _ = io.Copy(&capturedOutput, r)
	outStr := strings.TrimSpace(capturedOutput.String())

	jsonPart := strings.TrimPrefix(outStr, "[microfat] ")
	var dt format.DispatchTelemetry
	if err := json.Unmarshal([]byte(jsonPart), &dt); err != nil {
		t.Fatalf("unmarshaling DispatchTelemetry JSON failed: %v (raw: %s)", err, outStr)
	}

	if dt.Event != format.EventDispatch || dt.HostArch != "amd64" || dt.SelectedVariant != entry.Level {
		t.Errorf("DispatchTelemetry validation mismatch: %+v", dt)
	}
	if dt.TimestampUnixNano <= 0 || dt.DecompressionDurationUs <= 0 || dt.TotalLauncherUs <= 0 {
		t.Errorf("DispatchTelemetry invalid timestamps/durations: %+v", dt)
	}

	// 2. ErrorTelemetry validation (JSON)
	capturedOutput.Reset()
	r2, w2, _ := os.Pipe()
	os.Stderr = w2

	logErrorDiagnostics(format.StageMemfdCreate, syscall.EPERM, hostInfo, entry, policyRes, "mock details")
	_ = w2.Close()
	os.Stderr = oldStderr

	_, _ = io.Copy(&capturedOutput, r2)
	errStr := strings.TrimSpace(capturedOutput.String())
	errJSON := strings.TrimPrefix(errStr, "[microfat] ")

	var et format.ErrorTelemetry
	if err := json.Unmarshal([]byte(errJSON), &et); err != nil {
		t.Fatalf("unmarshaling ErrorTelemetry JSON failed: %v (raw: %s)", err, errStr)
	}

	if et.Event != format.EventError || et.Stage != format.StageMemfdCreate ||
		et.Error != syscall.EPERM.Error() || et.Details != "mock details" || et.Hint != format.HintMemfdSeccomp {
		t.Errorf("ErrorTelemetry validation mismatch: %+v", et)
	}

	t.Setenv(format.EnvLog, "")
}

func TestExecuteVariant_DiagnosticHints(t *testing.T) {
	tempDir := t.TempDir()
	payloadData := []byte("DIAGNOSTIC_HINTS_PAYLOAD")
	entry, rawFile := createDummyVariantFile(t, tempDir, payloadData)
	defer func() { _ = rawFile.Close() }()

	cacheDir := filepath.Join(tempDir, "hint_cache")
	t.Setenv(format.EnvCacheDir, cacheDir)

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	oldMemfd := memfdCreateFunc
	oldExec := execveFunc
	defer func() {
		memfdCreateFunc = oldMemfd
		execveFunc = oldExec
	}()

	// 1. In default mode (no MICROFAT_DEBUG), non-fatal memfd fallback should NOT emit hints to stderr
	t.Run("suppressed in default mode", func(t *testing.T) {
		t.Setenv(format.EnvDebug, "")
		t.Setenv(format.EnvLog, "")

		memfdCreateFunc = func(name string, flags int) (int, error) {
			return -1, syscall.EPERM
		}
		execveFunc = func(argv0 string, argv []string, envv []string) error {
			return nil
		}

		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		err := executeVariant(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, time.Now())
		_ = w.Close()
		os.Stderr = oldStderr

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)

		if err != nil {
			t.Fatalf("expected fallback to succeed, got: %v", err)
		}
		if strings.Contains(buf.String(), "[microfat:hint]") {
			t.Errorf("expected no hint in default mode, got: %s", buf.String())
		}
	})

	// 2. In debug mode (MICROFAT_DEBUG=1), non-fatal memfd fallback should emit hints to stderr
	t.Run("emitted in debug mode", func(t *testing.T) {
		t.Setenv(format.EnvDebug, "1")
		t.Setenv(format.EnvLog, "")

		memfdCreateFunc = func(name string, flags int) (int, error) {
			return -1, syscall.EPERM
		}
		execveFunc = func(argv0 string, argv []string, envv []string) error {
			return nil
		}

		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		err := executeVariant(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, time.Now())
		_ = w.Close()
		os.Stderr = oldStderr

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)

		if err != nil {
			t.Fatalf("expected fallback to succeed, got: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "[microfat:hint]") || !strings.Contains(out, "seccomp") {
			t.Errorf("expected seccomp hint in debug output, got: %s", out)
		}
	})

	// 3. ENOSYS in debug mode
	t.Run("enosys kernel hint in debug mode", func(t *testing.T) {
		t.Setenv(format.EnvDebug, "true")
		t.Setenv(format.EnvLog, "")

		memfdCreateFunc = func(name string, flags int) (int, error) {
			return -1, syscall.ENOSYS
		}
		execveFunc = func(argv0 string, argv []string, envv []string) error {
			return nil
		}

		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		err := executeVariant(rawFile, entry, []string{testAppArg}, []string{testPathEnv}, hostInfo, policyRes, time.Now())
		_ = w.Close()
		os.Stderr = oldStderr

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)

		if err != nil {
			t.Fatalf("expected fallback to succeed, got: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "[microfat:hint]") || !strings.Contains(out, "unsupported on this Linux kernel") {
			t.Errorf("expected kernel unsupported hint, got: %s", out)
		}
	})
}

func TestMain_DiagnosticHints(t *testing.T) {
	oldExit := exitFunc
	defer func() { exitFunc = oldExit }()

	var exitCode int
	exitFunc = func(code int) {
		exitCode = code
	}

	oldGetSelf := getSelfExecutablePathFunc
	defer func() { getSelfExecutablePathFunc = oldGetSelf }()

	// 1. Fatal error without hint
	t.Run("fatal error without specific hint", func(t *testing.T) {
		t.Setenv(format.EnvLog, "")
		getSelfExecutablePathFunc = func() (string, error) {
			return "", errors.New("cannot determine self path")
		}

		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		main()
		_ = w.Close()
		os.Stderr = oldStderr

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)

		if exitCode != 1 {
			t.Errorf("expected exit code 1, got %d", exitCode)
		}
		if !strings.Contains(buf.String(), "[microfat] error:") {
			t.Errorf("expected error message in stderr, got: %s", buf.String())
		}
	})

	// 2. Fatal error with diagnosed hint (e.g. read-only filesystem EROFS)
	t.Run("fatal error with diagnosed hint in default mode", func(t *testing.T) {
		t.Setenv(format.EnvLog, "")
		getSelfExecutablePathFunc = func() (string, error) {
			return "", syscall.EROFS
		}

		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		main()
		_ = w.Close()
		os.Stderr = oldStderr

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)

		if exitCode != 1 {
			t.Errorf("expected exit code 1, got %d", exitCode)
		}
		out := buf.String()
		if !strings.Contains(out, "[microfat] error:") {
			t.Errorf("expected error in stderr, got: %s", out)
		}
		if !strings.Contains(out, "[microfat:hint]") || !strings.Contains(out, "Read-only filesystem detected") {
			t.Errorf("expected hint in stderr on fatal exit, got: %s", out)
		}
	})

	// 3. Fatal error with JSON logging includes Hint field
	t.Run("fatal error with JSON logging", func(t *testing.T) {
		t.Setenv(format.EnvLog, "json")
		getSelfExecutablePathFunc = func() (string, error) {
			return "", syscall.EROFS
		}

		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		main()
		_ = w.Close()
		os.Stderr = oldStderr

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)

		out := buf.String()
		var lines []string
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[microfat] {") {
				lines = append(lines, line)
			}
		}

		if len(lines) == 0 {
			t.Fatalf("expected JSON telemetry line, got: %s", out)
		}

		jsonStr := strings.TrimPrefix(lines[0], "[microfat] ")
		var telem format.ErrorTelemetry
		if err := json.Unmarshal([]byte(jsonStr), &telem); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v (raw: %s)", err, jsonStr)
		}

		if telem.Hint != format.HintReadOnlyFS {
			t.Errorf("expected Hint=%q, got %q", format.HintReadOnlyFS, telem.Hint)
		}
	})
}

func TestStubMultiCodecExecutionAndErrors(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create fake launcher stub & dummy variants
	stubPath := filepath.Join(tempDir, "microfat-stub")
	_ = os.WriteFile(stubPath, []byte("#!/bin/sh\necho stub\n"), 0o755)

	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("#!/bin/sh\necho v1\n"), 0o755)
	v3Path := filepath.Join(tempDir, "v3")
	_ = os.WriteFile(v3Path, []byte("#!/bin/sh\necho v3\n"), 0o755)

	// Create fat binary with lz4 v1 and none v3
	fatPath := filepath.Join(tempDir, "multicodec.fat")
	opts := pack.Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "multicodec-stub-test",
		TargetOS:          testOSLinux,
		TargetArch:        testArchAMD64,
		SkipELFValidation: true,
		VariantCompression: map[string]pack.VariantCompressionOptions{
			"v1": {Compression: "lz4"},
			"v3": {Compression: "none"},
		},
		Variants: map[string]string{
			"v1": v1Path,
			"v3": v3Path,
		},
	}
	idx, err := pack.Pack(opts)
	if err != nil {
		t.Fatalf("pack multicodec fat binary: %v", err)
	}

	selfFile, err := os.Open(fatPath)
	if err != nil {
		t.Fatalf("open fat binary: %v", err)
	}
	defer func() { _ = selfFile.Close() }()

	v1Entry, _ := idx.FindVariant("v1")
	v3Entry, _ := idx.FindVariant("v3")

	// 2. Test extractVariantToWriter for lz4
	var bufLZ4 bytes.Buffer
	if err := extractVariantToWriter(selfFile, v1Entry, &bufLZ4); err != nil {
		t.Fatalf("extract lz4 variant failed: %v", err)
	}
	if bufLZ4.String() != "#!/bin/sh\necho v1\n" {
		t.Errorf("lz4 extracted payload mismatch: %q", bufLZ4.String())
	}

	// 3. Test extractVariantToWriter for none
	var bufNone bytes.Buffer
	if err := extractVariantToWriter(selfFile, v3Entry, &bufNone); err != nil {
		t.Fatalf("extract none variant failed: %v", err)
	}
	if bufNone.String() != "#!/bin/sh\necho v3\n" {
		t.Errorf("none extracted payload mismatch: %q", bufNone.String())
	}

	// 4. Test extractVariantToWriter with unsupported codec
	badEntry := *v1Entry
	badEntry.Compression = "unknown_codec_xyz"
	var bufBad bytes.Buffer
	if err := extractVariantToWriter(selfFile, &badEntry, &bufBad); err == nil {
		t.Errorf("expected error extracting variant with unknown codec")
	}

	// 5. Test optimizeTo with lz4 variant
	optDest := filepath.Join(tempDir, "opt_dest")
	if err := optimizeTo(optDest, selfFile, v1Entry); err != nil {
		t.Fatalf("optimizeTo with lz4 variant failed: %v", err)
	}
	optBytes, _ := os.ReadFile(optDest)
	if string(optBytes) != "#!/bin/sh\necho v1\n" {
		t.Errorf("optimizeTo content mismatch: %q", string(optBytes))
	}
}

func TestBuildAutoTunedEnviron_GCProfiles(t *testing.T) {
	entry := &format.VariantEntry{
		Level:            "v3",
		SHA256:           "abcdef123456",
		UncompressedSize: 4096,
	}
	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{}

	oldRead := readCgroupLimitsFunc
	defer func() { readCgroupLimitsFunc = oldRead }()

	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{
			CgroupVersion:    cgroup.VersionV2,
			MemoryLimitBytes: 1024 * 1024 * 1024,
			CPUQuota:         4.0,
			CPUs:             4,
		}, nil
	}

	t.Run("LatencyCriticalProfile", func(t *testing.T) {
		t.Setenv(format.EnvGCProfile, "latency_critical")
		env, limits := buildAutoTunedEnviron(nil, entry, format.ExecModeMemfd, hostInfo, policyRes)
		if limits == nil {
			t.Fatalf("expected limits")
		}
		envMap := make(map[string]string)
		for _, e := range env {
			parts := strings.SplitN(e, "=", 2)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}
		if envMap["GOGC"] != "75" {
			t.Errorf("expected GOGC=75, got %q", envMap["GOGC"])
		}
		if envMap[format.EnvCgroupGCProfile] != "latency_critical" {
			t.Errorf("expected profile telemetry latency_critical, got %q", envMap[format.EnvCgroupGCProfile])
		}
	})

	t.Run("BatchETLProfile", func(t *testing.T) {
		t.Setenv(format.EnvGCProfile, "batch_etl")
		env, _ := buildAutoTunedEnviron(nil, entry, format.ExecModeMemfd, hostInfo, policyRes)
		envMap := make(map[string]string)
		for _, e := range env {
			parts := strings.SplitN(e, "=", 2)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}
		if envMap["GOGC"] != "off" {
			t.Errorf("expected GOGC=off, got %q", envMap["GOGC"])
		}
	})

	t.Run("ExplicitGOGCPrecedence", func(t *testing.T) {
		t.Setenv(format.EnvGCProfile, "latency_critical")
		baseEnv := []string{"GOGC=120"}
		env, _ := buildAutoTunedEnviron(baseEnv, entry, format.ExecModeMemfd, hostInfo, policyRes)
		gogcCount := 0
		var finalGOGC string
		for _, e := range env {
			if strings.HasPrefix(e, "GOGC=") {
				gogcCount++
				finalGOGC = strings.TrimPrefix(e, "GOGC=")
			}
		}
		if gogcCount != 1 || finalGOGC != "120" {
			t.Errorf("expected single explicit GOGC=120, got count=%d final=%q", gogcCount, finalGOGC)
		}
	})
}

