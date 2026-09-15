package releasecheck_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveVersion_ExplicitTag(t *testing.T) {
	t.Parallel()
	v, err := releasecheck.DeriveVersion("", "v0.2.3")
	require.NoError(t, err)
	assert.Equal(t, "0.2.3", v)

	v, err = releasecheck.DeriveVersion("", "0.2.3")
	require.NoError(t, err)
	assert.Equal(t, "0.2.3", v)

	_, err = releasecheck.DeriveVersion("", "")
	require.Error(t, err)
}

func TestDeriveVersion_GoReleaserMetadata(t *testing.T) {
	t.Parallel()
	distDir := t.TempDir()

	metadataJSON := `{
		"project_name": "microfat",
		"tag": "v0.2.3-snapshot",
		"version": "0.2.3-SNAPSHOT-abc1234"
	}`
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "metadata.json"), []byte(metadataJSON), 0o644))

	v, err := releasecheck.DeriveVersion(distDir, "")
	require.NoError(t, err)
	assert.Equal(t, "0.2.3-SNAPSHOT-abc1234", v)

	// Wrong project name
	badMetadata := `{
		"project_name": "other-project",
		"version": "1.0.0"
	}`
	badDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(badDir, "metadata.json"), []byte(badMetadata), 0o644))
	_, err = releasecheck.DeriveVersion(badDir, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected project name")
}

func TestDeriveVersion_GoReleaserArtifacts(t *testing.T) {
	t.Parallel()
	distDir := t.TempDir()

	artifactsJSON := `[
		{
			"name": "microfat_0.2.3_linux_amd64.tar.gz",
			"type": "Archive"
		}
	]`
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "artifacts.json"), []byte(artifactsJSON), 0o644))

	v, err := releasecheck.DeriveVersion(distDir, "")
	require.NoError(t, err)
	assert.Equal(t, "0.2.3", v)
}

func TestDeriveVersion_DiskArchives(t *testing.T) {
	t.Parallel()
	distDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(distDir, "microfat_0.2.3_linux_amd64.tar.gz"), []byte("data"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "microfat_0.2.3_linux_arm64.tar.gz"), []byte("data"), 0o644))

	v, err := releasecheck.DeriveVersion(distDir, "")
	require.NoError(t, err)
	assert.Equal(t, "0.2.3", v)

	// Mismatched versions on disk
	mismatchDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(mismatchDir, "microfat_0.2.3_linux_amd64.tar.gz"), []byte("data"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mismatchDir, "microfat_0.2.4_linux_arm64.tar.gz"), []byte("data"), 0o644))

	_, err = releasecheck.DeriveVersion(mismatchDir, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mismatched versions")
}

func TestNewReleaseContract_Empty(t *testing.T) {
	t.Parallel()
	_, err := releasecheck.NewReleaseContract("")
	require.Error(t, err)

	c, err := releasecheck.NewReleaseContract("v0.2.3")
	require.NoError(t, err)
	assert.Equal(t, "0.2.3", c.Version)
	assert.Len(t, c.ExpectedPayloadNames, 6)
	assert.Len(t, c.RequiredExecutables, 3)
}
