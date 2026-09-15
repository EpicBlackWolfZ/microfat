package releasecheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testArchiveName     = "microfat_0.2.3_linux_amd64.tar.gz"
	testArchiveHash     = "1111111111111111111111111111111111111111111111111111111111111111"
	testMicrofatHash    = "2222222222222222222222222222222222222222222222222222222222222222"
	testStubHash        = "3333333333333333333333333333333333333333333333333333333333333333"
	testMinStubHash     = "4444444444444444444444444444444444444444444444444444444444444444"
	testV1Hash          = "5555555555555555555555555555555555555555555555555555555555555555"
	testV2Hash          = "6666666666666666666666666666666666666666666666666666666666666666"
	testPkgForkCompress = "github.com/fork/compress"
	testSPDXArchiveID   = "SPDXRef-Archive"
	testSPDXCobraID     = "SPDXRef-Package-cobra"
	testVersion         = "0.2.3"
	testNoAssertion     = "NOASSERTION"
	testAlgoSHA256      = "SHA256"
	testAlgoHyphen256   = "SHA-256"
	testRelContains     = "CONTAINS"
	testRelDependsOn    = "DEPENDS_ON"
	testAppType         = "application"
	testBOMRefStub      = "bin-stub"
)

func createTestFactsAndInventory() (*ArchiveFacts, *ArchiveInventory) {
	facts := &ArchiveFacts{
		ArchiveName:   testArchiveName,
		ArchiveSHA256: testArchiveHash,
		TargetArch:    ArchAMD64,
		Executables: map[string]*ExecutableFacts{
			ReleaseProjectName: {Name: ReleaseProjectName, SHA256: testMicrofatHash},
			ReleaseFullStub:    {Name: ReleaseFullStub, SHA256: testStubHash},
			ReleaseMinStub:     {Name: ReleaseMinStub, SHA256: testMinStubHash},
		},
		EmbeddedVariants: map[string]*VariantFacts{
			"v1": {Level: "v1", SHA256: testV1Hash},
			"v2": {Level: "v2", SHA256: testV2Hash},
		},
	}

	depSys := ModuleDep{Path: testPkgSys, Version: "v0.48.0"}
	depCobra := ModuleDep{Path: testPkgCobra, Version: "v1.10.2"}
	depCompress := ModuleDep{
		Path:        testPkgCompress,
		Version:     "v1.20.0",
		ReplacePath: testPkgForkCompress,
		ReplaceVer:  "v1.20.1",
	}

	inv := &ArchiveInventory{
		ArchiveName: testArchiveName,
		TargetArch:  ArchAMD64,
		Binaries: map[string]*BinaryInventory{
			ReleaseFullStub: {
				Identifier: ReleaseFullStub,
				BinaryName: ReleaseFullStub,
				Dependencies: map[string]ModuleDep{
					testPkgSys: depSys,
				},
			},
			ReleaseMinStub: {
				Identifier: ReleaseMinStub,
				BinaryName: ReleaseMinStub,
				Dependencies: map[string]ModuleDep{
					testPkgSys: depSys,
				},
			},
			"variant:v1": {
				Identifier:  "variant:v1",
				BinaryName:  ReleaseProjectName,
				VariantTier: "v1",
				Dependencies: map[string]ModuleDep{
					testPkgCobra:    depCobra,
					testPkgCompress: depCompress,
				},
			},
			"variant:v2": {
				Identifier:  "variant:v2",
				BinaryName:  ReleaseProjectName,
				VariantTier: "v2",
				Dependencies: map[string]ModuleDep{
					testPkgCobra:    depCobra,
					testPkgCompress: depCompress,
				},
			},
		},
		AllDependencies: map[string]ModuleDep{
			testPkgSys:      depSys,
			testPkgCobra:    depCobra,
			testPkgCompress: depCompress,
		},
	}

	return facts, inv
}

