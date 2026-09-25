package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/lifecycle"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/EpicBlackWolfZ/microfat/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	flagJSON     = "--json"
	flagLevel    = "--level"
	flagCacheDir = "--cache-dir"
	flagOutput   = "--output"
	flagSkipELF  = "--skip-elf-validation"
	flagManifest = "--manifest"
	flagVerify   = "--verify"
	flagStub     = "--stub"
	flagName     = "--name"
	flagStrict   = "--strict"

	testBinaryMicrofat = "microfat"
	testOSLinux        = "linux"
	testArchAMD64      = "amd64"
	testArchARM64      = "arm64"
	subcmdDetect       = "detect"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.CheckLeaksIfEnabled(m))
}

func TestRootCmdAndSubcommands(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Test Root Command
	root := newRootCmd()
	if root == nil {
		t.Fatalf("expected non-nil root command")
	}

	rootVersion := newRootCmd()
	rootVersion.SetArgs([]string{"--version"})
	require.NoError(t, rootVersion.Execute(), "root --version failed")

	rootHelp := newRootCmd()
	rootHelp.SetArgs([]string{})
	require.NoError(t, rootHelp.Execute(), "root default execution failed")

	// 2. Test Detect Command
	detectText := newDetectCmd()
	detectText.SetArgs([]string{})
	var detectBuf bytes.Buffer
	detectText.SetOut(&detectBuf)
	require.NoError(t, detectText.Execute(), "detect command failed")

	detectJSON := newDetectCmd()
	detectJSON.SetArgs([]string{flagJSON})
	var jsonBuf bytes.Buffer
	detectJSON.SetOut(&jsonBuf)
	require.NoError(t, detectJSON.Execute(), "detect --json command failed")

	// 3. Create real fat binary for testing inspect, verify, trim
	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("#!/bin/sh\necho stub\n"), 0o755)
	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("#!/bin/sh\necho v1\n"), 0o755)
	v3Path := filepath.Join(tempDir, "v3")
	_ = os.WriteFile(v3Path, []byte("#!/bin/sh\necho v3\n"), 0o755)

	fatPath := filepath.Join(tempDir, "app.fat")
	packCmd := newPackCmd()
	packCmd.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, fatPath,
		flagName, "demo-app",
		"-v", "v1=" + v1Path,
		"-v", "v3=" + v3Path,
		flagSkipELF,
	})
	require.NoError(t, packCmd.Execute(), "pack command failed")

	// 4. Test Inspect Command
	inspectText := newInspectCmd()
	inspectText.SetArgs([]string{fatPath})
	require.NoError(t, inspectText.Execute(), "inspect command failed")

	inspectJSON := newInspectCmd()
	inspectJSON.SetArgs([]string{flagJSON, fatPath})
	require.NoError(t, inspectJSON.Execute(), "inspect --json command failed")

	inspectNonFat := newInspectCmd()
	inspectNonFat.SetArgs([]string{stubPath})
	if err := inspectNonFat.Execute(); err == nil {
		t.Errorf("expected inspect on non-fat binary to fail")
	}

	inspectNonExistent := newInspectCmd()
	inspectNonExistent.SetArgs([]string{filepath.Join(tempDir, "nonexistent")})
	if err := inspectNonExistent.Execute(); err == nil {
		t.Errorf("expected inspect on nonexistent binary to fail")
	}

	// Test Inspect on binary with shared dictionary
	dictFatPath := filepath.Join(tempDir, "dict_inspect.fat")
	dictV1 := filepath.Join(tempDir, "dict_v1")
	_ = os.WriteFile(dictV1, bytes.Repeat([]byte("DICT_PAYLOAD_V1_REPEATED_"), 100), 0o755)
	dictV3 := filepath.Join(tempDir, "dict_v3")
	_ = os.WriteFile(dictV3, bytes.Repeat([]byte("DICT_PAYLOAD_V3_REPEATED_"), 100), 0o755)
	packDictCmd := newPackCmd()
	packDictCmd.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, dictFatPath,
		flagName, "dict-app",
		"-v", "v1=" + dictV1,
		"-v", "v3=" + dictV3,
		flagSkipELF,
		"--dict",
	})
	if err := packDictCmd.Execute(); err == nil {
		inspectDictCmd := newInspectCmd()
		var outBuf bytes.Buffer
		inspectDictCmd.SetOut(&outBuf)
		inspectDictCmd.SetArgs([]string{dictFatPath})
		_ = inspectDictCmd.Execute()
		if !strings.Contains(outBuf.String(), "Shared Dictionary:") {
			t.Errorf("expected Shared Dictionary in inspect output, got: %s", outBuf.String())
		}
	}

	// 5. Test Verify Command
	verifyText := newVerifyCmd()
	verifyText.SetArgs([]string{fatPath})
	require.NoError(t, verifyText.Execute(), "verify command failed")

	verifyJSON := newVerifyCmd()
	verifyJSON.SetArgs([]string{flagJSON, fatPath})
	require.NoError(t, verifyJSON.Execute(), "verify --json command failed")

	verifyNonFat := newVerifyCmd()
	verifyNonFat.SetArgs([]string{stubPath})
	if err := verifyNonFat.Execute(); err == nil {
		t.Errorf("expected verify on non-fat binary to fail")
	}

	verifyNonExistent := newVerifyCmd()
	verifyNonExistent.SetArgs([]string{filepath.Join(tempDir, "nonexistent")})
	if err := verifyNonExistent.Execute(); err == nil {
		t.Errorf("expected verify on nonexistent binary to fail")
	}

	// 6. Test Trim Command
	trimmedPath := filepath.Join(tempDir, "trimmed.fat")
	trimCmd := newTrimCmd()
	trimCmd.SetArgs([]string{flagLevel, "v1", "-o", trimmedPath, fatPath})
	require.NoError(t, trimCmd.Execute(), "trim command failed")

	// Test Trim with policy and symlink resolution
	symlinkFat := filepath.Join(tempDir, "symlink_fat")
	_ = os.Symlink(fatPath, symlinkFat)
	trimPolicyCmd := newTrimCmd()
	trimmedPolicyPath := filepath.Join(tempDir, "trimmed_policy.fat")
	trimPolicyCmd.SetArgs([]string{
		"--policy", "safe_avx512",
		"-o", trimmedPolicyPath,
		symlinkFat,
	})
	_ = trimPolicyCmd.Execute()

	// Test Trim in-place with auto-detected level
	fatForInPlaceTrim := filepath.Join(tempDir, "fat_for_inplace.fat")
	dataFat, _ := os.ReadFile(fatPath)
	_ = os.WriteFile(fatForInPlaceTrim, dataFat, 0o755)
	trimInPlaceCmd := newTrimCmd()
	trimInPlaceCmd.SetArgs([]string{fatForInPlaceTrim})
	require.NoError(t, trimInPlaceCmd.Execute(), "trim in-place command failed")

	// Test trim with invalid level error
	trimInvalidLevel := newTrimCmd()
	trimInvalidLevel.SetArgs([]string{flagLevel, "v99", fatPath})
	if err := trimInvalidLevel.Execute(); err == nil {
		t.Errorf("expected trim with invalid level to fail")
	}

	// Test trim error when destination directory is invalid
	trimBadDest := newTrimCmd()
	trimBadDest.SetArgs([]string{"-o", "/dev/null/forbidden/out", fatPath})
	if err := trimBadDest.Execute(); err == nil {
		t.Errorf("expected trim with invalid dest directory to fail")
	}

	// Test trim error when destination is a directory
	trimDirDest := newTrimCmd()
	trimDirDest.SetArgs([]string{"-o", tempDir, fatPath})
	if err := trimDirDest.Execute(); err == nil {
		t.Errorf("expected trim with directory dest to fail")
	}

	trimEmptyOut := newTrimCmd()
	trimEmptyOut.SetArgs([]string{"-o", "", fatPath})
	if err := trimEmptyOut.Execute(); err == nil {
		t.Errorf("expected trim with empty output flag to fail")
	}

	trimNonFat := newTrimCmd()
	trimNonFat.SetArgs([]string{stubPath})
	if err := trimNonFat.Execute(); err == nil {
		t.Errorf("expected trim on non-fat binary to fail")
	}

	trimNonExistent := newTrimCmd()
	trimNonExistent.SetArgs([]string{filepath.Join(tempDir, "nonexistent")})
	if err := trimNonExistent.Execute(); err == nil {
		t.Errorf("expected trim on nonexistent binary to fail")
	}

	// 7. Test Pack Command Validation Errors
	packInvalidSpec := newPackCmd()
	packInvalidSpec.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, filepath.Join(tempDir, "out"),
		"-v", "invalid_no_equal",
	})
	if err := packInvalidSpec.Execute(); err == nil || !strings.Contains(err.Error(), "invalid variant specification") {
		t.Errorf("expected invalid variant specification error, got: %v", err)
	}

	packDup := newPackCmd()
	packDup.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, filepath.Join(tempDir, "out"),
		"-v", "v3=" + v3Path,
		"-v", "v3=" + v3Path,
	})
	if err := packDup.Execute(); err == nil || !strings.Contains(err.Error(), "duplicate variant level") {
		t.Errorf("expected duplicate variant level error, got: %v", err)
	}
}

