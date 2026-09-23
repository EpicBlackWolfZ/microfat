//go:build !minimal

// Package main implements the minimal microfat launcher stub.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/internal/lifecycle"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

// trimInPlace trims the fat binary in-place to contain only the selected variant + stub.
func trimInPlace(selfPath string, selfFile *os.File, totalSize int64, targetLevel string, opts lifecycle.Options) error {
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
			_, trimErr := pack.TrimBinary(selfFile, totalSize, targetLevel, staged)
			return trimErr
		},
	})
}

// trimTo creates a new trimmed fat binary at the specified target destination path.
func trimTo(destPath string, selfFile *os.File, totalSize int64, targetLevel string, opts lifecycle.Options) error {
	return lifecycle.Execute(lifecycle.Transaction{
		SrcPath:  selfFile.Name(),
		SrcFile:  selfFile,
		DestPath: destPath,
		Opts:     opts,
		Transform: func(staged *os.File) error {
			_, trimErr := pack.TrimBinary(selfFile, totalSize, targetLevel, staged)
			return trimErr
		},
	})
}