func createValidSPDXDocument() *SPDXDocument {
	return &SPDXDocument{
		SPDXVersion:       "SPDX-2.3",
		DataLicense:       "CC0-1.0",
		SPDXID:            "SPDXRef-DOCUMENT",
		Name:              testArchiveName,
		DocumentNamespace: "https://github.com/EpicBlackWolfZ/microfat/releases/tag/v0.2.3/" + testArchiveName,
		Packages: []SPDXPackage{
			{
				SPDXID:           testSPDXArchiveID,
				Name:             testArchiveName,
				VersionInfo:      testVersion,
				DownloadLocation: testNoAssertion,
				Checksums: []SPDXChecksum{
					{Algorithm: testAlgoSHA256, ChecksumValue: testArchiveHash},
				},
				Comment: "release_archive;arch=amd64",
			},
			{
				SPDXID:           "SPDXRef-Binary-microfat",
				Name:             ReleaseProjectName,
				VersionInfo:      testVersion,
				DownloadLocation: testNoAssertion,
				Checksums: []SPDXChecksum{
					{Algorithm: testAlgoSHA256, ChecksumValue: testMicrofatHash},
				},
			},
			{
				SPDXID:           "SPDXRef-Binary-microfat-stub",
				Name:             ReleaseFullStub,
				VersionInfo:      testVersion,
				DownloadLocation: testNoAssertion,
				Checksums: []SPDXChecksum{
					{Algorithm: testAlgoSHA256, ChecksumValue: testStubHash},
				},
			},
			{
				SPDXID:           "SPDXRef-Binary-microfat-stub-minimal",
				Name:             ReleaseMinStub,
				VersionInfo:      testVersion,
				DownloadLocation: testNoAssertion,
				Checksums: []SPDXChecksum{
					{Algorithm: testAlgoSHA256, ChecksumValue: testMinStubHash},
				},
			},
			{
				SPDXID:           "SPDXRef-Variant-v1",
				Name:             "microfat-variant-v1",
				VersionInfo:      testVersion,
				DownloadLocation: testNoAssertion,
				Checksums: []SPDXChecksum{
					{Algorithm: testAlgoSHA256, ChecksumValue: testV1Hash},
				},
			},
			{
				SPDXID:           "SPDXRef-Variant-v2",
				Name:             "microfat-variant-v2",
				VersionInfo:      testVersion,
				DownloadLocation: testNoAssertion,
				Checksums: []SPDXChecksum{
					{Algorithm: testAlgoSHA256, ChecksumValue: testV2Hash},
				},
			},
			{
				SPDXID:           "SPDXRef-Package-golang-sys",
				Name:             testPkgSys,
				VersionInfo:      "v0.48.0",
				DownloadLocation: testNoAssertion,
			},
			{
				SPDXID:           testSPDXCobraID,
				Name:             testPkgCobra,
				VersionInfo:      "v1.10.2",
				DownloadLocation: testNoAssertion,
			},
			{
				SPDXID:           "SPDXRef-Package-compress",
				Name:             testPkgForkCompress,
				VersionInfo:      "v1.20.1",
				DownloadLocation: testNoAssertion,
			},
		},
		Relationships: []SPDXRelationship{
			{SPDXElementID: "SPDXRef-DOCUMENT", RelatedSPDXElement: testSPDXArchiveID, RelationshipType: "DESCRIBES"},
			{SPDXElementID: testSPDXArchiveID, RelatedSPDXElement: "SPDXRef-Binary-microfat", RelationshipType: testRelContains},
			{SPDXElementID: testSPDXArchiveID, RelatedSPDXElement: "SPDXRef-Binary-microfat-stub", RelationshipType: testRelContains},
			{SPDXElementID: testSPDXArchiveID, RelatedSPDXElement: "SPDXRef-Binary-microfat-stub-minimal", RelationshipType: testRelContains},
			{SPDXElementID: "SPDXRef-Binary-microfat", RelatedSPDXElement: "SPDXRef-Variant-v1", RelationshipType: testRelContains},
			{SPDXElementID: "SPDXRef-Binary-microfat", RelatedSPDXElement: "SPDXRef-Variant-v2", RelationshipType: testRelContains},
			{
				SPDXElementID:      "SPDXRef-Binary-microfat-stub",
				RelatedSPDXElement: "SPDXRef-Package-golang-sys",
				RelationshipType:   testRelDependsOn,
			},
			{
				SPDXElementID:      "SPDXRef-Binary-microfat-stub-minimal",
				RelatedSPDXElement: "SPDXRef-Package-golang-sys",
				RelationshipType:   testRelDependsOn,
			},
			{SPDXElementID: "SPDXRef-Variant-v1", RelatedSPDXElement: testSPDXCobraID, RelationshipType: testRelDependsOn},
			{SPDXElementID: "SPDXRef-Variant-v1", RelatedSPDXElement: "SPDXRef-Package-compress", RelationshipType: testRelDependsOn},
			{SPDXElementID: "SPDXRef-Variant-v2", RelatedSPDXElement: testSPDXCobraID, RelationshipType: testRelDependsOn},
			{SPDXElementID: "SPDXRef-Variant-v2", RelatedSPDXElement: "SPDXRef-Package-compress", RelationshipType: testRelDependsOn},
		},
	}
}

