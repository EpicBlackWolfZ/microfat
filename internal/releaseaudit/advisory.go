package releaseaudit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
)

// PrepareAdvisory authenticates the release before preparing its binaries for
// static inspection. It supports both release architectures without executing them.
func PrepareAdvisory(ctx context.Context, options Options, environment []string, execute Execute) error {
	options.Arch = runtime.GOARCH
	if err := options.Validate(runtime.GOOS, runtime.GOARCH); err != nil {
		return err
	}
	if err := os.Mkdir(options.Output, directoryMode); err != nil {
		return err
	}
	runner := Runner{Context: ctx, Output: options.Output, Environment: CleanEnvironment(environment), Execute: execute}
	verified, err := Authenticate(options, runner)
	if err != nil {
		return fmt.Errorf("authenticating published assets: %w", err)
	}
	contract, err := releasecheck.NewReleaseContract(options.Version)
	if err != nil {
		return err
	}
	for _, arch := range []string{releasecheck.ArchAMD64, releasecheck.ArchARM64} {
		if err := prepareAdvisoryArch(options, contract, arch); err != nil {
			return fmt.Errorf("preparing %s published binaries: %w", arch, err)
		}
	}
	return writeJSON(filepath.Join(options.Output, "verification.json"), map[string]any{
		"version": options.Version,
		"source":  options.Source,
		"assets":  verified,
	})
}

func prepareAdvisoryArch(options Options, contract *releasecheck.ReleaseContract, arch string) error {
	archive, err := releasecheck.ValidateArtifact(filepath.Join(options.Dist, contract.ExpectedArchives[arch]))
	if err != nil {
		return err
	}
	prepareErr := copyAdvisoryBinaries(options, contract, archive, arch)
	return errors.Join(prepareErr, archive.Cleanup())
}

func copyAdvisoryBinaries(options Options, contract *releasecheck.ReleaseContract, archive *releasecheck.ArchiveFacts, arch string) error {
	helper := filepath.Join(options.Dist, contract.ExpectedHelpers[arch])
	if _, err := releasecheck.ValidateArtifact(helper); err != nil {
		return err
	}
	output := filepath.Join(options.Output, arch)
	if err := os.Mkdir(output, directoryMode); err != nil {
		return err
	}
	for _, name := range contract.RequiredExecutables {
		if err := copyAdvisoryBinary(filepath.Join(archive.ExtractedDir, name), filepath.Join(output, name)); err != nil {
			return err
		}
	}
	for _, tier := range contract.ExpectedTiers[arch] {
		name := releasecheck.ReleaseProjectName + "-payload-" + tier
		if err := os.WriteFile(filepath.Join(output, name), archive.EmbeddedVariants[tier].Data, fileMode); err != nil {
			return err
		}
	}
	return copyAdvisoryBinary(helper, filepath.Join(output, releasecheck.ReleaseInstaller))
}

func copyAdvisoryBinary(source, destination string) error {
	// #nosec G304 -- source is an authenticated and validated release binary.
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, fileMode) // #nosec G703 -- fixed artifact name under the caller's private staging directory.
}
