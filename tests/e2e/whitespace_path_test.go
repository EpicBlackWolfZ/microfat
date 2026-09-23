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
