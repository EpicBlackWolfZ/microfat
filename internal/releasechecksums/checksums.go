// Package releasechecksums parses the signed GNU checksum inventory shared by
// installation and complete-release validation. Authentication is the caller's job.
package releasechecksums

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxBytes = 1024 * 1024
const maxEntries = 4096
const expectedMatches = 4

var checksumLine = regexp.MustCompile(`^([0-9a-fA-F]{64})[ \t]([ *])([^\r\n]+)$`)

func Parse(reader io.Reader) (map[string]string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, errors.New("checksum inventory exceeds size limit")
	}
	entries := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, hash, err := ParseLine(line, lineNumber)
		if err != nil {
			return nil, err
		}
		if _, exists := entries[name]; exists {
			return nil, fmt.Errorf("line %d: duplicate checksum entry for artifact: %q", lineNumber, name)
		}
		if len(entries) >= maxEntries {
			return nil, errors.New("checksum inventory exceeds entry limit")
		}
		entries[name] = hash
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning checksums.txt: %w", err)
	}
	return entries, nil
}

func ParseLine(line string, lineNumber int) (string, string, error) {
	matches := checksumLine.FindStringSubmatch(line)
	if len(matches) != expectedMatches {
		return "", "", fmt.Errorf("line %d: invalid checksum line format: %q", lineNumber, line)
	}
	hash := strings.ToLower(matches[1])
	name := matches[3]
	if strings.TrimSpace(name) != name {
		return "", "", fmt.Errorf("line %d: filename contains whitespace padding: %q", lineNumber, name)
	}
	if filepath.Clean(name) != name || strings.ContainsAny(name, "/\\\x00") || name == "." || name == ".." {
		return "", "", fmt.Errorf("line %d: illegal filename with path elements: %q", lineNumber, name)
	}
	return name, hash, nil
}
