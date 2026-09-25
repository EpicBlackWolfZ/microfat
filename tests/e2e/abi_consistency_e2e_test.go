//go:build linux

package e2e_test

import (
	"bytes"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
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

const payloadSuccessMarker = "MICROFAT_DISCRIMINATING_PAYLOAD_SUCCESS"

func findHostInterpreter(t *testing.T) string {
	t.Helper()
	hostELF := findHostDynamicELF(t)
	f, err := elf.Open(hostELF)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	for _, prog := range f.Progs {
		if prog.Type == elf.PT_INTERP {
			buf := make([]byte, prog.Filesz)
			_, rErr := prog.ReadAt(buf, 0)
			require.NoError(t, rErr)
			n := bytes.IndexByte(buf, 0)
			if n < 0 {
				n = len(buf)
			}
			interp := string(buf[:n])
			if _, sErr := os.Stat(interp); sErr == nil {
				return interp
			}
		}
	}
	t.Skip("skipping missing interpreter test: host interpreter not found on disk")
	return ""
}

func classifyMissingInterpreterFailure(output string, runErr error, expectedMode string) error {
	if runErr == nil {
		return errors.New("expected execution to fail, but command exited successfully")
	}
	if strings.Contains(output, payloadSuccessMarker) {
		return errors.New("payload success marker found in output despite reported error")
	}

	// Reject earlier-stage lifecycle, setup, extraction and permission failures
	forbiddenPhrases := []string{
		"permission denied",
		"unable to initialize cache",
		"materializing cache binary failed",
		"decompressing payload to cache failed",
		"checksum mismatch",
		"integrity verification failed",
		"unsupported microarchitecture level",
		"memfd_create failed",
		"sealing memfd failed",
		"refusal to execute symlink",
		"refusal to execute unsafe cache entry",
	}
	for _, phrase := range forbiddenPhrases {
		if strings.Contains(strings.ToLower(output), phrase) {
			return fmt.Errorf("classified as setup/security failure instead of missing interpreter: output contains %q", phrase)
		}
	}

	// Must be an execution stage failure matching the expected execution mode
	hasExecFailure := false
	switch expectedMode {
	case execModeMemfd:
		if strings.Contains(output, `"stage":"execve_cache"`) || strings.Contains(output, "cache execve failed") {
			hasExecFailure = false
		} else if strings.Contains(output, `"stage":"execve_memfd"`) || strings.Contains(output, `"stage":"memfd_exec"`) ||
			strings.Contains(output, "execve failed on /proc/self/fd/") || strings.Contains(output, "execve on /proc/self/fd/") {
			hasExecFailure = true
		}
	case execModeCache:
		if strings.Contains(output, `"stage":"execve_cache"`) || strings.Contains(output, `"stage":"cache_exec"`) ||
			strings.Contains(output, "cache execve failed") {
			hasExecFailure = true
		}
	default:
		return fmt.Errorf("unknown expected mode: %q", expectedMode)
	}

	if !hasExecFailure {
		return fmt.Errorf("failure occurred outside expected %s execution stage: %s", expectedMode, output)
	}

	// Must identify missing file / interpreter (ENOENT)
	if !strings.Contains(output, "no such file or directory") && !strings.Contains(output, "ENOENT") {
		return fmt.Errorf("execution error does not identify missing interpreter (ENOENT): %s", output)
	}

	return nil
}

func isStaticELF(t *testing.T, path string) bool {
	t.Helper()
	f, err := elf.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return false
		}
	}
	return true
}

func isDynamicELF(t *testing.T, path string) bool {
	t.Helper()
	f, err := elf.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return true
		}
	}
	return false
}

func hostVariantLabels() (string, string) {
	if currentHostArch == archARM64 {
		return "v8.0", "v8.1"
	}
	return "v1", "v2"
}

