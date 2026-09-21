package releasecheck

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	cdx "github.com/CycloneDX/cyclonedx-go"
	"github.com/EpicBlackWolfZ/microfat/internal/sbom"
)

// ValidateModernCycloneDXBytes independently compares a schema-valid modern BOM
// with hashes and build information extracted from the actual release archive.
func ValidateModernCycloneDXBytes(data []byte, facts *ArchiveFacts, inv *ArchiveInventory) error {
	catalog, err := sbom.ReadCycloneDX(data)
	if err != nil {
		return err
	}
	return validateModernCatalog(catalog, facts, inv)
}

// ValidateModernSPDXBytes verifies SPDX 3.0.1 JSON-LD and the independently
// reconstructed archive inventory, including native graph attribution.
func ValidateModernSPDXBytes(data []byte, facts *ArchiveFacts, inv *ArchiveInventory) error {
	graph, err := sbom.ReadSPDX(data)
	if err != nil {
		return err
	}
	catalog, err := graph.Catalog()
	if err != nil {
		return err
	}
	if err := validateModernCatalog(catalog, facts, inv); err != nil {
		return err
	}
	return graph.ValidateAttribution(catalog)
}

func validateModernCatalog(c *sbom.Catalog, facts *ArchiveFacts, inv *ArchiveInventory) error {
	if facts == nil || inv == nil {
		return fmt.Errorf("missing independent archive inventory")
	}
	version, err := checkModernArchive(c.Components[c.Root], facts)
	if err != nil {
		return err
	}
	binaries, modules, err := indexModernComponents(c)
	if err != nil {
		return err
	}
	if len(binaries) != len(inv.Binaries) {
		return fmt.Errorf("modern SBOM binary inventory count mismatch")
	}
	usedModules := map[string]bool{}
	for id, bin := range inv.Binaries {
		component, ok := binaries[id]
		if !ok || bin == nil {
			return fmt.Errorf("modern SBOM binary %s is missing", id)
		}
		if err := checkModernBinary(component, bin, facts, version); err != nil {
			return err
		}
		if err := checkModernDependencies(c, component, bin, modules, usedModules); err != nil {
			return err
		}
	}
	if len(usedModules) != len(modules) {
		return fmt.Errorf("modern SBOM contains modules not linked by any binary")
	}
	if err := checkModernContainment(c, binaries, facts); err != nil {
		return err
	}
	if err := checkModernLicenses(c, facts); err != nil {
		return err
	}
	return checkModernMetadata(c.BOM.Metadata)
}

func checkModernArchive(root cdx.Component, facts *ArchiveFacts) (string, error) {
	identity, err := ParseReleaseArchiveName(facts.ArchiveName)
	if err != nil {
		return "", err
	}
	if root.Type != cdx.ComponentTypeFile || root.Name != facts.ArchiveName || root.Version != identity.Version {
		return "", fmt.Errorf("modern SBOM archive identity mismatch")
	}
	if err := checkModernProperties(root, map[string]string{"microfat:target_arch": facts.TargetArch,
		"microfat:release_version": identity.Version, "microfat:component_type": "file"}); err != nil {
		return "", err
	}
	if err := checkModernHash(root, facts.ArchiveSHA256); err != nil {
		return "", err
	}
	return identity.Version, nil
}

func checkModernLicenses(c *sbom.Catalog, facts *ArchiveFacts) error {
	// #nosec G304 -- facts.ExtractedDir is the private verified archive staging directory.
	data, err := os.ReadFile(filepath.Join(facts.ExtractedDir, "LICENSE"))
	if err != nil {
		return fmt.Errorf("reading independent archive license: %w", err)
	}
	digest := sha256.Sum256(data)
	const apacheLicenseSHA = "ae380bc0d675e1d3bdf12ef3fe0c958f07365cf7bf20656ae95fce881e293f53"
	for _, component := range c.Components {
		if component.Type == cdx.ComponentTypeLibrary {
			continue
		}
		if component.Licenses == nil || len(*component.Licenses) != 1 {
			return fmt.Errorf("product license is missing")
		}
		license := (*component.Licenses)[0].License
		if license == nil || license.Text == nil || license.Text.Content != string(data) {
			return fmt.Errorf("product license text does not match archive LICENSE")
		}
		if hex.EncodeToString(digest[:]) == apacheLicenseSHA && license.ID != "Apache-2.0" {
			return fmt.Errorf("known Apache-2.0 archive license identity is missing")
		}
	}
	return nil
}

