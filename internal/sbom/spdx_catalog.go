package sbom

import (
	"encoding/json"
	"fmt"
	"strings"

	cdx "github.com/CycloneDX/cyclonedx-go"
)

// Catalog reconstructs the conversion's canonical component inventory from
// native SPDX package/hash fields and preserved CycloneDX metadata. It is used
// for independent archive verification, without requiring a companion SBOM.
func (g *SPDXGraph) Catalog() (*Catalog, error) {
	catalog := &Catalog{BOM: cdx.NewBOM(), Components: map[string]cdx.Component{},
		Contains: map[string][]string{}, Depends: map[string][]string{}}
	ids := map[string]string{}
	for ref, node := range g.Components {
		component, err := componentFromSPDX(ref, node)
		if err != nil {
			return nil, err
		}
		catalog.Components[ref] = component
		ids[text(node["spdxId"])] = ref
		if component.Type == cdx.ComponentTypeApplication {
			catalog.Depends[ref] = []string{}
		}
	}
	catalog.Root = ids[text(array(g.Document["rootElement"])[0])]
	if catalog.Root == "" {
		return nil, fmt.Errorf("SPDX archive root is not a component")
	}
	if err := g.catalogEdges(catalog.Contains, ids, "contains"); err != nil {
		return nil, err
	}
	if err := g.catalogEdges(catalog.Depends, ids, "dependsOn"); err != nil {
		return nil, err
	}
	metadata, err := g.catalogMetadata()
	if err != nil {
		return nil, err
	}
	root := catalog.Components[catalog.Root]
	metadata.Component = &root
	catalog.BOM.Metadata = metadata
	return catalog, nil
}

func componentFromSPDX(ref string, node Object) (cdx.Component, error) {
	ext, err := SPDXProperties(node)
	if err != nil {
		return cdx.Component{}, err
	}
	props := propertiesFromExtension(ext, "properties.")
	component := cdx.Component{BOMRef: ref, Name: text(node["name"]), Version: text(node["software_packageVersion"]),
		PackageURL: text(node["software_packageUrl"]), Description: text(node["description"]), Properties: &props,
		Type: cdx.ComponentType(ext["properties.microfat:component_type"])}
	if component.Type == cdx.ComponentTypeFile {
		component.Version = ext["properties.microfat:release_version"]
	}
	expected := "software_Package"
	if component.Type == cdx.ComponentTypeFile {
		expected = "software_File"
	}
	if text(node["type"]) != expected {
		return cdx.Component{}, fmt.Errorf("SPDX type contradicts preserved component metadata")
	}
	if encoded := ext["licenses"]; encoded != "" {
		var licenses cdx.Licenses
		if err := json.Unmarshal([]byte(encoded), &licenses); err != nil {
			return cdx.Component{}, err
		}
		component.Licenses = &licenses
	}
	if encoded := ext["externalReferences"]; encoded != "" {
		var references []cdx.ExternalReference
		if err := json.Unmarshal([]byte(encoded), &references); err != nil {
			return cdx.Component{}, err
		}
		component.ExternalReferences = &references
		if err := checkNativeReferences(node, references); err != nil {
			return cdx.Component{}, err
		}
	}
	if raw := array(node["verifiedUsing"]); len(raw) != 0 {
		hashes := make([]cdx.Hash, 0, len(raw))
		for _, entry := range raw {
			hash := object(entry)
			if text(hash["algorithm"]) != "sha256" {
				return cdx.Component{}, fmt.Errorf("release SPDX requires SHA-256 checksums")
			}
			hashes = append(hashes, cdx.Hash{Algorithm: cdx.HashAlgoSHA256, Value: text(hash["hashValue"])})
		}
		component.Hashes = &hashes
	}
	return component, nil
}

func checkNativeReferences(node Object, references []cdx.ExternalReference) error {
	for _, ref := range references {
		if ref.Type != cdx.ERTypeVCS {
			continue
		}
		found := false
		for _, raw := range array(node["externalRef"]) {
			entry := object(raw)
			if text(entry["externalRefType"]) != "vcs" {
				continue
			}
			for _, locator := range array(entry["locator"]) {
				if text(locator) == ref.URL {
					found = true
				}
			}
		}
		if !found {
			return fmt.Errorf("native SPDX source reference disagrees with preserved metadata")
		}
	}
	return nil
}

