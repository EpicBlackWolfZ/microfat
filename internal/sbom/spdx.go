package sbom

import (
	"encoding/json"
	"fmt"
	"slices"
)

// Object retains all SPDX JSON-LD fields, including conversion extensions.
type Object = map[string]any

// SPDXGraph indexes the SPDX 3.0.1 release document and its local references.
// Schema validation alone does not establish referential integrity or inventory.
type SPDXGraph struct {
	Document   Object
	Creation   Object
	Nodes      map[string]Object
	Components map[string]Object
	Edges      map[string]map[string][]string
	Raw        Object
}

// ReadSPDX validates the official schema and the complete local reference graph.
func ReadSPDX(data []byte) (*SPDXGraph, error) {
	if err := ValidateSchema(data, true); err != nil {
		return nil, err
	}
	var raw Object
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	graph := &SPDXGraph{Nodes: map[string]Object{}, Components: map[string]Object{},
		Edges: map[string]map[string][]string{}, Raw: raw}
	for _, value := range array(raw["@graph"]) {
		if err := graph.addNode(object(value)); err != nil {
			return nil, err
		}
	}
	if graph.Document == nil || graph.Creation == nil {
		return nil, fmt.Errorf("SPDX requires one document and creation record")
	}
	if text(graph.Creation["specVersion"]) != "3.0.1" {
		return nil, fmt.Errorf("SPDX creation version must be 3.0.1")
	}
	if err := graph.checkReferences(); err != nil {
		return nil, err
	}
	return graph, nil
}

func (g *SPDXGraph) addNode(node Object) error {
	id := text(node["spdxId"])
	if text(node["type"]) == "CreationInfo" {
		id = text(node["@id"])
	}
	if id == "" {
		return fmt.Errorf("SPDX graph node lacks an explicit identifier")
	}
	if _, ok := g.Nodes[id]; ok {
		return fmt.Errorf("duplicate SPDX identifier %s", id)
	}
	g.Nodes[id] = node
	switch text(node["type"]) {
	case "SpdxDocument":
		if g.Document != nil {
			return fmt.Errorf("multiple SPDX documents")
		}
		g.Document = node
	case "CreationInfo":
		if g.Creation != nil {
			return fmt.Errorf("multiple SPDX creation records")
		}
		g.Creation = node
	case "software_File", "software_Package":
		return g.addComponent(node)
	}
	return nil
}

func (g *SPDXGraph) addComponent(node Object) error {
	props, err := SPDXProperties(node)
	if err != nil {
		return err
	}
	ref := props["bomRef"]
	if ref == "" {
		return fmt.Errorf("SPDX component %s lacks its CycloneDX identity", text(node["spdxId"]))
	}
	if _, ok := g.Components[ref]; ok {
		return fmt.Errorf("duplicate SPDX component identity %s", ref)
	}
	g.Components[ref] = node
	return nil
}

func (g *SPDXGraph) checkReferences() error {
	creationID := text(g.Creation["@id"])
	for id, node := range g.Nodes {
		if id == creationID {
			continue
		}
		if text(node["creationInfo"]) != creationID {
			return fmt.Errorf("SPDX node %s has an invalid creation reference", id)
		}
		if text(node["type"]) == "Relationship" {
			if err := g.addRelationship(node); err != nil {
				return err
			}
		}
	}
	if err := g.checkElements(); err != nil {
		return err
	}
	for _, value := range array(g.Creation["createdUsing"]) {
		node := g.Nodes[text(value)]
		if text(node["type"]) != "Tool" {
			return fmt.Errorf("SPDX creator tool reference is missing or has the wrong type")
		}
	}
	return nil
}

func (g *SPDXGraph) checkElements() error {
	seen := map[string]bool{}
	for _, value := range array(g.Document["element"]) {
		id := text(value)
		if seen[id] || g.Nodes[id] == nil {
			return fmt.Errorf("duplicate or missing SPDX document element %s", id)
		}
		seen[id] = true
	}
	for id, node := range g.Nodes {
		if text(node["type"]) != "CreationInfo" && text(node["type"]) != "SpdxDocument" && !seen[id] {
			return fmt.Errorf("SPDX node %s is absent from document elements", id)
		}
	}
	roots := array(g.Document["rootElement"])
	if len(roots) != 1 || g.Nodes[text(roots[0])] == nil {
		return fmt.Errorf("SPDX requires exactly one existing archive root")
	}
	return nil
}

func (g *SPDXGraph) addRelationship(node Object) error {
	kind, from := text(node["relationshipType"]), text(node["from"])
	if g.Nodes[from] == nil {
		return fmt.Errorf("SPDX relationship source %s is absent", from)
	}
	if g.Edges[kind] == nil {
		g.Edges[kind] = map[string][]string{}
	}
	refs := slices.Clone(g.Edges[kind][from])
	for _, value := range array(node["to"]) {
		to := text(value)
		if g.Nodes[to] == nil || to == from || slices.Contains(refs, to) {
			return fmt.Errorf("SPDX relationship has a missing, self or duplicate target %s", to)
		}
		refs = append(refs, to)
	}
	slices.Sort(refs)
	g.Edges[kind][from] = refs
	return nil
}

// SPDXProperties reads the standard CycloneDX properties extension losslessly.
func SPDXProperties(node Object) (map[string]string, error) {
	props := map[string]string{}
	for _, value := range array(node["extension"]) {
		ext := object(value)
		if text(ext["type"]) != "extension_CdxPropertiesExtension" {
			continue
		}
		for _, raw := range array(ext["extension_cdxProperty"]) {
			entry := object(raw)
			name := text(entry["extension_cdxPropName"])
			if _, ok := props[name]; ok {
				return nil, fmt.Errorf("duplicate SPDX extension property %s", name)
			}
			props[name] = text(entry["extension_cdxPropValue"])
		}
	}
	return props, nil
}

func object(value any) Object { result, _ := value.(map[string]any); return result }
func array(value any) []any   { result, _ := value.([]any); return result }
func text(value any) string   { result, _ := value.(string); return result }
