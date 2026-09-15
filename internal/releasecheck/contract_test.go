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

func TestDeriveVersion_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("EmptyExplicitTag", func(t *testing.T) {
		_, err := releasecheck.DeriveVersion("", "   ")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "explicit tag is empty")

		_, err = releasecheck.DeriveVersion("", "v")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "explicit tag is empty")
	})

	t.Run("EmptyMetadataVersion", func(t *testing.T) {
		distDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(distDir, "metadata.json"), []byte(`{"version":" "}`), 0o644))
		_, err := releasecheck.DeriveVersion(distDir, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to derive release version")
	})

	t.Run("NoMatchingArtifactsInJSON", func(t *testing.T) {
		distDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(distDir, "artifacts.json"), []byte(`[{"name":"foo","type":"Binary"}]`), 0o644))
		_, err := releasecheck.DeriveVersion(distDir, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to derive release version")
	})

	t.Run("InvalidArchiveNamingFormat", func(t *testing.T) {
		distDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(distDir, "microfat_linux_amd64.tar.gz"), []byte("data"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(distDir, "microfat_linux_arm64.tar.gz"), []byte("data"), 0o644))
		_, err := releasecheck.DeriveVersion(distDir, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to derive release version")
	})
}

func TestParseReleaseArchiveName(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		input       string
		wantVersion string
		wantArch    string
		wantErr     bool
		errMsg      string
	}{
		{
			name:        "ValidAMD64Release",
			input:       "microfat_0.2.3_linux_amd64.tar.gz",
			wantVersion: "0.2.3",
			wantArch:    releasecheck.ArchAMD64,
		},
		{
			name:        "ValidARM64Release",
			input:       "microfat_0.2.3_linux_arm64.tar.gz",
			wantVersion: "0.2.3",
			wantArch:    releasecheck.ArchARM64,
		},
		{
			name:        "ValidSnapshotRelease",
			input:       "microfat_0.2.3-SNAPSHOT-deadbee_linux_amd64.tar.gz",
			wantVersion: "0.2.3-SNAPSHOT-deadbee",
			wantArch:    releasecheck.ArchAMD64,
		},
		{
			name:        "DistractingDirectoryARM64ForAMD64Archive",
			input:       "/tmp/arm64/microfat_0.2.3_linux_amd64.tar.gz",
			wantVersion: "0.2.3",
			wantArch:    releasecheck.ArchAMD64,
		},
		{
			name:        "DistractingDirectoryAMD64ForARM64Archive",
			input:       "/tmp/amd64/microfat_0.2.3_linux_arm64.tar.gz",
			wantVersion: "0.2.3",
			wantArch:    releasecheck.ArchARM64,
		},
		{
			name:    "WrongPrefix",
			input:   "foo_0.2.3_linux_amd64.tar.gz",
			wantErr: true,
			errMsg:  "missing required prefix",
		},
		{
			name:    "EmptyVersion",
			input:   "microfat__linux_amd64.tar.gz",
			wantErr: true,
			errMsg:  "empty version",
		},
		{
			name:    "UnknownArchitecture",
			input:   "microfat_0.2.3_linux_x86.tar.gz",
			wantErr: true,
			errMsg:  "missing or unknown architecture suffix",
		},
		{
			name:    "MissingLinuxComponent",
			input:   "microfat_0.2.3_amd64.tar.gz",
			wantErr: true,
			errMsg:  "missing or unknown architecture suffix",
		},
		{
			name:    "WrongSuffixZip",
			input:   "microfat_0.2.3_linux_arm64.zip",
			wantErr: true,
			errMsg:  "missing required suffix",
		},
		{
			name:    "EmptyName",
			input:   "",
			wantErr: true,
			errMsg:  "missing required prefix",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, err := releasecheck.ParseReleaseArchiveName(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				if tc.errMsg != "" {
					assert.Contains(t, err.Error(), tc.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantVersion, id.Version)
				assert.Equal(t, tc.wantArch, id.Arch)
			}
		})
	}
}
