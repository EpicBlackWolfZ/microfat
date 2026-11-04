package pack

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	profitableDictTargetSize    = 32 * 1024
	profitableSmallDictSize     = 8 * 1024
	unprofitableRandomExtraSize = 16 * 1024
	tinySpoolLimitBytes         = 64
	tieSeedOne                  = 409
	tieSeedTwo                  = 410
	preservedOutputMode         = 0o600
	multiBlockTestSize          = 600 * 1024
)

type countingRawCodec struct {
	name          string
	compressCalls atomic.Int64
}

func (c *countingRawCodec) Name() string { return c.name }

func (c *countingRawCodec) Compress(w io.Writer, src []byte, _ string) error {
	c.compressCalls.Add(1)
	_, err := w.Write(src)
	return err
}

func (c *countingRawCodec) Decompress(w io.Writer, r io.Reader, uncompressedSize int64) error {
	return codec.NewNoneCodec().Decompress(w, r, uncompressedSize)
}

type countingDictCodec struct {
	name          string
	noDictCalls   atomic.Int64
	withDictCalls atomic.Int64
	v1Shrink      int
	defaultShrink int
}

func (c *countingDictCodec) Name() string { return c.name }

func (c *countingDictCodec) Compress(w io.Writer, src []byte, _ string) error {
	c.noDictCalls.Add(1)
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], 0)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(src)
	return err
}

func (c *countingDictCodec) CompressWithDict(w io.Writer, src []byte, _ string, _ []byte) error {
	c.withDictCalls.Add(1)
	shrink := c.defaultShrink
	if c.v1Shrink > 0 && bytes.Contains(src, []byte("variant_tag_v1")) {
		shrink = c.v1Shrink
	}
	trim := min(max(shrink, 0), len(src))
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(trim)) // #nosec G115 -- bounded to len(src) in test fixture.
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(src[:len(src)-trim])
	return err
}

func (c *countingDictCodec) Decompress(w io.Writer, r io.Reader, uncompressedSize int64) error {
	return c.DecompressWithDict(w, r, uncompressedSize, nil)
}

func (c *countingDictCodec) DecompressWithDict(w io.Writer, r io.Reader, uncompressedSize int64, dict []byte) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	trim := int(binary.LittleEndian.Uint32(hdr[:]))
	rest, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	out := make([]byte, 0, len(rest)+trim)
	out = append(out, rest...)
	if trim > 0 {
		if len(dict) == 0 {
			return errors.New("missing dictionary for trimmed test frame")
		}
		// Reconstruct deterministic tail filler from test payload convention ('Z' bytes).
		out = append(out, bytes.Repeat([]byte{'Z'}, trim)...)
	}
	if int64(len(out)) != uncompressedSize {
		return codec.ErrSizeMismatch
	}
	_, err = w.Write(out)
	return err
}

