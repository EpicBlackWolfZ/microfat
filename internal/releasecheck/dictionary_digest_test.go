package releasecheck_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/require"
)

func TestValidateArchiveSharedDictionaryIntegrity(t *testing.T) {
	t.Parallel()
	contract, err := releasecheck.NewReleaseContract("0.3.0")
	require.NoError(t, err)
	tiers := contract.ExpectedTiers[releasecheck.ArchAMD64]
	var samples [][]byte
	const sampleSize = 8 * 1024
	for _, tier := range tiers {
		payload := getCompiledTestBinary(t, releasecheck.ArchAMD64, tier)
		for start := 0; start < len(payload); start += sampleSize {
			samples = append(samples, payload[start:min(start+sampleSize, len(payload))])
		}
	}
	const dictionarySize = 32 * 1024
	dictionary, err := codec.TrainDictionary(samples, dictionarySize, "fastest")
	require.NoError(t, err)
	require.NotEmpty(t, dictionary)
	stub := getCompiledTestBinary(t, releasecheck.ArchAMD64, tiers[0])
	for _, algorithm := range []string{"none", "zstd"} {
		for _, corruption := range []string{"valid", "bytes", "digest"} {
			t.Run(algorithm+"/"+corruption, func(t *testing.T) {
				t.Parallel()
				fat := createValidFatBinaryWithCodec(t, releasecheck.ArchAMD64, tiers, dictionary, algorithm)
				idx, err := format.ReadTrailerAndIndex(bytes.NewReader(fat), int64(len(fat)))
				require.NoError(t, err)
				if corruption == "bytes" {
					fat[idx.DictionaryOffset] ^= 1
				} else if corruption == "digest" {
					idx.DictionarySHA256 = strings.Repeat("0", 64)
					payloadEnd := idx.Variants[len(idx.Variants)-1].Offset + idx.Variants[len(idx.Variants)-1].CompressedSize
					var rebuilt bytes.Buffer
					rebuilt.Write(fat[:payloadEnd])
					_, err = format.WriteIndexAndTrailer(&rebuilt, idx, payloadEnd)
					require.NoError(t, err)
					fat = rebuilt.Bytes()
				}
				archive := filepath.Join(t.TempDir(), "dictionary.tar.gz")
				createOrderedTarGz(t, archive, []tarEntry{
					{Name: releasecheck.ReleaseProjectName, Data: fat, Mode: 0o755},
					{Name: releasecheck.ReleaseFullStub, Data: stub, Mode: 0o755},
					{Name: releasecheck.ReleaseMinStub, Data: stub, Mode: 0o755},
				})
				facts, err := releasecheck.ValidateArchive(archive, releasecheck.ArchAMD64, contract)
				if corruption == "valid" {
					require.NoError(t, err)
					require.Len(t, facts.EmbeddedVariants, len(tiers))
					require.NoError(t, facts.Cleanup())
				} else {
					require.ErrorIs(t, err, format.ErrDictionaryCorrupted)
					require.Nil(t, facts)
				}
				_, err = os.Stat(archive)
				require.NoError(t, err, "validation must not mutate the input archive")
			})
		}
	}
}
