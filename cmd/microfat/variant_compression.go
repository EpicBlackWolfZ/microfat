package main

import (
	"fmt"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/builder"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

func manifestVariantCompression(m *builder.Manifest, raw []string) (map[string]pack.VariantCompressionOptions, error) {
	levels := make([]string, 0, len(m.Variants))
	for _, variant := range m.Variants {
		levels = append(levels, variant.Level)
	}
	return parseVariantCompression(raw, m.TargetArch, levels)
}

// Parse operator controls before compiler work or packaging snapshots. Codec-only
// entries leave Level absent so the existing builder/packer can retain inheritance.
func parseVariantCompression(raw []string, arch string, levels []string) (map[string]pack.VariantCompressionOptions, error) {
	available := make(map[string]bool, len(levels))
	for _, level := range levels {
		available[format.NormalizeVariant(level)] = true
	}
	overrides := make(map[string]pack.VariantCompressionOptions, len(raw))
	for _, item := range raw {
		key, spec, found := strings.Cut(item, "=")
		if !found || strings.TrimSpace(key) == "" || strings.TrimSpace(spec) == "" {
			return nil, fmt.Errorf("invalid --variant-compression %q: expected level=codec[:level] (e.g. v3=zstd:best)", item)
		}
		level := format.NormalizeVariant(key)
		if microarch.Rank(arch, level) < 0 {
			return nil, fmt.Errorf("invalid --variant-compression level %q for architecture %s", key, arch)
		}
		if !available[level] {
			return nil, fmt.Errorf("--variant-compression level %q is not a supplied variant", key)
		}
		if _, exists := overrides[level]; exists {
			return nil, fmt.Errorf("duplicate --variant-compression level %q (normalizes to %q)", key, level)
		}
		compression, err := parseVariantCompressionSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("invalid --variant-compression %q: %w", item, err)
		}
		overrides[level] = compression
	}
	return overrides, nil
}

func parseVariantCompressionSpec(spec string) (pack.VariantCompressionOptions, error) {
	algorithm, level := codec.ParseCompressionSpec(spec)
	if algorithm == "" || strings.Count(spec, ":") > 1 || (strings.Contains(spec, ":") && level == "") {
		return pack.VariantCompressionOptions{}, fmt.Errorf("expected codec[:level] with a nonempty codec and optional nonempty level")
	}
	if _, err := codec.Get(algorithm); err != nil {
		return pack.VariantCompressionOptions{}, err
	}
	var err error
	switch algorithm {
	case codec.AlgorithmZstd:
		_, err = codec.NewZstdCodec().MapLevel(level)
	case codec.AlgorithmLZ4:
		_, err = codec.NewLZ4Codec().MapLevel(level)
	case codec.AlgorithmNone:
		if level != "" {
			err = fmt.Errorf("codec none does not accept a compression level; use none")
		}
	default:
		err = fmt.Errorf("expected codec zstd, lz4, or none")
	}
	if err != nil {
		return pack.VariantCompressionOptions{}, err
	}
	return pack.VariantCompressionOptions{Compression: algorithm, Level: level}, nil
}
