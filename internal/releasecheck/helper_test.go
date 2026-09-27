package releasecheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstallerInventoryVersionBoundary(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		version string
		helpers bool
	}{
		{"0.2.3", false}, {"0.2.4", false}, {"0.2.5", false}, {"0.2.6", false},
		{"0.3.0", true}, {"0.3.0-SNAPSHOT-aabbcc", true}, {"1.0.0", true}, {"unknown", true},
	} {
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()
			contract, err := NewReleaseContract(test.version)
			require.NoError(t, err)
			count := 6
			if test.helpers {
				count = 12
			}
			assert.Len(t, contract.ExpectedPayloadNames, count)
			for _, arch := range []string{ArchAMD64, ArchARM64} {
				name := ReleaseInstaller + "_" + test.version + "_linux_" + arch
				identity, err := ParseReleaseArtifactName(name)
				if test.helpers {
					require.NoError(t, err)
					assert.Equal(t, NativeHelper, identity.Kind)
					assert.Equal(t, arch, identity.Arch)
				} else {
					require.Error(t, err)
				}
			}
		})
	}
	for _, name := range []string{"microfat-install__linux_amd64", "microfat-install_0.3.0_linux_riscv64",
		"microfat-install_0.3.0_linux_amd64.tar.gz", "unknown"} {
		_, err := ParseReleaseArtifactName(name)
		require.Error(t, err)
	}
}

func TestNativeHelperArtifactValidation(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{ArchAMD64, ArchARM64} {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), ReleaseInstaller+"_0.3.0_linux_"+arch)
			cmd := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-o", path, "../../cmd/microfat-install")
			cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0", "GOAMD64=v1", "GOARM64=v8.0")
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			facts, err := ValidateArtifact(path)
			require.NoError(t, err)
			assert.Equal(t, NativeHelper, facts.Kind)
			assert.Equal(t, arch, facts.TargetArch)
			assert.Empty(t, facts.EmbeddedVariants)
			inv, err := ExtractArchiveInventory(facts)
			require.NoError(t, err)
			require.Len(t, inv.Binaries, 1)
			assert.NotEmpty(t, inv.Binaries[ReleaseInstaller].Dependencies)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			other := ArchARM64
			if arch == ArchARM64 {
				other = ArchAMD64
			}
			_, err = helperFacts(data, filepath.Base(path), other)
			require.Error(t, err)
			require.NoError(t, os.Chmod(path, 0o600))
			_, err = ValidateArtifact(path)
			require.NoError(t, err, "downloaded raw assets need not be executable before validation")
			link := filepath.Join(t.TempDir(), filepath.Base(path))
			require.NoError(t, os.Symlink(path, link))
			_, err = ValidateArtifact(link)
			require.Error(t, err)
			require.NoError(t, os.WriteFile(path, []byte("corrupt"), 0o755))
			require.NoError(t, os.Chmod(path, 0o755))
			_, err = ValidateArtifact(path)
			require.Error(t, err)
			require.NoError(t, os.Remove(path))
			_, err = ValidateArtifact(path)
			require.Error(t, err)
		})
	}
}
