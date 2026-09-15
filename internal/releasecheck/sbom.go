package releasecheck

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SPDXChecksum represents a checksum algorithm and value in an SPDX document.
type SPDXChecksum struct {
	Algorithm     string `json:"algorithm"`
	ChecksumValue string `json:"checksumValue"`
}

// SPDXPackage represents a software package entry in an SPDX document.
type SPDXPackage struct {
	Name             string         `json:"name"`
	SPDXID           string         `json:"SPDXID"`
	VersionInfo      string         `json:"versionInfo,omitempty"`
	DownloadLocation string         `json:"downloadLocation"`
	FilesAnalyzed    bool           `json:"filesAnalyzed"`
	Checksums        []SPDXChecksum `json:"checksums,omitempty"`
	Supplier         string         `json:"supplier,omitempty"`
	Originator       string         `json:"originator,omitempty"`
	Comment          string         `json:"comment,omitempty"`
}

// SPDXRelationship describes a relationship between two SPDX elements.
type SPDXRelationship struct {
	SPDXElementID      string `json:"spdxElementId"`
	RelatedSPDXElement string `json:"relatedSpdxElement"`
	RelationshipType   string `json:"relationshipType"`
	Comment            string `json:"comment,omitempty"`
}

// SPDXCreationInfo represents the creation information of an SPDX document.
type SPDXCreationInfo struct {
	Created            string   `json:"created"`
	Creators           []string `json:"creators"`
	LicenseListVersion string   `json:"licenseListVersion,omitempty"`
	Comment            string   `json:"comment,omitempty"`
}

// SPDXDocument represents an SPDX 2.3 JSON document.
type SPDXDocument struct {
	SPDXVersion       string             `json:"spdxVersion"`
	DataLicense       string             `json:"dataLicense"`
	SPDXID            string             `json:"SPDXID"`
	Name              string             `json:"name"`
	DocumentNamespace string             `json:"documentNamespace"`
	CreationInfo      SPDXCreationInfo   `json:"creationInfo"`
	Packages          []SPDXPackage      `json:"packages"`
	Relationships     []SPDXRelationship `json:"relationships"`
}

// CDXHash represents a cryptographic hash in a CycloneDX document.
type CDXHash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

// CDXProperty represents a name-value metadata property in CycloneDX.
type CDXProperty struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CDXComponent represents an individual software component in CycloneDX.
type CDXComponent struct {
	BOMRef     string         `json:"bom-ref"`
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	Version    string         `json:"version,omitempty"`
	Hashes     []CDXHash      `json:"hashes,omitempty"`
	Properties []CDXProperty  `json:"properties,omitempty"`
	Components []CDXComponent `json:"components,omitempty"`
}

// CDXMetadata represents the top-level metadata in a CycloneDX document.
type CDXMetadata struct {
	Timestamp string        `json:"timestamp,omitempty"`
	Component *CDXComponent `json:"component,omitempty"`
}

// CDXDependency represents a directed dependency edge in CycloneDX.
type CDXDependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// CDXDocument represents a CycloneDX JSON document.
type CDXDocument struct {
	BOMFormat    string          `json:"bomFormat"`
	SpecVersion  string          `json:"specVersion"`
	Metadata     CDXMetadata     `json:"metadata"`
	Components   []CDXComponent  `json:"components"`
	Dependencies []CDXDependency `json:"dependencies,omitempty"`
}

// ValidateSPDX reads and semantically validates an SPDX SBOM file against archive facts and inventory.
func ValidateSPDX(spdxPath string, facts *ArchiveFacts, inv *ArchiveInventory) error {
	// #nosec G304 -- spdxPath provided by caller
	data, err := os.ReadFile(spdxPath)
	if err != nil {
		return fmt.Errorf("reading SPDX document %s: %w", spdxPath, err)
	}
	return ValidateSPDXBytes(data, facts, inv)
}

