package sbom

import (
	"testing"

	cdx "github.com/CycloneDX/cyclonedx-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalCatalogAndFlatConversion(t *testing.T) {
	t.Parallel()
	catalog, err := ReadCycloneDX(fixture(t, "archive.cdx.json"))
	require.NoError(t, err)
	require.Len(t, catalog.Components, 14)
	require.Len(t, catalog.Contains[catalog.Root], 3)
	flat, err := catalog.ConversionView()
	require.NoError(t, err)
	converted, err := ReadCycloneDX(flat)
	require.NoError(t, err)
	assert.Len(t, converted.Contains, 0)
	assert.Equal(t, catalog.Depends, converted.Depends)
	assert.Equal(t, catalog.BOM.Metadata.Tools, converted.BOM.Metadata.Tools)
	for ref, component := range catalog.Components {
		component.Components = nil
		assert.Equal(t, component, converted.Components[ref])
	}
	assert.Len(t, catalog.Contains, 2, "flattening must not mutate canonical containment")
}

func TestCatalogRejectsBrokenIdentityAndReferences(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(Object){
		"missing_root": func(v Object) { delete(object(v["metadata"]), "component") },
		"missing_ref":  func(v Object) { delete(object(v["components"].([]any)[0]), "bom-ref") },
		"duplicate_ref": func(v Object) {
			components := array(v["components"])
			v["components"] = append(components, components[0])
		},
		"duplicate_property": func(v Object) {
			c := object(array(v["components"])[0])
			p := array(c["properties"])
			c["properties"] = append(p, p[0])
		},
		"missing_dependency_source":   func(v Object) { object(array(v["dependencies"])[0])["ref"] = "absent" },
		"missing_dependency_target":   func(v Object) { object(array(v["dependencies"])[0])["dependsOn"] = []string{"absent"} },
		"duplicate_dependency_source": func(v Object) { d := array(v["dependencies"]); v["dependencies"] = append(d, d[0]) },
		"self_dependency":             func(v Object) { d := object(array(v["dependencies"])[0]); d["dependsOn"] = []any{d["ref"]} },
		"duplicate_dependency_target": func(v Object) {
			d := object(array(v["dependencies"])[0])
			refs := array(d["dependsOn"])
			d["dependsOn"] = append(refs, refs[0])
		},
		"old_schema": func(v Object) { v["specVersion"] = "1.5" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := jsonObject(t, fixture(t, "archive.cdx.json"))
			mutate(v)
			_, err := ReadCycloneDX(jsonBytes(t, v))
			require.Error(t, err)
		})
	}
}

func TestOptionalEmptyCatalogFields(t *testing.T) {
	t.Parallel()
	bom := cdx.NewBOM()
	bom.Metadata = &cdx.Metadata{Component: &cdx.Component{BOMRef: "root", Type: cdx.ComponentTypeFile, Name: "archive"}}
	catalog, err := ReadCycloneDX(jsonBytes(t, bom))
	require.NoError(t, err)
	assert.Empty(t, catalog.Depends)
	props, err := Properties(*bom.Metadata.Component)
	require.NoError(t, err)
	assert.Empty(t, props)
}
