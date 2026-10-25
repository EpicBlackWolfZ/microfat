package pack

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testParallelEpoch          int64 = 1761401000
	testParallelVariantPayload       = 600 * 1024
	testParallelWorkersFour          = 4
	testParallelWorkersEight         = 8
	testParallelMem64MiB       int64 = 64 * 1024 * 1024
	testParallelMem128MiB      int64 = 128 * 1024 * 1024
	testParallelMem256MiB      int64 = 256 * 1024 * 1024
	testParallelMem512MiB      int64 = 512 * 1024 * 1024
	testParallelMem2GiB        int64 = 2 * 1024 * 1024 * 1024
	testParallelSize28MiB      int64 = 28 * 1024 * 1024
	testParallelSize74MiB      int64 = 74 * 1024 * 1024
	testParallelExistingMode         = 0o600
	testParallelSnapshotMode         = 0o755
)

func installPackResourceHooks(
	t *testing.T,
	gomaxprocs int,
	limits cgroup.Limits,
	limitsErr error,
	gomemlimit int64,
	hostAvail int64,
	hostOK bool,
) {
	t.Helper()
	origProcs := readPackGOMAXPROCSFunc
	origLimits := readPackCgroupLimitsFunc
	origGoMem := readPackGOMEMLIMITFunc
	origAvail := readHostMemAvailableFunc
	t.Cleanup(func() {
		readPackGOMAXPROCSFunc = origProcs
		readPackCgroupLimitsFunc = origLimits
		readPackGOMEMLIMITFunc = origGoMem
		readHostMemAvailableFunc = origAvail
	})

	readPackGOMAXPROCSFunc = func() int { return gomaxprocs }
	readPackCgroupLimitsFunc = func() (cgroup.Limits, error) { return limits, limitsErr }
	readPackGOMEMLIMITFunc = func() int64 { return gomemlimit }
	readHostMemAvailableFunc = func() (int64, bool) { return hostAvail, hostOK }
}

func makeDistinctVariantFixture(t *testing.T, dir, label string, size int) string {
	t.Helper()
	basePath, header := compressionContractInput(t)
	baseBytes, err := os.ReadFile(basePath)
	require.NoError(t, err)

	buf := make([]byte, max(size, len(baseBytes)))
	pattern := []byte("variant-" + label + "-deterministic-payload-block\n")
	for off := 0; off < len(buf); off += len(pattern) {
		copy(buf[off:], pattern)
	}
	copy(buf, header)
	binary.LittleEndian.PutUint64(buf[96:104], uint64(len(buf)))
	binary.LittleEndian.PutUint64(buf[104:112], uint64(len(buf)))

	out := filepath.Join(dir, "variant-"+label+".elf")
	require.NoError(t, os.WriteFile(out, buf, testParallelSnapshotMode))
	return out
}

