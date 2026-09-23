// Package builder implements the compiler orchestration and PGO matrix packaging engine for microfat.
package builder

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"gopkg.in/yaml.v3"
)

// Standard error definitions for builder operations.
var (
	ErrManifestNotFound    = errors.New("manifest file not found")
	ErrEmptyManifest       = errors.New("manifest has no variants defined")
	ErrInvalidManifest     = errors.New("invalid manifest configuration")
	ErrDuplicateVariant    = errors.New("duplicate variant level in manifest")
	ErrUnsupportedArch     = errors.New("unsupported target architecture")
	ErrInvalidVariantLevel = errors.New("invalid variant level for target architecture")
	ErrProfileNotFound     = errors.New("specified PGO profile file not found")
	ErrStubNotFound        = errors.New("microfat launcher stub binary not found")
	ErrInvalidTargetEnv    = errors.New("invalid target environment")
)

// CompressionConfig defines declarative compression parameters.
type CompressionConfig struct {
	Profile    string `json:"profile,omitempty" yaml:"profile,omitempty"`
	Algorithm  string `json:"algorithm,omitempty" yaml:"algorithm,omitempty"`
	Level      string `json:"level,omitempty" yaml:"level,omitempty"`
	EnableDict bool   `json:"dict,omitempty" yaml:"dict,omitempty"`
	DictSize   int    `json:"dict_size,omitempty" yaml:"dict_size,omitempty"`
}

// Manifest defines the declarative configuration for compiling and packaging
// a multi-microarchitecture Go binary with Profile-Guided Optimization (PGO).
type Manifest struct {
	AppName     string             `json:"name,omitempty" yaml:"name,omitempty"`
	Package     string             `json:"package,omitempty" yaml:"package,omitempty"`
	Output      string             `json:"output,omitempty" yaml:"output,omitempty"`
	Stub        string             `json:"stub,omitempty" yaml:"stub,omitempty"`
	TargetOS    string             `json:"target_os,omitempty" yaml:"target_os,omitempty"`
	TargetArch  string             `json:"target_arch,omitempty" yaml:"target_arch,omitempty"`
	DefaultPGO  string             `json:"default_pgo,omitempty" yaml:"default_pgo,omitempty"`
	BuildFlags  []string           `json:"build_flags,omitempty" yaml:"build_flags,omitempty"`
	Tags        []string           `json:"tags,omitempty" yaml:"tags,omitempty"`
	Env         map[string]string  `json:"env,omitempty" yaml:"env,omitempty"`
	Compression *CompressionConfig `json:"compression,omitempty" yaml:"compression,omitempty"`
	Variants    []VariantConfig    `json:"variants" yaml:"variants"`

	// Dir stores the absolute directory containing the loaded manifest for relative path resolution.
	Dir string `json:"-" yaml:"-"`
}

// VariantConfig defines compiler parameters and profile mappings for a single microarchitecture tier.
type VariantConfig struct {
	Level       string             `json:"level" yaml:"level"`
	PGO         string             `json:"pgo,omitempty" yaml:"pgo,omitempty"`
	Flags       []string           `json:"flags,omitempty" yaml:"flags,omitempty"`
	Env         map[string]string  `json:"env,omitempty" yaml:"env,omitempty"`
	Compression *CompressionConfig `json:"compression,omitempty" yaml:"compression,omitempty"`
}

// LoadManifest reads, unmarshals, and validates a YAML or JSON build manifest from the specified file path.
func LoadManifest(manifestPath string) (*Manifest, error) {
	cleanPath, err := filepath.Abs(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("resolving manifest path: %w", err)
	}
	file, err := inputfile.Open(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %s (%w)", ErrManifestNotFound, cleanPath, err)
	}
	defer func() { _ = file.Close() }()
	const maxManifestBytes = 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("manifest exceeds %d bytes", maxManifestBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s (%w)", ErrManifestNotFound, cleanPath, err)
	}

	manifestDir := filepath.Dir(cleanPath)
	m, err := ParseManifest(data, filepath.Ext(cleanPath))
	if err != nil {
		return nil, fmt.Errorf("%w in %s: %w", ErrInvalidManifest, cleanPath, err)
	}

	m.Dir = manifestDir

	if err := ValidateManifest(m); err != nil {
		return nil, err
	}

	return m, nil
}

// ParseManifest unmarshals manifest data based on file extension (JSON or YAML).
func ParseManifest(data []byte, ext string) (*Manifest, error) {
	var m Manifest

	switch strings.ToLower(ext) {
	case ".json":
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parsing JSON manifest: %w", err)
		}
	default:
		// Default to YAML (which also parses JSON as a valid subset)
		if err := yaml.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parsing YAML manifest: %w", err)
		}
	}

	return &m, nil
}

