package releasecheck

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	expectedChecksumMatches = 4
)

var (
	// Strict GNU checksum grammar line: 64 hex characters, space + marker (' ' or '*'), followed by filename
	checksumLineRegex = regexp.MustCompile(`^([0-9a-fA-F]{64})[ \t]([ *])([^\r\n]+)$`)
)

// ValidateChecksums parses and verifies distDir/checksums.txt against the ReleaseContract.
func ValidateChecksums(distDir string, contract *ReleaseContract) (map[string]string, error) {
	checksumsPath := filepath.Join(distDir, "checksums.txt")
	fi, err := os.Lstat(checksumsPath)
	if err != nil {
		return nil, fmt.Errorf("opening checksums.txt: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("checksums.txt is not a regular file")
	}

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
	// #nosec G304 -- checksums.txt validated path
	f, err := os.Open(checksumsPath)
	if err != nil {
		return nil, fmt.Errorf("reading checksums.txt: %w", err)
	}
	defer func() { _ = f.Close() }()

	parsedChecksums := make(map[string]string)
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		name, hash, err := parseChecksumLine(line, lineNum)
		if err != nil {
			return nil, err
		}

		if _, exists := parsedChecksums[name]; exists {
			return nil, fmt.Errorf("line %d: duplicate checksum entry for artifact: %q", lineNum, name)
		}
		parsedChecksums[name] = hash
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning checksums.txt: %w", err)
	}
	return parsedChecksums, nil
}

func parseChecksumLine(line string, lineNum int) (string, string, error) {
	matches := checksumLineRegex.FindStringSubmatch(line)
	if len(matches) != expectedChecksumMatches {
		return "", "", fmt.Errorf("line %d: invalid checksum line format: %q", lineNum, line)
	}

	hash := strings.ToLower(matches[1])
	if _, err := hex.DecodeString(hash); err != nil {
		return "", "", fmt.Errorf("line %d: invalid hex hash: %s", lineNum, hash)
	}

	rawName := matches[3]
	if strings.TrimSpace(rawName) != rawName {
		return "", "", fmt.Errorf("line %d: filename contains whitespace padding: %q", lineNum, rawName)
	}

	cleanName := filepath.Clean(rawName)
	if cleanName != rawName || strings.HasPrefix(rawName, "/") || strings.HasPrefix(rawName, "\\") ||
		strings.Contains(rawName, "/") || strings.Contains(rawName, "\\") ||
		strings.Contains(rawName, "\x00") || rawName == "." || rawName == ".." ||
		filepath.Base(rawName) != rawName {
		return "", "", fmt.Errorf("line %d: illegal filename with path elements: %q", lineNum, rawName)
	}

	return cleanName, hash, nil
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
		tfi, err := os.Lstat(targetPath)
		if err != nil {
			return fmt.Errorf("stat artifact %s: %w", name, err)
		}
		if !tfi.Mode().IsRegular() {
			return fmt.Errorf("artifact %s is not a regular file (symlinks forbidden)", name)
		}

		actualDigest, err := computeFileSHA256(targetPath)
		if err != nil {
			return fmt.Errorf("computing hash for %s: %w", name, err)
		}
		if actualDigest != expectedDigest {
			return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", name, expectedDigest, actualDigest)
		}
	}
	return nil
}

func computeFileSHA256(path string) (string, error) {
	// #nosec G304 -- path within validated distribution directory
	f, err := os.Open(path)
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
