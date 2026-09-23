package builder

import (
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

// Errors related to compiled artifact buildinfo validation.
var (
	ErrInvalidArtifactBuildInfo = errors.New("invalid compiled artifact build metadata")
	ErrStagedArtifactModified   = errors.New("staged artifact identity modified before packaging")
)

// ExpectedTarget defines the target parameters expected to be embedded in a compiled Go binary.
type ExpectedTarget struct {
	OS   string
	Arch string
	Tier string
}

// StagedArtifactIdentity records identity metadata of an inspected compiled artifact.
type StagedArtifactIdentity struct {
	Path    string
	Dev     uint64
	Ino     uint64
	Size    int64
	ModTime time.Time
}

var readBuildInfoFunc = buildinfo.Read

// ValidateArtifactBuildInfo opens a compiled artifact with input-safety conventions,
// extracts its Go build settings via debug/buildinfo without executing it,
// and verifies that GOOS, GOARCH, and the applicable CPU tier match expectations.
func ValidateArtifactBuildInfo(artifactPath string, expected ExpectedTarget) (*StagedArtifactIdentity, error) {
	f, err := inputfile.Open(artifactPath)
	if err != nil {
		return nil, fmt.Errorf("%w: opening %s: %w", ErrInvalidArtifactBuildInfo, artifactPath, err)
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: stating %s: %w", ErrInvalidArtifactBuildInfo, artifactPath, err)
	}

	bi, err := readBuildInfoFunc(f)
	if err != nil {
		return nil, fmt.Errorf("%w: reading buildinfo from %s: %w", ErrInvalidArtifactBuildInfo, artifactPath, err)
	}

	counts := make(map[string]int)
	settings := make(map[string]string)
	for _, s := range bi.Settings {
		if isReservedTargetKey(s.Key) {
			counts[s.Key]++
			if counts[s.Key] > 1 {
				return nil, fmt.Errorf("%w: duplicate build setting %q in %s", ErrInvalidArtifactBuildInfo, s.Key, artifactPath)
			}
			settings[s.Key] = s.Value
		}
	}

	// Validate GOOS
	actualOS, hasOS := settings[EnvGOOS]
	if !hasOS || actualOS == "" {
		return nil, fmt.Errorf("%w: missing GOOS setting in %s", ErrInvalidArtifactBuildInfo, artifactPath)
	}
	if actualOS != expected.OS {
		return nil, fmt.Errorf("%w: GOOS mismatch in %s (expected %q, got %q)",
			ErrInvalidArtifactBuildInfo, artifactPath, expected.OS, actualOS)
	}

	// Validate GOARCH
	actualArch, hasArch := settings[EnvGOARCH]
	if !hasArch || actualArch == "" {
		return nil, fmt.Errorf("%w: missing GOARCH setting in %s", ErrInvalidArtifactBuildInfo, artifactPath)
	}
	if actualArch != expected.Arch {
		return nil, fmt.Errorf("%w: GOARCH mismatch in %s (expected %q, got %q)",
			ErrInvalidArtifactBuildInfo, artifactPath, expected.Arch, actualArch)
	}

	// Validate architecture-specific CPU tier
	switch expected.Arch {
	case microarch.ArchAMD64:
		if val, has := settings[EnvGOARM64]; has {
			return nil, fmt.Errorf("%w: architecture-inapplicable GOARM64=%q in amd64 artifact %s",
				ErrInvalidArtifactBuildInfo, val, artifactPath)
		}
		actualTier, hasTier := settings[EnvGOAMD64]
		if !hasTier || actualTier == "" {
			return nil, fmt.Errorf("%w: missing GOAMD64 setting in %s", ErrInvalidArtifactBuildInfo, artifactPath)
		}
		if actualTier != expected.Tier {
			return nil, fmt.Errorf("%w: GOAMD64 mismatch in %s (expected %q, got %q)",
				ErrInvalidArtifactBuildInfo, artifactPath, expected.Tier, actualTier)
		}
	case microarch.ArchARM64:
		if val, has := settings[EnvGOAMD64]; has {
			return nil, fmt.Errorf("%w: architecture-inapplicable GOAMD64=%q in arm64 artifact %s",
				ErrInvalidArtifactBuildInfo, val, artifactPath)
		}
		actualTier, hasTier := settings[EnvGOARM64]
		if !hasTier || actualTier == "" {
			return nil, fmt.Errorf("%w: missing GOARM64 setting in %s", ErrInvalidArtifactBuildInfo, artifactPath)
		}
		if err := matchARM64Setting(actualTier, expected.Tier); err != nil {
			return nil, fmt.Errorf("%w: GOARM64 semantic mismatch in %s: %w", ErrInvalidArtifactBuildInfo, artifactPath, err)
		}
	default:
		return nil, fmt.Errorf("%w: unsupported target architecture %q", ErrInvalidArtifactBuildInfo, expected.Arch)
	}

	dev, ino, _ := fileDevIno(stat)
	identity := &StagedArtifactIdentity{
		Path:    artifactPath,
		Dev:     dev,
		Ino:     ino,
		Size:    stat.Size(),
		ModTime: stat.ModTime(),
	}
	return identity, nil
}

type arm64Setting struct {
	tier   string
	lse    bool
	crypto bool
}

var validARM64Tiers = map[string]struct{}{
	"v8.0": {}, "v8.1": {}, "v8.2": {}, "v8.3": {}, "v8.4": {},
	"v8.5": {}, "v8.6": {}, "v8.7": {}, "v8.8": {}, "v8.9": {},
	"v9.0": {}, "v9.1": {}, "v9.2": {}, "v9.3": {}, "v9.4": {}, "v9.5": {},
}

func parseARM64Setting(s string) (arm64Setting, error) {
	if s == "" {
		return arm64Setting{}, errors.New("empty ARM64 setting")
	}
	parts := strings.Split(s, ",")
	base := parts[0]
	if _, ok := validARM64Tiers[base]; !ok {
		return arm64Setting{}, fmt.Errorf("unknown ARM64 base tier %q", base)
	}

	res := arm64Setting{tier: base}
	// For tiers v8.1+ and v9.0+, LSE is mandatory in the Go compiler specification
	if base != "v8.0" {
		res.lse = true
	}

	seenSuffix := make(map[string]struct{}, len(parts)-1)
	for _, suffix := range parts[1:] {
		if suffix == "" {
			return arm64Setting{}, errors.New("empty ARM64 feature suffix")
		}
		if _, seen := seenSuffix[suffix]; seen {
			return arm64Setting{}, fmt.Errorf("duplicate ARM64 feature suffix %q", suffix)
		}
		seenSuffix[suffix] = struct{}{}
		switch suffix {
		case "lse":
			res.lse = true
		case "crypto":
			res.crypto = true
		default:
			return arm64Setting{}, fmt.Errorf("unknown ARM64 feature suffix %q", suffix)
		}
	}
	return res, nil
}

func matchARM64Setting(actualSetting, expectedTier string) error {
	expected, err := parseARM64Setting(expectedTier)
	if err != nil {
		return fmt.Errorf("invalid expected ARM64 tier %q: %w", expectedTier, err)
	}
	actual, err := parseARM64Setting(actualSetting)
	if err != nil {
		return fmt.Errorf("invalid artifact ARM64 setting %q: %w", actualSetting, err)
	}

	if expected.tier != actual.tier {
		return fmt.Errorf("tier mismatch: expected %q, got %q", expected.tier, actual.tier)
	}
	if expected.lse != actual.lse {
		return fmt.Errorf("LSE feature mismatch for %s: expected %v, got %v", expected.tier, expected.lse, actual.lse)
	}
	if expected.crypto != actual.crypto {
		return fmt.Errorf("crypto feature mismatch for %s: expected %v, got %v", expected.tier, expected.crypto, actual.crypto)
	}
	return nil
}

func verifyStagedIdentities(identities map[string]*StagedArtifactIdentity) error {
	for level, id := range identities {
		if id == nil {
			return fmt.Errorf("%w: missing identity for variant %s", ErrStagedArtifactModified, level)
		}
		fi, err := os.Stat(id.Path)
		if err != nil {
			return fmt.Errorf("%w: stating %s for variant %s: %w", ErrStagedArtifactModified, id.Path, level, err)
		}
		if fi.Size() != id.Size {
			return fmt.Errorf("%w: size changed for variant %s (expected %d, got %d)",
				ErrStagedArtifactModified, level, id.Size, fi.Size())
		}
		if !fi.ModTime().Equal(id.ModTime) {
			return fmt.Errorf("%w: modtime changed for variant %s (%s)",
				ErrStagedArtifactModified, level, id.Path)
		}
		dev, ino, ok := fileDevIno(fi)
		if ok && (dev != id.Dev || ino != id.Ino) {
			return fmt.Errorf("%w: device/inode changed for variant %s (%s)",
				ErrStagedArtifactModified, level, id.Path)
		}
	}
	return nil
}
