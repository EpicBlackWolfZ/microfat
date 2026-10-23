package pack

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cache"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prewarmPlanFixture(
	t *testing.T,
	version int,
	arch string,
	dictionary []byte,
	payloads [][]byte,
	mutate func(*format.Index),
) ([]byte, *format.Index) {
	t.Helper()
	idx := &format.Index{TargetOS: testOSLinux, TargetArch: arch}
	var data bytes.Buffer
	if len(dictionary) > 0 {
		idx.DictionarySize = int64(len(dictionary))
		idx.DictionarySHA256 = checksumCasingSHA256(dictionary)
		_, err := data.Write(dictionary)
		require.NoError(t, err)
	}
	for i, payload := range payloads {
		level := fmt.Sprintf("v%d", i+1)
		if arch == microarch.ArchARM64 {
			level = fmt.Sprintf("v8.%d", i)
		}
		idx.Variants = append(idx.Variants, format.VariantEntry{
			Level: level, Offset: int64(data.Len()), CompressedSize: int64(len(payload)),
			UncompressedSize: int64(len(payload)), SHA256: checksumCasingSHA256(payload), Compression: codec.AlgorithmNone,
		})
		_, err := data.Write(payload)
		require.NoError(t, err)
	}
	if mutate != nil {
		mutate(idx)
	}
	_, err := format.WriteIndexAndTrailerWithVersion(&data, idx, int64(data.Len()), version)
	require.NoError(t, err)
	return data.Bytes(), idx
}

func TestPlanPrewarmBinaryFormatsAndAccounting(t *testing.T) {
	t.Parallel()
	payload := []byte("shared payload")
	otherPayload := []byte("other selected payload")
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, dictionary := range [][]byte{nil, []byte("shared dictionary declaration")} {
			t.Run(fmt.Sprintf("format%d/dictionary%t", version, dictionary != nil), func(t *testing.T) {
				t.Parallel()
				data, _ := prewarmPlanFixture(t, version, testArchAMD64, dictionary,
					[][]byte{payload, payload, otherPayload}, nil)
				dir := filepath.Join(t.TempDir(), "cache")
				reader := bytes.NewReader(data)
				_, plan, err := PlanPrewarmBinary(reader, int64(len(data)), nil, dir, PrewarmOptions{})
				require.NoError(t, err)
				require.NoDirExists(t, dir)
				require.Len(t, plan.Entries, 3)
				assert.Equal(t, 2, plan.UniquePayloadCount)
				assert.Equal(t, uint64(len(payload)+len(otherPayload)), plan.UniquePayloadBytes)
				assert.Equal(t, plan.UniquePayloadBytes, plan.NeedsMaterializationBytes)
				assert.Zero(t, plan.VerifiedCacheBytes)
				assert.Zero(t, plan.BlockedPayloadBytes)
				for _, entry := range plan.Entries {
					assert.Equal(t, cache.ObservationMissing, entry.CacheStatus)
					assert.True(t, entry.DecoderAvailable)
					assert.True(t, entry.MaterializationPossible)
					assert.True(t, entry.CanPrewarm)
				}

				_, results, err := PrewarmBinaryWithOptions(reader, int64(len(data)),
					[]string{strings.ToUpper(compressionAliasV1)}, dir, PrewarmOptions{})
				require.NoError(t, err)
				require.Len(t, results, 1)
				_, plan, err = PlanPrewarmBinary(reader, int64(len(data)), nil, dir, PrewarmOptions{})
				require.NoError(t, err)
				assert.Equal(t, uint64(len(payload)), plan.VerifiedCacheBytes)
				assert.Equal(t, uint64(len(otherPayload)), plan.NeedsMaterializationBytes)
				_, _, err = PrewarmBinaryWithOptions(reader, int64(len(data)), nil, dir, PrewarmOptions{})
				require.NoError(t, err)
				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				require.Len(t, entries, plan.UniquePayloadCount)
				var materializedBytes uint64
				for _, entry := range entries {
					info, err := entry.Info()
					require.NoError(t, err)
					materializedBytes += uint64(info.Size())
				}
				assert.Equal(t, plan.UniquePayloadBytes, materializedBytes)
			})
		}
	}
}

