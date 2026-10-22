// Package benchmarks identifies the external tools pinned for benchmark evidence.
package benchmarks

import (
	_ "embed"
	"encoding/json"
	"errors"
	"regexp"
)

//go:embed tools.lock.json
var toolsLock []byte

var (
	stableToolVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	sourceCommit      = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// FortioPin records the exact source used to build the external load generator.
// Go's checksum database authenticates the downloaded module content separately.
type FortioPin struct {
	Module       string `json:"module"`
	Version      string `json:"version"`
	SourceCommit string `json:"source_commit"`
	Source       string `json:"source"`
}

// ReadFortioPin validates the release and immutable source identity in a tool lock.
func ReadFortioPin(data []byte) (FortioPin, error) {
	var lock struct {
		Fortio FortioPin `json:"fortio"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return FortioPin{}, err
	}
	pin := lock.Fortio
	if pin.Module != "fortio.org/fortio" || !stableToolVersion.MatchString(pin.Version) ||
		!sourceCommit.MatchString(pin.SourceCommit) || pin.Source != "https://github.com/fortio/fortio/tree/"+pin.Version {
		return FortioPin{}, errors.New("incomplete or unsupported Fortio lock")
	}
	return pin, nil
}

// PinnedFortio returns the checked-in identity embedded in this build.
// A malformed repository lock is a build configuration error and fails closed.
func PinnedFortio() FortioPin {
	return mustFortioPin(toolsLock)
}

func mustFortioPin(data []byte) FortioPin {
	pin, err := ReadFortioPin(data)
	if err != nil {
		panic(err)
	}
	return pin
}
