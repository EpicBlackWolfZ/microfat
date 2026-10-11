//go:build linux

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	stubInfoModeAuto   = "auto"
	stubInfoInherited  = "inherited"
	stubInfoUnset      = "unset"
	stubInfoEnabled    = "enabled"
	stubInfoDisabled   = "disabled"
	stubInfoDryRun     = "dry-run"
	stubInfoNativeCPU  = "native"
	stubInfoStaticCPU  = "static"
	stubInfoCacheNote  = "cache-first"
	stubInfoJSONFlag   = "--microfat:info=json"
	stubInfoTextFlag   = "--microfat:info"
	stubInfoPayloadTag = "golden:variant="
	stubInfoMaxProcs   = "GOMAXPROCS"
)

type stubInfoEnvironmentValue struct {
	Present bool   `json:"present"`
	Value   string `json:"value"`
	Source  string `json:"source"`
}

type stubInfoRuntimeCandidate struct {
	ExecMode             string                              `json:"exec_mode"`
	RetainedBytes        int64                               `json:"retained_executable_bytes"`
	EffectiveEnvironment map[string]stubInfoEnvironmentValue `json:"effective_environment"`
}

type stubInfoProcessReport struct {
	ExecMode string `json:"exec_mode"`
	Runtime  struct {
		Autotune   string                     `json:"autotune"`
		CPUPolicy  string                     `json:"cpu_policy"`
		Candidates []stubInfoRuntimeCandidate `json:"candidates"`
	} `json:"runtime"`
}

// A complete child environment makes unset runtime settings observable even
// when the test runner itself has resource or launcher overrides configured.
func stubInfoProcessEnvironment(cacheDir string, overrides ...string) []string {
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "MICROFAT_") || key == "GODEBUG" || key == stubInfoMaxProcs || key == "GOMEMLIMIT" || key == "GOGC" {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "MICROFAT_CACHE_DIR="+cacheDir)
	return append(env, overrides...)
}

func runStubInfoProcess(t *testing.T, env []string, args ...string) string {
	t.Helper()
	const timeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	err := runFixtureCommand(func() *exec.Cmd {
		cmd := exec.CommandContext(ctx, goldenFatBin, args...)
		cmd.Env = env
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		return cmd
	})
	require.NoError(t, ctx.Err(), "launcher blocked: %s", stderr.String())
	require.NoError(t, err, "launcher failed: %s", stderr.String())
	return stdout.String()
}

func readStubInfoProcessReport(t *testing.T, env []string) stubInfoProcessReport {
	t.Helper()
	stdout := runStubInfoProcess(t, env, stubInfoJSONFlag)
	require.NotContains(t, stdout, stubInfoPayloadTag, "info must not execute the application")
	var report stubInfoProcessReport
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), "info output: %s", stdout)
	return report
}

func TestLauncherInfoExecutionPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  []string
		mode string
	}{
		{name: "DefaultAuto", mode: stubInfoModeAuto},
		{name: "ExplicitAuto", env: []string{"MICROFAT_EXEC_MODE=auto"}, mode: stubInfoModeAuto},
		{name: "ExplicitCache", env: []string{envExecCache}, mode: execModeCache},
		{name: "ExplicitMemfd", env: []string{"MICROFAT_EXEC_MODE=memfd"}, mode: execModeMemfd},
		{name: "CaseInsensitiveCache", env: []string{"MICROFAT_EXEC_MODE=CACHE"}, mode: execModeCache},
		{name: "LegacyCache", env: []string{"MICROFAT_DISPATCH_MODE=cache"}, mode: execModeCache},
		{name: "LegacyMemfd", env: []string{"MICROFAT_EXEC_MODE=", "MICROFAT_DISPATCH_MODE=memfd"}, mode: execModeMemfd},
		{name: "PrimaryTakesPriority", env: []string{"MICROFAT_EXEC_MODE=cache", "MICROFAT_DISPATCH_MODE=memfd"}, mode: execModeCache},
		{name: "InvalidPrimaryUsesAuto", env: []string{"MICROFAT_EXEC_MODE=invalid", "MICROFAT_DISPATCH_MODE=cache"},
			mode: stubInfoModeAuto},
		{name: "InvalidLegacyUsesAuto", env: []string{"MICROFAT_DISPATCH_MODE=invalid"}, mode: stubInfoModeAuto},
	}
	for _, tt := range tests {
		for _, state := range []string{"AbsentCache", "PopulatedCache", "WarmCache"} {
			t.Run(tt.name+"/"+state, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				cacheDir := filepath.Join(root, "cache")
				entryPath := ""
				if state == "PopulatedCache" {
					require.NoError(t, os.Mkdir(cacheDir, privateDirPerm))
					entryPath = filepath.Join(cacheDir, "sentinel")
					require.NoError(t, os.WriteFile(entryPath, []byte("preserve cache contents"), privateFilePerm))
				} else if state == "WarmCache" {
					// Populate a verified entry before inspection: auto must still describe a
					// prospective policy when a cache hit is possible for later execution.
					prepareEnv := stubInfoProcessEnvironment(cacheDir, envExecCache)
					require.Equal(t, execModeCache, runStubInfoProcess(t, prepareEnv, "--echo-env", "MICROFAT_EXEC_MODE"))
					entries, err := os.ReadDir(cacheDir)
					require.NoError(t, err)
					require.Len(t, entries, 1)
					entryPath = filepath.Join(cacheDir, entries[0].Name())
				}
				var entryBefore os.FileInfo
				if entryPath != "" {
					var err error
					entryBefore, err = os.Stat(entryPath)
					require.NoError(t, err)
				}
				env := stubInfoProcessEnvironment(cacheDir, tt.env...)
				before := cacheTreeSnapshot(t, root)
				report := readStubInfoProcessReport(t, env)
				require.Equal(t, tt.mode, report.ExecMode)
				modes := make([]string, 0, len(report.Runtime.Candidates))
				for _, candidate := range report.Runtime.Candidates {
					modes = append(modes, candidate.ExecMode)
				}
				if tt.mode == stubInfoModeAuto {
					require.Equal(t, []string{execModeCache, execModeMemfd}, modes,
						"auto info describes both prospective paths before dispatch")
				} else {
					require.Equal(t, []string{tt.mode}, modes)
				}
				require.Equal(t, before, cacheTreeSnapshot(t, root), "JSON info changed the cache filesystem")
				stdout := runStubInfoProcess(t, env, stubInfoTextFlag)
				require.Contains(t, stdout, "Execution Policy:  "+tt.mode)
				require.NotContains(t, stdout, stubInfoPayloadTag, "info must not execute the application")
				if tt.mode == stubInfoModeAuto {
					require.Contains(t, stdout, stubInfoCacheNote)
				}
				require.Equal(t, before, cacheTreeSnapshot(t, root), "text info changed the cache filesystem")
				if entryPath != "" {
					entryAfter, err := os.Stat(entryPath)
					require.NoError(t, err)
					require.True(t, os.SameFile(entryBefore, entryAfter), "info replaced the cache inode")
					require.Equal(t, entryBefore.ModTime(), entryAfter.ModTime(), "info rewrote cache contents")
				}
			})
		}
	}
}

