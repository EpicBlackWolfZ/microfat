package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type payloadReport struct {
	OriginalExeHint string   `json:"original_exe_hint"`
	Executable      string   `json:"executable"`
	Args            []string `json:"args"`
}

func TestWhitespacePathExecution(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	reporterPkg := "./testdata/whitespace_reporter"
	reporterBin := filepath.Join(baseDir, "reporter_variant")
	require.NoError(t, compileBinary(reporterPkg, reporterBin, nil))

	decoyPkg := "./testdata/golden"
	decoyBin := filepath.Join(baseDir, "decoy_variant")
	require.NoError(t, compileBinary(decoyPkg, decoyBin, nil))

	baseLevel := "v1"
	if currentHostArch == archARM64 {
		baseLevel = manifestARM64Base
	}

	for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
		t.Run(profile, func(t *testing.T) {
			stub := stubPath
			if profile == launcherMinimalProfile {
				stub = filepath.Join(baseDir, "minimal-stub-"+profile)
				stubEnv := []string{"GOAMD64=" + baseLevel, "GOARM64=" + manifestARM64Base}
				require.NoError(t, compileBinaryWithFlags(stubPackagePath, stub, stubEnv, "-tags=minimal", "-ldflags=-s -w"))
			}

			for _, mode := range []string{format.ExecModeMemfd, format.ExecModeCache} {
				t.Run(mode, func(t *testing.T) {
					testDir := t.TempDir()
					cacheDir := filepath.Join(testDir, "private_cache")
					require.NoError(t, os.MkdirAll(cacheDir, 0o700))

					// 1. Trailing whitespace in filename: "app "
					targetFat := filepath.Join(testDir, "app ")
					require.NoError(t, packBinary(cliPath, stub, "ws-app", targetFat, map[string]string{baseLevel: reporterBin}))

					// 2. Decoy sibling in same directory without trailing whitespace: "app"
					decoyFat := filepath.Join(testDir, "app")
					require.NoError(t, packBinary(cliPath, stub, "decoy-app", decoyFat, map[string]string{baseLevel: decoyBin}))

					commonEnv := []string{
						"MICROFAT_EXEC_MODE=" + mode,
						"MICROFAT_CACHE_DIR=" + cacheDir,
						"XDG_CACHE_HOME=" + cacheDir,
					}

					// Test execution of target with trailing space
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()

					testArgs := []string{"--flag", "param with space", "literal\targ"}
					cmd := exec.CommandContext(ctx, targetFat, testArgs...)
					cmd.Env = append(os.Environ(), commonEnv...)
					// Inject spoofed inherited hint to verify launcher scrubs it
					cmd.Env = append(cmd.Env, "MICROFAT_ORIGINAL_EXE=/spoofed/fake/hint")

					var stdoutBuf, stderrBuf bytes.Buffer
					cmd.Stdout = &stdoutBuf
					cmd.Stderr = &stderrBuf

					err := cmd.Run()
					require.NoError(t, err, "execution failed; stderr:\n%s", stderrBuf.String())

					var rep payloadReport
					require.NoError(t, json.Unmarshal(stdoutBuf.Bytes(), &rep), "failed to decode JSON output: %s", stdoutBuf.String())

					// Assert exact whitespace preservation
					assert.Equal(t, targetFat, rep.OriginalExeHint, "original exe hint must preserve trailing space")
					assert.Equal(t, targetFat, rep.Executable, "runtimeinit.Executable must preserve trailing space")
					assert.NotEqual(t, decoyFat, rep.OriginalExeHint, "hint must not collapse to decoy")
					assert.NotEqual(t, decoyFat, rep.Executable, "executable must not collapse to decoy")
					assert.Equal(t, testArgs, rep.Args, "arguments must be preserved faithfully")

					// Test execution of decoy binary has distinct behavior
					ctxDecoy, cancelDecoy := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancelDecoy()
					cmdDecoy := exec.CommandContext(ctxDecoy, decoyFat, "--echo-args", "arg1", "arg2")
					cmdDecoy.Env = append(os.Environ(), commonEnv...)
					decoyOut, errDecoy := cmdDecoy.Output()
					require.NoError(t, errDecoy)
					assert.Equal(t, "arg1||arg2", string(decoyOut), "decoy binary should execute its own variant")

					// Test exit fidelity
					ctxFail, cancelFail := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancelFail()
					cmdFail := exec.CommandContext(ctxFail, targetFat, "--fail")
					cmdFail.Env = append(os.Environ(), commonEnv...)
					failErr := cmdFail.Run()
					require.Error(t, failErr, "expected exit failure")
					if exitErr, ok := failErr.(*exec.ExitError); ok {
						assert.Equal(t, 42, exitErr.ExitCode(), "exit code must match payload exit code")
					} else {
						t.Fatalf("expected ExitError, got %v", failErr)
					}

					// 3. Leading space in filename: " leading"
					leadingFat := filepath.Join(testDir, " leading")
					require.NoError(t, packBinary(cliPath, stub, "leading-app", leadingFat, map[string]string{baseLevel: reporterBin}))

					ctxLead, cancelLead := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancelLead()
					cmdLead := exec.CommandContext(ctxLead, leadingFat)
					cmdLead.Env = append(os.Environ(), commonEnv...)
					leadOut, errLead := cmdLead.Output()
					require.NoError(t, errLead)
					var leadRep payloadReport
					require.NoError(t, json.Unmarshal(leadOut, &leadRep))
					assert.Equal(t, leadingFat, leadRep.OriginalExeHint)
					assert.Equal(t, leadingFat, leadRep.Executable)

					// 4. Directory containing literal whitespace
					spacesDir := filepath.Join(testDir, "directory with spaces")
					require.NoError(t, os.MkdirAll(spacesDir, 0o755))
					spacesFat := filepath.Join(spacesDir, "myfat")
					require.NoError(t, packBinary(cliPath, stub, "spaces-dir-app", spacesFat, map[string]string{baseLevel: reporterBin}))

					ctxSpaces, cancelSpaces := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancelSpaces()
					cmdSpaces := exec.CommandContext(ctxSpaces, spacesFat)
					cmdSpaces.Env = append(os.Environ(), commonEnv...)
					spacesOut, errSpaces := cmdSpaces.Output()
					require.NoError(t, errSpaces)
					var spacesRep payloadReport
					require.NoError(t, json.Unmarshal(spacesOut, &spacesRep))
					assert.Equal(t, spacesFat, spacesRep.OriginalExeHint)
					assert.Equal(t, spacesFat, spacesRep.Executable)
				})
			}
		})
	}
}

