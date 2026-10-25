//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestRunImageSinglePassTrailerAndManifestErrors(t *testing.T) {
	// Serial: temporarily swaps execveFunc and os.Args.
	origExec := execveFunc
	origArgs := os.Args
	t.Cleanup(func() {
		execveFunc = origExec
		os.Args = origArgs
	})

	execveFunc = func(string, []string, []string) error {
		return nil
	}

	dir := t.TempDir()
	stubPath := filepath.Join(dir, "stub")
	payloadPath := filepath.Join(dir, "payload")
	require.NoError(t, os.WriteFile(stubPath, []byte("stub-binary-bytes-padding-to-exceed-trailer-size-cleanly-0123456789"), 0o700))
	require.NoError(t, os.WriteFile(payloadPath, []byte("variant-payload-content"), 0o700))

	host := microarch.Detect()
	level := "v1"
	if host.Arch == "arm64" {
		level = "v8.0"
	}

	for _, ver := range []int{format.FormatVersion1, format.FormatVersion2} {
		fatPath := filepath.Join(dir, "valid-v"+string(rune('0'+ver)))
		_, err := pack.Pack(pack.Options{
			StubPath:          stubPath,
			OutputPath:        fatPath,
			TargetArch:        host.Arch,
			FormatVersion:     ver,
			SkipELFValidation: true,
			Variants:          map[string]string{level: payloadPath},
		})
		require.NoError(t, err)
		os.Args = []string{fatPath}
		t.Setenv(format.EnvExecMode, format.ExecModeMemfd)
		require.NoError(t, runBinary(fatPath))
	}

	// Plain non-fat file (>= 56 bytes, missing trailer magic)
	err := runBinary(stubPath)
	require.ErrorContains(t, err, "missing magic trailer")
	require.ErrorContains(t, err, incompleteArtifactHint)

	// Truncated (< 56 bytes) file
	shortPath := filepath.Join(dir, "short")
	require.NoError(t, os.WriteFile(shortPath, []byte("short-file"), 0o700))
	err = runBinary(shortPath)
	require.ErrorContains(t, err, "missing magic trailer")
	require.ErrorContains(t, err, incompleteArtifactHint)

	// Bad trailer magic
	badMagicPath := filepath.Join(dir, "bad-magic")
	rawFat, err := os.ReadFile(filepath.Join(dir, "valid-v2"))
	require.NoError(t, err)
	badMagicBytes := bytes.Clone(rawFat)
	badMagicBytes[len(badMagicBytes)-1] ^= 0xff
	require.NoError(t, os.WriteFile(badMagicPath, badMagicBytes, 0o700))
	err = runBinary(badMagicPath)
	require.ErrorContains(t, err, "missing magic trailer")

	// Corrupted index SHA-256 checksum (valid trailer magic, flipped index byte)
	corruptIdxPath := filepath.Join(dir, "corrupt-idx")
	corruptIdxBytes := bytes.Clone(rawFat)
	corruptIdxBytes[len(corruptIdxBytes)-format.TrailerSize-1] ^= 0xff
	require.NoError(t, os.WriteFile(corruptIdxPath, corruptIdxBytes, 0o700))
	err = runBinary(corruptIdxPath)
	require.ErrorContains(t, err, "reading binary manifest")
	require.ErrorIs(t, err, format.ErrIndexCorrupted)
	require.ErrorContains(t, err, incompleteArtifactHint)
}

