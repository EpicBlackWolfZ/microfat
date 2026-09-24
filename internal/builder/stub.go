package builder

import (
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
)

const (
	// StubProfileFull selects the standard launcher stub with interactive meta-commands.
	StubProfileFull = "full"
	// StubProfileMinimal selects the lightweight launcher stub with meta-commands disabled.
	StubProfileMinimal = "minimal"

	// StubBinaryFull is the executable name of the standard full launcher stub.
	StubBinaryFull = "microfat-stub"
	// StubBinaryMinimal is the executable name of the lightweight minimal launcher stub.
	StubBinaryMinimal = "microfat-stub-minimal"
)

// ResolveStubOptions encapsulates arguments for resolving the launcher stub path.
type ResolveStubOptions struct {
	CLIStub         string
	CLIProfile      string
	ManifestStub    string
	ManifestProfile string
	ManifestDir     string
	TargetArch      string
}

// StubCompanionName returns the expected binary filename for the specified stub profile.
func StubCompanionName(profile string) (string, error) {
	switch profile {
	case "", StubProfileFull:
		return StubBinaryFull, nil
	case StubProfileMinimal:
		return StubBinaryMinimal, nil
	default:
		return "", fmt.Errorf("%w: %q (expected %q or %q)", ErrInvalidStubProfile, profile, StubProfileFull, StubProfileMinimal)
	}
}

// DetectStubProfile determines the launcher stub profile of a given stub file path
// based on filename conventions and embedded Go buildinfo tags.
// Returns StubProfileMinimal, StubProfileFull, or "" if indeterminate.
func DetectStubProfile(stubPath string) string {
	base := filepath.Base(stubPath)
	if base == StubBinaryMinimal || strings.HasSuffix(base, "-minimal") || strings.Contains(base, "stub-minimal") {
		return StubProfileMinimal
	}
	if base == StubBinaryFull || strings.HasSuffix(base, "microfat-stub") {
		return StubProfileFull
	}

	f, err := inputfile.Open(stubPath)
	if err == nil {
		defer func() { _ = f.Close() }()
		if bi, err := readBuildInfoFunc(f); err == nil && bi != nil {
			for _, s := range bi.Settings {
				if s.Key == "-tags" {
					for _, tag := range strings.Split(s.Value, ",") {
						if strings.TrimSpace(tag) == "minimal" {
							return StubProfileMinimal
						}
					}
				}
			}
			if strings.HasSuffix(bi.Path, "cmd/microfat-stub") {
				return StubProfileFull
			}
		}
	}
	return ""
}

// inspectCandidateELF performs non-executing candidate inspection, verifying ELF
// header magic and checking machine architecture compatibility when targetArch is non-empty.
func inspectCandidateELF(path, targetArch string) error {
	f, err := inputfile.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	const minELFHeaderSize = 20
	var hdr [minELFHeaderSize]byte
	n, err := f.ReadAt(hdr[:], 0)
	if err != nil && err != io.EOF {
		return err
	}
	if n < minELFHeaderSize {
		return errors.New("file too short for ELF header")
	}
	// Check ELF magic: \x7fELF
	if hdr[0] != 0x7f || hdr[1] != 'E' || hdr[2] != 'L' || hdr[3] != 'F' {
		return errors.New("invalid ELF magic")
	}

	if targetArch != "" {
		var machine uint16
		if hdr[5] == 1 { // Little-endian
			machine = binary.LittleEndian.Uint16(hdr[18:20])
		} else { // Big-endian
			machine = binary.BigEndian.Uint16(hdr[18:20])
		}
		switch targetArch {
		case "amd64", "x86_64":
			if machine != uint16(elf.EM_X86_64) {
				return fmt.Errorf("candidate ELF machine %d does not match target architecture %s", machine, targetArch)
			}
		case "arm64", "aarch64":
			if machine != uint16(elf.EM_AARCH64) {
				return fmt.Errorf("candidate ELF machine %d does not match target architecture %s", machine, targetArch)
			}
		}
	}
	return nil
}

