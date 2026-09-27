package releasesbom

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	cdx "github.com/CycloneDX/cyclonedx-go"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeHelperSBOMPipeline(t *testing.T) {
	t.Parallel()
	requireConverter(t)
	for _, arch := range []string{releasecheck.ArchAMD64, releasecheck.ArchARM64} {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "microfat-install_0.3.0_linux_"+arch)
			cmd := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-o", path, "../../cmd/microfat-install")
			cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0", "GOAMD64=v1", "GOARM64=v8.0")
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			facts, err := releasecheck.ValidateArtifact(path)
			require.NoError(t, err)
			inv, err := releasecheck.ExtractArchiveInventory(facts)
			require.NoError(t, err)
			meta := Metadata{Version: "0.3.0", Created: time.Now()}
			for _, format := range []string{FormatCycloneDX, FormatSPDX} {
				data, err := Generate(t.Context(), path, format, meta, Convert)
				require.NoError(t, err)
				if format == FormatSPDX {
					require.NoError(t, releasecheck.ValidateModernSPDXBytes(data, facts, inv))
					continue
				}
				require.NoError(t, releasecheck.ValidateModernCycloneDXBytes(data, facts, inv))
				var bom cdx.BOM
				require.NoError(t, json.Unmarshal(data, &bom))
				assert.Equal(t, filepath.Base(path), bom.Metadata.Component.Name)
				assert.Len(t, *bom.Metadata.Component.Components, 1)
				bom.Metadata.Component.Licenses = nil
				bad, err := json.Marshal(bom)
				require.NoError(t, err)
				require.Error(t, releasecheck.ValidateModernCycloneDXBytes(bad, facts, inv))
			}
		})
	}
}
