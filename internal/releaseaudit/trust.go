package releaseaudit

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
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/EpicBlackWolfZ/microfat/internal/sbom"
)

const Repository = "EpicBlackWolfZ/microfat"
const cliName = "microfat"
const fullStub = "microfat-stub"
const minimalStub = "microfat-stub-minimal"
const executeBits = 0o111
const maxArchiveBytes = 500 * 1024 * 1024
const maxFileBytes = 250 * 1024 * 1024

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
var sourcePattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Options struct{ Dist, Output, Version, Source, Arch string }

func (o Options) Validate(goos, goarch string) error {
	if !versionPattern.MatchString(o.Version) || !sourcePattern.MatchString(o.Source) {
		return errors.New("invalid release version or source SHA")
	}
	if goos != "linux" || !slices.Contains([]string{"amd64", "arm64"}, o.Arch) || goarch != o.Arch {
		return errors.New("audit requires a matching native Linux runner")
	}
	return nil
}

func Authenticate(options Options, runner Runner) (map[string]string, error) {
	identity := "https://github.com/" + Repository + "/.github/workflows/release.yml@refs/tags/v" + options.Version
	_, err := runner.Run([]string{"cosign", "verify-blob", "--certificate-identity", identity, "--certificate-oidc-issuer",
		"https://token.actions.githubusercontent.com", "--certificate-github-workflow-sha", options.Source,
		"--bundle", filepath.Join(options.Dist, "checksums.txt.sig"), filepath.Join(options.Dist, "checksums.txt")}, nil, true)
	if err != nil {
		return nil, err
	}
	contract, err := releasecheck.NewReleaseContract(options.Version)
	if err != nil {
		return nil, err
	}
	if err := lowercaseChecksums(filepath.Join(options.Dist, "checksums.txt")); err != nil {
		return nil, err
	}
	verified, err := releasecheck.ValidateChecksums(options.Dist, contract)
	if err != nil {
		return nil, err
	}
	for name := range verified {
		if strings.HasSuffix(name, ".json") {
			if err := releaseSchema(filepath.Join(options.Dist, name), options.Version); err != nil {
				return nil, err
			}
		}
	}
	return verified, nil
}

func lowercaseChecksums(filename string) error {
	data, err := os.ReadFile(filename) // #nosec G304 -- requested release's signed checksum file.
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		const fieldsPerChecksum = 2
		if len(fields) != fieldsPerChecksum || !checksumPattern.MatchString(fields[0]) {
			return errors.New("unexpected checksum inventory or digest syntax")
		}
	}
	return nil
}

func releaseSchema(filename, version string) error {
	data, err := os.ReadFile(filename) // #nosec G304 -- checksum-verified SBOM input.
	if err != nil {
		return err
	}
	if releasecheck.UsesModernSBOM(version) {
		return sbom.ValidateSchema(data, strings.HasSuffix(filename, ".spdx.json"))
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return err
	}
	if strings.HasSuffix(filename, ".cyclonedx.json") {
		if document["bomFormat"] != "CycloneDX" || document["specVersion"] != "1.5" {
			return errors.New("wrong historical CycloneDX schema")
		}
	} else if document["spdxVersion"] != "SPDX-2.3" {
		return errors.New("wrong historical SPDX schema")
	}
	return nil
}

func Digest(filename string) (string, error) {
	file, err := os.Open(filename) // #nosec G304 -- explicitly selected audit artifact.
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func Extract(options Options) (string, error) {
	destination := filepath.Join(options.Output, "products")
	if err := os.Mkdir(destination, directoryMode); err != nil {
		return "", err
	}
	archive := filepath.Join(options.Dist, fmt.Sprintf("microfat_%s_linux_%s.tar.gz", options.Version, options.Arch))
	if err := preflight(archive); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(options.Output, ".products-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := releasecheck.ExtractArchiveSafely(archive, staging); err != nil {
		return "", err
	}
	if err := requiredProducts(staging); err != nil {
		return "", err
	}
	if err := os.Remove(destination); err != nil {
		return "", err
	}
	if err := os.Rename(staging, destination); err != nil {
		return "", err
	}
	return destination, nil
}

func requiredProducts(directory string) error {
	for _, name := range []string{cliName, fullStub, minimalStub, "README.md", "LICENSE", "SECURITY.md"} {
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("not a regular archive entry: %s", name)
		}
		if slices.Contains([]string{cliName, fullStub, minimalStub}, name) && info.Mode()&executeBits == 0 {
			return fmt.Errorf("non-executable product: %s", name)
		}
	}
	return nil
}

func preflight(filename string) error {
	file, err := os.Open(filename) // #nosec G304 -- authenticated archive selected for this native audit.
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer func() { _ = compressed.Close() }()
	reader := tar.NewReader(compressed)
	seen := make(map[string]bool)
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := path.Clean(header.Name)
		if header.Typeflag != tar.TypeReg || path.IsAbs(header.Name) || slices.Contains(strings.Split(header.Name, "/"), "..") ||
			strings.ContainsAny(header.Name, "\\\x00") || name == "." || seen[name] {
			return errors.New("unsafe or duplicate release archive entry")
		}
		if header.Size < 0 || header.Size > maxFileBytes || header.Size > maxArchiveBytes-total {
			return errors.New("release archive exceeds extraction limits")
		}
		total += header.Size
		seen[name] = true
	}
	drained, err := io.Copy(io.Discard, io.LimitReader(compressed, maxArchiveBytes+1))
	if err != nil {
		return err
	}
	if drained > maxArchiveBytes {
		return errors.New("release archive trailer exceeds limit")
	}
	return nil
}