func TestPrewarmAdmissionBeforeCacheMutation(t *testing.T) {
	t.Parallel()
	payload := []byte("selected payload")
	data, _ := prewarmPlanFixture(t, format.FormatVersion2, testArchAMD64, nil, [][]byte{payload, payload}, nil)
	for _, tc := range []struct {
		name  string
		limit uint64
		ok    bool
	}{
		{name: "zero", limit: 0},
		{name: "one below", limit: uint64(len(payload) - 1)},
		{name: "exact deduplicated bytes", limit: uint64(len(payload)), ok: true},
		{name: "unbounded uint64", limit: math.MaxUint64, ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "cache")
			opts := PrewarmOptions{MaxPayloadBytes: &tc.limit}
			_, _, err := PlanPrewarmBinary(bytes.NewReader(data), int64(len(data)), nil, dir, opts)
			if !tc.ok {
				require.ErrorIs(t, err, ErrPrewarmPayloadLimit)
			} else {
				require.NoError(t, err)
			}
			require.NoDirExists(t, dir)
			_, results, err := PrewarmBinaryWithOptions(bytes.NewReader(data), int64(len(data)), nil, dir, opts)
			if !tc.ok {
				require.ErrorIs(t, err, ErrPrewarmPayloadLimit)
				require.Empty(t, results)
				require.NoDirExists(t, dir)
				return
			}
			require.NoError(t, err)
			require.Len(t, results, 2)
			assert.False(t, results[0].AlreadyCached)
			assert.True(t, results[1].AlreadyCached)
			// The cap is independent of existing verified bytes.
			zero := uint64(0)
			_, _, err = PrewarmBinaryWithOptions(bytes.NewReader(data), int64(len(data)), nil, dir,
				PrewarmOptions{MaxPayloadBytes: &zero})
			require.ErrorIs(t, err, ErrPrewarmPayloadLimit)
		})
	}
}

func TestPrewarmSelectionValidation(t *testing.T) {
	t.Parallel()
	payload := []byte("payload")
	_, original := prewarmPlanFixture(t, format.FormatVersion2, testArchAMD64, nil, [][]byte{payload, payload}, nil)
	for _, tc := range []struct {
		name   string
		levels []string
		mutate func(*format.Index)
		want   error
	}{
		{name: "deduplicated aliases", levels: []string{strings.ToUpper(compressionAliasV1), "v1", "V2"}},
		{name: "missing level", levels: []string{"v4"}, want: ErrVariantNotFound},
		{name: "conflicting identity sizes", mutate: func(idx *format.Index) { idx.Variants[1].UncompressedSize++ },
			want: ErrPrewarmAccounting},
		{name: "missing sha", mutate: func(idx *format.Index) { idx.Variants[0].SHA256 = "" }, want: format.ErrInvalidChecksum},
		{name: "malformed sha", mutate: func(idx *format.Index) { idx.Variants[0].SHA256 = "invalid" }, want: format.ErrInvalidChecksum},
		{name: "zero size", mutate: func(idx *format.Index) { idx.Variants[0].UncompressedSize = 0 }, want: format.ErrPayloadTooLarge},
		{name: "negative size", mutate: func(idx *format.Index) { idx.Variants[0].UncompressedSize = -1 }, want: format.ErrPayloadTooLarge},
		{name: "oversized", mutate: func(idx *format.Index) { idx.Variants[0].UncompressedSize = format.MaxPayloadSize + 1 },
			want: format.ErrPayloadTooLarge},
		{name: "identity case canonicalization", mutate: func(idx *format.Index) {
			idx.Variants[1].SHA256 = strings.ToUpper(idx.Variants[1].SHA256)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			idx := *original
			idx.Variants = append([]format.VariantEntry(nil), original.Variants...)
			if tc.mutate != nil {
				tc.mutate(&idx)
			}
			err := ValidatePrewarmAdmission(&idx, tc.levels, PrewarmOptions{})
			switch {
			case tc.name == "missing level":
				require.ErrorContains(t, err, "not found in binary manifest")
			case tc.want != nil:
				require.ErrorIs(t, err, tc.want)
			default:
				require.NoError(t, err)
			}
		})
	}
	require.ErrorIs(t, ValidatePrewarmAdmission(nil, nil, PrewarmOptions{}), ErrPrewarmAccounting)
	_, err := addPrewarmBytes(math.MaxUint64, 1)
	require.ErrorIs(t, err, ErrPrewarmAccounting)
	total, err := addPrewarmBytes(math.MaxUint64-1, 1)
	require.NoError(t, err)
	assert.Equal(t, uint64(math.MaxUint64), total)
}