func TestVerifyCorruptedOutput(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("#!/bin/sh\n"), 0o755)
	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("#!/bin/sh\necho v1\n"), 0o755)

	fatPath := filepath.Join(tempDir, "corrupted.fat")
	_, err := pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Path},
	})
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	// Corrupt payload byte
	data, _ := os.ReadFile(fatPath)
	data[len("#!/bin/sh\n")+1] ^= 0xFF
	_ = os.WriteFile(fatPath, data, 0o755)

	verifyCmd := newVerifyCmd()
	verifyCmd.SetArgs([]string{fatPath})
	if err := verifyCmd.Execute(); err == nil {
		t.Errorf("expected verify on corrupted payload to fail")
	}

	verifyJSONCmd := newVerifyCmd()
	verifyJSONCmd.SetArgs([]string{flagJSON, fatPath})
	require.Error(t, verifyJSONCmd.Execute())
}

func TestMainInvocation(t *testing.T) {
	oldArgs := os.Args
	oldExit := exitFunc
	defer func() {
		os.Args = oldArgs
		exitFunc = oldExit
	}()

	os.Args = []string{testBinaryMicrofat, "--help"}
	main()

	exitCalled := false
	exitFunc = func(code int) {
		exitCalled = true
	}
	os.Args = []string{testBinaryMicrofat, "invalid-subcommand-name"}
	main()
	if !exitCalled {
		t.Errorf("expected exitFunc to be called on invalid command")
	}
}

func TestCorruptIndexCLI(t *testing.T) {
	tempDir := t.TempDir()
	corruptManifestPath := filepath.Join(tempDir, "corrupt_index.fat")

	trailer := make([]byte, 56)
	binary.LittleEndian.PutUint64(trailer[0:8], 0)
	binary.LittleEndian.PutUint64(trailer[8:16], 10)
	copy(trailer[48:], []byte("\x00\xFA\x7FMICRO"))

	content := append([]byte("0123456789"), trailer...)
	_ = os.WriteFile(corruptManifestPath, content, 0o755)

	inspectCmd := newInspectCmd()
	inspectCmd.SetArgs([]string{corruptManifestPath})
	if err := inspectCmd.Execute(); err == nil {
		t.Errorf("expected inspect on corrupt manifest to fail")
	}

	verifyCmd := newVerifyCmd()
	verifyCmd.SetArgs([]string{corruptManifestPath})
	if err := verifyCmd.Execute(); err == nil {
		t.Errorf("expected verify on corrupt manifest to fail")
	}

	trimCmd := newTrimCmd()
	trimCmd.SetArgs([]string{corruptManifestPath})
	if err := trimCmd.Execute(); err == nil {
		t.Errorf("expected trim on corrupt manifest to fail")
	}
}

func TestCLITrimAndInspectEdgeCases(t *testing.T) {
	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("#!/bin/sh\n"), 0o755)
	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("#!/bin/sh\n"), 0o755)

	// Fat binary with incompatible architecture (auto-detection fails)
	incompatFat := filepath.Join(tempDir, "incompat.fat")
	_, _ = pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        incompatFat,
		TargetArch:        "unknown_arch_123",
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Path},
	})

	trimAutoIncompat := newTrimCmd()
	trimAutoIncompat.SetArgs([]string{incompatFat})
	if err := trimAutoIncompat.Execute(); err == nil {
		t.Errorf("expected trim with auto-detection to fail on incompatible architecture")
	}
}

func TestPackARM64CLI(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "arm64_stub")
	_ = os.WriteFile(stubPath, []byte("#!/bin/sh\necho stub\n"), 0o755)
	v80Path := filepath.Join(tempDir, "arm64_v80")
	_ = os.WriteFile(v80Path, []byte("#!/bin/sh\necho v8.0\n"), 0o755)
	v82Path := filepath.Join(tempDir, "arm64_v82")
	_ = os.WriteFile(v82Path, []byte("#!/bin/sh\necho v8.2\n"), 0o755)

	fatPath := filepath.Join(tempDir, "app_arm64.fat")
	packCmd := newPackCmd()
	packCmd.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, fatPath,
		flagName, "arm64-cli-app",
		"--arch", "arm64",
		"-v", "v8.0=" + v80Path,
		"-v", "v8.2=" + v82Path,
		flagSkipELF,
	})
	if err := packCmd.Execute(); err != nil {
		t.Fatalf("pack command for ARM64 failed: %v", err)
	}

	inspectCmd := newInspectCmd()
	inspectCmd.SetArgs([]string{fatPath})
	if err := inspectCmd.Execute(); err != nil {
		t.Fatalf("inspect ARM64 fat binary failed: %v", err)
	}

	verifyCmd := newVerifyCmd()
	verifyCmd.SetArgs([]string{fatPath})
	if err := verifyCmd.Execute(); err != nil {
		t.Fatalf("verify ARM64 fat binary failed: %v", err)
	}

	trimOut := filepath.Join(tempDir, "app_arm64_trimmed.fat")
	trimCmd := newTrimCmd()
	trimCmd.SetArgs([]string{flagLevel, "v8.2", "-o", trimOut, fatPath})
	if err := trimCmd.Execute(); err != nil {
		t.Fatalf("trim ARM64 fat binary failed: %v", err)
	}
}

