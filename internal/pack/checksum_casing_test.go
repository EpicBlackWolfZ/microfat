package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyBinaryChecksumCasing(t *testing.T) {
	t.Parallel()

	versions := []struct {
		name    string
		version int
	}{
		{name: "json_v1", version: format.FormatVersion1},
		{name: "binary_v2", version: format.FormatVersion2},
	}
	cases := []struct {
		name           string
		payloadCase    func(string) string
		dictionaryCase func(string) string
		wrongPayload   bool
		wrongDict      bool
		corruptPayload bool
		corruptDict    bool
	}{
		{name: "lowercase", payloadCase: strings.ToLower, dictionaryCase: strings.ToLower},
		{name: "uppercase_payload", payloadCase: strings.ToUpper, dictionaryCase: strings.ToLower},
		{name: "mixed_case_payload", payloadCase: alternatingChecksumCase, dictionaryCase: strings.ToLower},
		{name: "uppercase_dictionary", payloadCase: strings.ToLower, dictionaryCase: strings.ToUpper},
		{name: "mixed_case_dictionary", payloadCase: strings.ToLower, dictionaryCase: alternatingChecksumCase},
		{name: "uppercase_both", payloadCase: strings.ToUpper, dictionaryCase: strings.ToUpper},
		{name: "mixed_case_both", payloadCase: alternatingChecksumCase, dictionaryCase: alternatingChecksumCase},
		{name: "wrong_payload_hash", payloadCase: strings.ToUpper, dictionaryCase: strings.ToUpper, wrongPayload: true},
		{name: "wrong_dictionary_hash", payloadCase: strings.ToUpper, dictionaryCase: strings.ToUpper, wrongDict: true},
		{name: "corrupt_payload", payloadCase: strings.ToUpper, dictionaryCase: strings.ToUpper, corruptPayload: true},
		{name: "corrupt_dictionary", payloadCase: strings.ToUpper, dictionaryCase: strings.ToUpper, corruptDict: true},
	}

	for _, version := range versions {
		t.Run(version.name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					payload := []byte("checksum casing regression payload")
					dictionary := []byte("checksum casing regression dictionary")
					payloadHash := checksumCasingSHA256(payload)
					dictionaryHash := checksumCasingSHA256(dictionary)
					if tc.wrongPayload {
						payloadHash = checksumCasingSHA256([]byte("different payload"))
					}
					if tc.wrongDict {
						dictionaryHash = checksumCasingSHA256([]byte("different dictionary"))
					}
					idx := &format.Index{
						Version:          version.version,
						AppName:          "checksum-casing",
						TargetOS:         testOSLinux,
						TargetArch:       testArchAMD64,
						DictionarySize:   int64(len(dictionary)),
						DictionarySHA256: tc.dictionaryCase(dictionaryHash),
						Variants: []format.VariantEntry{{
							Level:            "v1",
							Offset:           int64(len(dictionary)),
							CompressedSize:   int64(len(payload)),
							UncompressedSize: int64(len(payload)),
							SHA256:           tc.payloadCase(payloadHash),
							Compression:      codec.AlgorithmNone,
						}},
					}
					var binary bytes.Buffer
					_, err := binary.Write(dictionary)
					require.NoError(t, err)
					_, err = binary.Write(payload)
					require.NoError(t, err)
					indexOffset := binary.Len()
					_, err = format.WriteIndexAndTrailerWithVersion(&binary, idx, int64(indexOffset), version.version)
					require.NoError(t, err)
					// Assert the fixture preserves wire casing so canonicalizing writers cannot hide a parser regression.
					require.True(t, bytes.Contains(binary.Bytes()[indexOffset:], []byte(idx.Variants[0].SHA256)))
					require.True(t, bytes.Contains(binary.Bytes()[indexOffset:], []byte(idx.DictionarySHA256)))
					if tc.corruptPayload {
						binary.Bytes()[idx.Variants[0].Offset] ^= 1
					}
					if tc.corruptDict {
						binary.Bytes()[idx.DictionaryOffset] ^= 1
					}

					parsed, results, err := VerifyBinary(bytes.NewReader(binary.Bytes()), int64(binary.Len()))
					if tc.wrongDict || tc.corruptDict {
						require.ErrorIs(t, err, format.ErrDictionaryCorrupted)
						return
					}
					require.NoError(t, err)
					require.Len(t, results, 1)
					assert.Equal(t, payloadHash, parsed.Variants[0].SHA256)
					assert.Equal(t, dictionaryHash, parsed.DictionarySHA256)
					assert.Equal(t, payloadHash, results[0].ExpectedSHA256)
					if tc.wrongPayload || tc.corruptPayload {
						assert.False(t, results[0].Valid)
						require.ErrorIs(t, results[0].Error, ErrChecksumMismatch)
						return
					}
					assert.True(t, results[0].Valid)
					assert.NoError(t, results[0].Error)
					assert.Equal(t, payloadHash, results[0].ActualSHA256)
				})
			}
		})
	}
}

func checksumCasingSHA256(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func alternatingChecksumCase(checksum string) string {
	result := []byte(checksum)
	uppercase := true
	for i, value := range result {
		if value < 'a' || value > 'f' {
			continue
		}
		if uppercase {
			result[i] = value - ('a' - 'A')
		}
		uppercase = !uppercase
	}
	return string(result)
}
