// microfat-install is the native helper executed by the authenticated bootstrap.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/EpicBlackWolfZ/microfat/internal/install"
	"github.com/EpicBlackWolfZ/microfat/internal/installrelease"
)

const failureCode = 1
const usageCode = 2

type options struct {
	version, bin, store, staging, cosign, pin string
	system, repair, downgrade, uninstall      bool
}

type releaseClient interface {
	Resolve(context.Context, string) (string, error)
	Acquire(context.Context, string, string, string, installrelease.Verifier) (install.Generation, string, error)
}

type environment struct {
	goos, arch string
	uid        int
	home       func() (string, error)
	getenv     func(string) string
	client     releaseClient
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], environment{goos: runtime.GOOS, arch: runtime.GOARCH, uid: os.Geteuid(), home: os.UserHomeDir,
		getenv: os.Getenv, client: installrelease.NewClient()}, os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("microfat-install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.version, "version", "", "Exact stable release to install (default: latest stable)")
	flags.StringVar(&opts.bin, "bin-dir", "", "Directory for the three public entrypoints (default: ~/.local/bin)")
	flags.StringVar(&opts.store, "store-dir", "", "Installation generation store (default: XDG user data directory)")
	flags.StringVar(&opts.staging, "staging-dir", "", "Private download staging parent directory")
	flags.StringVar(&opts.cosign, "cosign", "", "Absolute independently authenticated Cosign executable")
	flags.StringVar(&opts.pin, "cosign-sha256", "", "Independent SHA-256 pin for that verifier")
	flags.BoolVar(&opts.system, "system", false, "Explicit system installation (requires root and explicit bin/store directories)")
	flags.BoolVar(&opts.repair, "repair", false, "Explicitly repair a corrupt installer-owned generation")
	flags.BoolVar(&opts.downgrade, "allow-downgrade", false, "Allow an explicitly selected older stable release")
	flags.BoolVar(&opts.uninstall, "uninstall", false, "Remove owned entrypoint links; retain generations and user data")
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	if flags.NArg() != 0 {
		return opts, errors.New("unexpected positional arguments")
	}
	if opts.uninstall && (opts.version != "" || opts.repair || opts.downgrade) {
		return opts, errors.New("uninstall cannot select or repair a release")
	}
	if opts.downgrade && opts.version == "" {
		return opts, errors.New("--allow-downgrade requires --version")
	}
	return opts, nil
}

func run(ctx context.Context, args []string, env environment, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "microfat-install: %v\n", err)
		return usageCode
	}
	if err := execute(ctx, opts, env, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "microfat-install: %v\n", err)
		return failureCode
	}
	return 0
}

func resolvePaths(opts options, env environment) (install.Paths, error) {
	paths := install.Paths{Bin: opts.bin, Store: opts.store}
	if env.goos != "linux" || (env.arch != "amd64" && env.arch != "arm64") {
		return paths, errors.New("supported systems are Linux amd64 and arm64")
	}
	if opts.system {
		if env.uid != 0 || paths.Bin == "" || paths.Store == "" {
			return paths, errors.New("--system requires root and explicit --bin-dir and --store-dir")
		}
		return paths, paths.Validate()
	}
	if env.uid == 0 {
		return paths, errors.New("running as root requires explicit --system, --bin-dir and --store-dir; no sudo is invoked")
	}
	if paths.Bin == "" || paths.Store == "" {
		home, err := env.home()
		if err != nil {
			return paths, err
		}
		if !filepath.IsAbs(home) {
			return paths, errors.New("home directory must be absolute")
		}
		if paths.Bin == "" {
			paths.Bin = filepath.Join(home, ".local", "bin")
		}
		if paths.Store == "" {
			data := env.getenv("XDG_DATA_HOME")
			if data == "" {
				data = filepath.Join(home, ".local", "share")
			}
			if !filepath.IsAbs(data) {
				return paths, errors.New("XDG_DATA_HOME must be absolute")
			}
			paths.Store = filepath.Join(data, "microfat", "installations", "default")
		}
	}
	return paths, paths.Validate()
}

func execute(ctx context.Context, opts options, env environment, stdout io.Writer) error {
	paths, err := resolvePaths(opts, env)
	if err != nil {
		return err
	}
	snapshot, err := install.Inspect(paths)
	if err != nil {
		return err
	}
	if opts.uninstall {
		result, removeErr := install.Uninstall(ctx, snapshot)
		_, outputErr := fmt.Fprintf(stdout, "Removed %d owned entrypoints; preserved %d modified entries.\nRetained generations: %s\n",
			len(result.Removed), len(result.Preserved), paths.Store)
		return errors.Join(removeErr, outputErr)
	}
	if opts.cosign == "" || opts.pin == "" {
		return errors.New("use the verified bootstrap or supply --cosign and --cosign-sha256")
	}
	version, err := env.client.Resolve(ctx, opts.version)
	if err != nil {
		return err
	}
	if err := checkVersion(snapshot, version, opts); err != nil {
		return err
	}
	staging, err := installrelease.Staging(opts.staging)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	generation, source, err := env.client.Acquire(ctx, version, env.arch, staging, installrelease.Cosign{Path: opts.cosign, SHA256: opts.pin})
	if err != nil {
		return err
	}
	result, err := install.Apply(ctx, snapshot, generation, source, install.ApplyOptions{Repair: opts.repair})
	if err != nil {
		if result.Activated {
			return fmt.Errorf("v%s was activated, but installation completion failed: %w", result.Generation.Version, err)
		}
		return err
	}
	action := "Activated"
	if result.Reused {
		action = "Verified existing"
	}
	_, err = fmt.Fprintf(stdout, "%s microfat v%s (%s).\nEntrypoints: %s\nGeneration store: %s\nEnsure the entrypoint directory is in PATH.\n",
		action, result.Generation.Version, result.Generation.Arch, paths.Bin, paths.Store)
	return err
}

func checkVersion(snapshot install.Snapshot, version string, opts options) error {
	current, err := snapshot.Current()
	if err != nil {
		if errors.Is(err, install.ErrCorrupt) && opts.repair && opts.version != "" {
			return nil
		}
		return fmt.Errorf("cannot determine installed version; inspect the installation or explicitly repair a pinned release: %w", err)
	}
	if current != nil && installrelease.CompareVersions(version, current.Version) < 0 && !opts.downgrade {
		return errors.New("refusing downgrade; select --version and --allow-downgrade explicitly")
	}
	return nil
}
