//go:build minimal

// Package main implements minimal launcher stub meta-command routing.
package main

import (
	"errors"
	"os"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

func handleMetaCommand(
	arg1 string,
	selfPath string,
	selfFile *os.File,
	statSize int64,
	idx *format.Index,
	hostInfo microarch.Info,
	selectedEntry *format.VariantEntry,
	policyRes microarch.PolicyResult,
) (bool, error) {
	if strings.HasPrefix(arg1, "--microfat:") {
		return true, errors.New("meta-commands are disabled in minimal launcher stub profile")
	}
	return false, nil
}
