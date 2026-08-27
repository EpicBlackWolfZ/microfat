// Package main provides the microfat launcher stub.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

var (
	exitFunc                  = os.Exit
	getSelfExecutablePathFunc = os.Executable
)

func main() {
	if err := run(); err != nil {
		hint := format.DiagnoseError(format.StageLauncherMain, err)
		if strings.EqualFold(os.Getenv(format.EnvLog), "json") {
			e := format.ErrorTelemetry{
				Event:             format.EventError,
				TimestampUnixNano: time.Now().UnixNano(),
				Stage:             format.StageLauncherMain,
				Error:             err.Error(),
				Hint:              hint,
			}
			fmt.Fprintf(os.Stderr, "[microfat] %s\n", formatErrorTelemetryJSON(e))
		}
		fmt.Fprintf(os.Stderr, "[microfat] error: %v\n", err)
		if hint != "" {
			fmt.Fprintf(os.Stderr, "[microfat:hint] %s\n", hint)
		}
		exitFunc(1)
	}
}

func run() error {
	selfPath, err := getSelfExecutablePathFunc()
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}
	return runBinary(selfPath)
}

func runBinary(selfPath string) error {
	launcherStart := time.Now()

	// #nosec G304 -- launcher opens its own binary image to read payload index
	selfFile, err := os.Open(filepath.Clean(selfPath))
	if err != nil {
		return fmt.Errorf("opening executable %s: %w", selfPath, err)
	}
	defer func() { _ = selfFile.Close() }()

	stat, err := selfFile.Stat()
	if err != nil {
		return fmt.Errorf("stat executable: %w", err)
	}

	if !format.IsFatBinary(selfFile, stat.Size()) {
		return fmt.Errorf("file %s is not a valid microfat fat binary (missing magic trailer)", selfPath)
	}

	idx, err := format.ReadTrailerAndIndex(selfFile, stat.Size())
	if err != nil {
		return fmt.Errorf("reading binary manifest: %w", err)
	}

	hostInfo := microarch.Detect()
	policy := microarch.ReadPolicyFromEnv()
	policyRes, err := microarch.SelectVariantWithPolicy(idx.TargetArch, hostInfo.Level, idx.VariantLevels(), policy)
	if err != nil {
		return fmt.Errorf("selecting compatible CPU microarchitecture variant: %w", err)
	}

	selectedLevel := policyRes.SelectedVariant
	selectedEntry, found := idx.FindVariant(selectedLevel)
	if !found {
		return fmt.Errorf("internal error: matched variant %s not present in index", selectedLevel)
	}

	// Check for meta-commands
	if len(os.Args) > 1 {
		handled, err := handleMetaCommand(
			os.Args[1], selfPath, selfFile, stat.Size(), idx, hostInfo, selectedEntry, policyRes,
		)
		if handled {
			return err
		}
	}

	// Standard transparent execution
	return executeVariant(selfFile, selectedEntry, idx, os.Args, os.Environ(), hostInfo, policyRes, launcherStart)
}
