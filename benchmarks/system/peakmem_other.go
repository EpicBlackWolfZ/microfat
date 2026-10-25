//go:build !linux

package system

import (
	"context"
	"errors"
)

// RunIsolated requires Linux static ELF trampoline execution.
func RunIsolated(_ context.Context, _ string, _ IsolatedSpec) (IsolatedResult, error) {
	return IsolatedResult{}, errors.New("isolated child peak-memory execution requires Linux")
}
