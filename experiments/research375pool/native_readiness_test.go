package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOfflineNativeReadinessFixtureAndFailures(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	r, err := prepareNativeReadiness(familyGovulncheck, root)
	require.NoError(t, err)
	assert.True(t, recordedNativeReadinessValid(familyGovulncheck, r))
	assert.Equal(t, []string{"-version", "-db", "file://" + r.DatabaseDir}, r.Args)
	require.Len(t, r.Files, 2)
	for _, file := range r.Files {
		info, err := os.Stat(file.Path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(nativeDBFileMode), info.Mode().Perm())
	}
	run := func(_ context.Context, command string, args, env []string) (string, string, error) {
		assert.Equal(t, "fixture", command)
		assert.Equal(t, r.Args, args)
		assert.Contains(t, env, "GOVERSION="+researchGoVersion)
		return fixtureSuccess, "", nil
	}
	stdout, _, err := runNativeReadiness(context.Background(), "fixture", r, nativeLaunchEnv("", ""), run)
	require.NoError(t, err)
	assert.Equal(t, fixtureSuccess, stdout)
	for _, fault := range []string{fixtureMissing, "corrupt"} {
		t.Run(fault, func(t *testing.T) {
			r, err := prepareNativeReadiness(familyGovulncheck, t.TempDir())
			require.NoError(t, err)
			require.NoError(t, os.Remove(r.Files[0].Path))
			if fault != fixtureMissing {
				require.NoError(t, os.WriteFile(r.Files[0].Path, []byte("corrupt database"), nativeDBFileMode))
			}
			_, _, err = runNativeReadiness(context.Background(), "fixture", r, nil, nil)
			require.Error(t, err, "fixture must fail before a runner can accept ignored DB errors")
		})
	}
	for _, fault := range []string{"args", "version", "hash", "path", fixtureRelative, "extra-db"} {
		t.Run(fault, func(t *testing.T) {
			bad := r
			bad.Args, bad.Files = slices.Clone(r.Args), slices.Clone(r.Files)
			switch fault {
			case "args":
				bad.Args[0] = "scan"
			case "version":
				bad.GoVersion = fixtureWrong
			case "hash":
				bad.Files[0].SHA256 = fixtureWrong
			case "path":
				bad.Files[0].Path = filepath.Join(root, "foreign")
			case fixtureRelative:
				bad.DatabaseDir = fixtureRelative
			case "extra-db":
				assert.False(t, recordedNativeReadinessValid(familyTask, bad))
				return
			}
			assert.False(t, recordedNativeReadinessValid(familyGovulncheck, bad))
		})
	}
	_, err = expectedNativeReadiness(fixtureUnknown, root)
	require.Error(t, err)
	_, err = prepareNativeReadiness(fixtureUnknown, root)
	require.Error(t, err)
	assert.False(t, recordedNativeReadinessValid(fixtureUnknown, r))
}

func TestOfflineNativeReadinessSetupErrors(t *testing.T) {
	t.Parallel()
	for _, block := range []string{nativeDBDirectory, filepath.Join(nativeDBDirectory, "index", "db.json")} {
		t.Run(block, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, block)
			if block == nativeDBDirectory {
				require.NoError(t, os.WriteFile(path, nil, nativeDBFileMode))
			} else {
				require.NoError(t, os.MkdirAll(path, privateDirMode))
			}
			_, err := prepareNativeReadiness(familyGovulncheck, root)
			require.Error(t, err)
		})
	}
}

func TestGovulncheckNativeModesShareOwnedOfflineFixture(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{fixtureSuccess, fixtureMissing, "corrupt"} {
		t.Run(fault, func(t *testing.T) {
			m, fixture := nativeLaunchFixture(t)
			for i := range m.Variants {
				m.Variants[i].Family = familyGovulncheck
			}
			root := t.TempDir()
			hooks := nativeLaunchTestHooks(t, fixture)
			var initialArgs []string
			calls := 0
			hooks.run = func(_ context.Context, command string, args, env []string) (string, string, error) {
				calls++
				if initialArgs == nil {
					initialArgs = slices.Clone(args)
				}
				assert.Equal(t, initialArgs, args, "all modes must use the identical local DB path")
				assert.Contains(t, env, "GOVERSION="+researchGoVersion)
				mode := nativeLaunchTestEnvValue(env, format.EnvExecMode)
				if command == fixture.Path {
					if fault != fixtureSuccess {
						r, err := expectedNativeReadiness(familyGovulncheck, filepath.Join(rootFromDBArg(args), nativeDBDirectory))
						require.NoError(t, err)
						require.NoError(t, os.Remove(r.Files[0].Path))
						if fault == "corrupt" {
							require.NoError(t, os.WriteFile(r.Files[0].Path, []byte("corrupt"), nativeDBFileMode))
						}
					}
					return "offline version\n", "", nil
				}
				return "offline version\n", nativeLaunchTestDiagnostic(t, mode, fixture), nil
			}
			observations, err := verifyNativeLaunchesWithHooks(context.Background(), "artifact", m, familyGovulncheck, root, hooks)
			if fault == fixtureSuccess {
				require.NoError(t, err)
				require.Len(t, observations, nativeStudyModes)
				assert.Equal(t, nativeStudyModes, calls)
				for _, o := range observations {
					assert.True(t, recordedNativeReadinessValid(familyGovulncheck, o.Readiness))
				}
			} else {
				require.Error(t, err)
				require.Len(t, observations, 2)
				assert.Equal(t, 1, calls, "corrupt or missing fixture prevents the next child launch")
				assert.NotEmpty(t, observations[1].Error)
			}
			entries, err := os.ReadDir(root)
			require.NoError(t, err)
			assert.Empty(t, entries, "owned DB survives all launch records, then is cleaned with the cache")
		})
	}
}

func rootFromDBArg(args []string) string {
	return filepath.Dir(args[len(args)-1][len("file://"):])
}
