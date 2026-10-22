package releasecheck

import (
	"os"
	"strings"
	"testing"

	cdx "github.com/CycloneDX/cyclonedx-go"
	"github.com/EpicBlackWolfZ/microfat"
	"github.com/EpicBlackWolfZ/microfat/internal/sbom"
	"github.com/stretchr/testify/require"
)

func TestModernMetadataRetainsHistoricalConverter(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../sbom/testdata/archive.cdx.json")
	require.NoError(t, err)
	catalog, err := sbom.ReadCycloneDX(data)
	require.NoError(t, err)
	require.NoError(t, checkModernMetadata(catalog.BOM.Metadata))
}

func TestModernMetadataAuditsRecordedConverterIndependentlyOfCurrentPin(t *testing.T) {
	t.Parallel()
	current, _, _ := strings.Cut(microfat.ToolPin("CDXGEN_LINUX_AMD64"), "@")
	current = strings.TrimPrefix(current, "v")
	for _, tc := range []struct {
		name, converter string
	}{
		{"current", current},
		{"historical", "13.1.0"},
		{"future historical", "13.4.0"},
		{"independent stable version", "999.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, checkModernMetadata(converterMetadata(tc.converter)))
		})
	}
}

func converterMetadata(version string) *cdx.Metadata {
	tools := []cdx.Component{
		{Name: "microfat-release-sbom", Version: "0.3.1"},
		{Name: "cdxgen cdx-convert", Version: version,
			ExternalReferences: &[]cdx.ExternalReference{
				{Type: cdx.ERTypeVCS, URL: "https://github.com/cdxgen/cdxgen/tree/v" + version},
			}},
	}
	return &cdx.Metadata{Timestamp: "2026-10-22T00:00:00Z", Tools: &cdx.ToolsChoice{Components: &tools}}
}

func TestModernMetadataRejectsInvalidConverterIdentity(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*cdx.Metadata){
		"missing generator": func(meta *cdx.Metadata) { (*meta.Tools.Components)[0].Version = "" },
		"missing converter": func(meta *cdx.Metadata) { (*meta.Tools.Components)[1].Version = "" },
		"malformed version": func(meta *cdx.Metadata) { (*meta.Tools.Components)[1].Version = "latest" },
		"prerelease":        func(meta *cdx.Metadata) { (*meta.Tools.Components)[1].Version = "13.4.0-rc.1" },
		"leading zero":      func(meta *cdx.Metadata) { (*meta.Tools.Components)[1].Version = "013.4.0" },
		"missing source":    func(meta *cdx.Metadata) { (*meta.Tools.Components)[1].ExternalReferences = nil },
		"wrong source": func(meta *cdx.Metadata) {
			(*(*meta.Tools.Components)[1].ExternalReferences)[0].URL = "https://github.com/cdxgen/cdxgen/tree/v13.1.0"
		},
		"wrong reference type": func(meta *cdx.Metadata) {
			(*(*meta.Tools.Components)[1].ExternalReferences)[0].Type = cdx.ERTypeWebsite
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			meta := converterMetadata("13.4.0")
			mutate(meta)
			require.Error(t, checkModernMetadata(meta))
		})
	}
}
