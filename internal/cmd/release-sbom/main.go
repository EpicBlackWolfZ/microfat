// Package main generates and validates payload-aware modern release SBOMs.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/releasesbom"
	"github.com/EpicBlackWolfZ/microfat/internal/version"
)

func parseArgs(args []string) (archive, output, format string, err error) {
	flags := flag.NewFlagSet("release-sbom", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&archive, "archive", "", "release archive path")
	flags.StringVar(&output, "output", "", "output path or format=path")
	flags.StringVar(&format, "format", "", "cyclonedx-json or spdx-json")
	positional := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positional, args = args[0], args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return "", "", "", err
	}
	if flags.NArg() > 0 {
		if flags.NArg() != 1 || positional != "" {
			return "", "", "", fmt.Errorf("unexpected positional arguments")
		}
		positional = flags.Arg(0)
	}
	if positional != "" {
		if archive != "" {
			return "", "", "", fmt.Errorf("archive specified twice")
		}
		archive = positional
	}
	if archive == "" {
		return "", "", "", fmt.Errorf("missing archive path")
	}
	format, output, err = resolveOutput(format, output)
	return archive, output, format, err
}

func resolveOutput(format, output string) (string, string, error) {
	if output == "" {
		return "", "", fmt.Errorf("missing output path")
	}
	if prefix, path, ok := strings.Cut(output, "="); ok {
		if prefix == releasesbom.FormatSPDX || prefix == releasesbom.FormatCycloneDX {
			format, output = prefix, path
		}
	}
	if format == "" {
		switch {
		case strings.HasSuffix(output, ".spdx.json"):
			format = releasesbom.FormatSPDX
		case strings.HasSuffix(output, ".cyclonedx.json"):
			format = releasesbom.FormatCycloneDX
		default:
			return "", "", fmt.Errorf("unable to determine format")
		}
	}
	switch format {
	case "spdx":
		format = releasesbom.FormatSPDX
	case "cyclonedx":
		format = releasesbom.FormatCycloneDX
	}
	if output == "" || (format != releasesbom.FormatSPDX && format != releasesbom.FormatCycloneDX) {
		return "", "", fmt.Errorf("invalid output path or unsupported format")
	}
	return format, output, nil
}

func writeAtomic(path string, data []byte) error {
	const dirMode, fileMode = 0o755, 0o644
	// #nosec G703 -- the CLI caller explicitly chooses the output directory.
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".sbom-tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(file.Name()) // #nosec G703 -- remove only the owned temporary output file.
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// #nosec G703 -- set permissions only on the newly created owned temporary file.
	if err := os.Chmod(file.Name(), fileMode); err != nil {
		return err
	}
	return os.Rename(file.Name(), path) // #nosec G703 -- atomic replacement of the explicitly requested output.
}

type generator func(context.Context, string, string) ([]byte, error)

func generate(ctx context.Context, archive, format string) ([]byte, error) {
	commit := version.Commit
	if commit == "none" {
		commit = ""
	}
	return releasesbom.Generate(ctx, archive, format,
		releasesbom.Metadata{Version: version.Version, Commit: commit, Created: time.Now()}, releasesbom.Convert)
}

func run(ctx context.Context, args []string, stderr io.Writer, generate generator) int {
	archive, output, format, err := parseArgs(args)
	if err == nil {
		var data []byte
		data, err = generate(ctx, archive, format)
		if err == nil {
			err = writeAtomic(output, data)
		}
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "Error:", err)
		return 1
	}
	return 0
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stderr, generate)
	cancel()
	os.Exit(code)
}
