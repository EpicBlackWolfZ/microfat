package codec

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBoundedWriter(t *testing.T) {
	t.Parallel()

	t.Run("Exact limit writes", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		bw := newBoundedWriter(&buf, 100)

		n1, err1 := bw.Write(make([]byte, 50))
		require.NoError(t, err1)
		require.Equal(t, 50, n1)

		n2, err2 := bw.Write(make([]byte, 50))
		require.NoError(t, err2)
		require.Equal(t, 50, n2)
		require.Equal(t, int64(100), bw.written)
		require.Equal(t, 100, buf.Len())
	})

	t.Run("Exceeding limit on first write", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		bw := newBoundedWriter(&buf, 50)

		n, err := bw.Write(make([]byte, 100))
		require.ErrorIs(t, err, ErrSizeMismatch)
		require.Equal(t, 50, n)
		require.Equal(t, int64(50), bw.written)
		require.Equal(t, 50, buf.Len())
	})

	t.Run("Exceeding limit on subsequent write", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		bw := newBoundedWriter(&buf, 100)

		n1, err1 := bw.Write(make([]byte, 80))
		require.NoError(t, err1)
		require.Equal(t, 80, n1)

		n2, err2 := bw.Write(make([]byte, 30))
		require.ErrorIs(t, err2, ErrSizeMismatch)
		require.Equal(t, 20, n2)
		require.Equal(t, int64(100), bw.written)
		require.Equal(t, 100, buf.Len())
	})

	t.Run("Write after limit already exhausted", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		bw := newBoundedWriter(&buf, 50)

		_, _ = bw.Write(make([]byte, 60))
		n, err := bw.Write([]byte("extra"))
		require.ErrorIs(t, err, ErrSizeMismatch)
		require.Zero(t, n)
	})

	t.Run("Empty slice write", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		bw := newBoundedWriter(&buf, 50)

		n, err := bw.Write([]byte{})
		require.NoError(t, err)
		require.Zero(t, n)
	})

	t.Run("Unspecified limit defaults to DefaultMaxPayloadSize", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		bw1 := newBoundedWriter(&buf, 0)
		require.Equal(t, DefaultMaxPayloadSize, bw1.limit)

		bw2 := newBoundedWriter(&buf, -5)
		require.Equal(t, DefaultMaxPayloadSize, bw2.limit)
	})

	t.Run("Underlying writer error propagation", func(t *testing.T) {
		t.Parallel()
		ew := &errWriterInternal{}
		bw := newBoundedWriter(ew, 100)

		n, err := bw.Write(make([]byte, 150))
		require.ErrorIs(t, err, errSimulatedWrite)
		require.Zero(t, n)
	})
}

var errSimulatedWrite = errors.New("simulated internal write error")

type errWriterInternal struct{}

func (e *errWriterInternal) Write(p []byte) (n int, err error) {
	return 0, errSimulatedWrite
}