func validateSPDXHeader(doc *SPDXDocument, facts *ArchiveFacts) error {
	if doc.SPDXVersion != "SPDX-2.3" {
		return fmt.Errorf("invalid or unsupported SPDX version: %q, expected SPDX-2.3", doc.SPDXVersion)
	}
	if doc.DataLicense != "CC0-1.0" {
		return fmt.Errorf("invalid SPDX data license: %q, expected CC0-1.0", doc.DataLicense)
	}
	if doc.SPDXID != "SPDXRef-DOCUMENT" {
		return fmt.Errorf("invalid SPDX root document ID: %q, expected SPDXRef-DOCUMENT", doc.SPDXID)
	}
	if doc.Name != facts.ArchiveName {
		return fmt.Errorf("SPDX document name mismatch: expected %q, got %q", facts.ArchiveName, doc.Name)
	}
	if doc.CreationInfo.Created == "" {
		return fmt.Errorf("SPDX creationInfo missing required 'created' timestamp")
	}
	if len(doc.CreationInfo.Creators) == 0 {
		return fmt.Errorf("SPDX creationInfo missing required 'creators' list")
	}
	for _, c := range doc.CreationInfo.Creators {
		if strings.TrimSpace(c) == "" {
			return fmt.Errorf("SPDX creationInfo contains empty creator entry")
		}
	}
	return nil
}

func validateSPDXPackages(
	doc *SPDXDocument,
	facts *ArchiveFacts,
	inv *ArchiveInventory,
) (map[string]SPDXPackage, map[string]bool, error) {
	knownIDs := make(map[string]bool)
	knownIDs[doc.SPDXID] = true
	pkgByName := make(map[string]SPDXPackage)

	for _, p := range doc.Packages {
		if p.SPDXID == "" {
			return nil, nil, fmt.Errorf("package missing SPDXID: %s", p.Name)
		}
		if knownIDs[p.SPDXID] {
			return nil, nil, fmt.Errorf("duplicate SPDXID detected: %s", p.SPDXID)
		}
		knownIDs[p.SPDXID] = true
		pkgByName[p.Name] = p
	}

	archPkg, ok := pkgByName[facts.ArchiveName]
	if !ok {
		return nil, nil, fmt.Errorf("missing package describing release archive %s", facts.ArchiveName)
	}
	if err := verifySPDXChecksum(archPkg, facts.ArchiveSHA256); err != nil {
		return nil, nil, fmt.Errorf("archive package checksum: %w", err)
	}
	if archPkg.Comment != "" && !strings.Contains(archPkg.Comment, facts.TargetArch) {
		return nil, nil, fmt.Errorf("archive package architecture mismatch in comment: expected %s, got %s",
			facts.TargetArch, archPkg.Comment)
	}

	for exeName, exe := range facts.Executables {
		p, ok := pkgByName[exeName]
		if !ok {
			return nil, nil, fmt.Errorf("missing package for root executable: %s", exeName)
		}
		if err := verifySPDXChecksum(p, exe.SHA256); err != nil {
			return nil, nil, fmt.Errorf("executable %s checksum: %w", exeName, err)
		}
	}

	for tier, vf := range facts.EmbeddedVariants {
		vName := "microfat-variant-" + tier
		p, ok := pkgByName[vName]
		if !ok {
			return nil, nil, fmt.Errorf("missing package for embedded variant: %s", vName)
		}
		if err := verifySPDXChecksum(p, vf.SHA256); err != nil {
			return nil, nil, fmt.Errorf("variant %s checksum: %w", tier, err)
		}
	}

	if err := validateSPDXDependencies(pkgByName, inv); err != nil {
		return nil, nil, err
	}

	return pkgByName, knownIDs, nil
}

func validateSPDXDependencies(pkgByName map[string]SPDXPackage, inv *ArchiveInventory) error {
	for modPath, dep := range inv.AllDependencies {
		expectedPath := modPath
		expectedVer := dep.Version
		if dep.ReplacePath != "" {
			expectedPath = dep.ReplacePath
			expectedVer = dep.ReplaceVer
		}
		p, ok := pkgByName[expectedPath]
		if !ok {
			p, ok = pkgByName[modPath]
		}
		if !ok {
			return fmt.Errorf("missing required linked module dependency package: %s", modPath)
		}
		if p.VersionInfo != expectedVer && dep.Version != "(devel)" {
			return fmt.Errorf("module %s version mismatch: expected %s, got %s", modPath, expectedVer, p.VersionInfo)
		}
	}
	return nil
}

