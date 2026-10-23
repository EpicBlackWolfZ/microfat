package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const fixtureRevision = "1234567890abcdef1234567890abcdef12345678"

func fixtureCorpus(t *testing.T) (corpusManifest, string, string) {
	t.Helper()
	root := t.TempDir()
	inputs, levels := fixtureInputs()
	m := corpusManifest{SchemaVersion: 2, SourceRevision: fixtureRevision, GoVersion: goVersion, Architecture: "amd64",
		Build: corpusBuild{CGOEnabled: "0", GOOS: "linux", Trimpath: true}}
	for _, family := range []string{"alpha", "beta", "gamma", "delta"} {
		m.Sources = append(m.Sources, corpusSource{Family: family, Module: "example.org/" + family, Version: "v1.0.0",
			Package: ".", ModuleSum: "h1:fixture", GoModSum: "h1:fixture", SourceCommit: fixtureRevision, ReadinessArgs: []string{}})
		for _, profile := range []string{stripped, unstripped} {
			for i, level := range levels {
				path := filepath.Join(root, family+"-"+profile+"-"+level)
				require.NoError(t, os.WriteFile(path, inputs[i], privateFileMode))
				m.Variants = append(m.Variants, corpusVariant{family, profile, level, path, digest(inputs[i]), int64(len(inputs[i]))})
			}
		}
	}
	manifest := filepath.Join(root, "manifest.json")
	writeFixtureJSON(t, manifest, m)
	stub := filepath.Join(root, "stub")
	require.NoError(t, os.WriteFile(stub, fixtureELF([]byte("stub")), privateFileMode))
	return m, manifest, stub
}

func writeFixtureJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, privateFileMode))
}

func cloneManifest(t *testing.T, m corpusManifest) corpusManifest {
	t.Helper()
	data, err := json.Marshal(m)
	require.NoError(t, err)
	var result corpusManifest
	require.NoError(t, json.Unmarshal(data, &result))
	return result
}

func TestCorpusManifestValidation(t *testing.T) {
	t.Parallel()
	m, path, _ := fixtureCorpus(t)
	got, groups, checksum, err := loadManifest(path)
	require.NoError(t, err)
	require.Equal(t, m, got)
	require.Len(t, groups, maxFamilies*maxProfiles)
	require.Len(t, checksum, 2*sha256Bytes)
	inputs, levels, err := readGroup(groups[0], m.Architecture)
	require.NoError(t, err)
	require.Len(t, inputs, maxVariants)
	require.Equal(t, []string{"v1", "v2", "v3"}, levels)
	for _, tc := range []struct {
		name string
		edit func(*corpusManifest)
	}{
		{"schema", func(m *corpusManifest) { m.SchemaVersion++ }},
		{"toolchain", func(m *corpusManifest) { m.GoVersion = "go1.26" }},
		{"source", func(m *corpusManifest) { m.SourceRevision = "bad" }},
		{"arch", func(m *corpusManifest) { m.Architecture = "riscv64" }},
		{"cgo", func(m *corpusManifest) { m.Build.CGOEnabled = "1" }},
		{"trim", func(m *corpusManifest) { m.Build.Trimpath = false }},
		{"source-count", func(m *corpusManifest) { m.Sources = m.Sources[:1] }},
		{"source-duplicate", func(m *corpusManifest) { m.Sources[1] = m.Sources[0] }},
		{"source-name", func(m *corpusManifest) { m.Sources[0].Family = "../escape" }},
		{"source-pin", func(m *corpusManifest) { m.Sources[0].ModuleSum = "" }},
		{"family", func(m *corpusManifest) { m.Variants[0].Family = "absent" }},
		{"profile", func(m *corpusManifest) { m.Variants[0].Profile = "other" }},
		{"path", func(m *corpusManifest) { m.Variants[0].Path = "relative" }},
		{"hash", func(m *corpusManifest) { m.Variants[0].SHA256 = strings.Repeat("A", 2*sha256Bytes) }},
		{"size", func(m *corpusManifest) { m.Variants[0].Bytes = maxInput + 1 }},
		{"zero", func(m *corpusManifest) { m.Variants[0].Bytes = 0 }},
		{"aggregate", func(m *corpusManifest) {
			for i := range m.Variants {
				m.Variants[i].Bytes = maxInput
			}
		}},
		{"groups", func(m *corpusManifest) {
			for i := range m.Variants {
				m.Variants[i].Profile = stripped
			}
		}},
		{"tier-count", func(m *corpusManifest) { m.Variants[0].Profile = unstripped }},
		{"tier-duplicate", func(m *corpusManifest) { m.Variants[0].Level = m.Variants[1].Level }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := cloneManifest(t, m)
			tc.edit(&bad)
			_, err := validateManifest(bad)
			require.Error(t, err)
		})
	}
	arm := cloneManifest(t, m)
	arm.Architecture = "arm64"
	armLevels := []string{"v8.0", "v8.1", "v8.2"}
	for i := range arm.Variants {
		arm.Variants[i].Level = armLevels[i%maxVariants]
	}
	_, err = validateManifest(arm)
	require.NoError(t, err)
}

func TestCorpusRejectsMutationMalformedJSONAndNonregular(t *testing.T) {
	t.Parallel()
	m, path, stub := fixtureCorpus(t)
	_, _, _, err := loadManifest(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
	for _, raw := range []string{`{`, `{"schema_version":2,"unknown":true}`, `{"schema_version":2,"schema_version":2}`} {
		require.NoError(t, os.WriteFile(path, []byte(raw), privateFileMode))
		_, _, _, err = loadManifest(path)
		require.Error(t, err)
	}
	groups, err := validateManifest(m)
	require.NoError(t, err)
	g := groups[0]
	data, err := os.ReadFile(g.variants[0].Path)
	require.NoError(t, err)
	data[len(data)-1] ^= 1
	require.NoError(t, os.WriteFile(g.variants[0].Path, data, privateFileMode))
	_, _, err = readGroup(g, m.Architecture)
	require.ErrorContains(t, err, "identity changed")
	g.variants[0].Path = filepath.Join(t.TempDir(), "missing")
	_, _, err = readGroup(g, m.Architecture)
	require.Error(t, err)
	g.variants[0].Path, g.variants[0].SHA256, g.variants[0].Bytes = stub, digest([]byte("bad")), 3
	require.NoError(t, os.WriteFile(stub, []byte("bad"), privateFileMode))
	_, _, err = readGroup(g, m.Architecture)
	require.Error(t, err)
	_, err = readBounded(t.TempDir(), maxManifest)
	require.Error(t, err)
	_, err = readBounded(path, 1)
	require.Error(t, err)
	require.NoError(t, validateELF(fixtureELF([]byte{1}), "amd64"))
	require.Error(t, validateELF([]byte{1}, "amd64"))
	require.Error(t, validateELF(fixtureELF([]byte{1}), "arm64"))
	arm := fixtureELF([]byte{1})
	const armMachine = 183
	binary.LittleEndian.PutUint16(arm[18:], armMachine)
	require.NoError(t, validateELF(arm, "arm64"))
	newPath := filepath.Join(t.TempDir(), "new")
	require.NoError(t, writeNew(newPath, []byte{1}))
	require.Error(t, writeNew(newPath, []byte{2}))
	retained, err := os.ReadFile(newPath)
	require.NoError(t, err)
	require.True(t, bytes.Equal([]byte{1}, retained))
}
