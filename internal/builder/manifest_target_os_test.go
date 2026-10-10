package builder_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/builder"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateManifest_TargetOperatingSystem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		targetOS string
		valid    bool
	}{
		{name: "default", valid: true},
		{name: "linux", targetOS: testOSLinux, valid: true},
		{name: "uppercase", targetOS: "LINUX", valid: true},
		{name: "mixed case", targetOS: "LiNuX", valid: true},
		{name: "freebsd", targetOS: "freebsd"},
		{name: "windows", targetOS: "windows"},
		{name: "darwin", targetOS: "darwin"},
		{name: "unknown", targetOS: "banana"},
		{name: "leading whitespace", targetOS: " linux"},
		{name: "trailing whitespace", targetOS: "linux "},
		{name: "whitespace only", targetOS: "\t"},
	}
	for _, arch := range []struct{ name, level string }{
		{name: testArchAMD64, level: "v1"},
		{name: testArchARM64, level: testLevelV8_0},
	} {
		for _, tc := range cases {
			t.Run(arch.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				m := &builder.Manifest{
					TargetOS:   tc.targetOS,
					TargetArch: arch.name,
					Env:        map[string]string{keyGOOS: testOSLinux},
					Variants:   []builder.VariantConfig{{Level: arch.level}},
				}
				err := builder.ValidateManifest(m)
				if !tc.valid {
					require.ErrorIs(t, err, builder.ErrUnsupportedOS)
					require.ErrorIs(t, err, pack.ErrUnsupportedOS)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, testOSLinux, m.TargetOS)
			})
		}
	}

	t.Run("invalid architecture retains precedence", func(t *testing.T) {
		t.Parallel()

		m := &builder.Manifest{TargetOS: "freebsd", TargetArch: "banana"}
		require.ErrorIs(t, builder.ValidateManifest(m), builder.ErrUnsupportedArch)
	})
}

func TestBuildAndPack_UnsupportedOperatingSystemRejectsBeforeCompilation(t *testing.T) {
	t.Parallel()

	for _, targetOS := range []string{"freebsd", "windows", "darwin", "banana", " linux", "linux ", "\t"} {
		t.Run(targetOS, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			outputPath := filepath.Join(tempDir, "output.fat")
			stubPath := filepath.Join(tempDir, "stub")
			compilerPath := filepath.Join(tempDir, "compiler")
			markerPath := filepath.Join(tempDir, "compiler-invoked")
			sentinel := []byte("existing output")
			require.NoError(t, os.WriteFile(outputPath, sentinel, 0o600))
			require.NoError(t, os.WriteFile(stubPath, []byte("stub"), 0o755))
			compiler := "#!/bin/sh\n: > \"$(dirname \"$0\")/compiler-invoked\"\nexit 1\n"
			require.NoError(t, os.WriteFile(compilerPath, []byte(compiler), 0o755))

			m := &builder.Manifest{
				TargetOS:   targetOS,
				TargetArch: testArchAMD64,
				Output:     outputPath,
				Stub:       stubPath,
				Dir:        tempDir,
				Variants:   []builder.VariantConfig{{Level: "v1", PGO: pgoOff}},
			}
			result, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{
				GoBinary:          compilerPath,
				SkipELFValidation: true,
			})
			require.ErrorIs(t, err, pack.ErrUnsupportedOS)
			assert.Nil(t, result)
			assert.NoFileExists(t, markerPath)
			data, readErr := os.ReadFile(outputPath)
			require.NoError(t, readErr)
			assert.Equal(t, sentinel, data)
			intermediates, globErr := filepath.Glob(filepath.Join(tempDir, ".microfat-pgo-*"))
			require.NoError(t, globErr)
			assert.Empty(t, intermediates)
		})
	}
}
