//go:build linux

package e2e_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
)

func TestPrewarmPlanCompatibility(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	profiles := []cacheFirstProfile{
		{name: launcherFullProfile, path: stubPath},
		{name: launcherMinimalProfile, path: buildMinimalLauncherStub(t, dir)},
	}
	codecs := []checksumCasingCodec{
		{name: creationEpochNone, algorithm: creationEpochNone},
		{name: creationEpochLZ4, algorithm: creationEpochLZ4},
		{name: mountCodecZstd, algorithm: mountCodecZstd},
		{name: "zstd-dictionary", algorithm: mountCodecZstd, dictionary: true},
	}
	for _, profile := range profiles {
		for _, compression := range codecs {
			base := packChecksumCasingFixture(t, dir, profile, compression)
			for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
				t.Run(profile.name+"/"+compression.name+"/format"+strconv.Itoa(version), func(t *testing.T) {
					t.Parallel()
					fat, _ := rewriteChecksumCasingFixture(t, base, version, strings.ToLower, compression.dictionary)
					requirePlannedMaterialization(t, fat)
				})
			}
		}
	}
}

func requirePlannedMaterialization(t *testing.T, fat string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "cache")
	before := cacheTreeSnapshot(t, root)
	plan, stderr, err := readPrewarmPlan(t, fat, dir, prewarmAllFlag)
	require.NoError(t, err, stderr)
	require.Equal(t, before, cacheTreeSnapshot(t, root))
	require.Len(t, plan.Entries, len(goldenVariantBins))
	require.Equal(t, plan.UniquePayloadBytes, plan.NeedsMaterializationBytes)
	require.Zero(t, plan.VerifiedCacheBytes)
	require.Zero(t, plan.BlockedPayloadBytes)
	for _, entry := range plan.Entries {
		data, readErr := os.ReadFile(goldenVariantBins[entry.Level])
		require.NoError(t, readErr)
		require.Equal(t, uint64(len(data)), entry.UncompressedSize)
		require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(data)), entry.SHA256)
		require.Equal(t, prewarmPlanMissing, entry.CacheStatus)
		require.True(t, entry.DecoderAvailable)
		require.True(t, entry.MaterializationPossible)
	}
	output, err := runCacheCheck(t, cacheFirstEnv(dir), cliPath, cacheExecutablePrewarm, fat, prewarmAllFlag, prewarmCacheDirFlag, dir,
		prewarmPayloadCapFlag+"="+strconv.FormatUint(plan.UniquePayloadBytes, 10))
	require.NoError(t, err, string(output))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var actualBytes uint64
	for _, entry := range entries {
		require.True(t, entry.Type().IsRegular(), "unexpected materialization artifact: %s", entry.Name())
		data, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, readErr)
		require.Equal(t, entry.Name(), fmt.Sprintf("%x", sha256.Sum256(data)))
		actualBytes += uint64(len(data))
	}
	require.Equal(t, plan.UniquePayloadBytes, actualBytes, "planned logical bytes differ from extracted outputs")
	before = cacheTreeSnapshot(t, root)
	warm, stderr, err := readPrewarmPlan(t, fat, dir, prewarmAllFlag)
	require.NoError(t, err, stderr)
	require.Equal(t, before, cacheTreeSnapshot(t, root))
	require.Equal(t, actualBytes, warm.VerifiedCacheBytes)
	require.Zero(t, warm.NeedsMaterializationBytes)
	require.Zero(t, warm.BlockedPayloadBytes)
	for _, entry := range warm.Entries {
		require.Equal(t, prewarmPlanValid, entry.CacheStatus)
	}
}

func TestPrewarmPlanStaleCacheObservation(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "cache")
	preparePrewarmPlanState(t, prewarmPlanValid, dir)
	plan, stderr, err := readPrewarmPlan(t, goldenFatBin, dir, prewarmLevelFlag, updateTier())
	require.NoError(t, err, stderr)
	require.Equal(t, plan.UniquePayloadBytes, plan.VerifiedCacheBytes)
	require.Len(t, plan.Entries, 1)
	entry := filepath.Join(dir, plan.Entries[0].SHA256)
	original, err := os.ReadFile(entry)
	require.NoError(t, err)
	corrupt := bytes.Clone(original)
	corrupt[0] ^= 0xff
	require.NoError(t, os.WriteFile(entry, corrupt, defaultFilePerm))
	output, err := runCacheCheck(t, cacheFirstEnv(dir), cliPath,
		cacheExecutablePrewarm, goldenFatBin, prewarmLevelFlag, updateTier(), prewarmCacheDirFlag, dir,
		prewarmPayloadCapFlag+"="+strconv.FormatUint(plan.UniquePayloadBytes, 10))
	require.NoError(t, err, string(output))
	repaired, err := os.ReadFile(entry)
	require.NoError(t, err)
	require.Equal(t, original, repaired, "apply trusted the stale plan instead of validating the cache descriptor")
	// A safe plan cannot authorize an unsafe entry installed after the observation.
	_, stderr, err = readPrewarmPlan(t, goldenFatBin, dir, prewarmLevelFlag, updateTier())
	require.NoError(t, err, stderr)
	prepareUnsafeCacheFirstEntry(t, entry, cacheFirstSymlink)
	before := cacheTreeSnapshot(t, dir)
	output, err = runCacheCheck(t, cacheFirstEnv(dir), cliPath,
		cacheExecutablePrewarm, goldenFatBin, prewarmLevelFlag, updateTier(), prewarmCacheDirFlag, dir,
		prewarmPayloadCapFlag+"="+strconv.FormatUint(plan.UniquePayloadBytes, 10))
	require.Error(t, err, string(output))
	require.Equal(t, before, cacheTreeSnapshot(t, dir))
}

func TestPrewarmPlanDoesNotVerifyEmbeddedPayloadContents(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fat := filepath.Join(root, "corrupt-payload.fat")
	copyFile(t, goldenFatBin, fat)
	_, index := readTrailerAndIndex(t, fat)
	selected, found := index.FindVariant(updateTier())
	require.True(t, found)
	mutateFileBytes(t, fat, selected.Offset, bytes.Repeat([]byte{0xff}, int(trailerMagicSizeBytes)))
	dir := filepath.Join(root, "cache")
	before := cacheTreeSnapshot(t, root)
	plan, stderr, err := readPrewarmPlan(t, fat, dir, prewarmLevelFlag, updateTier())
	require.NoError(t, err, stderr)
	require.Equal(t, before, cacheTreeSnapshot(t, root))
	require.Len(t, plan.Entries, 1)
	require.Equal(t, selected.SHA256, plan.Entries[0].SHA256)
	require.Equal(t, prewarmPlanMissing, plan.Entries[0].CacheStatus)
	output, err := runCacheCheck(t, cacheFirstEnv(dir), cliPath,
		cacheExecutablePrewarm, fat, prewarmLevelFlag, updateTier(), prewarmCacheDirFlag, dir)
	require.Error(t, err, string(output))
}