func createValidCDXDocument() *CDXDocument {
	return &CDXDocument{
		BOMFormat:   "CycloneDX",
		SpecVersion: "1.5",
		Metadata: CDXMetadata{
			Component: &CDXComponent{
				BOMRef:  "archive-root",
				Type:    "file",
				Name:    testArchiveName,
				Version: testVersion,
				Hashes: []CDXHash{
					{Alg: testAlgoHyphen256, Content: testArchiveHash},
				},
				Properties: []CDXProperty{
					{Name: "microfat:target_arch", Value: ArchAMD64},
				},
			},
		},
		Components: []CDXComponent{
			{
				BOMRef:  "bin-microfat",
				Type:    testAppType,
				Name:    ReleaseProjectName,
				Version: testVersion,
				Hashes: []CDXHash{
					{Alg: testAlgoHyphen256, Content: testMicrofatHash},
				},
				Components: []CDXComponent{
					{
						BOMRef:  "var-v1",
						Type:    testAppType,
						Name:    "microfat-variant-v1",
						Version: testVersion,
						Hashes: []CDXHash{
							{Alg: testAlgoHyphen256, Content: testV1Hash},
						},
					},
					{
						BOMRef:  "var-v2",
						Type:    testAppType,
						Name:    "microfat-variant-v2",
						Version: testVersion,
						Hashes: []CDXHash{
							{Alg: testAlgoHyphen256, Content: testV2Hash},
						},
					},
				},
			},
			{
				BOMRef:  testBOMRefStub,
				Type:    testAppType,
				Name:    ReleaseFullStub,
				Version: testVersion,
				Hashes: []CDXHash{
					{Alg: testAlgoHyphen256, Content: testStubHash},
				},
			},
			{
				BOMRef:  "bin-min-stub",
				Type:    testAppType,
				Name:    ReleaseMinStub,
				Version: testVersion,
				Hashes: []CDXHash{
					{Alg: testAlgoHyphen256, Content: testMinStubHash},
				},
			},
			{
				BOMRef:  "mod-sys",
				Type:    "library",
				Name:    testPkgSys,
				Version: "v0.48.0",
			},
			{
				BOMRef:  "mod-cobra",
				Type:    "library",
				Name:    testPkgCobra,
				Version: "v1.10.2",
			},
			{
				BOMRef:  "mod-compress",
				Type:    "library",
				Name:    testPkgForkCompress,
				Version: "v1.20.1",
			},
		},
		Dependencies: []CDXDependency{
			{Ref: "archive-root", DependsOn: []string{"bin-microfat", testBOMRefStub, "bin-min-stub"}},
			{Ref: "bin-microfat", DependsOn: []string{"var-v1", "var-v2"}},
			{Ref: testBOMRefStub, DependsOn: []string{"mod-sys"}},
			{Ref: "bin-min-stub", DependsOn: []string{"mod-sys"}},
			{Ref: "var-v1", DependsOn: []string{"mod-cobra", "mod-compress"}},
			{Ref: "var-v2", DependsOn: []string{"mod-cobra", "mod-compress"}},
		},
	}
}

