package sbom

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	cdx "github.com/CycloneDX/cyclonedx-go"
)

// CompleteSPDX accepts only a lossless cdx-convert result, then supplies explicit
// containment, creator tools and declared-license relationships missing from
// that converter. It never repairs missing inventory or dependency components.
func CompleteSPDX(catalog *Catalog, converted []byte) ([]byte, error) {
	graph, err := ReadSPDX(converted)
	if err != nil {
		return nil, err
	}
	if err := compareConversion(catalog, graph); err != nil {
		return nil, err
	}
	if len(graph.Edges["contains"]) != 0 {
		return nil, fmt.Errorf("converter unexpectedly supplied containment")
	}
	for _, ref := range sortedMapKeys(catalog.Contains) {
		targets := []string{}
		for _, child := range catalog.Contains[ref] {
			targets = append(targets, text(graph.Components[child]["spdxId"]))
		}
		graph.appendRelationship("contains", text(graph.Components[ref]["spdxId"]), targets)
	}
	graph.addTools(catalog.BOM.Metadata)
	if err := graph.addLicenses(catalog); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(graph.Raw, "", "  ")
	if err != nil {
		return nil, err
	}
	completed, err := ReadSPDX(data)
	if err != nil {
		return nil, err
	}
	if err := completed.ValidateAttribution(catalog); err != nil {
		return nil, err
	}
	return data, nil
}

func compareConversion(c *Catalog, g *SPDXGraph) error {
	if len(c.Components) != len(g.Components) {
		return fmt.Errorf("SPDX conversion changed the component count")
	}
	if text(array(g.Document["rootElement"])[0]) != text(g.Components[c.Root]["spdxId"]) {
		return fmt.Errorf("SPDX conversion changed the archive root")
	}
	for ref, component := range c.Components {
		node := g.Components[ref]
		if node == nil {
			return fmt.Errorf("SPDX conversion lost component %s", ref)
		}
		if err := compareComponent(component, node); err != nil {
			return fmt.Errorf("component %s: %w", ref, err)
		}
	}
	if err := compareEdges(c.Depends, g.Edges["dependsOn"], g.Components); err != nil {
		return err
	}
	props, err := SPDXProperties(g.Document)
	if err != nil {
		return err
	}
	for name, value := range map[string]any{"metadataTools": c.BOM.Metadata.Tools} {
		if value != nil {
			if err := compareExtension(props[name], value); err != nil {
				return fmt.Errorf("SPDX lost %s: %w", name, err)
			}
		}
	}
	if err := comparePropertyEntries(c.BOM.Metadata.Properties, props, "metadataProperties."); err != nil {
		return fmt.Errorf("SPDX document metadata: %w", err)
	}
	if text(g.Creation["created"]) != c.BOM.Metadata.Timestamp {
		return fmt.Errorf("SPDX conversion changed the creation timestamp")
	}
	return nil
}

func compareComponent(component cdx.Component, node Object) error {
	if text(node["name"]) != component.Name || text(node["software_packageUrl"]) != component.PackageURL {
		return fmt.Errorf("SPDX name or package URL differs")
	}
	expectedType := spdxPackage
	if component.Type == cdx.ComponentTypeFile {
		expectedType = spdxFile
	}
	if text(node["type"]) != expectedType {
		return fmt.Errorf("SPDX component type differs")
	}
	if component.Type != cdx.ComponentTypeFile && text(node["software_packageVersion"]) != component.Version {
		return fmt.Errorf("SPDX package version differs")
	}
	if text(node["description"]) != component.Description {
		return fmt.Errorf("SPDX description differs")
	}
	props, err := SPDXProperties(node)
	if err != nil {
		return err
	}
	if err := compareComponentProperties(component, props); err != nil {
		return err
	}
	if err := compareHashes(component, node); err != nil {
		return err
	}
	return compareAdditionalFields(component, props)
}

func compareComponentProperties(component cdx.Component, props map[string]string) error {
	return comparePropertyEntries(component.Properties, props, "properties.")
}

func comparePropertyEntries(entries *[]cdx.Property, props map[string]string, prefix string) error {
	expected, err := Properties(cdx.Component{Properties: entries})
	if err != nil {
		return err
	}
	actual := map[string]string{}
	for key, value := range props {
		if name, ok := strings.CutPrefix(key, prefix); ok {
			actual[name] = value
		}
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("SPDX conversion changed component properties")
	}
	return nil
}

func compareHashes(component cdx.Component, node Object) error {
	actual := map[string]string{}
	for _, raw := range array(node["verifiedUsing"]) {
		hash := object(raw)
		algorithm := text(hash["algorithm"])
		if _, ok := actual[algorithm]; ok {
			return fmt.Errorf("duplicate SPDX checksum algorithm")
		}
		actual[algorithm] = text(hash["hashValue"])
	}
	expected := map[string]string{}
	if component.Hashes != nil {
		for _, hash := range *component.Hashes {
			algorithm := strings.ToLower(strings.ReplaceAll(string(hash.Algorithm), "-", ""))
			expected[algorithm] = hash.Value
		}
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("SPDX conversion changed checksums")
	}
	return nil
}

