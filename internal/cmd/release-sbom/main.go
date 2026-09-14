// Package main provides the payload-aware release SBOM generation tool for microfat archives.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	dirPerms     = 0o755
	tempDirPerms = 0o700
	filePerms    = 0o644
	execPerms    = 0o755

	maxTotalExtractBytes = 500 * 1024 * 1024 // 500 MB
	maxSingleFileBytes   = 250 * 1024 * 1024 // 250 MB
	keyValueParts        = 2
)

func parseArgs(args []string) (archivePath, outputPath, formatName string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--output" || arg == "-output":
			if i+1 < len(args) {
				outputPath = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--output="):
			outputPath = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-output="):
			outputPath = strings.TrimPrefix(arg, "-output=")
		case arg == "--format" || arg == "-format":
			if i+1 < len(args) {
				formatName = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--format="):
			formatName = strings.TrimPrefix(arg, "--format=")
		case strings.HasPrefix(arg, "-format="):
			formatName = strings.TrimPrefix(arg, "-format=")
		case arg == "--archive" || arg == "-archive":
			if i+1 < len(args) {
				archivePath = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--archive="):
			archivePath = strings.TrimPrefix(arg, "--archive=")
		case strings.HasPrefix(arg, "-archive="):
			archivePath = strings.TrimPrefix(arg, "-archive=")
		case !strings.HasPrefix(arg, "-") && archivePath == "":
			archivePath = arg
		}
	}

	if archivePath == "" {
		return "", "", "", errors.New("missing archive path (provide positional argument or --archive)")
	}

	if strings.Contains(outputPath, "=") {
		parts := strings.SplitN(outputPath, "=", keyValueParts)
		formatName = parts[0]
		outputPath = parts[1]
	}

	if outputPath == "" {
		return "", "", "", errors.New("missing output path (provide --output)")
	}

	if formatName == "" {
		switch {
		case strings.HasSuffix(outputPath, ".spdx.json"):
			formatName = "spdx-json"
		case strings.HasSuffix(outputPath, ".cyclonedx.json"):
			formatName = "cyclonedx-json"
		default:
			return "", "", "", errors.New("unable to determine format: specify --format or use format=path in --output")
		}
	}

	return archivePath, outputPath, formatName, nil
}

func validateArchiveEntryPath(name, targetDir string) (string, error) {
	cleanName := filepath.Clean(name)
	if strings.HasPrefix(cleanName, "/") || strings.HasPrefix(cleanName, "\\") || strings.Contains(cleanName, "..") {
		return "", fmt.Errorf("illegal relative or absolute path in archive: %s", name)
	}

	cleanTargetDir := filepath.Clean(targetDir) + string(filepath.Separator)
	destPath := filepath.Join(targetDir, cleanName)
	if !strings.HasPrefix(destPath, cleanTargetDir) {
		return "", fmt.Errorf("path escapes target directory: %s", destPath)
	}
	return destPath, nil
}

func extractArchiveFileEntry(tr io.Reader, hdr *tar.Header, destPath string, totalExtracted *int64) error {
	if err := os.MkdirAll(filepath.Dir(destPath), dirPerms); err != nil {
		return fmt.Errorf("creating parent directory for %s: %w", destPath, err)
	}
	if hdr.Size > maxSingleFileBytes {
		return fmt.Errorf("file %s exceeds maximum size limit of %d bytes", hdr.Name, maxSingleFileBytes)
	}
	*totalExtracted += hdr.Size
	if *totalExtracted > maxTotalExtractBytes {
		return fmt.Errorf("archive exceeds total uncompressed size limit of %d bytes", maxTotalExtractBytes)
	}

	perms := os.FileMode(filePerms)
	if hdr.Mode&0o111 != 0 {
		perms = execPerms
	}
	// #nosec G304 -- destPath validated against target directory
	outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perms)
	if err != nil {
		return fmt.Errorf("creating output file %s: %w", destPath, err)
	}
	written, err := io.Copy(outFile, io.LimitReader(tr, maxSingleFileBytes))
	closeErr := outFile.Close()
	if err != nil {
		return fmt.Errorf("extracting file %s: %w", destPath, err)
	}
	if closeErr != nil {
		return fmt.Errorf("closing file %s: %w", destPath, closeErr)
	}
	if written != hdr.Size {
		return fmt.Errorf("size mismatch for %s: header %d, written %d", hdr.Name, hdr.Size, written)
	}
	return nil
}

func extractArchiveSafely(archivePath, targetDir string) error {
	// #nosec G304,G703 -- archive path provided by user or GoReleaser
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening archive %s: %w", archivePath, err)
	}
	defer func() { _ = f.Close() }()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("creating gzip reader: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	var totalExtracted int64

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar header: %w", err)
		}

		destPath, err := validateArchiveEntryPath(hdr.Name, targetDir)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, dirPerms); err != nil {
				return fmt.Errorf("creating directory %s: %w", destPath, err)
			}
		case tar.TypeReg:
			if err := extractArchiveFileEntry(tr, hdr, destPath, &totalExtracted); err != nil {
				return err
			}
		default:
			return fmt.Errorf("refusing to extract unsafe archive entry %s of type %v", hdr.Name, hdr.Typeflag)
		}
	}
	return nil
}

