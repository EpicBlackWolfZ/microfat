package e2e_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

func TestLifecycleReleaseSmoke(t *testing.T) {
	t.Parallel()

	t.Run("Scenario37_FullUserLifecycleReleaseSmokeTest", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		fatPath := filepath.Join(tempDir, "lifecycle.fat")

		// 1. Pack
		if err := packBinary(cliPath, stubPath, "lifecycle-app", fatPath, goldenVariantBins); err != nil {
			t.Fatalf("microfat pack failed: %v", err)
		}

		// 2. Inspect CLI command
		inspectCmd := exec.Command(cliPath, "inspect", fatPath)
		var inspectBuf bytes.Buffer
		inspectCmd.Stdout = &inspectBuf
		if err := inspectCmd.Run(); err != nil {
			t.Fatalf("microfat inspect failed: %v", err)
		}
		inspectOut := inspectBuf.String()
		if !strings.Contains(inspectOut, "App Name:          lifecycle-app") || !strings.Contains(inspectOut, currentHostLevel) {
			t.Fatalf("unexpected inspect output:\n%s", inspectOut)
		}

		// 3. Verify CLI command
		verifyCmd := exec.Command(cliPath, "verify", fatPath)
		var verifyBuf bytes.Buffer
		verifyCmd.Stdout = &verifyBuf
		verifyCmd.Stderr = &verifyBuf
		if err := verifyCmd.Run(); err != nil {
			t.Fatalf("microfat verify failed: %v\noutput: %s", err, verifyBuf.String())
		}

		// 4. Transparent execution
		stdout, stderr, exitCode, err := executeFatBinary(t, fatPath, []string{envDebugTrue})
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("executing lifecycle fat binary failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
	})

	t.Run("Scenario38_LauncherInfoMetaCommand", func(t *testing.T) {
		t.Parallel()
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, nil, "--microfat:info")
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("--microfat:info failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}

		// Metadata must be presented without launching the payload application
		if strings.Contains(stdout, "golden:variant=") {
			t.Fatalf("--microfat:info must not launch child application, found output: %s", stdout)
		}
		if !strings.Contains(stdout, "App Name:") && !strings.Contains(stdout, "Selected Variant:") {
			t.Fatalf("expected launcher metadata in stdout, got:\n%s", stdout)
		}
	})

	t.Run("Scenario39_LauncherPrewarmMetaCommand", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "prewarm_cache")
		env := []string{
			"MICROFAT_CACHE_DIR=" + cacheDir,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env, "--microfat:prewarm")
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("--microfat:prewarm failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}

		// Payload must not be executed during prewarm
		if strings.Contains(stdout, "golden:variant=") {
			t.Fatalf("--microfat:prewarm must not launch child application, found: %s", stdout)
		}

		// Cache file must be populated
		entries, err := os.ReadDir(cacheDir)
		if err != nil || len(entries) != 1 {
			t.Fatalf("expected 1 prewarmed cached file in %s, found %d", cacheDir, len(entries))
		}
	})

	t.Run("Scenario40_LauncherTrimToMetaCommand", func(t *testing.T) {
		t.Parallel()
		trimmedPath := filepath.Join(t.TempDir(), "trimmed.fat")
		_, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, nil, "--microfat:trim-to="+trimmedPath)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("--microfat:trim-to failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}

		trimmedStat, err := os.Stat(trimmedPath)
		if err != nil {
			t.Fatalf("stat trimmed binary: %v", err)
		}
		originalStat, err := os.Stat(goldenFatBin)
		if err != nil {
			t.Fatalf("stat original fat binary: %v", err)
		}

		if trimmedStat.Size() >= originalStat.Size() {
			t.Fatalf("expected trimmed size (%d) to be smaller than fat binary (%d)", trimmedStat.Size(), originalStat.Size())
		}

		// Trimmed binary must execute cleanly and retain self-dispatching capability
		trimStdout, trimStderr, trimExitCode, trimErr := executeFatBinary(t, trimmedPath, []string{envDebugTrue})
		if trimErr != nil || trimExitCode != defaultExitCode {
			t.Fatalf("executing trimmed binary failed (code %d): %v\nstderr: %s", trimExitCode, trimErr, trimStderr)
		}
		if !strings.Contains(trimStdout, "golden:variant="+currentHostLevel) {
			t.Fatalf("unexpected output from trimmed binary:\n%s", trimStdout)
		}
	})

	t.Run("Scenario41_LauncherOptimizeToMetaCommand", func(t *testing.T) {
		t.Parallel()
		matPath := filepath.Join(t.TempDir(), "materialized_app")
		_, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, nil, "--microfat:optimize-to="+matPath)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("--microfat:optimize-to failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}

		matStat, err := os.Stat(matPath)
		if err != nil {
			t.Fatalf("stat materialized binary: %v", err)
		}
		if matStat.Size() == 0 {
			t.Fatalf("materialized binary is empty")
		}

		// Materialized binary must be a standalone ELF executable, NOT a fat binary with trailer
		matFile, err := os.Open(matPath)
		if err != nil {
			t.Fatalf("open materialized binary: %v", err)
		}
		defer func() { _ = matFile.Close() }()

		if format.IsFatBinary(matFile, matStat.Size()) {
			t.Fatalf("expected materialized binary to be standalone ELF, but has fat binary trailer")
		}
		_ = matFile.Close()

		// Execute materialized standalone ELF directly
		matStdout, matStderr, matExitCode, matErr := executeFatBinary(t, matPath, nil)
		if matErr != nil || matExitCode != defaultExitCode {
			t.Fatalf("executing materialized binary failed (code %d): %v\nstderr: %s", matExitCode, matErr, matStderr)
		}
		if !strings.Contains(matStdout, "golden:variant="+currentHostLevel) {
			t.Fatalf("unexpected output from materialized binary:\n%s", matStdout)
		}
	})
}
