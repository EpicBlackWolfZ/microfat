package installrelease

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquisitionFilesystemAndVerifiedArchiveFailures(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"lost-checksums", "conflicting-products", "invalid-archive"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			archive := archiveFixture(t, "amd64", nil)
			if scenario == "invalid-archive" {
				archive = []byte("authenticated but malformed producer output")
			}
			client := testClient(t, releaseResponses(archive))
			staging := t.TempDir()
			verifier := verifierFunc(func(_ context.Context, _, checksums, _ string) error {
				switch scenario {
				case "lost-checksums":
					require.NoError(t, os.Remove(checksums))
				case "conflicting-products":
					require.NoError(t, os.WriteFile(filepath.Join(staging, "products"), []byte("unrelated"), fileMode))
				}
				return nil
			})
			generation, source, err := client.Acquire(t.Context(), "0.3.0", "amd64", staging, verifier)
			require.Error(t, err)
			assert.Empty(t, generation.ID)
			assert.Empty(t, source)
		})
	}
}

func TestArchiveTruncationAndUnusableDestinations(t *testing.T) {
	t.Parallel()
	archive := filepath.Join(t.TempDir(), "archive")
	// A valid gzip header with a truncated tar header must fail during extraction.
	var truncated bytes.Buffer
	writer := gzip.NewWriter(&truncated)
	_, err := writer.Write([]byte("truncated tar header"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, os.WriteFile(archive, truncated.Bytes(), fileMode))
	_, err = extract(archive, t.TempDir(), "amd64")
	require.Error(t, err)
	data := archiveFixture(t, "amd64", nil)
	data[len(data)-1] ^= 1 // gzip trailer corruption is checked even after tar EOF.
	require.NoError(t, os.WriteFile(archive, data, fileMode))
	_, err = extract(archive, t.TempDir(), "amd64")
	require.Error(t, err)
	require.NoError(t, os.WriteFile(archive, archiveFixture(t, "amd64", nil), fileMode))
	_, err = extract(archive, filepath.Join(t.TempDir(), "missing"), "amd64")
	require.Error(t, err)
	root, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	defer root.Close()
	header := &tar.Header{Size: 10, Mode: executableMode}
	_, err = extractProduct(root, strings.NewReader("short"), header, "microfat")
	require.ErrorContains(t, err, "truncated")
	_, err = extractProduct(root, strings.NewReader("1234567890"), header, "microfat")
	require.ErrorIs(t, err, os.ErrExist)
	_, err = extractProduct(root, failingReader{}, header, "microfat-stub")
	require.Error(t, err)
	require.Error(t, validateProduct(root, "absent", "amd64"))
	require.Error(t, validateProduct(root, "microfat", "amd64"))
	file, err := root.OpenFile("microfat", os.O_WRONLY|os.O_TRUNC, fileMode)
	require.NoError(t, err)
	_, err = file.Write(fatFixture(t, "arm64"))
	require.NoError(t, err)
	require.NoError(t, file.Close())
	// Preserve the ELF machine while contradicting the independent fat index target.
	file, err = root.OpenFile("microfat", os.O_WRONLY, fileMode)
	require.NoError(t, err)
	_, err = file.WriteAt(elfFixture("amd64"), 0)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.ErrorContains(t, validateProduct(root, "microfat", "amd64"), "fat CLI target")
}

type failingOutput struct{}

func (failingOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestDownloadAndDigestReadFailures(t *testing.T) {
	t.Parallel()
	client := NewClient()
	require.Error(t, client.get(t.Context(), "\n", 1, io.Discard))
	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("bytes")), ContentLength: 5}
	require.ErrorIs(t, copyResponse(response, 10, failingOutput{}), io.ErrClosedPipe)
	require.Error(t, VerifyDigest(filepath.Join(t.TempDir(), "absent"), strings.Repeat("0", 64), maxDownloadBytes))
	if os.Geteuid() == 0 {
		return
	}
	verifier := pinnedScript(t, "exit 0")
	require.NoError(t, os.Chmod(verifier.Path, 0))
	t.Cleanup(func() { _ = os.Chmod(verifier.Path, 0o700) })
	require.ErrorIs(t, VerifyDigest(verifier.Path, verifier.SHA256, maxDownloadBytes), os.ErrPermission)
}
