package codec

import (
	"errors"
	"fmt"
	"io"
)

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

// Decompress reads raw bytes from r directly into w, verifying size if specified.
func (c *NoneCodec) Decompress(w io.Writer, r io.Reader, uncompressedSize int64) error {
	bw := newBoundedWriter(w, uncompressedSize)
	_, copyErr := io.Copy(bw, r)
	if copyErr != nil {
		if errors.Is(copyErr, ErrSizeMismatch) {
			return copyErr
		}
		return fmt.Errorf("%w: %v", ErrDecompressionFailed, copyErr)
	}

	if uncompressedSize > 0 && bw.written != uncompressedSize {
		return fmt.Errorf("%w: expected %d bytes, got %d", ErrSizeMismatch, uncompressedSize, bw.written)
	}

	return nil
}

