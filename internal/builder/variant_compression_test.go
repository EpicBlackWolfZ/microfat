package builder

import (
	"maps"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
)

const (
	variantCompressionBest     = "best"
	variantCompressionFastest  = "fastest"
	variantCompressionDefault  = "default"
	variantCompressionARMLevel = "v8.2"
)

func TestAssemblePackOptionsVariantCompression(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		manifestLevel string
		overrides     map[string]pack.VariantCompressionOptions
		want          pack.VariantCompressionOptions
	}{
		{
			name: "manifest fields retained when CLI omitted", manifestLevel: "v1",
			want: pack.VariantCompressionOptions{
				Profile: codec.ProfileSize, Compression: codec.AlgorithmZstd, Level: variantCompressionBest,
			},
		},
		{
			name: "CLI codec retains manifest profile and level", manifestLevel: "v1",
			overrides: map[string]pack.VariantCompressionOptions{"v1": {Compression: codec.AlgorithmLZ4}},
			want: pack.VariantCompressionOptions{
				Profile: codec.ProfileSize, Compression: codec.AlgorithmLZ4, Level: variantCompressionBest,
			},
		},
		{
			name: "CLI inline level outranks inherited separate level", manifestLevel: "v1",
			overrides: map[string]pack.VariantCompressionOptions{"v1": {Compression: "zstd:fastest"}},
			want: pack.VariantCompressionOptions{
				Profile: codec.ProfileSize, Compression: "zstd:fastest", Level: variantCompressionFastest,
			},
		},
		{
			name: "CLI separate level outranks own inline level", manifestLevel: "v1",
			overrides: map[string]pack.VariantCompressionOptions{
				"v1": {Compression: "zstd:fastest", Level: variantCompressionDefault},
			},
			want: pack.VariantCompressionOptions{
				Profile: codec.ProfileSize, Compression: "zstd:fastest", Level: variantCompressionDefault,
			},
		},
		{
			name: "CLI profile retains manifest codec and level", manifestLevel: "v1",
			overrides: map[string]pack.VariantCompressionOptions{"v1": {Profile: codec.ProfileLatency}},
			want: pack.VariantCompressionOptions{
				Profile: codec.ProfileLatency, Compression: codec.AlgorithmZstd, Level: variantCompressionBest,
			},
		},
		{
			name: "CLI level retains manifest codec and profile", manifestLevel: "v1",
			overrides: map[string]pack.VariantCompressionOptions{"v1": {Level: variantCompressionFastest}},
			want: pack.VariantCompressionOptions{
				Profile: codec.ProfileSize, Compression: codec.AlgorithmZstd, Level: variantCompressionFastest,
			},
		},
		{
			name: "CLI replaces each supplied field", manifestLevel: "v1",
			overrides: map[string]pack.VariantCompressionOptions{
				"v1": {Profile: codec.ProfileBalanced, Compression: codec.AlgorithmLZ4, Level: variantCompressionFastest},
			},
			want: pack.VariantCompressionOptions{
				Profile: codec.ProfileBalanced, Compression: codec.AlgorithmLZ4, Level: variantCompressionFastest,
			},
		},
		{
			name: "CLI alias merges with canonical manifest", manifestLevel: "v1",
			overrides: map[string]pack.VariantCompressionOptions{" AMD64_V1 ": {Compression: codec.AlgorithmLZ4}},
			want: pack.VariantCompressionOptions{
				Profile: codec.ProfileSize, Compression: codec.AlgorithmLZ4, Level: variantCompressionBest,
			},
		},
		{
			name: "manifest alias merges with canonical CLI", manifestLevel: "amd64-v1",
			overrides: map[string]pack.VariantCompressionOptions{"v1": {Compression: codec.AlgorithmLZ4}},
			want: pack.VariantCompressionOptions{
				Profile: codec.ProfileSize, Compression: codec.AlgorithmLZ4, Level: variantCompressionBest,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &Manifest{Variants: []VariantConfig{
				{Level: tc.manifestLevel, Compression: &CompressionConfig{
					Profile: codec.ProfileSize, Algorithm: codec.AlgorithmZstd, Level: variantCompressionBest,
				}},
				{Level: "v3", Compression: &CompressionConfig{Algorithm: codec.AlgorithmNone}},
				{Level: "v4"},
			}}
			beforeManifest := cloneManifest(m)
			beforeOverrides := maps.Clone(tc.overrides)
			opts := assemblePackOptions(m, "stub", "out", "app", nil, BuildOptions{VariantCompression: tc.overrides})
			assert.Equal(t, map[string]pack.VariantCompressionOptions{
				"v1": tc.want,
				"v3": {Compression: codec.AlgorithmNone},
			}, opts.VariantCompression)
			assert.Equal(t, beforeManifest, m, "assembly must preserve caller-owned manifest fields and aliases")
			assert.Equal(t, beforeOverrides, tc.overrides, "assembly must preserve caller-owned overrides")
			delete(opts.VariantCompression, "v1")
			assert.Equal(t, beforeOverrides, tc.overrides, "assembled overrides must have independent map storage")
		})
	}
}