func TestPlanPrewarmCacheStatesPreserved(t *testing.T) {
	t.Parallel()
	payload := []byte("cached payload")
	data, idx := prewarmPlanFixture(t, format.FormatVersion2, testArchAMD64, nil, [][]byte{payload}, nil)
	for _, state := range []string{
		"missing", string(cache.ObservationValid), "corrupt_safe", "unsafe_mode", "symlink", "nonregular", "insecure_directory",
	} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "cache")
			require.NoError(t, os.Mkdir(dir, 0o700))
			path := filepath.Join(dir, idx.Variants[0].SHA256)
			before := payload
			want := cache.ObservationUnsafe
			switch state {
			case "missing":
				want = cache.ObservationMissing
			case "symlink":
				target := filepath.Join(t.TempDir(), "target")
				require.NoError(t, os.WriteFile(target, before, 0o700))
				require.NoError(t, os.Symlink(target, path))
			case "nonregular":
				require.NoError(t, os.Mkdir(path, 0o700))
			default:
				mode := os.FileMode(0o700)
				switch state {
				case "corrupt_safe":
					before = []byte("bad")
					want = cache.ObservationCorruptSafe
				case "unsafe_mode":
					mode = 0o722
				case string(cache.ObservationValid):
					want = cache.ObservationValid
				}
				require.NoError(t, os.WriteFile(path, before, mode))
				require.NoError(t, os.Chmod(path, mode))
				if state == "insecure_directory" {
					require.NoError(t, os.Chmod(dir, 0o777))
				}
			}
			dirInfo, err := os.Lstat(dir)
			require.NoError(t, err)
			_, plan, err := PlanPrewarmBinary(bytes.NewReader(data), int64(len(data)), nil, dir, PrewarmOptions{})
			require.NoError(t, err)
			require.Len(t, plan.Entries, 1)
			assert.Equal(t, want, plan.Entries[0].CacheStatus)
			assert.Equal(t, plan.UniquePayloadBytes,
				plan.VerifiedCacheBytes+plan.NeedsMaterializationBytes+plan.BlockedPayloadBytes)
			if want == cache.ObservationUnsafe {
				assert.False(t, plan.Entries[0].CanPrewarm)
				assert.Equal(t, uint64(len(payload)), plan.BlockedPayloadBytes)
			}
			// An admission rejection must not even repair automatic or explicit cache state.
			zero := uint64(0)
			_, _, err = PrewarmBinaryWithOptions(bytes.NewReader(data), int64(len(data)), nil, dir,
				PrewarmOptions{MaxPayloadBytes: &zero})
			require.ErrorIs(t, err, ErrPrewarmPayloadLimit)
			afterInfo, err := os.Lstat(dir)
			require.NoError(t, err)
			assert.Equal(t, dirInfo.Mode(), afterInfo.Mode())
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			if state == "missing" {
				require.Empty(t, entries)
				return
			}
			require.Len(t, entries, 1)
			if state != "nonregular" {
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, before, after)
			}
		})
	}
}

