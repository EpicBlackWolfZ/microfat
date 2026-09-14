package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	flagOutput     = "--output"
	binMicrofat    = "microfat"
	binStub        = "microfat-stub"
	binStubMinimal = "microfat-stub-minimal"
	testAppTarGz   = "dist/app.tar.gz"
	fmtSPDXJSON    = "spdx-json"
	fmtCycloneDX   = "cyclonedx-json"
	testAppSPDX    = "dist/app.spdx.json"
	testArchAMD64   = "amd64"
	compressionNone = "none"
)

func TestParseArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		args           []string
		wantArchive    string
		wantOutput     string
		wantFormat     string
		wantErrContain string
	}{
		{
			name:        "GoReleaserStyle_SPDX",
			args:        []string{testAppTarGz, flagOutput, "spdx-json=dist/app.tar.gz.spdx.json"},
			wantArchive: testAppTarGz,
			wantOutput:  "dist/app.tar.gz.spdx.json",
			wantFormat:  fmtSPDXJSON,
		},
		{
			name:        "GoReleaserStyle_CycloneDX",
			args:        []string{testAppTarGz, flagOutput, "cyclonedx-json=dist/app.tar.gz.cyclonedx.json"},
			wantArchive: testAppTarGz,
			wantOutput:  "dist/app.tar.gz.cyclonedx.json",
			wantFormat:  fmtCycloneDX,
		},
		{
			name:        "ExplicitFlags",
			args:        []string{"--archive", testAppTarGz, flagOutput, testAppSPDX, "--format", fmtSPDXJSON},
			wantArchive: testAppTarGz,
			wantOutput:  testAppSPDX,
			wantFormat:  fmtSPDXJSON,
		},
		{
			name:        "InferredFormat_SPDX",
			args:        []string{testAppTarGz, flagOutput, testAppSPDX},
			wantArchive: testAppTarGz,
			wantOutput:  testAppSPDX,
			wantFormat:  fmtSPDXJSON,
		},
		{
			name:        "InferredFormat_CycloneDX",
			args:        []string{testAppTarGz, flagOutput, "dist/app.cyclonedx.json"},
			wantArchive: testAppTarGz,
			wantOutput:  "dist/app.cyclonedx.json",
			wantFormat:  fmtCycloneDX,
		},
		{
			name:        "PrefixFlags_DoubleHyphen",
			args:        []string{"--archive=" + testAppTarGz, "--output=" + testAppSPDX, "--format=" + fmtSPDXJSON},
			wantArchive: testAppTarGz,
			wantOutput:  testAppSPDX,
			wantFormat:  fmtSPDXJSON,
		},
		{
			name:        "PrefixFlags_SingleHyphen",
			args:        []string{"-archive=" + testAppTarGz, "-output=dist/app.cyclonedx.json", "-format=" + fmtCycloneDX},
			wantArchive: testAppTarGz,
			wantOutput:  "dist/app.cyclonedx.json",
			wantFormat:  fmtCycloneDX,
		},
		{
			name:           "MissingArchive",
			args:           []string{flagOutput, "out.spdx.json"},
			wantErrContain: "missing archive path",
		},
		{
			name:           "MissingOutput",
			args:           []string{testAppTarGz},
			wantErrContain: "missing output path",
		},
		{
			name:           "UninferredFormat",
			args:           []string{testAppTarGz, flagOutput, "dist/app.custom.txt"},
			wantErrContain: "unable to determine format",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			archive, output, formatName, err := parseArgs(tc.args)
			if tc.wantErrContain != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrContain)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantArchive, archive)
			assert.Equal(t, tc.wantOutput, output)
			assert.Equal(t, tc.wantFormat, formatName)
		})
	}
}