func TestExecuteViaMemfdPreallocateSealsBoundsAndCodecs(t *testing.T) {
	// Serial: hooks package-level memfd/execve/cgroup functions.
	origExec := execveFunc
	origSeal := memfdSealFunc
	origPrealloc := memfdPreallocateFunc
	origLimits := readCgroupLimitsFunc
	t.Cleanup(func() {
		execveFunc = origExec
		memfdSealFunc = origSeal
		memfdPreallocateFunc = origPrealloc
		readCgroupLimitsFunc = origLimits
	})

	const sampleCount = 32
	const linesPerSample = 48
	const dictSize = 16 * 1024

	samples := make([][]byte, sampleCount)
	for i := range samples {
		var sb bytes.Buffer
		for j := range linesPerSample {
			_, _ = sb.WriteString("MEMFD_DICT_SYMBOL_")
			sb.WriteByte(byte('A' + i%26))
			sb.WriteByte(byte('0' + (i/10)%10))
			sb.WriteByte(byte('0' + i%10))
			sb.WriteByte(byte('0' + (j/10)%10))
			sb.WriteByte(byte('0' + j%10))
			sb.WriteByte('\n')
		}
		samples[i] = sb.Bytes()
	}
	dictBytes, err := codec.TrainDictionary(samples, dictSize, "fastest")
	require.NoError(t, err)
	dictSum := sha256.Sum256(dictBytes)
	dictHex := hex.EncodeToString(dictSum[:])

	payload := bytes.Repeat(samples[0], 8)
	payloadSum := sha256.Sum256(payload)
	payloadHex := hex.EncodeToString(payloadSum[:])

	hostInfo := microarch.Info{OS: "linux", Arch: "amd64", Level: "v3"}
	policyRes := microarch.PolicyResult{SelectedVariant: "v1"}

	codecCases := []struct {
		name     string
		algo     string
		withDict bool
	}{
		{"none", codec.AlgorithmNone, false},
		{"lz4", codec.AlgorithmLZ4, false},
		{"zstd", codec.AlgorithmZstd, false},
		{"zstd+dict", codec.AlgorithmZstd, true},
	}

	dir := t.TempDir()
	for _, tc := range codecCases {
		t.Run(tc.name, func(t *testing.T) {
			c, getErr := codec.Get(tc.algo)
			require.NoError(t, getErr)

			var compBuf bytes.Buffer
			if tc.withDict {
				dc := c.(codec.DictCodec)
				require.NoError(t, dc.CompressWithDict(&compBuf, payload, "fastest", dictBytes))
			} else {
				require.NoError(t, c.Compress(&compBuf, payload, "fastest"))
			}

			var imageBuf bytes.Buffer
			var idx *format.Index
			var payloadOffset int64
			if tc.withDict {
				_, _ = imageBuf.Write(dictBytes)
				payloadOffset = int64(len(dictBytes))
				idx = &format.Index{
					DictionaryOffset: 0,
					DictionarySize:   int64(len(dictBytes)),
					DictionarySHA256: dictHex,
				}
			}
			_, _ = imageBuf.Write(compBuf.Bytes())

			imgPath := filepath.Join(dir, "img-"+tc.name)
			require.NoError(t, os.WriteFile(imgPath, imageBuf.Bytes(), 0o600))
			imgFile, openErr := os.Open(imgPath)
			require.NoError(t, openErr)
			defer func() { _ = imgFile.Close() }()

			entry := &format.VariantEntry{
				Level:            "v1",
				Offset:           payloadOffset,
				CompressedSize:   int64(compBuf.Len()),
				UncompressedSize: int64(len(payload)),
				SHA256:           payloadHex,
				Compression:      tc.algo,
			}

			// 1. Happy path + verify preallocate called + mandatory seals applied + exact size
			var preallocSize int64
			memfdPreallocateFunc = func(fd int, length int64) error {
				preallocSize = length
				return unix.Ftruncate(fd, length)
			}
			var appliedSeals int
			memfdSealFunc = func(fd int, seals int) error {
				appliedSeals = seals
				_, sErr := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals)
				return sErr
			}
			execveFunc = func(procPath string, _ []string, _ []string) error {
				st, statErr := os.Stat(procPath)
				require.NoError(t, statErr)
				assert.Equal(t, int64(len(payload)), st.Size())
				return nil
			}
			readCgroupLimitsFunc = cgroup.ReadLimits

			require.NoError(t, executeViaMemfd(imgPath, imgFile, entry, idx, []string{imgPath}, nil, hostInfo, policyRes, time.Now()))
			assert.Equal(t, int64(len(payload)), preallocSize)
			assert.Equal(t, memfdTargetSeals, appliedSeals)

			// 2. Non-fatal preallocate failure (e.g. ENOSYS) still succeeds cleanly
			memfdPreallocateFunc = func(int, int64) error { return syscall.ENOSYS }
			require.NoError(t, executeViaMemfd(imgPath, imgFile, entry, idx, []string{imgPath}, nil, hostInfo, policyRes, time.Now()))
			memfdPreallocateFunc = unix.Ftruncate

			// 3. Underflow (declared larger than actual) fails closed BEFORE sealing
			sealCalled := false
			memfdSealFunc = func(int, int) error {
				sealCalled = true
				return nil
			}
			underflowEntry := *entry
			underflowEntry.UncompressedSize = int64(len(payload) + 64)
			uErr := executeViaMemfd(imgPath, imgFile, &underflowEntry, idx, []string{imgPath}, nil, hostInfo, policyRes, time.Now())
			require.ErrorIs(t, uErr, format.ErrMemfdExtract)
			require.ErrorIs(t, uErr, codec.ErrSizeMismatch)
			assert.False(t, sealCalled, "underflow must fail before memfd sealing")

			// 4. Overflow (declared smaller than actual) fails closed BEFORE sealing
			overflowEntry := *entry
			overflowEntry.UncompressedSize = int64(len(payload) - 64)
			oErr := executeViaMemfd(imgPath, imgFile, &overflowEntry, idx, []string{imgPath}, nil, hostInfo, policyRes, time.Now())
			require.ErrorIs(t, oErr, format.ErrMemfdExtract)
			require.ErrorIs(t, oErr, codec.ErrSizeMismatch)
			assert.False(t, sealCalled, "overflow must fail before memfd sealing")

			// 5. Payload SHA-256 mismatch and dictionary SHA-256 mismatch fail closed BEFORE sealing
			badHashEntry := *entry
			badHashEntry.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
			hErr := executeViaMemfd(imgPath, imgFile, &badHashEntry, idx, []string{imgPath}, nil, hostInfo, policyRes, time.Now())
			require.ErrorIs(t, hErr, format.ErrPayloadCorrupted)
			assert.False(t, sealCalled)

			if tc.withDict {
				badDictIdx := *idx
				badDictIdx.DictionarySHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
				dErr := executeViaMemfd(imgPath, imgFile, entry, &badDictIdx, []string{imgPath}, nil, hostInfo, policyRes, time.Now())
				require.ErrorIs(t, dErr, format.ErrDictionaryCorrupted)
				assert.False(t, sealCalled)
			}

			// 6. Cgroup memory-budget preflight rejects before memfd creation
			readCgroupLimitsFunc = func() (cgroup.Limits, error) {
				return cgroup.Limits{
					CgroupVersion:             cgroup.VersionV2,
					MemoryLimitBytes:          1024,
					EffectiveMemoryLimitBytes: 1024,
					MemoryLimitFinite:         true,
				}, nil
			}
			bErr := executeViaMemfd(imgPath, imgFile, entry, idx, []string{imgPath}, nil, hostInfo, policyRes, time.Now())
			require.ErrorIs(t, bErr, cgroup.ErrMemoryBudget)
			assert.False(t, errors.Is(bErr, format.ErrMemfdSealingFailed))
		})
	}
}