func indexModernComponents(c *sbom.Catalog) (map[string]cdx.Component, map[string]cdx.Component, error) {
	bins, mods := map[string]cdx.Component{}, map[string]cdx.Component{}
	coordinates := map[ModuleDep]bool{}
	for ref, component := range c.Components {
		if ref == c.Root {
			continue
		}
		properties, err := sbom.Properties(component)
		if err != nil {
			return nil, nil, err
		}
		if component.Type == cdx.ComponentTypeLibrary {
			coordinate := ModuleDep{Path: properties["microfat:module:path"], Version: properties["microfat:module:version"],
				Sum: properties["microfat:module:sum"], ReplacePath: properties["microfat:module:replace_path"],
				ReplaceVer: properties["microfat:module:replace_version"], ReplaceSum: properties["microfat:module:replace_sum"]}
			if coordinates[coordinate] {
				return nil, nil, fmt.Errorf("duplicate module coordinate %s", coordinate.Path)
			}
			coordinates[coordinate] = true
			mods[ref] = component
			continue
		}
		id := properties["microfat:binary_id"]
		if id == "" || component.Type != cdx.ComponentTypeApplication {
			return nil, nil, fmt.Errorf("unattributed component %s", ref)
		}
		if _, exists := bins[id]; exists {
			return nil, nil, fmt.Errorf("duplicate binary identity %s", id)
		}
		bins[id] = component
	}
	return bins, mods, nil
}

func checkModernBinary(component cdx.Component, bin *BinaryInventory, facts *ArchiveFacts, version string) error {
	name, digest, err := expectedModernBinary(bin, facts)
	if err != nil {
		return err
	}
	if component.Name != name || component.Version != version {
		return fmt.Errorf("binary %s has incorrect name or version", bin.Identifier)
	}
	if component.PackageURL != "pkg:generic/"+url.PathEscape(name)+"@"+url.PathEscape(version) {
		return fmt.Errorf("binary %s has incorrect package URL", bin.Identifier)
	}
	if err := checkModernHash(component, digest); err != nil {
		return err
	}
	expected := map[string]string{"microfat:component_type": "application", "microfat:target_arch": facts.TargetArch,
		"microfat:binary_id": bin.Identifier, "microfat:variant_tier": bin.VariantTier,
		"microfat:go_version": bin.GoVersion, "microfat:main_path": bin.MainPath,
		"microfat:main_module": bin.MainModule, "microfat:main_version": bin.MainVersion,
		"microfat:stub_profile": expectedModernProfile(bin.BinaryName, bin.VariantTier)}
	if err := checkModernProperties(component, expected); err != nil {
		return err
	}
	properties, err := sbom.Properties(component)
	if err != nil {
		return err
	}
	var settings map[string]string
	if err := json.Unmarshal([]byte(properties["microfat:build_settings"]), &settings); err != nil {
		return err
	}
	if !reflect.DeepEqual(settings, bin.Settings) {
		return fmt.Errorf("binary %s build settings differ", bin.Identifier)
	}
	return checkModernSource(component, bin.Settings["vcs.revision"])
}

func expectedModernBinary(bin *BinaryInventory, facts *ArchiveFacts) (string, string, error) {
	if bin.VariantTier != "" {
		variant := facts.EmbeddedVariants[bin.VariantTier]
		if variant == nil {
			return "", "", fmt.Errorf("missing independent variant %s", bin.VariantTier)
		}
		return "microfat-variant-" + bin.VariantTier, variant.SHA256, nil
	}
	executable := facts.Executables[bin.BinaryName]
	if executable == nil {
		return "", "", fmt.Errorf("missing independent executable %s", bin.BinaryName)
	}
	return bin.BinaryName, executable.SHA256, nil
}

func checkModernDependencies(c *sbom.Catalog, component cdx.Component, bin *BinaryInventory,
	modules map[string]cdx.Component, used map[string]bool) error {
	refs, exists := c.Depends[component.BOMRef]
	if !exists || len(refs) != len(bin.Dependencies) {
		return fmt.Errorf("binary %s dependency count mismatch", bin.Identifier)
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		module, ok := modules[ref]
		if !ok {
			return fmt.Errorf("binary %s links a non-module component", bin.Identifier)
		}
		properties, err := sbom.Properties(module)
		if err != nil {
			return err
		}
		path := properties["microfat:module:path"]
		dep, ok := bin.Dependencies[path]
		if !ok || seen[path] {
			return fmt.Errorf("binary %s has an unexpected or duplicate dependency %s", bin.Identifier, path)
		}
		seen[path], used[ref] = true, true
		if err := checkModernModule(module, dep); err != nil {
			return err
		}
	}
	return nil
}

