package releasecheck_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testReadme = "README.md"

type tarEntry struct {
	Name     string
	Data     []byte
	Mode     int64
	Typeflag byte
	Linkname string
}

func createOrderedTarGz(t *testing.T, archivePath string, entries []tarEntry) {
	t.Helper()
	f, err := os.Create(archivePath)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	gw := gzip.NewWriter(f)
	defer func() { _ = gw.Close() }()

	tw := tar.NewWriter(gw)
	defer func() { _ = tw.Close() }()

	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.Name,
			Mode:     e.Mode,
			Size:     int64(len(e.Data)),
			Typeflag: e.Typeflag,
			Linkname: e.Linkname,
		}
		if hdr.Typeflag == 0 {
			hdr.Typeflag = tar.TypeReg
		}
		if hdr.Mode == 0 {
			if hdr.Typeflag == tar.TypeDir {
				hdr.Mode = 0o755
			} else {
				hdr.Mode = 0o644
			}
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if len(e.Data) > 0 {
			_, err := tw.Write(e.Data)
			require.NoError(t, err)
		}
	}
}

var (
	testBinaryCache = make(map[string][]byte)
	testBinaryMu    sync.Mutex
)

func getCompiledTestBinary(t *testing.T, arch, tier string) []byte {
	t.Helper()
	key := arch + "_" + tier
	testBinaryMu.Lock()
	defer testBinaryMu.Unlock()
	if b, ok := testBinaryCache[key]; ok {
		return b
	}

	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644))

	outBin := filepath.Join(tempDir, "bin")
	cmd := exec.Command("go", "build", "-ldflags=-s -w", "-trimpath", "-o", outBin, src)
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"CGO_ENABLED=0",
		"GOOS=linux",
		"GOARCH=" + arch,
	}
	if arch == releasecheck.ArchAMD64 {
		env = append(env, "GOAMD64="+tier)
	} else if arch == releasecheck.ArchARM64 {
		env = append(env, "GOARM64="+tier)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "compiling test binary: %s", string(out))

	data, err := os.ReadFile(outBin)
	require.NoError(t, err)

	testBinaryCache[key] = data
	return data
}

func createValidFatBinaryWithDict(t *testing.T, arch string, tiers []string, dict []byte) []byte {
	t.Helper()
	baselineTier := "v1"
	if arch == releasecheck.ArchARM64 {
		baselineTier = "v8.0"
	}
	stubBytes := getCompiledTestBinary(t, arch, baselineTier)

	c, err := codec.Get("none")
	require.NoError(t, err)

	var compVariantsBuf bytes.Buffer
	var variantEntries []format.VariantEntry

	dictOffset := int64(len(stubBytes))
	dictSize := int64(len(dict))
	dictHash := ""
	if dictSize > 0 {
		h := sha256.Sum256(dict)
		dictHash = hex.EncodeToString(h[:])
	}

	payloadOffset := dictOffset + dictSize

	for _, tier := range tiers {
		variantPayload := getCompiledTestBinary(t, arch, tier)
		h := sha256.Sum256(variantPayload)
		tierHash := hex.EncodeToString(h[:])

		var compBuf bytes.Buffer
		require.NoError(t, c.Compress(&compBuf, variantPayload, "fastest"))

		vEntry := format.VariantEntry{
			Level:            tier,
			Offset:           payloadOffset + int64(compVariantsBuf.Len()),
			CompressedSize:   int64(compBuf.Len()),
			UncompressedSize: int64(len(variantPayload)),
			SHA256:           tierHash,
			Compression:      "none",
		}
		variantEntries = append(variantEntries, vEntry)
		compVariantsBuf.Write(compBuf.Bytes())
	}

	idx := &format.Index{
		Version:          format.FormatVersion2,
		TargetArch:       arch,
		Variants:         variantEntries,
		DictionaryOffset: dictOffset,
		DictionarySize:   dictSize,
		DictionarySHA256: dictHash,
	}

	var fatBuf bytes.Buffer
	fatBuf.Write(stubBytes)
	if dictSize > 0 {
		fatBuf.Write(dict)
	}
	fatBuf.Write(compVariantsBuf.Bytes())
	_, err = format.WriteIndexAndTrailer(&fatBuf, idx, int64(fatBuf.Len()))
	require.NoError(t, err)

	return fatBuf.Bytes()
}

