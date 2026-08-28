package codec

import (
	"bytes"
	"errors"
	"testing"
)

func TestBoundedWriter(t *testing.T) {
	t.Parallel()

	t.Run("Exact limit writes", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		bw := newBoundedWriter(&buf, 100)

		n1, err1 := bw.Write(make([]byte, 50))
		if err1 != nil || n1 != 50 {
			t.Fatalf("first write failed: n=%d, err=%v", n1, err1)
		}

		n2, err2 := bw.Write(make([]byte, 50))
		if err2 != nil || n2 != 50 {
			t.Fatalf("second write failed: n=%d, err=%v", n2, err2)
		}

		if bw.written != 100 || buf.Len() != 100 {
			t.Fatalf("expected 100 bytes written, got bw.written=%d, buf.Len()=%d", bw.written, buf.Len())
		}
	})

	t.Run("Exceeding limit on first write", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		bw := newBoundedWriter(&buf, 50)

		n, err := bw.Write(make([]byte, 100))
		if !errors.Is(err, ErrSizeMismatch) {
			t.Fatalf("expected ErrSizeMismatch, got %v", err)
		}
		if n != 50 || bw.written != 50 || buf.Len() != 50 {
			t.Fatalf("expected 50 bytes written, got n=%d, bw.written=%d, buf.Len()=%d", n, bw.written, buf.Len())
		}
	})

	t.Run("Exceeding limit on subsequent write", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		bw := newBoundedWriter(&buf, 100)

		n1, err1 := bw.Write(make([]byte, 80))
		if err1 != nil || n1 != 80 {
			t.Fatalf("write 1 failed: n=%d, err=%v", n1, err1)
		}

		n2, err2 := bw.Write(make([]byte, 30))
		if !errors.Is(err2, ErrSizeMismatch) {
			t.Fatalf("expected ErrSizeMismatch, got %v", err2)
		}
		if n2 != 20 || bw.written != 100 || buf.Len() != 100 {
			t.Fatalf("expected 20 bytes written in second write, got n2=%d, bw.written=%d, buf.Len()=%d", n2, bw.written, buf.Len())
		}
	})

	t.Run("Write after limit already exhausted", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		bw := newBoundedWriter(&buf, 50)

		_, _ = bw.Write(make([]byte, 60))
		n, err := bw.Write([]byte("extra"))
		if !errors.Is(err, ErrSizeMismatch) {
			t.Fatalf("expected ErrSizeMismatch, got %v", err)
		}
		if n != 0 {
			t.Fatalf("expected 0 bytes written after exhaustion, got %d", n)
		}
	})

	t.Run("Empty slice write", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		bw := newBoundedWriter(&buf, 50)

		n, err := bw.Write([]byte{})
		if err != nil || n != 0 {
			t.Fatalf("expected 0 bytes and nil error on empty write, got n=%d, err=%v", n, err)
		}
	})

	t.Run("Unspecified limit defaults to DefaultMaxPayloadSize", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		bw1 := newBoundedWriter(&buf, 0)
		if bw1.limit != DefaultMaxPayloadSize {
			t.Fatalf("expected limit %d for 0, got %d", DefaultMaxPayloadSize, bw1.limit)
		}

		bw2 := newBoundedWriter(&buf, -5)
		if bw2.limit != DefaultMaxPayloadSize {
			t.Fatalf("expected limit %d for negative, got %d", DefaultMaxPayloadSize, bw2.limit)
		}
	})

	t.Run("Underlying writer error propagation", func(t *testing.T) {
		t.Parallel()
		ew := &errWriterInternal{}
		bw := newBoundedWriter(ew, 100)

		n, err := bw.Write(make([]byte, 150))
		if err == nil || !errors.Is(err, errSimulatedWrite) {
			t.Fatalf("expected simulated write error, got %v", err)
		}
		if n != 0 {
			t.Fatalf("expected 0 bytes on underlying error, got %d", n)
		}
	})
}

var errSimulatedWrite = errors.New("simulated internal write error")

type errWriterInternal struct{}

func (e *errWriterInternal) Write(p []byte) (n int, err error) {
	return 0, errSimulatedWrite
}