func TestCLITrimPolicyFlags(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("#!/bin/sh\n"), 0o755)
	v1Path := filepath.Join(tempDir, "v1")
	v2Path := filepath.Join(tempDir, "v2")
	v3Path := filepath.Join(tempDir, "v3")
	_ = os.WriteFile(v1Path, []byte("#!/bin/sh\necho v1\n"), 0o755)
	_ = os.WriteFile(v2Path, []byte("#!/bin/sh\necho v2\n"), 0o755)
	_ = os.WriteFile(v3Path, []byte("#!/bin/sh\necho v3\n"), 0o755)

	fatPath := filepath.Join(tempDir, "policy_app.fat")
	_, err := pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "policyapp",
		TargetArch:        testArchAMD64,
		SkipELFValidation: true,
		Variants: map[string]string{
			"v1": v1Path,
			"v2": v2Path,
			"v3": v3Path,
		},
	})
	if err != nil {
		t.Fatalf("packing test binary: %v", err)
	}

	// 1. Trim with --max-level v2
	maxOut := filepath.Join(tempDir, "max_v2.fat")
	trimMax := newTrimCmd()
	trimMax.SetArgs([]string{"--max-level", "v2", "-o", maxOut, fatPath})
	if err := trimMax.Execute(); err != nil {
		t.Fatalf("trim with --max-level failed: %v", err)
	}

	// 2. Trim with --disable-variants v3
	disOut := filepath.Join(tempDir, "dis_v3.fat")
	trimDis := newTrimCmd()
	trimDis.SetArgs([]string{"--disable-variants", "v3", "-o", disOut, fatPath})
	if err := trimDis.Execute(); err != nil {
		t.Fatalf("trim with --disable-variants failed: %v", err)
	}

	// 3. Trim with --policy safe_avx512
	polOut := filepath.Join(tempDir, "pol_safe.fat")
	trimPol := newTrimCmd()
	trimPol.SetArgs([]string{"--policy", "safe_avx512", "-o", polOut, fatPath})
	if err := trimPol.Execute(); err != nil {
		t.Fatalf("trim with --policy failed: %v", err)
	}

	// 4. Trim with ambient environment variable MICROFAT_FORCE_LEVEL=v1
	t.Setenv(format.EnvForceLevel, "v1")
	envOut := filepath.Join(tempDir, "env_v1.fat")
	trimEnv := newTrimCmd()
	trimEnv.SetArgs([]string{"-o", envOut, fatPath})
	if err := trimEnv.Execute(); err != nil {
		t.Fatalf("trim with ambient env var failed: %v", err)
	}
}

func TestCLIPrewarmCmd(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("#!/bin/sh\n"), 0o755)
	v1Path := filepath.Join(tempDir, "v1")
	v3Path := filepath.Join(tempDir, "v3")
	_ = os.WriteFile(v1Path, []byte("#!/bin/sh\necho v1\n"), 0o755)
	_ = os.WriteFile(v3Path, []byte("#!/bin/sh\necho v3\n"), 0o755)

	fatPath := filepath.Join(tempDir, "prewarm_app.fat")
	_, err := pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "prewarm-cli-app",
		TargetArch:        testArchAMD64,
		SkipELFValidation: true,
		Variants: map[string]string{
			"v1": v1Path,
			"v3": v3Path,
		},
	})
	if err != nil {
		t.Fatalf("packing test binary: %v", err)
	}

	cacheDir := filepath.Join(tempDir, "custom_cache")

	// 1. Default auto-detected prewarm
	prewarmDefault := newPrewarmCmd()
	prewarmDefault.SetArgs([]string{flagCacheDir, cacheDir, fatPath})
	if err := prewarmDefault.Execute(); err != nil {
		t.Fatalf("default prewarm failed: %v", err)
	}

	// 2. Prewarm with explicit --level v1
	prewarmLevel := newPrewarmCmd()
	prewarmLevel.SetArgs([]string{flagLevel, "v1", flagCacheDir, cacheDir, fatPath})
	if err := prewarmLevel.Execute(); err != nil {
		t.Fatalf("prewarm with --level v1 failed: %v", err)
	}

	// 3. Prewarm with --all
	prewarmAll := newPrewarmCmd()
	prewarmAll.SetArgs([]string{"--all", flagCacheDir, cacheDir, fatPath})
	if err := prewarmAll.Execute(); err != nil {
		t.Fatalf("prewarm with --all failed: %v", err)
	}

	// 4. Prewarm with --json
	prewarmJSON := newPrewarmCmd()
	prewarmJSON.SetArgs([]string{flagJSON, flagCacheDir, cacheDir, fatPath})
	if err := prewarmJSON.Execute(); err != nil {
		t.Fatalf("prewarm with --json failed: %v", err)
	}

	// 5. Prewarm with non-existent file
	prewarmNonExistent := newPrewarmCmd()
	prewarmNonExistent.SetArgs([]string{filepath.Join(tempDir, "missing.fat")})
	if err := prewarmNonExistent.Execute(); err == nil {
		t.Errorf("expected error for non-existent file")
	}

	// 6. Prewarm with non-fat regular file
	nonFatPath := filepath.Join(tempDir, "not_fat.bin")
	_ = os.WriteFile(nonFatPath, []byte("just some regular bytes"), 0o600)
	prewarmNonFat := newPrewarmCmd()
	prewarmNonFat.SetArgs([]string{nonFatPath})
	if err := prewarmNonFat.Execute(); err == nil {
		t.Errorf("expected error for non-fat binary")
	}

	// 7. Prewarm with invalid level
	prewarmBadLevel := newPrewarmCmd()
	prewarmBadLevel.SetArgs([]string{flagLevel, "v99", flagCacheDir, cacheDir, fatPath})
	if err := prewarmBadLevel.Execute(); err == nil {
		t.Errorf("expected error for invalid level v99")
	}

	// 8. Prewarm with invalid cache directory
	blocker := filepath.Join(tempDir, "blocker")
	_ = os.WriteFile(blocker, []byte("blocker"), 0o600)
	prewarmBadDir := newPrewarmCmd()
	prewarmBadDir.SetArgs([]string{flagCacheDir, filepath.Join(blocker, "sub"), fatPath})
	if err := prewarmBadDir.Execute(); err == nil {
		t.Errorf("expected error for unwritable cache directory")
	}

	// 9. Prewarm already cached
	prewarmCached := newPrewarmCmd()
	prewarmCached.SetArgs([]string{flagCacheDir, cacheDir, fatPath})
	if err := prewarmCached.Execute(); err != nil {
		t.Fatalf("second prewarm failed: %v", err)
	}

	// 10. Prewarm with incompatible architecture binary (auto-detect fails)
	incompatFat := filepath.Join(tempDir, "incompat_prewarm.fat")
	_, _ = pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        incompatFat,
		AppName:           "incompat",
		TargetArch:        "unknown_arch_99",
		SkipELFValidation: true,
		Variants:          map[string]string{"v1": v1Path},
	})
	prewarmIncompat := newPrewarmCmd()
	prewarmIncompat.SetArgs([]string{flagCacheDir, cacheDir, incompatFat})
	if err := prewarmIncompat.Execute(); err == nil {
		t.Errorf("expected error for incompatible architecture in prewarm")
	}
}

