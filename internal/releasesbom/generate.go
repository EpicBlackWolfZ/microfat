package releasesbom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/EpicBlackWolfZ/microfat/internal/sbom"
)

const (
	FormatCycloneDX   = "cyclonedx-json"
	FormatSPDX        = "spdx-json"
	converterTimeout  = 2 * time.Minute
	maxConverterBytes = 64 << 20
	privateFileMode   = 0o600
)

// Converter translates a flat CycloneDX document with the pinned cdxgen tool.
// Tests can inject command failures; production uses Convert with digest pins.
type Converter func(context.Context, []byte) ([]byte, error)

// Generate validates the complete two-format pipeline before returning either
// format. Thus both published documents name tools actually used by the pipeline.
func Generate(ctx context.Context, archive, format string, meta Metadata, convert Converter) ([]byte, error) {
	if format != FormatCycloneDX && format != FormatSPDX {
		return nil, fmt.Errorf("unsupported SBOM format %q", format)
	}
	identity, err := releasecheck.ParseReleaseArchiveName(archive)
	if err != nil {
		return nil, err
	}
	contract, err := releasecheck.NewReleaseContract(identity.Version)
	if err != nil {
		return nil, err
	}
	facts, err := releasecheck.ValidateArchive(archive, identity.Arch, contract)
	if err != nil {
		return nil, err
	}
	defer func() { _ = facts.Cleanup() }()
	inventory, err := releasecheck.ExtractArchiveInventory(facts)
	if err != nil {
		return nil, err
	}
	cdx, err := CycloneDX(facts, inventory, meta)
	if err != nil {
		return nil, err
	}
	if err := releasecheck.ValidateModernCycloneDXBytes(cdx, facts, inventory); err != nil {
		return nil, err
	}
	spdx, err := convertDocument(ctx, cdx, convert)
	if err != nil {
		return nil, err
	}
	if err := releasecheck.ValidateModernSPDXBytes(spdx, facts, inventory); err != nil {
		return nil, err
	}
	if format == FormatSPDX {
		return spdx, nil
	}
	return cdx, nil
}

func convertDocument(ctx context.Context, data []byte, convert Converter) ([]byte, error) {
	if convert == nil {
		return nil, fmt.Errorf("missing cdxgen converter")
	}
	catalog, err := sbom.ReadCycloneDX(data)
	if err != nil {
		return nil, err
	}
	flat, err := catalog.ConversionView()
	if err != nil {
		return nil, err
	}
	converted, err := convert(ctx, flat)
	if err != nil {
		return nil, fmt.Errorf("cdxgen conversion: %w", err)
	}
	return sbom.CompleteSPDX(catalog, converted)
}

// Convert runs the exact reviewed standalone cdx-convert v13.1.0 binary. It needs
// neither Python/BLINT nor a separately installed Node runtime.
func Convert(ctx context.Context, data []byte) ([]byte, error) {
	path, err := exec.LookPath("cdx-convert")
	if err != nil {
		return nil, fmt.Errorf("install pinned cdx-convert with scripts/install-cdxgen.sh: %w", err)
	}
	if err := verifyConverter(path, runtime.GOOS, runtime.GOARCH); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, converterTimeout)
	defer cancel()
	return runConverter(ctx, path, data, process.Run)
}

func verifyConverter(path, operatingSystem, arch string) error {
	if operatingSystem != "linux" {
		return fmt.Errorf("release SBOM conversion requires a pinned Linux tool")
	}
	pins := map[string]string{
		"amd64": "e87137134bf53346f6ea864a2c481c070177c0ab28c303a26d4e79065ae6d266",
		"arm64": "4832f37aa1b4aac12b0c3d6ad446a3b9e6324d51ae1921b90c670f494d47dfc9",
	}
	expected, ok := pins[arch]
	if !ok {
		return fmt.Errorf("unsupported converter architecture %s", arch)
	}
	// #nosec G304 -- path was resolved from the caller's tool PATH and must match a fixed digest.
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, maxConverterBytes+1))
	if err != nil {
		return err
	}
	if count > maxConverterBytes || hex.EncodeToString(hash.Sum(nil)) != expected {
		return fmt.Errorf("cdx-convert does not match the pinned v%s %s digest", converterVersion, arch)
	}
	return nil
}

type processRunner func(context.Context, process.Spec) ([]byte, []byte, error)

func runConverter(ctx context.Context, path string, data []byte, run processRunner) ([]byte, error) {
	if len(data) > sbom.MaxDocumentBytes {
		return nil, fmt.Errorf("conversion input exceeds SBOM size bound")
	}
	dir, err := os.MkdirTemp("", "microfat-cdx-convert-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	input, output := filepath.Join(dir, "input.json"), filepath.Join(dir, "output.jsonld")
	if err := os.WriteFile(input, data, privateFileMode); err != nil {
		return nil, err
	}
	_, stderr, err := run(ctx, process.Spec{Path: path, Args: []string{"--input", input, "--output", output, "--to", "spdx", "--validate"}})
	if err != nil {
		return nil, fmt.Errorf("cdx-convert process: %w: %s", err, stderr)
	}
	return readBoundedOutput(output)
}

func readBoundedOutput(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > sbom.MaxDocumentBytes {
		return nil, fmt.Errorf("invalid cdx-convert output type or size")
	}
	// #nosec G304 -- output is inside a private owned temporary directory.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, sbom.MaxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > sbom.MaxDocumentBytes {
		return nil, fmt.Errorf("cdx-convert output exceeded its bound while reading")
	}
	return data, nil
}
