package pack

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
)

type failingInput struct{}

func (failingInput) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestBoundedInputCopy(t *testing.T) {
	t.Parallel()
	const limit = 16
	for _, size := range []int{0, limit, limit + 1} {
		var output bytes.Buffer
		err := copyBoundedInput(&output, bytes.NewReader(bytes.Repeat([]byte{'x'}, size)), limit, int64(size))
		switch size {
		case 0:
			require.ErrorIs(t, err, ErrInvalidELF)
		case limit:
			require.NoError(t, err)
			require.Len(t, output.Bytes(), limit)
		default:
			require.ErrorIs(t, err, format.ErrPayloadTooLarge)
		}
	}
	require.ErrorIs(t, copyBoundedInput(io.Discard, failingInput{}, limit, 0), io.ErrUnexpectedEOF)
	require.ErrorIs(t, copyBoundedInput(io.Discard, bytes.NewReader(nil), 0, 0), format.ErrPayloadTooLarge)
	require.EqualValues(t, 1024*1024*1024, format.MaxPayloadSize)
}

func TestInputSnapshotsAndBounds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "snapshot")
	const limit = 16
	content := bytes.Repeat([]byte{'x'}, limit)
	require.NoError(t, os.WriteFile(source, content, 0o600))
	require.NoError(t, snapshotInput(source, target, limit))
	require.NoError(t, os.WriteFile(source, []byte("replacement"), 0o600))
	got, err := readBoundedInput(target, limit)
	require.NoError(t, err)
	require.Equal(t, content, got)
	_, err = readBoundedInput(target, limit-1)
	require.ErrorIs(t, err, format.ErrPayloadTooLarge)
	require.ErrorIs(t, snapshotInput(target, filepath.Join(dir, "too-large"), limit-1), format.ErrPayloadTooLarge)
	require.Error(t, snapshotInput(target, target, limit))
	require.NoError(t, os.WriteFile(source, nil, 0o600))
	require.ErrorIs(t, snapshotInput(source, filepath.Join(dir, "empty"), limit), ErrInvalidELF)
	_, err = readBoundedInput(source, limit)
	require.ErrorIs(t, err, ErrInvalidELF)
	_, err = sampleInput(source, limit)
	require.ErrorIs(t, err, ErrInvalidELF)
	_, err = sampleInput(target, limit-1)
	require.ErrorIs(t, err, format.ErrPayloadTooLarge)
	_, err = sampleInput(target, limit)
	require.NoError(t, err)
	require.True(t, errors.Is(snapshotInput("/missing", target, limit), os.ErrNotExist))
}

func TestChangingInputAndAggregateSamples(t *testing.T) {
	t.Parallel()
	const limit = 16
	require.ErrorIs(t, copyBoundedInput(io.Discard, bytes.NewReader([]byte("grown")), limit, 1), ErrSizeMismatch)
	require.ErrorIs(t, copyBoundedInput(io.Discard, bytes.NewReader([]byte("short")), limit, limit), ErrSizeMismatch)
	dir := t.TempDir()
	sparse := filepath.Join(dir, "oversized")
	f, err := os.Create(sparse)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(format.MaxPayloadSize+1))
	require.NoError(t, f.Close())
	require.ErrorIs(t, ValidateELFBinary(sparse, "linux", "amd64"), format.ErrPayloadTooLarge)
	source := filepath.Join(dir, "samples")
	require.NoError(t, os.WriteFile(source, make([]byte, sampleChunkSize*maxSamplesPerFile), 0o600))
	const sampleFiles = 33
	paths := map[string]string{"v1": source}
	levels := make([]string, sampleFiles)
	for i := range levels {
		levels[i] = "v1"
	}
	_, err = sampleVariantPayloads(paths, levels)
	require.ErrorIs(t, err, format.ErrPayloadTooLarge)
	output := filepath.Join(dir, "existing-output")
	require.NoError(t, os.WriteFile(output, []byte("keep"), 0o600))
	opts := DefaultOptions()
	opts.StubPath = source
	opts.SkipELFValidation = true
	opts.Variants = map[string]string{"v1": sparse}
	opts.OutputPath = output
	_, err = Pack(opts)
	require.ErrorIs(t, err, format.ErrPayloadTooLarge)
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, "keep", string(data))
}
