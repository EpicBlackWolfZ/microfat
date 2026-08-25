// Package main implements the minimal microfat launcher stub.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

// trimInPlace trims the fat binary in-place to contain only the selected variant + stub.
func trimInPlace(selfPath string, selfFile *os.File, totalSize int64, targetLevel string) error {
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
	tmpFile, err := os.CreateTemp(destDir, ".microfat-trim-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file in %s (check write permissions): %w", destDir, err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		// #nosec G703 -- cleanup temp file
		_ = os.Remove(tmpPath)
	}()

	if _, err := pack.TrimBinary(selfFile, totalSize, targetLevel, tmpFile); err != nil {
		return fmt.Errorf("trimming binary: %w", err)
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

// trimTo creates a new trimmed fat binary at the specified target destination path.
func trimTo(destPath string, selfFile *os.File, totalSize int64, targetLevel string) error {
	cleanDest := filepath.Clean(destPath)
	destDir := filepath.Dir(cleanDest)
	// #nosec G703 -- create destination directory
	if err := os.MkdirAll(destDir, defaultExecMode); err != nil {
		return fmt.Errorf("creating directory %s: %w", destDir, err)
	}

	tmpFile, err := os.CreateTemp(destDir, ".microfat-trim-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", destDir, err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		// #nosec G703 -- cleanup temp file
		_ = os.Remove(tmpPath)
	}()

	if _, err := pack.TrimBinary(selfFile, totalSize, targetLevel, tmpFile); err != nil {
		return fmt.Errorf("trimming binary: %w", err)
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

	// #nosec G703 -- move trimmed binary
	if err := os.Rename(tmpPath, cleanDest); err != nil {
		return fmt.Errorf("moving trimmed binary to %s: %w", cleanDest, err)
	}

	return nil
}