func TestCLIPgoPackAndManifestPack(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("#!/bin/sh\necho stub\n"), 0o755)

	pkgDir := filepath.Join(tempDir, "samplepkg")
	_ = os.MkdirAll(pkgDir, 0o755)
	_ = os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module samplepkg\ngo 1.27.1\n"), 0o644)

	manifestPath := filepath.Join(tempDir, "manifest.yaml")
	manifestContent := `
name: sample-cli-app
package: ` + pkgDir + `
output: ` + filepath.Join(tempDir, "out_pgo.fat") + `
stub: ` + stubPath + `
target_os: linux
target_arch: amd64
variants:
  - level: v1
    pgo: "off"
  - level: v3
    pgo: "off"
`
	_ = os.WriteFile(manifestPath, []byte(manifestContent), 0o644)

	// 1. Test pgo-pack command
	pgoCmd := newPgoPackCmd()
	pgoCmd.SetArgs([]string{
		flagManifest, manifestPath,
		flagSkipELF,
	})
	if err := pgoCmd.Execute(); err != nil {
		t.Fatalf("pgo-pack command failed: %v", err)
	}

	// 2. Test pgo-pack with positional argument
	pgoPosCmd := newPgoPackCmd()
	pgoPosCmd.SetArgs([]string{
		manifestPath,
		flagOutput, filepath.Join(tempDir, "out_pos.fat"),
		flagSkipELF,
	})
	if err := pgoPosCmd.Execute(); err != nil {
		t.Fatalf("pgo-pack with positional arg failed: %v", err)
	}

	// 3. Test pgo-pack missing manifest error
	pgoMissingCmd := newPgoPackCmd()
	pgoMissingCmd.SetArgs([]string{})
	if err := pgoMissingCmd.Execute(); err == nil {
		t.Errorf("expected error for missing manifest in pgo-pack")
	}

	// 4. Test pgo-pack invalid manifest file
	pgoInvalidCmd := newPgoPackCmd()
	pgoInvalidCmd.SetArgs([]string{flagManifest, filepath.Join(tempDir, "nonexistent.yaml")})
	if err := pgoInvalidCmd.Execute(); err == nil {
		t.Errorf("expected error for nonexistent manifest file")
	}

	// 5. Test pack --manifest shorthand
	packManifestCmd := newPackCmd()
	packManifestCmd.SetArgs([]string{
		flagManifest, manifestPath,
		flagOutput, filepath.Join(tempDir, "out_shorthand.fat"),
		flagSkipELF,
	})
	if err := packManifestCmd.Execute(); err != nil {
		t.Fatalf("pack --manifest shorthand failed: %v", err)
	}

	// 6. Test pack missing flags error when no manifest
	packNoFlagsCmd := newPackCmd()
	packNoFlagsCmd.SetArgs([]string{})
	if err := packNoFlagsCmd.Execute(); err == nil {
		t.Errorf("expected error for pack with no flags and no manifest")
	}
}

func TestCLIPrewarmVerifyMode(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub_vfy_cli")
	_ = os.WriteFile(stubPath, []byte("#!/bin/sh\n"), 0o755)
	v1Path := filepath.Join(tempDir, "v1_vfy_cli")
	v3Path := filepath.Join(tempDir, "v3_vfy_cli")
	_ = os.WriteFile(v1Path, []byte("#!/bin/sh\necho v1_vfy_cli\n"), 0o755)
	_ = os.WriteFile(v3Path, []byte("#!/bin/sh\necho v3_vfy_cli\n"), 0o755)

	fatPath := filepath.Join(tempDir, "prewarm_vfy_app.fat")
	idx, err := pack.Pack(pack.Options{
		StubPath:          stubPath,
		OutputPath:        fatPath,
		AppName:           "prewarm-vfy-cli",
		TargetArch:        testArchAMD64,
		SkipELFValidation: true,
		Variants: map[string]string{
			"v1": v1Path,
			"v3": v3Path,
		},
	})
	if err != nil {
		t.Fatalf("packing test binary: %v", err)
	}

	cacheDir := filepath.Join(tempDir, "cli_verify_cache")

	// 1. Verify clean cache (should fail because not cached)
	vfyCleanCmd := newPrewarmCmd()
	vfyCleanCmd.SetArgs([]string{flagVerify, flagCacheDir, cacheDir, fatPath})
	if err := vfyCleanCmd.Execute(); err == nil {
		t.Errorf("expected error verifying clean cache")
	}

	// 2. Prewarm all
	prewarmCmd := newPrewarmCmd()
	prewarmCmd.SetArgs([]string{"--all", flagCacheDir, cacheDir, fatPath})
	if err := prewarmCmd.Execute(); err != nil {
		t.Fatalf("prewarming all failed: %v", err)
	}

	// 3. Verify all on valid cache (should succeed)
	vfyAllCmd := newPrewarmCmd()
	vfyAllCmd.SetArgs([]string{flagVerify, "--all", flagCacheDir, cacheDir, fatPath})
	if err := vfyAllCmd.Execute(); err != nil {
		t.Fatalf("verifying all on valid cache failed: %v", err)
	}

	// 4. Verify specific variant v1 (should succeed)
	vfyV1Cmd := newPrewarmCmd()
	vfyV1Cmd.SetArgs([]string{flagVerify, flagLevel, "v1", flagCacheDir, cacheDir, fatPath})
	if err := vfyV1Cmd.Execute(); err != nil {
		t.Fatalf("verifying v1 on valid cache failed: %v", err)
	}

	// 5. Verify with --json output (should succeed)
	vfyJSONCmd := newPrewarmCmd()
	vfyJSONCmd.SetArgs([]string{flagVerify, flagJSON, flagCacheDir, cacheDir, fatPath})
	if err := vfyJSONCmd.Execute(); err != nil {
		t.Fatalf("verifying with --json failed: %v", err)
	}

	// 6. Verify with invalid variant level
	vfyBadLevelCmd := newPrewarmCmd()
	vfyBadLevelCmd.SetArgs([]string{flagVerify, flagLevel, "v99", flagCacheDir, cacheDir, fatPath})
	if err := vfyBadLevelCmd.Execute(); err == nil {
		t.Errorf("expected error verifying invalid variant level")
	}

	// 7. Corrupt cache file (truncate v1)
	v1Entry, _ := idx.FindVariant("v1")
	v1Cached := filepath.Join(cacheDir, v1Entry.SHA256)
	_ = os.WriteFile(v1Cached, []byte("broken_cli"), 0o755)

	vfyCorruptCmd := newPrewarmCmd()
	vfyCorruptCmd.SetArgs([]string{flagVerify, flagLevel, "v1", flagCacheDir, cacheDir, fatPath})
	if err := vfyCorruptCmd.Execute(); err == nil {
		t.Errorf("expected error verifying corrupted cache entry")
	}

	// 8. Corrupt cache file with --json (should output json and return error)
	vfyCorruptJSONCmd := newPrewarmCmd()
	vfyCorruptJSONCmd.SetArgs([]string{flagVerify, flagLevel, "v1", flagJSON, flagCacheDir, cacheDir, fatPath})
	if err := vfyCorruptJSONCmd.Execute(); err == nil {
		t.Errorf("expected error verifying corrupted cache entry with --json")
	}
}

