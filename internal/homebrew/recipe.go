// Package homebrew prepares byte-preserving casks from authenticated official releases.
package homebrew

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"text/template"

	"github.com/EpicBlackWolfZ/microfat"
	"github.com/EpicBlackWolfZ/microfat/internal/installrelease"
)

const Repository = "EpicBlackWolfZ/microfat"
const TapRepository = "EpicBlackWolfZ/homebrew-tap"
const CaskPath = "Casks/microfat.rb"
const firstVersion = "0.3.0"

// BrewVersion identifies the reviewed Homebrew release used by qualification.
func BrewVersion() string {
	version, _, _ := strings.Cut(microfat.ToolPin("HOMEBREW_SOURCE"), "@")
	return strings.TrimPrefix(version, "v")
}

// BrewCommit identifies the immutable upstream source used by qualification.
func BrewCommit() string {
	_, commit, _ := strings.Cut(microfat.ToolPin("HOMEBREW_SOURCE"), "@")
	return commit
}

//go:embed cask.rb.tmpl
var caskTemplate string

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var sourcePattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var recipeVersion = regexp.MustCompile(`(?m)^  version "([0-9]+\.[0-9]+\.[0-9]+)"$`)

type Recipe struct {
	Version string
	AMD64   string
	ARM64   string
}

func Version(tag string) (string, error) {
	version, err := installrelease.ParseVersion(tag)
	if err != nil {
		return "", err
	}
	if tag != "v"+version || installrelease.CompareVersions(version, firstVersion) < 0 {
		return "", errors.New("Homebrew requires an exact stable tag v0.3.0 or newer")
	}
	return version, nil
}

// Render contains no acquisition or execution. Only Prepare authenticates its inputs.
func Render(recipe Recipe) ([]byte, error) {
	if _, err := Version("v" + recipe.Version); err != nil {
		return nil, err
	}
	if !digestPattern.MatchString(recipe.AMD64) || !digestPattern.MatchString(recipe.ARM64) {
		return nil, errors.New("both Linux archive SHA-256 values are required")
	}
	var out bytes.Buffer
	err := template.Must(template.New("cask").Parse(caskTemplate)).Execute(&out, recipe)
	return out.Bytes(), err
}

func CaskVersion(data []byte) (string, error) {
	matches := recipeVersion.FindAllSubmatch(data, -1)
	if len(matches) != 1 {
		return "", errors.New("expected exactly one fixed microfat cask version")
	}
	return Version("v" + string(matches[0][1]))
}

// UpdateNeeded rejects downgrade and same-version rewrites, including human changes.
func UpdateNeeded(current, candidate []byte) (bool, error) {
	to, err := CaskVersion(candidate)
	if err != nil {
		return false, err
	}
	if len(current) == 0 {
		return true, nil
	}
	from, err := CaskVersion(current)
	if err != nil {
		return false, err
	}
	switch order := installrelease.CompareVersions(to, from); {
	case order < 0:
		return false, errors.New("refusing cask downgrade")
	case order == 0:
		if !bytes.Equal(current, candidate) {
			return false, errors.New("same-version cask differs; a reviewed repair is required")
		}
		return false, nil
	default:
		return true, nil
	}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func archiveName(version, arch string) string {
	return "microfat_" + strings.TrimPrefix(version, "v") + "_linux_" + arch + ".tar.gz"
}
