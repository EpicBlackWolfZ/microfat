package format

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIndexChecksumCasing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		digest string
	}{
		{name: "lowercase", digest: testSHA256Sample},
		{name: "uppercase", digest: strings.ToUpper(testSHA256Sample)},
		{name: "mixed", digest: strings.ReplaceAll(testSHA256Sample, "abcdef", "aBcDeF")},
	}
	for _, version := range []int{FormatVersion1, FormatVersion2} {
		for _, dictionary := range cases {
			for _, payload := range cases {
				t.Run(fmt.Sprintf("v%d/dictionary_%s/payload_%s", version, dictionary.name, payload.name), func(t *testing.T) {
					t.Parallel()
					idx := checksumCasingIndex(version, dictionary.digest, payload.digest)
					var encoded []byte
					var err error
					var decoded *Index
					if version == FormatVersion2 {
						encoded, err = MarshalBinaryIndex(idx)
						require.NoError(t, err)
						decoded, err = UnmarshalBinaryIndex(encoded)
					} else {
						encoded, err = marshalJSONIndex(idx)
						require.NoError(t, err)
						decoded, err = unmarshalJSONIndex(encoded)
					}
					require.NoError(t, err)
					require.Contains(t, string(encoded), dictionary.digest, "fixture must preserve wire casing")
					require.Contains(t, string(encoded), payload.digest, "fixture must preserve wire casing")
					require.Equal(t, testSHA256Sample, decoded.DictionarySHA256)
					require.Equal(t, testSHA256Sample, decoded.Variants[0].SHA256)

					buf := bytes.NewBuffer([]byte{0, 0})
					_, err = WriteIndexAndTrailerWithVersion(buf, idx, int64(buf.Len()), version)
					require.NoError(t, err)
					decoded, err = ReadTrailerAndIndex(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
					require.NoError(t, err)
					require.Equal(t, testSHA256Sample, decoded.DictionarySHA256)
					require.Equal(t, testSHA256Sample, decoded.Variants[0].SHA256)

					// Normalization must happen after verifying the original index hash.
					data := bytes.Clone(buf.Bytes())
					wirePosition := bytes.Index(data, []byte(payload.digest))
					require.NotEqual(t, -1, wirePosition)
					data[wirePosition] = 'f'
					_, err = ReadTrailerAndIndex(bytes.NewReader(data), int64(len(data)))
					require.ErrorIs(t, err, ErrIndexCorrupted)
				})
			}
		}
	}
}

func TestIndexChecksumCasingRejectsInvalidHex(t *testing.T) {
	t.Parallel()
	for _, version := range []int{FormatVersion1, FormatVersion2} {
		for _, field := range []string{"dictionary", "payload"} {
			for _, invalid := range []string{"", testSHA256Sample[:maxSHA256HexLen-1], testSHA256Sample + "A", "G" + testSHA256Sample[1:]} {
				t.Run(fmt.Sprintf("v%d/%s/%q", version, field, invalid), func(t *testing.T) {
					t.Parallel()
					idx := checksumCasingIndex(version, testSHA256Sample, testSHA256Sample)
					if field == "dictionary" {
						idx.DictionarySHA256 = invalid
					} else {
						idx.Variants[0].SHA256 = invalid
					}
					buf := bytes.NewBuffer([]byte{0, 0})
					_, err := WriteIndexAndTrailerWithVersion(buf, idx, int64(buf.Len()), version)
					if version == FormatVersion2 {
						require.ErrorIs(t, err, ErrInvalidChecksum)
						return
					}
					require.NoError(t, err)
					_, err = ReadTrailerAndIndex(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
					require.ErrorIs(t, err, ErrInvalidChecksum)
				})
			}
		}
	}
}

func checksumCasingIndex(version int, dictionary, payload string) *Index {
	return &Index{
		Version:          version,
		TargetOS:         testOSLinux,
		TargetArch:       testArchAMD64,
		DictionarySize:   1,
		DictionarySHA256: dictionary,
		Variants: []VariantEntry{{
			Level:            "v1",
			Offset:           1,
			CompressedSize:   1,
			UncompressedSize: 1,
			SHA256:           payload,
			Compression:      "none",
		}},
	}
}
