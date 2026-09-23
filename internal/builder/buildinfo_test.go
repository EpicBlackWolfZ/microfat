package builder

import (
	"context"
	"debug/buildinfo"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tierV8_0 = "v8.0"
	tierV8_1 = "v8.1"
)

func TestParseAndMatchARM64Setting(t *testing.T) {
	t.Parallel()

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
			name:          "leading_comma_rejected",
			expectedTier:  tierV8_1,
			actualSetting: ",lse",
			shouldMatch:   false,
			expectedErr:   "unknown ARM64 base tier",
		},
		{
			name:          "empty_suffix_rejected",
			expectedTier:  tierV8_1,
			actualSetting: "v8.1,",
			shouldMatch:   false,
			expectedErr:   "empty ARM64 feature suffix",
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
		{
			name:          "invalid_expected_tier_rejected",
			expectedTier:  "invalid-tier",
			actualSetting: tierV8_0,
			shouldMatch:   false,
			expectedErr:   "invalid expected ARM64 tier",
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

	// Case 6: modtime changed fails
	badModTimeID := *id1
	badModTimeID.ModTime = fi1.ModTime().Add(5 * time.Second)
	require.ErrorIs(t, verifyStagedIdentities(map[string]*StagedArtifactIdentity{
		"v1": &badModTimeID,
	}), ErrStagedArtifactModified)
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

func TestValidateArtifactBuildInfo_MockSettings(t *testing.T) {
	selfExe, err := os.Executable()
	require.NoError(t, err)

	orig := readBuildInfoFunc
	defer func() { readBuildInfoFunc = orig }()

	// 0. Error reading buildinfo
	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return nil, errors.New("simulated read failure")
	}
	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: testArchAMD64, Tier: "v1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated read failure")

	// 1. Duplicate build setting
	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return &buildinfo.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: EnvGOOS, Value: testOSLinux},
				{Key: EnvGOOS, Value: testOSLinux},
			},
		}, nil
	}
	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: testArchAMD64, Tier: "v1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate build setting")

	// 2. Missing GOOS
	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return &buildinfo.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: EnvGOARCH, Value: testArchAMD64},
				{Key: EnvGOAMD64, Value: "v1"},
			},
		}, nil
	}
	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: testArchAMD64, Tier: "v1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing GOOS setting")

	// 3. Missing GOARCH
	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return &buildinfo.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: EnvGOOS, Value: testOSLinux},
				{Key: EnvGOAMD64, Value: "v1"},
			},
		}, nil
	}
	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: testArchAMD64, Tier: "v1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing GOARCH setting")

	// 4. Inapplicable GOARM64 on amd64
	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return &buildinfo.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: EnvGOOS, Value: testOSLinux},
				{Key: EnvGOARCH, Value: testArchAMD64},
				{Key: EnvGOAMD64, Value: "v1"},
				{Key: EnvGOARM64, Value: tierV8_0},
			},
		}, nil
	}
	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: testArchAMD64, Tier: "v1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "architecture-inapplicable GOARM64")

	// 5. Missing GOAMD64
	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return &buildinfo.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: EnvGOOS, Value: testOSLinux},
				{Key: EnvGOARCH, Value: testArchAMD64},
			},
		}, nil
	}
	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: testArchAMD64, Tier: "v1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing GOAMD64 setting")

	// 6. Inapplicable GOAMD64 on arm64
	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return &buildinfo.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: EnvGOOS, Value: testOSLinux},
				{Key: EnvGOARCH, Value: testArchARM64},
				{Key: EnvGOAMD64, Value: "v1"},
				{Key: EnvGOARM64, Value: tierV8_0},
			},
		}, nil
	}
	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: testArchARM64, Tier: tierV8_0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "architecture-inapplicable GOAMD64")

	// 7. Missing GOARM64 on arm64
	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return &buildinfo.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: EnvGOOS, Value: testOSLinux},
				{Key: EnvGOARCH, Value: testArchARM64},
			},
		}, nil
	}
	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: testArchARM64, Tier: tierV8_0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing GOARM64 setting")

	// 8. Unsupported target architecture
	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return &buildinfo.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: EnvGOOS, Value: testOSLinux},
				{Key: EnvGOARCH, Value: "riscv64"},
			},
		}, nil
	}
	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: "riscv64", Tier: "rva22"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported target architecture")

	// 9. Semantic mismatch on arm64
	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return &buildinfo.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: EnvGOOS, Value: testOSLinux},
				{Key: EnvGOARCH, Value: testArchARM64},
				{Key: EnvGOARM64, Value: "v8.0,lse"},
			},
		}, nil
	}
	_, err = ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: testArchARM64, Tier: tierV8_0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GOARM64 semantic mismatch")

	// 10. Valid arm64 matches
	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return &buildinfo.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: EnvGOOS, Value: testOSLinux},
				{Key: EnvGOARCH, Value: testArchARM64},
				{Key: EnvGOARM64, Value: tierV8_0},
			},
		}, nil
	}
	id, err := ValidateArtifactBuildInfo(selfExe, ExpectedTarget{OS: testOSLinux, Arch: testArchARM64, Tier: tierV8_0})
	require.NoError(t, err)
	assert.Equal(t, selfExe, id.Path)
}

