//go:build !minimal

// Package main implements the minimal microfat launcher stub.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	defaultExecMode = 0o755
)

// optimizeInPlace extracts the selected variant over the current executable on disk.
func optimizeInPlace(selfPath string, selfFile *os.File, entry *format.VariantEntry) error {
	realPath, err := filepath.EvalSymlinks(selfPath)
	if err != nil {
		realPath = selfPath
	}
	if realPath != selfPath {
		fmt.Printf("[microfat] Notice: resolved symlink '%s' -> target '%s'\n", selfPath, realPath)
	}

	targetMode := os.FileMode(defaultExecMode)
	if stat, err := selfFile.Stat(); err == nil {
		targetMode = stat.Mode()
	}

	destDir := filepath.Dir(realPath)
	tmpFile, err := os.CreateTemp(destDir, ".microfat-opt-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file in %s (check write permissions): %w", destDir, err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		// #nosec G703 -- cleanup temp file
		_ = os.Remove(tmpPath)
	}()

	if err := extractVariantToWriter(selfFile, entry, tmpFile); err != nil {
		return fmt.Errorf("extracting variant: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("syncing file: %w", err)
	}
	if err := tmpFile.Chmod(targetMode); err != nil {
		return fmt.Errorf("chmodding file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	// #nosec G703 -- atomic replace of binary
	if err := os.Rename(tmpPath, realPath); err != nil {
		return fmt.Errorf("replacing binary %s: %w", realPath, err)
	}

	return nil
}

// optimizeTo extracts the selected variant directly to an explicit target path.
func optimizeTo(destPath string, selfFile *os.File, entry *format.VariantEntry) error {
	cleanDest := filepath.Clean(destPath)
	destDir := filepath.Dir(cleanDest)
	// #nosec G703 -- create destination directory
	if err := os.MkdirAll(destDir, defaultExecMode); err != nil {
		return fmt.Errorf("creating directory %s: %w", destDir, err)
	}

	tmpFile, err := os.CreateTemp(destDir, ".microfat-opt-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", destDir, err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		// #nosec G703 -- cleanup temp file
		_ = os.Remove(tmpPath)
	}()

	if err := extractVariantToWriter(selfFile, entry, tmpFile); err != nil {
		return fmt.Errorf("extracting variant: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("syncing file: %w", err)
	}
	if err := tmpFile.Chmod(defaultExecMode); err != nil {
		return fmt.Errorf("chmodding file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	// #nosec G703 -- move extracted binary
	if err := os.Rename(tmpPath, cleanDest); err != nil {
		return fmt.Errorf("moving extracted binary to %s: %w", cleanDest, err)
	}

	return nil
}
