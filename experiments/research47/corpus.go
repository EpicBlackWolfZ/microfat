package main

import (
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

const (
	maxManifest = 4 * mib
	maxFamilies = 4
	maxProfiles = 2
	maxCorpus   = 1024 * mib
	goVersion   = "go1.27.3"
	stripped    = "stripped"
	unstripped  = "unstripped"
)

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

type corpusVariant struct {
	Family  string `json:"family"`
	Profile string `json:"profile"`
	Level   string `json:"level"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
}

type corpusManifest struct {
	SchemaVersion  int             `json:"schema_version"`
	SourceRevision string          `json:"source_revision"`
	GoVersion      string          `json:"go_version"`
	Architecture   string          `json:"architecture"`
	Sources        []corpusSource  `json:"sources"`
	Build          corpusBuild     `json:"build"`
	Variants       []corpusVariant `json:"variants"`
}

type group struct {
	family   string
	profile  string
	variants []corpusVariant
}

var safeName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func validHex(value string, size int) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == size && value == strings.ToLower(value)
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := inputfile.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if stat.Size() > limit {
		return nil, errors.New("input exceeds byte bound")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != stat.Size() || int64(len(data)) > limit {
		return nil, errors.New("input changed or exceeds byte bound")
	}
	return data, nil
}

func loadManifest(path string) (corpusManifest, []group, string, error) {
	data, err := readBounded(path, maxManifest)
	if err != nil {
		return corpusManifest{}, nil, "", err
	}
	var manifest corpusManifest
	if err = json.Unmarshal(data, &manifest, json.RejectUnknownMembers(true)); err != nil {
		return corpusManifest{}, nil, "", err
	}
	groups, err := validateManifest(manifest)
	return manifest, groups, digest(data), err
}

func validManifestHeader(m corpusManifest) bool {
	const revisionBytes = 20
	return m.SchemaVersion == 2 && m.GoVersion == goVersion && validHex(m.SourceRevision, revisionBytes) &&
		(m.Architecture == "amd64" || m.Architecture == "arm64") && m.Build.CGOEnabled == "0" &&
		m.Build.GOOS == "linux" && m.Build.Trimpath && !m.Build.BuildVCS && len(m.Sources) == maxFamilies &&
		len(m.Variants) == maxFamilies*maxProfiles*maxVariants
}

func validateSources(sources []corpusSource) (map[string]bool, error) {
	const revisionBytes = 20
	families := make(map[string]bool)
	for _, source := range sources {
		if !safeName.MatchString(source.Family) || families[source.Family] || source.Module == "" || source.Version == "" ||
			source.Package == "" || !strings.HasPrefix(source.ModuleSum, "h1:") || !strings.HasPrefix(source.GoModSum, "h1:") ||
			!validHex(source.SourceCommit, revisionBytes) {
			return nil, errors.New("invalid or duplicate pinned source")
		}
		families[source.Family] = true
	}
	return families, nil
}

func validateManifest(m corpusManifest) ([]group, error) {
	if !validManifestHeader(m) {
		return nil, errors.New("manifest protocol/toolchain/build/family count mismatch")
	}
	families, err := validateSources(m.Sources)
	if err != nil {
		return nil, err
	}
	byGroup := make(map[string]*group)
	levels := []string{"v1", "v2", "v3"}
	if m.Architecture == "arm64" {
		levels = []string{"v8.0", "v8.1", "v8.2"}
	}
	totalBytes := int64(0)
	for _, variant := range m.Variants {
		if !families[variant.Family] || (variant.Profile != stripped && variant.Profile != unstripped) ||
			!filepath.IsAbs(variant.Path) || !validHex(variant.SHA256, sha256Bytes) || variant.Bytes <= 0 || variant.Bytes > maxInput {
			return nil, errors.New("invalid corpus variant")
		}
		totalBytes += variant.Bytes
		key := variant.Family + "/" + variant.Profile
		g := byGroup[key]
		if g == nil {
			g = &group{family: variant.Family, profile: variant.Profile}
			byGroup[key] = g
		}
		g.variants = append(g.variants, variant)
	}
	if totalBytes > maxCorpus {
		return nil, errors.New("corpus exceeds aggregate bound")
	}
	if len(byGroup) != maxFamilies*maxProfiles {
		return nil, errors.New("incomplete family/profile groups")
	}
	var groups []group
	for _, g := range byGroup {
		sort.Slice(g.variants, func(i, j int) bool { return g.variants[i].Level < g.variants[j].Level })
		if len(g.variants) != maxVariants {
			return nil, errors.New("incomplete tier set")
		}
		for i, variant := range g.variants {
			if variant.Level != levels[i] {
				return nil, errors.New("duplicate or unsupported tier")
			}
		}
		groups = append(groups, *g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].family != groups[j].family {
			return groups[i].family < groups[j].family
		}
		return groups[i].profile < groups[j].profile
	})
	return groups, nil
}

func readGroup(g group, arch string) ([][]byte, []string, error) {
	var inputs [][]byte
	var levels []string
	for _, v := range g.variants {
		data, err := readBounded(v.Path, maxInput)
		if err != nil {
			return nil, nil, err
		}
		if int64(len(data)) != v.Bytes || digest(data) != v.SHA256 {
			return nil, nil, fmt.Errorf("corpus identity changed: %s/%s/%s", g.family, g.profile, v.Level)
		}
		if err = validateELF(data, arch); err != nil {
			return nil, nil, err
		}
		inputs = append(inputs, data)
		levels = append(levels, v.Level)
	}
	return inputs, levels, nil
}

func validateELF(data []byte, arch string) error {
	const elfHeaderBytes = 64
	if len(data) < elfHeaderBytes {
		return errors.New("ELF header truncated")
	}
	if err := pack.PreflightELFHeaders(data); err != nil {
		return err
	}
	const elfMachineOffset = 18
	expected := elf.EM_X86_64
	if arch == "arm64" {
		expected = elf.EM_AARCH64
	}
	if data[elf.EI_DATA] != byte(elf.ELFDATA2LSB) || elf.Machine(binary.LittleEndian.Uint16(data[elfMachineOffset:])) != expected {
		return errors.New("ELF target mismatch")
	}
	return nil
}

func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode) // #nosec G304 -- owned output tree
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	return errors.Join(writeErr, f.Close())
}
