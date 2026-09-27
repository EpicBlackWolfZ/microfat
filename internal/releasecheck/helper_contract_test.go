package releasecheck

import (
	"testing"

	cdx "github.com/CycloneDX/cyclonedx-go"
	"github.com/EpicBlackWolfZ/microfat/internal/sbom"
	"github.com/stretchr/testify/require"
)

func TestNativeHelperSBOMRejectsAmbiguousKindAndLicense(t *testing.T) {
	t.Parallel()
	name := "microfat-install_0.3.0_linux_amd64"
	root := cdx.Component{Type: cdx.ComponentTypeFile, Name: name, Version: "0.3.0"}
	facts := &ArchiveFacts{ArchiveName: name}
	_, err := checkModernArchive(root, facts)
	require.ErrorContains(t, err, "explicit artifact kind")
	facts.Kind = NativeHelper
	_, err = checkModernArchive(root, facts)
	require.Error(t, err, "native-helper property is mandatory")
	for _, license := range []cdx.LicenseChoice{
		{License: &cdx.License{ID: "MIT"}},
		{License: &cdx.License{ID: "Apache-2.0", Text: &cdx.AttachedText{Content: "invented extracted text"}}},
		{Expression: "Apache-2.0"},
	} {
		licenses := cdx.Licenses{license}
		catalog := &sbom.Catalog{Components: map[string]cdx.Component{
			"installer-component": {Type: cdx.ComponentTypeApplication, Licenses: &licenses},
		}}
		require.Error(t, checkNativeHelperLicenses(catalog))
	}
	for _, executables := range []map[string]*ExecutableFacts{{}, {ReleaseProjectName: {}}, {ReleaseInstaller: {}, ReleaseFullStub: {}}} {
		facts.Executables = executables
		require.ErrorContains(t, checkModernContainment(&sbom.Catalog{}, nil, facts), "exactly one native helper")
	}
}