func TestPackParallelByteForByteEquivalence(t *testing.T) {
	dir := t.TempDir()
	stubPath := makeDistinctVariantFixture(t, dir, "stub", 64*1024)
	variants := map[string]string{
		"v1": makeDistinctVariantFixture(t, dir, "v1", testParallelVariantPayload),
		"v2": makeDistinctVariantFixture(t, dir, "v2", testParallelVariantPayload+4096),
		"v3": makeDistinctVariantFixture(t, dir, "v3", testParallelVariantPayload+8192),
		"v4": makeDistinctVariantFixture(t, dir, "v4", testParallelVariantPayload+12288),
	}

	cases := []struct {
		name       string
		profile    string
		enableDict bool
		overrides  map[string]VariantCompressionOptions
	}{
		{name: "latency", profile: codec.ProfileLatency},
		{name: "balanced-no-dict", profile: codec.ProfileBalanced},
		{name: "balanced-forced-dict", profile: codec.ProfileBalanced, enableDict: true},
		{name: "size-auto-dict", profile: codec.ProfileSize},
		{
			name:       "mixed-overrides-with-dict",
			profile:    codec.ProfileBalanced,
			enableDict: true,
			overrides: map[string]VariantCompressionOptions{
				"v1": {Compression: codec.AlgorithmNone},
				"v2": {Compression: codec.AlgorithmLZ4 + ":best"},
				"v3": {Compression: codec.AlgorithmZstd + ":fastest"},
				"v4": {Compression: codec.AlgorithmZstd + ":best"},
			},
		},
	}

	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("v%d/%s", version, tc.name), func(t *testing.T) {
				epoch := testParallelEpoch
				baseOpts := DefaultOptions()
				baseOpts.StubPath = stubPath
				baseOpts.FormatVersion = version
				baseOpts.Profile = tc.profile
				baseOpts.EnableDict = tc.enableDict
				baseOpts.CreatedUnix = &epoch
				baseOpts.Variants = variants
				baseOpts.VariantCompression = tc.overrides

				serialOut := filepath.Join(t.TempDir(), "serial.fat")
				serialOpts := baseOpts
				serialOpts.OutputPath = serialOut
				installPackResourceHooks(t, 1, cgroup.Limits{}, nil, math.MaxInt64, testParallelMem2GiB, true)
				serialIdx, err := Pack(serialOpts)
				require.NoError(t, err)

				parallelOut := filepath.Join(t.TempDir(), "parallel.fat")
				parallelOpts := baseOpts
				parallelOpts.OutputPath = parallelOut
				installPackResourceHooks(
					t, testParallelWorkersFour, cgroup.Limits{}, nil, math.MaxInt64, testParallelMem2GiB, true,
				)
				parallelIdx, err := Pack(parallelOpts)
				require.NoError(t, err)

				assert.Equal(t, serialIdx, parallelIdx)

				serialBytes, err := os.ReadFile(serialOut)
				require.NoError(t, err)
				parallelBytes, err := os.ReadFile(parallelOut)
				require.NoError(t, err)
				assert.True(t, bytes.Equal(serialBytes, parallelBytes),
					"parallel Pack artifact must be byte-for-byte identical to serial Pack")

				_, results, verifyErr := VerifyBinary(bytes.NewReader(parallelBytes), int64(len(parallelBytes)))
				require.NoError(t, verifyErr)
				require.Len(t, results, len(variants))
				for _, res := range results {
					assert.True(t, res.Valid)
				}
			})
		}
	}
}

