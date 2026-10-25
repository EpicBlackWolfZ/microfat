//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cache"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	testHashPayloadRepeat = 2048
)

type hashCounters struct {
	extract int
	stream  int
	verify  int
}

func installHashCounters(t *testing.T) *hashCounters {
	t.Helper()
	counts := &hashCounters{}
	oldExtract := newExtractHasherFunc
	oldStream := cache.NewStreamHasherFunc
	oldVerify := cache.NewVerifyHasherFunc
	t.Cleanup(func() {
		newExtractHasherFunc = oldExtract
		cache.NewStreamHasherFunc = oldStream
		cache.NewVerifyHasherFunc = oldVerify
	})
	newExtractHasherFunc = func() hash.Hash {
		counts.extract++
		return sha256.New()
	}
	cache.NewStreamHasherFunc = func() hash.Hash {
		counts.stream++
		return sha256.New()
	}
	cache.NewVerifyHasherFunc = func() hash.Hash {
		counts.verify++
		return sha256.New()
	}
	return counts
}

func buildCompressedStubSource(
	t *testing.T,
	payload []byte,
	useDict bool,
) (*os.File, *format.VariantEntry, *format.Index, []byte) {
	t.Helper()
	zstdCodec := codec.NewZstdCodec()
	var dictBytes []byte
	var dictHex string
	if useDict {
		sampleA := bytes.Repeat([]byte("microfat-shared-elf-header-section-"), testHashPayloadRepeat)
		sampleB := append(bytes.Clone(sampleA), []byte("-variant-b")...)
		var trainErr error
		dictBytes, trainErr = codec.TrainDictionary([][]byte{sampleA, sampleB}, codec.DefaultDictSize, "fastest")
		require.NoError(t, trainErr)
		dh := sha256.Sum256(dictBytes)
		dictHex = hex.EncodeToString(dh[:])
	}

	var frame bytes.Buffer
	if useDict {
		require.NoError(t, zstdCodec.CompressWithDict(&frame, payload, "fastest", dictBytes))
	} else {
		require.NoError(t, zstdCodec.Compress(&frame, payload, "fastest"))
	}

	var image bytes.Buffer
	var idx *format.Index
	if useDict {
		image.Write(dictBytes)
		idx = &format.Index{
			Version:          format.FormatVersion2,
			DictionaryOffset: 0,
			DictionarySize:   int64(len(dictBytes)),
			DictionarySHA256: dictHex,
		}
	}
	frameOffset := int64(image.Len())
	image.Write(frame.Bytes())

	srcPath := filepath.Join(t.TempDir(), "fat-source")
	require.NoError(t, os.WriteFile(srcPath, image.Bytes(), 0o600))
	f, err := os.Open(srcPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.Close()) })

	ph := sha256.Sum256(payload)
	entry := &format.VariantEntry{
		Level:            "v1",
		Compression:      codec.AlgorithmZstd,
		Offset:           frameOffset,
		CompressedSize:   int64(frame.Len()),
		UncompressedSize: int64(len(payload)),
		SHA256:           hex.EncodeToString(ph[:]),
	}
	return f, entry, idx, dictBytes
}

