// Package releasesbom generates payload-aware release SBOMs from verified archive bytes.
package releasesbom

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	cdx "github.com/CycloneDX/cyclonedx-go"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
)

const (
	archiveRef        = "archive-root"
	projectURL        = "https://github.com/EpicBlackWolfZ/microfat"
	generatorName     = "microfat-release-sbom"
	converterVersion  = "13.1.0"
	projectLicenseSHA = "ae380bc0d675e1d3bdf12ef3fe0c958f07365cf7bf20656ae95fce881e293f53"
)

// Metadata identifies the generator build, separately from the archived product.
type Metadata struct {
	Version string
	Commit  string
	Created time.Time
}

type cdxBuilder struct {
	facts        *releasecheck.ArchiveFacts
	inventory    *releasecheck.ArchiveInventory
	version      string
	license      cdx.Licenses
	modules      map[string]cdx.Component
	dependencies []cdx.Dependency
}

// CycloneDX constructs a complete CycloneDX 1.7 document. Linked modules are
// keyed by full coordinates, including replacements, rather than by module path.
func CycloneDX(facts *releasecheck.ArchiveFacts, inv *releasecheck.ArchiveInventory, meta Metadata) ([]byte, error) {
	if facts == nil || inv == nil {
		return nil, fmt.Errorf("missing archive facts or build-info inventory")
	}
	identity, err := releasecheck.ParseReleaseArchiveName(facts.ArchiveName)
	if err != nil {
		return nil, err
	}
	license, err := archiveLicense(facts.ExtractedDir)
	if err != nil {
		return nil, err
	}
	b := cdxBuilder{facts: facts, inventory: inv, version: identity.Version, license: license,
		modules: map[string]cdx.Component{}}
	root, err := b.root()
	if err != nil {
		return nil, err
	}
	bom := cdx.NewBOM()
	bom.SerialNumber = contentUUID(facts.ArchiveSHA256, meta)
	bom.Metadata = metadata(meta, root)
	components := sortedComponents(b.modules)
	bom.Components = &components
	bom.Dependencies = &b.dependencies
	return json.MarshalIndent(bom, "", "  ")
}

func archiveLicense(dir string) (cdx.Licenses, error) {
	// #nosec G304 -- dir is the verified private archive extraction directory.
	data, err := os.ReadFile(filepath.Join(dir, "LICENSE"))
	if err != nil {
		return nil, fmt.Errorf("reading archive license: %w", err)
	}
	license := cdx.License{Name: "License supplied in the release archive LICENSE",
		Acknowledgement: cdx.LicenseAcknowledgementDeclared,
		Text:            &cdx.AttachedText{ContentType: "text/plain", Content: string(data)}}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) == projectLicenseSHA {
		license.Name = ""
		license.ID = "Apache-2.0"
	}
	return cdx.Licenses{{License: &license}}, nil
}

func metadata(meta Metadata, root cdx.Component) *cdx.Metadata {
	tools := []cdx.Component{
		{Type: cdx.ComponentTypeApplication, Name: generatorName, Version: meta.Version,
			ExternalReferences: &[]cdx.ExternalReference{{Type: cdx.ERTypeVCS, URL: projectURL + "/tree/" + meta.Commit}}},
		{Type: cdx.ComponentTypeApplication, Name: "cdxgen cdx-convert", Version: converterVersion,
			ExternalReferences: &[]cdx.ExternalReference{{Type: cdx.ERTypeVCS, URL: "https://github.com/cdxgen/cdxgen/tree/v" + converterVersion}}},
	}
	return &cdx.Metadata{Timestamp: meta.Created.UTC().Format(time.RFC3339), Component: &root,
		Tools: &cdx.ToolsChoice{Components: &tools},
		Properties: properties("microfat:generator:commit", meta.Commit,
			"microfat:inventory:source", "verified archive bytes and Go build information"),
	}
}

func (b *cdxBuilder) root() (cdx.Component, error) {
	root := product(archiveRef, cdx.ComponentTypeFile, b.facts.ArchiveName, b.version, b.facts.ArchiveSHA256)
	root.Properties = properties("microfat:target_arch", b.facts.TargetArch,
		"microfat:release_version", b.version, "microfat:component_type", "file")
	root.Licenses = &b.license
	children := []cdx.Component{}
	for _, name := range sortedKeys(b.facts.Executables) {
		child, err := b.executable(name)
		if err != nil {
			return cdx.Component{}, err
		}
		children = append(children, child)
	}
	root.Components = &children
	return root, nil
}

func (b *cdxBuilder) executable(name string) (cdx.Component, error) {
	facts := b.facts.Executables[name]
	if facts == nil {
		return cdx.Component{}, fmt.Errorf("missing executable facts for %s", name)
	}
	component, err := b.binary(name, name, facts.SHA256)
	if err != nil {
		return cdx.Component{}, err
	}
	if name != releasecheck.ReleaseProjectName {
		return component, nil
	}
	variants := []cdx.Component{}
	for _, tier := range sortedKeys(b.facts.EmbeddedVariants) {
		variant := b.facts.EmbeddedVariants[tier]
		if variant == nil {
			return cdx.Component{}, fmt.Errorf("missing variant facts for %s", tier)
		}
		child, err := b.binary("variant:"+tier, "microfat-variant-"+tier, variant.SHA256)
		if err != nil {
			return cdx.Component{}, err
		}
		variants = append(variants, child)
	}
	component.Components = &variants
	return component, nil
}

