//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelativeTaskPathsAreIndependentOfBuildDirectory(t *testing.T) {
	t.Parallel()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	base, candidate, output := ".work/fd-handoff-base", ".", ".work/fd-handoff-confirmation"
	require.NoError(t, absolutePaths(&base, &candidate, &output))
	assert.Equal(t, filepath.Join(cwd, ".work/fd-handoff-base"), base)
	assert.Equal(t, cwd, candidate)
	assert.Equal(t, filepath.Join(cwd, ".work/fd-handoff-confirmation"), output)
	assert.True(t, filepath.IsAbs(filepath.Join(output, "bin", "baseline-stub")))
}

func TestEnvironmentIgnoresInheritedGoSettings(t *testing.T) {
	for key, value := range map[string]string{
		"GOENV": "/untrusted/settings", "GOTOOLCHAIN": "go1.27.2", "GOOS": "windows", "GOARCH": "unsupported",
		"GOMEMLIMIT": "111MiB", "GOMAXPROCS": "31", "GOGC": "12", "GODEBUG": "asyncpreemptoff=1",
	} {
		t.Setenv(key, value)
	}
	env := map[string]string{}
	for _, setting := range cleanEnv() {
		key, value, ok := strings.Cut(setting, "=")
		require.True(t, ok)
		env[key] = value
	}
	assert.Equal(t, "off", env["GOENV"])
	assert.Equal(t, "local", env["GOTOOLCHAIN"])
	assert.Equal(t, "linux", env["GOOS"])
	assert.Equal(t, runtime.GOARCH, env["GOARCH"])
	for _, key := range []string{"GOMEMLIMIT", "GOMAXPROCS", "GOGC", "GODEBUG"} {
		assert.NotContains(t, env, key)
	}
}

func TestToolchainRejectsAlternateCompilerAndMachine(t *testing.T) {
	t.Parallel()
	version := "go version " + goVersion + " linux/" + runtime.GOARCH
	machine := "x86_64"
	if runtime.GOARCH == "arm64" {
		machine = "aarch64"
	}
	for _, tc := range []struct {
		name, key, value string
	}{
		{"valid", "", ""},
		{"alternate compiler", "GOVERSION", "go1.27.2"},
		{"alternate OS", "GOOS", "windows"},
		{"alternate architecture", "GOARCH", "unsupported"},
		{"persistent settings", "GOENV", "/untrusted/settings"},
		{"automatic toolchain", "GOTOOLCHAIN", "auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			settings := map[string]string{
				"GOVERSION": goVersion, "GOOS": "linux", "GOARCH": runtime.GOARCH, "GOENV": "", "GOTOOLCHAIN": "local",
			}
			if tc.key != "" {
				settings[tc.key] = tc.value
				assert.Error(t, checkToolchain(version, settings, machine))
			} else {
				assert.NoError(t, checkToolchain(version, settings, machine))
				assert.Error(t, checkToolchain("go version go1.27.2 linux/"+runtime.GOARCH, settings, machine))
				assert.Error(t, checkToolchain(version, settings, "unsupported"))
				delete(settings, "GOENV")
				assert.Error(t, checkToolchain(version, settings, machine), "missing environment evidence must fail closed")
			}
		})
	}
}