func TestSPDXValidation_BaselineAndMutations(t *testing.T) {
	t.Parallel()
	facts, inv := createTestFactsAndInventory()

	t.Run("ValidBaseline", func(t *testing.T) {
		doc := createValidSPDXDocument()
		data, err := json.Marshal(doc)
		require.NoError(t, err)
		require.NoError(t, ValidateSPDXBytes(data, facts, inv))
	})

	t.Run("Mutation1_RemoveNonSentinelLinkedDependency", func(t *testing.T) {
		doc := createValidSPDXDocument()
		var filtered []SPDXPackage
		for _, p := range doc.Packages {
			if p.Name != testPkgSys {
				filtered = append(filtered, p)
			}
		}
		doc.Packages = filtered
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required linked module dependency package: "+testPkgSys)
	})

	t.Run("Mutation2_ChangeDependencyVersion", func(t *testing.T) {
		doc := createValidSPDXDocument()
		for i := range doc.Packages {
			if doc.Packages[i].Name == testPkgCobra {
				doc.Packages[i].VersionInfo = "v1.99.9"
			}
		}
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "version mismatch")
	})

	t.Run("Mutation3_AlterReplacement", func(t *testing.T) {
		doc := createValidSPDXDocument()
		for i := range doc.Packages {
			if doc.Packages[i].Name == testPkgForkCompress {
				doc.Packages[i].VersionInfo = "v1.20.99"
			}
		}
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "version mismatch")
	})

	t.Run("Mutation4_RemoveExecutableModuleAssociation", func(t *testing.T) {
		doc := createValidSPDXDocument()
		var filtered []SPDXRelationship
		for _, r := range doc.Relationships {
			if !(r.SPDXElementID == "SPDXRef-Binary-microfat-stub" && r.RelatedSPDXElement == "SPDXRef-Package-golang-sys") {
				filtered = append(filtered, r)
			}
		}
		doc.Relationships = filtered
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing module association relationship")
	})

	t.Run("Mutation5_RemoveTierNode", func(t *testing.T) {
		doc := createValidSPDXDocument()
		var filtered []SPDXPackage
		for _, p := range doc.Packages {
			if p.Name != "microfat-variant-v2" {
				filtered = append(filtered, p)
			}
		}
		doc.Packages = filtered
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing package for embedded variant: microfat-variant-v2")
	})

	t.Run("Mutation6_OrphanedReference", func(t *testing.T) {
		doc := createValidSPDXDocument()
		doc.Relationships = append(doc.Relationships, SPDXRelationship{
			SPDXElementID:      "SPDXRef-NonExistent",
			RelatedSPDXElement: testSPDXCobraID,
			RelationshipType:   testRelDependsOn,
		})
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "orphaned relationship source SPDXID")
	})

	t.Run("Mutation7_DuplicateID", func(t *testing.T) {
		doc := createValidSPDXDocument()
		doc.Packages = append(doc.Packages, SPDXPackage{
			SPDXID:           testSPDXCobraID, // duplicate
			Name:             "duplicate",
			DownloadLocation: testNoAssertion,
		})
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate SPDXID detected")
	})

	t.Run("Mutation8_RemoveArchiveDigest", func(t *testing.T) {
		doc := createValidSPDXDocument()
		for i := range doc.Packages {
			if doc.Packages[i].Name == testArchiveName {
				doc.Packages[i].Checksums = nil
			}
		}
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no SHA-256 checksum found")
	})

	t.Run("Mutation9_ChangeArchiveVersionOrName", func(t *testing.T) {
		doc := createValidSPDXDocument()
		doc.Name = "wrong-archive-name.tar.gz"
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "document name mismatch")
	})

	t.Run("Mutation10_SwapArchitectures", func(t *testing.T) {
		doc := createValidSPDXDocument()
		for i := range doc.Packages {
			if doc.Packages[i].Name == testArchiveName {
				doc.Packages[i].Comment = "release_archive;arch=arm64" // swapped from amd64
			}
		}
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "architecture mismatch in comment")
	})

	t.Run("Mutation11_HeaderOnlyJSON", func(t *testing.T) {
		headerOnly := []byte(`{"spdxVersion":"SPDX-2.3","dataLicense":"CC0-1.0","SPDXID":"SPDXRef-DOCUMENT","name":"` +
			testArchiveName + `"}`)
		err := ValidateSPDXBytes(headerOnly, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing package describing release archive")
	})
}

