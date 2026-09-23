package builder

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAndMatchARM64Setting(t *testing.T) {
	t.Parallel()

	const (
		tierV8_0 = "v8.0"
		tierV8_1 = "v8.1"
	)

	tests := []struct {
		name          string
		expectedTier  string
		actualSetting string
		shouldMatch   bool
		expectedErr   string
	}{
		{
			name:          "v8.0_exact_match",
			expectedTier:  tierV8_0,
			actualSetting: tierV8_0,
			shouldMatch:   true,
		},
		{
			name:          "v8.0_does_not_match_v8.0_lse",
			expectedTier:  tierV8_0,
			actualSetting: "v8.0,lse",
			shouldMatch:   false,
			expectedErr:   "LSE feature mismatch",
		},
		{
			name:          "v8.0_does_not_match_v8.0_crypto",
			expectedTier:  tierV8_0,
			actualSetting: "v8.0,crypto",
			shouldMatch:   false,
			expectedErr:   "crypto feature mismatch",
		},
		{
			name:          "v8.1_matches_canonicalized_v8.1_lse",
			expectedTier:  tierV8_1,
			actualSetting: "v8.1,lse",
			shouldMatch:   true,
		},
		{
			name:          "v8.1_matches_plain_v8.1",
			expectedTier:  tierV8_1,
			actualSetting: tierV8_1,
			shouldMatch:   true,
		},
		{
			name:          "v8.1_does_not_match_v8.1_crypto",
			expectedTier:  tierV8_1,
			actualSetting: "v8.1,crypto",
			shouldMatch:   false,
			expectedErr:   "crypto feature mismatch",
		},
		{
			name:          "v8.1_lse_does_not_match_v8.1_lse_crypto",
			expectedTier:  tierV8_1,
			actualSetting: "v8.1,lse,crypto",
			shouldMatch:   false,
			expectedErr:   "crypto feature mismatch",
		},
		{
			name:          "tier_mismatch_v8.1_vs_v8.2",
			expectedTier:  tierV8_1,
			actualSetting: "v8.2",
			shouldMatch:   false,
			expectedErr:   "tier mismatch",
		},
		{
			name:          "v9.0_matches_v9.0_lse",
			expectedTier:  "v9.0",
			actualSetting: "v9.0,lse",
			shouldMatch:   true,
		},
		{
			name:          "unknown_suffix_rejected",
			expectedTier:  tierV8_1,
			actualSetting: "v8.1,sve",
			shouldMatch:   false,
			expectedErr:   "unknown ARM64 feature suffix",
		},
		{
			name:          "duplicate_suffix_rejected",
			expectedTier:  tierV8_1,
			actualSetting: "v8.1,lse,lse",
			shouldMatch:   false,
			expectedErr:   "duplicate ARM64 feature suffix",
		},
		{
			name:          "empty_actual_rejected",
			expectedTier:  tierV8_1,
			actualSetting: "",
			shouldMatch:   false,
			expectedErr:   "empty ARM64 setting",
		},
		{
			name:          "unknown_base_tier_rejected",
			expectedTier:  tierV8_1,
			actualSetting: "v7.0",
			shouldMatch:   false,
			expectedErr:   "unknown ARM64 base tier",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := matchARM64Setting(tt.actualSetting, tt.expectedTier)
			if tt.shouldMatch {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				if tt.expectedErr != "" {
					assert.Contains(t, err.Error(), tt.expectedErr)
				}
			}
		})
	}
}

