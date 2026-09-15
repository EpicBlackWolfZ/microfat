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
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
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
		assert.Contains(t, err.Error(), "missing required root executable in archive")
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
		assert.Contains(t, err.Error(), "reading index and trailer")
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

	tempDir := t.TempDir()
	archiveBytes, err := os.ReadFile(archives[0])
	if err != nil {
		t.Skip("Snapshot archive unreadable or cleaned concurrently, skipping")
	}
	targetArchive := filepath.Join(tempDir, filepath.Base(archives[0]))
	require.NoError(t, os.WriteFile(targetArchive, archiveBytes, 0o644))

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

func TestExtractSingleVariant_DefenseInDepth(t *testing.T) {
	tempDir := t.TempDir()
	dummyFile, err := os.CreateTemp(tempDir, "dummy")
	require.NoError(t, err)
	defer func() { _ = dummyFile.Close() }()

	t.Run("InvalidChecksum", func(t *testing.T) {
		v := format.VariantEntry{
			Level:  "v1",
			SHA256: "not-a-valid-checksum",
		}
		err := extractSingleVariant(dummyFile, v, nil, tempDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid checksum for variant")
	})

	t.Run("InvalidUncompressedSize", func(t *testing.T) {
		v := format.VariantEntry{
			Level:            "v1",
			SHA256:           strings.Repeat("a", 64),
			UncompressedSize: 0,
		}
		err := extractSingleVariant(dummyFile, v, nil, tempDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid uncompressed size")
	})

	t.Run("InvalidOffsetOrCompressedSize", func(t *testing.T) {
		v := format.VariantEntry{
			Level:            "v1",
			SHA256:           strings.Repeat("a", 64),
			UncompressedSize: 10,
			Offset:           -1,
			CompressedSize:   10,
		}
		err := extractSingleVariant(dummyFile, v, nil, tempDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid offset")
	})
}

func TestReadSharedDictionary_DefenseInDepth(t *testing.T) {
	tempDir := t.TempDir()
	dummyFile, err := os.CreateTemp(tempDir, "dummy")
	require.NoError(t, err)
	defer func() { _ = dummyFile.Close() }()

	t.Run("SizeOutOfBounds", func(t *testing.T) {
		idx := &format.Index{
			DictionarySize: format.MaxDictionarySize + 1,
		}
		_, err := readSharedDictionary(dummyFile, idx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "out of bounds")
	})

	t.Run("InvalidChecksum", func(t *testing.T) {
		idx := &format.Index{
			DictionarySize:   10,
			DictionarySHA256: "not-valid-hex",
		}
		_, err := readSharedDictionary(dummyFile, idx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing or invalid sha256 checksum")
	})
}

func createSyntheticFatBinaryWithTiers(
	t *testing.T,
	targetArch string,
	dictBytes []byte,
	tierPayloads map[string][]byte,
	compCodec string,
) []byte {
	t.Helper()
	var buf bytes.Buffer
	baselineTier := "v1"
	if targetArch == "arm64" {
		baselineTier = "v8.0"
	}
	stubBytes := tierPayloads[baselineTier]
	if len(stubBytes) == 0 {
		stubBytes = append([]byte("\x7fELF\x02\x01\x01\x00"), make([]byte, 252)...)
	}
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

	tiers := []string{"v1", "v2", "v3", "v4"}
	if targetArch == "arm64" {
		tiers = []string{"v8.0", "v8.2", "v9.0"}
	}

	var variants []format.VariantEntry
	for _, level := range tiers {
		payload := tierPayloads[level]
		var compBuf bytes.Buffer
		if len(dictBytes) > 0 {
			dc, ok := c.(codec.DictCodec)
			require.True(t, ok)
			require.NoError(t, dc.CompressWithDict(&compBuf, payload, "fastest", dictBytes))
		} else {
			require.NoError(t, c.Compress(&compBuf, payload, "fastest"))
		}
		compBytes := compBuf.Bytes()
		pHash := sha256.Sum256(payload)

		payloadOffset := int64(buf.Len())
		buf.Write(compBytes)
		variants = append(variants, format.VariantEntry{
			Level:            level,
			Offset:           payloadOffset,
			CompressedSize:   int64(len(compBytes)),
			UncompressedSize: int64(len(payload)),
			SHA256:           hex.EncodeToString(pHash[:]),
			Compression:      compCodec,
		})
	}

	idx := &format.Index{
		Version:          format.FormatVersion2,
		TargetArch:       targetArch,
		DictionaryOffset: dictOffset,
		DictionarySize:   dictSize,
		DictionarySHA256: dictSHA256,
		Variants:         variants,
	}

	indexOffset := int64(buf.Len())
	_, err = format.WriteIndexAndTrailer(&buf, idx, indexOffset)
	require.NoError(t, err)

	return buf.Bytes()
}

func createSyntheticFatBinary(t *testing.T, targetArch string, dictBytes []byte, variantPayload []byte, compCodec string) []byte {
	t.Helper()
	tiers := []string{"v1", "v2", "v3", "v4"}
	if targetArch == "arm64" {
		tiers = []string{"v8.0", "v8.2", "v9.0"}
	}
	tierPayloads := make(map[string][]byte, len(tiers))
	for _, tier := range tiers {
		tierPayloads[tier] = variantPayload
	}
	return createSyntheticFatBinaryWithTiers(t, targetArch, dictBytes, tierPayloads, compCodec)
}

func createMockFactsAndInventory() (*releasecheck.ArchiveFacts, *releasecheck.ArchiveInventory) {
	facts := &releasecheck.ArchiveFacts{
		ArchiveName:   "microfat_0.2.3_linux_amd64.tar.gz",
		ArchiveSHA256: strings.Repeat("a", 64),
		TargetArch:    releasecheck.ArchAMD64,
		Executables: map[string]*releasecheck.ExecutableFacts{
			releasecheck.ReleaseProjectName: {Name: releasecheck.ReleaseProjectName, SHA256: strings.Repeat("b", 64)},
			releasecheck.ReleaseFullStub:    {Name: releasecheck.ReleaseFullStub, SHA256: strings.Repeat("c", 64)},
			releasecheck.ReleaseMinStub:     {Name: releasecheck.ReleaseMinStub, SHA256: strings.Repeat("d", 64)},
		},
		EmbeddedVariants: map[string]*releasecheck.VariantFacts{
			"v1": {Level: "v1", SHA256: strings.Repeat("e", 64)},
		},
	}
	depNormal := releasecheck.ModuleDep{Path: "golang.org/x/sys", Version: "v0.30.0"}
	depReplaced := releasecheck.ModuleDep{
		Path:        "github.com/orig/lib",
		Version:     "v1.0.0",
		ReplacePath: "github.com/replaced/lib",
		ReplaceVer:  "v1.0.1",
	}
	inv := &releasecheck.ArchiveInventory{
		ArchiveName: facts.ArchiveName,
		TargetArch:  facts.TargetArch,
		Binaries: map[string]*releasecheck.BinaryInventory{
			releasecheck.ReleaseProjectName: {
				Identifier:   releasecheck.ReleaseProjectName,
				BinaryName:   releasecheck.ReleaseProjectName,
				MainModule:   "github.com/EpicBlackWolfZ/microfat",
				Dependencies: map[string]releasecheck.ModuleDep{depNormal.Path: depNormal},
			},
			releasecheck.ReleaseFullStub: {
				Identifier:   releasecheck.ReleaseFullStub,
				BinaryName:   releasecheck.ReleaseFullStub,
				Dependencies: map[string]releasecheck.ModuleDep{depNormal.Path: depNormal},
			},
			releasecheck.ReleaseMinStub: {
				Identifier:   releasecheck.ReleaseMinStub,
				BinaryName:   releasecheck.ReleaseMinStub,
				Dependencies: map[string]releasecheck.ModuleDep{},
			},
			"variant:v1": {
				Identifier:   "variant:v1",
				BinaryName:   releasecheck.ReleaseProjectName,
				Dependencies: map[string]releasecheck.ModuleDep{depReplaced.Path: depReplaced},
			},
		},
		AllDependencies: map[string]releasecheck.ModuleDep{
			depNormal.Path:   depNormal,
			depReplaced.Path: depReplaced,
		},
	}
	return facts, inv
}

func TestAttributeSBOM(t *testing.T) {
	t.Parallel()
	facts, inv := createMockFactsAndInventory()

	t.Run("SPDX_Attribution", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{
			"spdxVersion": "SPDX-2.3",
			"name": "/tmp/scan-dir-123",
			"documentNamespace": "http://spdx.org/spdxdocs/repo/tmp/scan-dir-123",
			"creationInfo": {
				"created": "2026-09-15T12:00:00Z",
				"creators": ["Tool: syft-1.0.0"]
			}
		}`)
		out, err := attributeSBOM(raw, "spdx-json", facts, inv, "0.2.3")
		require.NoError(t, err)

		var doc map[string]any
		require.NoError(t, json.Unmarshal(out, &doc))
		assert.Equal(t, "microfat_0.2.3_linux_amd64.tar.gz", doc["name"])
		assert.Contains(t, doc["documentNamespace"], "microfat_0.2.3_linux_amd64.tar.gz")
		creation, ok := doc["creationInfo"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "2026-09-15T12:00:00Z", creation["created"])
	})

	t.Run("SPDX_MissingCreationInfo", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{
			"spdxVersion": "SPDX-2.3",
			"name": "/tmp/scan-dir-123",
			"documentNamespace": "http://spdx.org/spdxdocs/repo/tmp/scan-dir-123"
		}`)
		_, err := attributeSBOM(raw, "spdx-json", facts, inv, "0.2.3")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SPDX raw JSON missing required 'creationInfo'")
	})

	t.Run("SPDX_MissingCreated", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{
			"spdxVersion": "SPDX-2.3",
			"name": "/tmp/scan-dir-123",
			"documentNamespace": "http://spdx.org/spdxdocs/repo/tmp/scan-dir-123",
			"creationInfo": {
				"creators": ["Tool: syft-1.0.0"]
			}
		}`)
		_, err := attributeSBOM(raw, "spdx-json", facts, inv, "0.2.3")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SPDX raw JSON missing required 'creationInfo.created'")
	})

	t.Run("SPDX_EmptyCreators", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{
			"spdxVersion": "SPDX-2.3",
			"name": "/tmp/scan-dir-123",
			"documentNamespace": "http://spdx.org/spdxdocs/repo/tmp/scan-dir-123",
			"creationInfo": {
				"created": "2026-09-15T12:00:00Z",
				"creators": []
			}
		}`)
		_, err := attributeSBOM(raw, "spdx-json", facts, inv, "0.2.3")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SPDX raw JSON missing required 'creationInfo.creators'")
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
		out, err := attributeSBOM(raw, "cyclonedx-json", facts, inv, "0.2.3")
		require.NoError(t, err)

		var doc map[string]any
		require.NoError(t, json.Unmarshal(out, &doc))
		meta := doc["metadata"].(map[string]any)
		comp := meta["component"].(map[string]any)
		assert.Equal(t, "microfat_0.2.3_linux_amd64.tar.gz", comp["name"])
		assert.Equal(t, "file", comp["type"])
		nestedComps, ok := comp["components"].([]any)
		require.True(t, ok)
		assert.NotEmpty(t, nestedComps)
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		t.Parallel()
		_, err := attributeSBOM([]byte("invalid json"), "spdx-json", facts, inv, "0.2.3")
		require.Error(t, err)
	})
}

