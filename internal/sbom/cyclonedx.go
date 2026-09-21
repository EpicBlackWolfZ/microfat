package sbom

import (
	"encoding/json"
	"fmt"
	"slices"

	cdx "github.com/CycloneDX/cyclonedx-go"
)

// Catalog is the fully attributed component graph of a validated CycloneDX BOM.
// The full library model keeps licenses, purls, tool identity and source metadata.
type Catalog struct {
	BOM        *cdx.BOM
	Root       string
	Components map[string]cdx.Component
	Contains   map[string][]string
	Depends    map[string][]string
}

// ReadCycloneDX validates and indexes a canonical CycloneDX 1.7 release SBOM.
func ReadCycloneDX(data []byte) (*Catalog, error) {
	if err := ValidateSchema(data, false); err != nil {
		return nil, err
	}
	var bom cdx.BOM
	if err := json.Unmarshal(data, &bom); err != nil {
		return nil, err
	}
	if bom.SpecVersion != cdx.SpecVersion1_7 || bom.Metadata == nil || bom.Metadata.Component == nil {
		return nil, fmt.Errorf("release SBOM requires CycloneDX 1.7 and an archive root")
	}
	c := &Catalog{BOM: &bom, Root: bom.Metadata.Component.BOMRef, Components: map[string]cdx.Component{},
		Contains: map[string][]string{}, Depends: map[string][]string{}}
	if err := c.add(*bom.Metadata.Component, ""); err != nil {
		return nil, err
	}
	if bom.Components != nil {
		for _, component := range *bom.Components {
			if err := c.add(component, ""); err != nil {
				return nil, err
			}
		}
	}
	if bom.Dependencies != nil {
		for _, dep := range *bom.Dependencies {
			if err := c.addDependencies(dep); err != nil {
				return nil, err
			}
		}
	}
	return c, nil
}

func (c *Catalog) add(component cdx.Component, parent string) error {
	ref := component.BOMRef
	if ref == "" {
		return fmt.Errorf("component %s has no bom-ref", component.Name)
	}
	if _, ok := c.Components[ref]; ok {
		return fmt.Errorf("duplicate component reference %s", ref)
	}
	if _, err := Properties(component); err != nil {
		return err
	}
	c.Components[ref] = component
	if parent != "" {
		c.Contains[parent] = append(c.Contains[parent], ref)
	}
	if component.Components != nil {
		for _, child := range *component.Components {
			if err := c.add(child, ref); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Catalog) addDependencies(dep cdx.Dependency) error {
	if _, ok := c.Components[dep.Ref]; !ok {
		return fmt.Errorf("dependency source %s is absent", dep.Ref)
	}
	if _, ok := c.Depends[dep.Ref]; ok {
		return fmt.Errorf("duplicate dependency source %s", dep.Ref)
	}
	refs := []string{}
	if dep.Dependencies != nil {
		refs = slices.Clone(*dep.Dependencies)
	}
	slices.Sort(refs)
	for index, ref := range refs {
		if _, ok := c.Components[ref]; !ok {
			return fmt.Errorf("dependency target %s is absent", ref)
		}
		if ref == dep.Ref || (index > 0 && refs[index-1] == ref) {
			return fmt.Errorf("self or duplicate dependency %s -> %s", dep.Ref, ref)
		}
	}
	c.Depends[dep.Ref] = refs
	return nil
}

// Properties reads metadata without silently overwriting duplicate keys.
func Properties(component cdx.Component) (map[string]string, error) {
	values := map[string]string{}
	if component.Properties == nil {
		return values, nil
	}
	for _, p := range *component.Properties {
		if _, ok := values[p.Name]; ok {
			return nil, fmt.Errorf("duplicate property %s on %s", p.Name, component.BOMRef)
		}
		values[p.Name] = p.Value
	}
	return values, nil
}

// ConversionView flattens only the component hierarchy for cdx-convert, which
// enumerates metadata.component and top-level components. The canonical document
// retains containment; the SPDX adapter adds equivalent contains relationships.
func (c *Catalog) ConversionView() ([]byte, error) {
	data, err := json.Marshal(c.BOM)
	if err != nil {
		return nil, err
	}
	var flat cdx.BOM
	if err := json.Unmarshal(data, &flat); err != nil {
		return nil, err
	}
	flat.Metadata.Component.Components = nil
	refs := make([]string, 0, len(c.Components))
	for ref := range c.Components {
		if ref != c.Root {
			refs = append(refs, ref)
		}
	}
	slices.Sort(refs)
	components := make([]cdx.Component, 0, len(refs))
	for _, ref := range refs {
		component := c.Components[ref]
		component.Components = nil
		components = append(components, component)
	}
	flat.Components = &components
	return json.MarshalIndent(flat, "", "  ")
}
