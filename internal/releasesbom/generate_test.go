package releasesbom

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/EpicBlackWolfZ/microfat/internal/sbom"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireConverter(t *testing.T) {
	t.Helper()
	_, err := exec.LookPath("cdx-convert")
	if err != nil && os.Getenv("MICROFAT_SBOM_TESTS") != "required" {
		t.Skip("install pinned cdx-convert; CI sets MICROFAT_SBOM_TESTS=required")
	}
	require.NoError(t, err)
}

func TestGenerateRealVariantDependencyMatrix(t *testing.T) {
	t.Parallel()
	requireConverter(t)
	for _, arch := range []string{releasecheck.ArchAMD64, "arm64"} {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()
			archive := buildArchiveFixture(t, arch)
			meta := Metadata{Version: "0.2.5", Commit: "fixture", Created: time.Now()}
			for _, kind := range []string{FormatCycloneDX, FormatSPDX} {
				t.Run(kind, func(t *testing.T) {
					data, err := Generate(t.Context(), archive, kind, meta, Convert)
					require.NoError(t, err)
					var catalog *sbom.Catalog
					if kind == FormatSPDX {
						graph, err := sbom.ReadSPDX(data)
						require.NoError(t, err)
						catalog, err = graph.Catalog()
						require.NoError(t, err)
					} else {
						catalog, err = sbom.ReadCycloneDX(data)
						require.NoError(t, err)
					}
					versions := map[string]bool{}
					for _, component := range catalog.Components {
						props, err := sbom.Properties(component)
						require.NoError(t, err)
						if props["microfat:module:path"] == "example.com/dependency" {
							versions[props["microfat:module:version"]] = true
							assert.Equal(t, "../dependency", props["microfat:module:replace_path"])
							assert.Empty(t, component.PackageURL)
						}
					}
					assert.Equal(t, map[string]bool{fixtureModuleVersion: true, "v1.1.0": true}, versions)
				})
			}
			_, err := Generate(t.Context(), archive, FormatSPDX, meta, func(context.Context, []byte) ([]byte, error) {
				return nil, errors.New("injected converter failure")
			})
			require.ErrorContains(t, err, "injected converter failure")
			_, err = Generate(t.Context(), archive, FormatSPDX, meta, func(context.Context, []byte) ([]byte, error) {
				return []byte(`{"spdxVersion":"SPDX-2.3"}`), nil
			})
			require.Error(t, err, "legacy or lossy converter output must not publish")
		})
	}
}

func buildArchiveFixture(t *testing.T, arch string) string {
	t.Helper()
	dir := t.TempDir()
	app, dep := filepath.Join(dir, "app"), filepath.Join(dir, "dependency")
	require.NoError(t, os.MkdirAll(app, 0o700))
	require.NoError(t, os.MkdirAll(dep, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dep, "go.mod"), []byte("module example.com/dependency\n\ngo 1.27.1\n"), 0o600))
	dependencySource := "package dependency\nfunc Value() string { return \"fixture\" }\n"
	applicationSource := "package main\nimport \"example.com/dependency\"\nfunc main(){ println(dependency.Value()) }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dep, "dep.go"), []byte(dependencySource), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(app, "main.go"), []byte(applicationSource), 0o600))
	contract, err := releasecheck.NewReleaseContract("0.2.5")
	require.NoError(t, err)
	payloads := map[string][]byte{}
	for index, tier := range contract.ExpectedTiers[arch] {
		version := fixtureModuleVersion
		if index == 1 {
			version = "v1.1.0"
		}
		mod := "module example.com/app\n\ngo 1.27.1\n\nrequire example.com/dependency " + version +
			"\nreplace example.com/dependency => ../dependency\n"
		require.NoError(t, os.WriteFile(filepath.Join(app, "go.mod"), []byte(mod), 0o600))
		binary := filepath.Join(dir, "payload-"+tier)
		cmd := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-ldflags=-s -w", "-o", binary, ".")
		cmd.Dir = app
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0", "GOAMD64="+tier)
		if arch == "arm64" {
			cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOARM64="+tier)
		}
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		payloads[tier], err = os.ReadFile(binary)
		require.NoError(t, err)
	}
	baseline := payloads[contract.ExpectedTiers[arch][0]]
	var fat bytes.Buffer
	fat.Write(baseline)
	index := &format.Index{Version: format.FormatVersion2, TargetArch: arch}
	for _, tier := range contract.ExpectedTiers[arch] {
		payload := payloads[tier]
		hash := sha256.Sum256(payload)
		index.Variants = append(index.Variants, format.VariantEntry{Level: tier, Offset: int64(fat.Len()),
			CompressedSize: int64(len(payload)), UncompressedSize: int64(len(payload)),
			Compression: "none", SHA256: hex.EncodeToString(hash[:])})
		fat.Write(payload)
	}
	_, err = format.WriteIndexAndTrailer(&fat, index, int64(fat.Len()))
	require.NoError(t, err)
	archive := filepath.Join(dir, contract.ExpectedArchives[arch])
	writeArchiveFixture(t, archive, map[string][]byte{"microfat": fat.Bytes(), "microfat-stub": baseline,
		"microfat-stub-minimal": baseline, "LICENSE": []byte("Fixture project license\n")})
	return archive
}