func compareAdditionalFields(component cdx.Component, props map[string]string) error {
	data, err := json.Marshal(component)
	if err != nil {
		return err
	}
	var fields Object
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key, value := range fields {
		switch key {
		case "bom-ref", "type", "name", "version", "purl", "description", "hashes", "properties", "components":
			continue // Compared natively above, or supplied by explicit containment.
		case "group", "scope", "author", "publisher", "copyright", "cpe":
			if props[key] != text(value) {
				return fmt.Errorf("SPDX conversion lost %s", key)
			}
		default:
			if err := compareExtension(props[key], value); err != nil {
				return fmt.Errorf("SPDX conversion lost %s: %w", key, err)
			}
		}
	}
	return nil
}

func compareExtension(encoded string, expected any) error {
	var actual any
	if err := json.Unmarshal([]byte(encoded), &actual); err != nil {
		return err
	}
	data, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, normalized) {
		return fmt.Errorf("extension value differs")
	}
	return nil
}

func compareEdges(expected map[string][]string, actual map[string][]string, components map[string]Object) error {
	mapped := map[string][]string{}
	for from, refs := range expected {
		if len(refs) == 0 {
			continue
		}
		targets := make([]string, 0, len(refs))
		for _, ref := range refs {
			targets = append(targets, text(components[ref]["spdxId"]))
		}
		slices.Sort(targets)
		mapped[text(components[from]["spdxId"])] = targets
	}
	if len(mapped) != len(actual) {
		return fmt.Errorf("SPDX dependency relationship count differs")
	}
	for from, refs := range mapped {
		if !slices.Equal(refs, actual[from]) {
			return fmt.Errorf("SPDX dependency set differs for %s", from)
		}
	}
	return nil
}

func (g *SPDXGraph) appendNode(node Object) {
	g.Raw["@graph"] = append(array(g.Raw["@graph"]), node)
	g.Document["element"] = append(array(g.Document["element"]), node["spdxId"])
}

func (g *SPDXGraph) nextID(kind string) string {
	return fmt.Sprintf("%s-microfat-%s-%d", text(g.Document["spdxId"]), kind, len(array(g.Raw["@graph"])))
}

func (g *SPDXGraph) appendRelationship(kind, from string, targets []string) {
	to := make([]any, len(targets))
	for index, target := range targets {
		to[index] = target
	}
	g.appendNode(Object{"type": "Relationship", "spdxId": g.nextID(kind), "creationInfo": g.Creation["@id"],
		"from": from, "to": to, "relationshipType": kind})
}

func (g *SPDXGraph) addTools(metadata *cdx.Metadata) {
	tools := []any{}
	if metadata.Tools != nil && metadata.Tools.Components != nil {
		for _, tool := range *metadata.Tools.Components {
			id := g.nextID("tool")
			g.appendNode(Object{"type": "Tool", "spdxId": id, "creationInfo": g.Creation["@id"],
				"name": tool.Name + " " + tool.Version})
			tools = append(tools, id)
		}
	}
	g.Creation["createdUsing"] = tools
}

func (g *SPDXGraph) addLicenses(catalog *Catalog) error {
	for _, ref := range sortedMapKeys(catalog.Components) {
		component := catalog.Components[ref]
		if component.Licenses == nil {
			continue
		}
		for _, choice := range *component.Licenses {
			node, err := g.licenseNode(choice)
			if err != nil {
				return err
			}
			g.appendNode(node)
			g.appendRelationship("hasDeclaredLicense", text(g.Components[ref]["spdxId"]), []string{text(node["spdxId"])})
		}
	}
	profiles := array(g.Document["profileConformance"])
	g.Document["profileConformance"] = append(profiles, "simpleLicensing")
	return nil
}

func (g *SPDXGraph) licenseNode(choice cdx.LicenseChoice) (Object, error) {
	id := g.nextID("license")
	node := Object{"spdxId": id, "creationInfo": g.Creation["@id"]}
	expression := choice.Expression
	if choice.License != nil {
		expression = choice.License.ID
	}
	if expression != "" {
		node["type"] = "simplelicensing_LicenseExpression"
		node["simplelicensing_licenseExpression"] = expression
		return node, nil
	}
	if choice.License == nil || choice.License.Text == nil || choice.License.Text.Content == "" {
		return nil, fmt.Errorf("declared license has neither an expression nor license text")
	}
	node["type"] = "simplelicensing_SimpleLicensingText"
	node["name"] = choice.License.Name
	node["simplelicensing_licenseText"] = choice.License.Text.Content
	return node, nil
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