func TestLauncherInfoMatchesProspectiveRuntimeEnvironment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		env       []string
		autotune  string
		cpuPolicy string
		inherited bool
	}{
		{name: "ConfiguredTuning", env: []string{"MICROFAT_MEM_RATIO=0.5", "MICROFAT_GC_PROFILE=latency_critical"},
			autotune: stubInfoEnabled, cpuPolicy: stubInfoStaticCPU},
		{name: "NativeCPU", env: []string{"MICROFAT_CPU_POLICY=native"},
			autotune: stubInfoEnabled, cpuPolicy: stubInfoNativeCPU},
		{name: "AutotuneDisabled", env: []string{"MICROFAT_AUTOTUNE=false"},
			autotune: stubInfoDisabled, cpuPolicy: stubInfoStaticCPU},
		{name: "DryRun", env: []string{"MICROFAT_DRY_RUN=true"}, autotune: stubInfoDryRun, cpuPolicy: stubInfoStaticCPU},
		{name: "ExplicitOverrides", inherited: true, autotune: stubInfoEnabled, cpuPolicy: stubInfoStaticCPU},
		{name: "DisabledExplicitOverrides", env: []string{"MICROFAT_AUTOTUNE=0"},
			inherited: true, autotune: stubInfoDisabled, cpuPolicy: stubInfoStaticCPU},
		{name: "DryRunExplicitOverrides", env: []string{"MICROFAT_DRY_RUN=1"},
			inherited: true, autotune: stubInfoDryRun, cpuPolicy: stubInfoStaticCPU},
	}
	for _, mode := range []string{execModeCache, execModeMemfd} {
		for _, tt := range tests {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				cacheDir := filepath.Join(t.TempDir(), "cache")
				env := stubInfoProcessEnvironment(cacheDir, tt.env...)
				env = append(env, "MICROFAT_EXEC_MODE="+mode)
				if tt.inherited {
					env = append(env, "GOMEMLIMIT=384MiB", "GOMAXPROCS=3", "GOGC=75")
				}
				report := readStubInfoProcessReport(t, env)
				require.Equal(t, mode, report.ExecMode)
				require.Equal(t, tt.autotune, report.Runtime.Autotune)
				require.Equal(t, tt.cpuPolicy, report.Runtime.CPUPolicy)
				require.Len(t, report.Runtime.Candidates, 1)
				candidate := report.Runtime.Candidates[0]
				require.Equal(t, mode, candidate.ExecMode)
				if mode == execModeMemfd {
					require.Positive(t, candidate.RetainedBytes, "memfd candidates account for the selected executable")
				} else {
					require.Zero(t, candidate.RetainedBytes, "cache candidates do not retain an anonymous executable")
				}
				text := runStubInfoProcess(t, env, stubInfoTextFlag)
				require.Contains(t, text, "Runtime Tuning:    "+tt.autotune+" (CPU policy: "+tt.cpuPolicy+")")
				for _, variable := range []string{"GOMEMLIMIT", stubInfoMaxProcs, "GOGC"} {
					value, exists := candidate.EffectiveEnvironment[variable]
					require.True(t, exists, "info must distinguish an unset %s from missing diagnostics", variable)
					actual := runStubInfoProcess(t, env, "--echo-env", variable)
					require.Equal(t, actual, value.Value, "%s info differs from the real %s payload environment", variable, mode)
					if value.Present {
						require.Contains(t, text, fmt.Sprintf("Effective %s: %q (%s)", variable, actual, value.Source))
					} else {
						require.Contains(t, text, "Effective "+variable+": unset (Go runtime default)")
					}
					if tt.inherited {
						require.True(t, value.Present)
						require.Equal(t, stubInfoInherited, value.Source)
					} else if tt.autotune != stubInfoEnabled || (tt.cpuPolicy == stubInfoNativeCPU && variable == stubInfoMaxProcs) {
						require.False(t, value.Present)
						require.Equal(t, stubInfoUnset, value.Source)
						require.Empty(t, actual)
					}
				}
			})
		}
	}
}

func TestLauncherHelpDescribesCacheFirstAuto(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	env := stubInfoProcessEnvironment(filepath.Join(root, "cache"))
	before := cacheTreeSnapshot(t, root)
	stdout := runStubInfoProcess(t, env, "--microfat:help")
	require.Contains(t, stdout, "auto (default, cache-first)")
	require.Contains(t, stdout, "memfd or cache")
	require.NotContains(t, stdout, "memfd (default)")
	require.NotContains(t, stdout, stubInfoPayloadTag, "help must not execute the application")
	require.Equal(t, before, cacheTreeSnapshot(t, root), "help changed the cache filesystem")
}
