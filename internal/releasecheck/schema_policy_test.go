package releasecheck

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaPolicy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		version string
		modern  bool
	}{
		{"v0.2.3", false}, {"0.2.4", false}, {"0.2.4+build.1", false}, {"0.2.4-rc.1", false},
		{"0.2.5", true}, {"v0.2.5-SNAPSHOT-abcdef", true}, {"0.2.5+build.1", true},
		{"0.3.0", true}, {"1.0.0", true}, {"", true}, {"unknown", true}, {"0.2", true},
		{"0.bad.4", true}, {"0.-2.4", true}, {"9999999999999999999999999.0.0", true},
	} {
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.modern, UsesModernSBOM(test.version))
		})
	}
}

func TestModernReleaseRejectsLegacySchemas(t *testing.T) {
	t.Parallel()
	facts := &ArchiveFacts{ArchiveName: "microfat_0.2.5_linux_amd64.tar.gz"}
	inv := &ArchiveInventory{}
	cdx := []byte(`{"bomFormat":"CycloneDX","specVersion":"1.5"}`)
	require.ErrorContains(t, ValidateCycloneDXBytes(cdx, facts, inv), "1.7")
	require.ErrorContains(t, ValidateSPDXBytes([]byte(`{"spdxVersion":"SPDX-2.3"}`), facts, inv), "3.0.1")
}