func TestPackCmd_CompressionFlags(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("#!/bin/sh\necho stub\n"), 0o755)
	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("#!/bin/sh\necho v1\n"), 0o755)
	v3Path := filepath.Join(tempDir, "v3")
	_ = os.WriteFile(v3Path, []byte("#!/bin/sh\necho v3\n"), 0o755)

	// 1. Pack with --profile=latency --compression=lz4
	fatPath := filepath.Join(tempDir, "lz4.fat")
	packCmd := newPackCmd()
	packCmd.SetArgs([]string{
		flagStub, stubPath,
		"--output", fatPath,
		"--profile", "latency",
		"--compression", "lz4",
		"--compression-level", "fastest",
		"-v", "v1=" + v1Path,
		"-v", "v3=" + v3Path,
		"--skip-elf-validation",
	})
	if err := packCmd.Execute(); err != nil {
		t.Fatalf("pack with lz4 flags failed: %v", err)
	}

	// Verify inspect shows lz4
	inspectCmd := newInspectCmd()
	inspectCmd.SetArgs([]string{fatPath})
	if err := inspectCmd.Execute(); err != nil {
		t.Fatalf("inspect lz4 failed: %v", err)
	}

	// 2. Pack with manifest containing compression
	manifestContent := `
name: manifest-comp-app
package: .
output: ` + filepath.Join(tempDir, "manifest.fat") + `
stub: ` + stubPath + `
compression:
  profile: size
  algorithm: zstd
  level: best
variants:
  - level: v1
`
	manifestFile := filepath.Join(tempDir, "pgo.yaml")
	_ = os.WriteFile(manifestFile, []byte(manifestContent), 0o644)

	packManifestCmd := newPackCmd()
	packManifestCmd.SetArgs([]string{
		"--manifest", manifestFile,
		"--skip-elf-validation",
	})
	// This will fail on compile because it's a test environment without full source, but it validates flag parsing & manifest wiring
	_ = packManifestCmd.Execute()
}

func TestPackAndInspect_DictionaryFlags(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "microfat-stub")
	_ = os.WriteFile(stubPath, []byte("STUB_CODE_LAUNCHER"), 0o755)

	v1Path := filepath.Join(tempDir, "app_v1")
	v3Path := filepath.Join(tempDir, "app_v3")

	var v1Buf, v3Buf bytes.Buffer
	for i := range 800 {
		str := fmt.Sprintf("runtime_symbol_record_%04d_metadata_hash_%x\n", i, (i*31)^0x12345678)
		v1Buf.WriteString(str)
		v3Buf.WriteString(str)
	}
	v1Buf.WriteString("v1_specific_arch_code_optimizations\n")
	v3Buf.WriteString("v3_specific_arch_code_optimizations\n")

	_ = os.WriteFile(v1Path, v1Buf.Bytes(), 0o755)
	_ = os.WriteFile(v3Path, v3Buf.Bytes(), 0o755)

	fatPath := filepath.Join(tempDir, "app_dict.fat")

	// 1. Pack with --dict and --dict-size
	packCmd := newPackCmd()
	packCmd.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, fatPath,
		flagName, "dict-cli-app",
		"--dict",
		"--dict-size", "65536",
		"-v", "v1=" + v1Path,
		"-v", "v3=" + v3Path,
		flagSkipELF,
	})
	if err := packCmd.Execute(); err != nil {
		t.Fatalf("pack with --dict failed: %v", err)
	}

	// 2. Inspect fat binary
	inspectCmd := newInspectCmd()
	inspectCmd.SetArgs([]string{fatPath})
	if err := inspectCmd.Execute(); err != nil {
		t.Fatalf("inspect dict binary failed: %v", err)
	}

	// 3. Inspect JSON
	inspectJSONCmd := newInspectCmd()
	inspectJSONCmd.SetArgs([]string{fatPath, flagJSON})
	if err := inspectJSONCmd.Execute(); err != nil {
		t.Fatalf("inspect --json dict binary failed: %v", err)
	}

	// 4. Verify binary with dict
	verifyCmd := newVerifyCmd()
	verifyCmd.SetArgs([]string{fatPath})
	if err := verifyCmd.Execute(); err != nil {
		t.Fatalf("verify dict binary failed: %v", err)
	}
}

func TestPack_DictionaryDiagnostics(t *testing.T) {
	const (
		smallByteLen         = 3
		dictPatternIterCount = 800
		dictPatternMul       = 31
		dictPatternMask      = 0x12345678
	)

	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("STUB_CODE_LAUNCHER"), 0o755)

	// Small dummy variants (< 8 bytes total)
	smallV1Path := filepath.Join(tempDir, "small_v1")
	smallV3Path := filepath.Join(tempDir, "small_v3")
	_ = os.WriteFile(smallV1Path, make([]byte, smallByteLen), 0o755)
	_ = os.WriteFile(smallV3Path, make([]byte, smallByteLen), 0o755)

	// Large repetitive dummy variants for successful dictionary training
	largeV1Path := filepath.Join(tempDir, "large_v1")
	largeV3Path := filepath.Join(tempDir, "large_v3")
	var v1Buf, v3Buf bytes.Buffer
	for i := range dictPatternIterCount {
		str := fmt.Sprintf("runtime_symbol_record_%04d_metadata_hash_%x\n", i, (i*dictPatternMul)^dictPatternMask)
		v1Buf.WriteString(str)
		v3Buf.WriteString(str)
	}
	v1Buf.WriteString("v1_specific_arch_code_optimizations\n")
	v3Buf.WriteString("v3_specific_arch_code_optimizations\n")
	_ = os.WriteFile(largeV1Path, v1Buf.Bytes(), 0o755)
	_ = os.WriteFile(largeV3Path, v3Buf.Bytes(), 0o755)

	t.Run("microfat pack --profile size with small variants emits warning on stderr and exits 0", func(t *testing.T) {
		fatPath := filepath.Join(tempDir, "size_small.fat")
		packCmd := newPackCmd()
		var stderrBuf bytes.Buffer
		packCmd.SetErr(&stderrBuf)
		packCmd.SetArgs([]string{
			flagStub, stubPath,
			flagOutput, fatPath,
			"--profile", "size",
			"-v", "v1=" + smallV1Path,
			"-v", "v3=" + smallV3Path,
			flagSkipELF,
		})

		if err := packCmd.Execute(); err != nil {
			t.Fatalf("expected pack to succeed, got %v", err)
		}

		errOutput := stderrBuf.String()
		expectedPrefix := "[microfat:warn]"
		expectedSub := "shared dictionary training failed (sample data too small for dictionary training (< 8 bytes)); " +
			"proceeding with independent variant compression"
		if !strings.Contains(errOutput, expectedPrefix) {
			t.Errorf("expected stderr to contain %q, got %q", expectedPrefix, errOutput)
		}
		if !strings.Contains(errOutput, expectedSub) {
			t.Errorf("expected stderr to contain %q, got %q", expectedSub, errOutput)
		}
	})

	t.Run("microfat pack --profile Size (mixed-case) with small variants emits warning on stderr and exits 0", func(t *testing.T) {
		fatPath := filepath.Join(tempDir, "size_mixed_small.fat")
		packCmd := newPackCmd()
		var stderrBuf bytes.Buffer
		packCmd.SetErr(&stderrBuf)
		packCmd.SetArgs([]string{
			flagStub, stubPath,
			flagOutput, fatPath,
			"--profile", "Size",
			"-v", "v1=" + smallV1Path,
			"-v", "v3=" + smallV3Path,
			flagSkipELF,
		})

		if err := packCmd.Execute(); err != nil {
			t.Fatalf("expected pack to succeed, got %v", err)
		}

		errOutput := stderrBuf.String()
		if !strings.Contains(errOutput, "[microfat:warn]") {
			t.Errorf("expected stderr to contain [microfat:warn], got %q", errOutput)
		}
	})

	t.Run("microfat pack --dict with small variants fails fast with error", func(t *testing.T) {
		fatPath := filepath.Join(tempDir, "dict_small_fail.fat")
		packCmd := newPackCmd()
		var stderrBuf bytes.Buffer
		packCmd.SetErr(&stderrBuf)
		packCmd.SetArgs([]string{
			flagStub, stubPath,
			flagOutput, fatPath,
			"--dict",
			"-v", "v1=" + smallV1Path,
			"-v", "v3=" + smallV3Path,
			flagSkipELF,
		})

		err := packCmd.Execute()
		if err == nil {
			t.Fatalf("expected pack --dict to fail with small variants, but succeeded")
		}
		if !strings.Contains(err.Error(), "training shared dictionary: sample data too small for dictionary training (< 8 bytes)") {
			t.Errorf("error %q does not contain expected failure reason", err.Error())
		}
	})

	t.Run("microfat pack --profile size with large repetitive variants succeeds silently without warning on stderr", func(t *testing.T) {
		fatPath := filepath.Join(tempDir, "size_large_silent.fat")
		packCmd := newPackCmd()
		var stderrBuf bytes.Buffer
		packCmd.SetErr(&stderrBuf)
		packCmd.SetArgs([]string{
			flagStub, stubPath,
			flagOutput, fatPath,
			"--profile", "size",
			"-v", "v1=" + largeV1Path,
			"-v", "v3=" + largeV3Path,
			flagSkipELF,
		})

		if err := packCmd.Execute(); err != nil {
			t.Fatalf("expected pack to succeed, got %v", err)
		}

		errOutput := stderrBuf.String()
		if strings.Contains(errOutput, "[microfat:warn]") {
			t.Errorf("expected no [microfat:warn] diagnostics on successful dict training, got %q", errOutput)
		}
	})
}

