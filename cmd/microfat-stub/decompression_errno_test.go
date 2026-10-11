//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type errnoExtractionWriter struct{ errno syscall.Errno }

func (w errnoExtractionWriter) Write([]byte) (int, error) { return 0, w.errno }

func TestDecompressionErrnoThroughExtractionAndTelemetry(t *testing.T) {
	// The logger reads process environment and writes os.Stderr.
	t.Setenv(format.EnvLog, "json")
	for _, name := range []string{codec.AlgorithmZstd, codec.AlgorithmLZ4, codec.AlgorithmNone} {
		for _, cause := range []syscall.Errno{syscall.ENOSPC, syscall.EIO} {
			t.Run(fmt.Sprintf("%s/%s", name, cause.Error()), func(t *testing.T) {
				payload := []byte("ordinary payload for extraction")
				digest := sha256.Sum256(payload)
				c, err := codec.Get(name)
				require.NoError(t, err)
				var compressed bytes.Buffer
				require.NoError(t, c.Compress(&compressed, payload, "fastest"))
				path := filepath.Join(t.TempDir(), "payload")
				require.NoError(t, os.WriteFile(path, compressed.Bytes(), 0o600))
				file, err := os.Open(path)
				require.NoError(t, err)
				t.Cleanup(func() { _ = file.Close() })
				entry := &format.VariantEntry{
					Level: "v1", Compression: name, CompressedSize: int64(compressed.Len()),
					UncompressedSize: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]),
				}
				err = extractVariantToWriter(file, entry, nil, errnoExtractionWriter{cause})
				require.ErrorIs(t, err, codec.ErrDecompressionFailed)
				require.ErrorIs(t, err, cause)
				var errno syscall.Errno
				require.True(t, errors.As(err, &errno))
				assert.Equal(t, cause, errno)
				value, errnoName := format.ExtractErrno(err)
				assert.Equal(t, int(cause), value)
				assert.NotEmpty(t, errnoName)
				assert.True(t, isPayloadCorruptionOrDecompressionError(err), "sentinel still makes extraction terminal")

				capture, openErr := os.CreateTemp(t.TempDir(), "telemetry")
				require.NoError(t, openErr)
				t.Cleanup(func() { _ = capture.Close() })
				oldStderr := os.Stderr
				os.Stderr = capture
				logErrorDiagnosticsWithAttempts(format.StageMemfdExtract, fmt.Errorf("dispatch wrapper: %w", err),
					microarch.Info{Arch: testArchAMD64, Level: "v1"}, entry, microarch.PolicyResult{},
					format.ExecModeAuto, format.ExecModeMemfd, []format.ExecutionAttempt{{
						AttemptedMode: format.ExecModeMemfd, Err: err,
					}}, "")
				os.Stderr = oldStderr
				_, seekErr := capture.Seek(0, io.SeekStart)
				require.NoError(t, seekErr)
				data, readErr := io.ReadAll(capture)
				require.NoError(t, readErr)
				var telemetry format.ErrorTelemetry
				require.NoError(t, json.Unmarshal(bytes.TrimSpace([]byte(strings.TrimPrefix(string(data), "[microfat] "))), &telemetry))
				assert.Equal(t, format.EventError, telemetry.Event)
				assert.Equal(t, int(cause), telemetry.Errno)
				assert.Equal(t, errnoName, telemetry.ErrnoName)
			})
		}
	}
}
