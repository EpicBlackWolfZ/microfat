package releaseaudit

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/require"
)

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestMatrixFilesystemFailures(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"payload", "case directory", "results", "progress", "stub mismatch",
		"stub absent", "packed absent", "cache absent"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			options, runner, products, fake := setupFake(t, "amd64")
			progress := io.Discard
			switch failure {
			case "payload":
				require.NoError(t, os.Mkdir(filepath.Join(options.Output, "payload.go"), directoryMode))
			case "case directory":
				require.NoError(t, os.Mkdir(filepath.Join(options.Output, "v2-full-none-dict0"), directoryMode))
			case "results":
				require.NoError(t, os.Mkdir(filepath.Join(options.Output, "results.json"), directoryMode))
			case "progress":
				progress = failedWriter{}
			case "stub mismatch":
				write(t, filepath.Join(products, fullStub), "not same")
			case "stub absent":
				require.NoError(t, os.Remove(filepath.Join(products, fullStub)))
			case "packed absent":
				fake.change = func(spec process.Spec, _ *Result) {
					if len(spec.Args) > 0 && spec.Args[0] == "pack" {
						require.NoError(t, os.Remove(argument(spec, "-o")))
					}
				}
			case "cache absent":
				fake.change = func(spec process.Spec, _ *Result) {
					if len(spec.Args) > 0 && spec.Args[0] == "--microfat:prewarm=all,json" {
						require.NoError(t, os.RemoveAll(filepath.Join(filepath.Dir(spec.Path), "prewarm")))
					}
				}
			}
			require.Error(t, matrix(options, runner, products, progress))
		})
	}
}

func TestArtifactFilesystemErrors(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	absent := filepath.Join(directory, "absent")
	require.Error(t, checkStub(absent, absent))
	stub := filepath.Join(directory, "stub")
	packed := filepath.Join(directory, "packed")
	write(t, stub, "longer stub")
	write(t, packed, "short")
	require.Error(t, checkStub(packed, stub))
	require.Error(t, writeJSON(absent, make(chan bool)))
	_, err := snapshot(absent)
	require.Error(t, err)
	require.NoError(t, os.Symlink("missing", filepath.Join(directory, "link")))
	_, err = snapshot(directory)
	require.Error(t, err)
	_, err = Digest(directory)
	require.Error(t, err)
	require.Error(t, lowercaseChecksums(absent))
	require.Error(t, releaseSchema(absent, "0.2.4"))
	write(t, packed, "invalid json")
	require.Error(t, releaseSchema(packed, "0.2.4"))
	write(t, packed, `{}`)
	require.Error(t, releaseSchema(packed, "0.2.4"))
	index := packedIndex{}
	index.Variants = append(index.Variants, struct {
		Offset int64 `json:"offset"`
	}{Offset: 0})
	runner := runnerFor(t, executor(Result{}, nil))
	require.Error(t, rejectCorruption(runner, cliName, absent, index, directory))
	require.Error(t, rejectCorruption(runner, cliName, packed, index, absent))
	require.NoError(t, os.Mkdir(filepath.Join(directory, "corrupt"), directoryMode))
	require.Error(t, rejectCorruption(runner, cliName, packed, index, directory))
	options := optionsFor(t)
	options.Version = "bad"
	_, err = Authenticate(options, runner)
	require.Error(t, err)
}

func TestExtractionMissingProductAndInvalidGzip(t *testing.T) {
	t.Parallel()
	options := optionsFor(t)
	require.NoError(t, os.Mkdir(options.Output, directoryMode))
	makeArchive(t, options, productHeaders()[1:])
	_, err := Extract(options)
	require.Error(t, err)
	children, err := os.ReadDir(filepath.Join(options.Output, "products"))
	require.NoError(t, err)
	require.Empty(t, children)
	options = optionsFor(t)
	require.NoError(t, os.Mkdir(options.Output, directoryMode))
	_, err = Extract(options)
	require.Error(t, err)
	makeArchive(t, options, productHeaders())
	archive := filepath.Join(options.Dist, "microfat_0.2.4_linux_"+options.Arch+".tar.gz")
	data, err := os.ReadFile(archive)
	require.NoError(t, err)
	data[len(data)-1] ^= 0xff
	require.NoError(t, os.WriteFile(archive, data, fileMode))
	require.Error(t, preflight(archive))
	// A huge declared member must be rejected without allocating or decompressing it.
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	writer := tar.NewWriter(gz)
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "huge", Typeflag: tar.TypeReg, Size: maxFileBytes + 1, Mode: 0o755}))
	require.NoError(t, gz.Close())
	require.NoError(t, os.WriteFile(archive, compressed.Bytes(), fileMode))
	require.Error(t, preflight(archive))
}

