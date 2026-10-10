package e2e_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStubProfile_DirectPack_AutoDiscoveryAndExecution(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	binDir := filepath.Join(testDir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	// Copy microfat CLI to binDir so sibling discovery can find companion stubs in binDir
	siblingCLI := filepath.Join(binDir, "microfat")
	copyFile(t, cliPath, siblingCLI)

	// Compile full stub to binDir/microfat-stub
	fullStubPath := filepath.Join(binDir, "microfat-stub")
	require.NoError(t, compileBinary(stubPackagePath, fullStubPath, nil))

	// Compile minimal stub to binDir/microfat-stub-minimal (-tags minimal)
	minStubPath := filepath.Join(binDir, "microfat-stub-minimal")
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, minStubPath, nil, "-tags=minimal", "-ldflags=-s -w"))

	t.Run("DirectPack_MinimalProfile_AutoDiscoversSiblingMinimalStub", func(t *testing.T) {
		fatOut := filepath.Join(testDir, "app_minimal.fat")
		cmd := exec.Command(siblingCLI,
			"pack",
			"--arch", currentHostArch,
			"--stub-profile", "minimal",
			"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
			"-o", fatOut,
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "direct pack with minimal profile failed: %s", string(out))
		assert.Contains(t, string(out), "microfat-stub-minimal")

		// Verify binary integrity
		verifyFatIntegrity(t, fatOut)

		// Execute with --microfat-info: minimal stub must explicitly reject meta-commands
		infoCmd := exec.Command(fatOut, "--microfat:info")
		infoOut, infoErr := infoCmd.CombinedOutput()
		require.Error(t, infoErr, "minimal stub should reject meta-commands")
		assert.Contains(t, string(infoOut), "meta-commands are disabled in minimal launcher stub profile")

		// Normal execution without meta-commands must succeed
		runCmd := exec.Command(fatOut)
		runOut, runErr := runCmd.CombinedOutput()
		require.NoError(t, runErr, "execution failed: %s", string(runOut))
		assert.Contains(t, string(runOut), "golden:variant=")
	})

	t.Run("DirectPack_FullProfile_AutoDiscoversSiblingFullStub", func(t *testing.T) {
		fatOut := filepath.Join(testDir, "app_full.fat")
		cmd := exec.Command(siblingCLI,
			"pack",
			"--arch", currentHostArch,
			"--stub-profile", "full",
			"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
			"-o", fatOut,
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "direct pack with full profile failed: %s", string(out))
		assert.Contains(t, string(out), "microfat-stub")

		// Verify binary integrity
		verifyFatIntegrity(t, fatOut)

		// Execute with --microfat-info: full stub must succeed and output json info
		infoCmd := exec.Command(fatOut, "--microfat:info")
		infoOut, infoErr := infoCmd.CombinedOutput()
		require.NoError(t, infoErr, "full stub should support meta-commands: %s", string(infoOut))
		assert.Contains(t, string(infoOut), "=== Microfat Binary Info ===")
	})

	t.Run("DirectPack_DefaultProfile_SelectsFullStub", func(t *testing.T) {
		fatOut := filepath.Join(testDir, "app_default.fat")
		cmd := exec.Command(siblingCLI,
			"pack",
			"--arch", currentHostArch,
			"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
			"-o", fatOut,
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "direct pack with default profile failed: %s", string(out))
		assert.Contains(t, string(out), "microfat-stub")
		assert.NotContains(t, string(out), "microfat-stub-minimal")
	})
}

