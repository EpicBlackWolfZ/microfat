package installrelease

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Published resolves availability through HTTPS metadata, not artifact trust.
// A caller must still Acquire the frozen version before any installation.
func (c *Client) Published(ctx context.Context, requested, arch string) (string, error) {
	if !slices.Contains([]string{"amd64", "arm64"}, arch) || c.directory != "" {
		return "", errors.New("published release discovery requires a supported architecture and online client")
	}
	address := apiURL
	if requested != "" {
		version, err := ParseVersion(requested)
		if err != nil {
			return "", err
		}
		requested = version
		address = apiTagURL + version
	}
	var data strings.Builder
	if err := c.get(ctx, address, maxMetadataBytes, &data); err != nil {
		if isRateLimitError(err) {
			return c.publishedWebFallback(ctx, requested, arch, err)
		}
		return "", err
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      *bool  `json:"draft"`
		Prerelease *bool  `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(data.String()), &release); err != nil {
		return "", err
	}
	if release.Draft == nil || release.Prerelease == nil || *release.Draft || *release.Prerelease {
		return "", errors.New("release is not a published stable release")
	}
	version, err := ParseVersion(release.Tag)
	if err != nil {
		return "", err
	}
	if release.Tag != "v"+version || (requested != "" && requested != version) {
		return "", errors.New("release metadata does not match the selected tag")
	}
	counts := make(map[string]int)
	for _, asset := range release.Assets {
		counts[asset.Name]++
	}
	for _, name := range releaseRequiredAssets(version, arch) {
		if counts[name] != 1 {
			return "", fmt.Errorf("release requires exactly one asset named %s", name)
		}
	}
	return version, nil
}

func releaseRequiredAssets(version, arch string) []string {
	return []string{
		"microfat_" + version + "_linux_" + arch + ".tar.gz",
		"checksums.txt",
		"checksums.txt.sig",
	}
}

func (c *Client) publishedWebFallback(ctx context.Context, requested, arch string, apiErr error) (string, error) {
	version, err := c.resolveLatestWeb(ctx)
	if err != nil {
		return "", err
	}
	// Asset availability alone cannot establish the publication status of an exact tag.
	if requested != "" && requested != version {
		return "", fmt.Errorf("cannot verify %s as a published stable release (retry with GH_TOKEN): %w", requested, apiErr)
	}
	for _, name := range releaseRequiredAssets(version, arch) {
		if err := c.verifyAssetHead(ctx, version, name); err != nil {
			return "", err
		}
	}
	return version, nil
}