func createValidFatBinary(t *testing.T, arch string, tiers []string) []byte {
	return createValidFatBinaryWithDict(t, arch, tiers, nil)
}

func createStandardValidEntries(t *testing.T, arch string, contract *releasecheck.ReleaseContract) []tarEntry {
	t.Helper()
	tiers := contract.ExpectedTiers[arch]
	fatData := createValidFatBinary(t, arch, tiers)
	baselineTier := "v1"
	if arch == releasecheck.ArchARM64 {
		baselineTier = "v8.0"
	}
	stubData := getCompiledTestBinary(t, arch, baselineTier)

	return []tarEntry{
		{Name: releasecheck.ReleaseProjectName, Data: fatData, Mode: 0o755},
		{Name: releasecheck.ReleaseFullStub, Data: stubData, Mode: 0o755},
		{Name: releasecheck.ReleaseMinStub, Data: stubData, Mode: 0o755},
		{Name: testReadme, Data: []byte("# README"), Mode: 0o644},
	}
}

func TestValidateArchive_ValidAMD64(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	entries := createStandardValidEntries(t, releasecheck.ArchAMD64, contract)
	archivePath := filepath.Join(t.TempDir(), "microfat_0.2.3_linux_amd64.tar.gz")
	createOrderedTarGz(t, archivePath, entries)

	facts, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
	require.NoError(t, err)
	defer func() { _ = facts.Cleanup() }()
	assert.Equal(t, releasecheck.ArchAMD64, facts.TargetArch)
	assert.Len(t, facts.Executables, 3)
	assert.Len(t, facts.EmbeddedVariants, 4) // v1, v2, v3, v4
	assert.NotEmpty(t, facts.ArchiveSHA256)
}

func TestValidateArchive_DuplicateEntriesRejected(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	baseEntries := createStandardValidEntries(t, releasecheck.ArchAMD64, contract)

	t.Run("duplicate_after", func(t *testing.T) {
		t.Parallel()
		entries := append([]tarEntry{}, baseEntries...)
		entries = append(entries, tarEntry{
			Name: releasecheck.ReleaseProjectName,
			Data: []byte("malicious-second-copy"),
			Mode: 0o755,
		})
		archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
		createOrderedTarGz(t, archivePath, entries)

		_, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate entry in archive")
	})

	t.Run("duplicate_before", func(t *testing.T) {
		t.Parallel()
		entries := []tarEntry{
			{
				Name: releasecheck.ReleaseProjectName,
				Data: []byte("malicious-first-copy"),
				Mode: 0o755,
			},
		}
		entries = append(entries, baseEntries...)
		archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
		createOrderedTarGz(t, archivePath, entries)

		_, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate entry in archive")
	})

	t.Run("normalized_duplicate_dotslash", func(t *testing.T) {
		t.Parallel()
		entries := append([]tarEntry{}, baseEntries...)
		entries = append(entries, tarEntry{
			Name: "./" + releasecheck.ReleaseProjectName,
			Data: []byte("alias-copy"),
			Mode: 0o755,
		})
		archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
		createOrderedTarGz(t, archivePath, entries)

		_, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate entry in archive")
	})
}

func TestValidateArchive_NestedCollisionRejected(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	baseEntries := createStandardValidEntries(t, releasecheck.ArchAMD64, contract)

	entries := append([]tarEntry{}, baseEntries...)
	entries = append(entries, tarEntry{
		Name: "subdir/" + releasecheck.ReleaseProjectName,
		Data: []byte("nested-binary"),
		Mode: 0o755,
	})
	archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
	createOrderedTarGz(t, archivePath, entries)

	_, err = releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nested collision with required executable name")
}

