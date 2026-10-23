//go:build linux

package e2e_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

type cacheHandoffCodec struct {
	spec       string
	dictionary bool
}

func cacheHandoffCodecs() []cacheHandoffCodec {
	codecs := []cacheHandoffCodec{{spec: "none"}, {spec: "lz4:fast"}}
	const maxLZ4Level = 9
	for level := 1; level <= maxLZ4Level; level++ {
		codecs = append(codecs, cacheHandoffCodec{spec: "lz4:" + strconv.Itoa(level)})
	}
	for _, level := range []string{"fastest", "default", "better", "best"} {
		for _, dictionary := range []bool{false, true} {
			codecs = append(codecs, cacheHandoffCodec{spec: "zstd:" + level, dictionary: dictionary})
		}
	}
	return codecs
}

// The runtime security suite supplies fault/policy scenarios. This matrix adds
// every distinct encoder level and real descriptor execution to the cold path.
func TestVerifiedCacheDescriptorCompatibility(t *testing.T) {
	t.Parallel()
	minimal := buildMinimalLauncherStub(t, t.TempDir())
	profiles := []cacheFirstProfile{{name: launcherFullProfile, path: stubPath}, {name: launcherMinimalProfile, path: minimal}}
	for _, version := range []int{1, 2} {
		for _, profile := range profiles {
			for _, compression := range cacheHandoffCodecs() {
				name := fmt.Sprintf("format%d/%s/%s/dict%t", version, profile.name, compression.spec, compression.dictionary)
				t.Run(name, func(t *testing.T) {
					output := filepath.Join(t.TempDir(), "app.fat")
					args := []string{inputPackCommand, mountArchFlag, currentHostArch, inputStubFlag, profile.path,
						"--format-version", strconv.Itoa(version), "--compression", compression.spec, "-o", output}
					for _, level := range creationEpochLevels() {
						args = append(args, "-v", level+"="+goldenVariantBins[level])
					}
					if compression.dictionary {
						args = append(args, "--dict")
					}
					creationEpochCommand(t, nil, args...)
					index := inspectCreationEpoch(t, output)
					if compression.dictionary {
						require.Positive(t, index.DictionarySize)
					} else {
						require.Zero(t, index.DictionarySize)
					}
					verifyFatIntegrity(t, output)
					requireCacheHandoffLaunches(t, output)
				})
			}
		}
	}
}

func requireCacheHandoffLaunches(t *testing.T, output string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cache")
	env := append(cacheFirstEnv(dir), envExecCache)
	// First launch materializes and executes its held descriptor; the next launch
	// must validate the existing cache inode without replacing it.
	for _, warm := range []bool{false, true} {
		before := cacheTreeSnapshot(t, filepath.Dir(dir))
		stdout, stderr, code, err := executeFatBinary(t, output, env)
		require.NoError(t, err, "warm=%t, exit=%d: %s", warm, code, stderr)
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
		require.Contains(t, stderr, "exec_mode="+execModeCache)
		if warm {
			require.Equal(t, before, cacheTreeSnapshot(t, filepath.Dir(dir)))
		}
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(data)), entries[0].Name())
	// An existing cache does not change explicit memfd behavior.
	before := cacheTreeSnapshot(t, filepath.Dir(dir))
	stdout, stderr, code, err := executeFatBinary(t, output, append(cacheFirstEnv(dir), "MICROFAT_EXEC_MODE="+execModeMemfd))
	require.NoError(t, err, "memfd exit=%d: %s", code, stderr)
	assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
	require.Contains(t, stderr, "exec_mode="+execModeMemfd)
	require.Equal(t, before, cacheTreeSnapshot(t, filepath.Dir(dir)))
	// A real kernel-denied memfd on a separate cold cache must execute through
	// the same handoff, including the minimal profile.
	fallbackDir := filepath.Join(t.TempDir(), "fallback")
	stdout, stderr, code, err = executeWithSeccompBlockedMemfd(t, output, cacheFirstEnv(fallbackDir))
	require.NoError(t, err, "fallback exit=%d: %s", code, stderr)
	assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
	require.Contains(t, stderr, "exec_mode="+execModeCache)
	entries, err = os.ReadDir(fallbackDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}
