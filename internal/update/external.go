package update

import (
	json "encoding/json/v2"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
)

const distributionFile = "microfat-distribution.json"
const distributionLimit = 4096

func externalManagement(physical string) (string, error) {
	name := filepath.Join(filepath.Dir(physical), distributionFile)
	before, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return unmanaged, nil
	}
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", errors.New("distribution ownership marker must be a regular file")
	}
	file, err := inputfile.Open(name)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(before, info) || info.Size() > distributionLimit || info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("unsafe distribution ownership marker")
	}
	data, err := io.ReadAll(io.LimitReader(file, distributionLimit+1))
	if err != nil {
		return "", err
	}
	if len(data) > distributionLimit {
		return "", errors.New("distribution ownership marker exceeds limit")
	}
	var marker struct {
		Schema int    `json:"schema"`
		Owner  string `json:"owner"`
	}
	if err := json.Unmarshal(data, &marker, json.RejectUnknownMembers(true)); err != nil {
		return "", err
	}
	if marker.Schema != 1 || marker.Owner != homebrew {
		return "", errors.New("unsupported distribution ownership marker")
	}
	return homebrew, nil
}

func externalGuidance(management string) string {
	if management == homebrew {
		return "Homebrew manages this installation; use brew upgrade microfat."
	}
	return "Self-update requires an installer-owned generation. For Homebrew use brew upgrade microfat; " +
		"otherwise use your package manager or the documented manual move-aside/reinstall procedure."
}