func netPositiveVariantSet(t *testing.T) (string, map[string]string) {
	t.Helper()
	const (
		uniqueTokenLines = 1024
		tokenBytesPerRow = 28
	)
	dir := t.TempDir()
	stub := createDummyELF(t, dir, "stub", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
	elfHdr, err := os.ReadFile(stub)
	require.NoError(t, err)

	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+/"
	rng := rand.New(rand.NewPCG(tieSeedOne, 1))
	var sharedCatalog bytes.Buffer
	row := make([]byte, tokenBytesPerRow+1)
	row[tokenBytesPerRow] = '\n'
	for range uniqueTokenLines {
		for j := range tokenBytesPerRow {
			row[j] = alphabet[rng.UintN(uint(len(alphabet)))]
		}
		_, writeErr := sharedCatalog.Write(row)
		require.NoError(t, writeErr)
	}

	variants := make(map[string]string, 4)
	for idx, level := range []string{"v1", "v2", "v3", "v4"} {
		vPath := filepath.Join(dir, level)
		var buf bytes.Buffer
		_, writeErr := buf.Write(elfHdr)
		require.NoError(t, writeErr)
		_, writeErr = buf.Write(sharedCatalog.Bytes())
		require.NoError(t, writeErr)
		_, writeErr = fmt.Fprintf(&buf, "variant_footer_%s_%d\n", level, idx)
		require.NoError(t, writeErr)
		require.NoError(t, os.WriteFile(vPath, buf.Bytes(), defaultFileMode))
		variants[level] = vPath
	}
	return stub, variants
}

func structuredTailPaddedELF(t *testing.T, tailZBytes int) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	stub := createDummyELF(t, dir, "stub", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
	_, structured := dictionaryContractInput(t)
	v1Path := filepath.Join(dir, "variant_v1")
	v3Path := filepath.Join(dir, "variant_v3")
	v1Payload := append(append(bytes.Clone(structured), []byte("variant_tag_v1\n")...), bytes.Repeat([]byte{'Z'}, tailZBytes)...)
	v3Payload := append(append(bytes.Clone(structured), []byte("variant_tag_v3\n")...), bytes.Repeat([]byte{'Z'}, tailZBytes)...)
	require.NoError(t, os.WriteFile(v1Path, v1Payload, defaultFileMode))
	require.NoError(t, os.WriteFile(v3Path, v3Payload, defaultFileMode))
	return stub, v1Path, v3Path
}

func incompressibleVariantPair(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	stub := createDummyELF(t, dir, "stub", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
	v1 := createDummyELF(t, dir, "v1", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
	v3 := createDummyELF(t, dir, "v3", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))

	rng1 := rand.New(rand.NewPCG(tieSeedOne, tieSeedTwo))
	rng3 := rand.New(rand.NewPCG(tieSeedTwo, tieSeedOne))
	for _, item := range []struct {
		path string
		rng  *rand.Rand
	}{
		{v1, rng1},
		{v3, rng3},
	} {
		base, err := os.ReadFile(item.path)
		require.NoError(t, err)
		noise := make([]byte, unprofitableRandomExtraSize)
		for i := range noise {
			noise[i] = byte(item.rng.UintN(256))
		}
		require.NoError(t, os.WriteFile(item.path, append(base, noise...), defaultFileMode))
	}
	return stub, v1, v3
}

func TestDictProfitabilityNetPositiveAndUnprofitable(t *testing.T) {
	t.Parallel()
	epoch := reproducibleEpoch

	for _, version := range []int{format.FormatVersion2} {
		t.Run(fmt.Sprintf("format%d/net_positive_retains_dictionary", version), func(t *testing.T) {
			t.Parallel()
			stub, variants := netPositiveVariantSet(t)

			noDictOpts := DefaultOptions()
			noDictOpts.StubPath, noDictOpts.Variants, noDictOpts.CreatedUnix = stub, variants, &epoch
			noDictOpts.FormatVersion, noDictOpts.DictSize = version, profitableDictTargetSize
			noDictOpts.Profile, noDictOpts.Compression = codec.ProfileBalanced, codec.AlgorithmZstd+":"+compressionBest
			noDictOpts.OutputPath = filepath.Join(t.TempDir(), "no-dict")
			_, err := Pack(noDictOpts)
			require.NoError(t, err)
			noDictBytes, err := os.ReadFile(noDictOpts.OutputPath)
			require.NoError(t, err)

			forcedOpts := noDictOpts
			forcedOpts.Profile, forcedOpts.Compression, forcedOpts.EnableDict = codec.ProfileSize, "", false
			forcedOpts.OutputPath = filepath.Join(t.TempDir(), "size-auto")
			_, err = Pack(forcedOpts)
			require.NoError(t, err)
			autoDictBytes, err := os.ReadFile(forcedOpts.OutputPath)
			require.NoError(t, err)
			require.Less(t, len(autoDictBytes), len(noDictBytes), "fixture must be net-positive with dictionary")

			profOpts := forcedOpts
			profOpts.DictProfitable = true
			profOpts.OutputPath = filepath.Join(t.TempDir(), "size-profitable")
			idx, err := Pack(profOpts)
			require.NoError(t, err)
			require.Positive(t, idx.DictionarySize)
			profBytes, err := os.ReadFile(profOpts.OutputPath)
			require.NoError(t, err)
			assert.Equal(t, autoDictBytes, profBytes, "profitable mode must match winning dictionary artifact byte-for-byte")

			_, results, err := VerifyBinary(bytes.NewReader(profBytes), int64(len(profBytes)))
			require.NoError(t, err)
			for _, r := range results {
				assert.True(t, r.Valid)
			}
		})

		t.Run(fmt.Sprintf("format%d/unprofitable_omits_dictionary", version), func(t *testing.T) {
			t.Parallel()
			stub, v1, v3 := incompressibleVariantPair(t)
			variants := map[string]string{"v1": v1, "v3": v3}

			sizeDefaultOpts := DefaultOptions()
			sizeDefaultOpts.StubPath, sizeDefaultOpts.Variants, sizeDefaultOpts.CreatedUnix = stub, variants, &epoch
			sizeDefaultOpts.FormatVersion, sizeDefaultOpts.Profile = version, codec.ProfileSize
			sizeDefaultOpts.OutputPath = filepath.Join(t.TempDir(), "size-default")
			defaultIdx, err := Pack(sizeDefaultOpts)
			require.NoError(t, err)
			require.Positive(t, defaultIdx.DictionarySize)
			defaultBytes, err := os.ReadFile(sizeDefaultOpts.OutputPath)
			require.NoError(t, err)

			noDictControl := DefaultOptions()
			noDictControl.StubPath, noDictControl.Variants, noDictControl.CreatedUnix = stub, variants, &epoch
			noDictControl.FormatVersion = version
			noDictControl.Profile, noDictControl.Compression = codec.ProfileBalanced, codec.AlgorithmZstd+":"+compressionBest
			noDictControl.OutputPath = filepath.Join(t.TempDir(), "best-no-dict")
			_, err = Pack(noDictControl)
			require.NoError(t, err)
			noDictBytes, err := os.ReadFile(noDictControl.OutputPath)
			require.NoError(t, err)
			require.Less(t, len(noDictBytes), len(defaultBytes), "fixture must be unprofitable with automatic dictionary")

			outDir := t.TempDir()
			profOpts := sizeDefaultOpts
			profOpts.DictProfitable = true
			profOpts.OutputPath = filepath.Join(outDir, "size-profitable")
			profIdx, err := Pack(profOpts)
			require.NoError(t, err)
			assert.Zero(t, profIdx.DictionarySize)
			assert.Zero(t, profIdx.DictionaryOffset)
			assert.Empty(t, profIdx.DictionarySHA256)
			assert.Zero(t, profIdx.DictionaryID)

			profBytes, err := os.ReadFile(profOpts.OutputPath)
			require.NoError(t, err)
			assert.Equal(t, noDictBytes, profBytes, "unprofitable dictionary must fall back to exact no-dictionary artifact")

			dirEntries, err := os.ReadDir(outDir)
			require.NoError(t, err)
			require.Len(t, dirEntries, 1, "staging temp files must be cleaned up")

			_, results, err := VerifyBinary(bytes.NewReader(profBytes), int64(len(profBytes)))
			require.NoError(t, err)
			for _, r := range results {
				assert.True(t, r.Valid)
			}
		})
	}
}

func TestDictProfitabilityExactTieFavorsNoDictionary(t *testing.T) {
	t.Parallel()
	epoch := reproducibleEpoch

	for _, version := range []int{format.FormatVersion2} {
		t.Run(fmt.Sprintf("format%d", version), func(t *testing.T) {
			t.Parallel()
			stub, v1, v3 := structuredTailPaddedELF(t, profitableSmallDictSize*2)
			variants := map[string]string{"v1": v1, "v3": v3}

			probeName := fmt.Sprintf("test-tie-prb-v%d", version)
			codec.Register(&countingDictCodec{name: probeName})

			noDictOpts := DefaultOptions()
			noDictOpts.StubPath, noDictOpts.Variants, noDictOpts.CreatedUnix = stub, variants, &epoch
			noDictOpts.FormatVersion, noDictOpts.DictSize = version, profitableSmallDictSize
			noDictOpts.Compression = probeName
			noDictOpts.OutputPath = filepath.Join(t.TempDir(), "no-dict")
			_, err := Pack(noDictOpts)
			require.NoError(t, err)
			noDictStat, err := os.Stat(noDictOpts.OutputPath)
			require.NoError(t, err)

			forcedZeroOpts := noDictOpts
			forcedZeroOpts.EnableDict = true
			forcedZeroOpts.OutputPath = filepath.Join(t.TempDir(), "forced-zero-shrink")
			_, err = Pack(forcedZeroOpts)
			require.NoError(t, err)
			forcedZeroStat, err := os.Stat(forcedZeroOpts.OutputPath)
			require.NoError(t, err)

			overhead := int(forcedZeroStat.Size() - noDictStat.Size())
			require.Positive(t, overhead)

			// Calibrate exact complete-byte tie across no-dictionary and with-dictionary Format v2 indices.
			firstShrink := overhead / 2
			secondShrink := overhead - firstShrink
			calName := fmt.Sprintf("test-tie-cal-v%d", version)
			codec.Register(&countingDictCodec{name: calName, v1Shrink: firstShrink, defaultShrink: secondShrink})
			calOpts := forcedZeroOpts
			calOpts.Compression = calName
			calOpts.OutputPath = filepath.Join(t.TempDir(), "cal-forced")
			_, err = Pack(calOpts)
			require.NoError(t, err)
			calStat, err := os.Stat(calOpts.OutputPath)
			require.NoError(t, err)
			secondShrink += int(calStat.Size() - noDictStat.Size())

			tieName := fmt.Sprintf("test-tie-eq0-v%d", version)
			codec.Register(&countingDictCodec{name: tieName, v1Shrink: firstShrink, defaultShrink: secondShrink})

			tieOpts := noDictOpts
			tieOpts.Compression = tieName
			tieOpts.DictProfitable = true
			tieOpts.OutputPath = filepath.Join(t.TempDir(), "tie-result")
			tieIdx, err := Pack(tieOpts)
			require.NoError(t, err)
			assert.Zero(t, tieIdx.DictionarySize, "exact complete-byte tie must favor no-dictionary")
			tieStat, err := os.Stat(tieOpts.OutputPath)
			require.NoError(t, err)
			assert.Equal(t, noDictStat.Size(), tieStat.Size())

			winName := fmt.Sprintf("test-tie-win-v%d", version)
			codec.Register(&countingDictCodec{name: winName, v1Shrink: firstShrink, defaultShrink: secondShrink + 1})

			winOpts := noDictOpts
			winOpts.Compression = winName
			winOpts.DictProfitable = true
			winOpts.OutputPath = filepath.Join(t.TempDir(), "win-result")
			winIdx, err := Pack(winOpts)
			require.NoError(t, err)
			assert.Positive(t, winIdx.DictionarySize, "strictly smaller complete artifact must embed dictionary")
			winStat, err := os.Stat(winOpts.OutputPath)
			require.NoError(t, err)
			assert.Less(t, winStat.Size(), noDictStat.Size())
		})
	}
}

func TestDictProfitabilityFrameReuseAndMixedOverrides(t *testing.T) {
	t.Parallel()
	stub, v1, _ := structuredTailPaddedELF(t, profitableSmallDictSize*3)

	for _, tc := range []struct {
		name        string
		shrinkBytes int
		wantDict    bool
	}{
		{name: "no_dict_wins", shrinkBytes: 0, wantDict: false},
		{name: "with_dict_wins", shrinkBytes: profitableSmallDictSize * 2, wantDict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rawCodec := &countingRawCodec{name: "test-reuse-raw-" + tc.name}
			dictCodec := &countingDictCodec{name: "test-reuse-dict-" + tc.name, defaultShrink: tc.shrinkBytes}
			codec.Register(rawCodec)
			codec.Register(dictCodec)

			outDir := t.TempDir()
			opts := DefaultOptions()
			opts.StubPath = stub
			opts.OutputPath = filepath.Join(outDir, "fat")
			opts.DictSize = profitableSmallDictSize
			opts.DictProfitable = true
			opts.Compression = dictCodec.name
			opts.Variants = map[string]string{"v1": v1, "v2": v1, "v3": v1}
			opts.VariantCompression = map[string]VariantCompressionOptions{
				"v1": {Compression: rawCodec.name},
			}

			idx, packErr := Pack(opts)
			require.NoError(t, packErr)
			if tc.wantDict {
				assert.Positive(t, idx.DictionarySize)
			} else {
				assert.Zero(t, idx.DictionarySize)
			}

			assert.Equal(t, int64(1), rawCodec.compressCalls.Load(),
				"non-DictCodec variant must be compressed only once across both candidates")
			assert.Equal(t, int64(2), dictCodec.noDictCalls.Load(),
				"each DictCodec variant must be compressed once without dict (never re-encoded)")
			assert.Equal(t, int64(2), dictCodec.withDictCalls.Load(),
				"each DictCodec variant must be compressed once with dict (never re-encoded)")

			data, readErr := os.ReadFile(opts.OutputPath)
			require.NoError(t, readErr)
			_, results, verifyErr := VerifyBinary(bytes.NewReader(data), int64(len(data)))
			require.NoError(t, verifyErr)
			require.Len(t, results, 3)
			for _, r := range results {
				assert.True(t, r.Valid)
			}

			entries, dirErr := os.ReadDir(outDir)
			require.NoError(t, dirErr)
			require.Len(t, entries, 1, "all staged frames must be removed after publication")
		})
	}
}

func TestDictProfitabilitySpoolBudgetAndFallbacks(t *testing.T) {
	t.Parallel()
	input, _ := dictionaryContractInput(t)

	t.Run("conflicting_enable_dict_and_profitable_rejected", func(t *testing.T) {
		t.Parallel()
		opts := DefaultOptions()
		opts.StubPath = input
		opts.OutputPath = filepath.Join(t.TempDir(), "out")
		opts.Variants = map[string]string{"v1": input, "v3": input}
		opts.EnableDict = true
		opts.DictProfitable = true
		_, err := Pack(opts)
		require.ErrorIs(t, err, ErrConflictingDictOptions)
	})

	t.Run("negative_max_spool_bytes_rejected", func(t *testing.T) {
		t.Parallel()
		opts := DefaultOptions()
		opts.StubPath = input
		opts.OutputPath = filepath.Join(t.TempDir(), "out")
		opts.Variants = map[string]string{"v1": input, "v3": input}
		opts.DictProfitable = true
		opts.MaxSpoolBytes = -1
		_, err := Pack(opts)
		require.ErrorIs(t, err, ErrSpoolBudgetExceeded)
	})

	t.Run("spool_budget_exceeded_preserves_existing_output_and_cleans_temps", func(t *testing.T) {
		t.Parallel()
		outDir := t.TempDir()
		outPath := filepath.Join(outDir, "published")
		original := []byte("preserve existing binary on spool exhaustion")
		require.NoError(t, os.WriteFile(outPath, original, preservedOutputMode))
		before, err := os.Stat(outPath)
		require.NoError(t, err)

		opts := DefaultOptions()
		opts.StubPath = input
		opts.OutputPath = outPath
		opts.Variants = map[string]string{"v1": input, "v3": input}
		opts.DictProfitable = true
		opts.DictSize = profitableSmallDictSize
		opts.MaxSpoolBytes = tinySpoolLimitBytes
		_, err = Pack(opts)
		require.ErrorIs(t, err, ErrSpoolBudgetExceeded)

		after, err := os.Stat(outPath)
		require.NoError(t, err)
		assert.True(t, os.SameFile(before, after))
		got, err := os.ReadFile(outPath)
		require.NoError(t, err)
		assert.Equal(t, original, got)

		entries, err := os.ReadDir(outDir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, filepath.Base(outPath), entries[0].Name())
	})

	t.Run("single_variant_and_non_dict_codecs_skip_training_cleanly", func(t *testing.T) {
		t.Parallel()
		singleOpts := DefaultOptions()
		singleOpts.StubPath = input
		singleOpts.OutputPath = filepath.Join(t.TempDir(), "single")
		singleOpts.Variants = map[string]string{"v1": input}
		singleOpts.DictProfitable = true
		idx, err := Pack(singleOpts)
		require.NoError(t, err)
		assert.Zero(t, idx.DictionarySize)

		lz4Opts := DefaultOptions()
		lz4Opts.StubPath = input
		lz4Opts.OutputPath = filepath.Join(t.TempDir(), "lz4")
		lz4Opts.Variants = map[string]string{"v1": input, "v3": input}
		lz4Opts.Compression = codec.AlgorithmLZ4
		lz4Opts.DictProfitable = true
		idx, err = Pack(lz4Opts)
		require.NoError(t, err)
		assert.Zero(t, idx.DictionarySize)
	})

	t.Run("tiny_sample_warns_and_falls_back_to_no_dict", func(t *testing.T) {
		t.Parallel()
		tiny := filepath.Join(t.TempDir(), "tiny")
		require.NoError(t, os.WriteFile(tiny, []byte{1, 2, 3}, defaultFileMode))
		var warnings []string
		opts := DefaultOptions()
		opts.StubPath = tiny
		opts.OutputPath = filepath.Join(t.TempDir(), "out")
		opts.SkipELFValidation = true
		opts.Variants = map[string]string{"v1": tiny, "v3": tiny}
		opts.DictProfitable = true
		opts.WarnFunc = func(f string, a ...any) { warnings = append(warnings, fmt.Sprintf(f, a...)) }
		idx, err := Pack(opts)
		require.NoError(t, err)
		assert.Zero(t, idx.DictionarySize)
		assert.NotEmpty(t, warnings)
	})
}

func TestZstdDecodeBoundsAcrossFormatVersions(t *testing.T) {
	t.Parallel()
	payload, _ := codecPolicyInput(t, multiBlockTestSize)
	for _, version := range []int{format.FormatVersion2} {
		for _, dictProfitable := range []bool{false, true} {
			t.Run(fmt.Sprintf("format%d/profitable_%v", version, dictProfitable), func(t *testing.T) {
				t.Parallel()
				opts := DefaultOptions()
				opts.StubPath = payload
				opts.OutputPath = filepath.Join(t.TempDir(), "fat")
				opts.FormatVersion = version
				opts.Profile = codec.ProfileSize
				opts.DictSize = profitableSmallDictSize
				opts.DictProfitable = dictProfitable
				opts.Variants = map[string]string{"v1": payload, "v3": payload}
				idx, err := Pack(opts)
				require.NoError(t, err)

				artifact, err := os.ReadFile(opts.OutputPath)
				require.NoError(t, err)
				var dict []byte
				if idx.DictionarySize > 0 {
					dict = artifact[idx.DictionaryOffset : idx.DictionaryOffset+idx.DictionarySize]
				}
				for _, v := range idx.Variants {
					frame := artifact[v.Offset : v.Offset+v.CompressedSize]
					var hdr zstd.Header
					require.NoError(t, hdr.Decode(frame))
					assert.LessOrEqual(t, hdr.WindowSize, codec.DefaultDecoderMaxWindow)

					dOpts := []zstd.DOption{
						zstd.WithDecoderMaxMemory(codec.DefaultDecoderMaxMemory),
						zstd.WithDecoderMaxWindow(codec.DefaultDecoderMaxWindow),
					}
					if len(dict) > 0 {
						dOpts = append(dOpts, zstd.WithDecoderDicts(dict))
					}
					dec, decErr := zstd.NewReader(bytes.NewReader(frame), dOpts...)
					require.NoError(t, decErr)
					decoded, readErr := io.ReadAll(dec)
					dec.Close()
					require.NoError(t, readErr)
					assert.Equal(t, v.UncompressedSize, int64(len(decoded)))
				}
			})
		}
	}
}

func TestProfitabilityInternalErrorAndEdgeCoverage(t *testing.T) {
	t.Parallel()
	const (
		invalidFormatVer = 99
		stubOffsetVal    = 64
	)

	t.Run("spool_bounded_writer_zero_length_write", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		sw := &spoolBoundedWriter{w: &buf, tracker: newSpoolBudgetTracker(tinySpoolLimitBytes)}
		n, err := sw.Write(nil)
		require.NoError(t, err)
		assert.Zero(t, n)
		assert.Zero(t, sw.tracker.used.Load())
	})

	t.Run("invalid_and_removed_format_versions_in_options_rejected", func(t *testing.T) {
		t.Parallel()
		input, _ := dictionaryContractInput(t)
		opts := DefaultOptions()
		opts.StubPath = input
		opts.OutputPath = filepath.Join(t.TempDir(), "out")
		opts.Variants = map[string]string{"v1": input, "v3": input}
		opts.FormatVersion = format.FormatVersion1
		_, err := Pack(opts)
		require.ErrorIs(t, err, format.ErrUnsupportedVersion)
		require.ErrorContains(t, err, "Format v1 was removed in v0.4.0; use Format v2")

		opts.FormatVersion = invalidFormatVer
		_, err = Pack(opts)
		require.ErrorIs(t, err, format.ErrUnsupportedVersion)
	})

	t.Run("with_dict_stage_spool_exhaustion_and_worker_cancel", func(t *testing.T) {
		t.Parallel()
		stub, v1, v3 := structuredTailPaddedELF(t, profitableSmallDictSize)
		rawStat, err := os.Stat(v1)
		require.NoError(t, err)

		// Register custom dict codec so compressed size is exact (4 + rawStat.Size()).
		codecName := "test-withdict-spool-exhaust"
		codec.Register(&countingDictCodec{name: codecName})

		outDir := t.TempDir()
		opts := DefaultOptions()
		opts.StubPath = stub
		opts.OutputPath = filepath.Join(outDir, "out")
		opts.Compression = codecName
		opts.DictSize = profitableSmallDictSize
		opts.DictProfitable = true
		opts.Variants = map[string]string{"v1": v1, "v2": v1, "v3": v3, "v4": v3}
		// Allow all 4 no-dictionary frames to finish, then fail mid-way through with-dictionary staging.
		opts.MaxSpoolBytes = 4*(rawStat.Size()+4) + rawStat.Size()/2

		_, err = Pack(opts)
		require.ErrorIs(t, err, ErrSpoolBudgetExceeded)
		entries, dirErr := os.ReadDir(outDir)
		require.NoError(t, dirErr)
		assert.Empty(t, entries)
	})

	t.Run("assemble_internal_error_branches", func(t *testing.T) {
		t.Parallel()
		_, v1, v3 := structuredTailPaddedELF(t, profitableSmallDictSize*2)
		winCodec := &countingDictCodec{name: "test-internal-win", defaultShrink: profitableSmallDictSize * 2}
		loseCodec := &countingDictCodec{name: "test-internal-lose", defaultShrink: 0}
		codec.Register(winCodec)
		codec.Register(loseCodec)

		validDigest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		baseIdx := &format.Index{
			Version:    format.FormatVersion2,
			AppName:    "app",
			TargetOS:   "linux",
			TargetArch: "amd64",
		}
		levels := []string{"v1", "v3"}

		// 1. Unknown codec fails prepareVariantWorkPlans.
		badOpts := DefaultOptions()
		badOpts.Variants = map[string]string{"v1": v1, "v3": v3}
		badOpts.Compression = "nonexistent-codec-xyz"
		f1, err := os.CreateTemp(t.TempDir(), "tmp")
		require.NoError(t, err)
		defer f1.Close()
		_, err = assembleProfitableArtifact(f1, t.TempDir(), levels, &badOpts, stubOffsetVal, baseIdx, []byte("dict"), validDigest)
		require.Error(t, err)

		// 2. Invalid FormatVersion fails noDict measureCandidateCompleteBytes.
		verOpts := DefaultOptions()
		verOpts.Variants = map[string]string{"v1": v1, "v3": v3}
		verOpts.Compression = loseCodec.name
		verOpts.FormatVersion = invalidFormatVer
		_, err = assembleProfitableArtifact(f1, t.TempDir(), levels, &verOpts, stubOffsetVal, baseIdx, []byte("dict"), validDigest)
		require.ErrorIs(t, err, format.ErrUnsupportedVersion)

		// 3. Invalid dictionary SHA-256 fails withDict measureCandidateCompleteBytes while noDict succeeds.
		okOpts := DefaultOptions()
		okOpts.Variants = map[string]string{"v1": v1, "v3": v3}
		okOpts.Compression = winCodec.name
		okOpts.FormatVersion = format.FormatVersion2
		_, err = assembleProfitableArtifact(f1, t.TempDir(), levels, &okOpts, stubOffsetVal, baseIdx, []byte("dict"), "bad-sha")
		require.Error(t, err)

		// 4. Closed tmpFile fails writing dictionary when withDict wins.
		closedWin, err := os.CreateTemp(t.TempDir(), "closed-win")
		require.NoError(t, err)
		require.NoError(t, closedWin.Close())
		_, err = assembleProfitableArtifact(closedWin, t.TempDir(), levels, &okOpts, stubOffsetVal, baseIdx, []byte("dict"), validDigest)
		require.Error(t, err)

		// 5. Closed tmpFile fails appendStagedVariantFrame when noDict wins.
		loseOpts := okOpts
		loseOpts.Compression = loseCodec.name
		closedLose, err := os.CreateTemp(t.TempDir(), "closed-lose")
		require.NoError(t, err)
		require.NoError(t, closedLose.Close())
		_, err = assembleAndFinalizeProfitableArtifact(
			closedLose, closedLose.Name(), t.TempDir(), levels, &loseOpts, stubOffsetVal, baseIdx, []byte("dict"), validDigest,
		)
		require.Error(t, err)

		// 6. Missing destination parent directory fails finalizeOutputFile after successful assembly.
		renameFailOpts := loseOpts
		renameFailOpts.OutputPath = filepath.Join(t.TempDir(), "missing-parent", "out")
		validTmp, err := os.CreateTemp(t.TempDir(), "valid-tmp")
		require.NoError(t, err)
		defer func() { _ = os.Remove(validTmp.Name()) }()
		_, err = assembleAndFinalizeProfitableArtifact(
			validTmp, validTmp.Name(), t.TempDir(), levels, &renameFailOpts, stubOffsetVal, baseIdx, []byte("dict"), validDigest,
		)
		require.Error(t, err)
	})
}