func (g *SPDXGraph) catalogEdges(target map[string][]string, ids map[string]string, kind string) error {
	for from, refs := range g.Edges[kind] {
		parent := ids[from]
		if parent == "" {
			return fmt.Errorf("SPDX %s source is not a component", kind)
		}
		for _, id := range refs {
			child := ids[id]
			if child == "" {
				return fmt.Errorf("SPDX %s target is not a component", kind)
			}
			target[parent] = append(target[parent], child)
		}
	}
	return nil
}

func (g *SPDXGraph) catalogMetadata() (*cdx.Metadata, error) {
	ext, err := SPDXProperties(g.Document)
	if err != nil {
		return nil, err
	}
	var tools cdx.ToolsChoice
	if err := json.Unmarshal([]byte(ext["metadataTools"]), &tools); err != nil {
		return nil, fmt.Errorf("SPDX generator metadata: %w", err)
	}
	props := propertiesFromExtension(ext, "metadataProperties.")
	return &cdx.Metadata{Timestamp: text(g.Creation["created"]), Tools: &tools, Properties: &props}, nil
}

func propertiesFromExtension(ext map[string]string, prefix string) []cdx.Property {
	props := []cdx.Property{}
	for _, key := range sortedMapKeys(ext) {
		if name, ok := strings.CutPrefix(key, prefix); ok {
			props = append(props, cdx.Property{Name: name, Value: ext[key]})
		}
	}
	return props
}

// ValidateAttribution checks that declared license and creator relationships
// agree with the metadata retained during conversion.
func (g *SPDXGraph) ValidateAttribution(c *Catalog) error {
	for kind := range g.Edges {
		if kind != "contains" && kind != "dependsOn" && kind != "hasDeclaredLicense" {
			return fmt.Errorf("unexpected SPDX relationship kind %s", kind)
		}
	}
	for ref, component := range c.Components {
		node := g.Components[ref]
		ids := g.Edges["hasDeclaredLicense"][text(node["spdxId"])]
		if err := g.checkLicenses(component.Licenses, ids); err != nil {
			return fmt.Errorf("component %s: %w", ref, err)
		}
	}
	return g.checkTools(c.BOM.Metadata.Tools)
}

func (g *SPDXGraph) checkLicenses(licenses *cdx.Licenses, ids []string) error {
	if licenses == nil {
		if len(ids) != 0 {
			return fmt.Errorf("unexpected declared license")
		}
		return nil
	}
	if len(*licenses) != len(ids) {
		return fmt.Errorf("declared license relationship count differs")
	}
	for index, choice := range *licenses {
		node := g.Nodes[ids[index]]
		expression := choice.Expression
		if choice.License != nil {
			expression = choice.License.ID
		}
		if expression != "" {
			if text(node["type"]) != "simplelicensing_LicenseExpression" || text(node["simplelicensing_licenseExpression"]) != expression {
				return fmt.Errorf("declared license expression differs")
			}
			continue
		}
		if choice.License == nil || choice.License.Text == nil || text(node["type"]) != "simplelicensing_SimpleLicensingText" ||
			text(node["simplelicensing_licenseText"]) != choice.License.Text.Content {
			return fmt.Errorf("declared license text differs")
		}
	}
	return nil
}

func (g *SPDXGraph) checkTools(tools *cdx.ToolsChoice) error {
	if tools == nil || tools.Components == nil {
		return fmt.Errorf("SPDX creator tools are missing")
	}
	actual := map[string]bool{}
	for _, id := range array(g.Creation["createdUsing"]) {
		name := text(g.Nodes[text(id)]["name"])
		if actual[name] {
			return fmt.Errorf("duplicate SPDX creator tool")
		}
		actual[name] = true
	}
	if len(actual) != len(*tools.Components) {
		return fmt.Errorf("SPDX creator tool count differs")
	}
	for _, tool := range *tools.Components {
		if !actual[tool.Name+" "+tool.Version] {
			return fmt.Errorf("SPDX creator tool differs")
		}
	}
	return nil
}
