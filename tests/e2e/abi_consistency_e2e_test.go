//go:build linux

package e2e_test

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func findHostDynamicELF(t *testing.T) string {
	t.Helper()
	candidates := []string{"/bin/true", "/usr/bin/true", "/bin/echo", "/usr/bin/echo"}
	if lp, err := exec.LookPath("true"); err == nil {
		candidates = append([]string{lp}, candidates...)
	}
	for _, cand := range candidates {
		data, err := os.ReadFile(cand)
		if err != nil || len(data) < 16 {
			continue
		}
		if bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'}) && data[4] == 2 {
			f, oErr := elf.Open(cand)
			if oErr == nil {
				hasInterp := false
				for _, prog := range f.Progs {
					if prog.Type == elf.PT_INTERP {
						hasInterp = true
						break
					}
				}
				_ = f.Close()
				if hasInterp {
					return cand
				}
			}
		}
	}
	t.Skip("skipping dynamic ELF test: no dynamic 64-bit host ELF found")
	return ""
}

func TestDirectPack_ABIMismatchRejectionAndPreservation(t *testing.T) {
	t.Parallel()

	dynamicELF := findHostDynamicELF(t)
	staticELF, ok := goldenVariantBins[currentHostLevel]
	require.True(t, ok, "golden static binary for current host level must exist")

	dir := t.TempDir()
	outFat := filepath.Join(dir, "app-mismatch.fat")
	const canary = "PRESERVED_FILE_CONTENT_DO_NOT_OVERWRITE"
	require.NoError(t, os.WriteFile(outFat, []byte(canary), privateFilePerm))

	// Attempt direct pack without --allow-mixed-abi: must fail fast with descriptive error
	cmd := exec.Command(cliPath,
		"pack",
		"--stub", stubPath,
		"-o", outFat,
		"-v", "v1="+staticELF,
		"-v", "v2="+dynamicELF,
	)
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "pack with mismatched ABIs must fail")
	outStr := string(output)
	require.Contains(t, outStr, "declared ABI requirements mismatch between variants")
	require.Contains(t, outStr, "mixed static and dynamic payloads")

	// Verify that the destination file was not overwritten or removed
	preserved, rErr := os.ReadFile(outFat)
	require.NoError(t, rErr)
	require.Equal(t, canary, string(preserved), "destination file must be preserved intact on packaging failure")
}

func TestDirectPack_AllowMixedABIOverride(t *testing.T) {
	t.Parallel()

	dynamicELF := findHostDynamicELF(t)
	staticELF, ok := goldenVariantBins[currentHostLevel]
	require.True(t, ok, "golden static binary for current host level must exist")

	dir := t.TempDir()
	outFat := filepath.Join(dir, "app-allowed.fat")

	// Attempt direct pack with --allow-mixed-abi: must succeed and emit override warning
	cmd := exec.Command(cliPath,
		"pack",
		"--allow-mixed-abi",
		"--stub", stubPath,
		"-o", outFat,
		"-v", "v1="+staticELF,
		"-v", "v2="+dynamicELF,
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "pack with --allow-mixed-abi must succeed: %s", string(output))

	outStr := string(output)
	require.Contains(t, outStr, "[OVERRIDDEN]")
	require.Contains(t, outStr, "--allow-mixed-abi")
	require.Contains(t, outStr, "Declared ABI Requirements:")

	// Verify that the resulting fat binary verifies properly
	verifyCmd := exec.Command(cliPath, "verify", outFat)
	verifyOut, vErr := verifyCmd.CombinedOutput()
	require.NoError(t, vErr, "verify output: %s", string(verifyOut))
}

func TestDirectPack_ConsistentABISucceeds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outFat := filepath.Join(dir, "app-consistent.fat")

	args := []string{
		"pack",
		"--stub", stubPath,
		"-o", outFat,
	}
	for lvl, bin := range goldenVariantBins {
		args = append(args, "-v", lvl+"="+bin)
	}

	cmd := exec.Command(cliPath, args...)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "pack with consistent ABIs must succeed: %s", string(output))

	outStr := string(output)
	require.Contains(t, outStr, "Declared ABI Requirements:")
	require.NotContains(t, outStr, "[OVERRIDDEN]")

	verifyCmd := exec.Command(cliPath, "verify", outFat)
	verifyOut, vErr := verifyCmd.CombinedOutput()
	require.NoError(t, vErr, "verify output: %s", string(verifyOut))
}

func TestManifestPack_ABIConsistencyAndFlag(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"pack", "pgo-pack"} {
		for _, allowMixed := range []bool{false, true} {
			testName := command
			if allowMixed {
				testName += "/allow-mixed-abi"
			} else {
				testName += "/default"
			}

			t.Run(testName, func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()
				pkgDir := filepath.Join(dir, "pkg")
				writeManifestPackage(t, pkgDir)

				var variants []any
				switch currentHostArch {
				case archAMD64:
					variants = append(variants, map[string]any{manifestKeyLevel: "v1"}, map[string]any{manifestKeyLevel: "v2"})
				case archARM64:
					variants = append(variants, map[string]any{manifestKeyLevel: manifestARM64Base}, map[string]any{manifestKeyLevel: "v8.2"})
				default:
					variants = append(variants, map[string]any{manifestKeyLevel: currentHostLevel})
				}

				outFatName := "manifest-fat"
				manifest := map[string]any{
					"package":       "pkg",
					"output":        outFatName,
					"target_arch":   currentHostArch,
					manifestKeyStub: stubPath,
					"variants":      variants,
				}
				manifestPath := filepath.Join(dir, "build.json")
				writeManifestJSON(t, manifestPath, manifest)

				args := []string{command, "--manifest", manifestPath}
				if allowMixed {
					args = append(args, "--allow-mixed-abi")
				}

				cmd := exec.Command(cliPath, args...)
				cmd.Dir = dir
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "manifest %s failed: %s", command, string(output))

				outStr := string(output)
				require.Contains(t, outStr, "Declared ABI Requirements:")

				fatPath := filepath.Join(dir, outFatName)
				verifyCmd := exec.Command(cliPath, "verify", fatPath)
				verifyOut, vErr := verifyCmd.CombinedOutput()
				require.NoError(t, vErr, "verify output: %s", string(verifyOut))

				execOut, runErr := exec.Command(fatPath).CombinedOutput()
				require.NoError(t, runErr, "execute output: %s", string(execOut))
				require.Contains(t, string(execOut), "manifest payload")
			})
		}
	}
}
