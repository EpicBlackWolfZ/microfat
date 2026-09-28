package installrelease

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/install"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureSymlink = "symlink"
const fixtureHardlink = "hardlink"

func TestArchiveSafety(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"valid-amd64", "valid-arm64", "duplicate", "traversal", "absolute", "backslash",
		fixtureSymlink, fixtureHardlink,
		"directory", "fifo", "nonexecutable", "missing-product", "wrong-arch", "bad-elf", "bad-index", "bad-gzip", "fat-stub"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			arch := "amd64"
			if scenario == "valid-arm64" {
				arch = fixtureARM64
			}
			archive := archiveFixture(t, arch, func(header *tar.Header, data []byte) (*tar.Header, []byte) {
				if header.Name != "microfat-stub" {
					return header, data
				}
				switch scenario {
				case "duplicate":
					header.Name = "microfat"
				case "traversal":
					header.Name = "../escaped"
				case "absolute":
					header.Name = "/escaped"
				case "backslash":
					header.Name = "dir\\escaped"
				case fixtureSymlink, fixtureHardlink, "directory", "fifo":
					header.Typeflag = map[string]byte{fixtureSymlink: tar.TypeSymlink, fixtureHardlink: tar.TypeLink,
						"directory": tar.TypeDir, "fifo": tar.TypeFifo}[scenario]
					header.Linkname = "../outside"
					header.Size = 0
					data = nil
				case "nonexecutable":
					header.Mode = 0o644
				case "missing-product":
					return nil, nil
				case "wrong-arch":
					data = elfFixture(fixtureARM64)
				case "bad-elf":
					data[0] = 0
				case "fat-stub":
					data = append(data, []byte(format.MagicString)...)
					header.Size = int64(len(data))
				}
				return header, data
			})
			if scenario == "bad-index" {
				archive = archiveFixture(t, arch, func(h *tar.Header, data []byte) (*tar.Header, []byte) {
					if h.Name == "microfat" {
						data[len(data)-1] ^= 0xff
					}
					return h, data
				})
			}
			if scenario == "bad-gzip" {
				archive = []byte("not gzip")
			}
			path := filepath.Join(t.TempDir(), "release.tar.gz")
			require.NoError(t, os.WriteFile(path, archive, fileMode))
			files, err := extract(path, t.TempDir(), arch)
			if scenario != "valid-amd64" && scenario != "valid-arm64" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, files, 3)
		})
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("failed read") }

func TestArchiveBoundsAndPadding(t *testing.T) {
	t.Parallel()
	for _, header := range []*tar.Header{{Name: "file", Typeflag: tar.TypeReg, Size: install.MaxFileBytes + 1},
		{Name: "file", Typeflag: tar.TypeReg, Size: -1}, {Name: ".", Typeflag: tar.TypeReg}} {
		_, err := validateEntry(header, nil)
		require.Error(t, err)
	}
	seen := make(map[string]bool)
	for i := 0; i < maxArchiveEntries; i++ {
		seen[string(rune(i))] = true
	}
	_, err := validateEntry(&tar.Header{Name: "file", Typeflag: tar.TypeReg}, seen)
	require.Error(t, err)
	for _, tc := range []struct {
		name   string
		reader io.Reader
		limit  int64
		valid  bool
	}{
		{"zeros", bytes.NewReader(make([]byte, 20)), 21, true},
		{"limit", bytes.NewReader(make([]byte, 20)), 20, false},
		{"trailing-data", bytes.NewReader([]byte{1}), 20, false},
		{"read-error", failingReader{}, 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := drainPadding(&io.LimitedReader{R: tc.reader, N: tc.limit})
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	_, err = Staging("relative")
	require.Error(t, err)
	dir, err := Staging(t.TempDir())
	require.NoError(t, err)
	assert.DirExists(t, dir)
	_, err = extract(filepath.Join(t.TempDir(), "missing"), t.TempDir(), "amd64")
	require.Error(t, err)
}
