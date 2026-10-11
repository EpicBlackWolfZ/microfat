package codec_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"syscall"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZstdEquivalentNumericLevels(t *testing.T) {
	t.Parallel()
	c := codec.NewZstdCodec()
	payload := generateTestData(testPayloadSizeTiny)
	cases := []struct {
		number int
		level  zstd.EncoderLevel
	}{
		{-2, zstd.SpeedFastest}, {0, zstd.SpeedFastest}, {1, zstd.SpeedFastest},
		{2, zstd.SpeedDefault}, {3, zstd.SpeedBetterCompression}, {4, zstd.SpeedBetterCompression},
		{9, zstd.SpeedBetterCompression}, {10, zstd.SpeedBestCompression},
		{11, zstd.SpeedBestCompression}, {19, zstd.SpeedBestCompression},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.number), func(t *testing.T) {
			var control bytes.Buffer
			require.NoError(t, c.Compress(&control, payload, tc.level.String()))
			for _, spelling := range []string{
				strconv.Itoa(tc.number), fmt.Sprintf("%+d", tc.number), fmt.Sprintf("%03d", tc.number),
				" \t" + strconv.Itoa(tc.number) + "\n",
			} {
				t.Run(fmt.Sprintf("%q", spelling), func(t *testing.T) {
					level, err := c.MapLevel(spelling)
					require.NoError(t, err)
					assert.Equal(t, tc.level, level)
					for _, inline := range []bool{false, true} {
						algorithm, separate := codec.AlgorithmZstd, spelling
						if inline {
							algorithm, separate = "zstd:"+spelling, ""
						}
						resolved, resolvedLevel, resolveErr := codec.ResolveCompression("", algorithm, separate, int64(len(payload)))
						require.NoError(t, resolveErr)
						var compressed, decoded bytes.Buffer
						require.NoError(t, resolved.Compress(&compressed, payload, resolvedLevel))
						assert.Equal(t, control.Bytes(), compressed.Bytes())
						require.NoError(t, resolved.Decompress(&decoded, &compressed, int64(len(payload))))
						assert.Equal(t, payload, decoded.Bytes())
					}
				})
			}
		})
	}
}

func TestCodecExactDeclaredSize(t *testing.T) {
	t.Parallel()
	samples := generateDictionarySamples()
	dict, err := codec.TrainDictionary(samples, 4096, testLevelFastest)
	require.NoError(t, err)
	payload := []byte("payload larger than the declared bound")
	for _, name := range []string{codec.AlgorithmZstd, codec.AlgorithmLZ4, codec.AlgorithmNone} {
		for _, withDict := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dictionary=%t", name, withDict), func(t *testing.T) {
				t.Parallel()
				c, getErr := codec.Get(name)
				require.NoError(t, getErr)
				var dictionary []byte
				if withDict {
					dictionary = dict
				}
				cases := []struct {
					name    string
					payload []byte
					size    int64
					written int
					valid   bool
				}{
					{"zero with payload", payload, 0, 0, false},
					{"positive bound", payload, 1, 1, false},
					{"negative declaration", payload, -1, 0, false},
					{"oversized declaration", payload, codec.DefaultMaxPayloadSize + 1, 0, false},
					{"empty at zero", nil, 0, 0, true},
					{"exact payload", payload, int64(len(payload)), len(payload), true},
					{"short payload", payload, int64(len(payload) + 1), len(payload), false},
				}
				for _, tc := range cases {
					t.Run(tc.name, func(t *testing.T) {
						var compressed, output bytes.Buffer
						if dc, ok := c.(codec.DictCodec); ok && withDict {
							require.NoError(t, dc.CompressWithDict(&compressed, tc.payload, testLevelFastest, dictionary))
						} else {
							require.NoError(t, c.Compress(&compressed, tc.payload, testLevelFastest))
						}
						decompressErr := codec.DecompressWithOptionalDict(c, &output, &compressed, tc.size, dictionary)
						if tc.valid {
							require.NoError(t, decompressErr)
						} else {
							require.ErrorIs(t, decompressErr, codec.ErrSizeMismatch)
							assert.NotErrorIs(t, decompressErr, codec.ErrDecompressionFailed)
						}
						assert.Equal(t, tc.written, output.Len())
					})
				}
			})
		}
	}
}

type failingCodecIO struct{ err error }

func (f failingCodecIO) Write([]byte) (int, error) { return 0, f.err }
func (f failingCodecIO) Read([]byte) (int, error)  { return 0, f.err }

func TestCodecDecompressionRetainsIOCause(t *testing.T) {
	t.Parallel()
	payload := []byte("ordinary payload")
	for _, name := range []string{codec.AlgorithmZstd, codec.AlgorithmLZ4, codec.AlgorithmNone} {
		for _, cause := range []syscall.Errno{syscall.ENOSPC, syscall.EIO} {
			for _, sourceFails := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/reader=%t", name, cause.Error(), sourceFails), func(t *testing.T) {
					t.Parallel()
					c, getErr := codec.Get(name)
					require.NoError(t, getErr)
					var compressed bytes.Buffer
					require.NoError(t, c.Compress(&compressed, payload, testLevelFastest))
					var destination io.Writer = failingCodecIO{cause}
					var source io.Reader = &compressed
					if sourceFails {
						destination, source = io.Discard, failingCodecIO{cause}
					}
					err := c.Decompress(destination, source, int64(len(payload)))
					require.ErrorIs(t, err, codec.ErrDecompressionFailed)
					require.ErrorIs(t, err, cause)
					var errno syscall.Errno
					require.True(t, errors.As(err, &errno))
					assert.Equal(t, cause, errno)
					value, errnoName := format.ExtractErrno(err)
					assert.Equal(t, int(cause), value)
					assert.NotEmpty(t, errnoName)
				})
			}
		}
	}
}
