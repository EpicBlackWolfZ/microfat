package benchmarks

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPinnedFortio(t *testing.T) {
	t.Parallel()
	pin, err := ReadFortioPin(toolsLock)
	require.NoError(t, err)
	require.Equal(t, pin, PinnedFortio())
	for name, invalid := range map[string][]byte{
		"invalid JSON": []byte("{"), "missing": []byte("{}"), "null": []byte("null"),
		"module":  []byte(strings.ReplaceAll(string(toolsLock), "fortio.org/fortio", "example.org/tool")),
		"release": []byte(strings.ReplaceAll(string(toolsLock), pin.Version, pin.Version+"-rc1")),
		"commit":  []byte(strings.ReplaceAll(string(toolsLock), pin.SourceCommit, "unresolved")),
		"source":  []byte(strings.ReplaceAll(string(toolsLock), "https://github.com/", "https://example.org/")),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ReadFortioPin(invalid)
			require.Error(t, err)
			require.Panics(t, func() { mustFortioPin(invalid) })
		})
	}
}
