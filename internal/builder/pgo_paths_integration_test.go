package builder_test

import (
	"context"
	"debug/buildinfo"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/builder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAndPack_QuotedPGOPathPassedToGo(t *testing.T) {
	t.Parallel()

	for _, variant := range []bool{false, true} {
		name := "default_pgo"
		if variant {
			name = "variant_pgo"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			stub := createDummyELF(t, dir, "stub", testArchAMD64)
			pkgDir := filepath.Join(dir, "pkg")
			require.NoError(t, os.Mkdir(pkgDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module pgopath\ngo 1.27.2\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
			// Go accepts an empty profile. The trimmed control is deliberately invalid.
			const profileName = " profile.pgo "
			profilePath := filepath.Join(dir, profileName)
			require.NoError(t, os.WriteFile(profilePath, nil, 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "profile.pgo"), []byte("invalid profile"), 0o600))
			manifest := fmt.Sprintf("package: pkg\noutput: fat\nstub: %q\ndefault_pgo: %q\nvariants:\n  - level: v1\n",
				stub, profileName)
			if variant {
				manifest = fmt.Sprintf("package: pkg\noutput: fat\nstub: %q\ndefault_pgo: off\nvariants:\n  - level: v1\n    pgo: %q\n",
					stub, profileName)
			}
			manifestPath := filepath.Join(dir, "pgo.yaml")
			require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o600))
			m, err := builder.LoadManifest(manifestPath)
			require.NoError(t, err)
			res, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{KeepIntermediates: true})
			require.NoError(t, err)
			assert.Equal(t, "-pgo="+profilePath, res.VariantPGOApplied["v1"])
			bi, err := buildinfo.ReadFile(res.CompiledVariants["v1"])
			require.NoError(t, err)
			settings := make(map[string]string)
			for _, setting := range bi.Settings {
				settings[setting.Key] = setting.Value
			}
			assert.Equal(t, profilePath, settings["-pgo"], "compiled Go artifact must record the exact profile")
			require.FileExists(t, res.OutputPath)
		})
	}
}