func validateSPDXRelationships(
	doc *SPDXDocument,
	facts *ArchiveFacts,
	inv *ArchiveInventory,
	pkgByName map[string]SPDXPackage,
	knownIDs map[string]bool,
) error {
	relTypes := make(map[string]map[string]string)
	for _, rel := range doc.Relationships {
		if !knownIDs[rel.SPDXElementID] {
			return fmt.Errorf("orphaned relationship source SPDXID: %s", rel.SPDXElementID)
		}
		if !knownIDs[rel.RelatedSPDXElement] {
			return fmt.Errorf("orphaned relationship target SPDXID: %s", rel.RelatedSPDXElement)
		}
		if relTypes[rel.SPDXElementID] == nil {
			relTypes[rel.SPDXElementID] = make(map[string]string)
		}
		relTypes[rel.SPDXElementID][rel.RelatedSPDXElement] = rel.RelationshipType
	}

	archPkg := pkgByName[facts.ArchiveName]
	if relTypes[doc.SPDXID][archPkg.SPDXID] != "DESCRIBES" {
		return fmt.Errorf("document missing DESCRIBES relationship to archive package %s", archPkg.SPDXID)
	}

	for exeName := range facts.Executables {
		p := pkgByName[exeName]
		if relTypes[archPkg.SPDXID][p.SPDXID] != "CONTAINS" {
			return fmt.Errorf("archive package missing CONTAINS relationship to executable %s", exeName)
		}
	}

	fatPkg := pkgByName[ReleaseProjectName]
	for tier := range facts.EmbeddedVariants {
		vPkg := pkgByName["microfat-variant-"+tier]
		if relTypes[fatPkg.SPDXID][vPkg.SPDXID] != "CONTAINS" {
			return fmt.Errorf("fat binary package missing CONTAINS relationship to variant %s", tier)
		}
	}

	for binID, binInv := range inv.Binaries {
		sourcePkgID := getSPDXBinaryID(binID, pkgByName)
		if sourcePkgID == "" {
			continue
		}
		for modPath := range binInv.Dependencies {
			modPkg, ok := pkgByName[modPath]
			if !ok {
				continue
			}
			if relTypes[sourcePkgID][modPkg.SPDXID] == "" {
				return fmt.Errorf("missing module association relationship between binary %s and %s", sourcePkgID, modPath)
			}
		}
	}
	return nil
}

// ValidateSPDXBytes validates an in-memory SPDX document against archive facts and inventory.
func ValidateSPDXBytes(data []byte, facts *ArchiveFacts, inv *ArchiveInventory) error {
	if facts == nil || inv == nil {
		return fmt.Errorf("archive facts and inventory cannot be nil")
	}

	var doc SPDXDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing SPDX JSON: %w", err)
	}

	if err := validateSPDXHeader(&doc, facts); err != nil {
		return err
	}

	pkgByName, knownIDs, err := validateSPDXPackages(&doc, facts, inv)
	if err != nil {
		return err
	}

	return validateSPDXRelationships(&doc, facts, inv, pkgByName, knownIDs)
}

func getSPDXBinaryID(binID string, pkgByName map[string]SPDXPackage) string {
	if strings.HasPrefix(binID, "variant:") {
		tier := strings.TrimPrefix(binID, "variant:")
		if p, ok := pkgByName["microfat-variant-"+tier]; ok {
			return p.SPDXID
		}
	}
	if p, ok := pkgByName[binID]; ok {
		return p.SPDXID
	}
	return ""
}

func verifySPDXChecksum(p SPDXPackage, expectedSHA256 string) error {
	for _, cs := range p.Checksums {
		if strings.EqualFold(cs.Algorithm, "SHA256") || strings.EqualFold(cs.Algorithm, "SHA-256") {
			if strings.EqualFold(cs.ChecksumValue, expectedSHA256) {
				return nil
			}
			return fmt.Errorf("expected %s, got %s", expectedSHA256, cs.ChecksumValue)
		}
	}
	return fmt.Errorf("no SHA-256 checksum found on package %s", p.Name)
}

// ValidateCycloneDX reads and semantically validates a CycloneDX SBOM file against archive facts and inventory.
func ValidateCycloneDX(cdxPath string, facts *ArchiveFacts, inv *ArchiveInventory) error {
	// #nosec G304 -- cdxPath provided by caller
	data, err := os.ReadFile(cdxPath)
	if err != nil {
		return fmt.Errorf("reading CycloneDX document %s: %w", cdxPath, err)
	}
	return ValidateCycloneDXBytes(data, facts, inv)
}

