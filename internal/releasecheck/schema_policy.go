package releasecheck

import (
	"strconv"
	"strings"
)

// UsesModernSBOM selects the v0.2.5 schema contract, including its snapshots.
// Unrecognized release versions fail closed to the modern contract.
func UsesModernSBOM(version string) bool {
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
	const firstModernMinor, firstModernPatch = 2, 5
	return numbers[0] > 0 || numbers[1] > firstModernMinor || (numbers[1] == firstModernMinor && numbers[2] >= firstModernPatch)
}

func modernArchive(facts *ArchiveFacts) bool {
	identity, err := ParseReleaseArchiveName(facts.ArchiveName)
	return err != nil || UsesModernSBOM(identity.Version)
}