func TestColdCacheMaterializationSingleStreamingHashPass(t *testing.T) {
	modes := []string{format.ExecModeCache, format.ExecModeAuto}
	for _, mode := range modes {
		for _, useDict := range []bool{false, true} {
			name := mode + "/dict=" + boolLabel(useDict)
			t.Run(name, func(t *testing.T) {
				_, _, dir, _ := cacheFirstFixture(t)
				payload := bytes.Repeat([]byte("microfat-shared-elf-header-section-payload-"), testHashPayloadRepeat)
				srcFile, entry, idx, _ := buildCompressedStubSource(t, payload, useDict)

				t.Setenv(format.EnvExecMode, mode)
				counts := installHashCounters(t)

				oldOpen, oldCanonical := openCachedBinaryAtFunc, cache.OpenFileAtFunc
				t.Cleanup(func() {
					openCachedBinaryAtFunc, cache.OpenFileAtFunc = oldOpen, oldCanonical
				})
				publishedOpens := 0
				cache.OpenFileAtFunc = func(dirFD int, fileName string) (int, error) {
					fd, err := oldCanonical(dirFD, fileName)
					if err == nil {
						publishedOpens++
					}
					return fd, err
				}
				openCachedBinaryAtFunc = func(dirFD int, fileName string) (int, error) {
					return cache.OpenFileAtFunc(dirFD, fileName)
				}
				memfdCreateFunc = func(string, int) (int, error) {
					return -1, unix.EPERM
				}
				execCalls := 0
				execveFunc = func(procPath string, _, _ []string) error {
					execCalls++
					got, readErr := os.ReadFile(procPath)
					require.NoError(t, readErr)
					require.Equal(t, payload, got)
					return nil
				}

				err := executeVariant(srcFile.Name(), srcFile, entry, idx, []string{testAppArg}, nil,
					microarch.Info{}, microarch.PolicyResult{}, time.Now())
				require.NoError(t, err)
				assert.Equal(t, 1, execCalls)
				assert.Equal(t, 0, counts.extract, "cold cache must not run a duplicate launcher-side streaming hash")
				assert.Equal(t, 1, counts.stream, "cold cache must hash temporary file stream once prior to renameat")
				assert.Equal(t, 1, counts.verify, "cold cache must verify published read-only descriptor once post-renameat")
				assert.Equal(t, 1, publishedOpens)

				entries, readErr := os.ReadDir(dir)
				require.NoError(t, readErr)
				require.Len(t, entries, 1)
				assert.Equal(t, entry.SHA256, entries[0].Name())
			})
		}
	}
}

func TestMemfdAndPrewarmRetainFullSHA256Verification(t *testing.T) {
	t.Run("memfd", func(t *testing.T) {
		_, _, _, _ = cacheFirstFixture(t)
		payload := bytes.Repeat([]byte("memfd-payload-line-"), testHashPayloadRepeat)
		srcFile, entry, idx, _ := buildCompressedStubSource(t, payload, false)

		t.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		counts := installHashCounters(t)
		execCalls := 0
		execveFunc = func(procPath string, _, _ []string) error {
			execCalls++
			got, readErr := os.ReadFile(procPath)
			require.NoError(t, readErr)
			require.Equal(t, payload, got)
			return nil
		}

		err := executeVariant(srcFile.Name(), srcFile, entry, idx, []string{testAppArg}, nil,
			microarch.Info{}, microarch.PolicyResult{}, time.Now())
		require.NoError(t, err)
		assert.Equal(t, 1, execCalls)
		assert.Equal(t, 1, counts.extract, "memfd extraction must verify streaming SHA-256 before sealing")
		assert.Equal(t, 0, counts.stream)
		assert.Equal(t, 0, counts.verify)
	})

	t.Run("prewarm", func(t *testing.T) {
		payload := bytes.Repeat([]byte("microfat-shared-elf-header-section-prewarm-"), testHashPayloadRepeat)
		srcFile, entry, _, dictBytes := buildCompressedStubSource(t, payload, true)
		cacheDir := t.TempDir()
		require.NoError(t, os.Chmod(cacheDir, privateCacheDirMode))

		counts := installHashCounters(t)
		cachedPath, alreadyCached, _, err := pack.PrewarmVariantWithDict(srcFile, entry, cacheDir, dictBytes)
		require.NoError(t, err)
		assert.False(t, alreadyCached)
		assert.FileExists(t, cachedPath)
		assert.Equal(t, 1, counts.stream, "prewarm must hash temporary file stream once")
		assert.Equal(t, 1, counts.verify, "prewarm must verify published descriptor once")
		assert.Equal(t, 0, counts.extract)
	})
}

