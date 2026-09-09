package pack

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

// snapshotInputs consumes each input descriptor once into a private staging directory.
// Validation, dictionary sampling and compression then use these bounded snapshots,
// so replacing or growing the original pathname cannot change the consumed bytes.
func snapshotInputs(opts *Options) (func(), error) {
	dir, err := os.MkdirTemp("", "microfat-inputs-*")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	paths := make(map[string]string, len(opts.Variants)+1)
	snapshot := func(path string) (string, error) {
		clean := filepath.Clean(path)
		if existing, ok := paths[clean]; ok {
			return existing, nil
		}
		target := filepath.Join(dir, fmt.Sprintf("input-%d", len(paths)))
		if err := snapshotInput(clean, target, format.MaxPayloadSize); err != nil {
			return "", err
		}
		if !opts.SkipELFValidation {
			if err := ValidateELFBinary(target, opts.TargetOS, opts.TargetArch); err != nil {
				return "", err
			}
		}
		paths[clean] = target
		return target, nil
	}
	stub, err := snapshot(opts.StubPath)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("%w: snapshot stub: %w", ErrStubMissing, err)
	}
	variants := make(map[string]string, len(opts.Variants))
	for level, path := range opts.Variants {
		variants[level], err = snapshot(path)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("%w: snapshot variant %s: %w", ErrVariantNotFound, level, err)
		}
	}
	opts.StubPath = stub
	opts.Variants = variants
	return cleanup, nil
}

const inputSnapshotMode = 0o600

func snapshotInput(source, destination string, limit int64) error {
	input, err := os.Open(filepath.Clean(source))
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	stat, err := input.Stat()
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() || stat.Size() <= 0 {
		return fmt.Errorf("%w: input must be a nonempty regular file", ErrInvalidELF)
	}
	if stat.Size() > limit {
		return format.ErrPayloadTooLarge
	}
	output, err := os.OpenFile(filepath.Clean(destination), os.O_WRONLY|os.O_CREATE|os.O_EXCL, inputSnapshotMode)
	if err != nil {
		return err
	}
	defer func() { _ = output.Close() }()
	if err := copyBoundedInput(output, input, limit, stat.Size()); err != nil {
		return err
	}
	return output.Close()
}

func copyBoundedInput(output io.Writer, input io.Reader, limit, expectedSize int64) error {
	if limit <= 0 || limit > format.MaxPayloadSize {
		return format.ErrPayloadTooLarge
	}
	n, err := io.Copy(output, io.LimitReader(input, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return format.ErrPayloadTooLarge
	}
	if n == 0 {
		return fmt.Errorf("%w: empty input", ErrInvalidELF)
	}
	if n != expectedSize {
		return fmt.Errorf("%w: input changed while snapshotting", ErrSizeMismatch)
	}
	return nil
}

func readBoundedInput(path string, limit int64) ([]byte, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() <= 0 {
		return nil, fmt.Errorf("%w: empty or non-regular input", ErrInvalidELF)
	}
	if stat.Size() > limit {
		return nil, format.ErrPayloadTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, format.ErrPayloadTooLarge
	}
	if int64(len(data)) != stat.Size() {
		return nil, fmt.Errorf("%w: input size changed", ErrSizeMismatch)
	}
	return data, nil
}
