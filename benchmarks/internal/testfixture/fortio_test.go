package testfixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks"
	"github.com/stretchr/testify/require"
)

func TestFortioOutputPreservesCapture(t *testing.T) {
	t.Parallel()
	const original = `{"Version": "historic", "ActualQPS": 984.8895563616024}`
	path := filepath.Join(t.TempDir(), "capture.json")
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))
	data, err := FortioOutput(path)
	require.NoError(t, err)
	version := strings.TrimPrefix(benchmarks.PinnedFortio().Version, "v")
	require.Equal(t, `{"Version": "`+version+`", "ActualQPS": 984.8895563616024}`, string(data))
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, string(unchanged))
	_, err = FortioOutput(filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, err)
}