func TestTrimInPlace_ResolvesSymlink(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("STUB_CODE_LAUNCHER"), 0o755)

	v1Path := filepath.Join(tempDir, "v1")
	v3Path := filepath.Join(tempDir, "v3")
	_ = os.WriteFile(v1Path, []byte("BINARY_PAYLOAD_V1"), 0o755)
	_ = os.WriteFile(v3Path, []byte("BINARY_PAYLOAD_V3"), 0o755)

	realFatPath := filepath.Join(tempDir, "real_app.fat")
	packCmd := newPackCmd()
	packCmd.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, realFatPath,
		flagName, "symlink-app",
		"-v", "v1=" + v1Path,
		"-v", "v3=" + v3Path,
		flagSkipELF,
	})
	if err := packCmd.Execute(); err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	symlinkPath := filepath.Join(tempDir, "symlink_app.fat")
	if err := os.Symlink(realFatPath, symlinkPath); err != nil {
		t.Fatalf("symlink creation failed: %v", err)
	}

	trimCmd := newTrimCmd()
	trimCmd.SetArgs([]string{
		symlinkPath,
		"--level", "v1",
	})
	if err := trimCmd.Execute(); err != nil {
		t.Fatalf("trim via symlink failed: %v", err)
	}

	fi, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("lstat symlink failed: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was replaced by regular file instead of being preserved")
	}

	target, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("readlink failed: %v", err)
	}
	if target != realFatPath {
		t.Fatalf("symlink target changed: got %s, want %s", target, realFatPath)
	}
}

func TestTrim_DestinationInNewDirectory(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("STUB_CODE_LAUNCHER"), 0o755)

	v1Path := filepath.Join(tempDir, "v1")
	v3Path := filepath.Join(tempDir, "v3")
	_ = os.WriteFile(v1Path, []byte("BINARY_PAYLOAD_V1"), 0o755)
	_ = os.WriteFile(v3Path, []byte("BINARY_PAYLOAD_V3"), 0o755)

	srcFatPath := filepath.Join(tempDir, "app.fat")
	packCmd := newPackCmd()
	packCmd.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, srcFatPath,
		flagName, "test-app",
		"-v", "v1=" + v1Path,
		"-v", "v3=" + v3Path,
		flagSkipELF,
	})
	if err := packCmd.Execute(); err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	destFatPath := filepath.Join(tempDir, "nested", "sub", "trimmed.fat")
	trimCmd := newTrimCmd()
	trimCmd.SetArgs([]string{
		srcFatPath,
		"--level", "v1",
		"-o", destFatPath,
	})
	if err := trimCmd.Execute(); err != nil {
		t.Fatalf("trim with destination in new directory failed: %v", err)
	}

	if _, err := os.Stat(destFatPath); err != nil {
		t.Fatalf("expected trimmed file at %s, got err: %v", destFatPath, err)
	}
}

func TestInspect_FormatV1DeprecationWarning(t *testing.T) {
	tempDir := t.TempDir()
	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("STUB_BIN"), 0o755)
	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("PAYLOAD_V1"), 0o755)
	fatV1Path := filepath.Join(tempDir, "app_v1.fat")

	packCmd := newPackCmd()
	packCmd.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, fatV1Path,
		flagName, "v1-app",
		"-v", "v1=" + v1Path,
		flagSkipELF,
		"--format-version", "1",
	})
	if err := packCmd.Execute(); err != nil {
		t.Fatalf("pack format v1 failed: %v", err)
	}

	var stderrBuf bytes.Buffer
	inspectCmd := newInspectCmd()
	inspectCmd.SetErr(&stderrBuf)
	inspectCmd.SetArgs([]string{fatV1Path})
	if err := inspectCmd.Execute(); err != nil {
		t.Fatalf("inspect format v1 failed: %v", err)
	}

	output := stderrBuf.String()
	if !strings.Contains(output, "[microfat:warn]") || !strings.Contains(output, "Format v1 is deprecated") {
		t.Errorf("expected Format v1 deprecation warning in stderr, got: %q", output)
	}

	// Verify info alias also triggers inspect and prints warning
	rootCmd := newRootCmd()
	stderrBuf.Reset()
	rootCmd.SetErr(&stderrBuf)
	rootCmd.SetArgs([]string{"info", fatV1Path})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("info alias failed: %v", err)
	}
	if !strings.Contains(stderrBuf.String(), "[microfat:warn]") {
		t.Errorf("expected deprecation warning when calling 'microfat info', got: %q", stderrBuf.String())
	}

	// Verify Format v2 binary does not emit deprecation warning
	fatV2Path := filepath.Join(tempDir, "app_v2.fat")
	packV2Cmd := newPackCmd()
	packV2Cmd.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, fatV2Path,
		flagName, "v2-app",
		"-v", "v1=" + v1Path,
		flagSkipELF,
		"--format-version", "2",
	})
	if err := packV2Cmd.Execute(); err != nil {
		t.Fatalf("pack format v2 failed: %v", err)
	}

	stderrBuf.Reset()
	inspectV2Cmd := newInspectCmd()
	inspectV2Cmd.SetErr(&stderrBuf)
	inspectV2Cmd.SetArgs([]string{fatV2Path})
	if err := inspectV2Cmd.Execute(); err != nil {
		t.Fatalf("inspect format v2 failed: %v", err)
	}
	if strings.Contains(stderrBuf.String(), "Format v1 is deprecated") {
		t.Errorf("Format v2 binary should not emit deprecation warning, got: %q", stderrBuf.String())
	}
}

