package homebrew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"time"
)

const apiBase = "https://api.github.com/repos/" + Repository
const assetBase = "https://github.com/" + Repository + "/releases/download/"
const metadataLimit int64 = 1 << 20
const assetLimit int64 = 256 << 20
const downloadTimeout = 2 * time.Minute
const redirectLimit = 5
const privateMode = 0o700
const fileMode = 0o600

type client struct {
	http *http.Client
}

func newClient() client {
	return client{http: &http.Client{Timeout: downloadTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= redirectLimit || req.URL.Scheme != "https" || req.URL.User != nil ||
			!slices.Contains([]string{"github.com", "api.github.com", "release-assets.githubusercontent.com",
				"objects.githubusercontent.com"}, req.URL.Host) {
			return errors.New("release download redirect rejected")
		}
		if req.URL.Host != "api.github.com" {
			req.Header.Del("Authorization")
		}
		return nil
	}}}
}

func (c client) fetch(ctx context.Context, address string, limit int64, out io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	if req.URL.Host == "api.github.com" {
		req.Header.Set("Accept", "application/vnd.github+json")
		if token := os.Getenv("GH_TOKEN"); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	response, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("release download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return errors.New("release download exceeds limit")
	}
	n, err := io.Copy(out, io.LimitReader(response.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return errors.New("release download exceeds limit")
	}
	return nil
}

func (c client) json(ctx context.Context, address string, target any) error {
	var data limitedBuffer
	if err := c.fetch(ctx, address, metadataLimit, &data); err != nil {
		return err
	}
	return json.Unmarshal(data.data, target)
}

type limitedBuffer struct{ data []byte }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (c client) download(ctx context.Context, address, filename string, limit int64) error {
	// #nosec G304,G703 -- fixed contract asset name in newly created private staging; exclusive creation.
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return err
	}
	return errors.Join(c.fetch(ctx, address, limit, file), file.Close())
}
