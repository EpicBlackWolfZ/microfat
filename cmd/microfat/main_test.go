package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/pack"
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
	detectJSON.SetArgs([]string{"--json"})
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
		"--output", fatPath,
		"--name", "demo-app",
		"-v", "v1=" + v1Path,
		"-v", "v3=" + v3Path,
		"--skip-elf-validation",
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
	inspectJSON.SetArgs([]string{"--json", fatPath})
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
	verifyJSON.SetArgs([]string{"--json", fatPath})
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
	trimCmd.SetArgs([]string{"--level", "v1", "-o", trimmedPath, fatPath})
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
	trimInvalidLevel.SetArgs([]string{"--level", "v99", fatPath})
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
		"--output", filepath.Join(tempDir, "out"),
		"-v", "invalid_no_equal",
	})
	if err := packInvalidSpec.Execute(); err == nil || !strings.Contains(err.Error(), "invalid variant specification") {
		t.Errorf("expected invalid variant specification error, got: %v", err)
	}

	packDup := newPackCmd()
	packDup.SetArgs([]string{
		"--stub", stubPath,
		"--output", filepath.Join(tempDir, "out"),
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