// ValidateManifest verifies the semantic integrity and required fields of a Manifest.
func ValidateManifest(m *Manifest) error {
	if m.Package == "" {
		m.Package = "."
	}
	if m.TargetOS == "" {
		m.TargetOS = "linux"
	}
	if m.TargetArch == "" {
		m.TargetArch = "amd64"
	}

	targetArchLower := strings.ToLower(m.TargetArch)
	if targetArchLower != microarch.ArchAMD64 && targetArchLower != microarch.ArchARM64 {
		return fmt.Errorf("%w: %q (expected %s or %s)", ErrUnsupportedArch, m.TargetArch, microarch.ArchAMD64, microarch.ArchARM64)
	}
	m.TargetArch = targetArchLower

	if len(m.Variants) == 0 {
		return ErrEmptyManifest
	}

	if err := validateCompressionConfig(m.Compression, "manifest root"); err != nil {
		return err
	}

	seenLevels := make(map[string]struct{}, len(m.Variants))
	for i, v := range m.Variants {
		levelTrimmed := strings.TrimSpace(v.Level)
		if levelTrimmed == "" {
			return fmt.Errorf("%w: variant at index %d has empty level", ErrInvalidManifest, i)
		}

		normLevel := microarch.Normalize(levelTrimmed)
		if microarch.Rank(m.TargetArch, normLevel) < 0 {
			return fmt.Errorf("%w: %q is not a valid level for %s", ErrInvalidVariantLevel, v.Level, m.TargetArch)
		}

		if err := validateCompressionConfig(v.Compression, fmt.Sprintf("variant %s", normLevel)); err != nil {
			return err
		}

		if _, exists := seenLevels[normLevel]; exists {
			return fmt.Errorf("%w: %q (normalized %q)", ErrDuplicateVariant, v.Level, normLevel)
		}
		seenLevels[normLevel] = struct{}{}
		m.Variants[i].Level = normLevel
	}

	return validateTargetEnvironment(m)
}

func validateCompressionConfig(c *CompressionConfig, contextStr string) error {
	if c == nil {
		return nil
	}
	if c.Profile != "" {
		p := strings.ToLower(strings.TrimSpace(c.Profile))
		switch p {
		case codec.ProfileLatency, codec.ProfileBalanced, codec.ProfileSize:
		default:
			return fmt.Errorf("%w: invalid compression profile %q in %s", ErrInvalidManifest, c.Profile, contextStr)
		}
	}
	if c.Algorithm != "" {
		algo, _ := codec.ParseCompressionSpec(c.Algorithm)
		if _, err := codec.Get(algo); err != nil {
			return fmt.Errorf("%w: invalid compression algorithm %q in %s: %v", ErrInvalidManifest, c.Algorithm, contextStr, err)
		}
	}
	return nil
}

func validateTargetEnvironment(m *Manifest) error {
	var tierKey, inappKey string
	switch m.TargetArch {
	case microarch.ArchAMD64:
		tierKey = EnvGOAMD64
		inappKey = EnvGOARM64
	case microarch.ArchARM64:
		tierKey = EnvGOARM64
		inappKey = EnvGOAMD64
	}

	if err := validateRootTargetEnvironment(m, tierKey, inappKey); err != nil {
		return err
	}
	return validateVariantTargetEnvironment(m, tierKey, inappKey)
}

func validateRootTargetEnvironment(m *Manifest, tierKey, inappKey string) error {
	if err := validateEnvMap(m.Env, "manifest root"); err != nil {
		return err
	}
	if _, exists := m.Env[inappKey]; exists {
		return fmt.Errorf("%w: %w: architecture-inapplicable key %q in manifest root for %s",
			ErrInvalidManifest, ErrInvalidTargetEnv, inappKey, m.TargetArch)
	}
	if val, exists := m.Env[EnvGOOS]; exists {
		if val == "" {
			return fmt.Errorf("%w: %w: empty reserved target variable %q in manifest root",
				ErrInvalidManifest, ErrInvalidTargetEnv, EnvGOOS)
		}
		if val != m.TargetOS {
			return fmt.Errorf("%w: %w: root GOOS=%q contradicts manifest target_os=%q",
				ErrInvalidManifest, ErrInvalidTargetEnv, val, m.TargetOS)
		}
	}
	if val, exists := m.Env[EnvGOARCH]; exists {
		if val == "" {
			return fmt.Errorf("%w: %w: empty reserved target variable %q in manifest root",
				ErrInvalidManifest, ErrInvalidTargetEnv, EnvGOARCH)
		}
		if val != m.TargetArch {
			return fmt.Errorf("%w: %w: root GOARCH=%q contradicts manifest target_arch=%q",
				ErrInvalidManifest, ErrInvalidTargetEnv, val, m.TargetArch)
		}
	}
	if rootTier, exists := m.Env[tierKey]; exists {
		if rootTier == "" {
			return fmt.Errorf("%w: %w: empty reserved target variable %q in manifest root",
				ErrInvalidManifest, ErrInvalidTargetEnv, tierKey)
		}
		for _, v := range m.Variants {
			if err := matchDeclaredTier(m.TargetArch, rootTier, v.Level); err != nil {
				return fmt.Errorf("%w: %w: root %s=%q contradicts variant %s: %w",
					ErrInvalidManifest, ErrInvalidTargetEnv, tierKey, rootTier, v.Level, err)
			}
		}
	}
	return nil
}

