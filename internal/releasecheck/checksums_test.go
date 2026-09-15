package releasecheck_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createValidDistFixture(t *testing.T, version string) (string, *releasecheck.ReleaseContract, map[string]string) {
	t.Helper()
	distDir := t.TempDir()
	contract, err := releasecheck.NewReleaseContract(version)
	require.NoError(t, err)

	digests := make(map[string]string)
	for name := range contract.ExpectedPayloadNames {
		filePath := filepath.Join(distDir, name)
		content := []byte(fmt.Sprintf("content-of-%s", name))
		require.NoError(t, os.WriteFile(filePath, content, 0o644))

		h := sha256.Sum256(content)
		digests[name] = hex.EncodeToString(h[:])
	}

	var sb strings.Builder
	for name, hash := range digests {
		sb.WriteString(fmt.Sprintf("%s  %s\n", hash, name))
	}
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "checksums.txt"), []byte(sb.String()), 0o644))

	return distDir, contract, digests
}

func TestValidateChecksums_Valid(t *testing.T) {
	t.Parallel()
	distDir, contract, expectedDigests := createValidDistFixture(t, "0.2.3")

	digests, err := releasecheck.ValidateChecksums(distDir, contract)
	require.NoError(t, err)
	assert.Equal(t, expectedDigests, digests)
}

func TestValidateChecksums_BinaryMarkerSupported(t *testing.T) {
	t.Parallel()
	distDir, contract, expectedDigests := createValidDistFixture(t, "0.2.3")

	var sb strings.Builder
	for name, hash := range expectedDigests {
		// Use binary marker '*' instead of space
		sb.WriteString(fmt.Sprintf("%s *%s\n", hash, name))
	}
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "checksums.txt"), []byte(sb.String()), 0o644))

	digests, err := releasecheck.ValidateChecksums(distDir, contract)
	require.NoError(t, err)
	assert.Equal(t, expectedDigests, digests)
}

func TestValidateChecksums_MissingRequiredEntries(t *testing.T) {
	t.Parallel()
	for name := range (func() map[string]bool {
		c, _ := releasecheck.NewReleaseContract("0.2.3")
		return c.ExpectedPayloadNames
	})() {
		missingName := name
		t.Run("missing_"+missingName, func(t *testing.T) {
			t.Parallel()
			distDir, contract, digests := createValidDistFixture(t, "0.2.3")

			var sb strings.Builder
			for n, h := range digests {
				if n == missingName {
					continue
				}
				sb.WriteString(fmt.Sprintf("%s  %s\n", h, n))
			}
			require.NoError(t, os.WriteFile(filepath.Join(distDir, "checksums.txt"), []byte(sb.String()), 0o644))

			_, err := releasecheck.ValidateChecksums(distDir, contract)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing required product checksum entries")
			assert.Contains(t, err.Error(), missingName)
		})
	}
}

func TestValidateChecksums_DuplicateEntries(t *testing.T) {
	t.Parallel()
	distDir, contract, digests := createValidDistFixture(t, "0.2.3")

	// Append a duplicate entry
	var sb strings.Builder
	for n, h := range digests {
		sb.WriteString(fmt.Sprintf("%s  %s\n", h, n))
	}
	// duplicate first key with binary marker
	for n, h := range digests {
		sb.WriteString(fmt.Sprintf("%s *%s\n", h, n))
		break
	}
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "checksums.txt"), []byte(sb.String()), 0o644))

	_, err := releasecheck.ValidateChecksums(distDir, contract)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate checksum entry for artifact")
}

func TestValidateChecksums_SixCopiesOfOneEntry(t *testing.T) {
	t.Parallel()
	distDir, contract, digests := createValidDistFixture(t, "0.2.3")

	var singleName, singleHash string
	for n, h := range digests {
		singleName, singleHash = n, h
		break
	}

	var sb strings.Builder
	for range 6 {
		sb.WriteString(fmt.Sprintf("%s  %s\n", singleHash, singleName))
	}
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "checksums.txt"), []byte(sb.String()), 0o644))

	_, err := releasecheck.ValidateChecksums(distDir, contract)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate checksum entry")
}

func TestValidateChecksums_UnexpectedEntry(t *testing.T) {
	t.Parallel()
	distDir, contract, digests := createValidDistFixture(t, "0.2.3")

	// Add an extra valid file
	extraName := "unrelated.tar.gz"
	require.NoError(t, os.WriteFile(filepath.Join(distDir, extraName), []byte("extra"), 0o644))
	h := sha256.Sum256([]byte("extra"))
	extraHash := hex.EncodeToString(h[:])

	var sb strings.Builder
	for n, h := range digests {
		sb.WriteString(fmt.Sprintf("%s  %s\n", h, n))
	}
	sb.WriteString(fmt.Sprintf("%s  %s\n", extraHash, extraName))
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "checksums.txt"), []byte(sb.String()), 0o644))

	_, err := releasecheck.ValidateChecksums(distDir, contract)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected product entries in checksums.txt")
	assert.Contains(t, err.Error(), extraName)
}

