package runtimequalify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/releaseaudit"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
)

func (c *controller) cliLineage() (Lineage, error) {
	lineage := Lineage{Bundle: filepath.Join(c.products, "microfat"), BundleSHA256: c.summary.Products["microfat"]}
	if c.options.Input != Candidate {
		return lineage, nil
	}
	file, err := os.Open(lineage.Bundle)
	if err != nil {
		return lineage, err
	}
	defer func() { _ = file.Close() }()
	stat, err := file.Stat()
	if err != nil {
		return lineage, err
	}
	index, err := format.ReadTrailerAndIndex(file, stat.Size())
	if err != nil {
		return lineage, err
	}
	selected, err := microarch.SelectVariantForHost(index.TargetArch, microarch.Detect(), index.VariantLevels(), microarch.Policy{})
	if err != nil {
		return lineage, err
	}
	variant, ok := index.FindVariant(selected.SelectedVariant)
	if !ok {
		return lineage, errors.New("candidate CLI lacks selected payload")
	}
	lineage.PayloadSHA256, lineage.PayloadSize, lineage.SelectedTier = variant.SHA256, variant.UncompressedSize, variant.Level
	return lineage, nil
}

func (c *controller) acquire() error {
	if c.options.Input == Candidate {
		if c.summary.Dirty || c.summary.Source != c.options.Source {
			return errors.New("candidate requires its clean exact source revision")
		}
		options := releaseaudit.Options{Dist: c.options.Dist, Output: c.options.Output, Version: strings.TrimPrefix(c.options.Tag, "v"),
			Source: c.options.Source, Arch: c.summary.Architecture}
		var err error
		c.summary.Assets, err = releaseaudit.Authenticate(options, c.runner)
		if err != nil {
			return err
		}
		c.summary.ChecksumsSHA256, err = releaseaudit.Digest(filepath.Join(c.options.Dist, "checksums.txt"))
		if err != nil {
			return err
		}
		metadata, err := os.ReadFile(filepath.Join(c.options.Dist, "release.json"))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(metadata, &c.summary.Release); err != nil {
			return err
		}
		if err := ValidateCandidate(c.summary); err != nil {
			return err
		}
		if err := c.retainAuthentication(); err != nil {
			return err
		}
		c.products, err = releaseaudit.Extract(options)
		if err != nil {
			return err
		}
	} else {
		c.products = filepath.Join(c.options.Output, "products")
		if err := os.Mkdir(c.products, privateMode); err != nil {
			return err
		}
		for _, product := range []struct{ name, pkg, tags string }{
			{"microfat", "./cmd/microfat", ""}, {fullStub, "./cmd/microfat-stub", ""},
			{minimalStub, "./cmd/microfat-stub", "minimal"},
		} {
			if err := c.build(product.pkg, filepath.Join(c.products, product.name), product.tags); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"microfat", fullStub, minimalStub} {
		digest, err := releaseaudit.Digest(filepath.Join(c.products, name))
		if err != nil {
			return err
		}
		c.summary.Products[name] = digest
		if err := c.recordBuildSettings(name, filepath.Join(c.products, name)); err != nil {
			return err
		}
	}
	return c.save()
}

func (c *controller) retainAuthentication() error {
	dir := filepath.Join(c.options.Output, "authentication")
	if err := os.Mkdir(dir, privateMode); err != nil {
		return err
	}
	for _, name := range []string{"checksums.txt", "checksums.txt.sig", "release.json"} {
		path := filepath.Join(c.options.Dist, name)
		if name == "checksums.txt.sig" {
			hash, err := releaseaudit.Digest(path)
			if err != nil {
				return err
			}
			for _, asset := range c.summary.Release.Assets {
				if asset.Name == name && asset.Digest != "sha256:"+hash {
					return errors.New("signature bundle changed")
				}
			}
		}
		if err := copyFile(path, filepath.Join(dir, name), dataMode, false); err != nil {
			return err
		}
	}
	return nil
}

func (c *controller) build(pkg, output, tags string) error {
	args := []string{c.options.Go, "build", "-trimpath", "-ldflags=-s -w", "-o", output}
	if tags != "" {
		args = append(args, "-tags="+tags)
	}
	args = append(args, pkg)
	env := map[string]string{"GOOS": "linux", "GOARCH": c.summary.Architecture, "CGO_ENABLED": "0", "GOAMD64": "v1", "GOARM64": "v8.0"}
	_, err := c.runner.Run(args, env, true)
	return err
}

func (c *controller) buildHelpers() error {
	dir := filepath.Join(c.options.Output, "helpers")
	if err := os.Mkdir(dir, privateMode); err != nil {
		return err
	}
	c.helper, c.reporter = filepath.Join(dir, "mount-runner"), filepath.Join(dir, "reporter")
	for _, helper := range []struct{ pkg, path string }{
		{"./tests/e2e/testdata/mount_runner", c.helper}, {"./tests/e2e/testdata/mount_reporter", c.reporter},
	} {
		if err := c.build(helper.pkg, helper.path, ""); err != nil {
			return err
		}
		digest, err := releaseaudit.Digest(helper.path)
		if err != nil {
			return err
		}
		c.summary.Products[filepath.Base(helper.path)] = digest
		if err := c.recordBuildSettings(filepath.Base(helper.path), helper.path); err != nil {
			return err
		}
	}
	return c.save()
}

func (c *controller) recordBuildSettings(name, path string) error {
	settings, err := c.runner.Run([]string{c.options.Go, "version", "-m", path}, nil, true)
	if err != nil {
		return err
	}
	if c.summary.BuildSettings == nil {
		c.summary.BuildSettings = map[string]string{}
	}
	c.summary.BuildSettings[name] = settings
	return nil
}

func (c *controller) pack(configuration Configuration) (Lineage, error) {
	stub := fullStub
	if configuration.Profile == Minimal {
		stub += "-minimal"
	}
	lineage := Lineage{Bundle: filepath.Join(c.options.Output, configuration.Name()),
		StubSHA256: c.summary.Products[stub], PayloadSHA256: c.summary.Products["reporter"]}
	if c.options.Input == Candidate {
		contract, err := releasecheck.NewReleaseContract(c.options.Tag)
		if err != nil {
			return lineage, err
		}
		lineage.Archive = contract.ExpectedArchives[c.summary.Architecture]
		lineage.ArchiveSHA256 = c.summary.Assets[lineage.Archive]
	}
	stat, err := os.Stat(c.reporter)
	if err != nil {
		return lineage, err
	}
	lineage.PayloadSize = stat.Size()
	base, second := "v1", "v2"
	if c.summary.Architecture == arm64 {
		base, second = "v8.0", "v8.1"
	}
	lineage.SelectedTier = base
	if configuration.Dictionary {
		output, err := c.runner.Run([]string{filepath.Join(c.products, "microfat"), "detect", "--json"}, nil, true)
		if err != nil {
			return lineage, err
		}
		var detected struct{ Arch, Level string }
		if err := json.Unmarshal([]byte(output), &detected); err != nil {
			return lineage, err
		}
		if detected.Arch != c.summary.Architecture {
			return lineage, errors.New("candidate architecture mismatch")
		}
		if microarch.Rank(detected.Arch, detected.Level) >= microarch.Rank(detected.Arch, second) {
			lineage.SelectedTier = second
		}
	}
	lineage.Arguments = []string{filepath.Join(c.products, "microfat"), "pack", "--arch", c.summary.Architecture,
		"--format-version", strconv.Itoa(configuration.Format), "--compression", configuration.Codec,
		"--stub", filepath.Join(c.products, stub), "-v", base + "=" + c.reporter, "-o", lineage.Bundle}
	if configuration.Dictionary {
		lineage.Arguments = append(lineage.Arguments, "--dict", "-v", second+"="+c.reporter)
	}
	if _, err := c.runner.Run(lineage.Arguments, nil, true); err != nil {
		return lineage, err
	}
	// Verify the candidate stub is a byte-identical prefix of the derived fixture.
	prefix, err := os.ReadFile(filepath.Join(c.products, stub))
	if err != nil {
		return lineage, err
	}
	file, err := os.Open(lineage.Bundle)
	if err != nil {
		return lineage, err
	}
	actual := make([]byte, len(prefix))
	_, readErr := io.ReadFull(file, actual)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return lineage, err
	}
	if len(prefix) == 0 || !bytes.Equal(prefix, actual) {
		return lineage, errors.New("derived fixture substituted launcher bytes")
	}
	lineage.BundleSHA256, err = releaseaudit.Digest(lineage.Bundle)
	if err != nil {
		return lineage, err
	}
	if _, err := c.runner.Run([]string{filepath.Join(c.products, "microfat"), "verify", lineage.Bundle}, nil, true); err != nil {
		return lineage, fmt.Errorf("derived bundle integrity: %w", err)
	}
	return lineage, WriteJSON(lineage.Bundle+".json", lineage)
}
