//go:build !linux

package system

import (
	"context"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
)

func TraceExec(_ context.Context, _ process.Spec) ExecDiagnostic {
	return ExecDiagnostic{Status: "unavailable", Reason: "exec-boundary tracing requires Linux"}
}
