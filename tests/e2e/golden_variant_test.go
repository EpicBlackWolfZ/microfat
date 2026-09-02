package e2e_test

import (
	"strings"
	"testing"
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
		if currentHostArch == archAMD64 && currentHostLevel == "v4" {
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
		if strings.Contains(stdout, "golden:variant=") {
			t.Fatalf("expected child application to not execute for incompatible forced level, got:\n%s", stdout)
		}
		if !strings.Contains(stderr, "incompatible") && !strings.Contains(stderr, "unknown forced level") {
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
		if !hostSupportsAtLeastTier2() {
			t.Skipf("host level %s is baseline tier, skipping max level v2 test", currentHostLevel)
		}

		targetCap := "v2"
		if currentHostArch == archARM64 {
			targetCap = "v8.2"
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
		if !hostSupportsAtLeastTier2() {
			t.Skipf("host level %s is baseline tier, skipping fallback v2 test", currentHostLevel)
		}

		targetFallback := "v2"
		disabledVal := "v3,v4"
		if currentHostArch == archARM64 {
			targetFallback = "v8.2"
			disabledVal = "v9.0"
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
		if currentHostArch == archARM64 {
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
		if currentHostArch == archARM64 {
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
		if strings.Contains(stdout, "golden:variant=") {
			t.Fatalf("expected child application to not execute when all variants are disabled, got:\n%s", stdout)
		}
		if !strings.Contains(stderr, "no compatible microarchitecture variant found") {
			t.Fatalf("expected 'no compatible microarchitecture variant found' in stderr, got:\n%s", stderr)
		}
	})
}

// hostSupportsAtLeastTier2 returns true if the host CPU microarchitecture meets or exceeds
// the second capability tier (>= v2 on amd64 or >= v8.2 on arm64). Hosts with only baseline
// tiers (v1 / v8.0) cannot test tier-2 capping or fallback and are skipped.
//
// Invariant Note: This check avoids duplicating the internal architecture ranking model
// in the test harness by testing whether the detected host level is strictly beyond the
// baseline tier. If new architecture families or baseline levels are introduced, ensure their
// baseline identifiers are reflected here.
func hostSupportsAtLeastTier2() bool {
	return currentHostLevel != "v1" && currentHostLevel != "v8.0"
}
