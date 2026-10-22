//go:build linux

package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const updateQualification = "MICROFAT_UPDATE_TESTS"
const updateFixtureVersion = "0.3.0"
const updateCandidateVersion = "0.3.1"

type updateCLIResult struct {
	Status       string  `json:"status"`
	Current      *string `json:"current_version"`
	Running      *string `json:"running_version"`
	Target       *string `json:"target_version"`
	Available    bool    `json:"update_available"`
	Activated    bool    `json:"activated"`
	Verification string  `json:"verification"`
	Management   string  `json:"management"`
}

func updateLaunchers(t *testing.T) (map[string]string, string) {
	t.Helper()
	return updateLaunchersForVersion(t, updateFixtureVersion)
}

func updateLaunchersForVersion(t *testing.T, version string) (map[string]string, string) {
	t.Helper()
	dir := t.TempDir()
	cli := filepath.Join(dir, "cli")
	require.NoError(t, compileBinaryWithFlags(cliPackagePath, cli, []string{envBaselineAMD64, "GOARM64=v8.0"},
		"-ldflags=-X github.com/EpicBlackWolfZ/microfat/internal/version.Version="+version))
	minimal := filepath.Join(dir, "minimal")
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, minimal, []string{envBaselineAMD64, "GOARM64=v8.0"}, "-tags=minimal"))
	level := updateTier()
	launchers := map[string]string{execModeNative: cli}
	for profile, stub := range map[string]string{launcherFullProfile: stubPath, launcherMinimalProfile: minimal} {
		fat := filepath.Join(dir, profile)
		require.NoError(t, packBinary(cli, stub, "update-cli", fat, map[string]string{level: cli}))
		launchers[profile] = fat
	}
	return launchers, minimal
}

func updateTier() string {
	if currentHostArch == archARM64 {
		return manifestARM64Base
	}
	return "v1"
}

func updateCommand(t *testing.T, executable, mode string, args ...string) (updateCLIResult, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, executable, append([]string{"update", "--json"}, args...)...)
	command.Env = append(os.Environ(), "MICROFAT_EXEC_MODE="+mode, "PATH=/nonexistent")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	var result updateCLIResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result), "stdout=%s stderr=%s", stdout.String(), stderr.String())
	require.NoError(t, ctx.Err(), "bounded updater did not complete")
	return result, stderr.String(), err
}

func TestUpdateRejectsStaleRunningGeneration(t *testing.T) {
	launchers, minimal := updateLaunchers(t)
	for profile, cli := range launchers {
		for _, mode := range []string{execModeMemfd, execModeCache} {
			t.Run(profile+"/"+mode, func(t *testing.T) {
				bin, store, old := managedLayoutFixture(t, cli, minimal)
				args := []string{"update"}
				if os.Geteuid() == 0 {
					args = append(args, "--system")
				}
				environment := []string{"MICROFAT_EXEC_MODE=" + mode, "PATH=/nonexistent"}
				output, err := runPausedImage(t, filepath.Join(bin, "microfat"), args, environment, func() {
					require.NoError(t, os.Symlink(filepath.Join("generations", strings.Repeat("2", 32)), filepath.Join(store, "next")))
					require.NoError(t, os.Rename(filepath.Join(store, "next"), filepath.Join(store, "current")))
				})
				require.Error(t, err, output)
				require.Contains(t, output, "different generation is active")
				require.FileExists(t, filepath.Join(old, "microfat"))
				selected, err := os.Readlink(filepath.Join(store, "current"))
				require.NoError(t, err)
				require.Equal(t, filepath.Join("generations", strings.Repeat("2", 32)), selected)
			})
		}
	}
}

func TestUpdateExternalRefusal(t *testing.T) {
	launchers, _ := updateLaunchers(t)
	cli := launchers[execModeNative]
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(cli), "microfat-distribution.json"),
		[]byte(`{"schema":1,"owner":"homebrew"}`), 0o644))
	result, stderr, err := updateCommand(t, cli, execModeMemfd)
	require.Error(t, err)
	assert.Equal(t, "homebrew", result.Management)
	assert.Contains(t, stderr, "brew upgrade microfat")
	assert.False(t, result.Activated)
}