// createTestTarArchive creates a minimal valid tar.gz archive with specified files.
func createTestTarArchive(t *testing.T, archivePath string, files map[string][]byte, modes map[string]int64) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(archivePath), 0o755))
	f, err := os.Create(archivePath)
	require.NoError(t, err)
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	for name, content := range files {
		mode := int64(0o644)
		if m, ok := modes[name]; ok {
			mode = m
		}
		hdr := &tar.Header{
			Name:     name,
			Mode:     mode,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write(content)
		require.NoError(t, err)
	}
}

func TestExtractArchiveSafely_PathTraversal(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "traversal.tar.gz")

	// Create archive with path traversal entry
	f, err := os.Create(archivePath)
	require.NoError(t, err)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name:     "../escape.txt",
		Mode:     0o644,
		Size:     4,
		Typeflag: tar.TypeReg,
	}
	require.NoError(t, tw.WriteHeader(hdr))
	_, _ = tw.Write([]byte("evil"))
	_ = tw.Close()
	_ = gw.Close()
	_ = f.Close()

	extractDir := filepath.Join(tempDir, "out")
	require.NoError(t, os.MkdirAll(extractDir, 0o755))
	err = extractArchiveSafely(archivePath, extractDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "illegal relative or absolute path")
}

func TestExtractArchiveSafely_UnsafeTypeRejection(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "symlink.tar.gz")

	f, err := os.Create(archivePath)
	require.NoError(t, err)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name:     "symlink_target",
		Linkname: "/etc/passwd",
		Typeflag: tar.TypeSymlink,
	}
	require.NoError(t, tw.WriteHeader(hdr))
	_ = tw.Close()
	_ = gw.Close()
	_ = f.Close()

	extractDir := filepath.Join(tempDir, "out")
	require.NoError(t, os.MkdirAll(extractDir, 0o755))
	err = extractArchiveSafely(archivePath, extractDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to extract unsafe archive entry")
}

func TestExtractArchiveSafely_ValidDirAndGzipError(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	// Not a gzip file
	corruptGzip := filepath.Join(tempDir, "corrupt.tar.gz")
	require.NoError(t, os.WriteFile(corruptGzip, []byte("not-gzip-data"), 0o644))
	err := extractArchiveSafely(corruptGzip, filepath.Join(tempDir, "out"))
	require.Error(t, err)

	// Valid archive with directory header
	validArchive := filepath.Join(tempDir, "dir_archive.tar.gz")
	f, err := os.Create(validArchive)
	require.NoError(t, err)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "subdir",
		Mode:     0o755,
		Typeflag: tar.TypeDir,
	}))
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "subdir/file.txt",
		Mode:     0o644,
		Size:     4,
		Typeflag: tar.TypeReg,
	}))
	_, _ = tw.Write([]byte("data"))
	_ = tw.Close()
	_ = gw.Close()
	_ = f.Close()

	outDir := filepath.Join(tempDir, "valid_out")
	require.NoError(t, extractArchiveSafely(validArchive, outDir))
	readBack, err := os.ReadFile(filepath.Join(outDir, "subdir", "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, []byte("data"), readBack)
}

func TestGenerate_MutationsAndErrors(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	t.Run("NonExistentArchive", func(t *testing.T) {
		err := Generate(filepath.Join(tempDir, "non-existent.tar.gz"), filepath.Join(tempDir, "out.spdx.json"), "spdx-json")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "opening archive")
	})

	t.Run("MissingRequiredExecutable", func(t *testing.T) {
		archivePath := filepath.Join(tempDir, "incomplete.tar.gz")
		createTestTarArchive(t, archivePath, map[string][]byte{
			binMicrofat: []byte("bin"),
			// missing microfat-stub and microfat-stub-minimal
		}, map[string]int64{
			binMicrofat: 0o755,
		})

		err := Generate(archivePath, filepath.Join(tempDir, "out.spdx.json"), "spdx-json")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "verifying required archive executable")
	})

	t.Run("CorruptedFatBinaryPayload", func(t *testing.T) {
		// Valid archive structure but corrupted trailer/index on microfat binary
		archivePath := filepath.Join(tempDir, "corrupted_fat.tar.gz")
		createTestTarArchive(t, archivePath, map[string][]byte{
			binMicrofat:    []byte("not a real fat binary"),
			binStub:        []byte("stub"),
			binStubMinimal: []byte("min-stub"),
		}, map[string]int64{
			binMicrofat:    0o755,
			binStub:        0o755,
			binStubMinimal: 0o755,
		})

		err := Generate(archivePath, filepath.Join(tempDir, "out.spdx.json"), "spdx-json")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "extracting fat binary variants")
	})

	t.Run("VariantChecksumMismatch", func(t *testing.T) {
		// Construct fat binary with tampered variant payload
		stubBytes := []byte("\x7fELF\x02\x01\x01\x00" + strings.Repeat("\x00", 256))
		variantPayload := []byte("original payload")
		c, err := codec.Get(compressionNone)
		require.NoError(t, err)

		var compBuf bytes.Buffer
		require.NoError(t, c.Compress(&compBuf, variantPayload, "fastest"))

		idx := &format.Index{
			Version:    format.FormatVersion2,
			TargetArch: testArchAMD64,
			Variants: []format.VariantEntry{
				{
					Level:            "v1",
					Offset:           int64(len(stubBytes)),
					CompressedSize:   int64(compBuf.Len()),
					UncompressedSize: int64(len(variantPayload)),
					SHA256:           strings.Repeat("0", 64), // Mismatched SHA256
					Compression:      compressionNone,
				},
			},
		}
		var fatBuf bytes.Buffer
		fatBuf.Write(stubBytes)
		fatBuf.Write(compBuf.Bytes())
		_, err = format.WriteIndexAndTrailer(&fatBuf, idx, int64(len(stubBytes)+compBuf.Len()))
		require.NoError(t, err)

		archivePath := filepath.Join(tempDir, "tampered_variant.tar.gz")
		createTestTarArchive(t, archivePath, map[string][]byte{
			binMicrofat:    fatBuf.Bytes(),
			binStub:        stubBytes,
			binStubMinimal: stubBytes,
		}, map[string]int64{
			binMicrofat:    0o755,
			binStub:        0o755,
			binStubMinimal: 0o755,
		})

		err = Generate(archivePath, filepath.Join(tempDir, "out.spdx.json"), "spdx-json")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "checksum mismatch for variant")
	})
}

