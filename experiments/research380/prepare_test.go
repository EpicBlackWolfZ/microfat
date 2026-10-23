package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareFixture(t *testing.T) (prepareRequest, corpusManifest) {
	t.Helper()
	directory := t.TempDir()
	manifest := corpusManifest{SchemaVersion: 2, SourceRevision: "recipe-source", GoVersion: "go1.27.3", Architecture: "amd64",
		Sources: []corpusSource{{Family: testKnownFamily, SourceCommit: "source-pin", Args: []string{testVersionArgument}}}}
	in := prepareRequest{Manifest: filepath.Join(directory, "manifest.json"), Tool: filepath.Join(directory, "tool"),
		Stub: filepath.Join(directory, "stub"), Families: []string{testKnownFamily}, Profile: "stripped", Level: "v1"}
	for _, path := range []string{in.Tool, in.Stub} {
		require.NoError(t, os.WriteFile(path, []byte("known-source"), privateDirMode))
	}
	in.ToolSHA256, _ = hashFile(in.Tool)
	in.StubSHA256, _ = hashFile(in.Stub)
	for _, level := range []string{"v1", "v2", "v3"} {
		path := filepath.Join(directory, level)
		require.NoError(t, os.WriteFile(path, []byte("ELF"), privateDirMode))
		digest, err := hashFile(path)
		require.NoError(t, err)
		manifest.Variants = append(manifest.Variants, corpusVariant{Family: testKnownFamily, Profile: "stripped",
			Level: level, Path: path, SHA256: digest, Bytes: 3})
	}
	writeManifest(t, &in, manifest)
	return in, manifest
}

func writeManifest(t *testing.T, in *prepareRequest, manifest corpusManifest) {
	t.Helper()
	require.NoError(t, writeJSON(in.Manifest, manifest))
	digest, err := hashFile(in.Manifest)
	require.NoError(t, err)
	in.ManifestSHA256 = digest
}

func preparationRunner(t *testing.T) processRunner {
	t.Helper()
	return func(ctx context.Context, binary string, args, env []string) processResult {
		if args[0] == "pack" {
			for index, arg := range args {
				if arg == testOutputFlag {
					require.NoError(t, os.WriteFile(args[index+1], []byte("fat artifact"), privateDirMode))
				}
			}
		}
		return processResult{Stats: processStats{StdoutSHA256: textSHA("ready")}}
	}
}

func TestPreparationPreservesCorpusRecipeAndExactInputs(t *testing.T) {
	t.Parallel()
	in, manifest := prepareFixture(t)
	directory := filepath.Join(t.TempDir(), "artifacts")
	result, err := prepareArtifacts(context.Background(), in, directory, preparationRunner(t))
	require.NoError(t, err)
	require.Len(t, result.Applications, 1)
	app := result.Applications[0]
	assert.Equal(t, manifest.Sources, result.Manifest.Sources)
	assert.Equal(t, "source-pin", result.Manifest.Sources[0].SourceCommit)
	assert.Equal(t, textSHA("ELF"), app.PayloadSHA256)
	assert.Equal(t, int64(3), app.PayloadBytes)
	assert.Equal(t, textSHA("fat artifact"), app.ArtifactSHA256)
	assert.Equal(t, textSHA("ready"), app.StdoutSHA256)
	require.Len(t, result.Commands, 3)
	assert.Contains(t, result.Commands[0], "zstd")
	assert.Contains(t, result.Commands[0], "--format-version")
	_, err = os.Stat(filepath.Join(directory, "artifacts.json"))
	require.NoError(t, err)
	_, err = prepareArtifacts(context.Background(), in, directory, preparationRunner(t))
	require.Error(t, err)
}