func TestResolvePackWorkerCountAdmissionPolicy(t *testing.T) {
	makePlans := func(count int, size int64, algo, lvl string) []variantWorkPlan {
		plans := make([]variantWorkPlan, count)
		for i := range count {
			plans[i] = variantWorkPlan{
				level:          fmt.Sprintf("v%d", i+1),
				normalizedLvl:  fmt.Sprintf("v%d", i+1),
				statSize:       size,
				resolvedLevel:  lvl,
				estimatedBytes: estimateVariantWorkerMemoryBytes(size, algo, lvl),
			}
		}
		return plans
	}

	t.Run("single_variant_stays_serial", func(t *testing.T) {
		installPackResourceHooks(t, testParallelWorkersEight, cgroup.Limits{}, nil, math.MaxInt64, testParallelMem2GiB, true)
		assert.Equal(t, 1, resolvePackWorkerCount(makePlans(1, testParallelSize28MiB, codec.AlgorithmZstd, "default")))
	})

	t.Run("gomaxprocs_one_stays_serial", func(t *testing.T) {
		installPackResourceHooks(t, 1, cgroup.Limits{}, nil, math.MaxInt64, testParallelMem2GiB, true)
		assert.Equal(t, 1, resolvePackWorkerCount(makePlans(3, testParallelSize28MiB, codec.AlgorithmZstd, "default")))
	})

	t.Run("cgroup_cpu_quota_one_clamps_to_serial", func(t *testing.T) {
		installPackResourceHooks(
			t, testParallelWorkersEight, cgroup.Limits{CPUs: 1}, nil, math.MaxInt64, testParallelMem2GiB, true,
		)
		assert.Equal(t, 1, resolvePackWorkerCount(makePlans(3, testParallelSize28MiB, codec.AlgorithmZstd, "default")))
	})

	t.Run("tight_64_128_256_mib_envelopes_fall_back_to_serial", func(t *testing.T) {
		fortioPlans := makePlans(3, testParallelSize28MiB, codec.AlgorithmZstd, "better")
		for _, limit := range []int64{testParallelMem64MiB, testParallelMem128MiB, testParallelMem256MiB} {
			installPackResourceHooks(
				t, testParallelWorkersFour,
				cgroup.Limits{MemoryLimitBytes: limit, EffectiveMemoryLimitBytes: limit, MemoryLimitFinite: true},
				nil, math.MaxInt64, testParallelMem2GiB, true,
			)
			assert.Equal(t, 1, resolvePackWorkerCount(fortioPlans), "limit=%d must stay serial", limit)
		}
	})

	t.Run("task_size_512mib_stays_serial_but_2gib_parallelizes", func(t *testing.T) {
		taskSizePlans := makePlans(3, testParallelSize74MiB, codec.AlgorithmZstd, "best")
		installPackResourceHooks(
			t, testParallelWorkersFour,
			cgroup.Limits{
				MemoryLimitBytes:          testParallelMem512MiB,
				EffectiveMemoryLimitBytes: testParallelMem512MiB,
				MemoryLimitFinite:         true,
			},
			nil, math.MaxInt64, testParallelMem2GiB, true,
		)
		assert.Equal(t, 1, resolvePackWorkerCount(taskSizePlans))

		installPackResourceHooks(
			t, testParallelWorkersFour,
			cgroup.Limits{
				MemoryLimitBytes:          testParallelMem2GiB,
				EffectiveMemoryLimitBytes: testParallelMem2GiB,
				MemoryLimitFinite:         true,
			},
			nil, math.MaxInt64, testParallelMem2GiB, true,
		)
		assert.Equal(t, 3, resolvePackWorkerCount(taskSizePlans))
	})

	t.Run("gomemlimit_tighter_than_cgroup_controls_admission", func(t *testing.T) {
		plans := makePlans(3, testParallelSize28MiB, codec.AlgorithmZstd, "default")
		installPackResourceHooks(
			t, testParallelWorkersFour,
			cgroup.Limits{
				MemoryLimitBytes:          testParallelMem2GiB,
				EffectiveMemoryLimitBytes: testParallelMem2GiB,
				MemoryLimitFinite:         true,
			},
			nil, testParallelMem128MiB, testParallelMem2GiB, true,
		)
		assert.Equal(t, 1, resolvePackWorkerCount(plans))
	})

	t.Run("finite_zero_memory_limit_stays_serial", func(t *testing.T) {
		plans := makePlans(3, 1024, codec.AlgorithmNone, "")
		installPackResourceHooks(
			t, testParallelWorkersFour,
			cgroup.Limits{MemoryLimitFinite: true, MemoryLimitBytes: 0},
			nil, math.MaxInt64, testParallelMem2GiB, true,
		)
		assert.Equal(t, 1, resolvePackWorkerCount(plans))
	})

	t.Run("unlimited_all_sources_uses_max_workers", func(t *testing.T) {
		plans := makePlans(3, testParallelSize28MiB, codec.AlgorithmZstd, "default")
		installPackResourceHooks(
			t, testParallelWorkersFour, cgroup.Limits{}, errors.New("no cgroup"), math.MaxInt64, 0, false,
		)
		assert.Equal(t, 3, resolvePackWorkerCount(plans))
	})
}