// resolveExplicitStub verifies and resolves a caller-provided explicit stub path.
func resolveExplicitStub(stubPath, baseDir, sourceDesc string) (string, error) {
	resolved := stubPath
	if !filepath.IsAbs(resolved) {
		if baseDir != "" {
			resolved = filepath.Join(baseDir, resolved)
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return "", fmt.Errorf("resolving working directory for --stub: %w", err)
			}
			resolved = filepath.Join(cwd, resolved)
		}
	}
	clean := filepath.Clean(resolved)
	realFile, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("%w: %s (%s): %w", ErrStubNotFound, stubPath, sourceDesc, err)
	}
	stat, err := os.Stat(realFile)
	if err != nil {
		return "", fmt.Errorf("%w: %s (%s): %w", ErrStubNotFound, stubPath, sourceDesc, err)
	}
	if stat.IsDir() || !stat.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %w: %s (%s): not a regular file", ErrStubNotFound, inputfile.ErrNotRegular, stubPath, sourceDesc)
	}
	f, err := inputfile.Open(realFile)
	if err != nil {
		return "", fmt.Errorf("%w: %s cannot be read: %w", ErrStubNotFound, stubPath, err)
	}
	_ = f.Close()
	return clean, nil
}

func findSiblingStub(stubName, targetArch string) (string, error) {
	installDir, err := ResolveInstallationDirectory()
	if err != nil || installDir == "" {
		return "", ErrStubNotFound
	}
	candidate := filepath.Join(installDir, stubName)
	realCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", ErrStubNotFound
	}
	stat, err := os.Stat(realCandidate)
	if err != nil || stat.IsDir() || !stat.Mode().IsRegular() {
		return "", ErrStubNotFound
	}
	if err := inspectCandidateELF(realCandidate, targetArch); err != nil {
		return "", ErrStubNotFound
	}
	return filepath.Clean(candidate), nil
}

// findStubInPATH searches for an executable stub in the system PATH.
// Strict security policy: Only absolute directory entries are evaluated.
// Empty entries and relative path entries (e.g., ".", "bin", "../bin") are strictly ignored.
func findStubInPATH(stubName, targetArch string) (string, error) {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		return "", ErrStubNotFound
	}
	entries := filepath.SplitList(pathEnv)
	for _, entry := range entries {
		// Ignore empty and relative entries to prevent unauthorized local directory stub injection.
		if entry == "" || !filepath.IsAbs(entry) {
			continue
		}
		candidate := filepath.Join(entry, stubName)
		realCandidate, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		// #nosec G703 -- absolute PATH entries verified
		stat, err := os.Stat(realCandidate)
		if err != nil || stat.IsDir() || !stat.Mode().IsRegular() {
			continue
		}
		// Preserve normal executable requirement: check executable bit
		if stat.Mode().Perm()&0o111 == 0 {
			continue
		}
		if err := inspectCandidateELF(realCandidate, targetArch); err != nil {
			continue
		}
		return filepath.Clean(candidate), nil
	}
	return "", ErrStubNotFound
}

