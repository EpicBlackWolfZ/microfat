//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStudySelection(t *testing.T) {
	t.Parallel()
	original, err := selectStudy(originalStudy)
	require.NoError(t, err)
	require.Equal(t, protocol, original.Protocol)
	require.False(t, original.Isolated)
	isolated, err := selectStudy(isolatedStudy)
	require.NoError(t, err)
	require.True(t, isolated.Isolated)
	require.Equal(t, "docs/codec-policy-isolated.md", isolated.Protocol)
	_, err = selectStudy("unexpected")
	require.Error(t, err)
}

func isolatedFixture(t *testing.T) (studyConfig, string) {
	t.Helper()
	output := filepath.Join(t.TempDir(), "cohort")
	require.NoError(t, os.Mkdir(output, privateDir))
	bootstrap := modePath(output + ".bootstrap-config")
	require.NoError(t, os.MkdirAll(filepath.Dir(bootstrap), privateDir))
	require.NoError(t, os.WriteFile(bootstrap, []byte(offMode), privateFile))
	study, err := selectStudy(isolatedStudy)
	require.NoError(t, err)
	study, err = prepareStudy(study, output)
	require.NoError(t, err)
	return study, output
}

func TestIsolatedConfigurationAndEnvironment(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/poisoned-config")
	study, output := isolatedFixture(t)
	require.Equal(t, filepath.Join(output, "config"), study.ConfigDir)
	require.True(t, filepath.IsAbs(study.ConfigDir))
	for _, path := range study.frozenFiles() {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, offMode, string(data))
		info, err := os.Lstat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(privateFile), info.Mode().Perm())
	}
	var matches []string
	for _, item := range study.env("MICROFAT_AUTOTUNE=0") {
		if strings.HasPrefix(item, "XDG_CONFIG_HOME=") {
			matches = append(matches, item)
		}
	}
	require.Equal(t, []string{"XDG_CONFIG_HOME=" + study.ConfigDir}, matches)
	data, err := command(study, "", "/bin/sh", "-c", `printf '%s' "$XDG_CONFIG_HOME"`)
	require.NoError(t, err)
	require.Equal(t, study.ConfigDir, string(data), "preparation commands must receive controlled config")
	original, err := selectStudy(originalStudy)
	require.NoError(t, err)
	require.Contains(t, original.env(), "XDG_CONFIG_HOME=/poisoned-config", "original keeps its frozen environment semantics")
}

func TestIsolatedModeIntegrity(t *testing.T) {
	t.Parallel()
	study, output := isolatedFixture(t)
	var identities []identity
	for _, path := range study.frozenFiles() {
		id, err := identify(path)
		require.NoError(t, err)
		identities = append(identities, id)
	}
	require.NoError(t, saveJSON(filepath.Join(output, "freeze.json"), map[string]any{"study": study, "identities": identities}))
	require.NoError(t, recheckInputs(output, "same"))
	require.NoError(t, os.WriteFile(modePath(study.ConfigDir), []byte("local\n"), privateFile))
	require.Error(t, recheckInputs(output, "changed"))
	require.NoError(t, os.WriteFile(modePath(study.ConfigDir), []byte(offMode), privateFile))
	require.NoError(t, os.Chmod(modePath(study.BootstrapConfigDir), 0o644))
	require.Error(t, recheckInputs(output, "changed-mode"))
	require.NoError(t, os.Chmod(modePath(study.BootstrapConfigDir), privateFile))
	require.NoError(t, os.Chmod(filepath.Join(study.ConfigDir, "go"), 0o755))
	require.Error(t, recheckInputs(output, "changed-directory-mode"))
}

func TestIsolatedRequiresOwnedBootstrapConfiguration(t *testing.T) {
	t.Parallel()
	study, err := selectStudy(isolatedStudy)
	require.NoError(t, err)
	_, err = prepareStudy(study, t.TempDir())
	require.ErrorContains(t, err, "bootstrap config")
}
