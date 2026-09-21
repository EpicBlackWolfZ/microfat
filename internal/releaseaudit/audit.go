package releaseaudit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func Run(ctx context.Context, options Options, progress io.Writer, environment []string, execute Execute) error {
	if err := options.Validate(runtime.GOOS, runtime.GOARCH); err != nil {
		return err
	}
	var err error
	options.Dist, err = filepath.Abs(options.Dist)
	if err != nil {
		return err
	}
	options.Output, err = filepath.Abs(options.Output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(options.Output), directoryMode); err != nil {
		return err
	}
	if err := os.Mkdir(options.Output, directoryMode); err != nil {
		return err
	}
	runner := Runner{Context: ctx, Output: options.Output, Environment: CleanEnvironment(environment), Execute: execute}
	verified, err := Authenticate(options, runner)
	if err != nil {
		return err
	}
	products, err := Extract(options)
	if err != nil {
		return err
	}
	version, err := runner.Run([]string{"go", "version"}, nil, true)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(version, "go version go1.27.1 ") {
		return errors.New("Go 1.27.1 required")
	}
	hashes := make(map[string]string)
	for _, name := range []string{cliName, fullStub, minimalStub} {
		hashes[name], err = Digest(filepath.Join(products, name))
		if err != nil {
			return err
		}
	}
	record := map[string]any{"version": options.Version, "source": options.Source, "arch": options.Arch,
		"assets": verified, "products": hashes}
	if err := writeJSON(filepath.Join(options.Output, "verification.json"), record); err != nil {
		return err
	}
	return matrix(options, runner, products, progress)
}

func ValidateTag(tag string) error {
	if !strings.HasPrefix(tag, "v") || !versionPattern.MatchString(strings.TrimPrefix(tag, "v")) {
		return errors.New("invalid release tag")
	}
	return nil
}

// Metadata rejects absent boolean fields as well as drafts and mutable releases.
func Metadata(tag string, data []byte, source string) (string, error) {
	if err := ValidateTag(tag); err != nil {
		return "", err
	}
	var release struct {
		Tag       string `json:"tagName"`
		Draft     *bool  `json:"isDraft"`
		Immutable *bool  `json:"isImmutable"`
		Published string `json:"publishedAt"`
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return "", err
	}
	if release.Tag != tag || release.Draft == nil || *release.Draft || release.Immutable == nil || !*release.Immutable {
		return "", errors.New("audit requires the requested published immutable release")
	}
	source = strings.TrimSpace(source)
	if release.Published == "" || !sourcePattern.MatchString(source) {
		return "", errors.New("missing publication or source identity")
	}
	return "source=" + source + "\nversion=" + strings.TrimPrefix(tag, "v") + "\n", nil
}
