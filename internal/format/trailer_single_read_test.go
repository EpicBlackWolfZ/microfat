package format_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingReaderAt struct {
	r     io.ReaderAt
	reads atomic.Int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	c.reads.Add(1)
	return c.r.ReadAt(p, off)
}

type failingOffsetReaderAt struct {
	err error
}

func (f failingOffsetReaderAt) ReadAt([]byte, int64) (int, error) {
	return 0, f.err
}

func TestReadTrailerAndIndexSingleTrailerReadAndSentinels(t *testing.T) {
	t.Parallel()

	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			t.Parallel()
			payload := []byte("payload-bytes")
			var buf bytes.Buffer
			_, err := buf.Write(payload)
			require.NoError(t, err)

			idx := &format.Index{
				Version:    version,
				AppName:    "app",
				TargetOS:   "linux",
				TargetArch: "amd64",
				Variants: []format.VariantEntry{
					{
						Level:            "v1",
						Offset:           0,
						CompressedSize:   int64(len(payload)),
						UncompressedSize: int64(len(payload)),
						SHA256:           "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
						Compression:      "none",
					},
				},
			}
			_, err = format.WriteIndexAndTrailerWithVersion(&buf, idx, int64(len(payload)), version)
			require.NoError(t, err)

			cr := &countingReaderAt{r: bytes.NewReader(buf.Bytes())}
			parsed, err := format.ReadTrailerAndIndex(cr, int64(buf.Len()))
			require.NoError(t, err)
			require.Equal(t, version, parsed.Version)
			assert.Equal(t, int64(2), cr.reads.Load(), "expected exactly 1 trailer pread + 1 index pread")
		})
	}

	_, err := format.ReadTrailerAndIndex(bytes.NewReader([]byte("tiny")), 4)
	require.ErrorIs(t, err, format.ErrBinaryTooSmall)
	assert.True(t, format.IsMissingTrailerError(err))

	badMagic := bytes.Repeat([]byte("X"), format.TrailerSize*2)
	_, err = format.ReadTrailerAndIndex(bytes.NewReader(badMagic), int64(len(badMagic)))
	require.ErrorIs(t, err, format.ErrInvalidMagic)
	assert.True(t, format.IsMissingTrailerError(err))

	ioBoom := errors.New("simulated pread failure")
	_, err = format.ReadTrailerAndIndex(failingOffsetReaderAt{err: ioBoom}, format.TrailerSize*2)
	require.ErrorIs(t, err, format.ErrTrailerRead)
	require.ErrorIs(t, err, ioBoom)
	assert.False(t, format.IsMissingTrailerError(err))
}