func TestStubPath_WhitespaceDataAndEmptyRejection(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	stubBytes, err := os.ReadFile(stubPath)
	require.NoError(t, err)

	// 1. Create a trusted readable stub with a bare whitespace-only basename: " "
	bareStubPath := filepath.Join(workDir, " ")
	require.NoError(t, os.WriteFile(bareStubPath, stubBytes, 0o755))

	// 2. Create an absolute whitespace-ending stub path: "stub_ends_with_space "
	absEndingWSPath := filepath.Join(workDir, "stub_ends_with_space ")
	require.NoError(t, os.WriteFile(absEndingWSPath, stubBytes, 0o755))

	// 3. Create a readable non-executable stub (mode 0644)
	nonExecStubPath := filepath.Join(workDir, "stub_mode_0644")
	require.NoError(t, os.WriteFile(nonExecStubPath, stubBytes, 0o644))

	variantBin := goldenVariantBins[currentHostLevel]

	// Subtest 1: --stub= rejection across direct pack
	t.Run("DirectPack_StubEqualsEmpty_Rejected", func(t *testing.T) {
		cmd := exec.Command(cliPath, "pack", "--stub=", "--arch", currentHostArch, "-v", currentHostLevel+"="+variantBin, "-o", "out.fat")
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		require.Error(t, err)
		assert.Contains(t, string(out), "flag --stub cannot be empty")
	})

	// Subtest 2: --stub "" (empty string argument) rejected
	t.Run("DirectPack_StubEmptyString_Rejected", func(t *testing.T) {
		cmd := exec.Command(cliPath, "pack", "--stub", "", "--arch", currentHostArch, "-v", currentHostLevel+"="+variantBin, "-o", "out.fat")
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		require.Error(t, err)
		assert.Contains(t, string(out), "flag --stub cannot be empty")
	})

	// Subtest 3: --stub " " (bare whitespace-only pathname) succeeds when file " " exists in working directory
	t.Run("DirectPack_BareWhitespaceStub_SucceedsWhenFileExists", func(t *testing.T) {
		fatOut := filepath.Join(workDir, "bare_ws.fat")
		cmd := exec.Command(cliPath, "pack", "--stub", " ", "--arch", currentHostArch, "-v", currentHostLevel+"="+variantBin, "-o", fatOut)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "pack with bare whitespace stub failed: %s", string(out))

		// Verify binary integrity and execution
		verifyFatIntegrity(t, fatOut)
		runCmd := exec.Command(fatOut)
		runOut, runErr := runCmd.CombinedOutput()
		require.NoError(t, runErr, "exec failed: %s", string(runOut))
		assert.Contains(t, string(runOut), "golden:variant=")
	})

	// Subtest 4: Missing whitespace-only stub fails without falling back to auto-discovery
	t.Run("DirectPack_BareWhitespaceStub_FailsWhenMissingWithoutFallback", func(t *testing.T) {
		emptyDir := t.TempDir()
		fatOut := filepath.Join(emptyDir, "should_fail.fat")
		cmd := exec.Command(cliPath, "pack", "--stub", " ", "--arch", currentHostArch, "-v", currentHostLevel+"="+variantBin, "-o", fatOut)
		cmd.Dir = emptyDir
		out, err := cmd.CombinedOutput()
		require.Error(t, err, "pack must fail when whitespace stub file does not exist")
		assert.NotContains(t, string(out), "flag --stub cannot be empty")
		assert.NotContains(t, string(out), "Using auto-discovered launcher stub")
		assert.Contains(t, string(out), "launcher stub")
	})

	// Subtest 5: Absolute whitespace-ending pathname succeeds
	t.Run("DirectPack_AbsoluteWhitespaceEndingStub_Succeeds", func(t *testing.T) {
		fatOut := filepath.Join(workDir, "abs_ws.fat")
		cmd := exec.Command(cliPath, "pack",
			"--stub", absEndingWSPath,
			"--arch", currentHostArch,
			"-v", currentHostLevel+"="+variantBin,
			"-o", fatOut,
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "pack with whitespace-ending stub path failed: %s", string(out))
		verifyFatIntegrity(t, fatOut)
	})

	// Subtest 6: Profile whitespace rejection
	t.Run("DirectPack_WhitespaceProfile_Rejected", func(t *testing.T) {
		cmd := exec.Command(cliPath, "pack",
			"--stub-profile", "   ",
			"--arch", currentHostArch,
			"-v", currentHostLevel+"="+variantBin,
			"-o", "out.fat",
		)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		require.Error(t, err)
		assert.Contains(t, string(out), "flag --stub-profile cannot be empty")
	})

	// Subtest 7: Readable non-executable stub (0644) succeeds as input data
	t.Run("DirectPack_NonExecutableStub_SucceedsAsInputData", func(t *testing.T) {
		fatOut := filepath.Join(workDir, "nonexec_stub.fat")
		cmd := exec.Command(cliPath, "pack",
			"--stub", nonExecStubPath,
			"--arch", currentHostArch,
			"-v", currentHostLevel+"="+variantBin,
			"-o", fatOut,
		)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "pack with non-executable stub failed: %s", string(out))
		verifyFatIntegrity(t, fatOut)
	})

	// Subtest 8 & 9: Manifest pack and pgo-pack with bare whitespace stub
	pkgDir := filepath.Join(workDir, "src", "sample_ws")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	mainSource := `package main
import "fmt"
func main() { fmt.Println("WS_MANIFEST_OK") }
`
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte(mainSource), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module samplews\ngo 1.27.1\n"), 0o644))

	manifestContent := `name: ws-manifest-app
package: ` + pkgDir + `
target_os: linux
target_arch: ` + currentHostArch + `
stub: " "
variants:
  - level: ` + currentHostLevel + `
`
	manifestPath := filepath.Join(workDir, "manifest_ws.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifestContent), 0o644))

	t.Run("ManifestPack_BareWhitespaceStub_Succeeds", func(t *testing.T) {
		fatOut := filepath.Join(workDir, "manifest_ws.fat")
		cmd := exec.Command(cliPath, "pack", "--manifest", manifestPath, "-o", fatOut)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "manifest pack with bare whitespace stub failed: %s", string(out))
		verifyFatIntegrity(t, fatOut)
	})

	t.Run("ManifestPack_CLIStubEmpty_Rejected", func(t *testing.T) {
		cmd := exec.Command(cliPath, "pack", "--manifest", manifestPath, "--stub=", "-o", "fail.fat")
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		require.Error(t, err)
		assert.Contains(t, string(out), "flag --stub cannot be empty")
	})

	t.Run("PgoPack_BareWhitespaceStub_Succeeds", func(t *testing.T) {
		fatOut := filepath.Join(workDir, "pgo_ws.fat")
		cmd := exec.Command(cliPath, "pgo-pack", "--manifest", manifestPath, "--stub", " ", "-o", fatOut)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "pgo-pack with bare whitespace stub failed: %s", string(out))
		verifyFatIntegrity(t, fatOut)
	})

	t.Run("PgoPack_CLIStubEmpty_Rejected", func(t *testing.T) {
		cmd := exec.Command(cliPath, "pgo-pack", "--manifest", manifestPath, "--stub=", "-o", "fail.fat")
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		require.Error(t, err)
		assert.Contains(t, string(out), "flag --stub cannot be empty")
	})
}
