package installrelease

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/EpicBlackWolfZ/microfat"
	"github.com/EpicBlackWolfZ/microfat/internal/install"
)

// CosignVersion returns the reviewed verifier release from the shared tool pins.
// Platform versions advance together, as checked by the shared pin contracts.
func CosignVersion() string {
	version, _, _ := strings.Cut(microfat.ToolPin("COSIGN_LINUX_AMD64"), "@")
	return version
}

func cosignPin(arch string) (string, error) {
	switch arch {
	case "amd64", "arm64":
		_, digest, _ := strings.Cut(microfat.ToolPin("COSIGN_LINUX_"+strings.ToUpper(arch)), "@")
		return digest, nil
	default:
		return "", errors.New("unsupported verifier architecture")
	}
}

// PrepareVerifier authenticates a fixed verifier before it may be executed.
// Overrides are explicit independent trust inputs, never PATH lookups.
func (c *Client) PrepareVerifier(ctx context.Context, arch, staging string, override Cosign) (Cosign, error) {
	if override.Path != "" || override.SHA256 != "" {
		if err := install.ValidateVerifierExecutable(override.Path); err != nil {
			return Cosign{}, err
		}
		return override, VerifyDigest(override.Path, override.SHA256, maxDownloadBytes)
	}
	pin, err := cosignPin(arch)
	if err != nil {
		return Cosign{}, err
	}
	path := filepath.Join(staging, "cosign")
	address := "https://github.com/sigstore/cosign/releases/download/" + CosignVersion() + "/cosign-linux-" + arch
	if err := c.download(ctx, address, path, maxDownloadBytes); err != nil {
		return Cosign{}, err
	}
	if err := VerifyDigest(path, pin, maxDownloadBytes); err != nil {
		return Cosign{}, err
	}
	if err := os.Chmod(path, privateMode); err != nil {
		return Cosign{}, err
	}
	return Cosign{Path: path, SHA256: pin}, nil
}