func TestGenerate_SnapshotArchiveVerification(t *testing.T) {
	// Look for snapshot archives in dist
	repoRoot, err := filepath.Abs("../../..")
	require.NoError(t, err)
	distDir := filepath.Join(repoRoot, "dist")

	archives, globErr := filepath.Glob(filepath.Join(distDir, "microfat_*_linux_amd64.tar.gz"))
	if globErr != nil || len(archives) == 0 {
		t.Skip("No snapshot amd64 archive found in dist; run goreleaser snapshot first")
	}

	targetArchive := archives[0]
	tempDir := t.TempDir()

	t.Run("Generate_SPDX", func(t *testing.T) {
		spdxOut := filepath.Join(tempDir, "test.spdx.json")
		require.NoError(t, Generate(targetArchive, spdxOut, "spdx-json"))

		data, err := os.ReadFile(spdxOut)
		require.NoError(t, err)

		var spdx map[string]any
		require.NoError(t, json.Unmarshal(data, &spdx))

		assert.Equal(t, "SPDX-2.3", spdx["spdxVersion"])
		assert.Equal(t, filepath.Base(targetArchive), spdx["name"])

		packages, ok := spdx["packages"].([]any)
		require.True(t, ok)
		pkgNames := make(map[string]bool)
		for _, p := range packages {
			if pm, ok := p.(map[string]any); ok {
				if n, ok := pm["name"].(string); ok {
					pkgNames[n] = true
				}
			}
		}

		assert.True(t, pkgNames["github.com/spf13/cobra"], "SPDX must contain cobra from payload CLI")
		assert.True(t, pkgNames["github.com/EpicBlackWolfZ/microfat"], "SPDX must contain microfat root module")
		assert.True(t, pkgNames["github.com/klauspost/compress"], "SPDX must contain klauspost/compress")
	})

	t.Run("Generate_CycloneDX", func(t *testing.T) {
		cdxOut := filepath.Join(tempDir, "test.cyclonedx.json")
		require.NoError(t, Generate(targetArchive, cdxOut, "cyclonedx-json"))

		data, err := os.ReadFile(cdxOut)
		require.NoError(t, err)

		var cdx map[string]any
		require.NoError(t, json.Unmarshal(data, &cdx))

		assert.Equal(t, "CycloneDX", cdx["bomFormat"])
		meta, ok := cdx["metadata"].(map[string]any)
		require.True(t, ok)
		comp, ok := meta["component"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, filepath.Base(targetArchive), comp["name"])

		components, ok := cdx["components"].([]any)
		require.True(t, ok)
		compNames := make(map[string]bool)
		for _, c := range components {
			if cm, ok := c.(map[string]any); ok {
				if n, ok := cm["name"].(string); ok {
					compNames[n] = true
				}
			}
		}

		assert.True(t, compNames["github.com/spf13/cobra"], "CycloneDX must contain cobra from payload CLI")
		assert.True(t, compNames["github.com/EpicBlackWolfZ/microfat"], "CycloneDX must contain microfat root module")
	})

	t.Run("RunMain_SuccessAndFailure", func(t *testing.T) {
		outDoc := filepath.Join(tempDir, "runmain.spdx.json")
		exitCode := runMain([]string{targetArchive, flagOutput, "spdx-json=" + outDoc})
		assert.Equal(t, 0, exitCode)

		// Invalid arguments
		exitCodeErr := runMain([]string{"--invalid-flag"})
		assert.Equal(t, 1, exitCodeErr)

		// Generation failure
		exitCodeFail := runMain([]string{filepath.Join(tempDir, "missing.tar.gz"), flagOutput, outDoc})
		assert.Equal(t, 1, exitCodeFail)
	})
}