func TestStageExtractedVariants_CollisionIsolation(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	extractedDir := filepath.Join(tempDir, "archive")
	shippedVariantDir := filepath.Join(extractedDir, "embedded_variants")
	require.NoError(t, os.MkdirAll(shippedVariantDir, 0o755))

	// Shipped archive member at embedded_variants/microfat-variant-v1
	shippedVariantPath := filepath.Join(shippedVariantDir, "microfat-variant-v1")
	shippedContent := []byte("original-archive-shipped-file-content")
	require.NoError(t, os.WriteFile(shippedVariantPath, shippedContent, 0o755))

	derivedContent := []byte("derived-unpacked-variant-content")
	facts := &releasecheck.ArchiveFacts{
		ArchiveName:  "microfat_0.2.3_linux_amd64.tar.gz",
		TargetArch:   "amd64",
		StagingDir:   tempDir,
		ExtractedDir: extractedDir,
		EmbeddedVariants: map[string]*releasecheck.VariantFacts{
			"v1": {
				Level: "v1",
				Data:  derivedContent,
			},
		},
	}

	err := stageExtractedVariants(facts)
	require.NoError(t, err)

	// Verify original archive member was NOT overwritten
	shippedAfter, err := os.ReadFile(shippedVariantPath)
	require.NoError(t, err)
	assert.Equal(t, shippedContent, shippedAfter)

	// Verify derived variant exists in its own separate directory
	derivedPath := filepath.Join(tempDir, "derived", "variants", "microfat-variant-v1")
	derivedAfter, err := os.ReadFile(derivedPath)
	require.NoError(t, err)
	assert.Equal(t, derivedContent, derivedAfter)
}

