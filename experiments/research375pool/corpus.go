package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

const (
	maxVariants         = 8
	gitRevisionLength   = 40
	maxPortableVariants = 32
	maxPortableInput    = 96 * mib
	maxPortableCorpus   = 1024 * mib
)

var safeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

type corpusVariant struct {
	Family  string `json:"family"`
	Profile string `json:"profile,omitempty"`
	Level   string `json:"level"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
}

type corpusSource struct {
	Family        string   `json:"family"`
	Module        string   `json:"module"`
	Version       string   `json:"version"`
	Package       string   `json:"package"`
	ModuleSum     string   `json:"module_sum"`
	GoModSum      string   `json:"go_mod_sum"`
	SourceCommit  string   `json:"source_commit"`
	ReadinessArgs []string `json:"readiness_args"`
}

type corpusBuild struct {
	CGOEnabled string `json:"cgo_enabled"`
	GOOS       string `json:"goos"`
	Trimpath   bool   `json:"trimpath"`
	BuildVCS   bool   `json:"buildvcs"`
}

type corpusManifest struct {
	SchemaVersion  int             `json:"schema_version"`
	SourceRevision string          `json:"source_revision"`
	GoVersion      string          `json:"go_version"`
	Architecture   string          `json:"architecture,omitempty"`
	Sources        []corpusSource  `json:"sources,omitempty"`
	Build          *corpusBuild    `json:"build,omitempty"`
	Variants       []corpusVariant `json:"variants"`
}

func digestFile(path string, limit int64) (string, int64, error) {
	f, err := inputfile.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	s, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if s.Size() <= 0 || s.Size() > limit {
		return "", 0, errors.New("file outside size bound")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, limit+1))
	if err != nil {
		return "", 0, err
	}
	if n != s.Size() || n > limit {
		return "", 0, errors.New("input changed or exceeds bound")
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func validRevision(s string) bool {
	if len(s) != gitRevisionLength {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func buildManifest(dir, source string) (corpusManifest, error) {
	return buildManifestForArch(dir, source, microarch.ArchAMD64)
}

func buildManifestForArch(dir, source, arch string) (corpusManifest, error) {
	m := corpusManifest{SchemaVersion: 1, SourceRevision: source, GoVersion: runtime.Version()}
	if dir == "" || !validRevision(source) || m.GoVersion != researchGoVersion || (arch != "amd64" && arch != "arm64") {
		return m, errors.New("corpus directory, exact revision and Go 1.27.3 required")
	}
	levels := []string{"v1", "v2", "v3"}
	if arch == microarch.ArchARM64 {
		m.Architecture = arch
		levels = []string{"v8.0", "v8.1", "v8.2"}
	}
	for _, family := range []string{familyDemo, familyMicrofat} {
		for _, level := range levels {
			path, err := filepath.Abs(filepath.Join(dir, family+"-"+level))
			if err != nil {
				return m, err
			}
			h, n, err := digestFile(path, maxInput)
			if err != nil {
				return m, err
			}
			m.Variants = append(m.Variants, corpusVariant{Family: family, Level: level, Path: path, SHA256: h, Bytes: n})
		}
	}
	path := filepath.Join(dir, "manifest.json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return m, errors.New("manifest output already exists or cannot be inspected")
	}
	return m, writeJSON(path, m)
}

func loadCorpus(path string) (corpusManifest, []string, error) {
	var m corpusManifest
	if err := readJSON(path, &m); err != nil {
		return m, nil, err
	}
	variantLimit, inputLimit, totalLimit := maxVariants, maxInput, maxCorpus
	if m.SchemaVersion == portableCorpusSchema {
		variantLimit, inputLimit, totalLimit = maxPortableVariants, maxPortableInput, maxPortableCorpus
		if err := validatePortableCorpus(m); err != nil {
			return m, nil, err
		}
	}
	if (m.SchemaVersion != 1 && m.SchemaVersion != portableCorpusSchema) ||
		m.GoVersion != researchGoVersion || !validRevision(m.SourceRevision) ||
		len(m.Variants) == 0 || len(m.Variants) > variantLimit {
		return m, nil, errors.New("invalid corpus schema, revision, toolchain or count")
	}
	seen := map[string]bool{}
	var total int64
	families := []string{}
	for i := range m.Variants {
		v := &m.Variants[i]
		key := v.Family + "/" + v.Profile + "/" + v.Level
		if !safeName.MatchString(v.Family) || !validCorpusLevel(corpusArchitecture(m), v.Level) || seen[key] {
			return m, nil, errors.New("invalid or duplicate family/level")
		}
		seen[key] = true
		if !filepath.IsAbs(v.Path) {
			return m, nil, errors.New("corpus paths must be absolute")
		}
		h, n, err := digestFile(v.Path, inputLimit)
		if err != nil {
			return m, nil, err
		}
		if n != v.Bytes || h != v.SHA256 {
			return m, nil, fmt.Errorf("corpus integrity mismatch: %s", key)
		}
		total += n
		if !slices.Contains(families, v.Family) {
			families = append(families, v.Family)
		}
	}
	if total > totalLimit {
		return m, nil, errors.New("corpus total exceeds bound")
	}
	slices.Sort(families)
	return m, families, nil
}

func corpusArchitecture(m corpusManifest) string {
	if m.Architecture == "" {
		return microarch.ArchAMD64
	}
	return m.Architecture
}

func validCorpusLevel(arch, level string) bool {
	if arch == microarch.ArchAMD64 {
		return slices.Contains([]string{"v1", "v2", "v3"}, level)
	}
	return arch == microarch.ArchARM64 && slices.Contains([]string{"v8.0", "v8.1", "v8.2"}, level)
}

func validatePortableCorpus(m corpusManifest) error {
	if (m.Architecture != microarch.ArchAMD64 && m.Architecture != microarch.ArchARM64) || m.Build == nil ||
		m.Build.CGOEnabled != "0" || m.Build.GOOS != "linux" || !m.Build.Trimpath || m.Build.BuildVCS || len(m.Sources) == 0 {
		return errors.New("invalid portable corpus architecture or reproducible build metadata")
	}
	seen := map[string]bool{}
	for _, source := range m.Sources {
		args, err := nativeFixtureArgs(source.Family)
		if err != nil || seen[source.Family] || source.Module == "" || source.Version == "" || source.Package == "" ||
			!validRevision(source.SourceCommit) || !strings.HasPrefix(source.ModuleSum, "h1:") ||
			!strings.HasPrefix(source.GoModSum, "h1:") || !slices.Equal(args, source.ReadinessArgs) {
			return errors.New("invalid portable corpus source pin or readiness command")
		}
		seen[source.Family] = true
	}
	for _, variant := range m.Variants {
		if !seen[variant.Family] || (variant.Profile != profileStripped && variant.Profile != profileUnstripped) {
			return errors.New("portable variant lacks source pin or known profile")
		}
	}
	return nil
}

func corpusProfile(m corpusManifest, profile string) corpusManifest {
	out := m
	out.Variants = nil
	for _, v := range m.Variants {
		if v.Profile == profile || (v.Profile == "" && profile == profileStripped) {
			out.Variants = append(out.Variants, v)
		}
	}
	return out
}

func familyVariants(m corpusManifest, family string) map[string]string {
	paths := map[string]string{}
	for _, v := range m.Variants {
		if v.Family == family {
			paths[v.Level] = v.Path
		}
	}
	return paths
}
