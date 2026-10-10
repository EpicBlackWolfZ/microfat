package e2e_test

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	lifecycleInvalidPolicyFlag = "--microfat:metadata-policy=invalid"
	lifecycleOrdinaryArgument  = "ordinary"
)

func TestLauncherLifecycleOptionRouting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	payload := filepath.Join(dir, "argv-reporter")
	stubEnv := []string{envBaselineAMD64, "GOARM64=" + manifestARM64Base}
	require.NoError(t, compileBinary("./testdata/whitespace_reporter", payload, stubEnv))

	profiles := []struct {
		name string
		tags string
	}{
		{name: launcherFullProfile, tags: "-tags="},
		{name: launcherMinimalProfile, tags: "-tags=minimal"},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			t.Parallel()
			profileDir := t.TempDir()
			stub := filepath.Join(profileDir, "stub")
			require.NoError(t, compileBinaryWithFlags(stubPackagePath, stub, stubEnv, profile.tags, "-ldflags=-s -w"))
			fat := filepath.Join(profileDir, "argv.fat")
			require.NoError(t, packBinary(cliPath, stub, "lifecycle-routing", fat,
				map[string]string{currentHostLevel: payload}))

			t.Run("PayloadArguments", func(t *testing.T) {
				t.Parallel()
				testLifecyclePayloadArguments(t, fat)
			})
			if profile.name == launcherFullProfile {
				t.Run("NonTransformingCommands", func(t *testing.T) {
					t.Parallel()
					testLifecycleNonTransformingCommands(t, fat)
				})
				t.Run("InvalidTransformationPolicy", func(t *testing.T) {
					t.Parallel()
					testLifecycleTransformationPolicy(t, fat)
				})
			}
		})
	}
}

func testLifecyclePayloadArguments(t *testing.T, fat string) {
	t.Helper()
	tests := []struct {
		name string
		args []string
	}{
		{name: "InvalidPolicyAfterPayloadFlag", args: []string{"--echo-args", lifecycleInvalidPolicyFlag}},
		{name: "InvalidPolicyAfterOrdinaryArgument", args: []string{lifecycleOrdinaryArgument, lifecycleInvalidPolicyFlag}},
		{name: "StrictPolicy", args: []string{lifecycleOrdinaryArgument, "--microfat:metadata-policy=strict"}},
		{name: "StripPolicy", args: []string{lifecycleOrdinaryArgument, "--microfat:metadata-policy=strip"}},
		{name: "BreakHardlinks", args: []string{lifecycleOrdinaryArgument, "--microfat:break-hardlinks"}},
		{name: "LeadingSentinel", args: []string{"--", lifecycleInvalidPolicyFlag, "--microfat:trim"}},
		{name: "SentinelAfterPayloadFlag", args: []string{"--echo-args", "--", lifecycleInvalidPolicyFlag}},
		{name: "ReservedCommandsAfterOrdinaryArgument", args: []string{
			lifecycleOrdinaryArgument, "--microfat:help", "--microfat:info=json", "--microfat:prewarm", "--microfat:optimize",
			"--microfat:trim", "--microfat:specialize", "--microfat:optimize-to=destination", "--microfat:trim-to",
			"destination", "--microfat:specialize-to=destination",
		}},
		{name: "MixedOptionsAndWhitespace", args: []string{
			lifecycleOrdinaryArgument, "arg with spaces", "", "--microfat:break-hardlinks", "--microfat:metadata-policy=strip",
			"--", lifecycleInvalidPolicyFlag, "literal\targument",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := []string{"MICROFAT_CACHE_DIR=" + t.TempDir()}
			stdout, stderr, code, err := executeFatBinary(t, fat, env, tt.args...)
			require.NoError(t, err, "payload execution failed: %s", stderr)
			require.Equal(t, defaultExitCode, code, stderr)

			var report payloadReport
			require.NoError(t, json.Unmarshal([]byte(stdout), &report), "payload output: %s", stdout)
			require.Equal(t, tt.args, report.Args, "launcher must preserve every payload argument")
		})
	}
}

func testLifecycleNonTransformingCommands(t *testing.T, fat string) {
	t.Helper()
	tests := []struct {
		name    string
		command string
		output  string
	}{
		{name: "Help", command: "--microfat:help", output: "Microfat Universal Launcher"},
		{name: "Info", command: "--microfat:info", output: "App Name:"},
		{name: "Prewarm", command: "--microfat:prewarm", output: "Prewarmed variant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := []string{"MICROFAT_CACHE_DIR=" + t.TempDir()}
			stdout, stderr, code, err := executeFatBinary(t, fat, env,
				tt.command, lifecycleInvalidPolicyFlag, "--microfat:break-hardlinks")
			require.NoError(t, err, "non-transforming command must ignore lifecycle options: %s", stderr)
			require.Equal(t, defaultExitCode, code, stderr)
			require.Contains(t, stdout, tt.output)
			require.NotContains(t, stdout, "original_exe_hint", "meta-command must not execute the payload")
		})
	}
}

func testLifecycleTransformationPolicy(t *testing.T, fat string) {
	t.Helper()
	beforeData, err := os.ReadFile(fat)
	require.NoError(t, err)
	beforeHash := sha256.Sum256(beforeData)
	tests := []struct {
		name        string
		command     string
		destination string
	}{
		{name: "Optimize", command: "--microfat:optimize"},
		{name: "Trim", command: "--microfat:trim"},
		{name: "Specialize", command: "--microfat:specialize"},
		{name: "OptimizeToEquals", command: "--microfat:optimize-to", destination: "equals"},
		{name: "OptimizeToSeparate", command: "--microfat:optimize-to", destination: "separate"},
		{name: "TrimToEquals", command: "--microfat:trim-to", destination: "equals"},
		{name: "TrimToSeparate", command: "--microfat:trim-to", destination: "separate"},
		{name: "SpecializeToEquals", command: "--microfat:specialize-to", destination: "equals"},
		{name: "SpecializeToSeparate", command: "--microfat:specialize-to", destination: "separate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source := filepath.Join(dir, "source.fat")
			copyFile(t, fat, source)
			before, err := os.Stat(source)
			require.NoError(t, err)
			destination := filepath.Join(dir, "destination")
			args := []string{tt.command}
			switch tt.destination {
			case "equals":
				args[0] += "=" + destination
			case "separate":
				args = append(args, destination)
			}
			args = append(args, lifecycleInvalidPolicyFlag, "--microfat:break-hardlinks")
			_, stderr, code, err := executeFatBinary(t, source, nil, args...)
			require.Error(t, err, "transformation must reject an invalid metadata policy")
			require.NotEqual(t, defaultExitCode, code)
			require.Contains(t, stderr, "invalid metadata policy")

			after, err := os.Stat(source)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after), "rejected transformation must preserve the source inode")
			require.Equal(t, before.Mode(), after.Mode())
			require.Equal(t, before.ModTime(), after.ModTime())
			afterData, err := os.ReadFile(source)
			require.NoError(t, err)
			require.Equal(t, beforeHash, sha256.Sum256(afterData), "rejected transformation must preserve source bytes")
			_, err = os.Lstat(destination)
			require.True(t, os.IsNotExist(err), "rejected transformation must not create a destination: %v", err)
		})
	}
}
