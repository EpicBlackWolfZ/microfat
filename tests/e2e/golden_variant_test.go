package e2e_test

import (
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

func TestGoldenVariantSelection_HostDependent(t *testing.T) {
	t.Parallel()

	t.Run("Scenario1_BaselineHostSelection", func(t *testing.T) {
		t.Parallel()
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, []string{envDebugTrue})
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
	})

	t.Run("Scenario2_AVX512DownclockProtection", func(t *testing.T) {
		t.Parallel()
		env := []string{
			"MICROFAT_POLICY=safe_avx512",
			envDebugTrue,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		if currentHostArch == microarch.ArchAMD64 && currentHostLevel == "v4" {
			if strings.Contains(stderr, "avx512_downclock_protection") {
				assertSelectedMatchesExecuted(t, stdout, stderr, "v3")
				return
			}
		}
		// On non-downclocking hardware, standard optimal dispatch occurs
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
	})
}

func TestGoldenVariantSelection_Deterministic(t *testing.T) {
	t.Parallel()

	t.Run("Scenario3_ForcedLevelV1", func(t *testing.T) {
		t.Parallel()
		env := []string{
			"MICROFAT_FORCE_LEVEL=v1",
			envDebugTrue,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, "v1")
	})

	t.Run("Scenario4_ForcedLevelIncompatible", func(t *testing.T) {
		t.Parallel()
		env := []string{
			"MICROFAT_FORCE_LEVEL=v99",
			envDebugTrue,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected failure for forced level v99, but succeeded\nstdout: %s\nstderr: %s", stdout, stderr)
		}
		if !strings.Contains(stderr, "incompatible") && !strings.Contains(stderr, "unknown") && !strings.Contains(stderr, "error") {
			t.Fatalf("expected error diagnostics for forced level v99, got: %s", stderr)
		}
	})

	t.Run("Scenario5_MaxLevelCapV1", func(t *testing.T) {
		t.Parallel()
		env := []string{
			"MICROFAT_MAX_LEVEL=v1",
			envDebugTrue,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, "v1")
	})

	t.Run("Scenario6_MaxLevelCapV2", func(t *testing.T) {
		t.Parallel()
		targetCap := "v2"
		if currentHostArch == microarch.ArchARM64 {
			targetCap = "v8.2"
		}
		if microarch.Compare(currentHostArch, currentHostLevel, targetCap) < 0 {
			t.Skipf("host level %s does not support >= %s, skipping max level test", currentHostLevel, targetCap)
		}

		env := []string{
			"MICROFAT_MAX_LEVEL=" + targetCap,
			envDebugTrue,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, targetCap)
	})

	t.Run("Scenario7_DisabledVariantsFallbackToV2", func(t *testing.T) {
		t.Parallel()
		targetFallback := "v2"
		disabledVal := "v3,v4"
		if currentHostArch == microarch.ArchARM64 {
			targetFallback = "v8.2"
			disabledVal = "v9.0"
		}
		if microarch.Compare(currentHostArch, currentHostLevel, targetFallback) < 0 {
			t.Skipf("host level %s does not support >= %s, skipping fallback test", currentHostLevel, targetFallback)
		}

		env := []string{
			"MICROFAT_DISABLE_VARIANTS=" + disabledVal,
			envDebugTrue,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, targetFallback)
	})

	t.Run("Scenario8_DisabledVariantsFallbackToV1", func(t *testing.T) {
		t.Parallel()
		targetFallback := "v1"
		disabledVal := "v2,v3,v4"
		if currentHostArch == microarch.ArchARM64 {
			targetFallback = "v8.0"
			disabledVal = "v8.2,v9.0"
		}

		env := []string{
			"MICROFAT_DISABLE_VARIANTS=" + disabledVal,
			envDebugTrue,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, targetFallback)
	})

	t.Run("Scenario9_AllVariantsDisabled", func(t *testing.T) {
		t.Parallel()
		disabledVal := "v1,v2,v3,v4"
		if currentHostArch == microarch.ArchARM64 {
			disabledVal = "v8.0,v8.2,v9.0"
		}

		env := []string{
			"MICROFAT_DISABLE_VARIANTS=" + disabledVal,
			envDebugTrue,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected error when all variants are disabled, got success\nstdout: %s", stdout)
		}
		if !strings.Contains(stderr, "no compatible") && !strings.Contains(stderr, "error") {
			t.Fatalf("expected error message indicating no compatible variant, got:\n%s", stderr)
		}
	})
}