func TestStubProfile_Conflicts(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	fullStubPath := filepath.Join(testDir, "microfat-stub")
	require.NoError(t, compileBinary(stubPackagePath, fullStubPath, nil))

	minStubPath := filepath.Join(testDir, "microfat-stub-minimal")
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, minStubPath, nil, "-tags=minimal", "-ldflags=-s -w"))

	fatOut := filepath.Join(testDir, "app.fat")

	t.Run("DirectPack_StubFull_With_StubProfileMinimal_WinsWithNotice", func(t *testing.T) {
		cmd := exec.Command(cliPath,
			"pack",
			"--arch", currentHostArch,
			"--stub", fullStubPath,
			"--stub-profile", "minimal",
			"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
			"-o", fatOut,
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "pack failed: %s", string(out))
		assert.Contains(t, string(out), "automatic stub-profile selection was bypassed")

		// Verify explicit full stub won: meta-commands should work
		infoCmd := exec.Command(fatOut, "--microfat:info")
		infoOut, infoErr := infoCmd.CombinedOutput()
		require.NoError(t, infoErr, "info failed: %s", string(infoOut))
		assert.Contains(t, string(infoOut), "Embedded Variants")
	})

	t.Run("DirectPack_StubMinimal_With_StubProfileFull_WinsWithNotice", func(t *testing.T) {
		cmd := exec.Command(cliPath,
			"pack",
			"--arch", currentHostArch,
			"--stub", minStubPath,
			"--stub-profile", "full",
			"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
			"-o", fatOut,
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "pack failed: %s", string(out))
		assert.Contains(t, string(out), "automatic stub-profile selection was bypassed")

		// Verify explicit minimal stub won: meta-commands should be disabled
		infoCmd := exec.Command(fatOut, "--microfat:info")
		infoOut, infoErr := infoCmd.CombinedOutput()
		require.Error(t, infoErr)
		assert.Contains(t, string(infoOut), "meta-commands are disabled in minimal launcher stub profile")
	})

	t.Run("DirectPack_InvalidStubProfile_Fails", func(t *testing.T) {
		cmd := exec.Command(cliPath,
			"pack",
			"--arch", currentHostArch,
			"--stub-profile", "invalid_profile",
			"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
			"-o", fatOut,
		)
		out, err := cmd.CombinedOutput()
		require.Error(t, err)
		assert.Contains(t, string(out), "invalid stub profile")
	})
}

func TestStubProfile_MissingCompanionAssets(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	isolatedBinDir := filepath.Join(testDir, "bin")
	require.NoError(t, os.MkdirAll(isolatedBinDir, 0o755))

	siblingCLI := filepath.Join(isolatedBinDir, "microfat")
	copyFile(t, cliPath, siblingCLI)

	// In isolatedBinDir, do NOT create microfat-stub-minimal
	fatOut := filepath.Join(testDir, "app.fat")

	out, err := runFixtureCommandCombinedOutput(func() *exec.Cmd {
		cmd := exec.Command(siblingCLI,
			"pack",
			"--arch", currentHostArch,
			"--stub-profile", "minimal",
			"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
			"-o", fatOut,
		)
		cmd.Env = []string{"PATH=/nonexistent_empty_path"}
		return cmd
	})
	require.Error(t, err)
	assert.Contains(t, string(out), "launcher stub \"microfat-stub-minimal\" for profile \"minimal\" not found")
}