func TestPreparationRejectsBadIdentitiesAndManifest(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*prepareRequest, *corpusManifest){
		"no families":        func(in *prepareRequest, _ *corpusManifest) { in.Families = nil },
		"bad tool digest":    func(in *prepareRequest, _ *corpusManifest) { in.ToolSHA256 = "invalid" },
		"identity changed":   func(in *prepareRequest, _ *corpusManifest) { in.ToolSHA256 = strings.Repeat("b", 64) },
		"missing source":     func(in *prepareRequest, _ *corpusManifest) { in.Stub = "/missing" },
		"schema":             func(_ *prepareRequest, m *corpusManifest) { m.SchemaVersion = 1 },
		"Go pin":             func(_ *prepareRequest, m *corpusManifest) { m.GoVersion = "go1.26" },
		"repeated family":    func(in *prepareRequest, _ *corpusManifest) { in.Families = []string{testKnownFamily, testKnownFamily} },
		"no args":            func(_ *prepareRequest, m *corpusManifest) { m.Sources[0].Args = nil },
		"bad variant":        func(_ *prepareRequest, m *corpusManifest) { m.Variants[0].Path = "relative" },
		"repeated variant":   func(_ *prepareRequest, m *corpusManifest) { m.Variants = append(m.Variants, m.Variants[0]) },
		"variant digest":     func(_ *prepareRequest, m *corpusManifest) { m.Variants[0].SHA256 = strings.Repeat("b", 64) },
		"missing variant":    func(_ *prepareRequest, m *corpusManifest) { m.Variants[0].Path = "/missing" },
		"selected absent":    func(in *prepareRequest, _ *corpusManifest) { in.Level = "v4" },
		"irrelevant variant": func(_ *prepareRequest, m *corpusManifest) { m.Variants[0].Family = testOther },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in, m := prepareFixture(t)
			change(&in, &m)
			writeManifest(t, &in, m)
			_, err := prepareArtifacts(context.Background(), in, filepath.Join(t.TempDir(), "artifacts"), preparationRunner(t))
			require.Error(t, err)
		})
	}
	in, _ := prepareFixture(t)
	require.NoError(t, os.WriteFile(in.Manifest, []byte("{"), privateFileMode))
	in.ManifestSHA256, _ = hashFile(in.Manifest)
	_, err := prepareArtifacts(context.Background(), in, filepath.Join(t.TempDir(), "bad-json"), preparationRunner(t))
	require.Error(t, err)
}

func TestPreparationRetainsFailedCommandAndDoesNotAssumeOutput(t *testing.T) {
	t.Parallel()
	in, m := prepareFixture(t)
	failed := func(context.Context, string, []string, []string) processResult {
		return processResult{Stats: processStats{ExitCode: 1}}
	}
	_, commands, stats, err := prepareFamily(context.Background(), in, m, testKnownFamily, t.TempDir(), failed)
	require.Error(t, err)
	assert.Len(t, commands, 3)
	assert.Len(t, stats, 1)
	_, _, _, err = prepareFamily(context.Background(), in, m, testKnownFamily, t.TempDir(), successfulRunner)
	require.Error(t, err)
}

func TestTrainingCorpusUsesExplicitDemoAndCLIContracts(t *testing.T) {
	t.Parallel()
	for _, family := range []string{"demo", "microfat"} {
		t.Run(family, func(t *testing.T) {
			t.Parallel()
			in, m := prepareFixture(t)
			in.Families = []string{family}
			in.Profile = "training"
			m.SchemaVersion = 1
			m.Sources = nil
			for index := range m.Variants {
				m.Variants[index].Family = family
				m.Variants[index].Profile = ""
			}
			writeManifest(t, &in, m)
			result, err := prepareArtifacts(context.Background(), in, filepath.Join(t.TempDir(), "training"), preparationRunner(t))
			require.NoError(t, err)
			require.Len(t, result.Applications, 1)
			if family == "demo" {
				assert.Equal(t, []string{"--startup-only", "--json"}, result.Applications[0].Args)
			} else {
				assert.Equal(t, []string{testVersionArgument}, result.Applications[0].Args)
			}
		})
	}
}
