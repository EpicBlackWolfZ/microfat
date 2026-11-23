package codec

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspectZstdFrameWindow(t *testing.T) {
	t.Parallel()

	const (
		win4KiB          = uint64(4 * 1024)
		win256MiB        = uint64(256 * 1024 * 1024)
		smallLen         = 256
		largeRawLen      = 32 * 1024
		skippableID      = 3
		skippableHeader  = 8
		skippablePayload = 16
		headerLead       = 12
		negativeOffset   = -1
		truncPrefixLen   = 6
		magicLen         = 4
		bogusCompressed  = 100
		maxBadLen        = 14
		tinySkipLen      = 1024
		fhdMagicByte0    = 0x28
		fhdMagicByte1    = 0xb5
		fhdMagicByte2    = 0x2f
		fhdMagicByte3    = 0xfd
		fhdReservedBit   = 0x08
	)

	makeSkippableFrame := func(payloadLen int) []byte {
		const magicSkippable = uint32(0x184D2A50 | skippableID)
		frame := make([]byte, skippableHeader+payloadLen)
		binary.LittleEndian.PutUint32(frame[0:magicLen], magicSkippable)
		binary.LittleEndian.PutUint32(frame[magicLen:skippableHeader], uint32(payloadLen))
		return frame
	}

	makeCraftedWindowFrame := func(window uint64) []byte {
		frame, err := (&zstd.Header{WindowSize: window}).AppendTo(nil)
		require.NoError(t, err)
		// Terminate with a 1-byte empty raw block (last=1, type=0, size=0 -> 0x01,0x00,0x00).
		return append(frame, 0x01, 0x00, 0x00)
	}

	// Stream payload larger than win4KiB so zstd writes a real Window_Descriptor (4 KiB) instead of SingleSegment.
	var multiBlockFrame bytes.Buffer
	enc, err := zstd.NewWriter(&multiBlockFrame, zstd.WithWindowSize(int(win4KiB)))
	require.NoError(t, err)
	rawMulti := bytes.Repeat([]byte("microfat-zstd-window-"), largeRawLen/21)
	_, err = enc.Write(rawMulti)
	require.NoError(t, err)
	require.NoError(t, enc.Close())

	singleSegPayload := bytes.Repeat([]byte("single-seg-"), smallLen/11)
	var singleSegFrame bytes.Buffer
	singleEnc, err := zstd.NewWriter(&singleSegFrame, zstd.WithSingleSegment(true))
	require.NoError(t, err)
	_, err = singleEnc.Write(singleSegPayload)
	require.NoError(t, err)
	require.NoError(t, singleEnc.Close())

	t.Run("normal_frame_at_offset", func(t *testing.T) {
		t.Parallel()
		stream := multiBlockFrame.Bytes()
		blob := append(make([]byte, headerLead), stream...)
		win, winErr := InspectZstdFrameWindow(bytes.NewReader(blob), headerLead, int64(len(stream)))
		require.NoError(t, winErr)
		assert.Equal(t, win4KiB, win)
	})

	t.Run("single_segment_clamped_to_min_window", func(t *testing.T) {
		t.Parallel()
		stream := singleSegFrame.Bytes()
		win, winErr := InspectZstdFrameWindow(bytes.NewReader(stream), 0, int64(len(stream)))
		require.NoError(t, winErr)
		assert.Equal(t, uint64(zstd.MinWindowSize), win)
	})

	t.Run("leading_skippable_frame", func(t *testing.T) {
		t.Parallel()
		combined := append(makeSkippableFrame(skippablePayload), multiBlockFrame.Bytes()...)
		win, winErr := InspectZstdFrameWindow(bytes.NewReader(combined), 0, int64(len(combined)))
		require.NoError(t, winErr)
		assert.Equal(t, win4KiB, win)
	})

	t.Run("excessive_skippable_frames_rejected", func(t *testing.T) {
		t.Parallel()
		var combined []byte
		for range zstdMaxSkippableFrames + 1 {
			combined = append(combined, makeSkippableFrame(0)...)
		}
		combined = append(combined, multiBlockFrame.Bytes()...)
		_, winErr := InspectZstdFrameWindow(bytes.NewReader(combined), 0, int64(len(combined)))
		require.ErrorIs(t, winErr, ErrDecompressionFailed)
	})

	t.Run("crafted_oversized_window_rejected", func(t *testing.T) {
		t.Parallel()
		frame := makeCraftedWindowFrame(win256MiB)
		win, winErr := InspectZstdFrameWindow(bytes.NewReader(frame), 0, int64(len(frame)))
		require.ErrorIs(t, winErr, ErrWindowSizeExceeded)
		assert.Greater(t, win, DefaultDecoderMaxWindow)
	})

	t.Run("single_segment_oversized_fcs_rejected", func(t *testing.T) {
		t.Parallel()
		frame, appendErr := (&zstd.Header{SingleSegment: true, FrameContentSize: win256MiB}).AppendTo(nil)
		require.NoError(t, appendErr)
		frame = append(frame, 0x01, 0x00, 0x00)
		_, winErr := InspectZstdFrameWindow(bytes.NewReader(frame), 0, int64(len(frame)))
		require.ErrorIs(t, winErr, ErrWindowSizeExceeded)
	})

	t.Run("invalid_inputs_and_corruption", func(t *testing.T) {
		t.Parallel()
		_, winErr := InspectZstdFrameWindow(nil, 0, skippablePayload)
		require.ErrorIs(t, winErr, ErrDecompressionFailed)

		_, winErr = InspectZstdFrameWindow(bytes.NewReader([]byte{1, 2, 3, 4}), negativeOffset, magicLen)
		require.ErrorIs(t, winErr, ErrDecompressionFailed)

		_, winErr = InspectZstdFrameWindow(bytes.NewReader([]byte{1, 2, 3, 4}), 0, 0)
		require.ErrorIs(t, winErr, ErrDecompressionFailed)

		_, winErr = InspectZstdFrameWindow(bytes.NewReader([]byte{1, 2, 3, 4}), math.MaxInt64, 1)
		require.ErrorIs(t, winErr, ErrDecompressionFailed)

		_, winErr = InspectZstdFrameWindow(bytes.NewReader([]byte("not-zstd-magic")), 0, maxBadLen)
		require.ErrorIs(t, winErr, ErrDecompressionFailed)
		assert.NotErrorIs(t, winErr, ErrWindowSizeExceeded)

		truncatedSkip := makeSkippableFrame(tinySkipLen)[:skippablePayload]
		_, winErr = InspectZstdFrameWindow(bytes.NewReader(truncatedSkip), 0, int64(len(truncatedSkip)))
		require.ErrorIs(t, winErr, ErrDecompressionFailed)

		shortRead := bytes.NewReader([]byte{fhdMagicByte0, fhdMagicByte1, fhdMagicByte2, fhdMagicByte3})
		_, winErr = InspectZstdFrameWindow(shortRead, 0, bogusCompressed)
		require.ErrorIs(t, winErr, ErrDecompressionFailed)

		reservedHeader := []byte{fhdMagicByte0, fhdMagicByte1, fhdMagicByte2, fhdMagicByte3, fhdReservedBit, 0x00}
		_, winErr = InspectZstdFrameWindow(bytes.NewReader(reservedHeader), 0, int64(len(reservedHeader)))
		require.ErrorIs(t, winErr, ErrDecompressionFailed)
	})

	t.Run("eof_reader_with_short_slice", func(t *testing.T) {
		t.Parallel()
		stream := singleSegFrame.Bytes()
		// Truncate to a valid frame header shorter than HeaderMaxSize so ReadAt returns io.EOF with n > 0.
		headerOnly := bytes.Clone(stream[:truncPrefixLen])
		win, winErr := InspectZstdFrameWindow(bytes.NewReader(headerOnly), 0, int64(len(headerOnly)))
		if winErr != nil {
			require.ErrorIs(t, winErr, ErrDecompressionFailed)
		} else {
			assert.Equal(t, uint64(zstd.MinWindowSize), win)
		}
	})
}

