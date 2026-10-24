//go:build linux

package main

import (
	"os"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

type diagnosticMode uint8

const (
	diagnosticOff diagnosticMode = iota
	diagnosticText
	diagnosticJSON
)

// launchDiagnostics captures dispatch logging once per launch. Error diagnostics
// retain their separate environment and hint behavior.
type launchDiagnostics struct {
	mode  diagnosticMode
	start time.Time
}

func readDiagnosticMode() diagnosticMode {
	debugOpt := os.Getenv(format.EnvDebug)
	logOpt := os.Getenv(format.EnvLog)
	if strings.EqualFold(logOpt, "json") {
		return diagnosticJSON
	}
	if logOpt != "" || (debugOpt != "" && debugOpt != "0" && !strings.EqualFold(debugOpt, "false")) {
		return diagnosticText
	}
	return diagnosticOff
}

func newLaunchDiagnostics() launchDiagnostics {
	diagnostics := launchDiagnostics{mode: readDiagnosticMode()}
	diagnostics.start = diagnostics.startTimer()
	return diagnostics
}

func (d launchDiagnostics) startTimer() time.Time {
	if d.mode == diagnosticOff {
		return time.Time{}
	}
	return time.Now()
}

func (d launchDiagnostics) elapsed(start time.Time) time.Duration {
	if d.mode == diagnosticOff || start.IsZero() {
		return 0
	}
	return time.Since(start)
}

func (d launchDiagnostics) totalElapsed() time.Duration {
	return d.elapsed(d.start)
}