func TestPlanPrewarmDecoderAndStaleObservations(t *testing.T) {
	t.Parallel()
	payload := []byte("verified cached payload")
	data, idx := prewarmPlanFixture(t, format.FormatVersion2, testArchAMD64, nil, [][]byte{payload},
		func(idx *format.Index) { idx.Variants[0].Compression = "unavailable-decoder" })
	dir := t.TempDir()
	path := filepath.Join(dir, idx.Variants[0].SHA256)
	reader := bytes.NewReader(data)
	_, plan, err := PlanPrewarmBinary(reader, int64(len(data)), nil, dir, PrewarmOptions{})
	require.NoError(t, err)
	assert.False(t, plan.Entries[0].DecoderAvailable)
	assert.False(t, plan.Entries[0].CanPrewarm)
	assert.NotEmpty(t, plan.Entries[0].DecoderError)
	require.NoError(t, os.WriteFile(path, payload, 0o700))
	_, plan, err = PlanPrewarmBinary(reader, int64(len(data)), nil, dir, PrewarmOptions{})
	require.NoError(t, err)
	assert.Equal(t, cache.ObservationValid, plan.Entries[0].CacheStatus)
	assert.False(t, plan.Entries[0].DecoderAvailable)
	assert.False(t, plan.Entries[0].MaterializationPossible)
	assert.True(t, plan.Entries[0].CanPrewarm)
	_, results, err := PrewarmBinaryWithOptions(reader, int64(len(data)), nil, dir, PrewarmOptions{})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].AlreadyCached)
	// The prior valid observation cannot bypass validation after the entry changes.
	corrupt := bytes.Repeat([]byte("!"), len(payload))
	require.NoError(t, os.WriteFile(path, corrupt, 0o700))
	_, results, err = PrewarmBinaryWithOptions(reader, int64(len(data)), nil, dir, PrewarmOptions{})
	require.ErrorIs(t, err, codec.ErrUnsupportedCodec)
	require.Empty(t, results)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, corrupt, after)
	// A supported decoder repairs a newly corrupted safe file, still without trusting the plan.
	data, _ = prewarmPlanFixture(t, format.FormatVersion2, testArchAMD64, nil, [][]byte{payload}, nil)
	_, results, err = PrewarmBinaryWithOptions(bytes.NewReader(data), int64(len(data)), nil, dir, PrewarmOptions{})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].AlreadyCached)
	require.NoError(t, os.Chmod(path, 0o722))
	_, _, err = PrewarmBinaryWithOptions(bytes.NewReader(data), int64(len(data)), nil, dir, PrewarmOptions{})
	require.ErrorIs(t, err, cache.ErrUnsafeFile)
}

func TestPlanPrewarmDuplicateDecoderOrdering(t *testing.T) {
	t.Parallel()
	for _, unsupportedFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("unsupported_first_%t", unsupportedFirst), func(t *testing.T) {
			t.Parallel()
			data, _ := prewarmPlanFixture(t, format.FormatVersion2, testArchAMD64, nil,
				[][]byte{[]byte("shared payload"), []byte("shared payload")}, func(idx *format.Index) {
					position := 1
					if unsupportedFirst {
						position = 0
					}
					idx.Variants[position].Compression = "unavailable-decoder"
				})
			dir := t.TempDir()
			_, plan, err := PlanPrewarmBinary(bytes.NewReader(data), int64(len(data)), nil, dir, PrewarmOptions{})
			require.NoError(t, err)
			require.Len(t, plan.Entries, 2)
			assert.Equal(t, 1, plan.UniquePayloadCount)
			assert.Empty(t, plan.Entries[0].ReuseAfterLevel)
			if unsupportedFirst {
				assert.False(t, plan.Entries[0].CanPrewarm)
				assert.Empty(t, plan.Entries[1].ReuseAfterLevel)
			} else {
				assert.True(t, plan.Entries[0].CanPrewarm)
				assert.True(t, plan.Entries[1].CanPrewarm)
				assert.False(t, plan.Entries[1].DecoderAvailable)
				assert.False(t, plan.Entries[1].MaterializationPossible)
				assert.Equal(t, "v1", plan.Entries[1].ReuseAfterLevel)
			}
			_, results, err := PrewarmBinaryWithOptions(bytes.NewReader(data), int64(len(data)), nil, dir, PrewarmOptions{})
			if unsupportedFirst {
				require.ErrorIs(t, err, codec.ErrUnsupportedCodec)
				require.Empty(t, results)
				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				assert.Empty(t, entries)
			} else {
				require.NoError(t, err)
				require.Len(t, results, 2)
				assert.False(t, results[0].AlreadyCached)
				assert.True(t, results[1].AlreadyCached)
			}
		})
	}
}

func TestPlanPrewarmSourceAndSelectionErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		arch       string
		levels     []string
		mutate     func(*format.Index)
		corrupt    bool
		dictionary []byte
		want       error
	}{
		{name: "foreign architecture all", arch: microarch.ArchARM64},
		{name: "foreign explicit alias", arch: microarch.ArchARM64, levels: []string{"arm64_v8.0"}},
		{name: "unsupported architecture", arch: "riscv64", want: format.ErrInvalidVariant},
		{name: "invalid selection", arch: testArchAMD64, levels: []string{"v4"}, want: ErrVariantNotFound},
		{name: "conflicting declarations", arch: testArchAMD64,
			mutate: func(idx *format.Index) { idx.Variants[1].UncompressedSize++ }, want: ErrPrewarmAccounting},
		{name: "corrupt dictionary", arch: testArchAMD64, dictionary: []byte("dictionary"), corrupt: true,
			want: format.ErrDictionaryCorrupted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, _ := prewarmPlanFixture(t, format.FormatVersion2, tc.arch, tc.dictionary,
				[][]byte{[]byte("payload"), []byte("payload")}, tc.mutate)
			if tc.corrupt {
				data[0] ^= 1
			}
			dir := filepath.Join(t.TempDir(), "cache")
			_, _, err := PlanPrewarmBinary(bytes.NewReader(data), int64(len(data)), tc.levels, dir, PrewarmOptions{})
			switch {
			case tc.name == "invalid selection":
				require.ErrorContains(t, err, "not found in binary manifest")
			case tc.want != nil:
				require.ErrorIs(t, err, tc.want)
			default:
				require.NoError(t, err)
			}
			require.NoDirExists(t, dir)
		})
	}
	dir := filepath.Join(t.TempDir(), "cache")
	_, _, err := PlanPrewarmBinary(bytes.NewReader(nil), 0, nil, dir, PrewarmOptions{})
	require.Error(t, err)
	require.NoDirExists(t, dir)
}

func TestPlanPrewarmUnavailableDefaultCache(t *testing.T) {
	root := t.TempDir()
	t.Setenv(format.EnvCacheDir, "")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "xdg"))
	t.Setenv("TMPDIR", filepath.Join(root, "tmp"))
	data, _ := prewarmPlanFixture(t, format.FormatVersion2, testArchAMD64, nil, [][]byte{[]byte("payload")}, nil)
	_, plan, err := PlanPrewarmBinary(bytes.NewReader(data), int64(len(data)), nil, "", PrewarmOptions{})
	require.NoError(t, err)
	require.Len(t, plan.Entries, 1)
	assert.Equal(t, cache.ObservationUnavailable, plan.Entries[0].CacheStatus)
	assert.Empty(t, plan.Entries[0].CachePath)
	assert.NotEmpty(t, plan.Entries[0].CacheError)
	assert.False(t, plan.Entries[0].CanPrewarm)
	assert.Equal(t, plan.UniquePayloadBytes, plan.BlockedPayloadBytes)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPrewarmPartialResults(t *testing.T) {
	t.Parallel()
	data, idx := prewarmPlanFixture(t, format.FormatVersion2, testArchAMD64, nil,
		[][]byte{[]byte("first payload"), []byte("second payload")},
		func(idx *format.Index) { idx.Variants[1].Compression = "unavailable-decoder" })
	dir := t.TempDir()
	returnedIndex, results, err := PrewarmBinaryWithOptions(bytes.NewReader(data), int64(len(data)), nil, dir, PrewarmOptions{})
	require.ErrorIs(t, err, codec.ErrUnsupportedCodec)
	require.NotNil(t, returnedIndex)
	require.Len(t, results, 1)
	assert.Equal(t, idx.Variants[0].SHA256, results[0].SHA256)
	require.FileExists(t, filepath.Join(dir, idx.Variants[0].SHA256))
	require.NoFileExists(t, filepath.Join(dir, idx.Variants[1].SHA256))
	returnedIndex, results, err = PrewarmBinary(bytes.NewReader(data), int64(len(data)), nil, dir)
	require.ErrorIs(t, err, codec.ErrUnsupportedCodec)
	assert.Nil(t, returnedIndex)
	assert.Nil(t, results)
}

func TestReadPrewarmDictionaryErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		idx  format.Index
		want error
	}{
		{name: "negative size", idx: format.Index{DictionarySize: -1}, want: format.ErrInvalidDictionary},
		{name: "oversize", idx: format.Index{DictionarySize: format.MaxDictionarySize + 1}, want: format.ErrInvalidDictionary},
		{name: "negative offset", idx: format.Index{DictionarySize: 1, DictionaryOffset: -1}, want: format.ErrInvalidDictionary},
		{name: "missing checksum", idx: format.Index{DictionarySize: 1}, want: format.ErrInvalidChecksum},
		{name: "malformed checksum", idx: format.Index{DictionarySize: 1, DictionarySHA256: "invalid"}, want: format.ErrInvalidChecksum},
		{name: "read failure", idx: format.Index{DictionarySize: 1, DictionarySHA256: strings.Repeat("0", 64)}, want: io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := readPrewarmDictionary(bytes.NewReader(nil), &tc.idx)
			require.ErrorIs(t, err, tc.want)
		})
	}
}