func checkModernModule(component cdx.Component, dep ModuleDep) error {
	path, version := dep.Path, dep.Version
	if dep.ReplacePath != "" {
		path, version = dep.ReplacePath, dep.ReplaceVer
	}
	if component.Name != path || component.Version != version {
		return fmt.Errorf("linked module %s version or replacement differs", dep.Path)
	}
	parts := strings.Split(path, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	purl := ""
	if version != "" && !strings.HasPrefix(path, ".") && !filepath.IsAbs(path) {
		purl = "pkg:golang/" + strings.Join(parts, "/") + "@" + url.PathEscape(version)
	}
	if component.PackageURL != purl {
		return fmt.Errorf("module %s package URL differs", dep.Path)
	}
	return checkModernProperties(component, map[string]string{
		"microfat:component_type": "library", "microfat:module:path": dep.Path, "microfat:module:version": dep.Version,
		"microfat:module:sum": dep.Sum, "microfat:module:replace_path": dep.ReplacePath,
		"microfat:module:replace_version": dep.ReplaceVer, "microfat:module:replace_sum": dep.ReplaceSum,
	})
}

func checkModernContainment(c *sbom.Catalog, bins map[string]cdx.Component, facts *ArchiveFacts) error {
	expected := map[string][]string{c.Root: {}}
	for name := range facts.Executables {
		expected[c.Root] = append(expected[c.Root], bins[name].BOMRef)
	}
	cli := bins[ReleaseProjectName].BOMRef
	expected[cli] = []string{}
	for tier := range facts.EmbeddedVariants {
		expected[cli] = append(expected[cli], bins["variant:"+tier].BOMRef)
	}
	if len(expected) != len(c.Contains) {
		return fmt.Errorf("archive containment graph differs")
	}
	for from, refs := range expected {
		slices.Sort(refs)
		actual := slices.Clone(c.Contains[from])
		slices.Sort(actual)
		if !slices.Equal(refs, actual) {
			return fmt.Errorf("incorrect contained components for %s", from)
		}
	}
	if len(c.Depends) != len(bins) {
		return fmt.Errorf("unexpected dependency graph source")
	}
	return nil
}

func checkModernHash(component cdx.Component, expected string) error {
	if component.Hashes == nil || len(*component.Hashes) != 1 {
		return fmt.Errorf("component %s requires one exact SHA-256", component.BOMRef)
	}
	hash := (*component.Hashes)[0]
	if hash.Algorithm != cdx.HashAlgoSHA256 || hash.Value != expected {
		return fmt.Errorf("component %s SHA-256 mismatch", component.BOMRef)
	}
	return nil
}

func checkModernProperties(component cdx.Component, expected map[string]string) error {
	actual, err := sbom.Properties(component)
	if err != nil {
		return err
	}
	for key, value := range expected {
		got, ok := actual[key]
		if !ok || got != value {
			return fmt.Errorf("component %s property %s mismatch", component.BOMRef, key)
		}
	}
	return nil
}

func expectedModernProfile(name, tier string) string {
	if tier != "" {
		return ""
	}
	switch name {
	case ReleaseFullStub:
		return "full"
	case ReleaseMinStub:
		return "minimal"
	default:
		return ""
	}
}

func checkModernSource(component cdx.Component, revision string) error {
	if revision == "" {
		return nil
	}
	if component.ExternalReferences != nil {
		for _, ref := range *component.ExternalReferences {
			if ref.Type == cdx.ERTypeVCS && ref.URL == "https://github.com/EpicBlackWolfZ/microfat/tree/"+revision {
				return nil
			}
		}
	}
	return fmt.Errorf("component %s source revision is missing", component.BOMRef)
}

func checkModernMetadata(meta *cdx.Metadata) error {
	if meta == nil || meta.Timestamp == "" || meta.Tools == nil || meta.Tools.Components == nil {
		return fmt.Errorf("modern SBOM generator metadata is missing")
	}
	tools := map[string]string{}
	for _, tool := range *meta.Tools.Components {
		tools[tool.Name] = tool.Version
	}
	if tools["microfat-release-sbom"] == "" || tools["cdxgen cdx-convert"] != "13.1.0" {
		return fmt.Errorf("modern SBOM generator or pinned converter identity is missing")
	}
	return nil
}
