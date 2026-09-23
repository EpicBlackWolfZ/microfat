//go:build !minimal

// Package main implements the minimal microfat launcher stub.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/lifecycle"
)

// optimizeInPlace extracts the selected variant over the current executable on disk.
func optimizeInPlace(
	selfPath string, selfFile *os.File, entry *format.VariantEntry, idx *format.Index, opts lifecycle.Options,
) error {
	if err := validateDeploymentPath(selfPath, selfFile); err != nil {
		return err
	}
	realPath, err := filepath.EvalSymlinks(selfPath)
	if err != nil {
		realPath = selfPath
	}
	if realPath != selfPath {
		fmt.Printf("[microfat] Notice: resolved symlink '%s' -> target '%s'\n", selfPath, realPath)
	}

	return lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  realPath,
		SrcFile:  selfFile,
		DestPath: realPath,
		Opts:     opts,
		Transform: func(staged *os.File) error {
			return extractVariantToWriter(selfFile, entry, idx, staged)
		},
	})
}

// optimizeTo extracts the selected variant directly to an explicit target path.
func optimizeTo(
	destPath string, selfFile *os.File, entry *format.VariantEntry, idx *format.Index, opts lifecycle.Options,
) error {
	return lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  selfFile.Name(),
		SrcFile:  selfFile,
		DestPath: destPath,
		Opts:     opts,
		Transform: func(staged *os.File) error {
			return extractVariantToWriter(selfFile, entry, idx, staged)
		},
	})
}
