package releasecheck

import (
	"debug/buildinfo"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testPkgSys      = "golang.org/x/sys"
	testPkgCobra    = "github.com/spf13/cobra"
	testPkgCompress = "github.com/klauspost/compress"
)

func TestExtractArchiveInventory(t *testing.T) {
	t.Parallel()

	t.Run("NilFacts", func(t *testing.T) {
		t.Parallel()
		inv, err := ExtractArchiveInventory(nil)
		require.Error(t, err)
		assert.Nil(t, inv)
		assert.Contains(t, err.Error(), "archive facts cannot be nil")
	})

	t.Run("MissingRootExecutableBuildInfo", func(t *testing.T) {
		t.Parallel()
		facts := &ArchiveFacts{
			ArchiveName: "test.tar.gz",
			TargetArch:  ArchAMD64,
			Executables: map[string]*ExecutableFacts{
				ReleaseProjectName: {
					Name:      ReleaseProjectName,
					BuildInfo: nil, // missing
				},
			},
			EmbeddedVariants: make(map[string]*VariantFacts),
		}
		inv, err := ExtractArchiveInventory(facts)
		require.Error(t, err)
		assert.Nil(t, inv)
		assert.Contains(t, err.Error(), "missing buildinfo for root executable microfat")
	})

	t.Run("MissingVariantBuildInfo", func(t *testing.T) {
		t.Parallel()
		facts := &ArchiveFacts{
			ArchiveName: "test.tar.gz",
			TargetArch:  ArchAMD64,
			Executables: map[string]*ExecutableFacts{
				ReleaseProjectName: {
					Name: ReleaseProjectName,
					BuildInfo: &buildinfo.BuildInfo{
						GoVersion: "go1.27.1",
						Path:      "main",
					},
				},
			},
			EmbeddedVariants: map[string]*VariantFacts{
				"v1": {
					Level:     "v1",
					BuildInfo: nil, // missing
				},
			},
		}
		inv, err := ExtractArchiveInventory(facts)
		require.Error(t, err)
		assert.Nil(t, inv)
		assert.Contains(t, err.Error(), "missing buildinfo for embedded variant v1")
	})

	t.Run("ValidExtractionWithReplacementsAndSettings", func(t *testing.T) {
		t.Parallel()
		stubBI := &buildinfo.BuildInfo{
			GoVersion: "go1.27.1",
			Path:      "github.com/EpicBlackWolfZ/microfat/cmd/microfat-stub",
			Main: debug.Module{
				Path:    "github.com/EpicBlackWolfZ/microfat",
				Version: "v0.2.3",
			},
			Deps: []*debug.Module{
				{
					Path:    testPkgSys,
					Version: "v0.48.0",
				},
				nil, // test nil safety
			},
			Settings: []debug.BuildSetting{
				{Key: "-trimpath", Value: "true"},
			},
		}

		variantBI := &buildinfo.BuildInfo{
			GoVersion: "go1.27.1",
			Path:      "github.com/EpicBlackWolfZ/microfat/cmd/microfat",
			Main: debug.Module{
				Path:    "github.com/EpicBlackWolfZ/microfat",
				Version: "v0.2.3",
			},
			Deps: []*debug.Module{
				{
					Path:    testPkgCobra,
					Version: "v1.10.2",
					Replace: &debug.Module{
						Path:    "github.com/custom/cobra",
						Version: "v1.10.3",
					},
				},
				{
					Path:    testPkgCompress,
					Version: "v1.20.0",
				},
			},
			Settings: []debug.BuildSetting{
				{Key: "GOAMD64", Value: "v3"},
			},
		}

		facts := &ArchiveFacts{
			ArchiveName: "microfat_0.2.3_linux_amd64.tar.gz",
			TargetArch:  ArchAMD64,
			Executables: map[string]*ExecutableFacts{
				ReleaseFullStub: {
					Name:      ReleaseFullStub,
					BuildInfo: stubBI,
				},
			},
			EmbeddedVariants: map[string]*VariantFacts{
				"v3": {
					Level:     "v3",
					BuildInfo: variantBI,
				},
			},
		}

		inv, err := ExtractArchiveInventory(facts)
		require.NoError(t, err)
		require.NotNil(t, inv)

		assert.Equal(t, "microfat_0.2.3_linux_amd64.tar.gz", inv.ArchiveName)
		assert.Equal(t, ArchAMD64, inv.TargetArch)

		// Check stub inventory
		stubInv, ok := inv.Binaries[ReleaseFullStub]
		require.True(t, ok)
		assert.Equal(t, "go1.27.1", stubInv.GoVersion)
		assert.Equal(t, "true", stubInv.Settings["-trimpath"])
		assert.Equal(t, "v0.48.0", stubInv.Dependencies[testPkgSys].Version)

		// Check variant inventory
		varInv, ok := inv.Binaries["variant:v3"]
		require.True(t, ok)
		assert.Equal(t, "v3", varInv.VariantTier)
		assert.Equal(t, "v3", varInv.Settings["GOAMD64"])
		cobraDep := varInv.Dependencies[testPkgCobra]
		assert.Equal(t, "v1.10.2", cobraDep.Version)
		assert.Equal(t, "github.com/custom/cobra", cobraDep.ReplacePath)
		assert.Equal(t, "v1.10.3", cobraDep.ReplaceVer)

		// Check union
		assert.Contains(t, inv.AllDependencies, testPkgSys)
		assert.Contains(t, inv.AllDependencies, testPkgCobra)
		assert.Contains(t, inv.AllDependencies, testPkgCompress)
	})
}