func TestValidateArchive_UnsafeEntryTypesRejected(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	baseEntries := createStandardValidEntries(t, releasecheck.ArchAMD64, contract)

	t.Run("symlink", func(t *testing.T) {
		t.Parallel()
		entries := append([]tarEntry{}, baseEntries...)
		entries = append(entries, tarEntry{
			Name:     "link_entry",
			Typeflag: tar.TypeSymlink,
			Linkname: "/etc/passwd",
		})
		archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
		createOrderedTarGz(t, archivePath, entries)

		_, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported entry type")
	})

	t.Run("hardlink", func(t *testing.T) {
		t.Parallel()
		entries := append([]tarEntry{}, baseEntries...)
		entries = append(entries, tarEntry{
			Name:     "hardlink_entry",
			Typeflag: tar.TypeLink,
			Linkname: releasecheck.ReleaseProjectName,
		})
		archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
		createOrderedTarGz(t, archivePath, entries)

		_, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported entry type")
	})
}

func TestValidateArchive_PathTraversalRejected(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	baseEntries := createStandardValidEntries(t, releasecheck.ArchAMD64, contract)

	traversalPaths := []string{
		"../escaped",
		"/absolute/path",
		"dir/../../escaped",
		"win\\path",
	}

	for _, p := range traversalPaths {
		pathName := p
		t.Run("traversal_"+pathName, func(t *testing.T) {
			t.Parallel()
			entries := append([]tarEntry{}, baseEntries...)
			entries = append(entries, tarEntry{
				Name: pathName,
				Data: []byte("bad"),
				Mode: 0o644,
			})
			archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
			createOrderedTarGz(t, archivePath, entries)

			_, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
			require.Error(t, err)
		})
	}
}

func TestValidateArchive_MissingRequiredExecutable(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	for _, missing := range contract.RequiredExecutables {
		missingExe := missing
		t.Run("missing_"+missingExe, func(t *testing.T) {
			t.Parallel()
			allEntries := createStandardValidEntries(t, releasecheck.ArchAMD64, contract)
			var entries []tarEntry
			for _, e := range allEntries {
				if e.Name != missingExe {
					entries = append(entries, e)
				}
			}
			archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
			createOrderedTarGz(t, archivePath, entries)

			_, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing required root executable")
			assert.Contains(t, err.Error(), missingExe)
		})
	}
}

func TestValidateArchive_NonExecutablePermissions(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	entries := createStandardValidEntries(t, releasecheck.ArchAMD64, contract)
	// Change microfat permissions to non-executable 0644
	entries[0].Mode = 0o644

	archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
	createOrderedTarGz(t, archivePath, entries)

	_, err = releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must have executable permissions")
}

func TestValidateArchive_ArchMismatchInFatBinary(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	// Create arm64 fat binary but claim it's amd64 archive
	entries := createStandardValidEntries(t, releasecheck.ArchARM64, contract)
	archivePath := filepath.Join(t.TempDir(), "test.tar.gz")
	createOrderedTarGz(t, archivePath, entries)

	_, err = releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target arch mismatch")
}

