package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func corpusFixture(t *testing.T) (options, manifest) {
	t.Helper()
	dir := t.TempDir()
	inputs := fixtureInputs()
	for i := range inputs {
		inputs[i].Path = filepath.Join(dir, inputs[i].Level)
		require.NoError(t, os.WriteFile(inputs[i].Path, inputs[i].data, 0o600))
	}
	corpus := manifest{SchemaVersion: 1, SourceRevision: "fixture-source", GoVersion: "go1.27.3", Variants: inputs}
	path := filepath.Join(dir, "manifest.json")
	data, err := json.Marshal(corpus)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return options{manifestPath: path, family: "test", arch: "amd64", stubPath: inputs[0].Path,
		output: filepath.Join(dir, "out"), sizes: "4096", level: "fastest"}, corpus
}

func cliArgs(opts options) []string {
	return []string{"--manifest", opts.manifestPath, "--family", opts.family, "--stub", opts.stubPath,
		"--output", opts.output, "--sizes", opts.sizes, "--level", opts.level, "--arch", opts.arch}
}

func TestCLIAndCorpus(t *testing.T) {
	opts, _ := corpusFixture(t)
	parsed, err := parseOptions(cliArgs(opts), io.Discard)
	require.NoError(t, err)
	assert.Equal(t, opts, parsed)
	for _, args := range [][]string{nil, {"--unknown"}, append(cliArgs(opts), "extra"), {"--level", "invalid"}} {
		_, err := parseOptions(args, io.Discard)
		require.Error(t, err)
	}
	args := cliArgs(opts)
	args = append(args, "--level", "invalid")
	_, err = parseOptions(args, io.Discard)
	require.Error(t, err)
	for _, sizes := range []string{"", "no", "1", "4096,4096", "99999999"} {
		_, err := parseSizes(sizes)
		require.Error(t, err)
	}
	sizes, err := parseSizes(defaultSizes)
	require.NoError(t, err)
	assert.Equal(t, []int{32768, 114688}, sizes)
	corpus, inputs, hash, err := loadCorpus(opts)
	require.NoError(t, err)
	assert.Equal(t, "fixture-source", corpus.SourceRevision)
	require.Len(t, inputs, 2)
	assert.Len(t, hash, 64)
	assert.Equal(t, "v1", inputs[0].Level)
	require.NoError(t, run(cliArgs(opts), io.Discard))
	data, err := os.ReadFile(filepath.Join(opts.output, "report.json"))
	require.NoError(t, err)
	var rep report
	require.NoError(t, json.Unmarshal(data, &rep))
	require.Len(t, rep.Results, 7)
	assert.NotEmpty(t, rep.Smallest)
	assert.Equal(t, hash, rep.ManifestSHA256)
	for _, result := range rep.Results {
		require.Empty(t, result.Error)
		artifact, err := os.ReadFile(filepath.Join(opts.output, result.ID+".fat"))
		require.NoError(t, err)
		assert.Equal(t, result.ArtifactSHA256, digest(artifact))
	}
	require.Error(t, run(cliArgs(opts), io.Discard), "existing output must not be overwritten")
	assert.NotEmpty(t, codecVersion())
}

func TestMainSuccess(t *testing.T) {
	opts, corpus := corpusFixture(t)
	corpus.Variants = append(corpus.Variants, input{Family: "other-family", Path: "ignored"})
	data, err := json.Marshal(corpus)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(opts.manifestPath, data, privateFile))
	previous := os.Args
	os.Args = append([]string{"research161"}, cliArgs(opts)...)
	t.Cleanup(func() { os.Args = previous })
	main()
	_, err = os.Stat(filepath.Join(opts.output, "report.json"))
	require.NoError(t, err)
}

func TestManifestFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*manifest)
	}{
		{"schema", func(m *manifest) { m.SchemaVersion = 2 }},
		{"go version", func(m *manifest) { m.GoVersion = "go1.26.0" }},
		{"revision", func(m *manifest) { m.SourceRevision = "" }},
		{"no family", func(m *manifest) { m.Variants = nil }},
		{"missing input", func(m *manifest) { m.Variants[0].Path += "-missing" }},
		{"duplicate", func(m *manifest) { m.Variants[1].Level = "AMD64_V1" }},
		{"unknown level", func(m *manifest) { m.Variants[1].Level = "v0" }},
		{"empty level", func(m *manifest) { m.Variants[1].Level = "" }},
		{"hash", func(m *manifest) { m.Variants[0].SHA256 = "wrong" }},
		{"bytes", func(m *manifest) { m.Variants[0].Bytes++ }},
		{"invalid elf", func(m *manifest) {
			m.Variants[0].data = []byte("invalid")
			m.Variants[0].Bytes = len(m.Variants[0].data)
			m.Variants[0].SHA256 = digest(m.Variants[0].data)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts, corpus := corpusFixture(t)
			tc.mutate(&corpus)
			for _, item := range corpus.Variants {
				if item.data != nil && item.Path != "" && tc.name == "invalid elf" {
					require.NoError(t, os.WriteFile(item.Path, item.data, 0o600))
				}
			}
			data, err := json.Marshal(corpus)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(opts.manifestPath, data, 0o600))
			_, _, _, err = loadCorpus(opts)
			require.Error(t, err)
		})
	}
	for _, content := range []string{"invalid json", "{}"} {
		opts, _ := corpusFixture(t)
		require.NoError(t, os.WriteFile(opts.manifestPath, []byte(content), 0o600))
		_, _, _, err := loadCorpus(opts)
		require.Error(t, err)
	}
	opts, _ := corpusFixture(t)
	opts.manifestPath += "missing"
	_, _, _, err := loadCorpus(opts)
	require.Error(t, err)
	_, err = readBounded(t.TempDir())
	require.Error(t, err)
	_, err = readBounded(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
	large := filepath.Join(t.TempDir(), "large")
	f, err := os.Create(large)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(maxInputBytes+1))
	require.NoError(t, f.Close())
	_, err = readBounded(large)
	require.Error(t, err)
}

func TestRunFailures(t *testing.T) {
	for _, change := range []func(*options){
		func(o *options) { o.sizes = "bad" },
		func(o *options) { o.manifestPath += "missing" },
		func(o *options) { o.stubPath += "missing" },
		func(o *options) { o.stubPath = o.manifestPath },
		func(o *options) { o.output = filepath.Join(o.output, "missing", "out") },
	} {
		opts, _ := corpusFixture(t)
		change(&opts)
		require.Error(t, run(cliArgs(opts), io.Discard))
	}
	require.Error(t, run(nil, io.Discard))
}
