package e2e_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

//revive:disable-next-line:cyclomatic Keep the ordered release lifecycle and launcher meta-command outcomes visible together.
func TestLifecycleReleaseSmoke(t *testing.T) {
	t.Parallel()

	t.Run("Scenario37_FullUserLifecycleReleaseSmokeTest", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		fatPath := filepath.Join(tempDir, "lifecycle.fat")

		// 1. Pack
		require.NoError(t, packBinary(cliPath, stubPath, "lifecycle-app", fatPath, goldenVariantBins), "microfat pack failed")

		// 2. Inspect CLI command
		inspectCmd := exec.Command(cliPath, "inspect", fatPath)
		var inspectBuf bytes.Buffer
		inspectCmd.Stdout = &inspectBuf
		require.NoError(t, inspectCmd.Run(), "microfat inspect failed")
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
		require.NoError(t, err, "stat trimmed binary")
		originalStat, err := os.Stat(goldenFatBin)
		require.NoError(t, err, "stat original fat binary")

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
		require.NoError(t, err, "stat materialized binary")
		if matStat.Size() == 0 {
			t.Fatalf("materialized binary is empty")
		}

		// Materialized binary must be a standalone ELF executable, rejected by microfat inspect CLI
		inspectCmd := exec.Command(cliPath, "inspect", matPath)
		if err := inspectCmd.Run(); err == nil {
			t.Fatalf("expected microfat inspect to reject standalone materialized binary, but it succeeded")
		}

		// Execute materialized standalone ELF directly
		matStdout, matStderr, matExitCode, matErr := executeFatBinary(t, matPath, nil)
		if matErr != nil || matExitCode != defaultExitCode {
			t.Fatalf("executing materialized binary failed (code %d): %v\nstderr: %s", matExitCode, matErr, matStderr)
		}
		if !strings.Contains(matStdout, "golden:variant="+currentHostLevel) {
			t.Fatalf("unexpected output from materialized binary:\n%s", matStdout)
		}
	})

	t.Run("Scenario42_TransformationSafety_CollisionAndHardlink", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()

		// 1. Pre-existing destination collision on --microfat:trim-to
		existingDest := filepath.Join(tempDir, "existing.fat")
		require.NoError(t, os.WriteFile(existingDest, []byte("pre-existing content"), 0o755))

		_, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, nil, "--microfat:trim-to="+existingDest)
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected collision failure for --microfat:trim-to, but it succeeded")
		}
		if !strings.Contains(stderr, "destination already exists") {
			t.Fatalf("expected 'destination already exists' error message in stderr, got: %s", stderr)
		}

		// 2. Hardlink refusal on in-place trim
		copyFat := filepath.Join(tempDir, "copy.fat")
		fatData, err := os.ReadFile(goldenFatBin)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(copyFat, fatData, 0o755))

		hardlinkFat := filepath.Join(tempDir, "copy_link.fat")
		require.NoError(t, os.Link(copyFat, hardlinkFat))

		_, stderr, exitCode, err = executeFatBinary(t, hardlinkFat, nil, "--microfat:trim")
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected hard-link refusal for in-place trim without --break-hardlinks, but it succeeded")
		}
		if !strings.Contains(stderr, "multi-link file detected") {
			t.Fatalf("expected 'multi-link file detected' error in stderr, got: %s", stderr)
		}

		// 3. In-place trim with --microfat:break-hardlinks succeeds and severs the link
		_, stderr, exitCode, err = executeFatBinary(t, hardlinkFat, nil,
			"--microfat:trim", "--microfat:break-hardlinks", "--microfat:metadata-policy=strip")
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("in-place trim with --break-hardlinks failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}

		fiOrig, err := os.Stat(copyFat)
		require.NoError(t, err)
		fiSevered, err := os.Stat(hardlinkFat)
		require.NoError(t, err)
		statOrig, ok1 := fiOrig.Sys().(*syscall.Stat_t)
		statSevered, ok2 := fiSevered.Sys().(*syscall.Stat_t)
		if ok1 && ok2 && statOrig.Dev == statSevered.Dev {
			if statOrig.Ino == statSevered.Ino {
				t.Fatalf("expected hard link to be severed, but both still share inode %d", statOrig.Ino)
			}
		}
	})

	t.Run("Scenario43_TransformationSafety_EmptyDestinationRejection", func(t *testing.T) {
		t.Parallel()

		goldenData, err := os.ReadFile(goldenFatBin)
		require.NoError(t, err)

		flagsToTest := [][]string{
			{"--microfat:optimize-to="},
			{"--microfat:optimize-to", ""},
			{"--microfat:trim-to="},
			{"--microfat:trim-to", ""},
		}

		for _, flagArgs := range flagsToTest {
			flagDesc := strings.Join(flagArgs, " ")
			t.Run(flagDesc, func(t *testing.T) {
				t.Parallel()
				subDir := t.TempDir()
				copyFat := filepath.Join(subDir, "copy.fat")
				require.NoError(t, os.WriteFile(copyFat, goldenData, 0o755))

				fiBefore, err := os.Stat(copyFat)
				require.NoError(t, err)
				statBefore, okBefore := fiBefore.Sys().(*syscall.Stat_t)

				_, stderr, exitCode, err := executeFatBinary(t, copyFat, nil, flagArgs...)
				if err == nil && exitCode == defaultExitCode {
					t.Fatalf("expected empty destination to fail for args %v, but succeeded", flagArgs)
				}
				if !strings.Contains(stderr, "requires a destination path") {
					t.Fatalf("expected 'requires a destination path' in stderr for args %v, got: %s", flagArgs, stderr)
				}

				fiAfter, err := os.Stat(copyFat)
				require.NoError(t, err)
				statAfter, okAfter := fiAfter.Sys().(*syscall.Stat_t)

				if fiBefore.Size() != fiAfter.Size() {
					t.Fatalf("file size changed from %d to %d for args %v", fiBefore.Size(), fiAfter.Size(), flagArgs)
				}
				if okBefore && okAfter {
					if statBefore.Ino != statAfter.Ino {
						t.Fatalf("inode changed from %d to %d for args %v", statBefore.Ino, statAfter.Ino, flagArgs)
					}
				}

				afterData, err := os.ReadFile(copyFat)
				require.NoError(t, err)
				if !bytes.Equal(goldenData, afterData) {
					t.Fatalf("binary bytes modified in-place despite empty destination error for args %v", flagArgs)
				}
			})
		}

		// Also verify CLI rejects empty destination flag (-o "" or --output="")
		cliSubDir := t.TempDir()
		cliCopyFat := filepath.Join(cliSubDir, "cli_copy.fat")
		require.NoError(t, os.WriteFile(cliCopyFat, goldenData, 0o755))

		fiBefore, err := os.Stat(cliCopyFat)
		require.NoError(t, err)
		statBefore, okBefore := fiBefore.Sys().(*syscall.Stat_t)

		for _, optFlags := range [][]string{{"-o="}, {"--output="}, {"-o", ""}, {"--output", ""}} {
			cmdArgs := append([]string{"trim", cliCopyFat}, optFlags...)
			cmd := exec.Command(cliPath, cmdArgs...)
			var errBuf bytes.Buffer
			cmd.Stderr = &errBuf
			err := cmd.Run()
			flagDesc := strings.Join(optFlags, " ")
			if err == nil {
				t.Fatalf("expected microfat trim %s to fail, but succeeded", flagDesc)
			}
			if !strings.Contains(errBuf.String(), "destination output path cannot be empty") {
				t.Fatalf("expected 'destination output path cannot be empty' in stderr for %s, got: %s", flagDesc, errBuf.String())
			}

			fiAfter, err := os.Stat(cliCopyFat)
			require.NoError(t, err)
			statAfter, okAfter := fiAfter.Sys().(*syscall.Stat_t)

			if fiBefore.Size() != fiAfter.Size() {
				t.Fatalf("file size changed from %d to %d for %s", fiBefore.Size(), fiAfter.Size(), flagDesc)
			}
			if okBefore && okAfter && statBefore.Ino != statAfter.Ino {
				t.Fatalf("inode changed from %d to %d for %s", statBefore.Ino, statAfter.Ino, flagDesc)
			}

			afterData, err := os.ReadFile(cliCopyFat)
			require.NoError(t, err)
			if !bytes.Equal(goldenData, afterData) {
				t.Fatalf("binary modified in-place despite empty %s flag", flagDesc)
			}
		}
	})
}
