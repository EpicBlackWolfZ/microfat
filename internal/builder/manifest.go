// Package builder implements the compiler orchestration and PGO matrix packaging engine for microfat.
package builder

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
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

	// Dir stores the directory containing the manifest file for relative path resolution.
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
	cleanPath := filepath.Clean(manifestPath)
	data, err := os.ReadFile(cleanPath)
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

	return nil
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