func validateCDXHeader(doc *CDXDocument, facts *ArchiveFacts) error {
	if doc.BOMFormat != "CycloneDX" {
		return fmt.Errorf("invalid bomFormat: %q, expected CycloneDX", doc.BOMFormat)
	}
	if doc.SpecVersion == "" {
		return fmt.Errorf("missing specVersion in CycloneDX document")
	}

	if doc.Metadata.Component == nil {
		return fmt.Errorf("missing root metadata.component in CycloneDX document")
	}
	metaComp := doc.Metadata.Component
	if metaComp.Name != facts.ArchiveName {
		return fmt.Errorf("root component name mismatch: expected %q, got %q", facts.ArchiveName, metaComp.Name)
	}
	if err := verifyCDXHash(*metaComp, facts.ArchiveSHA256); err != nil {
		return fmt.Errorf("archive metadata component hash: %w", err)
	}
	for _, prop := range metaComp.Properties {
		if prop.Name == "microfat:target_arch" && prop.Value != facts.TargetArch {
			return fmt.Errorf("archive component target_arch mismatch: expected %s, got %s", facts.TargetArch, prop.Value)
		}
	}
	return nil
}

func populateCDXComponentsTree(
	doc *CDXDocument,
	compByName map[string]CDXComponent,
	knownRefs map[string]bool,
) error {
	var collectComponents func(comps []CDXComponent) error
	collectComponents = func(comps []CDXComponent) error {
		for _, c := range comps {
			if c.BOMRef == "" {
				return fmt.Errorf("component %q is missing bom-ref", c.Name)
			}
			if knownRefs[c.BOMRef] {
				return fmt.Errorf("duplicate bom-ref detected: %q", c.BOMRef)
			}
			knownRefs[c.BOMRef] = true
			compByName[c.Name] = c
			if len(c.Components) > 0 {
				if err := collectComponents(c.Components); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if doc.Metadata.Component != nil && len(doc.Metadata.Component.Components) > 0 {
		if err := collectComponents(doc.Metadata.Component.Components); err != nil {
			return err
		}
	}

	return collectComponents(doc.Components)
}

func verifyCDXPayloadHashes(compByName map[string]CDXComponent, facts *ArchiveFacts) error {
	for exeName, exe := range facts.Executables {
		c, ok := compByName[exeName]
		if !ok {
			return fmt.Errorf("missing component for root executable: %s", exeName)
		}
		if err := verifyCDXHash(c, exe.SHA256); err != nil {
			return fmt.Errorf("executable %s hash: %w", exeName, err)
		}
	}

	for tier, vf := range facts.EmbeddedVariants {
		vName := "microfat-variant-" + tier
		c, ok := compByName[vName]
		if !ok {
			return fmt.Errorf("missing component for embedded variant: %s", vName)
		}
		if err := verifyCDXHash(c, vf.SHA256); err != nil {
			return fmt.Errorf("variant %s hash: %w", tier, err)
		}
	}
	return nil
}

func validateCDXComponents(
	doc *CDXDocument,
	facts *ArchiveFacts,
	inv *ArchiveInventory,
) (map[string]CDXComponent, map[string]bool, error) {
	knownRefs := make(map[string]bool)
	if doc.Metadata.Component.BOMRef != "" {
		knownRefs[doc.Metadata.Component.BOMRef] = true
	}
	compByName := make(map[string]CDXComponent)

	if err := populateCDXComponentsTree(doc, compByName, knownRefs); err != nil {
		return nil, nil, err
	}

	if err := verifyCDXPayloadHashes(compByName, facts); err != nil {
		return nil, nil, err
	}

	if err := validateCDXDependencies(compByName, inv); err != nil {
		return nil, nil, err
	}

	return compByName, knownRefs, nil
}

func validateCDXDependencies(compByName map[string]CDXComponent, inv *ArchiveInventory) error {
	for modPath, dep := range inv.AllDependencies {
		expectedPath := modPath
		expectedVer := dep.Version
		if dep.ReplacePath != "" {
			expectedPath = dep.ReplacePath
			expectedVer = dep.ReplaceVer
		}
		c, ok := compByName[expectedPath]
		if !ok {
			c, ok = compByName[modPath]
		}
		if !ok {
			return fmt.Errorf("missing required linked module component: %s", modPath)
		}
		if c.Version != expectedVer && dep.Version != "(devel)" {
			return fmt.Errorf("module %s version mismatch: expected %s, got %s", modPath, expectedVer, c.Version)
		}
	}
	return nil
}

func buildCDXDependencyMap(
	deps []CDXDependency,
	knownRefs map[string]bool,
) (map[string]map[string]bool, error) {
	depMap := make(map[string]map[string]bool)
	for _, dep := range deps {
		if !knownRefs[dep.Ref] {
			return nil, fmt.Errorf("orphaned reference in dependencies ref: %s", dep.Ref)
		}
		if depMap[dep.Ref] == nil {
			depMap[dep.Ref] = make(map[string]bool)
		}
		for _, edge := range dep.DependsOn {
			if !knownRefs[edge] {
				return nil, fmt.Errorf("orphaned reference in dependsOn: %s", edge)
			}
			depMap[dep.Ref][edge] = true
		}
	}
	return depMap, nil
}

func verifyCDXComponentAssemblies(
	metaComp *CDXComponent,
	compByName map[string]CDXComponent,
	facts *ArchiveFacts,
) error {
	metaCompByRef := make(map[string]bool)
	for _, c := range metaComp.Components {
		metaCompByRef[c.BOMRef] = true
	}
	for exeName := range facts.Executables {
		exeComp := compByName[exeName]
		if !metaCompByRef[exeComp.BOMRef] {
			return fmt.Errorf("archive component assembly missing root executable component %s", exeName)
		}
	}

	fatComp := compByName[ReleaseProjectName]
	fatCompByRef := make(map[string]bool)
	for _, c := range fatComp.Components {
		fatCompByRef[c.BOMRef] = true
	}
	for tier := range facts.EmbeddedVariants {
		vComp := compByName["microfat-variant-"+tier]
		if !fatCompByRef[vComp.BOMRef] {
			return fmt.Errorf("fat binary component assembly missing embedded variant component %s", tier)
		}
	}
	return nil
}

func verifyCDXModuleDependencies(
	inv *ArchiveInventory,
	compByName map[string]CDXComponent,
	depMap map[string]map[string]bool,
) error {
	for binID, binInv := range inv.Binaries {
		sourceRef := getCDXBinaryRef(binID, compByName)
		if sourceRef == "" {
			continue
		}
		for modPath := range binInv.Dependencies {
			modComp, ok := compByName[modPath]
			if !ok {
				continue
			}
			if !depMap[sourceRef][modComp.BOMRef] {
				return fmt.Errorf("missing module association dependency edge between binary %s and %s", sourceRef, modPath)
			}
		}
	}
	return nil
}

func validateCDXGraph(
	doc *CDXDocument,
	facts *ArchiveFacts,
	inv *ArchiveInventory,
	compByName map[string]CDXComponent,
	knownRefs map[string]bool,
) error {
	depMap, err := buildCDXDependencyMap(doc.Dependencies, knownRefs)
	if err != nil {
		return err
	}

	metaComp := doc.Metadata.Component
	if len(depMap[metaComp.BOMRef]) > 0 {
		return fmt.Errorf("archive root component must not declare functional dependency edges (containment is modeled via component assembly)")
	}

	if err := verifyCDXComponentAssemblies(metaComp, compByName, facts); err != nil {
		return err
	}

	return verifyCDXModuleDependencies(inv, compByName, depMap)
}

// ValidateCycloneDXBytes validates an in-memory CycloneDX document against archive facts and inventory.
func ValidateCycloneDXBytes(data []byte, facts *ArchiveFacts, inv *ArchiveInventory) error {
	if facts == nil || inv == nil {
		return fmt.Errorf("archive facts and inventory cannot be nil")
	}

	var doc CDXDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing CycloneDX JSON: %w", err)
	}

	if err := validateCDXHeader(&doc, facts); err != nil {
		return err
	}

	compByName, knownRefs, err := validateCDXComponents(&doc, facts, inv)
	if err != nil {
		return err
	}

	return validateCDXGraph(&doc, facts, inv, compByName, knownRefs)
}

func getCDXBinaryRef(binID string, compByName map[string]CDXComponent) string {
	if strings.HasPrefix(binID, "variant:") {
		tier := strings.TrimPrefix(binID, "variant:")
		if c, ok := compByName["microfat-variant-"+tier]; ok {
			return c.BOMRef
		}
	}
	if c, ok := compByName[binID]; ok {
		return c.BOMRef
	}
	return ""
}

func verifyCDXHash(c CDXComponent, expectedSHA256 string) error {
	for _, h := range c.Hashes {
		if strings.EqualFold(h.Alg, "SHA-256") || strings.EqualFold(h.Alg, "SHA256") {
			if strings.EqualFold(h.Content, expectedSHA256) {
				return nil
			}
			return fmt.Errorf("expected %s, got %s", expectedSHA256, h.Content)
		}
	}
	return fmt.Errorf("no SHA-256 hash found on component %s", c.Name)
}