type failingReaderAt struct{}

func (failingReaderAt) ReadAt([]byte, int64) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestDecompressWithDictAndWindow(t *testing.T) {
	t.Parallel()

	const (
		win4KiB       = uint64(4 * 1024)
		win256KiB     = uint64(256 * 1024)
		streamSize    = 64 * 1024
		frame1Size    = 16 * 1024
		frame2Size    = 512 * 1024
		overMaxWindow = DefaultDecoderMaxWindow + 1
		errReadOffset = 0
		errReadLen    = 32
	)

	_, readErr := InspectZstdFrameWindow(failingReaderAt{}, errReadOffset, errReadLen)
	require.ErrorIs(t, readErr, ErrDecompressionFailed)

	zc := NewZstdCodec()

	t.Run("streaming_payload_larger_than_max_window_succeeds", func(t *testing.T) {
		t.Parallel()
		raw := bytes.Repeat([]byte("abcd0123wxyz9876"), streamSize/16)
		var compressed bytes.Buffer
		enc, err := zstd.NewWriter(&compressed, zstd.WithWindowSize(int(win4KiB)))
		require.NoError(t, err)
		_, err = enc.Write(raw)
		require.NoError(t, err)
		require.NoError(t, enc.Close())

		inspected, err := InspectZstdFrameWindow(bytes.NewReader(compressed.Bytes()), 0, int64(compressed.Len()))
		require.NoError(t, err)
		assert.Equal(t, win4KiB, inspected)

		var out bytes.Buffer
		err = DecompressWithOptionalDictAndWindow(zc, &out, bytes.NewReader(compressed.Bytes()), int64(len(raw)), nil, inspected)
		require.NoError(t, err)
		assert.Equal(t, raw, out.Bytes())
	})

	t.Run("multi_frame_window_escalation_rejected", func(t *testing.T) {
		t.Parallel()
		var frame1, frame2 bytes.Buffer
		raw1 := bytes.Repeat([]byte("frame1-"), frame1Size/7+1)[:frame1Size]
		raw2 := bytes.Repeat([]byte("frame2-"), frame2Size/7+1)[:frame2Size]

		e1, err := zstd.NewWriter(&frame1, zstd.WithWindowSize(int(win4KiB)))
		require.NoError(t, err)
		_, err = e1.Write(raw1)
		require.NoError(t, err)
		require.NoError(t, e1.Close())

		e2, err := zstd.NewWriter(&frame2, zstd.WithWindowSize(int(win256KiB)))
		require.NoError(t, err)
		_, err = e2.Write(raw2)
		require.NoError(t, err)
		require.NoError(t, e2.Close())

		combined := append(bytes.Clone(frame1.Bytes()), frame2.Bytes()...)
		inspected, err := InspectZstdFrameWindow(bytes.NewReader(combined), 0, int64(len(combined)))
		require.NoError(t, err)
		assert.Equal(t, win4KiB, inspected)

		var out bytes.Buffer
		err = zc.DecompressWithDictAndWindow(&out, bytes.NewReader(combined), int64(len(raw1)+len(raw2)), nil, inspected)
		require.ErrorIs(t, err, ErrDecompressionFailed)
		require.ErrorIs(t, err, ErrWindowSizeExceeded)
	})

	t.Run("rejects_window_exceeding_default_max", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		err := zc.DecompressWithDictAndWindow(&out, bytes.NewReader(nil), 1, nil, overMaxWindow)
		require.ErrorIs(t, err, ErrWindowSizeExceeded)
	})
}
