package codec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type dummyTestCodec struct {
	name string
}

func (d *dummyTestCodec) Name() string { return d.name }
func (d *dummyTestCodec) Compress(w io.Writer, src []byte, level string) error {
	_, err := w.Write(src)
	return err
}
func (d *dummyTestCodec) Decompress(w io.Writer, r io.Reader, uncompressedSize int64) error {
	_, err := io.Copy(w, r)
	return err
}

func TestGetUnsupportedCodecPendingWriterChild(t *testing.T) {
	if os.Getenv("MICROFAT_TEST_REGISTRY_DEADLOCK_CHILD") != "1" {
		t.Skip("skipping deadlock child runner")
	}

	inRLock := make(chan struct{})
	allowGetToProceed := make(chan struct{})
	dummy := &dummyTestCodec{name: "test-writer-codec"}

	getDone := make(chan error, 1)
	go func() {
		_, err := getWithHook("nonexistent-codec-name", func() {
			close(inRLock)
			<-allowGetToProceed
		})
		getDone <- err
	}()

	<-inRLock

	regDone := make(chan struct{})
	go func() {
		Register(dummy)
		close(regDone)
	}()

	deadline := time.Now().Add(3 * time.Second)
	writerPending := false
	for time.Now().Before(deadline) {
		if registryMu.TryRLock() {
			registryMu.RUnlock()
			runtime.Gosched()
			time.Sleep(100 * time.Microsecond)
		} else {
			writerPending = true
			break
		}
	}
	if !writerPending {
		t.Fatal("timed out waiting for writer to become pending on registryMu")
	}

	close(allowGetToProceed)

	select {
	case err := <-getDone:
		if !errors.Is(err, ErrUnsupportedCodec) {
			t.Fatalf("expected ErrUnsupportedCodec, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("deadlock: Get failed to complete with pending writer")
	}

	select {
	case <-regDone:
	case <-time.After(1 * time.Second):
		t.Fatal("deadlock: Register failed to complete")
	}
}

func TestGetUnsupportedCodecPendingWriter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestGetUnsupportedCodecPendingWriterChild")
	cmd.Env = append(os.Environ(), "MICROFAT_TEST_REGISTRY_DEADLOCK_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("deadlock regression failed or timed out: %v\nOutput:\n%s", err, string(out))
	}
	require.Contains(t, string(out), "PASS")
}

func TestRegistryConcurrencyAndOperations(t *testing.T) {
	// Snapshot original registry state and restore after test
	registryMu.Lock()
	originalRegistry := make(map[string]Codec, len(registry))
	for k, v := range registry {
		originalRegistry[k] = v
	}
	registryMu.Unlock()

	defer func() {
		registryMu.Lock()
		registry = make(map[string]Codec, len(originalRegistry))
		for k, v := range originalRegistry {
			registry[k] = v
		}
		registryMu.Unlock()
	}()

	// 1. Successful lookups, case-insensitivity, and empty string
	for _, name := range []string{"zstd", "lz4", "none", "ZSTD", "LZ4", "None", "  zstd  ", ""} {
		c, err := Get(name)
		require.NoError(t, err)
		require.NotNil(t, c)
	}

	// 2. Unsupported codec returns ErrUnsupportedCodec with sorted supported names
	_, err := Get("unsupported_codec_xyz")
	require.ErrorIs(t, err, ErrUnsupportedCodec)
	require.Contains(t, err.Error(), "unsupported_codec_xyz")
	require.Contains(t, err.Error(), "supported: ")

	var hookInvoked bool
	_, err = getWithHook("unsupported_codec_hook", func() {
		hookInvoked = true
	})
	require.ErrorIs(t, err, ErrUnsupportedCodec)
	require.True(t, hookInvoked)

	// 3. Register nil does nothing
	Register(nil)

	// 4. Replacement registration
	origZstd, err := Get("zstd")
	require.NoError(t, err)
	replacement := &dummyTestCodec{name: "zstd"}
	Register(replacement)
	gotZstd, err := Get("zstd")
	require.NoError(t, err)
	require.Equal(t, replacement, gotZstd)
	Register(origZstd) // restore

	// 5. Concurrent Get, List, and Register activity
	const workers = 8
	const iterations = 50
	stopCh := make(chan struct{})

	errCh := make(chan error, workers*3)
	for i := 0; i < workers; i++ {
		go func() {
			for j := 0; j < iterations; j++ {
				select {
				case <-stopCh:
					return
				default:
					_, _ = Get("zstd")
					_, _ = Get("unknown")
				}
			}
			errCh <- nil
		}()
		go func() {
			for j := 0; j < iterations; j++ {
				select {
				case <-stopCh:
					return
				default:
					list := List()
					if len(list) == 0 {
						errCh <- errors.New("empty list")
						return
					}
				}
			}
			errCh <- nil
		}()
		go func(workerID int) {
			for j := 0; j < iterations; j++ {
				select {
				case <-stopCh:
					return
				default:
					cName := fmt.Sprintf("dynamic-codec-%d-%d", workerID, j)
					Register(&dummyTestCodec{name: cName})
					c, err := Get(cName)
					if err != nil || c == nil {
						errCh <- fmt.Errorf("failed to retrieve newly registered codec %s: %w", cName, err)
						return
					}
				}
			}
			errCh <- nil
		}(i)
	}

	for i := 0; i < workers*3; i++ {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			close(stopCh)
			t.Fatal("timeout in concurrent registry operations")
		}
	}

	finalList := List()
	require.NotEmpty(t, finalList)
	require.Contains(t, finalList, "zstd")
	require.Contains(t, finalList, "lz4")
	require.Contains(t, finalList, "none")
}
