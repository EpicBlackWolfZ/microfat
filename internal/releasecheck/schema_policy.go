package releasecheck

import (
	"strconv"
	"strings"
)

// UsesModernSBOM selects the v0.2.5 schema contract, including its snapshots.
// Unrecognized release versions fail closed to the modern contract.
func UsesModernSBOM(version string) bool {
	const firstModernMinor, firstModernPatch = 2, 5
	return versionAtLeast(version, firstModernMinor, firstModernPatch)
}

// UsesInstallerHelper selects the v0.3.0 inventory, including its snapshots.
// Unknown versions require the expanded contract rather than omitting helpers.
func UsesInstallerHelper(version string) bool {
	const firstInstallerMinor = 3
	return versionAtLeast(version, firstInstallerMinor, 0)
}

func versionAtLeast(version string, minor, patch int) bool {
	const versionParts = 3
	base, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), "-")
	base, _, _ = strings.Cut(base, "+")
	parts := strings.Split(base, ".")
	if len(parts) != versionParts {
		return true
	}
	numbers := make([]int, versionParts)
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return true
		}
		numbers[index] = value
	}
	return numbers[0] > 0 || numbers[1] > minor || (numbers[1] == minor && numbers[2] >= patch)
}

func modernArchive(facts *ArchiveFacts) bool {
	identity, err := ParseReleaseArtifactName(facts.ArchiveName)
	return err != nil || UsesModernSBOM(identity.Version)
}