func TestStageExtractedVariants_Errors(t *testing.T) {
	t.Parallel()

	t.Run("MkdirError", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		blockedFile := filepath.Join(tempDir, "derived")
		require.NoError(t, os.WriteFile(blockedFile, []byte("block"), 0o644))
		facts := &releasecheck.ArchiveFacts{StagingDir: tempDir}
		err := stageExtractedVariants(facts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "creating variants staging dir")
	})

	t.Run("WriteError", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		facts := &releasecheck.ArchiveFacts{
			StagingDir: tempDir,
			EmbeddedVariants: map[string]*releasecheck.VariantFacts{
				"v1": {Level: "v1", Data: []byte("test")},
			},
		}
		variantDir := filepath.Join(tempDir, "derived", "variants", "microfat-variant-v1")
		require.NoError(t, os.MkdirAll(variantDir, 0o755))
		err := stageExtractedVariants(facts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "staging variant v1")
	})
}

func TestGenerate_ErrorsAndArchBranches(t *testing.T) {
	t.Parallel()

	t.Run("ARM64_BranchAndMissingArchive", func(t *testing.T) {
		t.Parallel()
		err := Generate("microfat_0.2.3_linux_arm64.tar.gz", "out.json", "spdx-json")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "validating archive")
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

func ensureSyftInPATH(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("syft"); err == nil {
		return
	}
	mockDir := t.TempDir()
	mockSyftPath := filepath.Join(mockDir, "syft")
	mockScript := "#!/bin/sh\n" +
		"format=\"spdx-json\"\n" +
		"for arg in \"$@\"; do\n" +
		"\tif [ \"$arg\" = \"cyclonedx-json\" ] || [ \"$arg\" = \"cyclonedx\" ]; then\n" +
		"\t\tformat=\"cyclonedx-json\"\n" +
		"\tfi\n" +
		"done\n" +
		"if [ \"$format\" = \"cyclonedx-json\" ]; then\n" +
		"\techo '{\"bomFormat\":\"CycloneDX\",\"specVersion\":\"1.5\",\"metadata\":{\"component\":{\"name\":\"archive\"}}," +
		"\"components\":[{\"name\":\"github.com/spf13/cobra\"},{\"name\":\"github.com/EpicBlackWolfZ/microfat\"}]}'\n" +
		"else\n" +
		"\techo '{\"spdxVersion\":\"SPDX-2.3\",\"name\":\"archive\",\"documentNamespace\":\"https://anchore.com/syft/dir/mock\"," +
		"\"creationInfo\":{\"created\":\"2026-09-15T12:00:00Z\",\"creators\":[\"Tool: syft-mock\"]}," +
		"\"packages\":[{\"name\":\"github.com/spf13/cobra\"},{\"name\":\"github.com/EpicBlackWolfZ/microfat\"}," +
		"{\"name\":\"github.com/klauspost/compress\"}]}'\n" +
		"fi\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(mockSyftPath, []byte(mockScript), 0o755))
	t.Setenv("PATH", mockDir+string(filepath.ListSeparator)+os.Getenv("PATH"))
}

func TestGenerate_SyntheticArchive(t *testing.T) {
	ensureSyftInPATH(t)

	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "microfat_0.2.3_linux_amd64.tar.gz")

	dummySrc := filepath.Join(tempDir, "dummy_main.go")
	require.NoError(t, os.WriteFile(dummySrc, []byte("package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"ok\")}\n"), 0o644))

	compileTier := func(tier string) []byte {
		binPath := filepath.Join(tempDir, "bin_"+tier)
		cmd := exec.Command("go", "build", "-ldflags=-s -w", "-trimpath", "-o", binPath, dummySrc)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "GOAMD64="+tier)
		cmdOut, err := cmd.CombinedOutput()
		require.NoError(t, err, string(cmdOut))
		data, err := os.ReadFile(binPath)
		require.NoError(t, err)
		return data
	}

	tierPayloads := map[string][]byte{
		"v1": compileTier("v1"),
		"v2": compileTier("v2"),
		"v3": compileTier("v3"),
		"v4": compileTier("v4"),
	}
	dummyELF := tierPayloads["v1"]
	fatBytes := createSyntheticFatBinaryWithTiers(t, testArchAMD64, nil, tierPayloads, compressionNone)

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

func TestRunSyft_ExecutionError(t *testing.T) {
	mockDir := t.TempDir()
	mockSyftPath := filepath.Join(mockDir, "syft")
	mockScript := "#!/bin/sh\necho \"simulated syft error\" >&2\nexit 2\n"
	require.NoError(t, os.WriteFile(mockSyftPath, []byte(mockScript), 0o755))
	t.Setenv("PATH", mockDir)

	tempDir := t.TempDir()
	_, err := runSyft(tempDir, "spdx-json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "syft failed with exit code 2: simulated syft error")
}

func TestAttributeSBOM_InvalidJSON(t *testing.T) {
	t.Parallel()
	facts, inv := createMockFactsAndInventory()
	_, err := attributeSBOM([]byte("{invalid-json"), "spdx-json", facts, inv, "0.2.3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing syft")
}

func TestWriteAtomic_CreateTempError(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("skipping read-only directory test when running as root")
	}
	tempDir := t.TempDir()
	roDir := filepath.Join(tempDir, "readonly")
	require.NoError(t, os.MkdirAll(roDir, 0o555))
	defer func() { _ = os.Chmod(roDir, 0o755) }()

	target := filepath.Join(roDir, "out.json")
	err := writeAtomic(target, []byte("test"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating temp file in")
}

func TestValidateAttributedSBOM_Formats(t *testing.T) {
	t.Parallel()
	facts, inv := createMockFactsAndInventory()

	t.Run("UnsupportedFormat", func(t *testing.T) {
		err := validateAttributedSBOM([]byte("{}"), "invalid-format", facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported format")
	})

	t.Run("InvalidSPDXBytes", func(t *testing.T) {
		err := validateAttributedSBOM([]byte("{not-valid-spdx"), "spdx-json", facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "semantic validation of generated SPDX failed")
	})

	t.Run("InvalidCycloneDXBytes", func(t *testing.T) {
		err := validateAttributedSBOM([]byte("{not-valid-cdx"), "cyclonedx-json", facts, inv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "semantic validation of generated CycloneDX failed")
	})
}

func TestResolveArchiveVersion(t *testing.T) {
	t.Parallel()

	v := resolveArchiveVersion("/some/path/microfat_v1.2.3_linux_amd64.tar.gz")
	assert.Equal(t, "1.2.3", v)

	vNoPrefix := resolveArchiveVersion("/some/path/microfat_2.0.0_linux_arm64.tar.gz")
	assert.Equal(t, "2.0.0", vNoPrefix)

	vFallback := resolveArchiveVersion("/some/path/invalid.tar.gz")
	assert.Equal(t, "0.0.0-dev", vFallback)
}


