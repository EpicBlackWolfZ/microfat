package codec

import (
	"errors"
	"fmt"
	"io"
	"sync"
)

const noneCopyBufferSize = 128 * 1024

var noneCopyBufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, noneCopyBufferSize)
		return &buf
	},
}

// NoneCodec implements the Codec interface for uncompressed passthrough payloads.
type NoneCodec struct{}

// NewNoneCodec returns a new instance of NoneCodec.
func NewNoneCodec() *NoneCodec {
	return &NoneCodec{}
}

// Name returns the unique identifier "none".
func (c *NoneCodec) Name() string {
	return AlgorithmNone
}

// Compress writes uncompressed src bytes directly to w.
func (c *NoneCodec) Compress(w io.Writer, src []byte, _ string) error {
	if _, err := w.Write(src); err != nil {
		return fmt.Errorf("writing uncompressed payload: %w", err)
	}
	return nil
}

// Decompress reads raw bytes from r directly into w, verifying the exact declared size.
func (c *NoneCodec) Decompress(w io.Writer, r io.Reader, uncompressedSize int64) error {
	if uncompressedSize < 0 {
		return fmt.Errorf("%w: invalid negative uncompressed size %d", ErrSizeMismatch, uncompressedSize)
	}
	if uncompressedSize > DefaultMaxPayloadSize {
		return fmt.Errorf("%w: uncompressed size %d exceeds safety limit %d", ErrSizeMismatch, uncompressedSize, DefaultMaxPayloadSize)
	}

	bw := newBoundedWriter(w, uncompressedSize)
	var copyErr error
	if wt, ok := r.(io.WriterTo); ok {
		_, copyErr = wt.WriteTo(bw)
	} else {
		bufPtr := noneCopyBufferPool.Get().(*[]byte)
		_, copyErr = io.CopyBuffer(bw, r, *bufPtr)
		noneCopyBufferPool.Put(bufPtr)
	}
	if copyErr != nil {
		if errors.Is(copyErr, ErrSizeMismatch) {
			return copyErr
		}
		return fmt.Errorf("%w: %w", ErrDecompressionFailed, copyErr)
	}

	if bw.written != uncompressedSize {
		return fmt.Errorf("%w: expected %d bytes, got %d", ErrSizeMismatch, uncompressedSize, bw.written)
	}

	return nil
}