func TestAssemblePackOptionsVariantCompressionWithoutManifestSettings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		overrides map[string]pack.VariantCompressionOptions
		want      map[string]pack.VariantCompressionOptions
	}{
		{name: "no overrides leave map nil"},
		{
			name:      "CLI adds compression to unconfigured variant",
			overrides: map[string]pack.VariantCompressionOptions{"amd64_v1": {Compression: codec.AlgorithmNone}},
			want:      map[string]pack.VariantCompressionOptions{"v1": {Compression: codec.AlgorithmNone}},
		},
		{
			name:      "ARM aliases use the same merge path",
			overrides: map[string]pack.VariantCompressionOptions{"arm64-v8.2": {Profile: codec.ProfileLatency}},
			want:      map[string]pack.VariantCompressionOptions{variantCompressionARMLevel: {Profile: codec.ProfileLatency}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &Manifest{Variants: []VariantConfig{{Level: "v1"}, {Level: variantCompressionARMLevel}}}
			beforeOverrides := maps.Clone(tc.overrides)
			opts := assemblePackOptions(m, "stub", "out", "app", nil, BuildOptions{VariantCompression: tc.overrides})
			assert.Equal(t, tc.want, opts.VariantCompression)
			assert.Equal(t, beforeOverrides, tc.overrides)
		})
	}
}

func TestAssemblePackOptionsVariantCompressionPreservesGlobals(t *testing.T) {
	t.Parallel()
	const (
		manifestDictSize = 65536
		cliDictSize      = 32768
	)
	cases := []struct {
		name string
		root *CompressionConfig
		cli  BuildOptions
		want CompressionConfig
	}{
		{
			name: "omitted globals retain pack defaults",
			want: CompressionConfig{Profile: codec.ProfileBalanced},
		},
		{
			name: "manifest globals and dictionary survive variant overrides",
			root: &CompressionConfig{
				Profile: codec.ProfileSize, Algorithm: codec.AlgorithmZstd, Level: variantCompressionBest,
				EnableDict: true, DictSize: manifestDictSize,
			},
			want: CompressionConfig{
				Profile: codec.ProfileSize, Algorithm: codec.AlgorithmZstd, Level: variantCompressionBest,
				EnableDict: true, DictSize: manifestDictSize,
			},
		},
		{
			name: "explicit globals retain precedence over manifest globals",
			root: &CompressionConfig{
				Profile: codec.ProfileSize, Algorithm: codec.AlgorithmZstd, Level: variantCompressionBest,
				EnableDict: true, DictSize: manifestDictSize,
			},
			cli: BuildOptions{
				Profile: codec.ProfileLatency, Compression: "lz4:fastest", CompressionLevel: variantCompressionDefault,
				DictSize: cliDictSize,
			},
			want: CompressionConfig{
				Profile: codec.ProfileLatency, Algorithm: "lz4:fastest", Level: variantCompressionDefault,
				EnableDict: true, DictSize: cliDictSize,
			},
		},
		{
			name: "CLI inline global level overrides inherited global level",
			root: &CompressionConfig{Algorithm: codec.AlgorithmZstd, Level: variantCompressionBest},
			cli:  BuildOptions{Compression: "lz4:fastest", EnableDict: true, DictSize: cliDictSize},
			want: CompressionConfig{
				Profile: codec.ProfileBalanced, Algorithm: "lz4:fastest", Level: variantCompressionFastest,
				EnableDict: true, DictSize: cliDictSize,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &Manifest{Compression: tc.root, Variants: []VariantConfig{
				{Level: "v1", Compression: &CompressionConfig{Profile: codec.ProfileSize, Algorithm: codec.AlgorithmZstd}},
			}}
			beforeManifest := cloneManifest(m)
			tc.cli.VariantCompression = map[string]pack.VariantCompressionOptions{"v1": {Compression: codec.AlgorithmNone}}
			opts := assemblePackOptions(m, "stub", "out", "app", nil, tc.cli)
			assert.Equal(t, tc.want, CompressionConfig{
				Profile: opts.Profile, Algorithm: opts.Compression, Level: opts.CompressionLevel,
				EnableDict: opts.EnableDict, DictSize: opts.DictSize,
			})
			assert.Equal(t, map[string]pack.VariantCompressionOptions{
				"v1": {Profile: codec.ProfileSize, Compression: codec.AlgorithmNone},
			}, opts.VariantCompression, "global CLI options must not overwrite variant settings")
			assert.Equal(t, beforeManifest, m)
		})
	}
}
