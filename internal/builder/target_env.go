package builder

import (
	"fmt"
	"sort"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

// Reserved target environment variable names controlled exclusively by the manifest.
const (
	EnvGOOS    = "GOOS"
	EnvGOARCH  = "GOARCH"
	EnvGOAMD64 = "GOAMD64"
	EnvGOARM64 = "GOARM64"
)

// assembleTargetEnv constructs a deterministic, deduplicated compiler environment
// for compiling variant v of manifest m. Controlled target keys (GOOS, GOARCH, GOAMD64, GOARM64)
// are stripped from the ambient environment, validated ordinary variables are overlaid
// in ambient -> root -> variant order, and exactly one authoritative GOOS, GOARCH, and active
// architecture-tier variable is added.
func assembleTargetEnv(ambient []string, m *Manifest, v VariantConfig) []string {
	// Parse and deduplicate ambient environment with last-value semantics.
	envMap := make(map[string]string, len(ambient))
	for _, entry := range ambient {
		k, val, ok := strings.Cut(entry, "=")
		if !ok || k == "" {
			continue
		}
		if isReservedTargetKey(k) {
			continue
		}
		envMap[k] = val
	}

	// Overlay ordinary root variables
	for k, val := range m.Env {
		if isReservedTargetKey(k) {
			continue
		}
		envMap[k] = val
	}

	// Overlay ordinary variant variables
	for k, val := range v.Env {
		if isReservedTargetKey(k) {
			continue
		}
		envMap[k] = val
	}

	// Add authoritative target configuration
	envMap[EnvGOOS] = m.TargetOS
	envMap[EnvGOARCH] = m.TargetArch
	switch m.TargetArch {
	case microarch.ArchAMD64:
		envMap[EnvGOAMD64] = v.Level
	case microarch.ArchARM64:
		envMap[EnvGOARM64] = v.Level
	}

	// Serialize deterministically sorted by key name
	keys := make([]string, 0, len(envMap))
	for k := range envMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := make([]string, len(keys))
	for i, k := range keys {
		result[i] = fmt.Sprintf("%s=%s", k, envMap[k])
	}
	return result
}

func isReservedTargetKey(k string) bool {
	return k == EnvGOOS || k == EnvGOARCH || k == EnvGOAMD64 || k == EnvGOARM64
}
