package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

const (
	flagJSON     = "--json"
	flagLevel    = "--level"
	flagCacheDir = "--cache-dir"
	flagOutput   = "--output"
	flagSkipELF  = "--skip-elf-validation"
	flagManifest = "--manifest"
	flagVerify   = "--verify"

	testOSLinux   = "linux"
	testArchAMD64 = "amd64"
	testArchARM64 = "arm64"
)

func TestRootCmdAndSubcommands(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Test Root Command
	root := newRootCmd()
	if root == nil {
		t.Fatalf("expected non-nil root command")
	}

	rootVersion := newRootCmd()
	rootVersion.SetArgs([]string{"--version"})
	if err := rootVersion.Execute(); err != nil {
		t.Fatalf("root --version failed: %v", err)
	}

	rootHelp := newRootCmd()
	rootHelp.SetArgs([]string{})
	if err := rootHelp.Execute(); err != nil {
		t.Fatalf("root default execution failed: %v", err)
	}

	// 2. Test Detect Command
	detectText := newDetectCmd()
	detectText.SetArgs([]string{})
	var detectBuf bytes.Buffer
	detectText.SetOut(&detectBuf)
	if err := detectText.Execute(); err != nil {
		t.Fatalf("detect command failed: %v", err)
	}

	detectJSON := newDetectCmd()
	detectJSON.SetArgs([]string{flagJSON})
	var jsonBuf bytes.Buffer
	detectJSON.SetOut(&jsonBuf)
	if err := detectJSON.Execute(); err != nil {
		t.Fatalf("detect --json command failed: %v", err)
	}

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
		"--stub", stubPath,
		flagOutput, fatPath,
		"--name", "demo-app",
		"-v", "v1=" + v1Path,
		"-v", "v3=" + v3Path,
		flagSkipELF,
	})
	if err := packCmd.Execute(); err != nil {
		t.Fatalf("pack command failed: %v", err)
	}

	// 4. Test Inspect Command
	inspectText := newInspectCmd()
	inspectText.SetArgs([]string{fatPath})
	if err := inspectText.Execute(); err != nil {
		t.Fatalf("inspect command failed: %v", err)
	}

	inspectJSON := newInspectCmd()
	inspectJSON.SetArgs([]string{flagJSON, fatPath})
	if err := inspectJSON.Execute(); err != nil {
		t.Fatalf("inspect --json command failed: %v", err)
	}

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

	// 5. Test Verify Command
	verifyText := newVerifyCmd()
	verifyText.SetArgs([]string{fatPath})
	if err := verifyText.Execute(); err != nil {
		t.Fatalf("verify command failed: %v", err)
	}

	verifyJSON := newVerifyCmd()
	verifyJSON.SetArgs([]string{flagJSON, fatPath})
	if err := verifyJSON.Execute(); err != nil {
		t.Fatalf("verify --json command failed: %v", err)
	}

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
	if err := trimCmd.Execute(); err != nil {
		t.Fatalf("trim command failed: %v", err)
	}

	// Test Trim in-place with auto-detected level
	fatForInPlaceTrim := filepath.Join(tempDir, "fat_for_inplace.fat")
	dataFat, _ := os.ReadFile(fatPath)
	_ = os.WriteFile(fatForInPlaceTrim, dataFat, 0o755)
	trimInPlaceCmd := newTrimCmd()
	trimInPlaceCmd.SetArgs([]string{fatForInPlaceTrim})
	if err := trimInPlaceCmd.Execute(); err != nil {
		t.Fatalf("trim in-place command failed: %v", err)
	}

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
		"--stub", stubPath,
		flagOutput, filepath.Join(tempDir, "out"),
		"-v", "invalid_no_equal",
	})
	if err := packInvalidSpec.Execute(); err == nil || !strings.Contains(err.Error(), "invalid variant specification") {
		t.Errorf("expected invalid variant specification error, got: %v", err)
	}

	packDup := newPackCmd()
	packDup.SetArgs([]string{
		"--stub", stubPath,
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
	verifyJSONCmd.SetArgs([]string{"--json", fatPath})
	if err := verifyJSONCmd.Execute(); err != nil {
		t.Fatalf("unexpected error running verify --json: %v", err)
	}
}

func TestMainInvocation(t *testing.T) {
	oldArgs := os.Args
	oldExit := exitFunc
	defer func() {
		os.Args = oldArgs
		exitFunc = oldExit
	}()

	os.Args = []string{"microfat", "--help"}
	main()

	exitCalled := false
	exitFunc = func(code int) {
		exitCalled = true
	}
	os.Args = []string{"microfat", "invalid-subcommand-name"}
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
		"--stub", stubPath,
		flagOutput, fatPath,
		"--name", "arm64-cli-app",
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
	_ = os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module samplepkg\ngo 1.27.0\n"), 0o644)

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


