package builder

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/install"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

func managedFixture(t *testing.T, paths install.Paths, version string, files map[string]string) string {
	t.Helper()
	source := t.TempDir()
	generation := install.Generation{Schema: install.SchemaVersion, ID: install.NewID(), Version: version, Arch: runtime.GOARCH,
		ArchiveSHA256: strings.Repeat("a", 64), Files: map[string]install.File{}}
	for _, name := range install.Products() {
		data, err := os.ReadFile(files[name])
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(source, name), data, 0o755))
		generation.Files[name] = install.File{Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	}
	snapshot, err := install.Inspect(paths)
	require.NoError(t, err)
	_, err = install.Apply(t.Context(), snapshot, generation, source, install.ApplyOptions{})
	require.NoError(t, err)
	return filepath.Join(paths.Store, "generations", generation.ID)
}

func TestManagedCompanionsBindToOldGeneration(t *testing.T) {
	// Existing origin hooks are process-global; deliberately not parallel.
	root := setupOriginMocks(t)
	paths := install.Paths{Bin: filepath.Join(root, "bin with space"), Store: filepath.Join(root, "store with space")}
	payload := createDummyELF(t, root, "payload", runtime.GOARCH)
	stub := createDummyELF(t, root, "stub", runtime.GOARCH)
	fat := filepath.Join(root, "fat")
	level := "v1"
	if runtime.GOARCH == "arm64" {
		level = "v8.0"
	}
	opts := pack.DefaultOptions()
	opts.StubPath, opts.OutputPath = stub, fat
	opts.Variants = map[string]string{level: payload}
	opts.SkipELFValidation = true
	_, err := pack.Pack(opts)
	require.NoError(t, err)
	products := map[string]string{"microfat": fat, StubBinaryFull: stub, StubBinaryMinimal: stub}
	old := managedFixture(t, paths, "0.3.0", products)
	// The fat executable and selected payload are byte-identical. Only the
	// physical generation can bind this old process after current changes.
	newDir := managedFixture(t, paths, "0.3.1", products)
	assert.NotEqual(t, old, newDir)
	data, err := os.ReadFile(payload)
	require.NoError(t, err)
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	readAndHashSelfExe = func() (int64, string, error) { return int64(len(data)), digest, nil }
	t.Setenv(format.EnvSelectedVariant, level)
	t.Setenv(format.EnvSelectedSize, strconv.Itoa(len(data)))
	t.Setenv(format.EnvSelectedSHA256, digest)
	t.Setenv(format.EnvOriginalExe, filepath.Join(old, "microfat"))
	t.Setenv("PATH", paths.Bin)
	for _, mode := range []string{"native", format.ExecModeMemfd, format.ExecModeCache} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(format.EnvExecMode, mode)
			osExecutableFunc = func() (string, error) { return filepath.Join(old, "microfat"), nil }
			readlinkProcSelfExe = osExecutableFunc
			if mode == format.ExecModeMemfd {
				readlinkProcSelfExe = func() (string, error) { return mockMemfdTarget, nil }
			}
			if mode == format.ExecModeCache {
				cache := filepath.Join(root, "cache")
				t.Setenv(format.EnvCacheDir, cache)
				readlinkProcSelfExe = func() (string, error) { return filepath.Join(cache, "payload"), nil }
			}
			for _, profile := range []string{StubProfileFull, StubProfileMinimal} {
				resolved, err := ResolveStubWithOptions(ResolveStubOptions{CLIProfile: profile, TargetArch: runtime.GOARCH})
				require.NoError(t, err)
				name, err := StubCompanionName(profile)
				require.NoError(t, err)
				assert.Equal(t, filepath.Join(old, name), resolved)
			}
			if mode != "native" {
				t.Setenv(format.EnvOriginalExe, filepath.Join(paths.Bin, "microfat"))
				_, err := ResolveStubWithOptions(ResolveStubOptions{TargetArch: runtime.GOARCH})
				require.ErrorIs(t, err, install.ErrDiscovery, "same payload hash cannot authorize a moving hint")
			}
		})
	}
	readlinkProcSelfExe = func() (string, error) { return mockMemfdTarget, nil }
	t.Setenv(format.EnvExecMode, format.ExecModeMemfd)
	for _, scenario := range []string{"bad-payload", "deleted-generation"} {
		t.Run(scenario, func(t *testing.T) {
			if scenario == "bad-payload" {
				t.Setenv(format.EnvSelectedSHA256, strings.Repeat("0", 64))
			} else {
				require.NoError(t, os.RemoveAll(old))
			}
			_, err := ResolveStubWithOptions(ResolveStubOptions{TargetArch: runtime.GOARCH})
			require.ErrorIs(t, err, install.ErrDiscovery, "never silently use the valid G2 companion on PATH")
		})
	}
}