func TestUpdatePublishedRelease(t *testing.T) {
	if os.Getenv(updateQualification) != "required" {
		t.Skip("native authenticated updater qualification is opt-in and mandatory in installer CI")
	}
	// This source-built updater exercises real signed older releases. The tag
	// workflow separately qualifies the exact signed candidate's updater.
	t.Logf("source-built updater fixture v%s; authenticated published targets v0.2.5 and v%s",
		updateCandidateVersion, updateFixtureVersion)
	launchers, minimal := updateLaunchersForVersion(t, updateCandidateVersion)
	verifier, err := exec.LookPath("cosign")
	require.NoError(t, err)
	verifier, err = filepath.EvalSymlinks(verifier)
	require.NoError(t, err)
	verifier, err = filepath.Abs(verifier)
	require.NoError(t, err)
	data, err := os.ReadFile(verifier)
	require.NoError(t, err)
	pin := fmt.Sprintf("%x", sha256.Sum256(data)) // independently provisioned by the pinned CI installer
	for _, target := range []string{"0.2.5", updateFixtureVersion} {
		for profile, cli := range launchers {
			modes := []string{execModeMemfd, execModeCache}
			if profile == execModeNative {
				modes = []string{execModeMemfd}
			}
			for _, mode := range modes {
				t.Run(target+"/"+profile+"/"+mode, func(t *testing.T) {
					bin, store, old := managedLayoutFixture(t, cli, minimal)
					writeManagedGeneration(t, store, filepath.Base(old), updateCandidateVersion, cli, stubPath, minimal, nil)
					executable := filepath.Join(bin, "microfat")
					before, err := os.Readlink(filepath.Join(store, "current"))
					require.NoError(t, err)
					check, stderr, err := updateCommand(t, executable, mode, "--check", "--version", target)
					require.NoError(t, err, stderr)
					assert.Equal(t, "metadata_only", check.Verification)
					assert.False(t, check.Available)
					require.NotNil(t, check.Running)
					assert.Equal(t, updateCandidateVersion, *check.Running)
					require.NotNil(t, check.Target)
					assert.Equal(t, target, *check.Target)
					after, err := os.Readlink(filepath.Join(store, "current"))
					require.NoError(t, err)
					assert.Equal(t, before, after)
					args := []string{"--version", target, "--allow-downgrade", "--cosign", verifier, "--cosign-sha256", pin, "--staging-dir", t.TempDir()}
					if profile == execModeNative {
						// Exercise automatic independently pinned verifier acquisition with no PATH tools.
						args = []string{"--version", target, "--allow-downgrade", "--staging-dir", t.TempDir()}
					}
					if os.Geteuid() == 0 {
						args = append(args, "--system")
					}
					result, stderr, err := updateCommand(t, executable, mode, args...)
					require.NoError(t, err, stderr)
					assert.True(t, result.Activated)
					assert.Equal(t, "artifact_verified", result.Verification)
					require.NotNil(t, result.Running)
					assert.Equal(t, updateCandidateVersion, *result.Running)
					require.NotNil(t, result.Current)
					assert.Equal(t, target, *result.Current)
					assert.FileExists(t, filepath.Join(old, "microfat"))
					verifyUpdatedGeneration(t, store)
					t.Logf("authenticated target v%s; host=%s; toolchain=%s; launcher=%s; mode=%s", target,
						runtime.GOARCH, runtime.Version(), profile, mode)
					command := exec.CommandContext(t.Context(), executable, "--version")
					output, err := command.CombinedOutput()
					require.NoError(t, err, string(output))
					assert.Contains(t, string(output), target)
					for _, stubProfile := range []string{launcherFullProfile, launcherMinimalProfile} {
						packed := filepath.Join(t.TempDir(), "payload")
						args := []string{"pack", "--arch", currentHostArch, "--name", "updated-companion",
							"-v", updateTier() + "=" + goldenVariantBins[updateTier()], "-o", packed}
						if stubProfile == launcherMinimalProfile {
							args = append(args, "--stub", filepath.Join(bin, "microfat-stub-minimal"))
						}
						command := exec.CommandContext(t.Context(), executable, args...)
						command.Env = append(os.Environ(), "MICROFAT_EXEC_MODE="+mode, "PATH=/nonexistent")
						output, err := command.CombinedOutput()
						require.NoError(t, err, string(output))
						output, err = exec.CommandContext(t.Context(), packed).CombinedOutput()
						require.NoError(t, err, string(output))
					}
				})
			}
		}
	}
}

func verifyUpdatedGeneration(t *testing.T, store string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(store, "current", "generation.json"))
	require.NoError(t, err)
	var generation struct {
		Files map[string]struct {
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(data, &generation))
	require.Len(t, generation.Files, 3)
	for name, metadata := range generation.Files {
		data, err := os.ReadFile(filepath.Join(store, "current", name))
		require.NoError(t, err)
		assert.Equal(t, metadata.Size, int64(len(data)))
		assert.Equal(t, metadata.SHA256, fmt.Sprintf("%x", sha256.Sum256(data)))
	}
}
