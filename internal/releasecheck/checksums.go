package releasecheck

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
	"github.com/EpicBlackWolfZ/microfat/internal/releasechecksums"
)

// ValidateChecksums parses and verifies distDir/checksums.txt against the ReleaseContract.
func ValidateChecksums(distDir string, contract *ReleaseContract) (map[string]string, error) {
	checksumsPath := filepath.Join(distDir, "checksums.txt")
	parsedChecksums, err := parseChecksumsFile(checksumsPath)
	if err != nil {
		return nil, err
	}

	if err := verifyChecksumCompleteness(parsedChecksums, contract); err != nil {
		return nil, err
	}

	if err := verifyDiskArtifacts(distDir, parsedChecksums); err != nil {
		return nil, err
	}

	return parsedChecksums, nil
}

func parseChecksumsFile(checksumsPath string) (map[string]string, error) {
	f, _, err := inputfile.OpenRegularNoFollow(checksumsPath)
	if err != nil {
		if errors.Is(err, inputfile.ErrNotRegular) || errors.Is(err, inputfile.ErrSymlink) {
			return nil, fmt.Errorf("checksums.txt is not a regular file: %w", err)
		}
		return nil, fmt.Errorf("opening checksums.txt: %w", err)
	}
	defer func() { _ = f.Close() }()

	return releasechecksums.Parse(f)
}

func verifyChecksumCompleteness(parsed map[string]string, contract *ReleaseContract) error {
	var missing []string
	for expected := range contract.ExpectedPayloadNames {
		if _, ok := parsed[expected]; !ok {
			missing = append(missing, expected)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required product checksum entries in checksums.txt: %s", strings.Join(missing, ", "))
	}

	var unexpected []string
	for found := range parsed {
		if !contract.ExpectedPayloadNames[found] {
			unexpected = append(unexpected, found)
		}
	}
	if len(unexpected) > 0 {
		return fmt.Errorf("unexpected product entries in checksums.txt: %s", strings.Join(unexpected, ", "))
	}
	return nil
}

func verifyDiskArtifacts(distDir string, parsed map[string]string) error {
	for name, expectedDigest := range parsed {
		targetPath := filepath.Join(distDir, name)
		actualDigest, err := computeFileSHA256(targetPath)
		if err != nil {
			if errors.Is(err, inputfile.ErrSymlink) || errors.Is(err, inputfile.ErrNotRegular) {
				return fmt.Errorf("artifact %s is not a regular file (symlinks forbidden): %w", name, err)
			}
			return fmt.Errorf("computing hash for %s: %w", name, err)
		}
		if actualDigest != expectedDigest {
			return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", name, expectedDigest, actualDigest)
		}
	}
	return nil
}

func computeFileSHA256(path string) (string, error) {
	f, _, err := inputfile.OpenRegularNoFollow(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