func TestVerifyStagedIdentities(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "file1")
	require.NoError(t, os.WriteFile(f1, []byte("content1"), 0o600))
	fi1, err := os.Stat(f1)
	require.NoError(t, err)

	dev1, ino1, _ := fileDevIno(fi1)

	id1 := &StagedArtifactIdentity{
		Path:    f1,
		Dev:     dev1,
		Ino:     ino1,
		Size:    fi1.Size(),
		ModTime: fi1.ModTime(),
	}

	identities := map[string]*StagedArtifactIdentity{
		"v1": id1,
	}

	// Case 1: unmutated file passes verification
	require.NoError(t, verifyStagedIdentities(identities))

	// Case 2: nil identity fails
	require.ErrorIs(t, verifyStagedIdentities(map[string]*StagedArtifactIdentity{"v1": nil}), ErrStagedArtifactModified)

	// Case 3: missing file fails
	require.ErrorIs(t, verifyStagedIdentities(map[string]*StagedArtifactIdentity{
		"v1": {Path: filepath.Join(tmpDir, "missing")},
	}), ErrStagedArtifactModified)

	// Case 4: size modified fails
	badSizeID := *id1
	badSizeID.Size = 9999
	require.ErrorIs(t, verifyStagedIdentities(map[string]*StagedArtifactIdentity{
		"v1": &badSizeID,
	}), ErrStagedArtifactModified)

	// Case 5: dev/ino changed fails
	if dev1 != 0 || ino1 != 0 {
		badInoID := *id1
		badInoID.Ino = ino1 + 1000
		require.ErrorIs(t, verifyStagedIdentities(map[string]*StagedArtifactIdentity{
			"v1": &badInoID,
		}), ErrStagedArtifactModified)
	}
}

func TestValidateArtifactBuildInfo_DirectErrors(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// 1. Non-existent file
	_, err := ValidateArtifactBuildInfo(filepath.Join(tmpDir, "does-not-exist"), ExpectedTarget{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidArtifactBuildInfo)
	assert.Contains(t, err.Error(), "opening")

	// 2. Non-ELF file
	notELF := filepath.Join(tmpDir, "not_elf.bin")
	require.NoError(t, os.WriteFile(notELF, []byte("plain text not elf"), 0o600))
	_, err = ValidateArtifactBuildInfo(notELF, ExpectedTarget{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidArtifactBuildInfo)
	assert.Contains(t, err.Error(), "reading buildinfo")

	// 3. Current executable has valid buildinfo, test target mismatch errors
	selfExe, err := os.Executable()
	require.NoError(t, err)

	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: "invalid_os", Arch: testArchAMD64, Tier: "v1"})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidArtifactBuildInfo)
	assert.Contains(t, err.Error(), "GOOS mismatch")

	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: "mips", Tier: "v1"})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidArtifactBuildInfo)
	assert.Contains(t, err.Error(), "GOARCH mismatch")

	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: testArchAMD64, Tier: "nonexistent_tier"})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidArtifactBuildInfo)
}

func TestCloneManifest(t *testing.T) {
	t.Parallel()

	require.Nil(t, cloneManifest(nil))

	orig := &Manifest{
		AppName:    "original",
		BuildFlags: []string{"-v"},
		Tags:       []string{"netgo"},
		Env:        map[string]string{"K": "V"},
		Compression: &CompressionConfig{
			EnableDict: true,
		},
		Variants: []VariantConfig{
			{
				Level: "v1",
				Flags: []string{"-race"},
				Env:   map[string]string{"VK": "VV"},
				Compression: &CompressionConfig{
					EnableDict: true,
				},
			},
		},
	}

	cloned := cloneManifest(orig)
	require.NotNil(t, cloned)
	assert.Equal(t, orig.AppName, cloned.AppName)

	// Mutate clone and assert original remains unchanged
	cloned.BuildFlags[0] = "-x"
	assert.Equal(t, "-v", orig.BuildFlags[0])

	cloned.Tags[0] = "osusergo"
	assert.Equal(t, "netgo", orig.Tags[0])

	cloned.Env["K"] = "MUTATED"
	assert.Equal(t, "V", orig.Env["K"])

	cloned.Variants[0].Flags[0] = "-msan"
	assert.Equal(t, "-race", orig.Variants[0].Flags[0])

	cloned.Variants[0].Env["VK"] = "MUTATED"
	assert.Equal(t, "VV", orig.Variants[0].Env["VK"])
}
