package builder

import (
	"debug/buildinfo"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStubCompanionName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		profile     string
		expected    string
		expectError bool
		errorTarget error
	}{
		{
			name:        "EmptyProfile_DefaultsToFullStub",
			profile:     "",
			expected:    StubBinaryFull,
			expectError: false,
		},
		{
			name:        "FullProfile_SelectsFullStub",
			profile:     StubProfileFull,
			expected:    StubBinaryFull,
			expectError: false,
		},
		{
			name:        "MinimalProfile_SelectsMinimalStub",
			profile:     StubProfileMinimal,
			expected:    StubBinaryMinimal,
			expectError: false,
		},
		{
			name:        "InvalidProfile_ReturnsError",
			profile:     "unsupported_profile",
			expectError: true,
			errorTarget: ErrInvalidStubProfile,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			actual, err := StubCompanionName(tt.profile)
			if tt.expectError {
				require.Error(t, err)
				require.ErrorIs(t, err, tt.errorTarget)
				assert.Empty(t, actual)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, actual)
			}
		})
	}
}

func TestDetectStubProfile(t *testing.T) {
	tempDir := t.TempDir()

	minFile := filepath.Join(tempDir, StubBinaryMinimal)
	require.NoError(t, os.WriteFile(minFile, []byte("stub"), 0o755))

	fullFile := filepath.Join(tempDir, StubBinaryFull)
	require.NoError(t, os.WriteFile(fullFile, []byte("stub"), 0o755))

	customMinFile := filepath.Join(tempDir, "launcher-minimal")
	require.NoError(t, os.WriteFile(customMinFile, []byte("stub"), 0o755))

	customStubFile := filepath.Join(tempDir, "custom-stub.elf")
	require.NoError(t, os.WriteFile(customStubFile, []byte("stub"), 0o755))

	assert.Equal(t, StubProfileMinimal, DetectStubProfile(minFile))
	assert.Equal(t, StubProfileFull, DetectStubProfile(fullFile))
	assert.Equal(t, StubProfileMinimal, DetectStubProfile(customMinFile))
	assert.Empty(t, DetectStubProfile(customStubFile))
	assert.Empty(t, DetectStubProfile(filepath.Join(tempDir, "nonexistent")))

	// Test Go buildinfo detection with mock
	origRead := readBuildInfoFunc
	defer func() { readBuildInfoFunc = origRead }()

	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return &buildinfo.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: "-tags", Value: "minimal,other"},
			},
		}, nil
	}
	assert.Equal(t, StubProfileMinimal, DetectStubProfile(customStubFile))

	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return &buildinfo.BuildInfo{
			Path: "github.com/EpicBlackWolfZ/microfat/cmd/microfat-stub",
			Settings: []debug.BuildSetting{
				{Key: "-tags", Value: "netgo"},
			},
		}, nil
	}
	assert.Equal(t, StubProfileFull, DetectStubProfile(customStubFile))

	readBuildInfoFunc = func(io.ReaderAt) (*buildinfo.BuildInfo, error) {
		return nil, errors.New("not a go binary")
	}
	assert.Empty(t, DetectStubProfile(customStubFile))
}