func TestEncoderWorkspaceAndMeminfoParsing(t *testing.T) {
	t.Parallel()
	assert.Equal(t, packWorkspaceNoneBytes, encoderWorkspaceBytes(codec.AlgorithmNone, ""))
	assert.Equal(t, packWorkspaceLZ4FastBytes, encoderWorkspaceBytes(codec.AlgorithmLZ4, "fast"))
	assert.Equal(t, packWorkspaceLZ4HCBytes, encoderWorkspaceBytes(codec.AlgorithmLZ4, "better"))
	assert.Equal(t, packWorkspaceLZ4HCBytes, encoderWorkspaceBytes(codec.AlgorithmLZ4, "6"))
	assert.Equal(t, packWorkspaceZstdFastestBytes, encoderWorkspaceBytes(codec.AlgorithmZstd, "fastest"))
	assert.Equal(t, packWorkspaceZstdDefaultBytes, encoderWorkspaceBytes(codec.AlgorithmZstd, "default"))
	assert.Equal(t, packWorkspaceZstdBetterBytes, encoderWorkspaceBytes(codec.AlgorithmZstd, "better"))
	assert.Equal(t, packWorkspaceZstdBestBytes, encoderWorkspaceBytes(codec.AlgorithmZstd, "best"))
	assert.Equal(t, packWorkspaceZstdDefaultBytes, encoderWorkspaceBytes(codec.AlgorithmZstd, "invalid-level"))
	assert.Equal(t, packWorkspaceZstdDefaultBytes, encoderWorkspaceBytes("custom-codec", ""))
	assert.Equal(t, int64(math.MaxInt64), estimateVariantWorkerMemoryBytes(math.MaxInt64, codec.AlgorithmZstd, "best"))
	assert.Equal(t, 1, admitWorkersByMemory(nil, 1, testParallelMem2GiB))

	dir := t.TempDir()
	validMeminfo := filepath.Join(dir, "meminfo-valid")
	require.NoError(t, os.WriteFile(validMeminfo, []byte("MemTotal:       16384000 kB\nMemAvailable:    2097152 kB\n"), 0o600))
	avail, ok := parseMemAvailableFromFile(validMeminfo)
	assert.True(t, ok)
	assert.Equal(t, int64(2097152)*packKibiByte, avail)

	for name, content := range map[string]string{
		"empty-val": "MemAvailable:\n",
		"bad-num":   "MemAvailable: notanumber kB\n",
		"zero":      "MemAvailable: 0 kB\n",
		"missing":   "MemTotal: 1024 kB\n",
	} {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
		_, gotOK := parseMemAvailableFromFile(p)
		assert.False(t, gotOK, "case %s", name)
	}
	_, ok = parseMemAvailableFromFile(filepath.Join(dir, "does-not-exist"))
	assert.False(t, ok)
}

func TestPackParallelMidFlightFailurePreservesDestinationAndCleansStaging(t *testing.T) {
	installPackResourceHooks(
		t, testParallelWorkersFour, cgroup.Limits{}, nil, math.MaxInt64, testParallelMem2GiB, true,
	)

	dir := t.TempDir()
	stubPath := makeDistinctVariantFixture(t, dir, "stub", 64*1024)
	v1 := makeDistinctVariantFixture(t, dir, "v1", testParallelVariantPayload)
	v2 := makeDistinctVariantFixture(t, dir, "v2", testParallelVariantPayload)
	v3 := makeDistinctVariantFixture(t, dir, "v3", testParallelVariantPayload)
	v4 := makeDistinctVariantFixture(t, dir, "v4", testParallelVariantPayload)

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "published.fat")
	original := []byte("existing-published-fat-binary-must-remain-untouched")
	require.NoError(t, os.WriteFile(outPath, original, testParallelExistingMode))
	beforeStat, err := os.Stat(outPath)
	require.NoError(t, err)

	opts := DefaultOptions()
	opts.StubPath = stubPath
	opts.OutputPath = outPath
	opts.Profile = codec.ProfileBalanced
	opts.Variants = map[string]string{"v1": v1, "v2": v2, "v3": v3, "v4": v4}
	opts.VariantCompression = map[string]VariantCompressionOptions{
		"v3": {Compression: codec.AlgorithmZstd, Level: "invalid-midflight-level"},
	}

	_, err = Pack(opts)
	require.ErrorIs(t, err, codec.ErrInvalidCompressionLevel)

	afterStat, err := os.Stat(outPath)
	require.NoError(t, err)
	assert.True(t, os.SameFile(beforeStat, afterStat))
	gotBytes, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Equal(t, original, gotBytes)

	entries, err := os.ReadDir(outDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "all .microfat-pack-* staging files must be cleaned up on error")
	assert.Equal(t, "published.fat", entries[0].Name())
}