func TestDirectPack_ABIMismatchRejectionAndPreservation(t *testing.T) {
	t.Parallel()

	dynamicELF := findHostDynamicELF(t)
	staticELF, ok := goldenVariantBins[currentHostLevel]
	require.True(t, ok, "golden static binary for current host level must exist")

	// R6: Prove which binary is static and which is dynamic through bounded metadata inspection
	require.True(t, isDynamicELF(t, dynamicELF), "dynamic fixture must have PT_INTERP")
	require.True(t, isStaticELF(t, staticELF), "static fixture must not have PT_INTERP")

	v1Label, v2Label := hostVariantLabels()

	dir := t.TempDir()
	outFat := filepath.Join(dir, "app-mismatch.fat")
	const canary = "PRESERVED_FILE_CONTENT_DO_NOT_OVERWRITE"
	require.NoError(t, os.WriteFile(outFat, []byte(canary), privateFilePerm))

	// R6: Explicit --arch currentHostArch and architecture-appropriate variant labels
	cmd := exec.Command(cliPath,
		"pack",
		"--arch", currentHostArch,
		"--stub", stubPath,
		"-o", outFat,
		"-v", v1Label+"="+staticELF,
		"-v", v2Label+"="+dynamicELF,
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

	require.True(t, isDynamicELF(t, dynamicELF), "dynamic fixture must have PT_INTERP")
	require.True(t, isStaticELF(t, staticELF), "static fixture must not have PT_INTERP")

	v1Label, v2Label := hostVariantLabels()

	dir := t.TempDir()
	outFat := filepath.Join(dir, "app-allowed.fat")

	// R6: Explicit --arch currentHostArch and architecture-appropriate variant labels
	cmd := exec.Command(cliPath,
		"pack",
		"--arch", currentHostArch,
		"--allow-mixed-abi",
		"--stub", stubPath,
		"-o", outFat,
		"-v", v1Label+"="+staticELF,
		"-v", v2Label+"="+dynamicELF,
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
		"--arch", currentHostArch,
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

	for _, command := range []string{inputPackCommand, inputPGOPackCommand} {
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

// A1: Acceptance tests with real version providers, missing interpreter, and genuine mismatches.
func TestABI_RuntimeAcceptance_VersionProvider(t *testing.T) {
	t.Parallel()

	gccPath, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("skipping version provider test: gcc not found in PATH")
	}

	dir := t.TempDir()
	dir1 := filepath.Join(dir, "provider1")
	dir2 := filepath.Join(dir, "provider2")
	require.NoError(t, os.MkdirAll(dir1, defaultFilePerm))
	require.NoError(t, os.MkdirAll(dir2, defaultFilePerm))

	// 1. Build Provider 1: libmicrofat_review.so.1 exporting review_value under MF_REVIEW_1
	libSource := "int review_value(void) { return 42; }\n"
	libSourcePath := filepath.Join(dir, "libreview.c")
	require.NoError(t, os.WriteFile(libSourcePath, []byte(libSource), privateFilePerm))

	verScript1 := "MF_REVIEW_1 {\n    global:\n        review_value;\n    local:\n        *;\n};\n"
	ver1Path := filepath.Join(dir, "ver1.map")
	require.NoError(t, os.WriteFile(ver1Path, []byte(verScript1), privateFilePerm))

	lib1Path := filepath.Join(dir1, "libmicrofat_review.so.1")
	cmdLib1 := exec.Command(gccPath, "-shared", "-fPIC",
		"-Wl,--version-script="+ver1Path,
		"-Wl,-soname,libmicrofat_review.so.1",
		"-o", lib1Path,
		libSourcePath)
	outLib1, err := cmdLib1.CombinedOutput()
	require.NoError(t, err, "compiling provider 1 failed: %s", string(outLib1))

	// 2. Build Provider 2: libmicrofat_review.so.1 exporting review_value under MF_REVIEW_2
	verScript2 := "MF_REVIEW_2 {\n    global:\n        review_value;\n    local:\n        *;\n};\n"
	ver2Path := filepath.Join(dir, "ver2.map")
	require.NoError(t, os.WriteFile(ver2Path, []byte(verScript2), privateFilePerm))

	lib2Path := filepath.Join(dir2, "libmicrofat_review.so.1")
	cmdLib2 := exec.Command(gccPath, "-shared", "-fPIC",
		"-Wl,--version-script="+ver2Path,
		"-Wl,-soname,libmicrofat_review.so.1",
		"-o", lib2Path,
		libSourcePath)
	outLib2, err := cmdLib2.CombinedOutput()
	require.NoError(t, err, "compiling provider 2 failed: %s", string(outLib2))

	// 3. Build payload executable linking against provider 1 (requires MF_REVIEW_1)
	mainSource := `#include <stdio.h>
extern int review_value(void);
int main(void) {
    printf("review_value=%d\n", review_value());
    return 0;
}
`
	mainSourcePath := filepath.Join(dir, "main.c")
	require.NoError(t, os.WriteFile(mainSourcePath, []byte(mainSource), privateFilePerm))

	payloadPath := filepath.Join(dir, "app_payload")
	cmdApp := exec.Command(gccPath, mainSourcePath, "-L"+dir1, "-l:libmicrofat_review.so.1", "-o", payloadPath)
	outApp, err := cmdApp.CombinedOutput()
	require.NoError(t, err, "compiling app payload failed: %s", string(outApp))

	// Verify metadata: ensure payload genuinely requires MF_REVIEW_1
	payloadData, err := os.ReadFile(payloadPath)
	require.NoError(t, err)
	require.Contains(t, string(payloadData), "MF_REVIEW_1", "payload must contain MF_REVIEW_1 version requirement string")

	// 4. Build minimal stub to test both full and minimal launcher profiles
	minimalStub := filepath.Join(dir, "stub-minimal")
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, minimalStub, nil, "-tags=minimal", "-ldflags=-s -w"))

	v1Label, _ := hostVariantLabels()

	// 5. Test execution across profiles (full, minimal) and modes (memfd, cache)
	type stubConfig struct {
		name string
		path string
	}
	stubs := []stubConfig{
		{name: launcherFullProfile, path: stubPath},
		{name: launcherMinimalProfile, path: minimalStub},
	}

	for _, sc := range stubs {
		sc := sc
		t.Run("profile_"+sc.name, func(t *testing.T) {
			t.Parallel()

			fatBin := filepath.Join(dir, "app-fat-"+sc.name)
			packCmd := exec.Command(cliPath, "pack",
				"--arch", currentHostArch,
				"--stub", sc.path,
				"-o", fatBin,
				"-v", v1Label+"="+payloadPath,
			)
			packOut, pErr := packCmd.CombinedOutput()
			require.NoError(t, pErr, "packing %s fat binary failed: %s", sc.name, string(packOut))

			verifyCmd := exec.Command(cliPath, "verify", fatBin)
			verifyOut, vErr := verifyCmd.CombinedOutput()
			require.NoError(t, vErr, "verifying %s fat binary failed: %s", sc.name, string(verifyOut))

			for _, mode := range []string{execModeMemfd, execModeCache} {
				mode := mode
				t.Run("mode_"+mode, func(t *testing.T) {
					privateCache := filepath.Join(dir, "cache-"+sc.name+"-"+mode)
					require.NoError(t, os.MkdirAll(privateCache, privateDirPerm))

					runEnv := func(libDir string) []string {
						env := []string{
							"PATH=" + os.Getenv("PATH"),
							"LD_LIBRARY_PATH=" + libDir,
							"XDG_CACHE_HOME=" + privateCache,
							"MICROFAT_EXEC_MODE=" + mode,
						}
						return env
					}

					// Environment 1: Compatible loader and matching provider 1 -> succeeds
					rawCmd := exec.Command(payloadPath)
					rawCmd.Env = runEnv(dir1)
					rawOut, rawErr := rawCmd.CombinedOutput()
					require.NoError(t, rawErr, "raw payload in provider1 must succeed: %s", string(rawOut))
					require.Equal(t, "review_value=42\n", string(rawOut))

					fatExecCmd := exec.Command(fatBin)
					fatExecCmd.Env = runEnv(dir1)
					fatOut, fatErr := fatExecCmd.CombinedOutput()
					require.NoError(t, fatErr, "fat binary (%s, %s) in provider1 must succeed: %s", sc.name, mode, string(fatOut))
					require.Equal(t, "review_value=42\n", string(fatOut))

					// Environment 2: Incompatible provider 2 lacking MF_REVIEW_1 -> fails for missing version requirement
					rawFailCmd := exec.Command(payloadPath)
					rawFailCmd.Env = runEnv(dir2)
					rawFailOut, rawFailErr := rawFailCmd.CombinedOutput()
					require.Error(t, rawFailErr, "raw payload in provider2 must fail")
					rawErrStr := string(rawFailOut)
					require.Contains(t, rawErrStr, "MF_REVIEW_1", "raw payload error must identify missing version")

					fatFailCmd := exec.Command(fatBin)
					fatFailCmd.Env = runEnv(dir2)
					fatFailOut, fatFailErr := fatFailCmd.CombinedOutput()
					require.Error(t, fatFailErr, "fat binary (%s, %s) in provider2 must fail", sc.name, mode)
					fatErrStr := string(fatFailOut)
					require.Contains(t, fatErrStr, "MF_REVIEW_1", "fat binary error must identify missing version")
				})
			}
		})
	}
}

func TestABI_RuntimeAcceptance_MissingInterpreter(t *testing.T) {
	t.Parallel()

	gccPath, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("skipping missing interpreter test: gcc not found in PATH")
	}
	hostInterp := findHostInterpreter(t)

	dir := t.TempDir()
	interpLink := filepath.Join(dir, "ld-private.so")
	require.NoError(t, os.Symlink(hostInterp, interpLink))

	mainSource := `#include <stdio.h>
int main(void) {
    puts("` + payloadSuccessMarker + `");
    return 0;
}
`
	mainSourcePath := filepath.Join(dir, "main.c")
	require.NoError(t, os.WriteFile(mainSourcePath, []byte(mainSource), privateFilePerm))

	payloadPath := filepath.Join(dir, "missing_interp_payload")
	cmdApp := exec.Command(gccPath, mainSourcePath, "-Wl,--dynamic-linker="+interpLink, "-o", payloadPath)
	outApp, err := cmdApp.CombinedOutput()
	require.NoError(t, err, "compiling missing interpreter payload failed: %s", string(outApp))

	// Verify compiled payload metadata
	payloadELF, elfErr := elf.Open(payloadPath)
	require.NoError(t, elfErr)
	var foundInterp string
	for _, prog := range payloadELF.Progs {
		if prog.Type == elf.PT_INTERP {
			buf := make([]byte, prog.Filesz)
			_, rErr := prog.ReadAt(buf, 0)
			require.NoError(t, rErr)
			n := bytes.IndexByte(buf, 0)
			if n < 0 {
				n = len(buf)
			}
			foundInterp = string(buf[:n])
		}
	}
	_ = payloadELF.Close()
	require.Equal(t, interpLink, foundInterp, "payload must point to private interpreter symlink")

	// Phase 1: Positive execution while symlink exists
	rawPosOut, rawPosErr := exec.Command(payloadPath).CombinedOutput()
	require.NoError(t, rawPosErr, "raw positive execution failed: %s", string(rawPosOut))
	require.Contains(t, string(rawPosOut), payloadSuccessMarker)

	minimalStub := filepath.Join(dir, "stub-minimal")
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, minimalStub, nil, "-tags=minimal", "-ldflags=-s -w"))

	v1Label, _ := hostVariantLabels()

	type stubConfig struct {
		name string
		stub string
	}
	stubs := []stubConfig{
		{launcherFullProfile, stubPath},
		{launcherMinimalProfile, minimalStub},
	}
	modes := []string{execModeMemfd, execModeCache}

	type packedArtifact struct {
		name   string
		path   string
		stub   string
		caches map[string]string
	}
	var packedList []packedArtifact

	for _, sc := range stubs {
		fatBin := filepath.Join(dir, "fat-missing-interp-"+sc.name)
		packCmd := exec.Command(cliPath, "pack",
			"--arch", currentHostArch,
			"--stub", sc.stub,
			"-o", fatBin,
			"-v", v1Label+"="+payloadPath,
		)
		packOut, pErr := packCmd.CombinedOutput()
		require.NoError(t, pErr, "packing must succeed: %s", string(packOut))

		verifyCmd := exec.Command(cliPath, "verify", fatBin)
		verifyOut, vErr := verifyCmd.CombinedOutput()
		require.NoError(t, vErr, "integrity verification must succeed: %s", string(verifyOut))

		pa := packedArtifact{
			name:   sc.name,
			path:   fatBin,
			stub:   sc.stub,
			caches: make(map[string]string),
		}

		for _, mode := range modes {
			privateCache := filepath.Join(dir, "cache-"+sc.name+"-"+mode)
			require.NoError(t, os.MkdirAll(privateCache, privateDirPerm))
			pa.caches[mode] = privateCache

			// Run positive packaged control
			runCmd := exec.Command(fatBin)
			runCmd.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"XDG_CACHE_HOME=" + privateCache,
				"MICROFAT_CACHE_DIR=" + privateCache,
				"MICROFAT_EXEC_MODE=" + mode,
				"MICROFAT_LOG=json",
			}
			posOut, posErr := runCmd.CombinedOutput()
			require.NoError(t, posErr, "positive packaged execution must succeed (%s, %s): %s", sc.name, mode, string(posOut))
			require.Contains(t, string(posOut), payloadSuccessMarker)
		}
		packedList = append(packedList, pa)
	}

	// Phase 2: Negative execution after removing ONLY the private interpreter symlink
	require.NoError(t, os.Remove(interpLink))
	_, statErr := os.Lstat(interpLink)
	require.True(t, os.IsNotExist(statErr), "private interpreter symlink must be removed")

	// Verify all payload and packed executables STILL exist
	_, pStat := os.Stat(payloadPath)
	require.NoError(t, pStat, "payload file must still exist")
	for _, pa := range packedList {
		_, fStat := os.Stat(pa.path)
		require.NoError(t, fStat, "fat binary %s must still exist", pa.name)
	}

	// Raw negative execution must fail because dynamic linker is missing (ENOENT)
	rawNegCmd := exec.Command(payloadPath)
	rawNegOut, rawNegErr := rawNegCmd.CombinedOutput()
	require.Error(t, rawNegErr, "raw execution with missing interpreter must fail")
	require.NotContains(t, string(rawNegOut), payloadSuccessMarker)
	require.True(t, errors.Is(rawNegErr, syscall.ENOENT) || errors.Is(rawNegErr, os.ErrNotExist) ||
		strings.Contains(rawNegErr.Error(), "no such file or directory"),
		"raw error must indicate ENOENT / missing file: %v", rawNegErr)

	// Packaged negative execution must fail at the execve stage with classified error
	for _, pa := range packedList {
		for _, mode := range modes {
			negCmd := exec.Command(pa.path)
			negCmd.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"XDG_CACHE_HOME=" + pa.caches[mode],
				"MICROFAT_CACHE_DIR=" + pa.caches[mode],
				"MICROFAT_EXEC_MODE=" + mode,
				"MICROFAT_LOG=json",
			}
			negOut, negErr := negCmd.CombinedOutput()
			require.Error(t, negErr, "dispatched execution with missing interpreter must fail (%s, %s)", pa.name, mode)
			classErr := classifyMissingInterpreterFailure(string(negOut), negErr, mode)
			require.NoError(t, classErr, "packaged failure must classify cleanly as missing interpreter (%s, %s): %s", pa.name, mode, string(negOut))
		}
	}

	// Phase 3: Restored execution after recreating the private interpreter symlink
	require.NoError(t, os.Symlink(hostInterp, interpLink))

	rawRestOut, rawRestErr := exec.Command(payloadPath).CombinedOutput()
	require.NoError(t, rawRestErr, "raw execution after interpreter restoration must succeed: %s", string(rawRestOut))
	require.Contains(t, string(rawRestOut), payloadSuccessMarker)

	for _, pa := range packedList {
		for _, mode := range modes {
			restCmd := exec.Command(pa.path)
			restCmd.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"XDG_CACHE_HOME=" + pa.caches[mode],
				"MICROFAT_CACHE_DIR=" + pa.caches[mode],
				"MICROFAT_EXEC_MODE=" + mode,
				"MICROFAT_LOG=json",
			}
			restOut, restErr := restCmd.CombinedOutput()
			require.NoError(t, restErr, "restored packaged execution must succeed (%s, %s): %s", pa.name, mode, string(restOut))
			require.Contains(t, string(restOut), payloadSuccessMarker)
		}
	}
}