func TestInspectCandidateELF(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	// 1. Non-existent file
	err := inspectCandidateELF(filepath.Join(tempDir, "missing"), testArchAMD64)
	require.Error(t, err)

	// 2. Directory
	subDir := filepath.Join(tempDir, "dir")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	err = inspectCandidateELF(subDir, testArchAMD64)
	require.Error(t, err)

	// 3. File too short
	shortFile := filepath.Join(tempDir, "short")
	require.NoError(t, os.WriteFile(shortFile, []byte("short"), 0o755))
	err = inspectCandidateELF(shortFile, testArchAMD64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too short")

	// 3b. File too short for ELF64 (63 bytes)
	shortELF := filepath.Join(tempDir, "short63")
	shortELFData := make([]byte, 63)
	copy(shortELFData, []byte{0x7f, 'E', 'L', 'F'})
	require.NoError(t, os.WriteFile(shortELF, shortELFData, 0o755))
	err = inspectCandidateELF(shortELF, testArchAMD64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too short for ELF64 header")

	// 4. Invalid magic
	badMagic := filepath.Join(tempDir, "badmagic")
	badData := make([]byte, 64)
	copy(badData, "NOT_ELF_HEADER_BYTES")
	require.NoError(t, os.WriteFile(badMagic, badData, 0o755))
	err = inspectCandidateELF(badMagic, testArchAMD64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ELF magic")

	// 4b. Invalid class (32-bit ELF)
	badClass := filepath.Join(tempDir, "badclass")
	badClassData := make([]byte, 64)
	copy(badClassData, []byte{0x7f, 'E', 'L', 'F', 1, 1, 1})
	require.NoError(t, os.WriteFile(badClass, badClassData, 0o755))
	err = inspectCandidateELF(badClass, testArchAMD64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ELF class")

	// 4c. Invalid data encoding
	badDataEnc := filepath.Join(tempDir, "baddataenc")
	badDataEncData := make([]byte, 64)
	copy(badDataEncData, []byte{0x7f, 'E', 'L', 'F', 2, 0, 1})
	require.NoError(t, os.WriteFile(badDataEnc, badDataEncData, 0o755))
	err = inspectCandidateELF(badDataEnc, testArchAMD64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ELF data encoding")

	// 4d. Invalid version
	badVersion := filepath.Join(tempDir, "badversion")
	badVersionData := make([]byte, 64)
	copy(badVersionData, []byte{0x7f, 'E', 'L', 'F', 2, 1, 0})
	require.NoError(t, os.WriteFile(badVersion, badVersionData, 0o755))
	err = inspectCandidateELF(badVersion, testArchAMD64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ELF version")

	// 4e. Invalid e_ehsize (< 64)
	badEHSize := filepath.Join(tempDir, "badehsize")
	badEHSizeData := make([]byte, 64)
	copy(badEHSizeData, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(badEHSizeData[52:54], 40)
	require.NoError(t, os.WriteFile(badEHSize, badEHSizeData, 0o755))
	err = inspectCandidateELF(badEHSize, testArchAMD64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ELF header size")

	// 5. AMD64 ELF candidate matching AMD64
	amd64File := createDummyELF(t, tempDir, "amd64.elf", testArchAMD64)
	require.NoError(t, inspectCandidateELF(amd64File, testArchAMD64))
	require.NoError(t, inspectCandidateELF(amd64File, ""))

	// 5b. Unsupported target architecture
	err = inspectCandidateELF(amd64File, "mips")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported target architecture")

	// 6. AMD64 ELF candidate against ARM64 target -> mismatch
	err = inspectCandidateELF(amd64File, testArchARM64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match target architecture")

	// 7. ARM64 ELF candidate matching ARM64
	arm64File := createDummyELF(t, tempDir, "arm64.elf", testArchARM64)
	require.NoError(t, inspectCandidateELF(arm64File, testArchARM64))

	// 8. ARM64 ELF candidate against AMD64 target -> mismatch
	err = inspectCandidateELF(arm64File, testArchAMD64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match target architecture")
}

func TestResolveStubWithOptions_MinimalProfile(t *testing.T) {
	f := setupStubTestFixture(t)

	// Add minimal stub to sibling directory
	minSiblingStub := createDummyELF(t, f.siblingDir, StubBinaryMinimal, testArchAMD64)
	// Add minimal stub to trusted bin dir (PATH)
	minPathStub := createDummyELF(t, f.trustedBinDir, StubBinaryMinimal, testArchAMD64)
	require.NoError(t, os.Chmod(minPathStub, 0o755))

	t.Run("MinimalProfile_SiblingDiscovery", func(t *testing.T) {
		f.mockNativeDir(f.siblingDir)
		_ = os.Setenv("PATH", "")

		res, err := ResolveStubWithOptions(ResolveStubOptions{
			CLIProfile: StubProfileMinimal,
			TargetArch: testArchAMD64,
		})
		require.NoError(t, err)
		assert.Equal(t, minSiblingStub, res)
	})

	t.Run("MinimalProfile_PATHDiscovery", func(t *testing.T) {
		f.mockNativeDir(f.tmpDir) // sibling dir without stubs
		_ = os.Setenv("PATH", f.trustedBinDir)

		res, err := ResolveStubWithOptions(ResolveStubOptions{
			CLIProfile: StubProfileMinimal,
			TargetArch: testArchAMD64,
		})
		require.NoError(t, err)
		assert.Equal(t, minPathStub, res)
	})

	t.Run("MinimalProfile_Absent_ReturnsErrStubNotFound", func(t *testing.T) {
		emptyDir := filepath.Join(f.tmpDir, "empty-dir")
		require.NoError(t, os.MkdirAll(emptyDir, 0o755))
		f.mockNativeDir(emptyDir)
		_ = os.Setenv("PATH", "")

		_, err := ResolveStubWithOptions(ResolveStubOptions{
			CLIProfile: StubProfileMinimal,
			TargetArch: testArchAMD64,
		})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrStubNotFound)
		assert.Contains(t, err.Error(), StubBinaryMinimal)
	})

	t.Run("FullProfile_Absent_ReturnsErrStubNotFound", func(t *testing.T) {
		onlyMinDir := filepath.Join(f.tmpDir, "only-min-dir")
		require.NoError(t, os.MkdirAll(onlyMinDir, 0o755))
		createDummyELF(t, onlyMinDir, StubBinaryMinimal, testArchAMD64)
		f.mockNativeDir(onlyMinDir)
		_ = os.Setenv("PATH", "")

		_, err := ResolveStubWithOptions(ResolveStubOptions{
			CLIProfile: StubProfileFull,
			TargetArch: testArchAMD64,
		})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrStubNotFound)
		assert.Contains(t, err.Error(), StubBinaryFull)
	})
}

func TestResolveStubWithOptions_PrecedenceAndNotices(t *testing.T) {
	tempDir := t.TempDir()
	fullStub := createDummyELF(t, tempDir, StubBinaryFull, testArchAMD64)
	minStub := createDummyELF(t, tempDir, StubBinaryMinimal, testArchAMD64)

	t.Run("CLIStub_Full_With_CLIProfile_Minimal_WinsWithNotice", func(t *testing.T) {
		var notices []string
		warnFunc := func(format string, args ...any) {
			notices = append(notices, fmt.Sprintf(format, args...))
		}
		res, err := ResolveStubWithOptions(ResolveStubOptions{
			CLIStub:    fullStub,
			CLIProfile: StubProfileMinimal,
			WarnFunc:   warnFunc,
		})
		require.NoError(t, err)
		assert.Equal(t, fullStub, res)
		require.Len(t, notices, 1)
		assert.Contains(t, notices[0], "Using explicit launcher stub")
		assert.Contains(t, notices[0], "automatic stub-profile selection was bypassed")
	})

	t.Run("CLIStub_Minimal_With_CLIProfile_Full_WinsWithNotice", func(t *testing.T) {
		var notices []string
		warnFunc := func(format string, args ...any) {
			notices = append(notices, fmt.Sprintf(format, args...))
		}
		res, err := ResolveStubWithOptions(ResolveStubOptions{
			CLIStub:    minStub,
			CLIProfile: StubProfileFull,
			WarnFunc:   warnFunc,
		})
		require.NoError(t, err)
		assert.Equal(t, minStub, res)
		require.Len(t, notices, 1)
		assert.Contains(t, notices[0], "Using explicit launcher stub")
		assert.Contains(t, notices[0], "automatic stub-profile selection was bypassed")
	})

	t.Run("CLIStub_Minimal_With_CLIProfile_Minimal_WinsWithNotice", func(t *testing.T) {
		var notices []string
		warnFunc := func(format string, args ...any) {
			notices = append(notices, fmt.Sprintf(format, args...))
		}
		res, err := ResolveStubWithOptions(ResolveStubOptions{
			CLIStub:    minStub,
			CLIProfile: StubProfileMinimal,
			WarnFunc:   warnFunc,
		})
		require.NoError(t, err)
		assert.Equal(t, minStub, res)
		require.Len(t, notices, 1)
		assert.Contains(t, notices[0], "Using explicit launcher stub")
	})

	t.Run("CLIStub_Without_ExplicitProfile_EmitsNoNotice", func(t *testing.T) {
		var notices []string
		warnFunc := func(format string, args ...any) {
			notices = append(notices, fmt.Sprintf(format, args...))
		}
		res, err := ResolveStubWithOptions(ResolveStubOptions{
			CLIStub:  fullStub,
			WarnFunc: warnFunc,
		})
		require.NoError(t, err)
		assert.Equal(t, fullStub, res)
		assert.Empty(t, notices)
	})

	t.Run("ManifestStub_Full_With_ManifestProfile_Minimal_WinsWithNotice", func(t *testing.T) {
		var notices []string
		warnFunc := func(format string, args ...any) {
			notices = append(notices, fmt.Sprintf(format, args...))
		}
		res, err := ResolveStubWithOptions(ResolveStubOptions{
			ManifestStub:    fullStub,
			ManifestProfile: StubProfileMinimal,
			WarnFunc:        warnFunc,
		})
		require.NoError(t, err)
		assert.Equal(t, fullStub, res)
		require.Len(t, notices, 1)
		assert.Contains(t, notices[0], "Using explicit launcher stub")
	})

	t.Run("ManifestStub_Full_With_CLIProfile_Minimal_WinsWithNotice", func(t *testing.T) {
		var notices []string
		warnFunc := func(format string, args ...any) {
			notices = append(notices, fmt.Sprintf(format, args...))
		}
		res, err := ResolveStubWithOptions(ResolveStubOptions{
			ManifestStub: fullStub,
			CLIProfile:   StubProfileMinimal,
			WarnFunc:     warnFunc,
		})
		require.NoError(t, err)
		assert.Equal(t, fullStub, res)
		require.Len(t, notices, 1)
		assert.Contains(t, notices[0], "Using explicit launcher stub")
	})

	t.Run("CLIStub_Precedence_Over_ManifestStub_ManifestNotEvaluated", func(t *testing.T) {
		var notices []string
		warnFunc := func(format string, args ...any) {
			notices = append(notices, fmt.Sprintf(format, args...))
		}
		res, err := ResolveStubWithOptions(ResolveStubOptions{
			CLIStub:         fullStub,
			ManifestStub:    "nonexistent-manifest-stub-that-would-error-if-evaluated",
			ManifestProfile: StubProfileMinimal,
			WarnFunc:        warnFunc,
		})
		require.NoError(t, err)
		assert.Equal(t, fullStub, res)
		require.Len(t, notices, 1)
		assert.Contains(t, notices[0], "Using explicit launcher stub")
	})

	t.Run("Invalid_CLIProfile_FailsWithErrInvalidStubProfile", func(t *testing.T) {
		_, err := ResolveStubWithOptions(ResolveStubOptions{
			CLIProfile: "bogus",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidStubProfile)
	})

	t.Run("Invalid_ManifestProfile_FailsWithErrInvalidStubProfile", func(t *testing.T) {
		_, err := ResolveStubWithOptions(ResolveStubOptions{
			ManifestProfile: "bogus",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidStubProfile)
	})

	t.Run("Deliberate_CLIStub_Precedence_Over_ManifestProfile", func(t *testing.T) {
		// When CLIStub is specified without CLIProfile, it takes deliberate precedence over manifest profile
		res, err := ResolveStubWithOptions(ResolveStubOptions{
			CLIStub:         minStub,
			ManifestProfile: StubProfileFull,
		})
		require.NoError(t, err)
		assert.Equal(t, minStub, res)
	})

	t.Run("CLIProfile_Overrides_ManifestProfile", func(t *testing.T) {
		f := setupStubTestFixture(t)
		minSiblingStub := createDummyELF(t, f.siblingDir, StubBinaryMinimal, testArchAMD64)
		f.mockNativeDir(f.siblingDir)
		_ = os.Setenv("PATH", "")

		res, err := ResolveStubWithOptions(ResolveStubOptions{
			CLIProfile:      StubProfileMinimal,
			ManifestProfile: StubProfileFull,
		})
		require.NoError(t, err)
		assert.Equal(t, minSiblingStub, res)
	})
}

func TestManifestValidation_StubProfile(t *testing.T) {
	t.Parallel()

	t.Run("ValidMinimalProfile", func(t *testing.T) {
		m := &Manifest{
			AppName:     "test",
			TargetOS:    "linux",
			TargetArch:  "amd64",
			StubProfile: StubProfileMinimal,
			Variants:    []VariantConfig{{Level: "v1"}},
		}
		require.NoError(t, ValidateManifest(m))
		assert.Equal(t, StubProfileMinimal, m.StubProfile)
	})

	t.Run("ValidFullProfile", func(t *testing.T) {
		m := &Manifest{
			AppName:     "test",
			TargetOS:    "linux",
			TargetArch:  "amd64",
			StubProfile: StubProfileFull,
			Variants:    []VariantConfig{{Level: "v1"}},
		}
		require.NoError(t, ValidateManifest(m))
		assert.Equal(t, StubProfileFull, m.StubProfile)
	})

	t.Run("InvalidProfile_Fails", func(t *testing.T) {
		m := &Manifest{
			AppName:     "test",
			TargetOS:    "linux",
			TargetArch:  "amd64",
			StubProfile: "turbo",
			Variants:    []VariantConfig{{Level: "v1"}},
		}
		err := ValidateManifest(m)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidStubProfile)
	})

	t.Run("ExplicitStubWithProfile_Succeeds", func(t *testing.T) {
		m := &Manifest{
			AppName:     "test",
			TargetOS:    "linux",
			TargetArch:  "amd64",
			Stub:        "bin/microfat-stub",
			StubProfile: StubProfileMinimal,
			Variants:    []VariantConfig{{Level: "v1"}},
		}
		require.NoError(t, ValidateManifest(m))
		assert.Equal(t, StubProfileMinimal, m.StubProfile)
		assert.Equal(t, "bin/microfat-stub", m.Stub)
	})
}

func TestManifest_YAMLAndJSON_StubProfile(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	yamlContent := `name: yaml-app
package: .
target_os: linux
target_arch: amd64
stub_profile: minimal
variants:
  - level: v1
`
	yamlPath := filepath.Join(tempDir, "manifest.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0o644))

	mYaml, err := LoadManifest(yamlPath)
	require.NoError(t, err)
	assert.Equal(t, StubProfileMinimal, mYaml.StubProfile)

	jsonContent := `{
  "name": "json-app",
  "package": ".",
  "target_os": "linux",
  "target_arch": "amd64",
  "stub_profile": "minimal",
  "variants": [
    {"level": "v1"}
  ]
}`
	jsonPath := filepath.Join(tempDir, "manifest.json")
	require.NoError(t, os.WriteFile(jsonPath, []byte(jsonContent), 0o644))

	mJSON, err := LoadManifest(jsonPath)
	require.NoError(t, err)
	assert.Equal(t, StubProfileMinimal, mJSON.StubProfile)
}