func TestValidateChecksums_ReplacedByUnrelated(t *testing.T) {
	t.Parallel()
	distDir, contract, digests := createValidDistFixture(t, "0.2.3")

	extraName := "unrelated.tar.gz"
	require.NoError(t, os.WriteFile(filepath.Join(distDir, extraName), []byte("extra"), 0o644))
	h := sha256.Sum256([]byte("extra"))
	extraHash := hex.EncodeToString(h[:])

	var sb strings.Builder
	first := true
	for n, h := range digests {
		if first {
			first = false
			continue // omit one
		}
		sb.WriteString(fmt.Sprintf("%s  %s\n", h, n))
	}
	sb.WriteString(fmt.Sprintf("%s  %s\n", extraHash, extraName))
	require.NoError(t, os.WriteFile(filepath.Join(distDir, "checksums.txt"), []byte(sb.String()), 0o644))

	_, err := releasecheck.ValidateChecksums(distDir, contract)
	require.Error(t, err)
	// Should report both missing and unexpected
	assert.Contains(t, err.Error(), "missing required product checksum entries")
}

func TestValidateChecksums_IllegalPaths(t *testing.T) {
	t.Parallel()
	invalidPaths := []string{
		"../escape.tar.gz",
		"/root/file.tar.gz",
		"sub/dir/file.tar.gz",
		"./relative.tar.gz",
		"file\x00null.tar.gz",
		".",
		"..",
		"windows\\path.tar.gz",
	}

	for _, p := range invalidPaths {
		pathName := p
		t.Run("path_"+pathName, func(t *testing.T) {
			t.Parallel()
			distDir, contract, digests := createValidDistFixture(t, "0.2.3")

			var sb strings.Builder
			for n, h := range digests {
				sb.WriteString(fmt.Sprintf("%s  %s\n", h, n))
			}
			sb.WriteString(fmt.Sprintf("%s  %s\n", strings.Repeat("a", 64), pathName))
			require.NoError(t, os.WriteFile(filepath.Join(distDir, "checksums.txt"), []byte(sb.String()), 0o644))

			_, err := releasecheck.ValidateChecksums(distDir, contract)
			require.Error(t, err)
		})
	}
}

func TestValidateChecksums_MalformedLines(t *testing.T) {
	t.Parallel()
	malformed := []string{
		"not-a-valid-line",
		"1234  short-hash.tar.gz",
		strings.Repeat("g", 64) + "  invalid-hex.tar.gz",
		strings.Repeat("a", 64) + " \t extra-space.tar.gz",
		"\\abcdef  escaped.tar.gz",
	}

	for _, line := range malformed {
		l := line
		t.Run(l, func(t *testing.T) {
			t.Parallel()
			distDir, contract, _ := createValidDistFixture(t, "0.2.3")

			require.NoError(t, os.WriteFile(filepath.Join(distDir, "checksums.txt"), []byte(l+"\n"), 0o644))

			_, err := releasecheck.ValidateChecksums(distDir, contract)
			require.Error(t, err)
		})
	}
}

func TestValidateChecksums_SymlinkRejected(t *testing.T) {
	t.Parallel()
	distDir, contract, digests := createValidDistFixture(t, "0.2.3")

	// Replace one real file with a symlink
	var targetName string
	for n := range digests {
		targetName = n
		break
	}
	targetPath := filepath.Join(distDir, targetName)
	realPath := filepath.Join(distDir, "real-"+targetName)
	require.NoError(t, os.Rename(targetPath, realPath))
	require.NoError(t, os.Symlink(realPath, targetPath))

	_, err := releasecheck.ValidateChecksums(distDir, contract)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlinks forbidden")
}

func TestValidateChecksums_ContentMismatch(t *testing.T) {
	t.Parallel()
	distDir, contract, digests := createValidDistFixture(t, "0.2.3")

	// Corrupt one file
	var targetName string
	for n := range digests {
		targetName = n
		break
	}
	require.NoError(t, os.WriteFile(filepath.Join(distDir, targetName), []byte("corrupt-content"), 0o644))

	_, err := releasecheck.ValidateChecksums(distDir, contract)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
}

func TestValidateChecksums_NonRegularChecksumsFile(t *testing.T) {
	t.Parallel()
	distDir := t.TempDir()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	// Make checksums.txt a directory
	require.NoError(t, os.Mkdir(filepath.Join(distDir, "checksums.txt"), 0o755))

	_, err = releasecheck.ValidateChecksums(distDir, contract)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

func TestValidateChecksums_MissingFileOnDisk(t *testing.T) {
	t.Parallel()
	distDir, contract, digests := createValidDistFixture(t, "0.2.3")

	for n := range digests {
		require.NoError(t, os.Remove(filepath.Join(distDir, n)))
		break
	}

	_, err := releasecheck.ValidateChecksums(distDir, contract)
	require.Error(t, err)
}

func TestValidateChecksums_MissingChecksumsFile(t *testing.T) {
	t.Parallel()
	distDir := t.TempDir()
	contract, err := releasecheck.NewReleaseContract("0.2.3")
	require.NoError(t, err)

	_, err = releasecheck.ValidateChecksums(distDir, contract)
	require.Error(t, err)
}
