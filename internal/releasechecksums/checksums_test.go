package releasechecksums

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestChecksumInventory(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, data string
		valid      bool
	}{
		{"text", hash + "  archive.tar.gz\n", true},
		{"binary", strings.ToUpper(hash) + " *archive.tar.gz\n", true},
		{"blank", "\n  \n" + hash + "  archive.tar.gz\n", true},
		{"duplicate", hash + "  archive.tar.gz\n" + hash + " *archive.tar.gz\n", false},
		{"bad hash", strings.Repeat("z", 64) + "  archive.tar.gz", false},
		{"short", "abc  archive.tar.gz", false},
		{"traversal", hash + "  ../archive.tar.gz", false},
		{"absolute", hash + "  /archive.tar.gz", false},
		{"backslash", hash + "  dir\\archive.tar.gz", false},
		{"null", hash + "  archive\x00.tar.gz", false},
		{"padding", hash + "   archive.tar.gz", false},
		{"dot", hash + "  .", false},
		{"dotdot", hash + "  ..", false},
		{"scanner bound", hash + "  " + strings.Repeat("x", 100000), false},
		{"byte bound", strings.Repeat(" ", MaxBytes+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entries, err := Parse(strings.NewReader(tc.data))
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, map[string]string{"archive.tar.gz": hash}, entries)
		})
	}
	t.Run("entry bound", func(t *testing.T) {
		t.Parallel()
		var data strings.Builder
		for i := 0; i <= maxEntries; i++ {
			fmt.Fprintf(&data, "%s  file-%d\n", hash, i)
		}
		_, err := Parse(strings.NewReader(data.String()))
		require.ErrorContains(t, err, "entry limit")
	})
	t.Run("read error", func(t *testing.T) {
		t.Parallel()
		_, err := Parse(brokenReader{})
		require.ErrorContains(t, err, "read failed")
	})
}