func readSharedDictionary(f *os.File, idx *format.Index) ([]byte, error) {
	if idx.DictionarySize <= 0 {
		return nil, nil
	}
	if idx.DictionarySize > format.MaxDictionarySize || idx.DictionaryOffset < 0 {
		return nil, fmt.Errorf("%w: dictionary size %d or offset %d out of bounds",
			format.ErrInvalidDictionary, idx.DictionarySize, idx.DictionaryOffset)
	}
	if idx.DictionarySHA256 == "" || !format.ValidateChecksum(idx.DictionarySHA256) {
		return nil, fmt.Errorf("%w: dictionary missing or invalid sha256 checksum", format.ErrInvalidChecksum)
	}
	dictBytes := make([]byte, idx.DictionarySize)
	if _, err := f.ReadAt(dictBytes, idx.DictionaryOffset); err != nil {
		return nil, fmt.Errorf("reading shared dictionary: %w", err)
	}
	dictHash := sha256.Sum256(dictBytes)
	if hex.EncodeToString(dictHash[:]) != idx.DictionarySHA256 {
		return nil, fmt.Errorf("%w: dictionary hash mismatch", format.ErrInvalidChecksum)
	}
	return dictBytes, nil
}

func extractSingleVariant(f *os.File, v format.VariantEntry, dictBytes []byte, stagingDir string) error {
	if v.SHA256 == "" || !format.ValidateChecksum(v.SHA256) {
		return fmt.Errorf("%w: invalid checksum for variant %s", format.ErrInvalidChecksum, v.Level)
	}
	if v.UncompressedSize <= 0 || v.UncompressedSize > format.MaxPayloadSize {
		return fmt.Errorf("%w: invalid uncompressed size %d for variant %s",
			format.ErrPayloadTooLarge, v.UncompressedSize, v.Level)
	}
	if v.Offset < 0 || v.CompressedSize <= 0 {
		return fmt.Errorf("invalid offset %d or compressed size %d for variant %s",
			v.Offset, v.CompressedSize, v.Level)
	}

	c, err := codec.Get(v.Compression)
	if err != nil {
		return fmt.Errorf("getting codec %s for variant %s: %w", v.Compression, v.Level, err)
	}

	variantPath := filepath.Join(stagingDir, fmt.Sprintf("microfat-variant-%s", v.Level))
	// #nosec G304 -- variantPath within temporary staging dir
	outFile, err := os.OpenFile(variantPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, execPerms)
	if err != nil {
		return fmt.Errorf("creating file for variant %s: %w", v.Level, err)
	}

	hasher := sha256.New()
	mw := io.MultiWriter(outFile, hasher)
	secReader := io.NewSectionReader(f, v.Offset, v.CompressedSize)

	if err := codec.DecompressWithOptionalDict(c, mw, secReader, v.UncompressedSize, dictBytes); err != nil {
		_ = outFile.Close()
		return fmt.Errorf("decompressing variant %s: %w", v.Level, err)
	}

	if err := outFile.Close(); err != nil {
		return fmt.Errorf("closing extracted variant %s: %w", v.Level, err)
	}

	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != v.SHA256 {
		return fmt.Errorf("checksum mismatch for variant %s: expected %s, got %s", v.Level, v.SHA256, actualHash)
	}
	return nil
}

func extractVariantsFromFatBinary(fatBinaryPath, stagingDir string) error {
	// #nosec G304 -- fatBinaryPath within temporary staging dir
	f, err := os.Open(fatBinaryPath)
	if err != nil {
		return fmt.Errorf("opening fat binary %s: %w", fatBinaryPath, err)
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat fat binary: %w", err)
	}

	idx, err := format.ReadTrailerAndIndex(f, fi.Size())
	if err != nil {
		return fmt.Errorf("reading fat trailer and index: %w", err)
	}

	if len(idx.Variants) == 0 {
		return fmt.Errorf("fat binary index in %s contains no variants", fatBinaryPath)
	}

	dictBytes, err := readSharedDictionary(f, idx)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(stagingDir, dirPerms); err != nil {
		return fmt.Errorf("creating staging dir %s: %w", stagingDir, err)
	}

	for _, v := range idx.Variants {
		if err := extractSingleVariant(f, v, dictBytes, stagingDir); err != nil {
			return err
		}
	}

	return nil
}