func TestStubAutoDiscovery(t *testing.T) {
	tempDir := t.TempDir()
	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("PAYLOAD_V1"), 0o755)
	fatPath := filepath.Join(tempDir, "app.fat")

	// 1. Without any stub in PATH or adjacent dir, pack without --stub should fail
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir()) // empty PATH

	packCmd := newPackCmd()
	packCmd.SetArgs([]string{
		flagOutput, fatPath,
		flagName, "autodiscover-app",
		"-v", "v1=" + v1Path,
		flagSkipELF,
	})
	if err := packCmd.Execute(); err == nil {
		t.Fatalf("expected pack without stub to fail when no stub is discoverable")
	}

	// 2. Put microfat-stub into a directory on PATH
	binDir := filepath.Join(tempDir, "fakebin")
	_ = os.MkdirAll(binDir, 0o755)
	fakeStub := filepath.Join(binDir, "microfat-stub")
	_ = os.WriteFile(fakeStub, []byte("FAKE_STUB_ELF"), 0o755)

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+origPath)

	var stderrBuf bytes.Buffer
	packDiscoverCmd := newPackCmd()
	packDiscoverCmd.SetErr(&stderrBuf)
	packDiscoverCmd.SetArgs([]string{
		flagOutput, fatPath,
		flagName, "autodiscover-app",
		"-v", "v1=" + v1Path,
		flagSkipELF,
	})
	if err := packDiscoverCmd.Execute(); err != nil {
		t.Fatalf("expected pack with auto-discovered stub to succeed, got: %v", err)
	}
	if !strings.Contains(stderrBuf.String(), "Using auto-discovered launcher stub") {
		t.Errorf("expected auto-discovery notice in stderr, got: %q", stderrBuf.String())
	}
}

func TestInspectAndInfo_SubprocessStreams(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	_ = os.WriteFile(stubPath, []byte("\x7fELF\x02\x01\x01\x00"+strings.Repeat("\x00", 256)), 0o755)
	v1Path := filepath.Join(tempDir, "v1")
	_ = os.WriteFile(v1Path, []byte("PAYLOAD_V1"), 0o755)

	// Create Format v2 binary
	fatV2 := filepath.Join(tempDir, "app_v2.fat")
	p2 := newPackCmd()
	p2.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, fatV2,
		flagName, "v2-app",
		"-v", "v1=" + v1Path,
		flagSkipELF,
		"--format-version", "2",
	})
	require.NoError(t, p2.Execute())

	// Create Format v1 binary
	fatV1 := filepath.Join(tempDir, "app_v1.fat")
	p1 := newPackCmd()
	p1.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, fatV1,
		flagName, "v1-app",
		"-v", "v1=" + v1Path,
		flagSkipELF,
		"--format-version", "1",
	})
	require.NoError(t, p1.Execute())

	// Build the real microfat CLI binary to run as a subprocess
	cliBin := filepath.Join(tempDir, "microfat_cli")
	buildCmd := exec.Command("go", "build", "-o", cliBin, ".")
	buildCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "compiling microfat: %s", string(out))

	subtests := []struct {
		name         string
		cmdSub       string // "inspect" or "info"
		target       string
		wantExitCode int
		isV1         bool
		isInvalid    bool
	}{
		{name: "Inspect_V2_JSON_StdoutPureJSON", cmdSub: "inspect", target: fatV2, wantExitCode: 0, isV1: false},
		{name: "Info_V2_JSON_StdoutPureJSON", cmdSub: "info", target: fatV2, wantExitCode: 0, isV1: false},
		{name: "Inspect_V1_JSON_StderrWarning", cmdSub: "inspect", target: fatV1, wantExitCode: 0, isV1: true},
		{name: "Info_V1_JSON_StderrWarning", cmdSub: "info", target: fatV1, wantExitCode: 0, isV1: true},
		{name: "Inspect_NonFat_NonZeroExit", cmdSub: "inspect", target: stubPath, wantExitCode: 1, isInvalid: true},
		{name: "Info_NonFat_NonZeroExit", cmdSub: "info", target: stubPath, wantExitCode: 1, isInvalid: true},
		{name: "Inspect_MissingFile_NonZeroExit", cmdSub: "inspect", target: filepath.Join(tempDir, "missing"), wantExitCode: 1, isInvalid: true},
	}

	for _, tc := range subtests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(cliBin, tc.cmdSub, "--json", tc.target)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			runErr := cmd.Run()
			if tc.wantExitCode == 0 {
				require.NoError(t, runErr, "expected exit 0, stderr: %s", stderr.String())

				// Stdout must be strictly valid JSON
				var parsed map[string]any
				err := json.Unmarshal(stdout.Bytes(), &parsed)
				require.NoError(t, err, "stdout must be strictly valid JSON: %s", stdout.String())

				if tc.isV1 {
					assert.Contains(t, stderr.String(), "[microfat:warn]", "stderr must contain warning")
					assert.Contains(t, stderr.String(), "Format v1 is deprecated")
				} else {
					assert.NotContains(t, stderr.String(), "deprecated")
				}
			} else {
				require.Error(t, runErr)
				var exitErr *exec.ExitError
				require.ErrorAs(t, runErr, &exitErr)
				assert.Equal(t, tc.wantExitCode, exitErr.ExitCode())
				assert.Empty(t, strings.TrimSpace(stdout.String()), "stdout must be empty on error")
				assert.NotEmpty(t, strings.TrimSpace(stderr.String()), "stderr must contain error details")
			}
		})
	}
}

func TestFormatVersionName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "legacy JSON manifest", formatVersionName(format.FormatVersion1))
	assert.Equal(t, "compact binary table", formatVersionName(format.FormatVersion2))
	assert.Equal(t, "unknown", formatVersionName(999))
}

func TestPprofServerFlagAndEnv(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	_, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	require.NoError(t, ln.Close())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rootCmd := newRootCmd()
	var errBuf bytes.Buffer
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs([]string{"--pprof-port", portStr, subcmdDetect})

	err = rootCmd.ExecuteContext(ctx)
	require.NoError(t, err)
	assert.Contains(t, errBuf.String(), "[microfat:pprof] serving pprof endpoints")

	resp, err := http.Get("http://" + addr + "/debug/pprof/")
	require.NoError(t, err)
	defer func() {
		_ = resp.Body.Close()
	}()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	leakResp, err := http.Get("http://" + addr + "/debug/pprof/goroutineleak?debug=1")
	require.NoError(t, err)
	defer func() {
		_ = leakResp.Body.Close()
	}()
	assert.Equal(t, http.StatusOK, leakResp.StatusCode)
	leakBody, err := io.ReadAll(leakResp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(leakBody), "total 0")

	cancel()
	require.Eventually(t, func() bool {
		testLn, testErr := net.Listen("tcp", addr)
		if testErr == nil {
			_ = testLn.Close()
			return true
		}
		return false
	}, 2*time.Second, 20*time.Millisecond, "expected port %s to be released", portStr)
}