func TestRunSyft_UnsupportedFormat(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	_, err := runSyft(tempDir, "unsupported-format")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported SBOM format")
}

func TestRunSyft_MissingSyftExecutable(t *testing.T) {
	t.Setenv("PATH", "")
	tempDir := t.TempDir()
	_, err := runSyft(tempDir, "spdx-json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "syft executable not found in PATH")
}

func TestWriteAtomic_InvalidDirectory(t *testing.T) {
	t.Parallel()
	// Create a regular file and try to write into a subpath of it
	tempDir := t.TempDir()
	blocker := filepath.Join(tempDir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("file"), 0o644))

	invalidPath := filepath.Join(blocker, "sub", "output.json")
	err := writeAtomic(invalidPath, []byte("data"))
	require.Error(t, err)
}

func TestExtractVariantsFromFatBinary_InvalidSize(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	stubBytes := []byte("\x7fELF\x02\x01\x01\x00" + strings.Repeat("\x00", 256))
	variantPayload := []byte("payload")
	c, err := codec.Get(compressionNone)
	require.NoError(t, err)

	var compBuf bytes.Buffer
	require.NoError(t, c.Compress(&compBuf, variantPayload, "fastest"))

	idx := &format.Index{
		Version:    format.FormatVersion2,
		TargetArch: testArchAMD64,
		Variants: []format.VariantEntry{
			{
				Level:            "v1",
				Offset:           int64(len(stubBytes)),
				CompressedSize:   int64(compBuf.Len()),
				UncompressedSize: 2 * format.MaxPayloadSize, // Over 1GB limit
				SHA256:           strings.Repeat("a", 64),
				Compression:      compressionNone,
			},
		},
	}

	var fatBuf bytes.Buffer
	fatBuf.Write(stubBytes)
	fatBuf.Write(compBuf.Bytes())
	_, err = format.WriteIndexAndTrailer(&fatBuf, idx, int64(len(stubBytes)+compBuf.Len()))
	require.NoError(t, err)

	fatPath := filepath.Join(tempDir, "oversized_variant.fat")
	require.NoError(t, os.WriteFile(fatPath, fatBuf.Bytes(), 0o755))

	stagingDir := filepath.Join(tempDir, "staging")
	err = extractVariantsFromFatBinary(fatPath, stagingDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds 1GB limit")
}

func TestExtractVariantsFromFatBinary_Errors(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	stubBytes := []byte("\x7fELF\x02\x01\x01\x00" + strings.Repeat("\x00", 256))

	t.Run("EmptyVariants", func(t *testing.T) {
		idx := &format.Index{
			Version:    format.FormatVersion2,
			TargetArch: testArchAMD64,
			Variants:   nil,
		}
		var fatBuf bytes.Buffer
		fatBuf.Write(stubBytes)
		_, err := format.WriteIndexAndTrailer(&fatBuf, idx, int64(len(stubBytes)))
		require.NoError(t, err)

		fatPath := filepath.Join(tempDir, "empty_variants.fat")
		require.NoError(t, os.WriteFile(fatPath, fatBuf.Bytes(), 0o755))
		err = extractVariantsFromFatBinary(fatPath, filepath.Join(tempDir, "stage1"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one microarchitecture variant must be specified")
	})

	t.Run("DictHashMismatch", func(t *testing.T) {
		dictBytes := []byte("valid-dict-data")
		idx := &format.Index{
			Version:          format.FormatVersion2,
			TargetArch:       testArchAMD64,
			DictionaryOffset: int64(len(stubBytes)),
			DictionarySize:   int64(len(dictBytes)),
			DictionarySHA256: strings.Repeat("f", 64), // Mismatch
			Variants: []format.VariantEntry{
				{
					Level:            "v1",
					Offset:           int64(len(stubBytes) + len(dictBytes)),
					CompressedSize:   10,
					UncompressedSize: 10,
					SHA256:           strings.Repeat("0", 64),
					Compression:      compressionNone,
				},
			},
		}
		var fatBuf bytes.Buffer
		fatBuf.Write(stubBytes)
		fatBuf.Write(dictBytes)
		fatBuf.Write(make([]byte, 10))
		_, err := format.WriteIndexAndTrailer(&fatBuf, idx, int64(fatBuf.Len()))
		require.NoError(t, err)

		fatPath := filepath.Join(tempDir, "bad_dict.fat")
		require.NoError(t, os.WriteFile(fatPath, fatBuf.Bytes(), 0o755))
		err = extractVariantsFromFatBinary(fatPath, filepath.Join(tempDir, "stage2"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dictionary hash mismatch")
	})

	t.Run("UnsupportedCodec", func(t *testing.T) {
		idx := &format.Index{
			Version:    format.FormatVersion2,
			TargetArch: testArchAMD64,
			Variants: []format.VariantEntry{
				{
					Level:            "v1",
					Offset:           int64(len(stubBytes)),
					CompressedSize:   10,
					UncompressedSize: 10,
					SHA256:           strings.Repeat("0", 64),
					Compression:      "unsupported_codec",
				},
			},
		}
		var fatBuf bytes.Buffer
		fatBuf.Write(stubBytes)
		fatBuf.Write(make([]byte, 10))
		_, err := format.WriteIndexAndTrailer(&fatBuf, idx, int64(fatBuf.Len()))
		require.NoError(t, err)

		fatPath := filepath.Join(tempDir, "bad_codec.fat")
		require.NoError(t, os.WriteFile(fatPath, fatBuf.Bytes(), 0o755))
		err = extractVariantsFromFatBinary(fatPath, filepath.Join(tempDir, "stage3"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getting codec unsupported_codec")
	})
}

func createSyntheticFatBinary(t *testing.T, targetArch string, dictBytes []byte, variantPayload []byte, compCodec string) []byte {
	t.Helper()
	var buf bytes.Buffer
	stubBytes := make([]byte, 256)
	copy(stubBytes, []byte("\x7fELF\x02\x01\x01\x00"))
	buf.Write(stubBytes)

	var dictOffset int64
	var dictSize int64
	var dictSHA256 string
	if len(dictBytes) > 0 {
		dictOffset = int64(buf.Len())
		dictSize = int64(len(dictBytes))
		dHash := sha256.Sum256(dictBytes)
		dictSHA256 = hex.EncodeToString(dHash[:])
		buf.Write(dictBytes)
	}

	c, err := codec.Get(compCodec)
	require.NoError(t, err)

	var compBuf bytes.Buffer
	if len(dictBytes) > 0 {
		dc, ok := c.(codec.DictCodec)
		require.True(t, ok)
		require.NoError(t, dc.CompressWithDict(&compBuf, variantPayload, "fastest", dictBytes))
	} else {
		require.NoError(t, c.Compress(&compBuf, variantPayload, "fastest"))
	}

	payloadOffset := int64(buf.Len())
	compBytes := compBuf.Bytes()
	buf.Write(compBytes)

	pHash := sha256.Sum256(variantPayload)
	idx := &format.Index{
		Version:          format.FormatVersion2,
		TargetArch:       targetArch,
		DictionaryOffset: dictOffset,
		DictionarySize:   dictSize,
		DictionarySHA256: dictSHA256,
		Variants: []format.VariantEntry{
			{
				Level:            "v1",
				Offset:           payloadOffset,
				CompressedSize:   int64(len(compBytes)),
				UncompressedSize: int64(len(variantPayload)),
				SHA256:           hex.EncodeToString(pHash[:]),
				Compression:      compCodec,
			},
		},
	}

	currentOffset := int64(buf.Len())
	_, err = format.WriteIndexAndTrailer(&buf, idx, currentOffset)
	require.NoError(t, err)

	return buf.Bytes()
}

func TestAttributeSBOM(t *testing.T) {
	t.Parallel()

	t.Run("SPDX_Attribution", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{
			"spdxVersion": "SPDX-2.3",
			"name": "/tmp/scan-dir-123",
			"documentNamespace": "http://spdx.org/spdxdocs/repo/tmp/scan-dir-123"
		}`)
		out, err := attributeSBOM(raw, "spdx-json", "dist/microfat_0.2.3_linux_amd64.tar.gz", "/tmp/scan-dir-123")
		require.NoError(t, err)

		var doc map[string]any
		require.NoError(t, json.Unmarshal(out, &doc))
		assert.Equal(t, "microfat_0.2.3_linux_amd64.tar.gz", doc["name"])
		assert.Equal(t, "http://spdx.org/spdxdocs/repo/microfat_0.2.3_linux_amd64.tar.gz", doc["documentNamespace"])
	})

	t.Run("CycloneDX_Attribution", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{
			"bomFormat": "CycloneDX",
			"metadata": {
				"component": {
					"name": "scan-dir",
					"type": "directory"
				}
			}
		}`)
		out, err := attributeSBOM(raw, "cyclonedx-json", "dist/microfat_0.2.3_linux_amd64.tar.gz", "/tmp/scan-dir")
		require.NoError(t, err)

		var doc map[string]any
		require.NoError(t, json.Unmarshal(out, &doc))
		meta := doc["metadata"].(map[string]any)
		comp := meta["component"].(map[string]any)
		assert.Equal(t, "microfat_0.2.3_linux_amd64.tar.gz", comp["name"])
		assert.Equal(t, "file", comp["type"])
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		t.Parallel()
		_, err := attributeSBOM([]byte("invalid json"), "spdx-json", "app.tar.gz", "/tmp")
		require.Error(t, err)
	})
}

func TestWriteAtomic_Success(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "out", "sbom.json")
	content := []byte(`{"test": true}`)

	err := writeAtomic(targetPath, content)
	require.NoError(t, err)

	readBack, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	assert.Equal(t, content, readBack)
}

func TestExtractVariantsFromFatBinary_WithDict(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	samples := [][]byte{
		bytes.Repeat([]byte("alpha beta gamma delta "), 50),
		bytes.Repeat([]byte("epsilon zeta eta theta "), 50),
	}
	dictBytes, err := codec.TrainDictionary(samples, 1024, "fastest")
	require.NoError(t, err)

	variantPayload := []byte("alpha beta gamma delta epsilon zeta eta theta 1234567890")
	fatBytes := createSyntheticFatBinary(t, testArchAMD64, dictBytes, variantPayload, "zstd")

	fatPath := filepath.Join(tempDir, "dict_fat.bin")
	require.NoError(t, os.WriteFile(fatPath, fatBytes, 0o755))

	stagingDir := filepath.Join(tempDir, "staging")
	require.NoError(t, extractVariantsFromFatBinary(fatPath, stagingDir))

	extractedVariant := filepath.Join(stagingDir, "microfat-variant-v1")
	readPayload, err := os.ReadFile(extractedVariant)
	require.NoError(t, err)
	assert.Equal(t, variantPayload, readPayload)
}

func TestGenerate_SyntheticArchive(t *testing.T) {
	if _, err := exec.LookPath("syft"); err != nil {
		t.Skip("syft not available in PATH")
	}

	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "microfat_0.2.3_linux_amd64.tar.gz")

	fatBytes := createSyntheticFatBinary(t, testArchAMD64, nil, []byte("payload-bytes"), compressionNone)
	dummyELF := []byte("\x7fELF\x02\x01\x01\x00" + strings.Repeat("\x00", 256))

	createTestTarArchive(t, archivePath, map[string][]byte{
		binMicrofat:    fatBytes,
		binStub:        dummyELF,
		binStubMinimal: dummyELF,
	}, map[string]int64{
		binMicrofat:    0o755,
		binStub:        0o755,
		binStubMinimal: 0o755,
	})

	t.Run("Generate_SPDX", func(t *testing.T) {
		outDoc := filepath.Join(tempDir, "synthetic.spdx.json")
		require.NoError(t, Generate(archivePath, outDoc, "spdx-json"))
		fi, err := os.Stat(outDoc)
		require.NoError(t, err)
		assert.Greater(t, fi.Size(), int64(0))
	})

	t.Run("Generate_CycloneDX", func(t *testing.T) {
		outDoc := filepath.Join(tempDir, "synthetic.cyclonedx.json")
		require.NoError(t, Generate(archivePath, outDoc, "cyclonedx-json"))
		fi, err := os.Stat(outDoc)
		require.NoError(t, err)
		assert.Greater(t, fi.Size(), int64(0))
	})

	t.Run("RunMain_Success", func(t *testing.T) {
		outDoc := filepath.Join(tempDir, "runmain_synthetic.spdx.json")
		code := runMain([]string{archivePath, flagOutput, "spdx-json=" + outDoc})
		assert.Equal(t, 0, code)
	})

	t.Run("RunMain_Failure", func(t *testing.T) {
		code := runMain([]string{})
		assert.Equal(t, 1, code)

		missingArchive := filepath.Join(tempDir, "missing.tar.gz")
		codeErr := runMain([]string{missingArchive, flagOutput, "spdx-json=" + filepath.Join(tempDir, "out.spdx.json")})
		assert.Equal(t, 1, codeErr)
	})

	t.Run("Main_Invocation", func(t *testing.T) {
		outDoc := filepath.Join(tempDir, "main_synthetic.spdx.json")
		oldArgs := os.Args
		oldExit := exitFunc
		defer func() {
			os.Args = oldArgs
			exitFunc = oldExit
		}()

		var exitCode int
		exitFunc = func(c int) {
			exitCode = c
		}
		os.Args = []string{"release-sbom", archivePath, flagOutput, "spdx-json=" + outDoc}
		main()
		assert.Equal(t, 0, exitCode)
	})
}

func TestExtractArchiveSafely_EdgeCases(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	t.Run("InvalidGzipReader", func(t *testing.T) {
		corruptedGz := filepath.Join(tempDir, "not_gzip.tar.gz")
		require.NoError(t, os.WriteFile(corruptedGz, []byte("plain text not gzip"), 0o600))
		err := extractArchiveSafely(corruptedGz, filepath.Join(tempDir, "target1"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "creating gzip reader")
	})

	t.Run("ArchiveWithDirectoryEntry", func(t *testing.T) {
		archPath := filepath.Join(tempDir, "with_dir.tar.gz")
		f, err := os.Create(archPath)
		require.NoError(t, err)
		gw := gzip.NewWriter(f)
		tw := tar.NewWriter(gw)

		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "subdir/",
			Typeflag: tar.TypeDir,
			Mode:     0o755,
		}))

		fileContent := []byte("inner file content")
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "subdir/file.txt",
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(fileContent)),
		}))
		_, err = tw.Write(fileContent)
		require.NoError(t, err)

		require.NoError(t, tw.Close())
		require.NoError(t, gw.Close())
		require.NoError(t, f.Close())

		targetDir := filepath.Join(tempDir, "target_dir")
		require.NoError(t, extractArchiveSafely(archPath, targetDir))

		fi, err := os.Stat(filepath.Join(targetDir, "subdir", "file.txt"))
		require.NoError(t, err)
		assert.Equal(t, int64(len(fileContent)), fi.Size())
	})

	t.Run("PathTraversalAttempt", func(t *testing.T) {
		archPath := filepath.Join(tempDir, "traversal.tar.gz")
		f, err := os.Create(archPath)
		require.NoError(t, err)
		gw := gzip.NewWriter(f)
		tw := tar.NewWriter(gw)

		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "../escape.txt",
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     4,
		}))
		_, _ = tw.Write([]byte("evil"))

		require.NoError(t, tw.Close())
		require.NoError(t, gw.Close())
		require.NoError(t, f.Close())

		err = extractArchiveSafely(archPath, filepath.Join(tempDir, "target_traversal"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "illegal relative or absolute path")
	})

	t.Run("UnsupportedEntryType", func(t *testing.T) {
		archPath := filepath.Join(tempDir, "symlink.tar.gz")
		f, err := os.Create(archPath)
		require.NoError(t, err)
		gw := gzip.NewWriter(f)
		tw := tar.NewWriter(gw)

		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "link.txt",
			Typeflag: tar.TypeSymlink,
			Linkname: "target.txt",
		}))

		require.NoError(t, tw.Close())
		require.NoError(t, gw.Close())
		require.NoError(t, f.Close())

		err = extractArchiveSafely(archPath, filepath.Join(tempDir, "target_symlink"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refusing to extract unsafe archive entry")
	})
}

func TestWriteAtomic_EdgeCases(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	t.Run("InvalidTargetDir", func(t *testing.T) {
		err := writeAtomic("/dev/null/forbidden/file.json", []byte("data"))
		require.Error(t, err)
	})

	t.Run("TargetIsExistingDirectory", func(t *testing.T) {
		dir := filepath.Join(tempDir, "dir_target")
		require.NoError(t, os.MkdirAll(dir, 0o755))
		err := writeAtomic(dir, []byte("data"))
		require.Error(t, err)
	})
}

func TestExtractVariantsFromFatBinary_EdgeCases(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	t.Run("NonFatBinary", func(t *testing.T) {
		nonFat := filepath.Join(tempDir, "non_fat")
		require.NoError(t, os.WriteFile(nonFat, []byte("not a fat binary"), 0o755))
		err := extractVariantsFromFatBinary(nonFat, filepath.Join(tempDir, "out_non_fat"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading fat trailer and index")
	})

	t.Run("NonExistentFatBinary", func(t *testing.T) {
		err := extractVariantsFromFatBinary(filepath.Join(tempDir, "missing.fat"), filepath.Join(tempDir, "out_missing"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "opening fat binary")
	})
}
