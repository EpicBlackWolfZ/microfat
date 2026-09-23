package e2e_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	manifestKeyPackage     = "package"
	manifestKeyOutput      = "output"
	manifestKeyTargetArch  = "target_arch"
	manifestKeyStub        = "stub"
	manifestKeyVariants    = "variants"
	manifestKeyLevel       = "level"
	manifestKeyCompression = "compression"
	manifestKeyProfile     = "profile"
	manifestKeyAlgorithm   = "algorithm"
	stubEnvAMD64           = "GOAMD64=" + "v1"
	stubEnvARM64           = "GOARM64=" + "v8.0"
	latencySmallThreshold  = 256 * 1024
)

type inspectVariant struct {
	Level       string `json:"level"`
	Compression string `json:"compression"`
}

type inspectOutput struct {
	AppName  string           `json:"app_name"`
	Variants []inspectVariant `json:"variants"`
}

func inspectFatIndex(t *testing.T, binPath string) inspectOutput {
	t.Helper()
	cmd := exec.Command(cliPath, "inspect", "--json", binPath)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "inspect failed on %s: %s", binPath, string(out))
	var inspected inspectOutput
	require.NoError(t, json.Unmarshal(out, &inspected))
	return inspected
}

func verifyFatIntegrity(t *testing.T, binPath string) {
	t.Helper()
	cmd := exec.Command(cliPath, "verify", binPath)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "verify failed on %s: %s", binPath, string(out))
}

func compileSmallExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	outPath := filepath.Join(dir, name)
	// Build a minimal native C executable (~12 KB < 256 KB latency threshold)
	const cSource = `#include <stdio.h>
int main(void) {
    printf("small_exec_payload\n");
    return 0;
}
`
	srcPath := filepath.Join(dir, name+".c")
	require.NoError(t, os.WriteFile(srcPath, []byte(cSource), 0o600))

	cmd := exec.Command("gcc", "-O2", "-o", outPath, srcPath)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "gcc failed: %s", string(out))

	stat, err := os.Stat(outPath)
	require.NoError(t, err)
	require.Less(t, stat.Size(), int64(latencySmallThreshold),
		"small executable must be smaller than latency threshold (%d bytes)", latencySmallThreshold)

	return outPath
}

func buildMinimalLauncherStub(t *testing.T, dir string) string {
	t.Helper()
	stub := filepath.Join(dir, "stub-minimal")
	stubEnv := []string{stubEnvAMD64, stubEnvARM64}
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, stub, stubEnv, "-tags=minimal", "-ldflags=-s -w"))
	return stub
}

func TestDirectPackCompressionMatrix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	smallBin := compileSmallExecutable(t, dir, "small_bin")
	largeBin := goldenVariantBins[currentHostLevel]
	require.NotEmpty(t, largeBin, "missing host golden variant binary")

	minimalStub := buildMinimalLauncherStub(t, dir)
	stubs := []struct {
		name string
		path string
	}{
		{name: "full", path: stubPath},
		{name: "minimal", path: minimalStub},
	}

	for _, stub := range stubs {
		t.Run(stub.name, func(t *testing.T) {
			// Case 1: Profile-only latency with small input (< 256 KiB) resolves to 'none'
			t.Run("SmallInput_LatencyProfileResolvesNone", func(t *testing.T) {
				outPath := filepath.Join(dir, fmt.Sprintf("small_latency_%s.fat", stub.name))
				cmd := exec.Command(cliPath, "pack",
					"--stub", stub.path,
					"--arch", currentHostArch,
					"-v", fmt.Sprintf("%s=%s", currentHostLevel, smallBin),
					"-o", outPath,
					"--profile", "latency",
				)
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, string(out))

				idx := inspectFatIndex(t, outPath)
				require.Len(t, idx.Variants, 1)
				assert.Equal(t, "none", idx.Variants[0].Compression,
					"small input with latency profile must resolve to 'none'")

				verifyFatIntegrity(t, outPath)

				stdout, stderr, exitCode, err := executeFatBinary(t, outPath, nil)
				require.NoError(t, err, "execution failed (code %d): %s", exitCode, stderr)
				assert.Equal(t, 0, exitCode)
				assert.Contains(t, stdout, "small_exec_payload")
			})

			// Case 2: Profile-only latency with large input (> 256 KiB) resolves to 'lz4'
			t.Run("LargeInput_LatencyProfileResolvesLZ4", func(t *testing.T) {
				outPath := filepath.Join(dir, fmt.Sprintf("large_latency_%s.fat", stub.name))
				cmd := exec.Command(cliPath, "pack",
					"--stub", stub.path,
					"--arch", currentHostArch,
					"-v", fmt.Sprintf("%s=%s", currentHostLevel, largeBin),
					"-o", outPath,
					"--profile", "latency",
				)
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, string(out))

				idx := inspectFatIndex(t, outPath)
				require.Len(t, idx.Variants, 1)
				assert.Equal(t, "lz4", idx.Variants[0].Compression,
					"large input with latency profile must resolve to 'lz4'")

				verifyFatIntegrity(t, outPath)

				stdout, stderr, exitCode, err := executeFatBinary(t, outPath, nil)
				require.NoError(t, err, "execution failed (code %d): %s", exitCode, stderr)
				assert.Equal(t, 0, exitCode)
				assert.Contains(t, stdout, "golden:variant=")
			})

			// Case 3: Explicit Zstd overriding latency profile on small input resolves to 'zstd'
			t.Run("SmallInput_ExplicitZstdOverridesLatencyProfile", func(t *testing.T) {
				outPath := filepath.Join(dir, fmt.Sprintf("small_zstd_override_%s.fat", stub.name))
				cmd := exec.Command(cliPath, "pack",
					"--stub", stub.path,
					"--arch", currentHostArch,
					"-v", fmt.Sprintf("%s=%s", currentHostLevel, smallBin),
					"-o", outPath,
					"--profile", "latency",
					"--compression", "zstd",
				)
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, string(out))

				idx := inspectFatIndex(t, outPath)
				require.Len(t, idx.Variants, 1)
				assert.Equal(t, "zstd", idx.Variants[0].Compression,
					"explicit --compression=zstd must override latency profile")

				verifyFatIntegrity(t, outPath)

				stdout, stderr, exitCode, err := executeFatBinary(t, outPath, nil)
				require.NoError(t, err, "execution failed (code %d): %s", exitCode, stderr)
				assert.Equal(t, 0, exitCode)
				assert.Contains(t, stdout, "small_exec_payload")
			})
		})
	}
}

func TestManifestPackCompressionPrecedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeManifestPackage(t, dir)

	// Case 1: Implicit root algorithm with child profile resolves to 'lz4'
	t.Run("ImplicitRootAlgorithm_ChildProfileResolves", func(t *testing.T) {
		manifestPath := filepath.Join(dir, "manifest_implicit.json")
		fatPath := filepath.Join(dir, "app_manifest_implicit.fat")

		manifest := map[string]any{
			manifestKeyPackage:    ".",
			manifestKeyOutput:     fatPath,
			manifestKeyTargetArch: currentHostArch,
			manifestKeyStub:       stubPath,
			manifestKeyVariants: []any{
				map[string]any{
					manifestKeyLevel: currentHostLevel,
					manifestKeyCompression: map[string]any{
						manifestKeyProfile: "latency",
					},
				},
			},
		}
		writeManifestJSON(t, manifestPath, manifest)

		cmd := exec.Command(cliPath, "pack", "--manifest", manifestPath)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))

		idx := inspectFatIndex(t, fatPath)
		require.Len(t, idx.Variants, 1)
		assert.Equal(t, "lz4", idx.Variants[0].Compression,
			"compiled Go variant with child latency profile must resolve to 'lz4'")

		verifyFatIntegrity(t, fatPath)

		stdout, stderr, exitCode, err := executeFatBinary(t, fatPath, nil)
		require.NoError(t, err, "execution failed (code %d): %s", exitCode, stderr)
		assert.Equal(t, 0, exitCode)
		assert.Contains(t, stdout, "manifest payload")
	})

	// Case 2: Explicit root algorithm retained unless explicitly overridden
	t.Run("ExplicitRootAlgorithm_RetainedUnlessOverridden", func(t *testing.T) {
		manifestPath := filepath.Join(dir, "manifest_root_zstd.json")
		fatPath := filepath.Join(dir, "app_root_zstd.fat")

		manifest := map[string]any{
			manifestKeyPackage:    ".",
			manifestKeyOutput:     fatPath,
			manifestKeyTargetArch: currentHostArch,
			manifestKeyStub:       stubPath,
			manifestKeyCompression: map[string]any{
				manifestKeyAlgorithm: "zstd",
			},
			manifestKeyVariants: []any{
				map[string]any{
					manifestKeyLevel: currentHostLevel,
					manifestKeyCompression: map[string]any{
						manifestKeyProfile: "latency", // Has profile, but root has explicit zstd -> must retain root zstd
					},
				},
			},
		}
		writeManifestJSON(t, manifestPath, manifest)

		cmd := exec.Command(cliPath, "pack", "--manifest", manifestPath)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))

		idx := inspectFatIndex(t, fatPath)
		require.Len(t, idx.Variants, 1)
		assert.Equal(t, "zstd", idx.Variants[0].Compression,
			"explicit root compression=zstd must be retained over variant profile")

		verifyFatIntegrity(t, fatPath)

		stdout, stderr, exitCode, err := executeFatBinary(t, fatPath, nil)
		require.NoError(t, err, "execution failed (code %d): %s", exitCode, stderr)
		assert.Equal(t, 0, exitCode)
		assert.Contains(t, stdout, "manifest payload")
	})

	// Case 3: Explicit variant algorithm overrides explicit root algorithm
	t.Run("ExplicitVariantAlgorithm_OverridesExplicitRootAlgorithm", func(t *testing.T) {
		manifestPath := filepath.Join(dir, "manifest_variant_override.json")
		fatPath := filepath.Join(dir, "app_variant_override.fat")

		manifest := map[string]any{
			manifestKeyPackage:    ".",
			manifestKeyOutput:     fatPath,
			manifestKeyTargetArch: currentHostArch,
			manifestKeyStub:       stubPath,
			manifestKeyCompression: map[string]any{
				manifestKeyAlgorithm: "zstd",
			},
			manifestKeyVariants: []any{
				map[string]any{
					manifestKeyLevel: currentHostLevel,
					manifestKeyCompression: map[string]any{
						manifestKeyAlgorithm: "none", // Explicitly overrides root zstd to none
					},
				},
			},
		}
		writeManifestJSON(t, manifestPath, manifest)

		cmd := exec.Command(cliPath, "pack", "--manifest", manifestPath)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))

		idx := inspectFatIndex(t, fatPath)
		require.Len(t, idx.Variants, 1)
		assert.Equal(t, "none", idx.Variants[0].Compression,
			"explicit variant compression=none must override root compression=zstd")

		verifyFatIntegrity(t, fatPath)

		stdout, stderr, exitCode, err := executeFatBinary(t, fatPath, nil)
		require.NoError(t, err, "execution failed (code %d): %s", exitCode, stderr)
		assert.Equal(t, 0, exitCode)
		assert.Contains(t, stdout, "manifest payload")
	})

	// Case 4: CLI flag overrides root manifest algorithm
	t.Run("CLIFlag_OverridesRootManifestAlgorithm", func(t *testing.T) {
		manifestPath := filepath.Join(dir, "manifest_cli_override.json")
		fatPath := filepath.Join(dir, "app_cli_override.fat")

		manifest := map[string]any{
			manifestKeyPackage:    ".",
			manifestKeyOutput:     fatPath,
			manifestKeyTargetArch: currentHostArch,
			manifestKeyStub:       stubPath,
			manifestKeyCompression: map[string]any{
				manifestKeyAlgorithm: "zstd",
			},
			manifestKeyVariants: []any{
				map[string]any{
					manifestKeyLevel: currentHostLevel,
				},
			},
		}
		writeManifestJSON(t, manifestPath, manifest)

		// CLI flag --compression=none overrides manifest root zstd
		cmd := exec.Command(cliPath, "pack", "--manifest", manifestPath, "--compression", "none")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))

		idx := inspectFatIndex(t, fatPath)
		require.Len(t, idx.Variants, 1)
		assert.Equal(t, "none", idx.Variants[0].Compression,
			"CLI flag --compression=none must override manifest root compression=zstd")

		verifyFatIntegrity(t, fatPath)

		stdout, stderr, exitCode, err := executeFatBinary(t, fatPath, nil)
		require.NoError(t, err, "execution failed (code %d): %s", exitCode, stderr)
		assert.Equal(t, 0, exitCode)
		assert.Contains(t, stdout, "manifest payload")
	})
}

func TestPGOPackCompressionPrecedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeManifestPackage(t, dir)

	t.Run("PGOPack_PreservesPrecedenceRules", func(t *testing.T) {
		manifestPath := filepath.Join(dir, "manifest_pgo.json")
		fatPath := filepath.Join(dir, "app_pgo.fat")

		manifest := map[string]any{
			manifestKeyPackage:    ".",
			manifestKeyOutput:     fatPath,
			manifestKeyTargetArch: currentHostArch,
			manifestKeyStub:       stubPath,
			manifestKeyCompression: map[string]any{
				manifestKeyProfile: "latency",
			},
			manifestKeyVariants: []any{
				map[string]any{
					manifestKeyLevel: currentHostLevel,
					"pgo":            "off",
				},
			},
		}
		writeManifestJSON(t, manifestPath, manifest)

		// Run pgo-pack with --compression=none override
		cmd := exec.Command(cliPath, "pgo-pack", "--manifest", manifestPath, "--compression", "none")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "pgo-pack failed: %s", string(out))

		idx := inspectFatIndex(t, fatPath)
		require.Len(t, idx.Variants, 1)
		assert.Equal(t, "none", idx.Variants[0].Compression,
			"pgo-pack CLI override --compression=none must take precedence")

		verifyFatIntegrity(t, fatPath)

		stdout, stderr, exitCode, err := executeFatBinary(t, fatPath, nil)
		require.NoError(t, err, "execution failed (code %d): %s", exitCode, stderr)
		assert.Equal(t, 0, exitCode)
		assert.Contains(t, stdout, "manifest payload")
	})
}