func runSyft(scanDir, formatName string) ([]byte, error) {
	var syftFormat string
	switch formatName {
	case "spdx", "spdx-json":
		syftFormat = "spdx-json"
	case "cyclonedx", "cyclonedx-json":
		syftFormat = "cyclonedx-json"
	default:
		return nil, fmt.Errorf("unsupported SBOM format %q (expected spdx-json or cyclonedx-json)", formatName)
	}

	syftPath, err := exec.LookPath("syft")
	if err != nil {
		return nil, fmt.Errorf("syft executable not found in PATH: %w", err)
	}

	// #nosec G204 -- syftPath resolved via LookPath, formatName validated against allowlist
	cmd := exec.Command(syftPath, scanDir, "-o", syftFormat)
	cmd.Env = os.Environ()

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("syft failed with exit code %d: %s", exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("running syft: %w", err)
	}
	return out, nil
}

func attributeSBOM(rawJSON []byte, formatName, archivePath, scanDir string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(rawJSON, &doc); err != nil {
		return nil, fmt.Errorf("parsing syft JSON output: %w", err)
	}

	archiveName := filepath.Base(archivePath)
	cleanScanDir := filepath.Clean(scanDir)

	switch formatName {
	case "spdx", "spdx-json":
		doc["name"] = archiveName
		if ns, ok := doc["documentNamespace"].(string); ok {
			doc["documentNamespace"] = strings.ReplaceAll(ns, cleanScanDir, "/"+archiveName)
		}
	case "cyclonedx", "cyclonedx-json":
		if meta, ok := doc["metadata"].(map[string]any); ok {
			if comp, ok := meta["component"].(map[string]any); ok {
				comp["name"] = archiveName
				comp["type"] = "file"
			}
		}
	}

	sanitizedJSON, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("re-marshaling sanitized SBOM JSON: %w", err)
	}

	sanitizedStr := string(sanitizedJSON)
	sanitizedStr = strings.ReplaceAll(sanitizedStr, cleanScanDir+"/", "")
	sanitizedStr = strings.ReplaceAll(sanitizedStr, cleanScanDir, archiveName)

	return []byte(sanitizedStr + "\n"), nil
}

func writeAtomic(targetPath string, data []byte) error {
	dir := filepath.Dir(targetPath)
	// #nosec G703 -- directory created from output path
	if err := os.MkdirAll(dir, dirPerms); err != nil {
		return fmt.Errorf("creating directory for %s: %w", targetPath, err)
	}

	tmpFile, err := os.CreateTemp(dir, ".sbom-tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		// #nosec G703 -- cleaning up temporary file
		_ = os.Remove(tmpName)
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing to temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("syncing temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	// #nosec G703 -- setting permissions on newly created temporary file
	if err := os.Chmod(tmpName, filePerms); err != nil {
		return fmt.Errorf("chmodding temp file: %w", err)
	}

	// #nosec G703 -- renaming temporary file to target path
	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpName, targetPath, err)
	}

	return nil
}

// Generate processes an archive tarball, extracts embedded variants, invokes syft, and writes an attributed SBOM.
func Generate(archivePath, outputPath, formatName string) error {
	tempDir, err := os.MkdirTemp("", "microfat-release-sbom-*")
	if err != nil {
		return fmt.Errorf("creating temporary directory: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	if err := os.Chmod(tempDir, tempDirPerms); err != nil {
		return fmt.Errorf("chmodding temporary directory: %w", err)
	}

	if err := extractArchiveSafely(archivePath, tempDir); err != nil {
		return fmt.Errorf("extracting archive %s: %w", archivePath, err)
	}

	microfatPath := filepath.Join(tempDir, "microfat")
	stubPath := filepath.Join(tempDir, "microfat-stub")
	minStubPath := filepath.Join(tempDir, "microfat-stub-minimal")

	for _, p := range []string{microfatPath, stubPath, minStubPath} {
		fi, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("verifying required archive executable %s: %w", p, err)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("expected regular file for %s, got mode %v", p, fi.Mode())
		}
	}

	stagingDir := filepath.Join(tempDir, "embedded_variants")
	if err := extractVariantsFromFatBinary(microfatPath, stagingDir); err != nil {
		return fmt.Errorf("extracting fat binary variants from %s: %w", microfatPath, err)
	}

	rawSBOM, err := runSyft(tempDir, formatName)
	if err != nil {
		return fmt.Errorf("generating SBOM with syft: %w", err)
	}

	attributed, err := attributeSBOM(rawSBOM, formatName, archivePath, tempDir)
	if err != nil {
		return fmt.Errorf("attributing SBOM: %w", err)
	}

	if err := writeAtomic(outputPath, attributed); err != nil {
		return fmt.Errorf("writing SBOM output to %s: %w", outputPath, err)
	}

	return nil
}

func runMain(args []string) int {
	archivePath, outputPath, formatName, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if err := Generate(archivePath, outputPath, formatName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

var exitFunc = os.Exit

func main() {
	exitFunc(runMain(os.Args[1:]))
}
