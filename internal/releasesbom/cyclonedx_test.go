package releasesbom

import (
	"debug/buildinfo"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	cdx "github.com/CycloneDX/cyclonedx-go"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/EpicBlackWolfZ/microfat/internal/sbom"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureModuleVersion = "v1.0.0"

func inventoryFixture(t *testing.T, arch string) (*releasecheck.ArchiveFacts, *releasecheck.ArchiveInventory, Metadata) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("Fixture license text\n"), 0o600))
	facts := &releasecheck.ArchiveFacts{ArchiveName: "microfat_0.2.5_linux_" + arch + ".tar.gz", ArchiveSHA256: strings.Repeat("a", 64),
		TargetArch: arch, ExtractedDir: dir, Executables: map[string]*releasecheck.ExecutableFacts{},
		EmbeddedVariants: map[string]*releasecheck.VariantFacts{}}
	info := func(version string) *buildinfo.BuildInfo {
		return &debug.BuildInfo{GoVersion: "go1.27.1", Path: "example.com/application/cmd/app",
			Main:     debug.Module{Path: "example.com/application", Version: fixtureModuleVersion},
			Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: strings.Repeat("f", 40)}, {Key: "GOARCH", Value: arch}},
			Deps: []*debug.Module{{Path: "example.com/dependency", Version: version, Sum: "h1:original",
				Replace: &debug.Module{Path: "example.com/replacement", Version: version, Sum: "h1:replacement"}}}}
	}
	for _, name := range []string{"microfat", "microfat-stub", "microfat-stub-minimal"} {
		facts.Executables[name] = &releasecheck.ExecutableFacts{Name: name, SHA256: strings.Repeat("b", 64),
			BuildInfo: info(fixtureModuleVersion)}
	}
	contract, err := releasecheck.NewReleaseContract("0.2.5")
	require.NoError(t, err)
	for index, tier := range contract.ExpectedTiers[arch] {
		version := fixtureModuleVersion
		if index == 1 {
			version = "v2.0.0"
		}
		facts.EmbeddedVariants[tier] = &releasecheck.VariantFacts{Level: tier, SHA256: strings.Repeat("c", 64), BuildInfo: info(version)}
	}
	inventory, err := releasecheck.ExtractArchiveInventory(facts)
	require.NoError(t, err)
	return facts, inventory, Metadata{Version: "0.2.5", Commit: strings.Repeat("e", 40),
		Created: time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC)}
}

func TestCycloneDXIndependentInventory(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{releasecheck.ArchAMD64, "arm64"} {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()
			facts, inv, meta := inventoryFixture(t, arch)
			data, err := CycloneDX(facts, inv, meta)
			require.NoError(t, err)
			require.NoError(t, releasecheck.ValidateModernCycloneDXBytes(data, facts, inv))
			catalog, err := sbom.ReadCycloneDX(data)
			require.NoError(t, err)
			var modules []cdx.Component
			for _, c := range catalog.Components {
				if c.Type == cdx.ComponentTypeLibrary {
					modules = append(modules, c)
				}
			}
			require.Len(t, modules, 2, "different linked versions must not collapse by module path")
			for _, m := range modules {
				assert.Equal(t, "example.com/replacement", m.Name)
				assert.Contains(t, m.PackageURL, "example.com/replacement@")
				props, err := sbom.Properties(m)
				require.NoError(t, err)
				assert.Equal(t, "h1:original", props["microfat:module:sum"])
				assert.Equal(t, "h1:replacement", props["microfat:module:replace_sum"])
			}
			second, err := CycloneDX(facts, inv, meta)
			require.NoError(t, err)
			assert.JSONEq(t, string(data), string(second), "fixed metadata produces a deterministic document")
		})
	}
}

func TestLocalModuleReplacementHasNoInventedRegistryURL(t *testing.T) {
	t.Parallel()
	for _, dep := range []releasecheck.ModuleDep{
		{Path: "example.com/original", Version: fixtureModuleVersion, ReplacePath: "../local"},
		{Path: "example.com/original", Version: fixtureModuleVersion, ReplacePath: "/tmp/local"},
		{Path: "example.com/original"},
	} {
		c := moduleComponent(dep)
		assert.Empty(t, c.PackageURL)
		p, err := sbom.Properties(c)
		require.NoError(t, err)
		assert.Equal(t, dep.Path, p["microfat:module:path"])
	}
}

func TestBuilderRejectsMissingFacts(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*releasecheck.ArchiveFacts, *releasecheck.ArchiveInventory){
		"archive_name": func(f *releasecheck.ArchiveFacts, _ *releasecheck.ArchiveInventory) { f.ArchiveName = "invalid" },
		"license": func(f *releasecheck.ArchiveFacts, _ *releasecheck.ArchiveInventory) {
			f.ExtractedDir = filepath.Join(f.ExtractedDir, "missing")
		},
		"executable": func(f *releasecheck.ArchiveFacts, _ *releasecheck.ArchiveInventory) {
			f.Executables["microfat-stub"] = nil
		},
		"variant": func(f *releasecheck.ArchiveFacts, _ *releasecheck.ArchiveInventory) { f.EmbeddedVariants["v2"] = nil },
		"binary_inventory": func(_ *releasecheck.ArchiveFacts, i *releasecheck.ArchiveInventory) {
			delete(i.Binaries, "microfat-stub")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, i, m := inventoryFixture(t, releasecheck.ArchAMD64)
			mutate(f, i)
			_, err := CycloneDX(f, i, m)
			require.Error(t, err)
		})
	}
	_, err := CycloneDX(nil, nil, Metadata{})
	require.Error(t, err)
}