func TestStageAndAppendStagedVariantEdgeCases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	vPath := makeDistinctVariantFixture(t, dir, "v1", 8192)
	opts := DefaultOptions()
	opts.Variants = map[string]string{"v1": vPath}

	_, err := prepareVariantWorkPlans([]string{"v1"}, &Options{
		Variants: map[string]string{"v1": filepath.Join(dir, "missing.elf")},
	})
	require.ErrorIs(t, err, ErrVariantNotFound)

	plans, err := prepareVariantWorkPlans([]string{"v1"}, &opts)
	require.NoError(t, err)
	require.Len(t, plans, 1)

	// Missing input at worker execution time
	missingPlan := plans[0]
	missingPlan.cleanPath = filepath.Join(dir, "vanished.elf")
	res := stageCompressedVariant(dir, &missingPlan, &opts, nil)
	require.ErrorIs(t, res.err, ErrVariantNotFound)

	// Size changed relative to statSize triggers compression re-resolution cleanly
	staleSizePlan := plans[0]
	staleSizePlan.statSize = 1
	res = stageCompressedVariant(dir, &staleSizePlan, &opts, nil)
	require.NoError(t, res.err)
	require.FileExists(t, res.stagedPath)

	// Missing outDir fails staging temp creation
	resBadDir := stageCompressedVariant(filepath.Join(dir, "no-such-dir"), &plans[0], &opts, nil)
	require.Error(t, resBadDir.err)

	// Append staged variant frame checks: missing, truncated, grown, and happy path
	dst, err := os.CreateTemp(dir, "dst-*")
	require.NoError(t, err)
	defer func() { _ = dst.Close() }()

	_, err = appendStagedVariantFrame(dst, filepath.Join(dir, "missing-stage"), 10)
	require.Error(t, err)

	_, err = appendStagedVariantFrame(dst, res.stagedPath, res.entry.CompressedSize+5)
	require.ErrorIs(t, err, ErrSizeMismatch)

	if res.entry.CompressedSize > 1 {
		_, err = appendStagedVariantFrame(dst, res.stagedPath, res.entry.CompressedSize-1)
		require.ErrorIs(t, err, ErrSizeMismatch)
	}

	closedDst, err := os.CreateTemp(dir, "closed-dst-*")
	require.NoError(t, err)
	require.NoError(t, closedDst.Close())
	_, err = appendStagedVariantFrame(closedDst, res.stagedPath, res.entry.CompressedSize)
	require.Error(t, err)

	err = compressVariantIntoWriter(
		&bytes.Buffer{}, "v1", codec.NewZstdCodec(), "invalid-dict-level", []byte("payload"), []byte("dict"),
	)
	require.ErrorIs(t, err, codec.ErrInvalidCompressionLevel)

	// Re-resolve error when size changed and override is invalid
	badReResolveOpts := opts
	badReResolveOpts.Profile = "unsupported-profile"
	resBadResolve := stageCompressedVariant(dir, &staleSizePlan, &badReResolveOpts, nil)
	require.ErrorIs(t, resBadResolve.err, codec.ErrUnsupportedProfile)

	require.NoError(t, os.Remove(res.stagedPath))
}
