package releasecheck_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func createValidFatBinary(t *testing.T, arch string, tiers []string) []byte {
	t.Helper()
	// Minimal valid ELF header: \x7fELF (64-bit, little endian, exec, x86_64 or aarch64)
	elfMachine := byte(0x3e) // AMD64
	if arch == "arm64" {
		elfMachine = byte(0xb7) // AARCH64
	}
	elfHeader := []byte{
		0x7f, 'E', 'L', 'F', // Magic
		2, 1, 1, 0, // 64-bit, LE, v1, System V
		0, 0, 0, 0, 0, 0, 0, 0, // Padding
		2, 0, // ET_EXEC
		elfMachine, 0, // e_machine
		1, 0, 0, 0, // e_version
	}
	stubBytes := append(append([]byte(nil), elfHeader...), bytes.Repeat([]byte{0x90}, 256)...)

	c, err := codec.Get("none")
	require.NoError(t, err)

	var compVariantsBuf bytes.Buffer
	var variantEntries []format.VariantEntry

	for _, tier := range tiers {
		variantPayload := append(append([]byte(nil), stubBytes...), []byte("-variant-"+tier)...)
		h := sha256.Sum256(variantPayload)
		tierHash := hex.EncodeToString(h[:])

		var compBuf bytes.Buffer
		require.NoError(t, c.Compress(&compBuf, variantPayload, "fastest"))

		vEntry := format.VariantEntry{
			Level:            tier,
			Offset:           int64(len(stubBytes) + compVariantsBuf.Len()),
			CompressedSize:   int64(compBuf.Len()),
			UncompressedSize: int64(len(variantPayload)),
			SHA256:           tierHash,
			Compression:      "none",
		}
		variantEntries = append(variantEntries, vEntry)
		compVariantsBuf.Write(compBuf.Bytes())
	}

	idx := &format.Index{
		Version:    format.FormatVersion2,
		TargetArch: arch,
		Variants:   variantEntries,
	}

	var fatBuf bytes.Buffer
	fatBuf.Write(stubBytes)
	fatBuf.Write(compVariantsBuf.Bytes())
	_, err = format.WriteIndexAndTrailer(&fatBuf, idx, int64(fatBuf.Len()))
	require.NoError(t, err)

	return fatBuf.Bytes()
}

func createStandardValidEntries(t *testing.T, arch string, contract *releasecheck.ReleaseContract) []tarEntry {
	t.Helper()
	tiers := contract.ExpectedTiers[arch]
	fatData := createValidFatBinary(t, arch, tiers)
	stubData := []byte("\x7fELF\x02\x01\x01\x00" + strings.Repeat("\x00", 64))

	return []tarEntry{
		{Name: releasecheck.ReleaseProjectName, Data: fatData, Mode: 0o755},
		{Name: releasecheck.ReleaseFullStub, Data: stubData, Mode: 0o755},
		{Name: releasecheck.ReleaseMinStub, Data: stubData, Mode: 0o755},
		{Name: "README.md", Data: []byte("# README"), Mode: 0o644},
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
			Linkname: "microfat",
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
		Name: "README.md",
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
		Name: "README.md",
		Data: []byte("dupe"),
		Mode: 0o644,
	})
	dupeArchive := filepath.Join(t.TempDir(), "dupe.tar.gz")
	createOrderedTarGz(t, dupeArchive, dupeEntries)

	err = releasecheck.ExtractFileFromArchive(dupeArchive, releasecheck.ReleaseProjectName, filepath.Join(t.TempDir(), "out2"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate entry in archive")
}