func (b *cdxBuilder) binary(id, name, digest string) (cdx.Component, error) {
	inv := b.inventory.Binaries[id]
	if inv == nil {
		return cdx.Component{}, fmt.Errorf("missing binary inventory for %s", id)
	}
	component := product("binary-"+id, cdx.ComponentTypeApplication, name, b.version, digest)
	component.PackageURL = "pkg:generic/" + url.PathEscape(name) + "@" + url.PathEscape(b.version)
	component.Licenses = &b.license
	settings, err := json.Marshal(inv.Settings)
	if err != nil {
		return cdx.Component{}, err
	}
	component.Properties = properties("microfat:target_arch", b.facts.TargetArch,
		"microfat:component_type", "application",
		"microfat:binary_id", id, "microfat:variant_tier", inv.VariantTier,
		"microfat:go_version", inv.GoVersion, "microfat:main_path", inv.MainPath,
		"microfat:main_module", inv.MainModule, "microfat:main_version", inv.MainVersion,
		"microfat:build_settings", string(settings), "microfat:stub_profile", stubProfile(name))
	component.ExternalReferences = sourceReferences(inv.Settings)
	refs := []string{}
	for _, path := range sortedKeys(inv.Dependencies) {
		dep := moduleComponent(inv.Dependencies[path])
		b.modules[dep.BOMRef] = dep
		refs = append(refs, dep.BOMRef)
	}
	slices.Sort(refs)
	b.dependencies = append(b.dependencies, cdx.Dependency{Ref: component.BOMRef, Dependencies: &refs})
	return component, nil
}

func moduleComponent(dep releasecheck.ModuleDep) cdx.Component {
	coordinate, _ := json.Marshal(dep)
	digest := sha256.Sum256(coordinate)
	path, version := dep.Path, dep.Version
	if dep.ReplacePath != "" {
		path, version = dep.ReplacePath, dep.ReplaceVer
	}
	component := cdx.Component{Type: cdx.ComponentTypeLibrary, BOMRef: "module-" + hex.EncodeToString(digest[:]),
		Name: path, Version: version,
		Properties: properties("microfat:module:path", dep.Path, "microfat:module:version", dep.Version,
			"microfat:component_type", "library",
			"microfat:module:sum", dep.Sum, "microfat:module:replace_sum", dep.ReplaceSum,
			"microfat:module:replace_path", dep.ReplacePath, "microfat:module:replace_version", dep.ReplaceVer,
			"microfat:license_status", "not available in Go build information")}
	// Local replacements have no registry identity; do not invent a package URL.
	if version != "" && !strings.HasPrefix(path, ".") && !filepath.IsAbs(path) {
		component.PackageURL = "pkg:golang/" + escapeModulePath(path) + "@" + url.PathEscape(version)
	}
	return component
}

func escapeModulePath(path string) string {
	parts := strings.Split(path, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}

func sourceReferences(settings map[string]string) *[]cdx.ExternalReference {
	revision := settings["vcs.revision"]
	if revision == "" {
		return nil
	}
	return &[]cdx.ExternalReference{{Type: cdx.ERTypeVCS, URL: projectURL + "/tree/" + revision}}
}

func stubProfile(name string) string {
	switch name {
	case releasecheck.ReleaseFullStub:
		return "full"
	case releasecheck.ReleaseMinStub:
		return "minimal"
	default:
		return ""
	}
}

func product(ref string, kind cdx.ComponentType, name, version, digest string) cdx.Component {
	return cdx.Component{BOMRef: ref, Type: kind, Name: name, Version: version,
		Hashes: &[]cdx.Hash{{Algorithm: cdx.HashAlgoSHA256, Value: digest}}}
}

func properties(values ...string) *[]cdx.Property {
	const pairSize = 2
	props := make([]cdx.Property, 0, len(values)/pairSize)
	for index := 0; index+1 < len(values); index += pairSize {
		props = append(props, cdx.Property{Name: values[index], Value: values[index+1]})
	}
	return &props
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func sortedComponents(values map[string]cdx.Component) []cdx.Component {
	components := make([]cdx.Component, 0, len(values))
	for _, key := range sortedKeys(values) {
		components = append(components, values[key])
	}
	return components
}

func contentUUID(archiveHash string, meta Metadata) string {
	// A UUIDv8 identifies this archive, generator and generation timestamp.
	const versionIndex, variantIndex, versionMask, variantMask = 6, 8, 0x80, 0x80
	const versionRetainMask, variantRetainMask = 0x0f, 0x3f
	digest := sha256.Sum256([]byte(archiveHash + "\n" + meta.Version + "\n" + meta.Commit + "\n" + meta.Created.Format(time.RFC3339Nano)))
	digest[versionIndex] = digest[versionIndex]&versionRetainMask | versionMask
	digest[variantIndex] = digest[variantIndex]&variantRetainMask | variantMask
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", digest[0:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
}