func validateVariantTargetEnvironment(m *Manifest, tierKey, inappKey string) error {
	for _, v := range m.Variants {
		contextStr := fmt.Sprintf("variant %s", v.Level)
		if err := validateEnvMap(v.Env, contextStr); err != nil {
			return err
		}
		if _, exists := v.Env[inappKey]; exists {
			return fmt.Errorf("%w: %w: architecture-inapplicable key %q in %s for %s",
				ErrInvalidManifest, ErrInvalidTargetEnv, inappKey, contextStr, m.TargetArch)
		}
		if val, exists := v.Env[EnvGOOS]; exists {
			if val == "" {
				return fmt.Errorf("%w: %w: empty reserved target variable %q in %s",
					ErrInvalidManifest, ErrInvalidTargetEnv, EnvGOOS, contextStr)
			}
			if val != m.TargetOS {
				return fmt.Errorf("%w: %w: %s GOOS=%q contradicts manifest target_os=%q",
					ErrInvalidManifest, ErrInvalidTargetEnv, contextStr, val, m.TargetOS)
			}
		}
		if val, exists := v.Env[EnvGOARCH]; exists {
			if val == "" {
				return fmt.Errorf("%w: %w: empty reserved target variable %q in %s",
					ErrInvalidManifest, ErrInvalidTargetEnv, EnvGOARCH, contextStr)
			}
			if val != m.TargetArch {
				return fmt.Errorf("%w: %w: %s GOARCH=%q contradicts manifest target_arch=%q",
					ErrInvalidManifest, ErrInvalidTargetEnv, contextStr, val, m.TargetArch)
			}
		}
		if varTier, exists := v.Env[tierKey]; exists {
			if varTier == "" {
				return fmt.Errorf("%w: %w: empty reserved target variable %q in %s",
					ErrInvalidManifest, ErrInvalidTargetEnv, tierKey, contextStr)
			}
			if err := matchDeclaredTier(m.TargetArch, varTier, v.Level); err != nil {
				return fmt.Errorf("%w: %w: %s %s=%q contradicts declared level: %w",
					ErrInvalidManifest, ErrInvalidTargetEnv, contextStr, tierKey, varTier, err)
			}
		}
	}
	return nil
}

func validateEnvMap(env map[string]string, contextStr string) error {
	for k, v := range env {
		if k == "" {
			return fmt.Errorf("%w: %w: empty environment variable name in %s",
				ErrInvalidManifest, ErrInvalidTargetEnv, contextStr)
		}
		if strings.ContainsAny(k, "=\x00") {
			return fmt.Errorf("%w: %w: environment variable name %q contains invalid characters in %s",
				ErrInvalidManifest, ErrInvalidTargetEnv, k, contextStr)
		}
		if strings.Contains(v, "\x00") {
			return fmt.Errorf("%w: %w: environment variable value for %q contains NUL in %s",
				ErrInvalidManifest, ErrInvalidTargetEnv, k, contextStr)
		}
	}
	return nil
}

func matchDeclaredTier(arch, envVal, declaredLevel string) error {
	switch arch {
	case microarch.ArchAMD64:
		if envVal != declaredLevel {
			return fmt.Errorf("expected %q, got %q", declaredLevel, envVal)
		}
	case microarch.ArchARM64:
		if err := matchARM64Setting(envVal, declaredLevel); err != nil {
			return err
		}
	}
	return nil
}

func cloneManifest(m *Manifest) *Manifest {
	if m == nil {
		return nil
	}
	clone := *m
	if m.BuildFlags != nil {
		clone.BuildFlags = append([]string(nil), m.BuildFlags...)
	}
	if m.Tags != nil {
		clone.Tags = append([]string(nil), m.Tags...)
	}
	if m.Env != nil {
		clone.Env = make(map[string]string, len(m.Env))
		for k, v := range m.Env {
			clone.Env[k] = v
		}
	}
	if m.Compression != nil {
		comp := *m.Compression
		clone.Compression = &comp
	}
	if m.Variants != nil {
		clone.Variants = make([]VariantConfig, len(m.Variants))
		for i, v := range m.Variants {
			varClone := v
			if v.Flags != nil {
				varClone.Flags = append([]string(nil), v.Flags...)
			}
			if v.Env != nil {
				varClone.Env = make(map[string]string, len(v.Env))
				for k, val := range v.Env {
					varClone.Env[k] = val
				}
			}
			if v.Compression != nil {
				comp := *v.Compression
				varClone.Compression = &comp
			}
			clone.Variants[i] = varClone
		}
	}
	return &clone
}
