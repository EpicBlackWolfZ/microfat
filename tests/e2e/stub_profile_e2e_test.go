package e2e_test

import (
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
	cliBytes, err := os.ReadFile(cliPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(siblingCLI, cliBytes, 0o755))

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

	t.Run("DirectPack_StubFull_With_StubProfileMinimal_Fails", func(t *testing.T) {
		cmd := exec.Command(cliPath,
			"pack",
			"--stub", fullStubPath,
			"--stub-profile", "minimal",
			"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
			"-o", fatOut,
		)
		out, err := cmd.CombinedOutput()
		require.Error(t, err)
		assert.Contains(t, string(out), "conflict")
	})

	t.Run("DirectPack_StubMinimal_With_StubProfileFull_Fails", func(t *testing.T) {
		cmd := exec.Command(cliPath,
			"pack",
			"--stub", minStubPath,
			"--stub-profile", "full",
			"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
			"-o", fatOut,
		)
		out, err := cmd.CombinedOutput()
		require.Error(t, err)
		assert.Contains(t, string(out), "conflict")
	})

	t.Run("DirectPack_InvalidStubProfile_Fails", func(t *testing.T) {
		cmd := exec.Command(cliPath,
			"pack",
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
	cliBytes, err := os.ReadFile(cliPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(siblingCLI, cliBytes, 0o755))

	// In isolatedBinDir, do NOT create microfat-stub-minimal
	fatOut := filepath.Join(testDir, "app.fat")

	cmd := exec.Command(siblingCLI,
		"pack",
		"--stub-profile", "minimal",
		"-v", currentHostLevel+"="+goldenVariantBins[currentHostLevel],
		"-o", fatOut,
	)
	cmd.Env = []string{"PATH=/nonexistent_empty_path"}
	out, err := cmd.CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(out), "launcher stub \"microfat-stub-minimal\" for profile \"minimal\" not found")
}

func TestStubProfile_ManifestPackAndPgoPack(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	binDir := filepath.Join(testDir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	siblingCLI := filepath.Join(binDir, "microfat")
	cliBytes, err := os.ReadFile(cliPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(siblingCLI, cliBytes, 0o755))

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
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module sample\ngo 1.27.1\n"), 0o644))

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

	t.Run("PgoPack_ConflictBetweenManifestStubAndCLIProfile", func(t *testing.T) {
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
		require.Error(t, err)
		assert.Contains(t, string(out), "conflict")
	})
}