func TestStubProfile_ManifestPackAndPgoPack(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	binDir := filepath.Join(testDir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	siblingCLI := filepath.Join(binDir, "microfat")
	copyFile(t, cliPath, siblingCLI)

	fullStubPath := filepath.Join(binDir, "microfat-stub")
	require.NoError(t, compileBinary(stubPackagePath, fullStubPath, nil))

	minStubPath := filepath.Join(binDir, "microfat-stub-minimal")
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, minStubPath, nil, "-tags=minimal", "-ldflags=-s -w"))

	// Create test Go package for manifest compilation
	pkgDir := filepath.Join(testDir, "src", "sample")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	mainSource := `package main
import "fmt"
func main() { fmt.Println("SAMPLE_MANIFEST_SUCCESS") }
`
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte(mainSource), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module sample\ngo 1.27.2\n"), 0o644))

	t.Run("ManifestPack_StubProfileMinimal_InManifest", func(t *testing.T) {
		fatOut := filepath.Join(testDir, "manifest_min.fat")
		manifestContent := `name: manifest-min-app
package: ` + pkgDir + `
target_os: linux
target_arch: ` + currentHostArch + `
stub_profile: minimal
variants:
  - level: ` + currentHostLevel + `
`
		manifestPath := filepath.Join(testDir, "manifest_min.yaml")
		require.NoError(t, os.WriteFile(manifestPath, []byte(manifestContent), 0o644))

		cmd := exec.Command(siblingCLI, "pack", "--manifest", manifestPath, "-o", fatOut)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "manifest pack failed: %s", string(out))

		// Verify rejection of meta-commands
		infoCmd := exec.Command(fatOut, "--microfat:info")
		infoOut, infoErr := infoCmd.CombinedOutput()
		require.Error(t, infoErr)
		assert.Contains(t, string(infoOut), "meta-commands are disabled in minimal launcher stub profile")
	})

	t.Run("PgoPack_CLIStubProfileMinimal_OverridesManifest", func(t *testing.T) {
		fatOut := filepath.Join(testDir, "pgo_min.fat")
		manifestContent := `name: pgo-app
package: ` + pkgDir + `
target_os: linux
target_arch: ` + currentHostArch + `
stub_profile: full
variants:
  - level: ` + currentHostLevel + `
`
		manifestPath := filepath.Join(testDir, "manifest_pgo.yaml")
		require.NoError(t, os.WriteFile(manifestPath, []byte(manifestContent), 0o644))

		cmd := exec.Command(siblingCLI, "pgo-pack", "--manifest", manifestPath, "--stub-profile", "minimal", "-o", fatOut)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "pgo-pack failed: %s", string(out))

		// CLI flag --stub-profile minimal overrode manifest full, so meta-commands should be disabled
		infoCmd := exec.Command(fatOut, "--microfat:info")
		infoOut, infoErr := infoCmd.CombinedOutput()
		require.Error(t, infoErr)
		assert.Contains(t, string(infoOut), "meta-commands are disabled in minimal launcher stub profile")
	})

	t.Run("PgoPack_ManifestStubWithCLIProfile_WinsWithNotice", func(t *testing.T) {
		fatOut := filepath.Join(testDir, "pgo_conflict.fat")
		manifestContent := `name: pgo-conflict
package: ` + pkgDir + `
target_os: linux
target_arch: ` + currentHostArch + `
stub: ` + fullStubPath + `
variants:
  - level: ` + currentHostLevel + `
`
		manifestPath := filepath.Join(testDir, "manifest_conflict.yaml")
		require.NoError(t, os.WriteFile(manifestPath, []byte(manifestContent), 0o644))

		cmd := exec.Command(siblingCLI, "pgo-pack", "--manifest", manifestPath, "--stub-profile", "minimal", "-o", fatOut)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "pgo-pack failed: %s", string(out))
		assert.Contains(t, string(out), "automatic stub-profile selection was bypassed")

		// Manifest stub (full) wins over CLI profile: meta-commands should work
		infoCmd := exec.Command(fatOut, "--microfat:info")
		infoOut, infoErr := infoCmd.CombinedOutput()
		require.NoError(t, infoErr, "info failed: %s", string(infoOut))
		assert.Contains(t, string(infoOut), "Embedded Variants")
	})
}

