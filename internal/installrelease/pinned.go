package installrelease

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/internal/install"
)

// These independent pins match the reviewed bootstrap trust anchor, sourced
// from sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6.
const CosignVersion = "v3.0.6"

func cosignPin(arch string) (string, error) {
	switch arch {
	case "amd64":
		return "c956e5dfcac53d52bcf058360d579472f0c1d2d9b69f55209e256fe7783f4c74", nil
	case "arm64":
		return "bedac92e8c3729864e13d4a17048007cfafa79d5deca993a43a90ffe018ef2b8", nil
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
	address := "https://github.com/sigstore/cosign/releases/download/" + CosignVersion + "/cosign-linux-" + arch
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