func TestColdCacheMaterializationCorruptionAndCleanupContracts(t *testing.T) {
	payload := bytes.Repeat([]byte("microfat-shared-elf-header-section-clean-"), testHashPayloadRepeat)

	t.Run("corrupt_compressed_stream", func(t *testing.T) {
		_, _, dir, _ := cacheFirstFixture(t)
		srcFile, entry, idx, _ := buildCompressedStubSource(t, payload, false)
		raw, err := os.ReadFile(srcFile.Name())
		require.NoError(t, err)
		for i := range raw {
			raw[i] ^= 0xff
		}
		require.NoError(t, os.WriteFile(srcFile.Name(), raw, 0o600))

		t.Setenv(format.EnvExecMode, format.ExecModeCache)
		execveFunc = func(string, []string, []string) error { t.Fatal("must not exec corrupt stream"); return nil }

		err = executeVariant(srcFile.Name(), srcFile, entry, idx, []string{testAppArg}, nil,
			microarch.Info{}, microarch.PolicyResult{}, time.Now())
		require.ErrorIs(t, err, format.ErrCacheExtract)
		require.ErrorIs(t, err, codec.ErrDecompressionFailed)
		assertNoTempOrPublishedFiles(t, dir)
	})

	t.Run("bit_flipped_decompressed_payload_digest_mismatch", func(t *testing.T) {
		_, _, dir, _ := cacheFirstFixture(t)
		tamperedPayload := bytes.Clone(payload)
		tamperedPayload[0] ^= 0x01
		srcFile, entry, idx, _ := buildCompressedStubSource(t, tamperedPayload, false)
		expectedSum := sha256.Sum256(payload)
		entry.SHA256 = hex.EncodeToString(expectedSum[:])

		t.Setenv(format.EnvExecMode, format.ExecModeCache)
		execveFunc = func(string, []string, []string) error { t.Fatal("must not exec digest-mismatched payload"); return nil }

		err := executeVariant(srcFile.Name(), srcFile, entry, idx, []string{testAppArg}, nil,
			microarch.Info{}, microarch.PolicyResult{}, time.Now())
		require.ErrorIs(t, err, format.ErrCacheExtract)
		require.ErrorIs(t, err, format.ErrPayloadCorrupted)
		assertNoTempOrPublishedFiles(t, dir)
	})

	t.Run("corrupt_shared_dictionary", func(t *testing.T) {
		_, _, dir, _ := cacheFirstFixture(t)
		srcFile, entry, idx, _ := buildCompressedStubSource(t, payload, true)
		raw, err := os.ReadFile(srcFile.Name())
		require.NoError(t, err)
		raw[0] ^= 0xff
		require.NoError(t, os.WriteFile(srcFile.Name(), raw, 0o600))

		t.Setenv(format.EnvExecMode, format.ExecModeCache)
		execveFunc = func(string, []string, []string) error { t.Fatal("must not exec with corrupt dictionary"); return nil }

		err = executeVariant(srcFile.Name(), srcFile, entry, idx, []string{testAppArg}, nil,
			microarch.Info{}, microarch.PolicyResult{}, time.Now())
		require.ErrorIs(t, err, format.ErrCacheExtract)
		require.ErrorIs(t, err, format.ErrDictionaryCorrupted)
		assertNoTempOrPublishedFiles(t, dir)
	})

	t.Run("invalid_dictionary_checksum_metadata", func(t *testing.T) {
		_, _, dir, _ := cacheFirstFixture(t)
		srcFile, entry, idx, _ := buildCompressedStubSource(t, payload, true)
		idx.DictionarySHA256 = "invalid"

		t.Setenv(format.EnvExecMode, format.ExecModeCache)
		execveFunc = func(string, []string, []string) error {
			t.Fatal("must not exec with invalid dictionary checksum")
			return nil
		}

		err := executeVariant(srcFile.Name(), srcFile, entry, idx, []string{testAppArg}, nil,
			microarch.Info{}, microarch.PolicyResult{}, time.Now())
		require.ErrorIs(t, err, format.ErrCacheExtract)
		require.ErrorIs(t, err, format.ErrInvalidChecksum)
		assertNoTempOrPublishedFiles(t, dir)
	})

	t.Run("truncated_dictionary_read", func(t *testing.T) {
		_, _, dir, _ := cacheFirstFixture(t)
		srcFile, entry, idx, _ := buildCompressedStubSource(t, payload, true)
		idx.DictionaryOffset = entry.Offset + entry.CompressedSize + 1

		t.Setenv(format.EnvExecMode, format.ExecModeCache)
		execveFunc = func(string, []string, []string) error { t.Fatal("must not exec with truncated dictionary"); return nil }

		err := executeVariant(srcFile.Name(), srcFile, entry, idx, []string{testAppArg}, nil,
			microarch.Info{}, microarch.PolicyResult{}, time.Now())
		require.ErrorIs(t, err, format.ErrCacheExtract)
		assertNoTempOrPublishedFiles(t, dir)
	})

	t.Run("uncompressed_size_mismatch", func(t *testing.T) {
		_, _, dir, _ := cacheFirstFixture(t)
		srcFile, entry, idx, _ := buildCompressedStubSource(t, payload, false)
		entry.UncompressedSize++

		t.Setenv(format.EnvExecMode, format.ExecModeCache)
		execveFunc = func(string, []string, []string) error { t.Fatal("must not exec size-mismatched payload"); return nil }

		err := executeVariant(srcFile.Name(), srcFile, entry, idx, []string{testAppArg}, nil,
			microarch.Info{}, microarch.PolicyResult{}, time.Now())
		require.ErrorIs(t, err, format.ErrCacheExtract)
		require.ErrorIs(t, err, codec.ErrSizeMismatch)
		assertNoTempOrPublishedFiles(t, dir)
	})

	t.Run("symlink_cache_entry_untouched", func(t *testing.T) {
		_, _, dir, _ := cacheFirstFixture(t)
		srcFile, entry, idx, _ := buildCompressedStubSource(t, payload, false)
		outsideTarget := filepath.Join(t.TempDir(), "victim")
		require.NoError(t, os.WriteFile(outsideTarget, []byte("keep"), 0o600))
		linkPath := filepath.Join(dir, entry.SHA256)
		require.NoError(t, os.Symlink(outsideTarget, linkPath))

		t.Setenv(format.EnvExecMode, format.ExecModeCache)
		execveFunc = func(string, []string, []string) error { t.Fatal("must not exec symlink cache entry"); return nil }

		err := executeVariant(srcFile.Name(), srcFile, entry, idx, []string{testAppArg}, nil,
			microarch.Info{}, microarch.PolicyResult{}, time.Now())
		require.ErrorIs(t, err, format.ErrCacheWrite)

		info, lstatErr := os.Lstat(linkPath)
		require.NoError(t, lstatErr)
		assert.NotZero(t, info.Mode()&os.ModeSymlink)
		victimBytes, readErr := os.ReadFile(outsideTarget)
		require.NoError(t, readErr)
		assert.Equal(t, "keep", string(victimBytes))
		assertNoTempFiles(t, dir)
	})
}

func assertNoTempOrPublishedFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "failed cold-cache materialization must unlink temporary files and publish nothing")
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), ".exec-"), "unexpected leftover temp file %s", entry.Name())
	}
}

func boolLabel(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func TestDecompressVariantToWriterBoundsAllProfiles(t *testing.T) {
	t.Parallel()
	payload := []byte("minimal-profile-bounds-check")
	srcFile, baseEntry, baseIdx, _ := buildCompressedStubSource(t, payload, true)

	t.Run("dictionary_bounds", func(t *testing.T) {
		t.Parallel()
		idx := *baseIdx
		idx.DictionaryOffset = -1
		err := decompressVariantToWriter(srcFile, baseEntry, &idx, &bytes.Buffer{})
		require.ErrorIs(t, err, format.ErrInvalidDictionary)
	})

	t.Run("payload_size_bounds", func(t *testing.T) {
		t.Parallel()
		entry := *baseEntry
		entry.UncompressedSize = 0
		err := decompressVariantToWriter(srcFile, &entry, nil, &bytes.Buffer{})
		require.ErrorIs(t, err, format.ErrPayloadTooLarge)
	})

	t.Run("invalid_entry_checksum", func(t *testing.T) {
		t.Parallel()
		entry := *baseEntry
		entry.SHA256 = "bad"
		err := decompressVariantToWriter(srcFile, &entry, nil, &bytes.Buffer{})
		require.ErrorIs(t, err, format.ErrInvalidChecksum)
	})

	t.Run("unsupported_codec", func(t *testing.T) {
		t.Parallel()
		entry := *baseEntry
		entry.Compression = "unsupported-codec"
		err := decompressVariantToWriter(srcFile, &entry, nil, &bytes.Buffer{})
		require.ErrorIs(t, err, codec.ErrUnsupportedCodec)
	})
}
