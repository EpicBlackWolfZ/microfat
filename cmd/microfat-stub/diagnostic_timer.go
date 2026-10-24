package main

import (
	"os"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

// diagnosticsEnabled preserves the dispatch logger's environment semantics,
// including text diagnostics for nonempty, unrecognized log/debug values.
func diagnosticsEnabled() bool {
	return diagnosticOptionsEnabled(os.Getenv(format.EnvDebug), os.Getenv(format.EnvLog))
}

func diagnosticOptionsEnabled(debugOpt, logOpt string) bool {
	return logOpt != "" || (debugOpt != "" && debugOpt != "0" && !strings.EqualFold(debugOpt, "false"))
}

// A zero start disables timing without consulting a clock. Keep the decision
// local to a launch rather than caching process environment in package state.
func startDiagnosticTimer(enabled bool) time.Time {
	if !enabled {
		return time.Time{}
	}
	return time.Now()
}

func diagnosticElapsed(start time.Time) time.Duration {
	if start.IsZero() {
		return 0
	}
	return time.Since(start)
}
