package releasecheck

import (
	"debug/buildinfo"
	"fmt"
	"maps"
)

// ModuleDep represents a linked Go dependency module and any replacement.
type ModuleDep struct {
	Path        string `json:"path"`
	Version     string `json:"version"`
	Sum         string `json:"sum,omitempty"`
	ReplacePath string `json:"replace_path,omitempty"`
	ReplaceVer  string `json:"replace_ver,omitempty"`
	ReplaceSum  string `json:"replace_sum,omitempty"`
}

// BinaryInventory contains the linked module inventory extracted directly from a binary's buildinfo.
type BinaryInventory struct {
	Identifier   string               `json:"identifier"`
	BinaryName   string               `json:"binary_name"`
	VariantTier  string               `json:"variant_tier,omitempty"`
	MainPath     string               `json:"main_path"`
	MainModule   string               `json:"main_module"`
	MainVersion  string               `json:"main_version"`
	GoVersion    string               `json:"go_version"`
	Settings     map[string]string    `json:"settings,omitempty"`
	Dependencies map[string]ModuleDep `json:"dependencies"`
}

// ArchiveInventory represents the combined independent inventory extracted from all binaries in an archive.
type ArchiveInventory struct {
	ArchiveName     string                      `json:"archive_name"`
	TargetArch      string                      `json:"target_arch"`
	Binaries        map[string]*BinaryInventory `json:"binaries"`
	AllDependencies map[string]ModuleDep        `json:"all_dependencies"`
}

func extractBinaryInventory(identifier, binName, tier string, bi *buildinfo.BuildInfo) *BinaryInventory {
	binInv := &BinaryInventory{
		Identifier:   identifier,
		BinaryName:   binName,
		VariantTier:  tier,
		MainPath:     bi.Path,
		MainModule:   bi.Main.Path,
		MainVersion:  bi.Main.Version,
		GoVersion:    bi.GoVersion,
		Settings:     make(map[string]string),
		Dependencies: make(map[string]ModuleDep),
	}

	for _, s := range bi.Settings {
		binInv.Settings[s.Key] = s.Value
	}

	for _, d := range bi.Deps {
		if d == nil {
			continue
		}
		dep := ModuleDep{
			Path:    d.Path,
			Version: d.Version,
			Sum:     d.Sum,
		}
		if d.Replace != nil {
			dep.ReplacePath = d.Replace.Path
			dep.ReplaceVer = d.Replace.Version
			dep.ReplaceSum = d.Replace.Sum
		}
		binInv.Dependencies[d.Path] = dep
	}

	return binInv
}

// ExtractArchiveInventory extracts linked module and toolchain inventory directly from binary buildinfo.
func ExtractArchiveInventory(facts *ArchiveFacts) (*ArchiveInventory, error) {
	if facts == nil {
		return nil, fmt.Errorf("archive facts cannot be nil")
	}

	inv := &ArchiveInventory{
		ArchiveName:     facts.ArchiveName,
		TargetArch:      facts.TargetArch,
		Binaries:        make(map[string]*BinaryInventory),
		AllDependencies: make(map[string]ModuleDep),
	}

	// 1. Process root executables
	for exeName, exe := range facts.Executables {
		if exe == nil || exe.BuildInfo == nil {
			return nil, fmt.Errorf("missing buildinfo for root executable %s", exeName)
		}
		binInv := extractBinaryInventory(exeName, exeName, "", exe.BuildInfo)
		inv.Binaries[exeName] = binInv
		maps.Copy(inv.AllDependencies, binInv.Dependencies)
	}

	// 2. Process embedded variants
	for tier, vf := range facts.EmbeddedVariants {
		if vf == nil || vf.BuildInfo == nil {
			return nil, fmt.Errorf("missing buildinfo for embedded variant %s", tier)
		}
		id := "variant:" + tier
		binInv := extractBinaryInventory(id, ReleaseProjectName, tier, vf.BuildInfo)
		inv.Binaries[id] = binInv
		maps.Copy(inv.AllDependencies, binInv.Dependencies)
	}

	return inv, nil
}
