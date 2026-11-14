//go:build linux

package e2e_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
)

func TestPrewarmPlanForeignSelectionAndDeduplication(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	arch, levels, buildEnv := archARM64, []string{manifestARM64Base, "v8.1"}, []string{"GOARCH=arm64", stubEnvARM64}
	if currentHostArch == archARM64 {
		arch, levels, buildEnv = archAMD64, []string{"v1", "v2"}, []string{"GOARCH=amd64", envBaselineAMD64}
	}
	foreignStub, payload := filepath.Join(dir, "foreign-stub"), filepath.Join(dir, "foreign-payload")
	require.NoError(t, compileBinary(stubPackagePath, foreignStub, buildEnv))
	require.NoError(t, compileBinary(goldenAppPkg, payload, buildEnv))
	fat := filepath.Join(dir, "foreign.fat")
	output, err := runCacheCheck(t, nil, cliPath, "pack", "--arch", arch, "--stub", foreignStub,
		"-v", levels[0]+"="+payload, "-v", levels[1]+"="+payload, "-o", fat)
	require.NoError(t, err, string(output))
	data, err := os.ReadFile(payload)
	require.NoError(t, err)
	expectedBytes := uint64(len(data))
	for _, all := range []bool{false, true} {
		t.Run("all="+strconv.FormatBool(all), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			cacheDir := filepath.Join(root, "cache")
			selection := []string{prewarmLevelFlag, levels[0]}
			entryCount := 1
			if all {
				selection = []string{prewarmAllFlag}
				entryCount = len(levels)
			}
			plan, stderr, planErr := readPrewarmPlan(t, fat, cacheDir, selection...)
			require.NoError(t, planErr, stderr)
			require.Equal(t, arch, plan.TargetArch)
			require.Len(t, plan.Entries, entryCount)
			require.Equal(t, 1, plan.UniquePayloadCount)
			require.Equal(t, expectedBytes, plan.UniquePayloadBytes, "equal payload identities were counted once per level")
			require.Equal(t, expectedBytes, plan.NeedsMaterializationBytes)
			before := cacheTreeSnapshot(t, root)
			args := append([]string{cacheExecutablePrewarm, fat, prewarmCacheDirFlag, cacheDir}, selection...)
			_, stderr, runErr := runCacheFirstProcess(t, cliPath, cacheFirstEnv(cacheDir),
				append(args, prewarmPayloadCapFlag+"="+strconv.FormatUint(expectedBytes-1, 10))...)
			require.Error(t, runErr, stderr)
			require.Contains(t, stderr, "exceed")
			require.Equal(t, before, cacheTreeSnapshot(t, root))
			output, runErr := runCacheCheck(t, cacheFirstEnv(cacheDir), cliPath,
				append(args, prewarmPayloadCapFlag+"="+strconv.FormatUint(expectedBytes, 10))...)
			require.NoError(t, runErr, string(output))
			entries, readErr := os.ReadDir(cacheDir)
			require.NoError(t, readErr)
			require.Len(t, entries, 1, "duplicate levels should share one cache payload")
			cached, readErr := os.ReadFile(filepath.Join(cacheDir, entries[0].Name()))
			require.NoError(t, readErr)
			require.Equal(t, data, cached)
			warm, stderr, runErr := readPrewarmPlan(t, fat, cacheDir, selection...)
			require.NoError(t, runErr, stderr)
			require.Equal(t, expectedBytes, warm.VerifiedCacheBytes)
			require.Zero(t, warm.NeedsMaterializationBytes)
		})
	}
	t.Run("foreign default selection fails", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		cacheDir := filepath.Join(root, "cache")
		before := cacheTreeSnapshot(t, root)
		_, stderr, runErr := runCacheFirstProcess(t, cliPath, cacheFirstEnv(cacheDir),
			cacheExecutablePrewarm, fat, prewarmPlanFlag, prewarmCacheDirFlag, cacheDir)
		require.Error(t, runErr, stderr)
		require.Equal(t, before, cacheTreeSnapshot(t, root))
	})
}

func TestPrewarmPlanUnsupportedTarget(t *testing.T) {
	t.Parallel()
	fat := rewritePrewarmPlanIndex(t, goldenFatBin, func(index *format.Index) { index.TargetArch = "mips64" })
	for _, selection := range [][]string{{prewarmAllFlag}, {prewarmLevelFlag, updateTier()}} {
		t.Run(selection[0], func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "cache")
			before := cacheTreeSnapshot(t, root)
			args := append([]string{cacheExecutablePrewarm, fat, prewarmPlanFlag, prewarmCacheDirFlag, dir}, selection...)
			_, stderr, err := runCacheFirstProcess(t, cliPath, cacheFirstEnv(dir), args...)
			require.Error(t, err, stderr)
			require.Contains(t, stderr, "architecture")
			require.Equal(t, before, cacheTreeSnapshot(t, root))
		})
	}
}

func TestPrewarmPlanUnavailableDecoderAndVerifiedHit(t *testing.T) {
	t.Parallel()
	fat := rewritePrewarmPlanIndex(t, goldenFatBin, func(index *format.Index) {
		for i := range index.Variants {
			index.Variants[i].Compression = "unavailable-test-codec"
		}
	})
	for _, warm := range []bool{false, true} {
		t.Run("warm="+strconv.FormatBool(warm), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "cache")
			if warm {
				preparePrewarmPlanState(t, prewarmPlanValid, dir)
			}
			before := cacheTreeSnapshot(t, root)
			plan, stderr, err := readPrewarmPlan(t, fat, dir, prewarmLevelFlag, updateTier())
			if warm {
				require.NoError(t, err, stderr)
			} else {
				require.Error(t, err, stderr)
			}
			require.Equal(t, before, cacheTreeSnapshot(t, root))
			require.Len(t, plan.Entries, 1)
			entry := plan.Entries[0]
			require.False(t, entry.DecoderAvailable)
			require.NotEmpty(t, entry.DecoderError)
			require.False(t, entry.MaterializationPossible)
			require.Equal(t, warm, entry.CanPrewarm)
			if warm {
				require.Equal(t, prewarmPlanValid, entry.CacheStatus)
				require.Equal(t, plan.UniquePayloadBytes, plan.VerifiedCacheBytes)
				output, runErr := runCacheCheck(t, cacheFirstEnv(dir), cliPath,
					cacheExecutablePrewarm, fat, prewarmLevelFlag, updateTier(), prewarmCacheDirFlag, dir)
				require.NoError(t, runErr, string(output))
				require.Equal(t, before, cacheTreeSnapshot(t, root))
			} else {
				require.Equal(t, prewarmPlanMissing, entry.CacheStatus)
				require.Equal(t, plan.UniquePayloadBytes, plan.NeedsMaterializationBytes)
			}
		})
	}
}

func rewritePrewarmPlanIndex(t *testing.T, source string, change func(*format.Index)) string {
	t.Helper()
	trailer, index := readTrailerAndIndex(t, source)
	change(index)
	var metadata bytes.Buffer
	_, err := format.WriteIndexAndTrailerWithVersion(&metadata, index, trailer.IndexOffset, index.Version)
	require.NoError(t, err)
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	data = append(data[:trailer.IndexOffset], metadata.Bytes()...)
	fat := filepath.Join(t.TempDir(), "plan-metadata.fat")
	require.NoError(t, os.WriteFile(fat, data, defaultFilePerm))
	return fat
}
