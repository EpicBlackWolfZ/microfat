package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemfdAndCacheFallback(t *testing.T) {
	t.Parallel()

	t.Run("Scenario18_CleanInMemoryMemfdExecution", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "memfd_cache_empty")
		if err := os.MkdirAll(cacheDir, defaultFilePerm); err != nil {
			t.Fatalf("mkdir cache: %v", err)
		}

		env := []string{
			"MICROFAT_CACHE_DIR=" + cacheDir,
			envDebugTrue,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}

		if !strings.Contains(stderr, "exec_mode=memfd") {
			t.Fatalf("expected exec_mode=memfd in stderr telemetry, got:\n%s", stderr)
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)

		// Assert cache directory remained completely untouched
		entries, err := os.ReadDir(cacheDir)
		if err != nil {
			t.Fatalf("read cache dir: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("expected cache directory to remain empty during memfd execution, found %d entries", len(entries))
		}
	})

	t.Run("Scenario19_ExplicitCacheMode", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "explicit_cache")
		env := []string{
			envExecCache,
			"MICROFAT_CACHE_DIR=" + cacheDir,
			envDebugTrue,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}

		if !strings.Contains(stderr, "exec_mode=cache") {
			t.Fatalf("expected exec_mode=cache in stderr telemetry, got:\n%s", stderr)
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)

		// Assert cache binary was written
		entries, err := os.ReadDir(cacheDir)
		if err != nil {
			t.Fatalf("read cache dir: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected exactly 1 cached binary in %s, found %d", cacheDir, len(entries))
		}
	})

	t.Run("Scenario20_KernelRestrictedMemfdFallbackViaSeccomp", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "seccomp_fallback_cache")
		env := []string{
			"MICROFAT_CACHE_DIR=" + cacheDir,
			envDebugTrue,
		}

		stdout, stderr, exitCode, err := executeWithSeccompBlockedMemfd(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution under seccomp runner failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}

		// Verify launcher caught memfd failure and logged fallback
		if !strings.Contains(stderr, "memfd_create is unsupported") && !strings.Contains(stderr, "memfd_create") {
			t.Fatalf("expected memfd fallback telemetry log in stderr, got:\n%s", stderr)
		}

		// Verify execution proceeded via cache mode
		if !strings.Contains(stderr, "exec_mode=cache") {
			t.Fatalf("expected exec_mode=cache in stderr telemetry, got:\n%s", stderr)
		}

		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)

		// Verify cached binary was populated
		entries, err := os.ReadDir(cacheDir)
		if err != nil {
			t.Fatalf("read cache dir: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected exactly 1 cached file after seccomp fallback, found %d", len(entries))
		}
	})
}