func TestStubProfile_ABICombinedIntegration(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	binDir := filepath.Join(testDir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	siblingCLI := filepath.Join(binDir, "microfat")
	copyFile(t, cliPath, siblingCLI)

	fullStubPath := filepath.Join(binDir, "microfat-stub")
	require.NoError(t, compileBinary(stubPackagePath, fullStubPath, nil))

	minStubPath := filepath.Join(binDir, "microfat-stub-minimal")
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, minStubPath, nil, "-tags=minimal", "-ldflags=-s -w"))

	// Create test Go package for manifest and PGO mixed-ABI tests
	pkgDir := filepath.Join(testDir, "src", "mixedapp")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module mixedapp\ngo 1.27.2\n"), 0o644))
	mainSrc := []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"MIXED_OK\") }\n")
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "main.go"), mainSrc, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "cgo.go"), []byte("//go:build cgo_variant\npackage main\nimport \"C\"\n"), 0o644))

	v1Label, v2Label := hostVariantLabels()

	t.Run("DirectPack_AutoDiscovery_MinimalAndFull_InspectsAndReportsABI", func(t *testing.T) {
		// 1. Minimal profile direct pack
		fatMin := filepath.Join(testDir, "direct_min.fat")
		outMin, errMin := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(siblingCLI,
				"pack",
				"--arch", currentHostArch,
				"--stub-profile", "minimal",
				"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
				"-o", fatMin,
			)
		})
		require.NoError(t, errMin, "direct pack minimal failed: %s", string(outMin))
		assert.Contains(t, string(outMin), "microfat-stub-minimal")
		assert.Contains(t, string(outMin), "Declared ABI Requirements:")
		verifyFatIntegrity(t, fatMin)

		infoOutMin, infoErrMin := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(fatMin, "--microfat:info")
		})
		require.Error(t, infoErrMin)
		assert.Contains(t, string(infoOutMin), "meta-commands are disabled in minimal launcher stub profile")

		runOutMin, runErrMin := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(fatMin)
		})
		require.NoError(t, runErrMin)
		assert.Contains(t, string(runOutMin), "golden:variant=")

		// 2. Full profile direct pack
		fatFull := filepath.Join(testDir, "direct_full.fat")
		outFull, errFull := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(siblingCLI,
				"pack",
				"--arch", currentHostArch,
				"--stub-profile", "full",
				"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
				"-o", fatFull,
			)
		})
		require.NoError(t, errFull, "direct pack full failed: %s", string(outFull))
		assert.Contains(t, string(outFull), "microfat-stub")
		assert.Contains(t, string(outFull), "Declared ABI Requirements:")
		verifyFatIntegrity(t, fatFull)

		infoOutFull, infoErrFull := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(fatFull, "--microfat:info")
		})
		require.NoError(t, infoErrFull, "info full failed: %s", string(infoOutFull))
		assert.Contains(t, string(infoOutFull), "=== Microfat Binary Info ===")
	})

	t.Run("PackManifest_ProfileAndMixedABIPolicy", func(t *testing.T) {
		manifestPath := filepath.Join(testDir, "manifest_mixed_min.yaml")
		manifestContent := "name: mixed-app\npackage: " + pkgDir + "\ntarget_os: linux\ntarget_arch: " +
			currentHostArch + "\nstub_profile: minimal\nvariants:\n  - level: " + v1Label +
			"\n    env:\n      CGO_ENABLED: \"0\"\n  - level: " + v2Label +
			"\n    flags:\n      - \"-tags=cgo_variant\"\n    env:\n      CGO_ENABLED: \"1\"\n"
		require.NoError(t, os.WriteFile(manifestPath, []byte(manifestContent), 0o644))

		canaryFile := filepath.Join(testDir, "pack_manifest_canary.fat")
		const canary = "CANARY_PACK_MANIFEST_PRESERVED"
		require.NoError(t, os.WriteFile(canaryFile, []byte(canary), 0o644))

		// Default: reject mixed ABI and preserve destination
		outReject, errReject := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(siblingCLI, "pack", "--manifest", manifestPath, "-o", canaryFile)
		})
		require.Error(t, errReject, "manifest pack must reject mixed ABI by default: %s", string(outReject))
		assert.Contains(t, string(outReject), "declared ABI requirements mismatch")

		canaryBytes, err := os.ReadFile(canaryFile)
		require.NoError(t, err)
		assert.Equal(t, canary, string(canaryBytes))

		// Override: succeed with --allow-mixed-abi, using minimal profile
		outAllow, errAllow := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(siblingCLI, "pack", "--manifest", manifestPath, "-o", canaryFile, "--allow-mixed-abi")
		})
		require.NoError(t, errAllow, "manifest pack with --allow-mixed-abi failed: %s", string(outAllow))
		assert.Contains(t, string(outAllow), "[OVERRIDDEN]")
		verifyFatIntegrity(t, canaryFile)

		infoOut, infoErr := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(canaryFile, "--microfat:info")
		})
		require.Error(t, infoErr)
		assert.Contains(t, string(infoOut), "meta-commands are disabled in minimal launcher stub profile")

		runOut, runErr := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(canaryFile)
		})
		require.NoError(t, runErr, "run failed: %s", string(runOut))
		assert.Contains(t, string(runOut), "MIXED_OK")
	})

	t.Run("PgoPack_ProfilePrecedenceAndMixedABIPolicy", func(t *testing.T) {
		manifestPath := filepath.Join(testDir, "manifest_pgo_mixed.yaml")
		manifestContent := "name: pgo-mixed-app\npackage: " + pkgDir + "\ntarget_os: linux\ntarget_arch: " +
			currentHostArch + "\nstub_profile: full\nvariants:\n  - level: " + v1Label +
			"\n    env:\n      CGO_ENABLED: \"0\"\n  - level: " + v2Label +
			"\n    flags:\n      - \"-tags=cgo_variant\"\n    env:\n      CGO_ENABLED: \"1\"\n"
		require.NoError(t, os.WriteFile(manifestPath, []byte(manifestContent), 0o644))

		canaryFile := filepath.Join(testDir, "pgo_canary.fat")
		const canary = "CANARY_PGO_PRESERVED"
		require.NoError(t, os.WriteFile(canaryFile, []byte(canary), 0o644))

		// Default: reject mixed ABI
		outReject, errReject := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(siblingCLI, "pgo-pack", "--manifest", manifestPath, "--stub-profile", "minimal", "-o", canaryFile)
		})
		require.Error(t, errReject, "pgo-pack must reject mixed ABI by default: %s", string(outReject))
		assert.Contains(t, string(outReject), "declared ABI requirements mismatch")

		canaryBytes, err := os.ReadFile(canaryFile)
		require.NoError(t, err)
		assert.Equal(t, canary, string(canaryBytes))

		outAllow, errAllow := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(siblingCLI,
				"pgo-pack", "--manifest", manifestPath,
				"--stub-profile", "minimal",
				"-o", canaryFile,
				"--allow-mixed-abi")
		})
		require.NoError(t, errAllow, "pgo-pack with --allow-mixed-abi failed: %s", string(outAllow))
		assert.Contains(t, string(outAllow), "[OVERRIDDEN]")
		verifyFatIntegrity(t, canaryFile)

		infoOut, infoErr := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(canaryFile, "--microfat:info")
		})
		require.Error(t, infoErr)
		assert.Contains(t, string(infoOut), "meta-commands are disabled in minimal launcher stub profile")

		runOut, runErr := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(canaryFile)
		})
		require.NoError(t, runErr, "run failed: %s", string(runOut))
		assert.Contains(t, string(runOut), "MIXED_OK")
	})

	t.Run("ExplicitStubWithProfile_BypassNoticeAndABIPreserved", func(t *testing.T) {
		fatOut := filepath.Join(testDir, "explicit_bypass.fat")
		out, err := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(cliPath,
				"pack",
				"--arch", currentHostArch,
				"--stub", fullStubPath,
				"--stub-profile", "minimal",
				"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
				"-o", fatOut,
			)
		})
		require.NoError(t, err, "pack failed: %s", string(out))
		assert.Contains(t, string(out), "automatic stub-profile selection was bypassed")
		assert.Contains(t, string(out), "Declared ABI Requirements:")
		verifyFatIntegrity(t, fatOut)

		// Full stub was used despite minimal profile flag: meta-commands work
		infoOut, infoErr := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(fatOut, "--microfat:info")
		})
		require.NoError(t, infoErr, "info failed: %s", string(infoOut))
		assert.Contains(t, string(infoOut), "=== Microfat Binary Info ===")
	})

	t.Run("AllowMixedABI_DoesNotWaiveMalformedELF", func(t *testing.T) {
		corruptPath := filepath.Join(testDir, "corrupt_variant")
		require.NoError(t, os.WriteFile(corruptPath, []byte("NOT_A_VALID_ELF_BINARY"), 0o755))
		fatOut := filepath.Join(testDir, "corrupt.fat")

		out, err := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(cliPath,
				"pack",
				"--arch", currentHostArch,
				"--stub", fullStubPath,
				"-v", currentHostLevel+"="+corruptPath,
				"-o", fatOut,
				"--allow-mixed-abi",
			)
		})
		require.Error(t, err, "--allow-mixed-abi must not waive malformed ELF errors")
		assert.NotContains(t, string(out), "[OVERRIDDEN]")
		assert.Contains(t, string(out), "ELF")
	})

	t.Run("SkipELFValidation_WarnsAndPreservesStubProfileAndMissingStubRejection", func(t *testing.T) {
		// 1. Skip validation emits warning, reports skipped, but still respects stub profile
		fatSkip := filepath.Join(testDir, "skip_valid.fat")
		out, err := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(siblingCLI,
				"pack",
				"--arch", currentHostArch,
				"--skip-elf-validation",
				"--stub-profile", "minimal",
				"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
				"-o", fatSkip,
			)
		})
		require.NoError(t, err, "pack with skip validation failed: %s", string(out))
		assert.Contains(t, string(out),
			"[microfat] Warning: ELF architecture and declared ABI validation explicitly skipped via --skip-elf-validation")
		assert.Contains(t, string(out), "Declared ABI Requirements: skipped (--skip-elf-validation)")
		assert.Contains(t, string(out), "microfat-stub-minimal")
		verifyFatIntegrity(t, fatSkip)

		infoOut, infoErr := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			return exec.Command(fatSkip, "--microfat:info")
		})
		require.Error(t, infoErr)
		assert.Contains(t, string(infoOut), "meta-commands are disabled in minimal launcher stub profile")

		// 2. Skip validation must NOT turn missing stub discovery into success
		isolatedDir := filepath.Join(testDir, "isolated_bin")
		require.NoError(t, os.MkdirAll(isolatedDir, 0o755))
		isolatedCLI := filepath.Join(isolatedDir, "microfat")
		copyFile(t, cliPath, isolatedCLI)

		outMissing, errMissing := runFixtureCommandCombinedOutput(func() *exec.Cmd {
			cmd := exec.Command(isolatedCLI,
				"pack",
				"--arch", currentHostArch,
				"--skip-elf-validation",
				"--stub-profile", "minimal",
				"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
				"-o", filepath.Join(testDir, "missing.fat"),
			)
			cmd.Env = []string{"PATH=/nonexistent_empty_path"}
			return cmd
		})
		require.Error(t, errMissing, "missing stub must still fail even with --skip-elf-validation")
		assert.Contains(t, string(outMissing), "launcher stub \"microfat-stub-minimal\" for profile \"minimal\" not found")
	})
}

func runFixtureCommandCombinedOutput(newCommand func() *exec.Cmd) ([]byte, error) {
	var (
		cmd *exec.Cmd
		buf bytes.Buffer
	)
	if err := retryFixtureBusy(func() error {
		buf.Reset()
		cmd = newCommand()
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		return cmd.Start()
	}); err != nil {
		return nil, err
	}
	err := cmd.Wait()
	return buf.Bytes(), err
}
