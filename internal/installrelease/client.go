// Package installrelease authenticates and safely stages official Linux releases.
package installrelease

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
	"github.com/EpicBlackWolfZ/microfat/internal/install"
	"github.com/EpicBlackWolfZ/microfat/internal/releasechecksums"
)

const (
	Repository             = "EpicBlackWolfZ/microfat"
	Issuer                 = "https://token.actions.githubusercontent.com"
	apiURL                 = "https://api.github.com/repos/" + Repository + "/releases/latest"
	downloadURL            = "https://github.com/" + Repository + "/releases/download/v"
	maxDownloadBytes int64 = 256 * 1024 * 1024
	maxMetadataBytes int64 = 1024 * 1024
	requestTimeout         = 2 * time.Minute
	maxRedirects           = 5
	maxVersionLength       = 64
	privateMode            = 0o700
	fileMode               = 0o600
)

var versionSyntax = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ParseVersion deliberately supports stable releases with an established signing contract.
func ParseVersion(value string) (string, error) {
	version := strings.TrimPrefix(value, "v")
	if len(version) > maxVersionLength || !versionSyntax.MatchString(version) {
		return "", errors.New("expected a stable version such as 0.3.0")
	}
	if CompareVersions(version, "0.2.3") < 0 {
		return "", errors.New("release signing policy is only established for v0.2.3 and later")
	}
	return version, nil
}

func CompareVersions(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := range left {
		if i >= len(right) {
			return 1
		}
		if len(left[i]) != len(right[i]) {
			return len(left[i]) - len(right[i])
		}
		if result := strings.Compare(left[i], right[i]); result != 0 {
			return result
		}
	}
	return len(left) - len(right)
}

func Identity(version string) string {
	return "https://github.com/" + Repository + "/.github/workflows/release.yml@refs/tags/v" + version
}

type Client struct {
	http      *http.Client
	directory string
}

func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: requestTimeout, CheckRedirect: checkRedirect}}
}

// NewLocalClient consumes operator-downloaded assets, including an unpublished
// signed draft. The same exact tag signature and checksum policy remains mandatory.
func NewLocalClient(directory string) (*Client, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, errors.New("release directory must be a clean absolute path")
	}
	return &Client{directory: directory}, nil
}

func allowedURL(location *url.URL) bool {
	return location.Scheme == "https" && location.User == nil && location.Port() == "" &&
		slices.Contains([]string{"api.github.com", "github.com", "release-assets.githubusercontent.com",
			"objects.githubusercontent.com"}, location.Host)
}

func checkRedirect(request *http.Request, previous []*http.Request) error {
	if len(previous) >= maxRedirects || !allowedURL(request.URL) {
		return errors.New("refusing release download redirect")
	}
	return nil
}

func (c *Client) get(ctx context.Context, address string, limit int64, output io.Writer) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	if !allowedURL(request.URL) {
		return errors.New("refusing non-official or non-HTTPS release URL")
	}
	request.Header.Set("User-Agent", "microfat-installer")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	return errors.Join(copyResponse(response, limit, output), response.Body.Close())
}

func copyResponse(response *http.Response, limit int64, output io.Writer) error {
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("release download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return errors.New("release download exceeds size limit")
	}
	n, err := io.Copy(output, io.LimitReader(response.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return errors.New("release download exceeds size limit")
	}
	return nil
}

func (c *Client) Resolve(ctx context.Context, version string) (string, error) {
	if version != "" {
		return ParseVersion(version)
	}
	if c.directory != "" {
		return "", errors.New("offline release assets require an explicit version")
	}
	var data strings.Builder
	if err := c.get(ctx, apiURL, maxMetadataBytes, &data); err != nil {
		return "", err
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      *bool  `json:"draft"`
		Prerelease *bool  `json:"prerelease"`
	}
	if err := json.Unmarshal([]byte(data.String()), &release); err != nil {
		return "", err
	}
	if release.Draft == nil || release.Prerelease == nil || *release.Draft || *release.Prerelease {
		return "", errors.New("latest release is not a published stable release")
	}
	return ParseVersion(release.Tag)
}

func (c *Client) download(ctx context.Context, address, destination string, limit int64) error {
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode) // #nosec G304 -- fresh private staging path.
	if err != nil {
		return err
	}
	if c.directory != "" {
		return errors.Join(copyLocal(ctx, filepath.Join(c.directory, filepath.Base(address)), limit, file), file.Close())
	}
	return errors.Join(c.get(ctx, address, limit, file), file.Close())
}

func copyLocal(ctx context.Context, path string, limit int64, destination io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return errors.New("invalid local release artifact type or size")
	}
	file, err := inputfile.Open(path)
	if err != nil {
		return err
	}
	count, copyErr := io.Copy(destination, io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if count > limit {
		return errors.New("local release artifact exceeds size limit")
	}
	return errors.Join(copyErr, closeErr, ctx.Err())
}

type Verifier interface {
	Verify(context.Context, string, string, string) error
}

// Acquire authenticates the selected archive before extraction. The caller owns
// the private staging directory and its cleanup; this method never changes an installation.
func (c *Client) Acquire(ctx context.Context, version, arch, staging string, verifier Verifier) (install.Generation, string, error) {
	var generation install.Generation
	version, err := ParseVersion(version)
	if err != nil {
		return generation, "", err
	}
	if !slices.Contains([]string{"amd64", "arm64"}, arch) {
		return generation, "", errors.New("unsupported release architecture")
	}
	if verifier == nil {
		return generation, "", errors.New("release verifier is required")
	}
	base := downloadURL + version + "/"
	for _, name := range []string{"checksums.txt", "checksums.txt.sig"} {
		if err := c.download(ctx, base+name, filepath.Join(staging, name), maxMetadataBytes); err != nil {
			return generation, "", err
		}
	}
	checksums, bundle := filepath.Join(staging, "checksums.txt"), filepath.Join(staging, "checksums.txt.sig")
	if err := verifier.Verify(ctx, version, checksums, bundle); err != nil {
		return generation, "", err
	}
	data, err := os.Open(checksums) // #nosec G304 -- private staging, verified checksum bytes.
	if err != nil {
		return generation, "", err
	}
	entries, parseErr := releasechecksums.Parse(data)
	if err := errors.Join(parseErr, data.Close()); err != nil {
		return generation, "", err
	}
	name := "microfat_" + version + "_linux_" + arch + ".tar.gz"
	digest, exists := entries[name]
	if !exists {
		return generation, "", errors.New("signed inventory has no selected archive")
	}
	archive := filepath.Join(staging, name)
	if err := c.download(ctx, base+name, archive, maxDownloadBytes); err != nil {
		return generation, "", err
	}
	if err := VerifyDigest(archive, digest, maxDownloadBytes); err != nil {
		return generation, "", err
	}
	products := filepath.Join(staging, "products")
	if err := os.Mkdir(products, privateMode); err != nil {
		return generation, "", err
	}
	files, err := extract(archive, products, arch)
	if err != nil {
		return generation, "", err
	}
	generation = install.Generation{Schema: install.SchemaVersion, ID: install.NewID(), Version: version, Arch: arch,
		ArchiveSHA256: digest, Files: files}
	return generation, products, generation.Validate()
}