func writeArchiveFixture(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	file, err := os.Create(path)
	require.NoError(t, err)
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range sortedKeys(files) {
		data := files[name]
		require.NoError(t, tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}))
		_, err := tarWriter.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	require.NoError(t, file.Close())
}

func TestConverterProcessFailureAndOutputBounds(t *testing.T) {
	t.Parallel()
	for name, run := range map[string]processRunner{
		"exit_failure": func(context.Context, process.Spec) ([]byte, []byte, error) {
			return nil, []byte("diagnostic"), errors.New("failure")
		},
		"missing_output": func(context.Context, process.Spec) ([]byte, []byte, error) { return nil, nil, nil },
		"symlink_output": func(_ context.Context, s process.Spec) ([]byte, []byte, error) {
			return nil, nil, os.Symlink(s.Args[1], s.Args[3])
		},
		"oversized_output": func(_ context.Context, s process.Spec) ([]byte, []byte, error) {
			f, err := os.Create(s.Args[3])
			if err != nil {
				return nil, nil, err
			}
			err = f.Truncate(sbom.MaxDocumentBytes + 1)
			return nil, nil, errors.Join(err, f.Close())
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := runConverter(t.Context(), "converter", []byte("{}"), run)
			require.Error(t, err)
		})
	}
	data, err := runConverter(t.Context(), "converter", []byte("{}"), func(_ context.Context, s process.Spec) ([]byte, []byte, error) {
		return nil, nil, os.WriteFile(s.Args[3], []byte("converted"), 0o600)
	})
	require.NoError(t, err)
	assert.Equal(t, "converted", string(data))
	_, err = runConverter(t.Context(), "converter", make([]byte, sbom.MaxDocumentBytes+1), nil)
	require.Error(t, err)
}

func TestRejectUnpinnedConvertersAndInvalidInputs(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "untrusted")
	require.NoError(t, os.WriteFile(file, []byte("untrusted"), 0o700))
	for _, test := range []struct{ system, arch, path string }{
		{"darwin", releasecheck.ArchAMD64, file}, {"linux", "riscv64", file},
		{"linux", releasecheck.ArchAMD64, file}, {"linux", "arm64", file}, {"linux", releasecheck.ArchAMD64, file + "-missing"},
	} {
		require.Error(t, verifyConverter(test.path, test.system, test.arch))
	}
	_, err := Generate(t.Context(), "missing", "unsupported", Metadata{}, nil)
	require.ErrorContains(t, err, "unsupported")
	_, err = Generate(t.Context(), "bad-name", FormatSPDX, Metadata{}, nil)
	require.Error(t, err)
	_, err = Generate(t.Context(), filepath.Join(t.TempDir(), "microfat_0.2.5_linux_amd64.tar.gz"), FormatSPDX, Metadata{}, nil)
	require.Error(t, err)
	_, err = convertDocument(t.Context(), nil, nil)
	require.Error(t, err)
	_, err = convertDocument(t.Context(), []byte("invalid"), Convert)
	require.Error(t, err)
}
