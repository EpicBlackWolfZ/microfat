//go:build linux

package e2e_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
)

type checksumCasingCodec struct {
	name       string
	algorithm  string
	dictionary bool
}

func TestChecksumCasingRuntimeMatrix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	profiles := []cacheFirstProfile{
		{name: launcherFullProfile, path: stubPath},
		{name: launcherMinimalProfile, path: buildMinimalLauncherStub(t, dir)},
	}
	codecs := []checksumCasingCodec{
		{name: codec.AlgorithmNone, algorithm: codec.AlgorithmNone},
		{name: codec.AlgorithmLZ4, algorithm: codec.AlgorithmLZ4},
		{name: mountCodecZstd, algorithm: mountCodecZstd},
		{name: "zstd-dictionary", algorithm: mountCodecZstd, dictionary: true},
	}
	casings := []struct {
		name   string
		change func(string) string
	}{
		{name: "lowercase", change: strings.ToLower},
		{name: "uppercase", change: strings.ToUpper},
		{name: "mixed-case", change: mixedChecksumCasing},
	}
	for _, profile := range profiles {
		for _, codec := range codecs {
			base := packChecksumCasingFixture(t, dir, profile, codec)
			for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
				for _, casing := range casings {
					t.Run(profile.name+"/"+codec.name+"/v"+strconv.Itoa(version)+"/"+casing.name, func(t *testing.T) {
						t.Parallel()
						fat, digest := rewriteChecksumCasingFixture(t, base, version, casing.change, codec.dictionary)
						lowercase, _ := rewriteChecksumCasingFixture(t, base, version, strings.ToLower, codec.dictionary)
						verifyFatIntegrity(t, fat)
						assertChecksumCasingDispatch(t, fat, lowercase, digest)
					})
				}
			}
		}
	}
}

func packChecksumCasingFixture(t *testing.T, dir string, profile cacheFirstProfile, codec checksumCasingCodec) string {
	t.Helper()
	fat := filepath.Join(dir, profile.name+"-"+codec.name)
	args := []string{inputPackCommand, inputStubFlag, profile.path, mountArchFlag,
		currentHostArch, "--compression", codec.algorithm, "-o", fat}
	if codec.dictionary {
		args = append(args, "--dict")
	}
	for level, path := range goldenVariantBins {
		args = append(args, "-v", level+"="+path)
	}
	output, err := exec.Command(cliPath, args...).CombinedOutput()
	require.NoError(t, err, string(output))
	return fat
}

func rewriteChecksumCasingFixture(
	t *testing.T, source string, version int, change func(string) string, dictionary bool,
) (string, string) {
	t.Helper()
	trailer, index := readTrailerAndIndex(t, source)
	selected, found := index.FindVariant(updateTier())
	require.True(t, found)
	digest := selected.SHA256
	for i := range index.Variants {
		index.Variants[i].SHA256 = change(index.Variants[i].SHA256)
	}
	if dictionary {
		require.Positive(t, index.DictionarySize, "fixture must exercise dictionary checksum verification")
		index.DictionarySHA256 = change(index.DictionarySHA256)
	}
	var metadata bytes.Buffer
	_, err := format.WriteIndexAndTrailerWithVersion(&metadata, index, trailer.IndexOffset, version)
	require.NoError(t, err)
	wire := metadata.Bytes()[:metadata.Len()-format.TrailerSize]
	if version == format.FormatVersion2 {
		require.True(t, bytes.HasPrefix(wire, []byte(format.IndexMagicV2)), "fixture must use the actual binary v2 index")
	} else {
		require.True(t, bytes.HasPrefix(wire, []byte("{")), "fixture must use the legacy JSON v1 index")
	}
	// Inspect serialized bytes rather than reparsing: parser normalization must
	// not silently turn the uppercase/mixed-case fixtures into lowercase inputs.
	for _, variant := range index.Variants {
		require.Contains(t, string(wire), variant.SHA256)
	}
	if dictionary {
		require.Contains(t, string(wire), index.DictionarySHA256)
	}
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	data = append(data[:trailer.IndexOffset], metadata.Bytes()...)
	fat := filepath.Join(t.TempDir(), "checksum-casing.fat")
	require.NoError(t, os.WriteFile(fat, data, defaultFilePerm))
	return fat, digest
}

func mixedChecksumCasing(digest string) string {
	var result strings.Builder
	uppercase := false
	for _, digit := range digest {
		if digit >= 'a' && digit <= 'f' {
			uppercase = !uppercase
			if uppercase {
				digit -= 'a' - 'A'
			}
		}
		result.WriteRune(digit)
	}
	return result.String()
}

func assertChecksumCasingDispatch(t *testing.T, fat, lowercase, digest string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cache")
	env := append(cacheFirstEnv(dir), "MICROFAT_FORCE_LEVEL="+updateTier())
	requireCacheFirstMode(t, fat, append(env, "MICROFAT_EXEC_MODE=memfd"), execModeMemfd)
	_, err := os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist, "memfd execution materialized the cache")

	requireCacheFirstMode(t, fat, append(env, envExecCache), execModeCache)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, strings.ToLower(digest), entries[0].Name(), "cache identity must use canonical lowercase SHA-256")
	entry := filepath.Join(dir, entries[0].Name())
	original, err := os.Stat(entry)
	require.NoError(t, err)
	before := cacheTreeSnapshot(t, dir)
	requireCacheFirstMode(t, fat, env, execModeCache)
	// Differently cased wire digests identify the same already materialized
	// lowercase cache pathname and descriptor, across executable artifacts.
	requireCacheFirstMode(t, lowercase, env, execModeCache)
	require.Equal(t, before, cacheTreeSnapshot(t, dir), "warm auto dispatch modified the cache")
	after, err := os.Stat(entry)
	require.NoError(t, err)
	require.True(t, os.SameFile(original, after), "warm auto dispatch replaced the cache inode")
	require.Equal(t, original.ModTime(), after.ModTime())

	// Same-sized executable bytes must still be verified at the warm boundary.
	// A rejected safe entry stays untouched while auto dispatch uses memfd.
	corrupt, err := os.ReadFile(entry)
	require.NoError(t, err)
	require.NotEmpty(t, corrupt)
	corrupt[len(corrupt)-1] ^= 0xff
	require.NoError(t, retryFixtureBusy(func() error { return os.WriteFile(entry, corrupt, defaultFilePerm) }))
	before = cacheTreeSnapshot(t, dir)
	requireCacheFirstMode(t, fat, env, execModeMemfd)
	require.Equal(t, before, cacheTreeSnapshot(t, dir), "auto dispatch modified the corrupt cache entry")
}