func TestToolchainPreflightWithPinnedCompiler(t *testing.T) {
	t.Setenv("PATH", filepath.Join(runtime.GOROOT(), "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	output := t.TempDir()
	require.NoError(t, verifyToolchain(output))
	data, err := os.ReadFile(filepath.Join(output, "toolchain.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), goVersion)
}

func TestScheduleIsCompleteAndBalanced(t *testing.T) {
	t.Parallel()
	plan := schedule()
	require.Len(t, plan, pairs*len(corpus)*3*2)
	seen := map[string]int{}
	for i, step := range plan {
		require.Equal(t, i, step.Sequence)
		seen[fmt.Sprintf("%s/%s/%s/%d", step.Family, step.Path, step.Arm, step.Round)]++
		want := armBaseline
		if step.Round%2 != 0 {
			want = armCandidate
		}
		if i%2 == 0 {
			require.Equal(t, want, step.Arm, "unbalanced arm order")
		}
	}
	for key, count := range seen {
		assert.Equal(t, 1, count, "repeated observation %s", key)
	}
}

func passingObservations() []observation {
	var result []observation
	for _, step := range schedule() {
		obs := observation{scheduled: step, Success: true, WallNS: 1000, CPUNS: 1000, MaxRSSKiB: 1024}
		if step.Arm == armCandidate && step.Path == pathCold {
			obs.WallNS = 900
		}
		result = append(result, obs)
	}
	return result
}

func TestDecisionsRetainFailuresAndUnresolvedControls(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func([]observation) []observation
		pass   bool
	}{
		{"all gates pass", func(o []observation) []observation { return o }, true},
		{"missing attempt cannot pass", func(o []observation) []observation { return o[:len(o)-1] }, false},
		{"failed attempt cannot pass", func(o []observation) []observation { o[0].Success = false; return o }, false},
		{"zero CPU cannot pass", func(o []observation) []observation { o[0].CPUNS = 0; return o }, false},
		{"zero wall cannot pass", func(o []observation) []observation { o[0].WallNS = 0; return o }, false},
		{"zero RSS cannot pass", func(o []observation) []observation { o[0].MaxRSSKiB = 0; return o }, false},
		{"excess RSS cannot pass", func(o []observation) []observation { o[0].MaxRSSKiB = maxRSSKiB + 1; return o }, false},
		{"duplicate replaces missing attempt", func(o []observation) []observation { o[1] = o[0]; return o }, false},
		{"extra duplicate attempt", func(o []observation) []observation { return append(o, o[0]) }, false},
		{"cleanup failure cannot pass", func(o []observation) []observation { o[0].CleanupError = "failed"; return o }, false},
		{"memfd regression retained", func(o []observation) []observation {
			for i := range o {
				if o[i].Path == pathMemfd && o[i].Arm == armCandidate {
					o[i].WallNS = 1100
				}
			}
			return o
		}, false},
		{"unexplained control improvement retained", func(o []observation) []observation {
			for i := range o {
				if o[i].Path == pathWarm && o[i].Arm == armCandidate {
					o[i].WallNS = 900
				}
			}
			return o
		}, false},
		{"one useful family insufficient", func(o []observation) []observation {
			for i := range o {
				if o[i].Path == pathCold && o[i].Family != "shfmt" {
					o[i].WallNS = 1000
				}
			}
			return o
		}, false},
		{"cpu regression retained", func(o []observation) []observation {
			for i := range o {
				if o[i].Arm == armCandidate {
					o[i].CPUNS = 1200
				}
			}
			return o
		}, false},
		{"rss regression retained", func(o []observation) []observation {
			for i := range o {
				if o[i].Arm == armCandidate {
					o[i].MaxRSSKiB = 4096
				}
			}
			return o
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decisions, pass := evaluate(tc.mutate(passingObservations()))
			assert.Equal(t, tc.pass, pass, "%+v", decisions)
			require.Len(t, decisions, len(corpus)*3, "lost a family or control")
		})
	}
}

func TestBootstrapDeterministicAndRetainsPrecisionFailure(t *testing.T) {
	t.Parallel()
	values := make([]float64, pairs)
	for i := range values {
		if i%2 == 0 {
			values[i] = 0.90
		} else {
			values[i] = 1.10
		}
	}
	a, b := bootstrap(values), bootstrap(values)
	assert.Equal(t, a, b, "bootstrap seed is not deterministic")
	assert.Less(t, a.Lower95, controlLower, "wide control uncertainty lost")
	assert.Greater(t, a.Upper95, controlUpper, "wide control uncertainty lost")
	assert.Equal(t, interval{}, bootstrap(nil))
}

func TestPrecisionAndResourceBoundaryRules(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		wall, cpu, rss int64
		pass           bool
	}{
		{"control lower boundary is inclusive", 950, 1100, 3174, true},
		{"control upper boundary is inclusive", 1050, 1100, 3174, true},
		{"control lower outside is unresolved", 949, 1000, 1024, false},
		{"control upper outside is unresolved", 1051, 1000, 1024, false},
		{"CPU upper outside is unresolved", 1000, 1101, 1024, false},
		{"RSS upper outside is unresolved", 1000, 1000, 3175, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			observations := passingObservations()
			for i := range observations {
				if observations[i].Arm == armCandidate && observations[i].Path == pathMemfd {
					observations[i].WallNS, observations[i].CPUNS, observations[i].MaxRSSKiB = tc.wall, tc.cpu, tc.rss
				}
			}
			_, pass := evaluate(observations)
			assert.Equal(t, tc.pass, pass)
		})
	}
}

func TestCacheValidationChecksBytesAndUnexpectedEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, checkCache(dir, "unused", true))
	native := filepath.Join(t.TempDir(), "native")
	require.NoError(t, os.WriteFile(native, []byte("selected payload"), privateDirMode))
	id, err := identify(native)
	require.NoError(t, err)
	cache := filepath.Join(dir, id.SHA256)
	require.NoError(t, os.WriteFile(cache, []byte("selected payload"), privateDirMode))
	require.NoError(t, checkCache(dir, id.SHA256, false))
	assert.Error(t, checkCache(dir, id.SHA256, true), "memfd cache side effect accepted")
	require.NoError(t, os.WriteFile(cache, []byte("corrupt"), privateDirMode))
	assert.Error(t, checkCache(dir, id.SHA256, false), "corrupt payload accepted")
}
