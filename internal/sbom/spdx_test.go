package sbom

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const absentSPDXID = "urn:absent"

func nodeOfType(t *testing.T, value Object, kind string) Object {
	t.Helper()
	for _, raw := range array(value["@graph"]) {
		node := object(raw)
		if text(node["type"]) == kind {
			return node
		}
	}
	t.Fatalf("missing node type %s", kind)
	return nil
}

func removeNode(value Object, target Object) {
	id := text(target["spdxId"])
	graph := []any{}
	for _, raw := range array(value["@graph"]) {
		n := object(raw)
		if text(n["spdxId"]) != id {
			graph = append(graph, n)
		}
	}
	value["@graph"] = graph
}

func TestSPDXConversionPreservesInventory(t *testing.T) {
	t.Parallel()
	catalog, err := ReadCycloneDX(fixture(t, "archive.cdx.json"))
	require.NoError(t, err)
	data, err := CompleteSPDX(catalog, fixture(t, "converted.spdx.json"))
	require.NoError(t, err)
	graph, err := ReadSPDX(data)
	require.NoError(t, err)
	recovered, err := graph.Catalog()
	require.NoError(t, err)
	require.NoError(t, graph.ValidateAttribution(recovered))
	assert.Equal(t, catalog.Root, recovered.Root)
	assert.Equal(t, catalog.Depends, recovered.Depends)
	assert.Len(t, graph.Edges["contains"], 2)
	assert.Len(t, graph.Edges["hasDeclaredLicense"], 8)
	for ref, original := range catalog.Components {
		original.Components = nil
		actual := recovered.Components[ref]
		// Property order is immaterial; all values must remain present.
		wantProps, err := Properties(original)
		require.NoError(t, err)
		gotProps, err := Properties(actual)
		require.NoError(t, err)
		assert.Equal(t, wantProps, gotProps)
		original.Properties = nil
		actual.Properties = nil
		assert.Equal(t, original, actual)
	}
}

func TestSPDXRejectsReferenceFailures(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*testing.T, Object){
		"missing_component": func(t *testing.T, v Object) { removeNode(v, nodeOfType(t, v, "software_Package")) },
		"duplicate_identifier": func(t *testing.T, v Object) {
			v["@graph"] = append(array(v["@graph"]), nodeOfType(t, v, "software_Package"))
		},
		"invalid_creation_reference":  func(t *testing.T, v Object) { nodeOfType(t, v, "software_Package")["creationInfo"] = absentSPDXID },
		"missing_relationship_target": func(t *testing.T, v Object) { nodeOfType(t, v, "Relationship")["to"] = []string{absentSPDXID} },
		"self_relationship":           func(t *testing.T, v Object) { n := nodeOfType(t, v, "Relationship"); n["to"] = []any{n["from"]} },
		"duplicate_target": func(t *testing.T, v Object) {
			n := nodeOfType(t, v, "Relationship")
			a := array(n["to"])
			n["to"] = append(a, a[0])
		},
		"missing_source": func(t *testing.T, v Object) { nodeOfType(t, v, "Relationship")["from"] = absentSPDXID },
		"missing_document_element": func(t *testing.T, v Object) {
			n := nodeOfType(t, v, "SpdxDocument")
			n["element"] = array(n["element"])[1:]
		},
		"duplicate_document_element": func(t *testing.T, v Object) {
			n := nodeOfType(t, v, "SpdxDocument")
			a := array(n["element"])
			n["element"] = append(a, a[0])
		},
		"missing_root": func(t *testing.T, v Object) { nodeOfType(t, v, "SpdxDocument")["rootElement"] = []string{absentSPDXID} },
		"missing_tool": func(t *testing.T, v Object) {
			nodeOfType(t, v, "CreationInfo")["createdUsing"] = []string{absentSPDXID}
		},
		"bad_context":   func(t *testing.T, v Object) { v["@context"] = "https://untrusted.invalid/context" },
		"wrong_version": func(t *testing.T, v Object) { nodeOfType(t, v, "CreationInfo")["specVersion"] = "2.3" },
		"duplicate_extension_key": func(t *testing.T, v Object) {
			n := nodeOfType(t, v, "software_Package")
			ext := object(array(n["extension"])[0])
			a := array(ext["extension_cdxProperty"])
			ext["extension_cdxProperty"] = append(a, a[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := jsonObject(t, fixture(t, "archive.spdx.json"))
			mutate(t, v)
			_, err := ReadSPDX(jsonBytes(t, v))
			require.Error(t, err)
		})
	}
}