func TestCycloneDXValidation_BaselineAndMutations(t *testing.T) {
	t.Parallel()
	facts, inv := createTestFactsAndInventory()

	t.Run("ValidBaseline", func(t *testing.T) {
		doc := createValidCDXDocument()
		data, err := json.Marshal(doc)
		require.NoError(t, err)
		require.NoError(t, ValidateCycloneDXBytes(data, facts, inv))
	})

	t.Run("Mutation1_RemoveNonSentinelLinkedDependency", func(t *testing.T) {
		doc := createValidCDXDocument()
		var filtered []CDXComponent
		for _, c := range doc.Components {
			if c.Name != testPkgSys {
				filtered = append(filtered, c)
			}
		}
		doc.Components = filtered
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required linked module component: "+testPkgSys)
	})

	t.Run("Mutation2_ChangeDependencyVersion", func(t *testing.T) {
		doc := createValidCDXDocument()
		for i := range doc.Components {
			if doc.Components[i].Name == testPkgCobra {
				doc.Components[i].Version = "v1.99.9"
			}
		}
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "version mismatch")
	})

	t.Run("Mutation3_AlterReplacement", func(t *testing.T) {
		doc := createValidCDXDocument()
		for i := range doc.Components {
			if doc.Components[i].Name == testPkgForkCompress {
				doc.Components[i].Version = "v1.20.99"
			}
		}
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "version mismatch")
	})

	t.Run("Mutation4_RemoveExecutableModuleAssociation", func(t *testing.T) {
		doc := createValidCDXDocument()
		for i := range doc.Dependencies {
			if doc.Dependencies[i].Ref == testBOMRefStub {
				doc.Dependencies[i].DependsOn = nil
			}
		}
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing module association dependency edge")
	})

	t.Run("Mutation5_RemoveTierNode", func(t *testing.T) {
		doc := createValidCDXDocument()
		for i := range doc.Components {
			if doc.Components[i].Name == ReleaseProjectName {
				var filteredNested []CDXComponent
				for _, nested := range doc.Components[i].Components {
					if nested.Name != "microfat-variant-v2" {
						filteredNested = append(filteredNested, nested)
					}
				}
				doc.Components[i].Components = filteredNested
			}
		}
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing component for embedded variant: microfat-variant-v2")
	})

	t.Run("Mutation6_OrphanedReference", func(t *testing.T) {
		doc := createValidCDXDocument()
		doc.Dependencies = append(doc.Dependencies, CDXDependency{
			Ref:       testBOMRefStub,
			DependsOn: []string{"orphaned-ref-12345"},
		})
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "orphaned reference in dependsOn")
	})

	t.Run("Mutation7_DuplicateID", func(t *testing.T) {
		doc := createValidCDXDocument()
		doc.Components = append(doc.Components, CDXComponent{
			BOMRef: testBOMRefStub, // duplicate
			Type:   testAppType,
			Name:   "duplicate",
		})
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate bom-ref detected")
	})

	t.Run("Mutation8_RemoveArchiveDigest", func(t *testing.T) {
		doc := createValidCDXDocument()
		doc.Metadata.Component.Hashes = nil
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no SHA-256 hash found on component")
	})

	t.Run("Mutation9_ChangeArchiveVersionOrName", func(t *testing.T) {
		doc := createValidCDXDocument()
		doc.Metadata.Component.Name = "wrong-name.tar.gz"
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "root component name mismatch")
	})

	t.Run("Mutation10_SwapArchitectures", func(t *testing.T) {
		doc := createValidCDXDocument()
		doc.Metadata.Component.Properties = []CDXProperty{
			{Name: "microfat:target_arch", Value: ArchARM64}, // swapped
		}
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "archive component target_arch mismatch")
	})

	t.Run("Mutation11_HeaderOnlyJSON", func(t *testing.T) {
		headerOnly := []byte(`{"bomFormat":"CycloneDX","specVersion":"1.5"}`)
		err := ValidateCycloneDXBytes(headerOnly, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing root metadata.component")
	})
}

func TestSBOM_RepeatedSharedModulesAcrossVariants(t *testing.T) {
	t.Parallel()
	facts, inv := createTestFactsAndInventory()

	// Both SPDX and CycloneDX should pass with multiple variants referencing the same module
	spdxDoc := createValidSPDXDocument()
	spdxData, err := json.Marshal(spdxDoc)
	require.NoError(t, err)
	require.NoError(t, ValidateSPDXBytes(spdxData, facts, inv))

	cdxDoc := createValidCDXDocument()
	cdxData, err := json.Marshal(cdxDoc)
	require.NoError(t, err)
	require.NoError(t, ValidateCycloneDXBytes(cdxData, facts, inv))
}

