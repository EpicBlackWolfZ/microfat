package sbom

import (
	"maps"
	"testing"

	cdx "github.com/CycloneDX/cyclonedx-go"
	"github.com/stretchr/testify/require"
)

func replaceExtension(t *testing.T, node Object, name, value string) {
	t.Helper()
	for _, raw := range array(node["extension"]) {
		for _, entry := range array(object(raw)["extension_cdxProperty"]) {
			property := object(entry)
			if text(property["extension_cdxPropName"]) == name {
				property["extension_cdxPropValue"] = value
				return
			}
		}
	}
	t.Fatalf("missing extension property %s", name)
}

func TestSPDXCatalogRejectsContradictions(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*testing.T, Object){
		"license_encoding": func(t *testing.T, v Object) {
			replaceExtension(t, nodeOfType(t, v, "software_File"), "licenses", "not JSON")
		},
		"reference_encoding": func(t *testing.T, v Object) {
			replaceExtension(t, nodeOfType(t, v, "software_Package"), "externalReferences", "not JSON")
		},
		"native_source_missing": func(t *testing.T, v Object) {
			delete(nodeOfType(t, v, "software_Package"), "externalRef")
		},
		"component_type": func(t *testing.T, v Object) {
			replaceExtension(t, nodeOfType(t, v, "software_File"), "properties.microfat:component_type", "application")
		},
		"non_component_root": func(t *testing.T, v Object) {
			nodeOfType(t, v, "SpdxDocument")["rootElement"] = []any{nodeOfType(t, v, "Tool")["spdxId"]}
		},
		"non_component_edge_source": func(t *testing.T, v Object) {
			nodeOfType(t, v, "Relationship")["from"] = nodeOfType(t, v, "Tool")["spdxId"]
		},
		"non_component_edge_target": func(t *testing.T, v Object) {
			nodeOfType(t, v, "Relationship")["to"] = []any{nodeOfType(t, v, "Tool")["spdxId"]}
		},
		"tool_encoding": func(t *testing.T, v Object) {
			replaceExtension(t, nodeOfType(t, v, "SpdxDocument"), "metadataTools", "not JSON")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := jsonObject(t, fixture(t, "archive.spdx.json"))
			mutate(t, v)
			graph, err := ReadSPDX(jsonBytes(t, v))
			require.NoError(t, err, "mutation remains schema-valid; semantic reconstruction must reject it")
			_, err = graph.Catalog()
			require.Error(t, err)
		})
	}
}

func TestSPDXRejectsUnattributedNodes(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"Tool", "simplelicensing_LicenseExpression"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			v := jsonObject(t, fixture(t, "archive.spdx.json"))
			node := maps.Clone(nodeOfType(t, v, kind))
			node["spdxId"] = "urn:unattributed:extra"
			v["@graph"] = append(array(v["@graph"]), node)
			doc := nodeOfType(t, v, "SpdxDocument")
			doc["element"] = append(array(doc["element"]), node["spdxId"])
			graph, err := ReadSPDX(jsonBytes(t, v))
			require.NoError(t, err)
			catalog, err := graph.Catalog()
			require.NoError(t, err)
			require.ErrorContains(t, graph.ValidateAttribution(catalog), "unattributed")
		})
	}
}

func TestSPDXLicenseEdgesMustDescribeComponents(t *testing.T) {
	t.Parallel()
	v := jsonObject(t, fixture(t, "archive.spdx.json"))
	for _, raw := range array(v["@graph"]) {
		node := object(raw)
		if text(node["relationshipType"]) == "hasDeclaredLicense" {
			node["from"] = nodeOfType(t, v, "Tool")["spdxId"]
			break
		}
	}
	graph, err := ReadSPDX(jsonBytes(t, v))
	require.NoError(t, err)
	catalog, err := graph.Catalog()
	require.NoError(t, err)
	require.ErrorContains(t, graph.ValidateAttribution(catalog), "source is not a component")
}

func TestSPDXLicenseChoiceOrder(t *testing.T) {
	t.Parallel()
	const licenseExpressionID, licenseTextID = "urn:a", "urn:b"
	licenses := cdx.Licenses{{License: &cdx.License{ID: "MIT"}}, {Expression: "Apache-2.0"},
		{License: &cdx.License{Name: "Custom", Text: &cdx.AttachedText{Content: "custom terms"}}}}
	graph := &SPDXGraph{Nodes: map[string]Object{
		licenseExpressionID: {"type": "simplelicensing_LicenseExpression", "simplelicensing_licenseExpression": "Apache-2.0"},
		licenseTextID:       {"type": "simplelicensing_SimpleLicensingText", "simplelicensing_licenseText": "custom terms"},
		"urn:c":             {"type": "simplelicensing_LicenseExpression", "simplelicensing_licenseExpression": "MIT"},
	}}
	require.NoError(t, graph.checkLicenses(&licenses, []string{licenseExpressionID, licenseTextID, "urn:c"}))
	require.Error(t, graph.checkLicenses(&licenses, []string{licenseExpressionID, licenseTextID, licenseTextID}))
	require.Error(t, graph.checkLicenses(&licenses, []string{licenseExpressionID}))
	require.Error(t, graph.checkLicenses(nil, []string{licenseExpressionID}))
	require.NoError(t, graph.checkLicenses(nil, nil))
	require.Error(t, graph.checkLicenses(&cdx.Licenses{{}}, []string{licenseExpressionID}))
	graph.Nodes[licenseExpressionID]["type"] = "Tool"
	require.Error(t, graph.checkLicenses(&licenses, []string{licenseExpressionID, licenseTextID, "urn:c"}))
}

func TestSPDXCreatorIntegrity(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*testing.T, Object){
		"duplicate_creator_reference": func(t *testing.T, v Object) {
			node := nodeOfType(t, v, "CreationInfo")
			ids := array(node["createdUsing"])
			node["createdUsing"] = append(ids, ids[0])
		},
		"missing_preserved_creator": func(t *testing.T, v Object) {
			replaceExtension(t, nodeOfType(t, v, "SpdxDocument"), "metadataTools", `{}`)
		},
		"wrong_preserved_creator_count": func(t *testing.T, v Object) {
			replaceExtension(t, nodeOfType(t, v, "SpdxDocument"), "metadataTools", `{"components":[]}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := jsonObject(t, fixture(t, "archive.spdx.json"))
			mutate(t, v)
			graph, err := ReadSPDX(jsonBytes(t, v))
			require.NoError(t, err)
			catalog, err := graph.Catalog()
			require.NoError(t, err)
			require.Error(t, graph.ValidateAttribution(catalog))
		})
	}
}