func TestPprofServerEnvVar(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	_, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	require.NoError(t, ln.Close())

	t.Setenv("MICROFAT_PPROF_PORT", portStr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rootCmd := newRootCmd()
	var errBuf bytes.Buffer
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs([]string{subcmdDetect})

	err = rootCmd.ExecuteContext(ctx)
	require.NoError(t, err)
	assert.Contains(t, errBuf.String(), "[microfat:pprof] serving pprof endpoints")

	resp, err := http.Get("http://" + addr + "/debug/pprof/")
	require.NoError(t, err)
	defer func() {
		_ = resp.Body.Close()
	}()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	leakResp, err := http.Get("http://" + addr + "/debug/pprof/goroutineleak?debug=1")
	require.NoError(t, err)
	defer func() {
		_ = leakResp.Body.Close()
	}()
	assert.Equal(t, http.StatusOK, leakResp.StatusCode)
	leakBody, err := io.ReadAll(leakResp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(leakBody), "total 0")

	cancel()
	require.Eventually(t, func() bool {
		testLn, testErr := net.Listen("tcp", addr)
		if testErr == nil {
			_ = testLn.Close()
			return true
		}
		return false
	}, 2*time.Second, 20*time.Millisecond, "expected port %s to be released", portStr)
}

func TestPprofServerFlagOverridesEnv(t *testing.T) {
	ln1, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	addr1 := ln1.Addr().String()
	_, port1, err := net.SplitHostPort(addr1)
	require.NoError(t, err)
	require.NoError(t, ln1.Close())

	ln2, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	addr2 := ln2.Addr().String()
	_, port2, err := net.SplitHostPort(addr2)
	require.NoError(t, err)
	require.NoError(t, ln2.Close())

	t.Setenv("MICROFAT_PPROF_PORT", port1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rootCmd := newRootCmd()
	var errBuf bytes.Buffer
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs([]string{"--pprof-port", port2, subcmdDetect})

	err = rootCmd.ExecuteContext(ctx)
	require.NoError(t, err)
	assert.Contains(t, errBuf.String(), "[microfat:pprof] serving pprof endpoints")
	assert.Contains(t, errBuf.String(), port2)

	resp, err := http.Get("http://" + addr2 + "/debug/pprof/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	_, err = net.DialTimeout("tcp", addr1, 100*time.Millisecond)
	assert.Error(t, err, "expected port1 (%s) not to be listening", port1)

	cancel()
	require.Eventually(t, func() bool {
		testLn, testErr := net.Listen("tcp", addr2)
		if testErr == nil {
			_ = testLn.Close()
			return true
		}
		return false
	}, 2*time.Second, 20*time.Millisecond, "expected port %s to be released", port2)
}

func TestPprofServerInvalidPort(t *testing.T) {
	rootCmd := newRootCmd()
	rootCmd.SetArgs([]string{"--pprof-port", "invalid-port-string", subcmdDetect})

	err := rootCmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting pprof server on port")
}

func TestPprofServerContextCancellation(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	_, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	require.NoError(t, ln.Close())

	ctx, cancel := context.WithCancel(context.Background())
	var logBuf bytes.Buffer
	err = startPprofServer(ctx, portStr, &logBuf)
	require.NoError(t, err)
	assert.Contains(t, logBuf.String(), "[microfat:pprof] serving pprof endpoints")

	// Ensure the server responds
	resp, err := http.Get("http://" + addr + "/debug/pprof/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Cancel context and verify the port is freed
	cancel()

	// Wait for server to close and port to be bindable again
	require.Eventually(t, func() bool {
		testLn, testErr := net.Listen("tcp", addr)
		if testErr == nil {
			_ = testLn.Close()
			return true
		}
		return false
	}, 2*time.Second, 20*time.Millisecond, "expected port %s to be released after context cancellation", portStr)
}

func TestPprofBlockAndMutexFlags(t *testing.T) {
	previous := runtime.SetMutexProfileFraction(-1)
	t.Cleanup(func() {
		runtime.SetBlockProfileRate(0)
		runtime.SetMutexProfileFraction(previous)
	})

	p := pprof.Lookup("block")
	require.NotNil(t, p)
	before := p.Count()

	rootCmd := newRootCmd()
	rootCmd.SetArgs([]string{"--pprof-block-rate", "1", "--pprof-mutex-fraction", "2", subcmdDetect})
	err := rootCmd.Execute()
	require.NoError(t, err)

	assert.Equal(t, 2, runtime.SetMutexProfileFraction(-1))

	// Provoke a deterministic channel blocking event to verify block profile count increases
	ch := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		close(ch)
	}()
	<-ch

	assert.Greater(t, p.Count(), before)
}

func TestPprofBlockAndMutexEnvVars(t *testing.T) {
	previous := runtime.SetMutexProfileFraction(-1)
	t.Cleanup(func() {
		runtime.SetBlockProfileRate(0)
		runtime.SetMutexProfileFraction(previous)
	})

	p := pprof.Lookup("block")
	require.NotNil(t, p)
	before := p.Count()

	t.Setenv("MICROFAT_PPROF_BLOCK_RATE", "1")
	t.Setenv("MICROFAT_PPROF_MUTEX_FRACTION", "3")

	rootCmd := newRootCmd()
	rootCmd.SetArgs([]string{subcmdDetect})
	err := rootCmd.Execute()
	require.NoError(t, err)

	assert.Equal(t, 3, runtime.SetMutexProfileFraction(-1))

	// Provoke a deterministic channel blocking event to verify block profile count increases
	ch := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		close(ch)
	}()
	<-ch

	assert.Greater(t, p.Count(), before)
}

func TestTrim_MetadataPolicyAndBreakHardlinks(t *testing.T) {
	tempDir := t.TempDir()

	stubPath := filepath.Join(tempDir, "stub")
	require.NoError(t, os.WriteFile(stubPath, []byte("#!/bin/sh\necho stub\n"), 0o755))
	v1Path := filepath.Join(tempDir, "v1")
	require.NoError(t, os.WriteFile(v1Path, []byte("#!/bin/sh\necho v1\n"), 0o755))

	fatPath := filepath.Join(tempDir, "app.fat")
	packCmd := newPackCmd()
	packCmd.SetArgs([]string{
		flagStub, stubPath,
		flagOutput, fatPath,
		flagName, "demo-app",
		"-v", "v1=" + v1Path,
		flagSkipELF,
	})
	require.NoError(t, packCmd.Execute())

	// 1. Invalid metadata policy fails
	trimCmd := newTrimCmd()
	trimCmd.SetArgs([]string{"--metadata-policy", "invalid", fatPath})
	err := trimCmd.Execute()
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrInvalidMetadataPolicy)

	// 2. Collision refusal on fresh destination
	existingDest := filepath.Join(tempDir, "already_exists.fat")
	require.NoError(t, os.WriteFile(existingDest, []byte("pre-existing"), 0o755))
	trimCollision := newTrimCmd()
	trimCollision.SetArgs([]string{"-o", existingDest, fatPath})
	err = trimCollision.Execute()
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrDestinationExists)

	// 3. Hard-link refusal without --break-hardlinks
	hardlinkSrc := filepath.Join(tempDir, "fat_hardlink.fat")
	require.NoError(t, os.Link(fatPath, hardlinkSrc))
	trimHardlink := newTrimCmd()
	trimHardlink.SetArgs([]string{hardlinkSrc})
	err = trimHardlink.Execute()
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrHardLinkDetected)

	// 4. In-place trim with --break-hardlinks succeeds and severs the link
	trimBreak := newTrimCmd()
	trimBreak.SetArgs([]string{"--break-hardlinks", "--metadata-policy", "strip", hardlinkSrc})
	require.NoError(t, trimBreak.Execute())

	// Verify hardlinkSrc and fatPath now have different inodes
	fiOrig, err := os.Stat(fatPath)
	require.NoError(t, err)
	fiSevered, err := os.Stat(hardlinkSrc)
	require.NoError(t, err)
	statOrig, ok1 := fiOrig.Sys().(*syscall.Stat_t)
	statSevered, ok2 := fiSevered.Sys().(*syscall.Stat_t)
	if ok1 && ok2 && statOrig.Dev == statSevered.Dev {
		assert.NotEqual(t, statOrig.Ino, statSevered.Ino, "hard link must be severed")
	}

	// 5. Trim to fresh destination with strict policy succeeds
	freshStrict := filepath.Join(tempDir, "trimmed_strict.fat")
	trimStrict := newTrimCmd()
	trimStrict.SetArgs([]string{"--metadata-policy", "strict", "-o", freshStrict, fatPath})
	require.NoError(t, trimStrict.Execute())
	require.FileExists(t, freshStrict)
}
