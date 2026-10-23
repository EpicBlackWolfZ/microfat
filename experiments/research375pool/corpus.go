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

	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
)

const (
	maxVariants       = 8
	gitRevisionLength = 40
)

var safeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

type corpusVariant struct {
	Family string `json:"family"`
	Level  string `json:"level"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type corpusManifest struct {
	SchemaVersion  int             `json:"schema_version"`
	SourceRevision string          `json:"source_revision"`
	GoVersion      string          `json:"go_version"`
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
	m := corpusManifest{SchemaVersion: 1, SourceRevision: source, GoVersion: runtime.Version()}
	if dir == "" || !validRevision(source) || m.GoVersion != "go1.27.3" {
		return m, errors.New("corpus directory, exact revision and Go 1.27.3 required")
	}
	for _, family := range []string{"demo", "microfat"} {
		for _, level := range []string{"v1", "v2", "v3"} {
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
	if m.SchemaVersion != 1 || m.GoVersion != "go1.27.3" || !validRevision(m.SourceRevision) ||
		len(m.Variants) == 0 || len(m.Variants) > maxVariants {
		return m, nil, errors.New("invalid corpus schema, revision, toolchain or count")
	}
	seen := map[string]bool{}
	var total int64
	families := []string{}
	for i := range m.Variants {
		v := &m.Variants[i]
		key := v.Family + "/" + v.Level
		if !safeName.MatchString(v.Family) || !slices.Contains([]string{"v1", "v2", "v3"}, v.Level) || seen[key] {
			return m, nil, errors.New("invalid or duplicate family/level")
		}
		seen[key] = true
		if !filepath.IsAbs(v.Path) {
			return m, nil, errors.New("corpus paths must be absolute")
		}
		h, n, err := digestFile(v.Path, maxInput)
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
	if total > maxCorpus {
		return m, nil, errors.New("corpus total exceeds bound")
	}
	slices.Sort(families)
	return m, families, nil
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