// ResolveStubWithOptions resolves the path to the microfat launcher stub binary using strict precedence:
// 1. Validates profile flags/fields (full or minimal).
// 2. Evaluates explicit stub and profile conflicts across CLI and manifest inputs.
// 3. Explicit CLI flag `--stub`: resolved against current working directory; invalid value returns immediate error with no fallback.
// 4. Manifest field `stub:`: resolved against manifest directory; invalid value returns immediate error with no fallback.
// 5. Sibling companion stub beside the original installed CLI as resolved by ResolveInstallationDirectory().
// 6. Companion stub in the caller's PATH, searching only absolute directories in order (skipping empty, relative, ".", "bin" entries).
// 7. Returns ErrStubNotFound with an explanatory error.
//
// Implicit repository-relative lookups (such as bin/microfat-stub or ../bin/microfat-stub) are strictly forbidden.
func validateStubOptions(opts ResolveStubOptions) error {
	if opts.CLIProfile != "" && opts.CLIProfile != StubProfileFull && opts.CLIProfile != StubProfileMinimal {
		return fmt.Errorf("%w: %q (expected %q or %q)", ErrInvalidStubProfile, opts.CLIProfile, StubProfileFull, StubProfileMinimal)
	}
	if opts.ManifestProfile != "" && opts.ManifestProfile != StubProfileFull && opts.ManifestProfile != StubProfileMinimal {
		return fmt.Errorf("%w: %q (expected %q or %q)", ErrInvalidStubProfile, opts.ManifestProfile, StubProfileFull, StubProfileMinimal)
	}

	// Conflict check: Manifest stub vs Manifest profile
	if opts.ManifestStub != "" && opts.ManifestProfile != "" {
		detected := DetectStubProfile(opts.ManifestStub)
		if detected != "" && detected != opts.ManifestProfile {
			return fmt.Errorf("%w: manifest stub %q has profile %q, which conflicts with manifest stub_profile %q",
				ErrStubProfileConflict, opts.ManifestStub, detected, opts.ManifestProfile)
		}
	}

	// Conflict check: CLI stub vs CLI profile
	if opts.CLIStub != "" && opts.CLIProfile != "" {
		detected := DetectStubProfile(opts.CLIStub)
		if detected != "" && detected != opts.CLIProfile {
			return fmt.Errorf("%w: explicit stub %q has profile %q, which conflicts with explicit stub profile %q",
				ErrStubProfileConflict, opts.CLIStub, detected, opts.CLIProfile)
		}
	}

	// Conflict check: CLI profile vs Manifest stub (when CLI stub is omitted)
	if opts.CLIStub == "" && opts.CLIProfile != "" && opts.ManifestStub != "" {
		detected := DetectStubProfile(opts.ManifestStub)
		if detected != "" && detected != opts.CLIProfile {
			return fmt.Errorf("%w: explicit CLI stub profile %q conflicts with manifest stub %q (profile %q)",
				ErrStubProfileConflict, opts.CLIProfile, opts.ManifestStub, detected)
		}
	}
	return nil
}

func discoverCompanionStub(companionName, targetArch, effectiveProfile string) (string, error) {
	if siblingStub, err := findSiblingStub(companionName, targetArch); err == nil {
		return siblingStub, nil
	}
	if pathStub, err := findStubInPATH(companionName, targetArch); err == nil {
		return pathStub, nil
	}
	return "", fmt.Errorf(
		"%w: launcher stub %q for profile %q not found; install %s beside microfat, "+
			"place %s in an absolute directory in PATH, or specify explicit --stub",
		ErrStubNotFound, companionName, effectiveProfile, companionName, companionName,
	)
}

// ResolveStubWithOptions resolves the launcher stub path honoring stub profiles,
// explicit overrides, companion discovery, and candidate validation.
func ResolveStubWithOptions(opts ResolveStubOptions) (string, error) {
	if err := validateStubOptions(opts); err != nil {
		return "", err
	}

	// 1. Explicit CLI flag `--stub` takes deliberate precedence
	if opts.CLIStub != "" {
		return resolveExplicitStub(opts.CLIStub, "", "specified via --stub")
	}

	// 2. Manifest field `stub:`
	if opts.ManifestStub != "" {
		return resolveExplicitStub(opts.ManifestStub, opts.ManifestDir, "specified in manifest")
	}

	// 3. Determine effective profile for companion stub discovery
	effectiveProfile := StubProfileFull
	if opts.ManifestProfile != "" {
		effectiveProfile = opts.ManifestProfile
	}
	if opts.CLIProfile != "" {
		effectiveProfile = opts.CLIProfile
	}

	companionName, err := StubCompanionName(effectiveProfile)
	if err != nil {
		return "", err
	}

	return discoverCompanionStub(companionName, opts.TargetArch, effectiveProfile)
}

// ResolveStubPath resolves the path to the microfat launcher stub binary using full profile defaults.
func ResolveStubPath(cliStub, manifestStub, manifestDir string) (string, error) {
	return ResolveStubWithOptions(ResolveStubOptions{
		CLIStub:      cliStub,
		ManifestStub: manifestStub,
		ManifestDir:  manifestDir,
	})
}