func TestValidateSBOM_FilePathsAndErrors(t *testing.T) {
	t.Parallel()
	facts, inv := createTestFactsAndInventory()
	tempDir := t.TempDir()

	t.Run("ValidateSPDX_FileSuccess", func(t *testing.T) {
		doc := createValidSPDXDocument()
		data, _ := json.Marshal(doc)
		p := filepath.Join(tempDir, "test.spdx.json")
		require.NoError(t, os.WriteFile(p, data, 0o644))
		require.NoError(t, ValidateSPDX(p, facts, inv))
	})

	t.Run("ValidateSPDX_MissingFile", func(t *testing.T) {
		err := ValidateSPDX(filepath.Join(tempDir, "missing.spdx.json"), facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading SPDX document")
	})

	t.Run("ValidateCycloneDX_FileSuccess", func(t *testing.T) {
		doc := createValidCDXDocument()
		data, _ := json.Marshal(doc)
		p := filepath.Join(tempDir, "test.cyclonedx.json")
		require.NoError(t, os.WriteFile(p, data, 0o644))
		require.NoError(t, ValidateCycloneDX(p, facts, inv))
	})

	t.Run("ValidateCycloneDX_MissingFile", func(t *testing.T) {
		err := ValidateCycloneDX(filepath.Join(tempDir, "missing.cyclonedx.json"), facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading CycloneDX document")
	})

	t.Run("NilFactsOrInventory", func(t *testing.T) {
		require.Error(t, ValidateSPDXBytes([]byte(`{}`), nil, inv))
		require.Error(t, ValidateSPDXBytes([]byte(`{}`), facts, nil))
		require.Error(t, ValidateCycloneDXBytes([]byte(`{}`), nil, inv))
		require.Error(t, ValidateCycloneDXBytes([]byte(`{}`), facts, nil))
	})
}

func TestValidateSPDX_HeaderMutations(t *testing.T) {
	t.Parallel()
	facts, inv := createTestFactsAndInventory()

	t.Run("InvalidSPDXVersion", func(t *testing.T) {
		doc := createValidSPDXDocument()
		doc.SPDXVersion = "SPDX-2.2"
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid or unsupported SPDX version")
	})

	t.Run("InvalidDataLicense", func(t *testing.T) {
		doc := createValidSPDXDocument()
		doc.DataLicense = "MIT"
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid SPDX data license")
	})

	t.Run("InvalidSPDXID", func(t *testing.T) {
		doc := createValidSPDXDocument()
		doc.SPDXID = "SPDXRef-OTHER"
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid SPDX root document ID")
	})

	t.Run("NameMismatch", func(t *testing.T) {
		doc := createValidSPDXDocument()
		doc.Name = "wrong_name.tar.gz"
		data, _ := json.Marshal(doc)
		err := ValidateSPDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SPDX document name mismatch")
	})
}

func TestValidateCycloneDX_HeaderMutations(t *testing.T) {
	t.Parallel()
	facts, inv := createTestFactsAndInventory()

	t.Run("InvalidBOMFormat", func(t *testing.T) {
		doc := createValidCDXDocument()
		doc.BOMFormat = "InvalidFormat"
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid bomFormat")
	})

	t.Run("MissingSpecVersion", func(t *testing.T) {
		doc := createValidCDXDocument()
		doc.SpecVersion = ""
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing specVersion in CycloneDX document")
	})

	t.Run("MissingRootComponent", func(t *testing.T) {
		doc := createValidCDXDocument()
		doc.Metadata.Component = nil
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing root metadata.component")
	})

	t.Run("RootComponentNameMismatch", func(t *testing.T) {
		doc := createValidCDXDocument()
		doc.Metadata.Component.Name = "mismatch.tar.gz"
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "root component name mismatch")
	})

	t.Run("RootComponentHashMismatch", func(t *testing.T) {
		doc := createValidCDXDocument()
		doc.Metadata.Component.Hashes[0].Content = "0000000000000000000000000000000000000000000000000000000000000000"
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "archive metadata component hash")
	})

	t.Run("RootComponentArchMismatch", func(t *testing.T) {
		doc := createValidCDXDocument()
		doc.Metadata.Component.Properties = []CDXProperty{
			{Name: "microfat:target_arch", Value: "arm64"},
		}
		data, _ := json.Marshal(doc)
		err := ValidateCycloneDXBytes(data, facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "archive component target_arch mismatch")
	})
}
