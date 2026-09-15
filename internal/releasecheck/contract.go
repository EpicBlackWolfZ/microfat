package releasecheck

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// Project name and binary names
	ReleaseProjectName = "microfat"
	ReleaseFullStub    = "microfat-stub"
	ReleaseMinStub     = "microfat-stub-minimal"

	// Architectures
	ArchAMD64 = "amd64"
	ArchARM64 = "arm64"

	minArchiveParts = 4
)

// GoReleaserMetadata represents dist/metadata.json schema emitted by GoReleaser v2.
type GoReleaserMetadata struct {
	ProjectName string `json:"project_name"`
	Version     string `json:"version"`
	Tag         string `json:"tag"`
	Commit      string `json:"commit"`
}

// ReleaseContract defines the explicit expectations for microfat release artifacts.
type ReleaseContract struct {
	Version              string
	ExpectedArchives     map[string]string   // arch -> archive filename
	ExpectedPayloadNames map[string]bool     // all 6 expected product payload names
	RequiredExecutables  []string            // root executables in each archive
	ExpectedTiers        map[string][]string // arch -> list of variant levels
}

// NewReleaseContract creates a new contract for the given version.
func NewReleaseContract(version string) (*ReleaseContract, error) {
	v := strings.TrimSpace(version)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return nil, fmt.Errorf("version cannot be empty")
	}

	amd64Archive := fmt.Sprintf("microfat_%s_linux_%s.tar.gz", v, ArchAMD64)
	arm64Archive := fmt.Sprintf("microfat_%s_linux_%s.tar.gz", v, ArchARM64)

	payloads := map[string]bool{
		amd64Archive:                    true,
		arm64Archive:                    true,
		amd64Archive + ".spdx.json":     true,
		amd64Archive + ".cyclonedx.json": true,
		arm64Archive + ".spdx.json":     true,
		arm64Archive + ".cyclonedx.json": true,
	}

	return &ReleaseContract{
		Version: v,
		ExpectedArchives: map[string]string{
			ArchAMD64: amd64Archive,
			ArchARM64: arm64Archive,
		},
		ExpectedPayloadNames: payloads,
		RequiredExecutables: []string{
			ReleaseProjectName,
			ReleaseFullStub,
			ReleaseMinStub,
		},
		ExpectedTiers: map[string][]string{
			ArchAMD64: {"v1", "v2", "v3", "v4"},
			ArchARM64: {"v8.0", "v8.2", "v9.0"},
		},
	}, nil
}

func deriveVersionFromExplicitTag(explicitTag string) (string, error) {
	v := strings.TrimSpace(explicitTag)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return "", fmt.Errorf("explicit tag is empty")
	}
	return v, nil
}

func deriveVersionFromMetadata(distDir string) (string, error) {
	metaPath := filepath.Join(distDir, "metadata.json")
	// #nosec G304 -- metaPath within checked dist directory
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return "", err
	}
	var meta GoReleaserMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", err
	}
	if meta.ProjectName != "" && meta.ProjectName != ReleaseProjectName {
		return "", fmt.Errorf("unexpected project name in metadata.json: %s", meta.ProjectName)
	}
	v := strings.TrimSpace(meta.Version)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return "", fmt.Errorf("empty version in metadata.json")
	}
	return v, nil
}

func deriveVersionFromArtifacts(distDir string) (string, error) {
	artPath := filepath.Join(distDir, "artifacts.json")
	// #nosec G304 -- artPath within checked dist directory
	data, err := os.ReadFile(artPath)
	if err != nil {
		return "", err
	}
	var artifacts []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &artifacts); err != nil {
		return "", err
	}
	for _, a := range artifacts {
		if a.Type == "Archive" && strings.HasPrefix(a.Name, "microfat_") && strings.HasSuffix(a.Name, ".tar.gz") {
			parts := strings.Split(a.Name, "_")
			if len(parts) >= minArchiveParts {
				v := strings.TrimPrefix(parts[1], "v")
				if v != "" {
					return v, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no valid archive found in artifacts.json")
}

func deriveVersionFromArchives(distDir string) (string, error) {
	amd64Matches, _ := filepath.Glob(filepath.Join(distDir, "microfat_*_linux_amd64.tar.gz"))
	arm64Matches, _ := filepath.Glob(filepath.Join(distDir, "microfat_*_linux_arm64.tar.gz"))
	if len(amd64Matches) != 1 || len(arm64Matches) != 1 {
		return "", fmt.Errorf("expected 1 amd64 and 1 arm64 archive in %s", distDir)
	}
	partsAmd64 := strings.Split(filepath.Base(amd64Matches[0]), "_")
	partsArm64 := strings.Split(filepath.Base(arm64Matches[0]), "_")
	if len(partsAmd64) < minArchiveParts || len(partsArm64) < minArchiveParts {
		return "", fmt.Errorf("invalid archive naming format")
	}
	vAmd64 := strings.TrimPrefix(partsAmd64[1], "v")
	vArm64 := strings.TrimPrefix(partsArm64[1], "v")
	if vAmd64 != vArm64 || vAmd64 == "" {
		return "", fmt.Errorf("architecture archives have mismatched versions: %s vs %s", vAmd64, vArm64)
	}
	return vAmd64, nil
}

// DeriveVersion determines the release version from an explicit tag, GoReleaser metadata, or matching archives.
func DeriveVersion(distDir string, explicitTag string) (string, error) {
	if explicitTag != "" {
		return deriveVersionFromExplicitTag(explicitTag)
	}
	if v, err := deriveVersionFromMetadata(distDir); err == nil {
		return v, nil
	} else if strings.Contains(err.Error(), "unexpected project name") {
		return "", err
	}
	if v, err := deriveVersionFromArtifacts(distDir); err == nil {
		return v, nil
	}
	if v, err := deriveVersionFromArchives(distDir); err == nil {
		return v, nil
	} else if strings.Contains(err.Error(), "mismatched versions") {
		return "", err
	}
	return "", fmt.Errorf("unable to derive release version from tag, metadata, or archives in %s", distDir)
}
