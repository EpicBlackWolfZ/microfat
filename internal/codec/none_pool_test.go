package codec_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoneCodecSectionReaderPooledBufferAndWriterTo(t *testing.T) {
	t.Parallel()

	const payloadSize = 300 * 1024 // spans multiple 128 KiB pooled copy chunks
	payload := generateTestData(payloadSize)
	noneCodec := codec.NewNoneCodec()

	// 1. Non-WriterTo path (io.SectionReader mirrors launcher extractVariantToWriter)
	secReader := io.NewSectionReader(bytes.NewReader(payload), 0, int64(len(payload)))
	var outSection bytes.Buffer
	require.NoError(t, noneCodec.Decompress(&outSection, secReader, int64(len(payload))))
	assert.Equal(t, payload, outSection.Bytes())

	// 2. WriterTo path (*bytes.Reader)
	var outWriterTo bytes.Buffer
	require.NoError(t, noneCodec.Decompress(&outWriterTo, bytes.NewReader(payload), int64(len(payload))))
	assert.Equal(t, payload, outWriterTo.Bytes())
}
