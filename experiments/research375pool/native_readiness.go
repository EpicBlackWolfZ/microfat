package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
)

const (
	nativeDBDirectory = "govulncheck-db"
	nativeDBMetadata  = "{\"modified\":\"2026-01-01T00:00:00Z\"}\n"
	nativeDBModules   = "[]\n"
	nativeDBFileMode  = 0o400
)

type nativeReadinessFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type nativeReadiness struct {
	Args        []string              `json:"effective_args"`
	GoVersion   string                `json:"fixed_goversion"`
	DatabaseDir string                `json:"offline_database_dir,omitempty"`
	Files       []nativeReadinessFile `json:"offline_database_files,omitempty"`
}

// The immutable local database is a version-command fixture only. No scan is
// performed and it must never be treated as a usable vulnerability database.
func expectedNativeReadiness(family, databaseDir string) (nativeReadiness, error) {
	args, err := nativeFixtureArgs(family)
	if err != nil {
		return nativeReadiness{}, err
	}
	r := nativeReadiness{Args: args, GoVersion: researchGoVersion}
	if family != familyGovulncheck {
		return r, nil
	}
	if !filepath.IsAbs(databaseDir) {
		return nativeReadiness{}, errors.New("offline version database requires an absolute owned directory")
	}
	r.DatabaseDir = databaseDir
	databaseURL := (&url.URL{Scheme: "file", Path: databaseDir}).String()
	r.Args = append(r.Args, "-db", databaseURL)
	for i, name := range []string{"db.json", "modules.json"} {
		data := []string{nativeDBMetadata, nativeDBModules}[i]
		sum := sha256.Sum256([]byte(data))
		r.Files = append(r.Files, nativeReadinessFile{Path: filepath.Join(databaseDir, "index", name),
			SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data))})
	}
	return r, nil
}

func prepareNativeReadiness(family, ownedRoot string) (nativeReadiness, error) {
	r, err := expectedNativeReadiness(family, filepath.Join(ownedRoot, nativeDBDirectory))
	if err != nil {
		return r, err
	}
	if r.DatabaseDir == "" {
		return r, nil
	}
	if err := os.MkdirAll(filepath.Join(r.DatabaseDir, "index"), privateDirMode); err != nil {
		return r, err
	}
	for i, file := range r.Files {
		data := []string{nativeDBMetadata, nativeDBModules}[i]
		if err := os.WriteFile(file.Path, []byte(data), nativeDBFileMode); err != nil {
			return r, err
		}
	}
	return r, nil
}

func recordedNativeReadinessValid(family string, r nativeReadiness) bool {
	expected, err := expectedNativeReadiness(family, r.DatabaseDir)
	return err == nil && r.GoVersion == expected.GoVersion && r.DatabaseDir == expected.DatabaseDir &&
		slices.Equal(r.Args, expected.Args) && slices.Equal(r.Files, expected.Files)
}

func runNativeReadiness(
	ctx context.Context, executable string, r nativeReadiness, env []string, run nativeLaunchRunner,
) (string, string, error) {
	for _, file := range r.Files {
		hash, size, err := digestFile(file.Path, nativeLaunchLimit)
		if err != nil {
			return "", "", fmt.Errorf("offline version fixture: %w", err)
		}
		if hash != file.SHA256 || size != file.Bytes {
			return "", "", errors.New("offline version fixture changed before native correctness launch")
		}
	}
	return run(ctx, executable, r.Args, env)
}