func TestExtractArchiveSafely_ValidAndRejections(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	entries := createStandardValidEntries(t, releasecheck.ArchAMD64, contract)
	archivePath := filepath.Join(t.TempDir(), "valid.tar.gz")
	createOrderedTarGz(t, archivePath, entries)

	extractDir := filepath.Join(t.TempDir(), "extracted")
	require.NoError(t, os.MkdirAll(extractDir, 0o755))

	err = releasecheck.ExtractArchiveSafely(archivePath, extractDir)
	require.NoError(t, err)

	// Verify extracted files
	for _, req := range contract.RequiredExecutables {
		p := filepath.Join(extractDir, req)
		fi, err := os.Stat(p)
		require.NoError(t, err)
		assert.True(t, fi.Mode()&0o111 != 0)
	}

	// Test duplicate rejection
	dupeEntries := append([]tarEntry{}, entries...)
	dupeEntries = append(dupeEntries, tarEntry{
		Name: testReadme,
		Data: []byte("second-readme"),
		Mode: 0o644,
	})
	dupeArchive := filepath.Join(t.TempDir(), "dupe.tar.gz")
	createOrderedTarGz(t, dupeArchive, dupeEntries)

	dupeExtractDir := filepath.Join(t.TempDir(), "dupe_extracted")
	require.NoError(t, os.MkdirAll(dupeExtractDir, 0o755))
	err = releasecheck.ExtractArchiveSafely(dupeArchive, dupeExtractDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate entry in archive")
}

func TestExtractFileFromArchive_ValidAndDuplicate(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	entries := createStandardValidEntries(t, releasecheck.ArchAMD64, contract)
	archivePath := filepath.Join(t.TempDir(), "valid.tar.gz")
	createOrderedTarGz(t, archivePath, entries)

	destPath := filepath.Join(t.TempDir(), "microfat-out")
	err = releasecheck.ExtractFileFromArchive(archivePath, releasecheck.ReleaseProjectName, destPath)
	require.NoError(t, err)
	fi, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.True(t, fi.Mode()&0o111 != 0)

	// Missing file
	err = releasecheck.ExtractFileFromArchive(archivePath, "nonexistent", filepath.Join(t.TempDir(), "out"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in archive")

	// Archive with duplicate must fail even if target was encountered first
	dupeEntries := append([]tarEntry{}, entries...)
	dupeEntries = append(dupeEntries, tarEntry{
		Name: testReadme,
		Data: []byte("dupe"),
		Mode: 0o644,
	})
	dupeArchive := filepath.Join(t.TempDir(), "dupe.tar.gz")
	createOrderedTarGz(t, dupeArchive, dupeEntries)

	err = releasecheck.ExtractFileFromArchive(dupeArchive, releasecheck.ReleaseProjectName, filepath.Join(t.TempDir(), "out2"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate entry in archive")
}

func createTruncatedTarGz(t *testing.T, archivePath, name string, advertisedSize int64, actualData []byte) {
	t.Helper()
	f, err := os.Create(archivePath)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	gw := gzip.NewWriter(f)
	defer func() { _ = gw.Close() }()

	hdr := &tar.Header{
		Name:     name,
		Mode:     0o755,
		Size:     advertisedSize,
		Typeflag: tar.TypeReg,
	}
	tw := tar.NewWriter(gw)
	require.NoError(t, tw.WriteHeader(hdr))
	if len(actualData) > 0 {
		_, err := tw.Write(actualData)
		require.NoError(t, err)
	}
}

func TestExtractFileFromArchive_Hardening(t *testing.T) {
	t.Parallel()

	targetExe := releasecheck.ReleaseProjectName

	t.Run("ValidExact100Bytes", func(t *testing.T) {
		t.Parallel()
		data := bytes.Repeat([]byte("X"), 100)
		archivePath := filepath.Join(t.TempDir(), "valid100.tar.gz")
		createOrderedTarGz(t, archivePath, []tarEntry{
			{Name: targetExe, Data: data, Mode: 0o755},
		})
		destPath := filepath.Join(t.TempDir(), "extracted-microfat")
		err := releasecheck.ExtractFileFromArchive(archivePath, targetExe, destPath)
		require.NoError(t, err)
		readBack, err := os.ReadFile(destPath)
		require.NoError(t, err)
		assert.Equal(t, data, readBack)
		assert.Len(t, readBack, 100)
	})

	t.Run("TruncatedMember", func(t *testing.T) {
		t.Parallel()
		archivePath := filepath.Join(t.TempDir(), "truncated.tar.gz")
		actualData := bytes.Repeat([]byte("Y"), 50)
		createTruncatedTarGz(t, archivePath, targetExe, 100, actualData)

		destPath := filepath.Join(t.TempDir(), "extracted-truncated")
		err := releasecheck.ExtractFileFromArchive(archivePath, targetExe, destPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading target entry")
	})

	t.Run("OversizedMember", func(t *testing.T) {
		t.Parallel()
		archivePath := filepath.Join(t.TempDir(), "oversized.tar.gz")
		createTruncatedTarGz(t, archivePath, targetExe, 250*1024*1024+1, nil)

		destPath := filepath.Join(t.TempDir(), "extracted-oversized")
		err := releasecheck.ExtractFileFromArchive(archivePath, targetExe, destPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds size limit")
	})

	t.Run("DuplicateTargetEntries", func(t *testing.T) {
		t.Parallel()
		archivePath := filepath.Join(t.TempDir(), "duplicate_target.tar.gz")
		createOrderedTarGz(t, archivePath, []tarEntry{
			{Name: targetExe, Data: []byte("first"), Mode: 0o755},
			{Name: targetExe, Data: []byte("second"), Mode: 0o755},
		})

		destPath := filepath.Join(t.TempDir(), "extracted-dupe")
		err := releasecheck.ExtractFileFromArchive(archivePath, targetExe, destPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate entry in archive")
	})

	t.Run("PathTraversalTargets", func(t *testing.T) {
		t.Parallel()
		archivePath := filepath.Join(t.TempDir(), "traversal_test.tar.gz")
		createOrderedTarGz(t, archivePath, []tarEntry{
			{Name: targetExe, Data: []byte("payload"), Mode: 0o755},
		})

		invalidTargets := []string{
			"../" + targetExe,
			"subdir/" + targetExe,
			"/" + targetExe,
			"",
			".",
			"..",
			targetExe + "/../" + targetExe,
			"sub\\" + targetExe,
		}

		for _, target := range invalidTargets {
			t.Run(target, func(t *testing.T) {
				destPath := filepath.Join(t.TempDir(), "extracted")
				err := releasecheck.ExtractFileFromArchive(archivePath, target, destPath)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid target name")
			})
		}
	})
}

func TestValidateArchive_WithSharedDict(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	dict := []byte("shared-dictionary-data-for-compression")
	tiers := contract.ExpectedTiers[releasecheck.ArchAMD64]
	fatData := createValidFatBinaryWithDict(t, releasecheck.ArchAMD64, tiers, dict)
	stubData := getCompiledTestBinary(t, releasecheck.ArchAMD64, "v1")

	entries := []tarEntry{
		{Name: releasecheck.ReleaseProjectName, Data: fatData, Mode: 0o755},
		{Name: releasecheck.ReleaseFullStub, Data: stubData, Mode: 0o755},
		{Name: releasecheck.ReleaseMinStub, Data: stubData, Mode: 0o755},
		{Name: testReadme, Data: []byte("# README"), Mode: 0o644},
	}

	archivePath := filepath.Join(t.TempDir(), "with_dict.tar.gz")
	createOrderedTarGz(t, archivePath, entries)

	facts, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
	require.NoError(t, err)
	defer func() { _ = facts.Cleanup() }()
	assert.Equal(t, releasecheck.ArchAMD64, facts.TargetArch)
	assert.NotEmpty(t, facts.EmbeddedVariants)
}

func TestValidateArchive_InspectFatBinary_Errors(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	t.Run("NonFatBinaryExecutable", func(t *testing.T) {
		t.Parallel()
		stubData := []byte("\x7fELF\x02\x01\x01\x00" + strings.Repeat("\x00", 64))
		entries := []tarEntry{
			{Name: releasecheck.ReleaseProjectName, Data: stubData, Mode: 0o755},
			{Name: releasecheck.ReleaseFullStub, Data: stubData, Mode: 0o755},
			{Name: releasecheck.ReleaseMinStub, Data: stubData, Mode: 0o755},
			{Name: testReadme, Data: []byte("# README"), Mode: 0o644},
		}
		archivePath := filepath.Join(t.TempDir(), "nonfat.tar.gz")
		createOrderedTarGz(t, archivePath, entries)

		_, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading index and trailer")
	})

	t.Run("InvalidFormatVersion", func(t *testing.T) {
		t.Parallel()
		stubBytes := []byte("\x7fELF\x02\x01\x01\x00" + strings.Repeat("\x00", 64))
		variantPayload := []byte("\x7fELF\x02\x01\x01\x00" + strings.Repeat("\x00", 64))
		variants := []format.VariantEntry{
			{
				Level:            "v1",
				Offset:           int64(len(stubBytes)),
				CompressedSize:   int64(len(variantPayload)),
				UncompressedSize: int64(len(variantPayload)),
				SHA256:           "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				Compression:      "none",
			},
		}
		idx := &format.Index{
			Version:    format.FormatVersion1,
			TargetArch: releasecheck.ArchAMD64,
			Variants:   variants,
		}
		var fatBuf bytes.Buffer
		fatBuf.Write(stubBytes)
		fatBuf.Write(variantPayload)
		_, err := format.WriteIndexAndTrailerWithVersion(&fatBuf, idx, int64(fatBuf.Len()), format.FormatVersion1)
		require.NoError(t, err)

		entries := []tarEntry{
			{Name: releasecheck.ReleaseProjectName, Data: fatBuf.Bytes(), Mode: 0o755},
			{Name: releasecheck.ReleaseFullStub, Data: stubBytes, Mode: 0o755},
			{Name: releasecheck.ReleaseMinStub, Data: stubBytes, Mode: 0o755},
		}
		archivePath := filepath.Join(t.TempDir(), "badver.tar.gz")
		createOrderedTarGz(t, archivePath, entries)

		_, err = releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected format v2 index")
	})

	t.Run("UnexpectedVariantLevel", func(t *testing.T) {
		t.Parallel()
		contractCopy, err := releasecheck.NewReleaseContract("0.2.3")
		require.NoError(t, err)
		contractCopy.ExpectedTiers[releasecheck.ArchAMD64] = []string{"v1"}

		fatData := createValidFatBinary(t, releasecheck.ArchAMD64, []string{"v1", "v2"})
		stubData := []byte("\x7fELF\x02\x01\x01\x00" + strings.Repeat("\x00", 64))
		entries := []tarEntry{
			{Name: releasecheck.ReleaseProjectName, Data: fatData, Mode: 0o755},
			{Name: releasecheck.ReleaseFullStub, Data: stubData, Mode: 0o755},
			{Name: releasecheck.ReleaseMinStub, Data: stubData, Mode: 0o755},
		}
		archivePath := filepath.Join(t.TempDir(), "badtier.tar.gz")
		createOrderedTarGz(t, archivePath, entries)

		_, err = releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contractCopy)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected variant level")
	})
}

func TestExtractArchiveSafely_Errors(t *testing.T) {
	t.Parallel()

	t.Run("NonExistentArchive", func(t *testing.T) {
		t.Parallel()
		err := releasecheck.ExtractArchiveSafely("/nonexistent/path.tar.gz", t.TempDir())
		require.Error(t, err)
	})

	t.Run("TargetNotDirectory", func(t *testing.T) {
		t.Parallel()
		contract, err := releasecheck.NewReleaseContract("0.2.3")
		require.NoError(t, err)

		entries := createStandardValidEntries(t, releasecheck.ArchAMD64, contract)
		archivePath := filepath.Join(t.TempDir(), "valid.tar.gz")
		createOrderedTarGz(t, archivePath, entries)

		f, err := os.CreateTemp(t.TempDir(), "not_a_dir")
		require.NoError(t, err)
		_ = f.Close()

		err = releasecheck.ExtractArchiveSafely(archivePath, f.Name())
		require.Error(t, err)
	})
}

func TestValidateArchive_ValidARM64(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	entries := createStandardValidEntries(t, releasecheck.ArchARM64, contract)
	archivePath := filepath.Join(t.TempDir(), "microfat_0.2.3_linux_arm64.tar.gz")
	createOrderedTarGz(t, archivePath, entries)

	facts, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchARM64, contract)
	require.NoError(t, err)
	defer func() { _ = facts.Cleanup() }()
	assert.Equal(t, releasecheck.ArchARM64, facts.TargetArch)
	assert.Len(t, facts.Executables, 3)
	assert.Len(t, facts.EmbeddedVariants, 3) // v8.0, v8.2, v9.0
	assert.NotEmpty(t, facts.ArchiveSHA256)
}

func TestValidateArchive_ISABuildSettings_Mismatches(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	t.Run("AMD64_VariantMismatch", func(t *testing.T) {
		t.Parallel()
		badV1Payload := getCompiledTestBinary(t, releasecheck.ArchAMD64, "v4")
		tiers := contract.ExpectedTiers[releasecheck.ArchAMD64]

		stubBytes := getCompiledTestBinary(t, releasecheck.ArchAMD64, "v1")
		c, err := codec.Get("none")
		require.NoError(t, err)

		var compVariantsBuf bytes.Buffer
		var variantEntries []format.VariantEntry
		dictOffset := int64(len(stubBytes))

		for _, tier := range tiers {
			payload := getCompiledTestBinary(t, releasecheck.ArchAMD64, tier)
			if tier == "v1" {
				payload = badV1Payload
			}
			h := sha256.Sum256(payload)
			var compBuf bytes.Buffer
			require.NoError(t, c.Compress(&compBuf, payload, "fastest"))

			variantEntries = append(variantEntries, format.VariantEntry{
				Level:            tier,
				Offset:           dictOffset + int64(compVariantsBuf.Len()),
				CompressedSize:   int64(compBuf.Len()),
				UncompressedSize: int64(len(payload)),
				SHA256:           hex.EncodeToString(h[:]),
				Compression:      "none",
			})
			compVariantsBuf.Write(compBuf.Bytes())
		}

		idx := &format.Index{
			Version:    format.FormatVersion2,
			TargetArch: releasecheck.ArchAMD64,
			Variants:   variantEntries,
		}

		var fatBuf bytes.Buffer
		fatBuf.Write(stubBytes)
		fatBuf.Write(compVariantsBuf.Bytes())
		_, err = format.WriteIndexAndTrailer(&fatBuf, idx, int64(fatBuf.Len()))
		require.NoError(t, err)

		entries := []tarEntry{
			{Name: releasecheck.ReleaseProjectName, Data: fatBuf.Bytes(), Mode: 0o755},
			{Name: releasecheck.ReleaseFullStub, Data: stubBytes, Mode: 0o755},
			{Name: releasecheck.ReleaseMinStub, Data: stubBytes, Mode: 0o755},
		}

		archivePath := filepath.Join(t.TempDir(), "bad_variant.tar.gz")
		createOrderedTarGz(t, archivePath, entries)

		_, err = releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "embedded variant v1 GOAMD64 mismatch")
	})

	t.Run("AMD64_RootExecutableMismatch", func(t *testing.T) {
		t.Parallel()
		entries := createStandardValidEntries(t, releasecheck.ArchAMD64, contract)
		badStub := getCompiledTestBinary(t, releasecheck.ArchAMD64, "v4")
		for i := range entries {
			if entries[i].Name == releasecheck.ReleaseFullStub {
				entries[i].Data = badStub
			}
		}

		archivePath := filepath.Join(t.TempDir(), "bad_root.tar.gz")
		createOrderedTarGz(t, archivePath, entries)

		_, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "root executable microfat-stub GOAMD64 setting mismatch")
	})
}

func TestValidateArchive_Cleanup(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	t.Run("ExplicitCleanup", func(t *testing.T) {
		t.Parallel()
		entries := createStandardValidEntries(t, releasecheck.ArchAMD64, contract)
		archivePath := filepath.Join(t.TempDir(), "test_cleanup.tar.gz")
		createOrderedTarGz(t, archivePath, entries)

		facts, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
		require.NoError(t, err)
		stagingDir := facts.StagingDir
		require.DirExists(t, stagingDir)

		require.NoError(t, facts.Cleanup())
		assert.NoDirExists(t, stagingDir)
		assert.Empty(t, facts.StagingDir)
		assert.Empty(t, facts.ExtractedDir)

		require.NoError(t, facts.Cleanup())

		// Nil receiver or empty staging dir should be safe no-op
		var nilFacts *releasecheck.ArchiveFacts
		require.NoError(t, nilFacts.Cleanup())
		emptyFacts := &releasecheck.ArchiveFacts{}
		require.NoError(t, emptyFacts.Cleanup())
	})

	t.Run("FailureRollbackCleanup", func(t *testing.T) {
		t.Parallel()
		archivePath := filepath.Join(t.TempDir(), "corrupt.tar.gz")
		require.NoError(t, os.WriteFile(archivePath, []byte("not-a-valid-tar-gz"), 0o644))

		_, err := releasecheck.ValidateArchive(archivePath, releasecheck.ArchAMD64, contract)
		require.Error(t, err)
	})
}

func TestValidateISABuildSettings_DirectUnitTests(t *testing.T) {
	t.Parallel()

	t.Run("GetBuildSetting_NilAndNotFound", func(t *testing.T) {
		val, ok := releasecheck.GetBuildSetting(nil, "GOAMD64")
		assert.False(t, ok)
		assert.Empty(t, val)

		bi := &buildinfo.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: "GOOS", Value: "linux"},
			},
		}
		val, ok = releasecheck.GetBuildSetting(bi, "GOAMD64")
		assert.False(t, ok)
		assert.Empty(t, val)

		val, ok = releasecheck.GetBuildSetting(bi, "GOOS")
		assert.True(t, ok)
		assert.Equal(t, "linux", val)
	})

	t.Run("RootExecutable_EdgeCases", func(t *testing.T) {
		err := releasecheck.ValidateRootExecutableISA(releasecheck.ArchAMD64, "microfat", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing executable facts")

		err = releasecheck.ValidateRootExecutableISA(releasecheck.ArchAMD64, "microfat", &releasecheck.ExecutableFacts{
			ELFHeader: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid amd64 ELF binary")

		err = releasecheck.ValidateRootExecutableISA(releasecheck.ArchAMD64, "microfat", &releasecheck.ExecutableFacts{
			ELFHeader: &elf.FileHeader{Machine: elf.EM_AARCH64},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid amd64 ELF binary")

		err = releasecheck.ValidateRootExecutableISA(releasecheck.ArchAMD64, "microfat", &releasecheck.ExecutableFacts{
			ELFHeader: &elf.FileHeader{Machine: elf.EM_X86_64},
			BuildInfo: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing Go buildinfo")

		err = releasecheck.ValidateRootExecutableISA(releasecheck.ArchARM64, "microfat", &releasecheck.ExecutableFacts{
			ELFHeader: &elf.FileHeader{Machine: elf.EM_X86_64},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid arm64 ELF binary")
	})

	t.Run("EmbeddedVariant_EdgeCases", func(t *testing.T) {
		err := releasecheck.ValidateEmbeddedVariantISA(releasecheck.ArchAMD64, "v1", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing variant facts")

		err = releasecheck.ValidateEmbeddedVariantISA(releasecheck.ArchAMD64, "v1", &releasecheck.VariantFacts{
			ELFHeader: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid amd64 ELF binary")

		err = releasecheck.ValidateEmbeddedVariantISA(releasecheck.ArchAMD64, "v1", &releasecheck.VariantFacts{
			ELFHeader: &elf.FileHeader{Machine: elf.EM_AARCH64},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid amd64 ELF binary")

		err = releasecheck.ValidateEmbeddedVariantISA(releasecheck.ArchAMD64, "v1", &releasecheck.VariantFacts{
			ELFHeader: &elf.FileHeader{Machine: elf.EM_X86_64},
			BuildInfo: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing Go buildinfo")

		// Missing required GOAMD64 on non-baseline tier
		err = releasecheck.ValidateEmbeddedVariantISA(releasecheck.ArchAMD64, "v2", &releasecheck.VariantFacts{
			ELFHeader: &elf.FileHeader{Machine: elf.EM_X86_64},
			BuildInfo: &buildinfo.BuildInfo{},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required GOAMD64 setting (expected v2)")

		// Missing required GOARM64 on non-baseline tier
		err = releasecheck.ValidateEmbeddedVariantISA(releasecheck.ArchARM64, "v8.2", &releasecheck.VariantFacts{
			ELFHeader: &elf.FileHeader{Machine: elf.EM_AARCH64},
			BuildInfo: &buildinfo.BuildInfo{},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required GOARM64 setting (expected v8.2)")
	})
}