func TestConversionRejectsSemanticLoss(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*testing.T, Object){
		"name":        func(t *testing.T, v Object) { nodeOfType(t, v, "software_Package")["name"] = "changed" },
		"package_url": func(t *testing.T, v Object) { delete(nodeOfType(t, v, "software_Package"), "software_packageUrl") },
		"version": func(t *testing.T, v Object) {
			nodeOfType(t, v, "software_Package")["software_packageVersion"] = "wrong"
		},
		"hash": func(t *testing.T, v Object) {
			n := nodeOfType(t, v, "software_File")
			object(array(n["verifiedUsing"])[0])["hashValue"] = string(make([]byte, 64))
		},
		"description": func(t *testing.T, v Object) { nodeOfType(t, v, "software_Package")["description"] = "invented" },
		"timestamp":   func(t *testing.T, v Object) { nodeOfType(t, v, "CreationInfo")["created"] = "2026-01-01T00:00:00Z" },
		"properties": func(t *testing.T, v Object) {
			n := nodeOfType(t, v, "software_Package")
			ext := object(array(n["extension"])[0])
			for _, raw := range array(ext["extension_cdxProperty"]) {
				p := object(raw)
				if text(p["extension_cdxPropName"]) == "properties.microfat:target_arch" {
					p["extension_cdxPropValue"] = "wrong"
				}
			}
		},
		"license": func(t *testing.T, v Object) {
			n := nodeOfType(t, v, "software_Package")
			ext := object(array(n["extension"])[0])
			for _, raw := range array(ext["extension_cdxProperty"]) {
				p := object(raw)
				if text(p["extension_cdxPropName"]) == "licenses" {
					p["extension_cdxPropValue"] = "[]"
				}
			}
		},
		"dependency_set": func(t *testing.T, v Object) {
			n := nodeOfType(t, v, "Relationship")
			a := array(n["to"])
			n["to"] = a[1:]
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, err := ReadCycloneDX(fixture(t, "archive.cdx.json"))
			require.NoError(t, err)
			v := jsonObject(t, fixture(t, "converted.spdx.json"))
			mutate(t, v)
			_, err = CompleteSPDX(c, jsonBytes(t, v))
			require.Error(t, err)
		})
	}
}

func TestSPDXRejectsAttributionMismatch(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*testing.T, Object){
		"license_expression": func(t *testing.T, v Object) {
			nodeOfType(t, v, "simplelicensing_LicenseExpression")["simplelicensing_licenseExpression"] = "MIT"
		},
		"creator_name":         func(t *testing.T, v Object) { nodeOfType(t, v, "Tool")["name"] = "unreviewed tool" },
		"missing_creator_link": func(t *testing.T, v Object) { delete(nodeOfType(t, v, "CreationInfo"), "createdUsing") },
		"unexpected_edge":      func(t *testing.T, v Object) { nodeOfType(t, v, "Relationship")["relationshipType"] = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := jsonObject(t, fixture(t, "archive.spdx.json"))
			mutate(t, v)
			g, err := ReadSPDX(jsonBytes(t, v))
			require.NoError(t, err)
			c, err := g.Catalog()
			require.NoError(t, err)
			require.Error(t, g.ValidateAttribution(c))
		})
	}
}