func TestBuildGoCommand_FlagsAndTags(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	pkgSubDir := filepath.Join(tmpDir, "cmd", "app")
	require.NoError(t, os.MkdirAll(pkgSubDir, 0o755))

	ctx := context.Background()
	m := &Manifest{
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Package:    filepath.Join("cmd", "app"),
		Dir:        tmpDir,
		Tags:       []string{"netgo", "osusergo"},
		BuildFlags: []string{"-trimpath", "-v"},
	}
	v := VariantConfig{
		Level: "v1",
		Flags: []string{"-ldflags=-s -w"},
	}
	cmd := buildGoCommand(ctx, "go", m, v, "-pgo=auto", "/out/bin", []string{"PATH=/usr/bin"})
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Args, "-tags=netgo,osusergo")
	assert.Contains(t, cmd.Args, "-trimpath")
	assert.Contains(t, cmd.Args, "-v")
	assert.Contains(t, cmd.Args, "-ldflags=-s -w")
	assert.Contains(t, cmd.Args, "-pgo=auto")
	assert.Equal(t, pkgSubDir, cmd.Dir)
	assert.Equal(t, ".", cmd.Args[len(cmd.Args)-1])
}

func TestResolveAllVariantPGOs_ErrorsAndDefaults(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// 1. Variant PGO missing
	m1 := &Manifest{
		Package: tmpDir,
		Variants: []VariantConfig{
			{Level: "v1", PGO: "nonexistent.pgo"},
		},
	}
	_, err := resolveAllVariantPGOs(m1)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProfileNotFound)

	// 2. Default PGO missing
	m2 := &Manifest{
		Package:    tmpDir,
		DefaultPGO: "nonexistent-default.pgo",
		Variants: []VariantConfig{
			{Level: "v1"},
		},
	}
	_, err = resolveAllVariantPGOs(m2)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProfileNotFound)

	// 3. Package default.pgo present and variant off
	pgoPath := filepath.Join(tmpDir, defaultPGOFile)
	require.NoError(t, os.WriteFile(pgoPath, []byte("profile"), 0o600))
	m3 := &Manifest{
		Package: tmpDir,
		Variants: []VariantConfig{
			{Level: "v1"},
			{Level: "v2", PGO: "off"},
		},
	}
	pgoMap, err := resolveAllVariantPGOs(m3)
	require.NoError(t, err)
	assert.Equal(t, "-pgo="+pgoPath, pgoMap["v1"])
	assert.Equal(t, pgoOffFlag, pgoMap["v2"])

	// 4. Default PGO off
	m4 := &Manifest{
		Package:    tmpDir,
		DefaultPGO: "off",
		Variants: []VariantConfig{
			{Level: "v3"},
		},
	}
	pgoMap4, err := resolveAllVariantPGOs(m4)
	require.NoError(t, err)
	assert.Equal(t, pgoOffFlag, pgoMap4["v3"])
}

func TestBuildAndPack_CancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tmpDir := t.TempDir()
	stubFile := createDummyELF(t, tmpDir, "microfat-stub", testArchAMD64)
	m := &Manifest{
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Package:    tmpDir,
		Dir:        tmpDir,
		Variants: []VariantConfig{
			{Level: "v1"},
		},
	}
	_, err := BuildAndPack(ctx, m, BuildOptions{
		StubPath:   stubFile,
		OutputPath: filepath.Join(tmpDir, "out"),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "canceled"))
}
