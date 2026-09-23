package builder_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/builder"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateManifest_TargetEnvironment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		manifest    *builder.Manifest
		expectedErr string
	}{
		{
			name: "amd64_rejects_inapplicable_GOARM64_in_root",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Env: map[string]string{
					keyGOARM64: testLevelV8_0,
				},
				Variants: []builder.VariantConfig{{Level: "v1"}},
			},
			expectedErr: "architecture-inapplicable key \"GOARM64\" in manifest root for amd64",
		},
		{
			name: "amd64_rejects_inapplicable_empty_GOARM64_in_variant",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Variants: []builder.VariantConfig{
					{
						Level: "v1",
						Env: map[string]string{
							keyGOARM64: "",
						},
					},
				},
			},
			expectedErr: "architecture-inapplicable key \"GOARM64\" in variant v1 for amd64",
		},
		{
			name: "arm64_rejects_inapplicable_GOAMD64_in_root",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchARM64,
				Env: map[string]string{
					keyGOAMD64: "v3",
				},
				Variants: []builder.VariantConfig{{Level: testLevelV8_0}},
			},
			expectedErr: "architecture-inapplicable key \"GOAMD64\" in manifest root for arm64",
		},
		{
			name: "arm64_rejects_inapplicable_GOAMD64_in_variant",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchARM64,
				Variants: []builder.VariantConfig{
					{
						Level: testLevelV8_0,
						Env: map[string]string{
							keyGOAMD64: "v1",
						},
					},
				},
			},
			expectedErr: "architecture-inapplicable key \"GOAMD64\" in variant v8.0 for arm64",
		},
		{
			name: "rejects_empty_applicable_GOOS_in_root",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Env: map[string]string{
					keyGOOS: "",
				},
				Variants: []builder.VariantConfig{{Level: "v1"}},
			},
			expectedErr: "empty reserved target variable \"GOOS\" in manifest root",
		},
		{
			name: "rejects_empty_applicable_GOARCH_in_root",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Env: map[string]string{
					keyGOARCH: "",
				},
				Variants: []builder.VariantConfig{{Level: "v1"}},
			},
			expectedErr: "empty reserved target variable \"GOARCH\" in manifest root",
		},
		{
			name: "arm64_rejects_empty_applicable_GOARM64_in_root",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchARM64,
				Env: map[string]string{
					keyGOARM64: "",
				},
				Variants: []builder.VariantConfig{{Level: testLevelV8_0}},
			},
			expectedErr: "empty reserved target variable \"GOARM64\" in manifest root",
		},
		{
			name: "arm64_rejects_root_tier_contradicting_variant",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchARM64,
				Env: map[string]string{
					keyGOARM64: testLevelV8_0,
				},
				Variants: []builder.VariantConfig{
					{Level: testLevelV8_0},
					{Level: testLevelV8_1},
				},
			},
			expectedErr: "root GOARM64=\"v8.0\" contradicts variant v8.1",
		},
		{
			name: "rejects_empty_applicable_GOARCH_in_variant",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Variants: []builder.VariantConfig{
					{
						Level: "v1",
						Env: map[string]string{
							keyGOARCH: "",
						},
					},
				},
			},
			expectedErr: "empty reserved target variable \"GOARCH\" in variant v1",
		},
		{
			name: "rejects_empty_applicable_GOAMD64_in_variant",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Variants: []builder.VariantConfig{
					{
						Level: "v1",
						Env: map[string]string{
							keyGOAMD64: "",
						},
					},
				},
			},
			expectedErr: "empty reserved target variable \"GOAMD64\" in variant v1",
		},
		{
			name: "rejects_empty_variant_GOOS",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Variants: []builder.VariantConfig{
					{
						Level: "v1",
						Env: map[string]string{
							keyGOOS: "",
						},
					},
				},
			},
			expectedErr: "empty reserved target variable \"GOOS\" in variant v1",
		},
		{
			name: "rejects_empty_variant_GOARCH",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Variants: []builder.VariantConfig{
					{
						Level: "v1",
						Env: map[string]string{
							keyGOARCH: "",
						},
					},
				},
			},
			expectedErr: "empty reserved target variable \"GOARCH\" in variant v1",
		},
		{
			name: "rejects_contradictory_root_GOOS",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Env: map[string]string{
					keyGOOS: "darwin",
				},
				Variants: []builder.VariantConfig{{Level: "v1"}},
			},
			expectedErr: "root GOOS=\"darwin\" contradicts manifest target_os=\"linux\"",
		},
		{
			name: "rejects_contradictory_variant_GOOS",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Variants: []builder.VariantConfig{
					{
						Level: "v1",
						Env: map[string]string{
							keyGOOS: "windows",
						},
					},
				},
			},
			expectedErr: "variant v1 GOOS=\"windows\" contradicts manifest target_os=\"linux\"",
		},
		{
			name: "rejects_contradictory_root_GOARCH",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Env: map[string]string{
					keyGOARCH: "arm64",
				},
				Variants: []builder.VariantConfig{{Level: "v1"}},
			},
			expectedErr: "root GOARCH=\"arm64\" contradicts manifest target_arch=\"amd64\"",
		},
		{
			name: "rejects_contradictory_variant_GOARCH",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Variants: []builder.VariantConfig{
					{
						Level: "v1",
						Env: map[string]string{
							keyGOARCH: "arm64",
						},
					},
				},
			},
			expectedErr: "variant v1 GOARCH=\"arm64\" contradicts manifest target_arch=\"amd64\"",
		},
		{
			name: "rejects_contradictory_variant_GOAMD64",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Variants: []builder.VariantConfig{
					{
						Level: "v1",
						Env: map[string]string{
							keyGOAMD64: "v4",
						},
					},
				},
			},
			expectedErr: "variant v1 GOAMD64=\"v4\" contradicts declared level",
		},
		{
			name: "rejects_root_tier_contradicting_one_variant_in_multi_variant_build",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Env: map[string]string{
					keyGOAMD64: "v1",
				},
				Variants: []builder.VariantConfig{
					{Level: "v1"},
					{Level: "v3"},
				},
			},
			expectedErr: "root GOAMD64=\"v1\" contradicts variant v3",
		},
		{
			name: "rejects_root_contradiction_masked_by_child_override",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Env: map[string]string{
					keyGOAMD64: "v1",
				},
				Variants: []builder.VariantConfig{
					{Level: "v1"},
					{
						Level: "v3",
						Env: map[string]string{
							keyGOAMD64: "v3", // Child attempts to mask root contradiction
						},
					},
				},
			},
			expectedErr: "root GOAMD64=\"v1\" contradicts variant v3",
		},
		{
			name: "rejects_empty_variable_name",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Env: map[string]string{
					"": "val",
				},
				Variants: []builder.VariantConfig{{Level: "v1"}},
			},
			expectedErr: "empty environment variable name in manifest root",
		},
		{
			name: "rejects_variable_name_containing_equal",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Variants: []builder.VariantConfig{
					{
						Level: "v1",
						Env: map[string]string{
							"INVALID=KEY": "val",
						},
					},
				},
			},
			expectedErr: "contains invalid characters in variant v1",
		},
		{
			name: "rejects_variable_value_containing_nul",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Env: map[string]string{
					"FOO": "bar\x00baz",
				},
				Variants: []builder.VariantConfig{{Level: "v1"}},
			},
			expectedErr: "contains NUL in manifest root",
		},
		{
			name: "accepts_redundant_matching_reserved_settings",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchAMD64,
				Env: map[string]string{
					keyGOOS:   testOSLinux,
					keyGOARCH: testArchAMD64,
				},
				Variants: []builder.VariantConfig{
					{
						Level: "v1",
						Env: map[string]string{
							keyGOOS:    testOSLinux,
							keyGOARCH:  testArchAMD64,
							keyGOAMD64: "v1",
						},
					},
					{
						Level: "v3",
						Env: map[string]string{
							keyGOAMD64: "v3",
						},
					},
				},
			},
			expectedErr: "",
		},
		{
			name: "accepts_redundant_matching_arm64_lse_setting",
			manifest: &builder.Manifest{
				TargetOS:   testOSLinux,
				TargetArch: microarch.ArchARM64,
				Variants: []builder.VariantConfig{
					{
						Level: testLevelV8_1,
						Env: map[string]string{
							keyGOARM64: "v8.1,lse", // Redundant canonicalized equivalent
						},
					},
				},
			},
			expectedErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := builder.ValidateManifest(tt.manifest)
			if tt.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.ErrorIs(t, err, builder.ErrInvalidManifest)
				require.ErrorIs(t, err, builder.ErrInvalidTargetEnv)
				assert.Contains(t, err.Error(), tt.expectedErr)
			}
		})
	}
}

func TestBuildAndPack_TargetEnvValidation_PreservesPreexistingOutput(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "preexisting.bin")
	originalContent := []byte("original uncorrupted binary content")
	require.NoError(t, os.WriteFile(outFile, originalContent, 0o755))

	m := &builder.Manifest{
		AppName:    "fail-test",
		Output:     outFile,
		TargetOS:   testOSLinux,
		TargetArch: microarch.ArchAMD64,
		Variants: []builder.VariantConfig{
			{
				Level: "v1",
				Env: map[string]string{
					keyGOAMD64: "v4", // Contradiction
				},
			},
		},
	}

	res, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, builder.ErrInvalidTargetEnv)
	assert.Nil(t, res)

	// Pre-existing file must remain byte-for-byte identical
	data, readErr := os.ReadFile(outFile)
	require.NoError(t, readErr)
	assert.Equal(t, originalContent, data, "pre-existing output must not be modified when manifest validation fails")
}
