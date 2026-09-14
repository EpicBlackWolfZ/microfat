package builder

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveStubPath resolves the path to the microfat launcher stub binary using strict precedence:
// 1. Explicit CLI flag `--stub`: resolved against current working directory; invalid value returns immediate error with no fallback.
// 2. Manifest field `stub:`: resolved against manifest directory; invalid value returns immediate error with no fallback.
// 3. Sibling full stub beside the original installed CLI (microfat-stub), as resolved by ResolveInstallationDirectory().
// 4. Full stub in the caller's PATH, searching only absolute directories in order (skipping empty, relative, ".", "bin" entries).
// 5. Returns ErrStubNotFound with an explanatory error.
//
// Note: microfat-stub-minimal is never automatically selected; it requires explicit selection via --stub or stub:.
// Implicit repository-relative lookups (such as bin/microfat-stub or ../bin/microfat-stub) are strictly forbidden.
func ResolveStubPath(cliStub, manifestStub, manifestDir string) (string, error) {
	// 1. Explicit CLI flag `--stub`
	if cliStub != "" {
		resolved := cliStub
		if !filepath.IsAbs(resolved) {
			cwd, err := os.Getwd()
			if err != nil {
				return "", fmt.Errorf("resolving working directory for --stub: %w", err)
			}
			resolved = filepath.Join(cwd, resolved)
		}
		clean := filepath.Clean(resolved)
		realFile, err := filepath.EvalSymlinks(clean)
		if err != nil {
			return "", fmt.Errorf("%w: %s (specified via --stub): %w", ErrStubNotFound, cliStub, err)
		}
		stat, err := os.Stat(realFile)
		if err != nil || stat.IsDir() || !stat.Mode().IsRegular() {
			return "", fmt.Errorf("%w: %s (specified via --stub)", ErrStubNotFound, cliStub)
		}
		f, err := os.Open(realFile)
		if err != nil {
			return "", fmt.Errorf("%w: %s cannot be read: %w", ErrStubNotFound, cliStub, err)
		}
		_ = f.Close()
		return clean, nil
	}

	// 2. Manifest field `stub:`
	if manifestStub != "" {
		resolved := manifestStub
		if !filepath.IsAbs(resolved) && manifestDir != "" {
			resolved = filepath.Join(manifestDir, resolved)
		}
		clean := filepath.Clean(resolved)
		realFile, err := filepath.EvalSymlinks(clean)
		if err != nil {
			return "", fmt.Errorf("%w: %s (specified in manifest): %w", ErrStubNotFound, manifestStub, err)
		}
		stat, err := os.Stat(realFile)
		if err != nil || stat.IsDir() || !stat.Mode().IsRegular() {
			return "", fmt.Errorf("%w: %s (specified in manifest)", ErrStubNotFound, manifestStub)
		}
		f, err := os.Open(realFile)
		if err != nil {
			return "", fmt.Errorf("%w: %s cannot be read: %w", ErrStubNotFound, manifestStub, err)
		}
		_ = f.Close()
		return clean, nil
	}

	// 3. Sibling microfat-stub beside original installed CLI
	if installDir, err := ResolveInstallationDirectory(); err == nil && installDir != "" {
		candidate := filepath.Join(installDir, "microfat-stub")
		if realCandidate, err := filepath.EvalSymlinks(candidate); err == nil {
			if stat, err := os.Stat(realCandidate); err == nil && !stat.IsDir() && stat.Mode().IsRegular() {
				if f, err := os.Open(realCandidate); err == nil {
					_ = f.Close()
					return filepath.Clean(candidate), nil
				}
			}
		}
	}

	// 4. Sibling microfat-stub in caller's PATH (absolute entries only)
	if pathStub, err := findStubInPATH("microfat-stub"); err == nil {
		return pathStub, nil
	}

	// 5. Explanatory error detailing all options
	return "", fmt.Errorf(
		"%w: provide --stub flag, 'stub:' manifest entry, install microfat-stub beside microfat, "+
			"or place microfat-stub in an absolute directory in PATH",
		ErrStubNotFound,
	)
}

// findStubInPATH searches for an executable stub in the system PATH.
// Strict security policy: Only absolute directory entries are evaluated.
// Empty entries and relative path entries (e.g., ".", "bin", "../bin") are strictly ignored.
func findStubInPATH(stubName string) (string, error) {
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
		// Verify readable regular file
		// #nosec G703 -- absolute PATH entries verified
		f, err := os.Open(realCandidate)
		if err != nil {
			continue
		}
		_ = f.Close()
		return filepath.Clean(candidate), nil
	}
	return "", ErrStubNotFound
}