func TestCancellationRetainsCommandRecord(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	runner := runnerFor(t, func(ctx context.Context, _ process.Spec) (Result, error) {
		cancel()
		<-ctx.Done()
		return Result{}, ctx.Err()
	})
	runner.Context = ctx
	_, err := runner.Run([]string{"product"}, nil, true)
	require.ErrorIs(t, err, context.Canceled)
	data, err := os.ReadFile(filepath.Join(runner.Output, "commands.jsonl"))
	require.NoError(t, err)
	require.Contains(t, string(data), "context canceled")
	require.NoError(t, lowercaseChecksums(writeChecksum(t, "\n")))
}
func writeChecksum(t *testing.T, value string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "checksums.txt")
	write(t, filename, strings.TrimSpace(value)+"\n")
	return filename
}

func TestPreflight_TarEntryTypes(t *testing.T) {
	t.Parallel()

	writeTarGz := func(t *testing.T, entries []struct {
		hdr  tar.Header
		body []byte
	}) string {
		t.Helper()
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for _, e := range entries {
			hdr := e.hdr
			hdr.Size = int64(len(e.body))
			if e.hdr.Size != 0 {
				hdr.Size = e.hdr.Size
			}
			require.NoError(t, tw.WriteHeader(&hdr))
			if len(e.body) > 0 {
				_, err := tw.Write(e.body)
				require.NoError(t, err)
			}
		}
		require.NoError(t, tw.Close())
		require.NoError(t, gz.Close())
		archive := filepath.Join(t.TempDir(), "release.tar.gz")
		require.NoError(t, os.WriteFile(archive, buf.Bytes(), fileMode))
		return archive
	}

	t.Run("allows root directory, subdirectories, TypeReg, and legacy TypeRegA", func(t *testing.T) {
		t.Parallel()
		archive := writeTarGz(t, []struct {
			hdr  tar.Header
			body []byte
		}{
			{hdr: tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755}},
			{hdr: tar.Header{Name: "sub/", Typeflag: tar.TypeDir, Mode: 0o755}},
			{hdr: tar.Header{Name: "sub/README.md", Typeflag: tar.TypeReg, Mode: 0o644}, body: []byte("readme")},
			{hdr: tar.Header{Name: "sub/legacy.txt", Typeflag: tar.TypeRegA, Mode: 0o644}, body: []byte("legacy")},
		})
		require.NoError(t, preflight(archive))
		dest := t.TempDir()
		require.NoError(t, releasecheck.ExtractArchiveSafely(archive, dest))
	})

	t.Run("rejects directory with non-zero size", func(t *testing.T) {
		t.Parallel()
		var rawTar bytes.Buffer
		tw := tar.NewWriter(&rawTar)
		body := []byte("bad")
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "sub", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}))
		_, err := tw.Write(body)
		require.NoError(t, err)
		require.NoError(t, tw.Close())

		rawBytes := rawTar.Bytes()
		const typeflagOffset = 156
		const chksumOffset = 148
		const chksumLen = 8
		const headerLen = 512
		rawBytes[typeflagOffset] = tar.TypeDir
		for i := range chksumLen {
			rawBytes[chksumOffset+i] = ' '
		}
		var sum int
		for _, b := range rawBytes[:headerLen] {
			sum += int(b)
		}
		copy(rawBytes[chksumOffset:chksumOffset+chksumLen], fmt.Sprintf("%06o\x00 ", sum))

		var compressed bytes.Buffer
		gz := gzip.NewWriter(&compressed)
		_, err = gz.Write(rawBytes)
		require.NoError(t, err)
		require.NoError(t, gz.Close())

		archive := filepath.Join(t.TempDir(), "non-zero-dir.tar.gz")
		require.NoError(t, os.WriteFile(archive, compressed.Bytes(), fileMode))
		require.ErrorContains(t, preflight(archive), "unsafe or duplicate release archive entry")
	})

	t.Run("rejects duplicate directory entry", func(t *testing.T) {
		t.Parallel()
		archive := writeTarGz(t, []struct {
			hdr  tar.Header
			body []byte
		}{
			{hdr: tar.Header{Name: "sub/", Typeflag: tar.TypeDir, Mode: 0o755}},
			{hdr: tar.Header{Name: "./sub", Typeflag: tar.TypeDir, Mode: 0o755}},
		})
		require.ErrorContains(t, preflight(archive), "unsafe or duplicate release archive entry")
	})

	t.Run("rejects symlink and hardlink entries", func(t *testing.T) {
		t.Parallel()
		for _, tf := range []byte{tar.TypeSymlink, tar.TypeLink} {
			archive := writeTarGz(t, []struct {
				hdr  tar.Header
				body []byte
			}{
				{hdr: tar.Header{Name: "bad", Typeflag: tf, Linkname: "target", Mode: 0o777}},
			})
			require.ErrorContains(t, preflight(archive), "unsafe or duplicate release archive entry")
		}
	})
}
