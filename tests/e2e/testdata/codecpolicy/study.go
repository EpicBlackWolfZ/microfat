//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

const (
	originalStudy        = "original"
	isolatedStudy        = "isolated"
	offMode              = "off 1970-01-01\n"
	configDirectoryDepth = 3
)

// A study context is constructed once before preparation, then passed unchanged.
type studyConfig struct {
	Name, Protocol                string
	Isolated                      bool
	ConfigDir, BootstrapConfigDir string
}

func selectStudy(name string) (studyConfig, error) {
	switch name {
	case originalStudy:
		return studyConfig{Name: originalStudy, Protocol: protocol}, nil
	case isolatedStudy:
		return studyConfig{Name: isolatedStudy, Protocol: "docs/codec-policy-isolated.md", Isolated: true}, nil
	default:
		return studyConfig{}, fmt.Errorf("unknown study %q: use original or isolated", name)
	}
}

func prepareStudy(study studyConfig, output string) (studyConfig, error) {
	if study.Isolated {
		study.ConfigDir, study.BootstrapConfigDir = filepath.Join(output, "config"), output+".bootstrap-config"
		if err := validateOffMode(modePath(study.BootstrapConfigDir)); err != nil {
			return study, fmt.Errorf("isolated study requires retained bootstrap config from collector wrapper: %w", err)
		}
		path := modePath(study.ConfigDir)
		if err := os.MkdirAll(filepath.Dir(path), privateDir); err != nil {
			return study, err
		}
		// #nosec G703 -- fixed filename below the new private evidence directory.
		if err := os.WriteFile(path, []byte(offMode), privateFile); err != nil {
			return study, err
		}
		if err := study.validateConfig(); err != nil {
			return study, err
		}
	}
	return study, saveJSON(filepath.Join(output, "study.json"), study)
}

func modePath(configDir string) string { return filepath.Join(configDir, "go", "telemetry", "mode") }

func (study studyConfig) frozenFiles() []string {
	if !study.Isolated {
		return nil
	}
	return []string{modePath(study.ConfigDir), modePath(study.BootstrapConfigDir)}
}

func (study studyConfig) validateConfig() error {
	if !study.Isolated {
		return nil
	}
	if !filepath.IsAbs(study.ConfigDir) || !filepath.IsAbs(study.BootstrapConfigDir) {
		return errors.New("isolated config paths must be absolute")
	}
	for _, path := range study.frozenFiles() {
		dir := filepath.Dir(path)
		for range configDirectoryDepth {
			info, err := os.Lstat(dir)
			if err != nil {
				return err
			}
			if !info.IsDir() || info.Mode().Perm() != privateDir || !ownedFile(info) {
				return errors.New("isolated configuration directories must be owned regular mode0700 directories")
			}
			dir = filepath.Dir(dir)
		}
		if err := validateOffMode(path); err != nil {
			return err
		}
	}
	return nil
}

func validateOffMode(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != privateFile || !ownedFile(info) {
		return errors.New("telemetry mode must be regular mode0600")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- explicit private study configuration file, never the user's telemetry settings.
	if err != nil {
		return err
	}
	if string(data) != offMode {
		return errors.New("isolated telemetry mode differs from fixed off value")
	}
	return nil
}

func ownedFile(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(stat.Uid) == int64(os.Geteuid())
}

func (study studyConfig) env(extra ...string) []string {
	env := cleanEnv(extra...)
	if !study.Isolated {
		return env
	}
	env = slices.DeleteFunc(env, func(item string) bool { key, _, _ := strings.Cut(item, "="); return key == "XDG_CONFIG_HOME" })
	return append(env, "XDG_CONFIG_HOME="+study.ConfigDir)
}
