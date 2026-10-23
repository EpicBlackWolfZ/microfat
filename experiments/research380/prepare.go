package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	json "encoding/json/v2"
)

type prepareRequest struct {
	Manifest       string   `json:"manifest"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	Tool           string   `json:"microfat"`
	ToolSHA256     string   `json:"microfat_sha256"`
	Stub           string   `json:"stub"`
	StubSHA256     string   `json:"stub_sha256"`
	Families       []string `json:"families"`
	Profile        string   `json:"profile"`
	Level          string   `json:"selected_level"`
}

type corpusManifest struct {
	SchemaVersion  int             `json:"schema_version"`
	SourceRevision string          `json:"source_revision"`
	GoVersion      string          `json:"go_version"`
	Architecture   string          `json:"architecture"`
	Sources        []corpusSource  `json:"sources"`
	Build          any             `json:"build"`
	Variants       []corpusVariant `json:"variants"`
}
type corpusSource struct {
	Family       string   `json:"family"`
	Module       string   `json:"module"`
	Version      string   `json:"version"`
	Package      string   `json:"package"`
	ModuleSum    string   `json:"module_sum"`
	GoModSum     string   `json:"go_mod_sum"`
	SourceCommit string   `json:"source_commit"`
	Args         []string `json:"readiness_args"`
}
type corpusVariant struct {
	Family  string `json:"family"`
	Profile string `json:"profile"`
	Level   string `json:"level"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
}
type preparedArtifacts struct {
	Request                prepareRequest `json:"request"`
	Manifest               corpusManifest `json:"corpus_manifest"`
	Applications           []application  `json:"applications"`
	Commands               [][]string     `json:"commands"`
	DiagnosticProcessStats []processStats `json:"diagnostic_process_stats"`
}

func prepareArtifacts(ctx context.Context, in prepareRequest, directory string, runner processRunner) (preparedArtifacts, error) {
	result := preparedArtifacts{Request: in}
	if len(in.Families) < 1 || len(in.Families) > maximumApps || in.Profile == "" || in.Level == "" ||
		!filepath.IsAbs(in.Tool) || !filepath.IsAbs(in.Stub) || !validSHA(in.ToolSHA256) || !validSHA(in.StubSHA256) ||
		!validSHA(in.ManifestSHA256) {
		return result, errors.New("declare bounded preparation families, profile, selected level and absolute tool/stub identities")
	}
	for path, want := range map[string]string{in.Manifest: in.ManifestSHA256, in.Tool: in.ToolSHA256, in.Stub: in.StubSHA256} {
		got, err := hashFile(path)
		if err != nil {
			return result, err
		}
		if got != want {
			return result, fmt.Errorf("preparation identity mismatch: %s", path)
		}
	}
	data, err := readJSON(in.Manifest)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(data, &result.Manifest, json.RejectUnknownMembers(true)); err != nil {
		return result, err
	}
	if !validCorpus(in, result.Manifest) {
		return result, errors.New("corpus must declare schema2, recipe revision and Go1.27.3")
	}
	if err := os.Mkdir(directory, privateDirMode); err != nil {
		return result, err
	}
	seen := make(map[string]bool)
	for _, family := range in.Families {
		if family == "" || seen[family] {
			return result, errors.New("preparation families must be nonempty and unique")
		}
		seen[family] = true
		app, commands, stats, err := prepareFamily(ctx, in, result.Manifest, family, directory, runner)
		if err != nil {
			return result, err
		}
		result.Applications = append(result.Applications, app)
		result.Commands = append(result.Commands, commands...)
		result.DiagnosticProcessStats = append(result.DiagnosticProcessStats, stats...)
	}
	return result, writeJSON(filepath.Join(directory, "artifacts.json"), result)
}

func validCorpus(in prepareRequest, manifest corpusManifest) bool {
	training := manifest.SchemaVersion == 1 && in.Profile == "training"
	for _, family := range in.Families {
		training = training && (family == "demo" || family == "microfat")
	}
	return (manifest.SchemaVersion == 2 || training) && manifest.SourceRevision != "" && manifest.GoVersion == "go1.27.3"
}

func prepareFamily(ctx context.Context, in prepareRequest, manifest corpusManifest, family, directory string,
	runner processRunner) (application, [][]string, []processStats, error) {
	app := application{Family: family, Level: in.Level}
	// Use a digest of the family rather than an untrusted family string as a path.
	app.Artifact = filepath.Join(directory, "artifact-"+textSHA(family))
	app.Args = familyReadiness(in, manifest, family)
	if len(app.Args) == 0 || len(app.Args) > maximumArguments {
		return app, nil, nil, errors.New("missing or excessive readiness arguments")
	}
	args := []string{"pack", "--stub", in.Stub, "--output", app.Artifact, "--compression", "zstd", "--format-version", "2"}
	selected := ""
	seen := make(map[string]bool)
	for _, variant := range manifest.Variants {
		profileMatches := variant.Profile == in.Profile || (manifest.SchemaVersion == 1 && in.Profile == "training" && variant.Profile == "")
		if variant.Family != family || !profileMatches {
			continue
		}
		if !filepath.IsAbs(variant.Path) || !validSHA(variant.SHA256) || variant.Bytes < 1 ||
			variant.Bytes > maximumExecutable || seen[variant.Level] {
			return app, nil, nil, errors.New("invalid, repeated or oversized corpus variant")
		}
		seen[variant.Level] = true
		actual, err := hashFile(variant.Path)
		if err != nil {
			return app, nil, nil, err
		}
		if actual != variant.SHA256 {
			return app, nil, nil, errors.New("corpus variant digest mismatch")
		}
		args = append(args, "--variant", variant.Level+"="+variant.Path)
		if variant.Level == in.Level {
			selected = variant.Path
			app.PayloadSHA256, app.PayloadBytes = variant.SHA256, variant.Bytes
		}
	}
	if selected == "" {
		return app, nil, nil, errors.New("selected variant absent from corpus")
	}
	commands := [][]string{{in.Tool}, {in.Tool, "verify", app.Artifact}, {selected}}
	commands[0] = append(commands[0], args...)
	commands[2] = append(commands[2], app.Args...)
	stats := make([]processStats, 0, len(commands))
	for _, command := range commands {
		measured := invoke(ctx, int64(maximumTimeout), runner, command[0], command[1:], processEnvironment("", in.Level, 1))
		stats = append(stats, measured.Stats)
		if measured.Stats.ExitCode != 0 || measured.Stats.Error != "" {
			return app, commands, stats, errors.New("artifact preparation/verification/native command failed")
		}
	}
	app.StdoutSHA256 = stats[len(stats)-1].StdoutSHA256
	digest, err := hashFile(app.Artifact)
	app.ArtifactSHA256 = digest
	return app, commands, stats, err
}

func familyReadiness(in prepareRequest, manifest corpusManifest, family string) []string {
	if manifest.SchemaVersion == 1 && in.Profile == "training" {
		switch family {
		case "demo":
			return []string{"--startup-only", "--json"}
		case "microfat":
			return []string{"--version"}
		}
	}
	for _, source := range manifest.Sources {
		if source.Family == family {
			return source.Args
		}
	}
	return nil
}