func TestABI_MissingInterpreter_ClassifierMatrix(t *testing.T) {
	t.Parallel()

	dummyExitErr := errors.New("exit status 1")

	t.Run("valid_memfd_exec_failure", func(t *testing.T) {
		out := `[microfat] {"event":"error","stage":"memfd_exec","error":"execve on /proc/self/fd/3 failed: no such file or directory"}`
		require.NoError(t, classifyMissingInterpreterFailure(out, dummyExitErr, execModeMemfd))
	})

	t.Run("valid_cache_exec_failure", func(t *testing.T) {
		out := `[microfat] {"event":"error","stage":"cache_exec","error":"cache execve failed on /proc/self/fd/4: no such file or directory"}`
		require.NoError(t, classifyMissingInterpreterFailure(out, dummyExitErr, execModeCache))
	})

	t.Run("successful_exit_rejected", func(t *testing.T) {
		out := `success`
		err := classifyMissingInterpreterFailure(out, nil, execModeMemfd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected execution to fail")
	})

	t.Run("success_marker_present_rejected", func(t *testing.T) {
		out := `[microfat] {"stage":"memfd_exec","error":"no such file or directory"}` + payloadSuccessMarker
		err := classifyMissingInterpreterFailure(out, dummyExitErr, execModeMemfd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "payload success marker found")
	})

	t.Run("earlier_stage_permission_denied_rejected", func(t *testing.T) {
		out := `[microfat] {"stage":"memfd_create","error":"memfd_create failed: permission denied"}`
		err := classifyMissingInterpreterFailure(out, dummyExitErr, execModeMemfd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "classified as setup/security failure")
	})

	t.Run("earlier_stage_cache_dir_init_rejected", func(t *testing.T) {
		out := `[microfat] {"stage":"cache_dir_init","error":"unable to initialize cache: permission denied"}`
		err := classifyMissingInterpreterFailure(out, dummyExitErr, execModeCache)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "classified as setup/security failure")
	})

	t.Run("earlier_stage_integrity_failure_rejected", func(t *testing.T) {
		out := `[microfat] {"stage":"integrity","error":"integrity verification failed: checksum mismatch"}`
		err := classifyMissingInterpreterFailure(out, dummyExitErr, execModeMemfd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "classified as setup/security failure")
	})

	t.Run("wrong_execution_mode_rejected", func(t *testing.T) {
		out := `[microfat] {"stage":"cache_exec","error":"cache execve failed on /proc/self/fd/4: no such file or directory"}`
		err := classifyMissingInterpreterFailure(out, dummyExitErr, execModeMemfd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failure occurred outside expected memfd execution stage")
	})

	t.Run("empty_stderr_arbitrary_exit_rejected", func(t *testing.T) {
		err := classifyMissingInterpreterFailure("", dummyExitErr, execModeMemfd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failure occurred outside expected memfd execution stage")
	})
}

func TestABI_GenuineMismatchPackagingMatrix(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "mixedpkg")
	require.NoError(t, os.MkdirAll(pkgDir, defaultFilePerm))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module mixedfixture\ngo 1.27.1\n"), privateFilePerm))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte(`package main
import "fmt"
func main() { fmt.Println("genuine mixed payload") }
`), privateFilePerm))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "cgo.go"), []byte(`//go:build cgo_variant
package main
// #include <stdlib.h>
import "C"
`), privateFilePerm))

	v1Label, v2Label := hostVariantLabels()

	manifest := map[string]any{
		"app_name":      "mixedapp",
		"package":       "./mixedpkg",
		"output":        "mixed-manifest.fat",
		"target_os":     "linux",
		"target_arch":   currentHostArch,
		manifestKeyStub: stubPath,
		"variants": []map[string]any{
			{"level": v1Label, "env": map[string]string{"CGO_ENABLED": "0"}},
			{"level": v2Label, "flags": []string{"-tags=cgo_variant"}, "env": map[string]string{"CGO_ENABLED": "1"}},
		},
	}
	manifestPath := filepath.Join(dir, "build.json")
	encoded, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, encoded, privateFilePerm))

	for _, entryPoint := range []string{inputPackCommand, inputPGOPackCommand} {
		entryPoint := entryPoint
		t.Run("entrypoint_"+entryPoint, func(t *testing.T) {
			outFat := filepath.Join(dir, "canary-"+entryPoint+".fat")
			const canary = "PRESERVED_CANARY_DO_NOT_DELETE"
			require.NoError(t, os.WriteFile(outFat, []byte(canary), privateFilePerm))

			// 1. Default policy must reject and preserve canary
			cmdReject := exec.Command(cliPath, entryPoint, "--manifest", manifestPath, "-o", outFat)
			cmdReject.Dir = dir
			outReject, errReject := cmdReject.CombinedOutput()
			require.Error(t, errReject, "%s default must reject mixed ABI", entryPoint)
			require.Contains(t, string(outReject), "declared ABI requirements mismatch")

			canaryBytes, cErr := os.ReadFile(outFat)
			require.NoError(t, cErr)
			require.Equal(t, canary, string(canaryBytes), "canary must be preserved on reject")

			// 2. Override flag must allow packaging with [OVERRIDDEN] warning
			cmdAllow := exec.Command(cliPath, entryPoint, "--manifest", manifestPath, "-o", outFat, "--allow-mixed-abi")
			cmdAllow.Dir = dir
			outAllow, errAllow := cmdAllow.CombinedOutput()
			require.NoError(t, errAllow, "%s with --allow-mixed-abi must succeed: %s", entryPoint, string(outAllow))
			require.Contains(t, string(outAllow), "[OVERRIDDEN]")

			// 3. Verify resulting binary passes integrity check
			verifyCmd := exec.Command(cliPath, "verify", outFat)
			verifyOut, vErr := verifyCmd.CombinedOutput()
			require.NoError(t, vErr, "verify %s output failed: %s", entryPoint, string(verifyOut))

			// 4. Executable payload runs
			execCmd := exec.Command(outFat)
			execOut, eErr := execCmd.CombinedOutput()
			require.NoError(t, eErr, "running fat binary failed: %s", string(execOut))
			require.Contains(t, string(execOut), "genuine mixed payload")
		})
	}
}
