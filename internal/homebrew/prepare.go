package homebrew

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/installrelease"
	"github.com/EpicBlackWolfZ/microfat/internal/releaseaudit"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
)

type release struct {
	ID         int64  `json:"id"`
	Tag        string `json:"tag_name"`
	Draft      *bool  `json:"draft"`
	Prerelease *bool  `json:"prerelease"`
	Immutable  *bool  `json:"immutable"`
	Published  string `json:"published_at"`
	Assets     []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (r release) validate(tag string) error {
	if _, err := Version(tag); err != nil {
		return err
	}
	if r.ID <= 0 || r.Tag != tag || r.Draft == nil || *r.Draft || r.Prerelease == nil || *r.Prerelease ||
		r.Immutable == nil || !*r.Immutable {
		return errors.New("expected the exact public, stable, immutable release")
	}
	if _, err := time.Parse(time.RFC3339, r.Published); err != nil {
		return errors.New("missing or invalid publication time")
	}
	return nil
}

type Evidence struct {
	ReleaseID      int64                        `json:"release_id"`
	Tag            string                       `json:"tag"`
	Source         string                       `json:"source"`
	Checksums      string                       `json:"checksums_sha256"`
	Assets         map[string]string            `json:"assets"`
	Products       map[string]map[string]string `json:"products"`
	Recipe         string                       `json:"recipe_sha256"`
	GoVersion      string                       `json:"go_version"`
	CosignVersion  string                       `json:"cosign_version"`
	HomebrewCommit string                       `json:"homebrew_commit"`
}

// Discover returns an empty tag before the first eligible release. Invalid metadata is never an empty success.
func Discover(ctx context.Context) (string, error) { return newClient().discover(ctx) }

func (c client) discover(ctx context.Context) (string, error) {
	var r release
	if err := c.json(ctx, apiBase+"/releases/latest", &r); err != nil {
		return "", err
	}
	version, err := installrelease.ParseVersion(r.Tag)
	if err != nil {
		return "", err
	}
	if r.Tag != "v"+version || r.ID <= 0 || r.Draft == nil || *r.Draft || r.Prerelease == nil || *r.Prerelease {
		return "", errors.New("latest metadata is not a published stable release")
	}
	if _, err := time.Parse(time.RFC3339, r.Published); err != nil {
		return "", err
	}
	if installrelease.CompareVersions(version, firstVersion) < 0 {
		return "", nil
	}
	return r.Tag, r.validate(r.Tag)
}

// Prepare downloads and authenticates complete published assets before writing a cask.
// The output directory must not exist. Failed evidence is retained, but no candidate is emitted on failure.
func Prepare(ctx context.Context, tag, output string) (Evidence, error) {
	return newClient().prepare(ctx, tag, output, authenticate, validateProducts)
}

type authentication func(context.Context, string, string, string) (map[string]string, error)
type validation func(string, string) (map[string]map[string]string, error)

func (c client) prepare(ctx context.Context, tag, output string, auth authentication, validate validation) (Evidence, error) {
	var evidence Evidence
	version, err := Version(tag)
	if err != nil {
		return evidence, err
	}
	var r release
	if err := c.json(ctx, apiBase+"/releases/tags/"+tag, &r); err != nil {
		return evidence, err
	}
	if err := r.validate(tag); err != nil {
		return evidence, err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := c.json(ctx, apiBase+"/commits/"+tag, &commit); err != nil {
		return evidence, err
	}
	if !sourcePattern.MatchString(commit.SHA) {
		return evidence, errors.New("invalid release source commit")
	}
	names, err := requiredAssets(r, version)
	if err != nil {
		return evidence, err
	}
	if err := os.Mkdir(output, privateMode); err != nil {
		return evidence, err
	}
	dist := filepath.Join(output, "dist")
	if err := os.Mkdir(dist, privateMode); err != nil {
		return evidence, err
	}
	for _, name := range names {
		limit := assetLimit
		if name == "checksums.txt" || name == "checksums.txt.sig" {
			limit = metadataLimit
		}
		if err := c.download(ctx, assetBase+tag+"/"+name, filepath.Join(dist, name), limit); err != nil {
			return evidence, err
		}
	}
	hashes, err := auth(ctx, dist, version, commit.SHA)
	if err != nil {
		return evidence, err
	}
	products, err := validate(dist, version)
	if err != nil {
		return evidence, err
	}
	cask, err := Render(Recipe{Version: version, AMD64: hashes[archiveName(version, "amd64")], ARM64: hashes[archiveName(version, "arm64")]})
	if err != nil {
		return evidence, err
	}
	checksums, err := releaseaudit.Digest(filepath.Join(dist, "checksums.txt"))
	if err != nil {
		return evidence, err
	}
	evidence = Evidence{ReleaseID: r.ID, Tag: tag, Source: commit.SHA, Checksums: checksums, Assets: hashes,
		Products: products, Recipe: digest(cask), GoVersion: runtime.Version(), CosignVersion: installrelease.CosignVersion,
		HomebrewCommit: BrewCommit}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return evidence, err
	}
	if err := os.WriteFile(filepath.Join(output, "verification.json"), append(data, '\n'), fileMode); err != nil {
		return evidence, err
	}
	return evidence, os.WriteFile(filepath.Join(output, "microfat.rb"), cask, fileMode)
}

func requiredAssets(r release, version string) ([]string, error) {
	contract, err := releasecheck.NewReleaseContract(version)
	if err != nil {
		return nil, err
	}
	names := []string{"checksums.txt", "checksums.txt.sig"}
	for name := range contract.ExpectedPayloadNames {
		names = append(names, name)
	}
	seen := make(map[string]bool)
	for _, asset := range r.Assets {
		if asset.Name == "" || seen[asset.Name] || asset.Size <= 0 || asset.Size > assetLimit {
			return nil, errors.New("invalid, empty, duplicate or oversized release asset")
		}
		seen[asset.Name] = true
	}
	for _, name := range names {
		if !seen[name] {
			return nil, fmt.Errorf("release is missing %s", name)
		}
	}
	slices.Sort(names)
	return names, nil
}

func authenticate(ctx context.Context, dist, version, source string) (map[string]string, error) {
	return authenticateWith(ctx, dist, version, source, installrelease.Staging,
		installrelease.NewClient().PrepareVerifier, releaseaudit.ExecuteProcess)
}

type verifierPreparation func(context.Context, string, string, installrelease.Cosign) (installrelease.Cosign, error)

func authenticateWith(ctx context.Context, dist, version, source string, stagingDirectory func(string) (string, error),
	prepare verifierPreparation, execute releaseaudit.Execute) (map[string]string, error) {
	staging, err := stagingDirectory("")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	verifier, err := prepare(ctx, runtime.GOARCH, staging, installrelease.Cosign{})
	if err != nil {
		return nil, err
	}
	environment := releaseaudit.CleanEnvironment(os.Environ())
	environment = slices.DeleteFunc(environment, func(entry string) bool {
		return bytes.HasPrefix([]byte(entry), []byte("COSIGN_")) || bytes.HasPrefix([]byte(entry), []byte("SIGSTORE_"))
	})
	runner := releaseaudit.Runner{Context: ctx, Output: filepath.Dir(dist), Environment: environment,
		Execute: func(ctx context.Context, spec process.Spec) (releaseaudit.Result, error) {
			if spec.Path != "cosign" {
				return releaseaudit.Result{}, errors.New("unexpected authentication command")
			}
			spec.Path = verifier.Path
			return execute(ctx, spec)
		}}
	return releaseaudit.Authenticate(releaseaudit.Options{Dist: dist, Version: version, Source: source}, runner)
}

func validateProducts(dist, version string) (map[string]map[string]string, error) {
	return inspectProducts(dist, version, releasecheck.ValidateArtifact, validateSBOMs)
}

func inspectProducts(dist, version string, inspect func(string) (*releasecheck.ArchiveFacts, error),
	validate func(string, string, *releasecheck.ArchiveFacts) error) (map[string]map[string]string, error) {
	contract, err := releasecheck.NewReleaseContract(version)
	if err != nil {
		return nil, err
	}
	products := make(map[string]map[string]string)
	for name := range contract.ExpectedPayloadNames {
		if filepath.Ext(name) == ".json" {
			continue
		}
		facts, err := inspect(filepath.Join(dist, name))
		if err != nil {
			return nil, err
		}
		if err := validate(dist, name, facts); err != nil {
			return nil, errors.Join(err, facts.Cleanup())
		}
		if facts.Kind != releasecheck.NativeHelper {
			products[facts.TargetArch] = make(map[string]string)
			for product, executable := range facts.Executables {
				products[facts.TargetArch][product] = executable.SHA256
			}
		}
		if err := facts.Cleanup(); err != nil {
			return nil, err
		}
	}
	return products, nil
}

func validateSBOMs(dist, name string, facts *releasecheck.ArchiveFacts) error {
	inventory, err := releasecheck.ExtractArchiveInventory(facts)
	if err != nil {
		return err
	}
	if err := releasecheck.ValidateSPDX(filepath.Join(dist, name+".spdx.json"), facts, inventory); err != nil {
		return err
	}
	return releasecheck.ValidateCycloneDX(filepath.Join(dist, name+".cyclonedx.json"), facts, inventory)
}

func Check(ctx context.Context, filename string) error {
	return check(ctx, filename, Prepare)
}

func check(ctx context.Context, filename string, prepare func(context.Context, string, string) (Evidence, error)) error {
	info, err := os.Lstat(filename)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > metadataLimit {
		return errors.New("cask must be a bounded regular file")
	}
	data, err := os.ReadFile(filename) // #nosec G304 -- bounded regular cask selected for read-only verification.
	if err != nil {
		return err
	}
	version, err := CaskVersion(data)
	if err != nil {
		return err
	}
	parent, err := os.MkdirTemp("", "microfat-homebrew-check-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(parent) }()
	output := filepath.Join(parent, "candidate")
	if _, err := prepare(ctx, "v"+version, output); err != nil {
		return err
	}
	generated, err := os.ReadFile(filepath.Join(output, "microfat.rb")) // #nosec G304 -- fixed generated file in private staging.
	if err != nil {
		return err
	}
	if !bytes.Equal(data, generated) {
		return errors.New("cask differs from authenticated deterministic recipe")
	}
	return nil
}