func TestIndependentValidationRejectsChangedMetadata(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*cdx.BOM){
		"archive_version":        func(b *cdx.BOM) { b.Metadata.Component.Version = "9.9.9" },
		"archive_properties":     func(b *cdx.BOM) { b.Metadata.Component.Properties = nil },
		"missing_hash":           func(b *cdx.BOM) { b.Metadata.Component.Hashes = nil },
		"unattributed_component": func(b *cdx.BOM) { (*b.Components)[0].Type = cdx.ComponentTypeFile },
		"missing_binary": func(b *cdx.BOM) {
			children := b.Metadata.Component.Components
			b.Components = &[]cdx.Component{(*children)[0]}
		},
		"duplicate_binary": func(b *cdx.BOM) {
			copy := (*b.Metadata.Component.Components)[1]
			copy.BOMRef = "urn:duplicate:binary"
			*b.Components = append(*b.Components, copy)
		},
		"duplicate_module_coordinate": func(b *cdx.BOM) {
			copy := (*b.Components)[0]
			copy.BOMRef = "urn:duplicate:module"
			*b.Components = append(*b.Components, copy)
		},
		"unused_module": func(b *cdx.BOM) {
			copy := moduleComponent(releasecheck.ModuleDep{Path: "example.com/unused", Version: fixtureModuleVersion})
			*b.Components = append(*b.Components, copy)
		},
		"missing_license": func(b *cdx.BOM) { b.Metadata.Component.Licenses = nil },
		"invalid_binary_settings": func(b *cdx.BOM) {
			props := (*b.Metadata.Component.Components)[0].Properties
			for index := range *props {
				if (*props)[index].Name == "microfat:build_settings" {
					(*props)[index].Value = "not JSON"
				}
			}
		},
		"wrong_binary_settings": func(b *cdx.BOM) {
			props := (*b.Metadata.Component.Components)[0].Properties
			for index := range *props {
				if (*props)[index].Name == "microfat:build_settings" {
					(*props)[index].Value = "{}"
				}
			}
		},
		"archive_hash":      func(b *cdx.BOM) { (*b.Metadata.Component.Hashes)[0].Value = strings.Repeat("f", 64) },
		"binary_hash":       func(b *cdx.BOM) { (*(*b.Metadata.Component.Components)[0].Hashes)[0].Value = strings.Repeat("f", 64) },
		"binary_name":       func(b *cdx.BOM) { (*b.Metadata.Component.Components)[0].Name = "unexpected" },
		"binary_purl":       func(b *cdx.BOM) { (*b.Metadata.Component.Components)[0].PackageURL = "pkg:generic/unexpected@1" },
		"binary_source":     func(b *cdx.BOM) { (*b.Metadata.Component.Components)[0].ExternalReferences = nil },
		"binary_properties": func(b *cdx.BOM) { (*b.Metadata.Component.Components)[0].Properties = nil },
		"module_version":    func(b *cdx.BOM) { (*b.Components)[0].Version = "v9.0.0" },
		"module_purl":       func(b *cdx.BOM) { (*b.Components)[0].PackageURL = "pkg:golang/example.com/wrong@v1.0.0" },
		"dependency_set":    func(b *cdx.BOM) { (*b.Dependencies)[0].Dependencies = &[]string{} },
		"license_text":      func(b *cdx.BOM) { (*b.Metadata.Component.Licenses)[0].License.Text.Content = "changed license" },
		"tools":             func(b *cdx.BOM) { b.Metadata.Tools = nil },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, i, m := inventoryFixture(t, releasecheck.ArchAMD64)
			data, err := CycloneDX(f, i, m)
			require.NoError(t, err)
			var bom cdx.BOM
			require.NoError(t, json.Unmarshal(data, &bom))
			mutate(&bom)
			changed, err := json.Marshal(bom)
			require.NoError(t, err)
			require.Error(t, releasecheck.ValidateModernCycloneDXBytes(changed, f, i))
		})
	}
}

func TestModernValidationRequiresIndependentFacts(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*releasecheck.ArchiveFacts, *releasecheck.ArchiveInventory){
		"invalid_archive_identity": func(f *releasecheck.ArchiveFacts, _ *releasecheck.ArchiveInventory) { f.ArchiveName = "invalid" },
		"missing_independent_license": func(f *releasecheck.ArchiveFacts, _ *releasecheck.ArchiveInventory) {
			f.ExtractedDir = filepath.Join(f.ExtractedDir, "missing")
		},
		"missing_executable_bytes": func(f *releasecheck.ArchiveFacts, _ *releasecheck.ArchiveInventory) {
			f.Executables["microfat-stub"] = nil
		},
		"missing_variant_bytes": func(f *releasecheck.ArchiveFacts, _ *releasecheck.ArchiveInventory) {
			f.EmbeddedVariants["v2"] = nil
		},
		"missing_binary_record":  func(_ *releasecheck.ArchiveFacts, i *releasecheck.ArchiveInventory) { i.Binaries["microfat"] = nil },
		"changed_inventory_size": func(_ *releasecheck.ArchiveFacts, i *releasecheck.ArchiveInventory) { delete(i.Binaries, "microfat") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			facts, inv, meta := inventoryFixture(t, releasecheck.ArchAMD64)
			data, err := CycloneDX(facts, inv, meta)
			require.NoError(t, err)
			mutate(facts, inv)
			require.Error(t, releasecheck.ValidateModernCycloneDXBytes(data, facts, inv))
		})
	}
	facts, inv, meta := inventoryFixture(t, releasecheck.ArchAMD64)
	data, err := CycloneDX(facts, inv, meta)
	require.NoError(t, err)
	require.ErrorContains(t, releasecheck.ValidateModernCycloneDXBytes(data, nil, inv), "independent")
	require.ErrorContains(t, releasecheck.ValidateModernCycloneDXBytes(data, facts, nil), "independent")
}
